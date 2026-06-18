package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/drewlesueur/tts-inventory-getter/internal/api"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/discovery"
	"github.com/drewlesueur/tts-inventory-getter/internal/inventoryapi"
	"github.com/drewlesueur/tts-inventory-getter/internal/metrics"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
	"github.com/drewlesueur/tts-inventory-getter/internal/sites"
	"github.com/drewlesueur/tts-inventory-getter/internal/store"
)

func main() {
	bootLogger, _ := zap.NewProduction()
	cfg, err := config.Load()
	if err != nil {
		bootLogger.Fatal("config load failed", zap.Error(err))
	}
	logger, errLogClose, err := newLogger(cfg.ErrorLogPath)
	if err != nil {
		bootLogger.Fatal("logger init failed", zap.Error(err))
	}
	_ = bootLogger.Sync()
	defer logger.Sync()
	defer errLogClose()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resultStore, err := store.NewSQLiteResultStore(cfg.SQLitePath)
	if err != nil {
		logger.Fatal("sqlite open failed", zap.Error(err))
	}
	defer resultStore.Close()
	httpFetcher := scrape.NewHTTPFetcherWithTimeout(time.Duration(cfg.HTTPFetchTimeoutSec) * time.Second)
	rodBrowser, cancelRod := scrape.NewRodBrowser(cfg.Headless)
	defer cancelRod()
	playwrightBrowser := scrape.NewPlaywrightBrowser(cfg.PlaywrightCommand)

	imageSizes := scrape.NewImageSizeCache()
	scraper := scrape.Service{
		Browser:       playwrightBrowser,
		AltBrowser:    rodBrowser,
		Fetcher:       httpFetcher,
		DetailFetcher: scrape.HTMLDetailFetcher{Fetcher: httpFetcher, Browser: rodBrowser, ImageSizes: imageSizes},
		Extractors:    []scrape.Extractor{scrape.LoopHTMLExtractor{}, scrape.DOMExtractor{}, scrape.NextDataExtractor{}, scrape.RegexExtractor{}},
		Concurrency:   cfg.Concurrency,
		AIEnricher:    &scrape.AIEnricher{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIModel},
		Logger:        logger,
	}

	m := metrics.New()
	var discoverClient *discovery.Client
	if cfg.EnableCodexDiscovery {
		discoverClient = &discovery.Client{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIModel}
	}
	siteLoader := config.NewLoader(cfg.SiteConfigsDir)
	if n, werr := siteLoader.WarmCache(); werr != nil {
		logger.Warn("site config warmup failed", zap.Error(werr))
	} else if n > 0 {
		logger.Info("site config cache warmed", zap.Int("count", n))
	}
	invClient := &inventoryapi.Client{BaseURL: cfg.InventoryAPIBaseURL, ServiceKey: cfg.ServiceKey}
	s := api.NewServer(cfg, logger, scraper, siteLoader, resultStore, m, discoverClient, invClient)
	s.SetDailyUpsertJob(func() {
		runDailyUpsert(logger, cfg, scraper, siteLoader, discoverClient, resultStore, invClient)
	})
	router := s.Router()
	httpServer := &http.Server{Addr: ":" + cfg.Port, Handler: router}

	var dailyCronRunner, weeklyCronRunner, idempotencyCronRunner *scrape.CronRunner
	if cfg.EnableDailyUpsertCron {
		dailyCronRunner, err = scrape.StartCron(logger, cfg.DailyUpsertCronSpec, func() {
			runDailyUpsert(logger, cfg, scraper, siteLoader, discoverClient, resultStore, invClient)
		})
		if err != nil {
			logger.Fatal("daily upsert cron start failed", zap.Error(err))
		}
	}
	if cfg.EnableWeeklyUpsertCron {
		weeklyCronRunner, err = scrape.StartCron(logger, cfg.WeeklyUpsertCronSpec, func() {
			runWeeklyUpsert(logger, cfg, scraper, siteLoader, discoverClient, resultStore, invClient)
		})
		if err != nil {
			logger.Fatal("weekly upsert cron start failed", zap.Error(err))
		}
	}
	if cfg.EnableIdempotencyClearCron {
		idempotencyCronRunner, err = scrape.StartCron(logger, cfg.IdempotencyClearCronSpec, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := resultStore.ClearIdempotency(ctx); err != nil {
				logger.Error("idempotency clear failed", zap.Error(err))
				return
			}
			logger.Info("idempotency mapping cleared")
		})
		if err != nil {
			logger.Fatal("idempotency cron start failed", zap.Error(err))
		}
	}

	go func() {
		logger.Info("server starting", zap.String("port", cfg.Port))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server exited unexpectedly", zap.Error(err))
		}
	}()

	<-ctx.Done()
	if dailyCronRunner != nil {
		dailyCronRunner.Stop(context.Background())
	}
	if weeklyCronRunner != nil {
		weeklyCronRunner.Stop(context.Background())
	}
	if idempotencyCronRunner != nil {
		idempotencyCronRunner.Stop(context.Background())
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	logger.Info("server stopped gracefully")
}

type scrapedPage struct {
	page  inventoryapi.PageEntry
	items []model.InventoryItem
}

func scrapeAllPages(logger *zap.Logger, cfg config.Config, scraper scrape.Service, siteLoader config.Loader, discoverClient *discovery.Client, resultStore store.ResultStore, invClient *inventoryapi.Client, jobName, scheduleType string) []scrapedPage {
	listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer listCancel()
	pages, err := invClient.ListPages(listCtx)
	if err != nil {
		logger.Error("inventory api list failed", zap.String("job", jobName), zap.Error(err))
		return nil
	}
	eligible := make([]inventoryapi.PageEntry, 0, len(pages))
	for _, p := range pages {
		if pageMatchesSchedule(p, scheduleType) {
			eligible = append(eligible, p)
		}
	}
	logger.Info("scrape job starting", zap.String("job", jobName), zap.String("schedule", scheduleType), zap.Int("pages", len(eligible)))

	out := make([]scrapedPage, 0, len(eligible))
	for _, p := range eligible {
		if p.FTPSyncEnabled() {
			syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := invClient.SyncAccountInventorySources(syncCtx, p.AccountID); err != nil {
				logger.Error("inventory source sync failed", zap.String("job", jobName), zap.String("accountID", p.AccountID), zap.Error(err))
			} else {
				logger.Info("inventory source synced", zap.String("job", jobName), zap.String("accountID", p.AccountID))
			}
			syncCancel()
		}
		if !p.ScrapeSyncEnabled() {
			continue
		}
		if p.DealershipID == "" || p.URL == "" {
			logger.Warn("skipping invalid page entry", zap.String("job", jobName), zap.Any("entry", p))
			continue
		}
		resolveCtx, resolveCancel := context.WithTimeout(context.Background(), 60*time.Second)
		site, err := resolveSite(resolveCtx, logger, siteLoader, discoverClient, scraper, p.DealershipID, p.URL)
		resolveCancel()
		if err != nil {
			logger.Warn("site config resolve failed, skipping", zap.String("job", jobName), zap.String("dealershipId", p.DealershipID), zap.Error(err))
			continue
		}

		runCtx, runCancel := context.WithTimeout(context.Background(), cfg.DefaultRunTimeout())
		started := time.Now().UTC()
		res := scraper.ScrapeOnceWithOptions(runCtx, p.URL, site, scrape.Options{
			DealershipID:       p.DealershipID,
			SourceURL:          p.URL,
			BrowserStrategy:    "rod_first",
			EnableAIEnrichment: scraper.AIEnricher != nil,
		})
		runCancel()

		record := model.ScrapeResult{
			ResultID:     uuid.NewString(),
			DealershipID: p.DealershipID,
			SourceURL:    p.URL,
			Status:       model.RunStatusSuccess,
			StartedAt:    started,
			FinishedAt:   time.Now().UTC(),
			TotalItems:   model.ScrapedInventoryCount(res.Items),
			SuccessItems: model.ScrapedInventoryCount(res.Items),
			ErrorCount:   len(res.Errors),
			Items:        res.Items,
			Errors:       res.Errors,
		}
		if len(res.Errors) > 0 {
			record.Status = model.RunStatusPartial
		}
		if err := resultStore.UpsertResult(context.Background(), record); err != nil {
			logger.Error("result store upsert failed", zap.String("resultId", record.ResultID), zap.Error(err))
		}

		out = append(out, scrapedPage{page: p, items: res.Items})
	}
	return out
}

func pageMatchesSchedule(page inventoryapi.PageEntry, scheduleType string) bool {
	requested := strings.ToLower(strings.TrimSpace(scheduleType))
	if page.ScrapeFrequencyMinutes > 0 {
		if page.ScrapeFrequencyMinutes == 7*24*60 {
			return requested == "weekly"
		}
		return requested == "daily"
	}
	configured := strings.ToLower(strings.TrimSpace(page.Schedule.Type))
	if requested == "daily" {
		return configured == "" || configured == "daily"
	}
	return configured == requested
}

func runDailyUpsert(logger *zap.Logger, cfg config.Config, scraper scrape.Service, siteLoader config.Loader, discoverClient *discovery.Client, resultStore store.ResultStore, invClient *inventoryapi.Client) {
	runScheduledUpsert(logger, cfg, scraper, siteLoader, discoverClient, resultStore, invClient, "daily-upsert", "daily")
}

func runWeeklyUpsert(logger *zap.Logger, cfg config.Config, scraper scrape.Service, siteLoader config.Loader, discoverClient *discovery.Client, resultStore store.ResultStore, invClient *inventoryapi.Client) {
	runScheduledUpsert(logger, cfg, scraper, siteLoader, discoverClient, resultStore, invClient, "weekly-upsert", "weekly")
}

func runScheduledUpsert(logger *zap.Logger, cfg config.Config, scraper scrape.Service, siteLoader config.Loader, discoverClient *discovery.Client, resultStore store.ResultStore, invClient *inventoryapi.Client, jobName, scheduleType string) {
	for _, sp := range scrapeAllPages(logger, cfg, scraper, siteLoader, discoverClient, resultStore, invClient, jobName, scheduleType) {
		items := make([]model.InventoryItem, 0, len(sp.items))
		for _, it := range sp.items {
			if it.StockID == "" {
				continue
			}
			items = append(items, it)
		}
		if len(items) == 0 {
			logger.Info("no inventory to upsert", zap.String("dealershipId", sp.page.DealershipID), zap.String("accountID", sp.page.AccountID))
			continue
		}
		postCtx, postCancel := context.WithTimeout(context.Background(), 60*time.Second)
		if err := invClient.UpsertInventory(postCtx, sp.page.AccountID, sp.page.DealershipID, items); err != nil {
			logger.Error("inventory api upsert failed", zap.String("accountID", sp.page.AccountID), zap.Error(err))
		} else {
			logger.Info("inventory upserted", zap.String("accountID", sp.page.AccountID), zap.Int("count", len(items)))
		}
		postCancel()
	}
}

func newLogger(errorLogPath string) (*zap.Logger, func(), error) {
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoder := zapcore.NewJSONEncoder(encCfg)

	consoleCore := zapcore.NewCore(encoder, zapcore.Lock(os.Stderr), zapcore.InfoLevel)
	cores := []zapcore.Core{consoleCore}

	closeFn := func() {}
	if errorLogPath != "" {
		if dir := filepath.Dir(errorLogPath); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, nil, fmt.Errorf("create error log dir: %w", err)
			}
		}
		f, err := os.OpenFile(errorLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("open error log: %w", err)
		}
		closeFn = func() { _ = f.Close() }
		cores = append(cores, zapcore.NewCore(encoder, zapcore.Lock(zapcore.AddSync(f)), zapcore.ErrorLevel))
	}

	return zap.New(zapcore.NewTee(cores...), zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), closeFn, nil
}

func resolveSite(ctx context.Context, logger *zap.Logger, siteLoader config.Loader, discoverClient *discovery.Client, scraper scrape.Service, dealershipID, sourceURL string) (config.SiteConfig, error) {
	resolver := sites.Resolver{
		Loader:   siteLoader,
		Discover: discoverClient,
		Browser:  scraper.Browser,
		Fetcher:  scraper.Fetcher,
		Logger:   logger,
	}
	return resolver.Resolve(ctx, dealershipID, sourceURL)
}

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

	cookieStore := scrape.NewCookieStore(nil)
	cookieStore.PersistPath = "data/cookies.json"
	if cfg.DataDomeCookie != "" {
		_ = cookieStore.Set("datadome", cfg.DataDomeCookie)
	}
	// Persist file takes precedence over .env — API updates survive restarts.
	if err := cookieStore.LoadPersisted(); err != nil {
		logger.Warn("cookie store load failed", zap.Error(err))
	}
	if cookieStore.Len() > 0 {
		logger.Info("DataDome cookie ready", zap.Int("cookies", cookieStore.Len()))
	}

	httpFetcher := scrape.NewHTTPFetcherWithTimeout(time.Duration(cfg.HTTPFetchTimeoutSec) * time.Second)
	httpFetcher.CookieStore = cookieStore
	if cfg.ScraperProxy != "" {
		httpFetcher.ProxyURL = cfg.ScraperProxy
		logger.Info("scraper proxy configured")
	}
	rodBrowser, cancelRod := scrape.NewRodBrowser(cfg.Headless)
	defer cancelRod()
	playwrightBrowser := scrape.NewPlaywrightBrowser(cfg.PlaywrightCommand)

	var activeFetcher scrape.Fetcher = httpFetcher
	var batchDetailFetcher *scrape.BatchDetailFetcher
	if cfg.FetchScriptPath != "" {
		if _, statErr := os.Stat(cfg.FetchScriptPath); statErr == nil {
			cf := scrape.NewCurlFetcher(cfg.FetchScriptPath, cfg.PythonBin, cookieStore)
			cf.Fallback = httpFetcher // plain HTTP fallback for non-DataDome sites / python misconfig
			activeFetcher = cf
			logger.Info("curl_cffi fetcher enabled", zap.String("script", cfg.FetchScriptPath), zap.String("python", cfg.PythonBin))
		}
	}
	if cfg.DetailScriptPath != "" {
		if _, statErr := os.Stat(cfg.DetailScriptPath); statErr == nil {
			imgSizes := scrape.NewImageSizeCache()
			batchDetailFetcher = scrape.NewBatchDetailFetcher(cfg.DetailScriptPath, cfg.PythonBin, imgSizes, cookieStore)
			batchDetailFetcher.MaxPages = cfg.DetailMaxPages
			logger.Info("batch detail fetcher enabled", zap.String("script", cfg.DetailScriptPath), zap.Int("maxPages", cfg.DetailMaxPages))
		}
	}

	imageSizes := scrape.NewImageSizeCache()
	scraper := scrape.Service{
		Browser:            playwrightBrowser,
		AltBrowser:         rodBrowser,
		Fetcher:            activeFetcher,
		DetailFetcher:      scrape.HTMLDetailFetcher{Fetcher: activeFetcher, Browser: rodBrowser, ImageSizes: imageSizes},
		BatchDetailFetcher: batchDetailFetcher,
		Extractors:         []scrape.Extractor{scrape.LoopHTMLExtractor{}, scrape.DOMExtractor{}, scrape.NextDataExtractor{}, scrape.RegexExtractor{}},
		Concurrency:        cfg.Concurrency,
		AIEnricher:         &scrape.AIEnricher{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIModel},
		Logger:             logger,
		DefaultCookies:     cookieStore,
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
	s := api.NewServer(cfg, logger, scraper, siteLoader, resultStore, m, discoverClient, invClient, cookieStore)
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

	// URLs that must never be live-scraped on the cloud (e.g. DataDome-blocked
	// from this IP) — served from the cache that a local scraper syncs in.
	cacheOnly := make(map[string]struct{}, len(cfg.CacheOnlyURLs))
	for _, u := range cfg.CacheOnlyURLs {
		cacheOnly[store.NormalizeURLKey(u)] = struct{}{}
	}

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

		var res scrape.RunResult
		started := time.Now().UTC()

		// Hybrid mode: statically cache-only URLs and URLs auto-flagged as
		// bot-protected on this host are served from the synced cache.
		_, useCache := cacheOnly[store.NormalizeURLKey(p.URL)]
		if !useCache {
			if flagged, ferr := resultStore.IsProtectedURL(context.Background(), p.URL); ferr == nil && flagged {
				useCache = true
			}
		}
		if useCache {
			cached, cerr := resultStore.GetCachedInventory(context.Background(), p.URL)
			if cerr != nil {
				logger.Warn("cache-first url has no cached inventory, skipping",
					zap.String("job", jobName), zap.String("url", p.URL), zap.Error(cerr))
				continue
			}
			res = scrape.RunResult{Items: cached.Items}
			logger.Info("served from cache",
				zap.String("job", jobName), zap.String("url", p.URL),
				zap.Int("items", len(cached.Items)), zap.Time("cachedAt", cached.UpdatedAt))
		} else {
			resolveCtx, resolveCancel := context.WithTimeout(context.Background(), 60*time.Second)
			site, err := resolveSite(resolveCtx, logger, siteLoader, discoverClient, scraper, p.DealershipID, p.URL)
			resolveCancel()
			if err != nil {
				logger.Warn("site config resolve failed, skipping", zap.String("job", jobName), zap.String("dealershipId", p.DealershipID), zap.Error(err))
				continue
			}

			runCtx, runCancel := context.WithTimeout(context.Background(), cfg.DefaultRunTimeout())
			res = scraper.ScrapeOnceWithOptions(runCtx, p.URL, site, scrape.Options{
				DealershipID:       p.DealershipID,
				SourceURL:          p.URL,
				BrowserStrategy:    "rod_first",
				EnableAIEnrichment: scraper.AIEnricher != nil,
			})
			runCancel()

			// Hybrid mode: bot-blocked -> flag for cache-first and fall back to
			// any synced cache instead of upserting an empty result.
			if len(res.Items) == 0 && scrape.IsBotProtectionFailure(res.Errors) {
				if ferr := resultStore.FlagProtectedURL(context.Background(), store.ProtectedURL{SourceURL: p.URL, Reason: "bot-blocked in scheduled job"}); ferr == nil {
					logger.Info("url flagged as bot-protected by scheduled job", zap.String("job", jobName), zap.String("url", p.URL))
				}
				if cached, cerr := resultStore.GetCachedInventory(context.Background(), p.URL); cerr == nil {
					res = scrape.RunResult{Items: cached.Items}
					logger.Info("served from cache after bot-protection failure",
						zap.String("job", jobName), zap.String("url", p.URL), zap.Int("items", len(cached.Items)))
				} else {
					logger.Warn("bot-blocked with no cache; skipping upsert of empty result",
						zap.String("job", jobName), zap.String("url", p.URL))
					continue
				}
			}
		}

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

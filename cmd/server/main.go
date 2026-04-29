package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/example/inventory-scraper/internal/api"
	"github.com/example/inventory-scraper/internal/config"
	"github.com/example/inventory-scraper/internal/discovery"
	"github.com/example/inventory-scraper/internal/metrics"
	"github.com/example/inventory-scraper/internal/model"
	"github.com/example/inventory-scraper/internal/scrape"
	"github.com/example/inventory-scraper/internal/store"
)

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("config load failed", zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		logger.Fatal("mongo connect failed", zap.Error(err))
	}
	defer mongoClient.Disconnect(context.Background())

	runStore := store.NewMongoRunStore(mongoClient, cfg.MongoDBName, cfg.MongoCollection)
	httpFetcher := scrape.NewHTTPFetcher()
	browser, cancelBrowser := scrape.NewChromeBrowser(cfg.Headless)
	defer cancelBrowser()

	scraper := scrape.Service{
		Browser:       browser,
		Fetcher:       httpFetcher,
		DetailFetcher: scrape.HTMLDetailFetcher{Fetcher: httpFetcher},
		Extractors:    []scrape.Extractor{scrape.LoopHTMLExtractor{}, scrape.DOMExtractor{}, scrape.RegexExtractor{}},
		Concurrency:   cfg.Concurrency,
	}

	m := metrics.New()
	var discoverClient *discovery.Client
	if cfg.EnableCodexDiscovery {
		discoverClient = &discovery.Client{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIModel}
	}
	s := api.NewServer(cfg, logger, scraper, config.Loader{Dir: cfg.SiteConfigsDir}, runStore, m, discoverClient)
	router := s.Router()
	httpServer := &http.Server{Addr: ":" + cfg.Port, Handler: router}

	var cronRunner *scrape.CronRunner
	if cfg.EnableCron {
		cronRunner, err = scrape.StartCron(logger, cfg.CronSpec, func() {
			site, err := config.Loader{Dir: cfg.SiteConfigsDir}.LoadByName(cfg.CronDealershipID)
			if err != nil {
				logger.Error("cron site load failed", zap.Error(err))
				return
			}
			cctx, cancel := context.WithTimeout(context.Background(), cfg.DefaultRunTimeout())
			defer cancel()
			res := scraper.ScrapeOnce(cctx, cfg.CronSourceURL, site)
			summary := model.RunSummary{RunID: uuid.NewString(), DealershipID: cfg.CronDealershipID, SourceURL: cfg.CronSourceURL, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), TotalItems: len(res.Items), SuccessItems: len(res.Items), ErrorCount: len(res.Errors), Status: model.RunStatusSuccess}
			if len(res.Errors) > 0 && len(res.Items) > 0 {
				summary.Status = model.RunStatusPartial
			}
			if len(res.Items) == 0 {
				summary.Status = model.RunStatusFailed
				summary.FailureReason = "cron no inventory"
			}
			_ = runStore.UpsertRun(context.Background(), summary)
		})
		if err != nil {
			logger.Fatal("cron start failed", zap.Error(err))
		}
	}

	go func() {
		logger.Info("server starting", zap.String("port", cfg.Port))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server exited unexpectedly", zap.Error(err))
		}
	}()

	<-ctx.Done()
	if cronRunner != nil {
		cronRunner.Stop(context.Background())
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	logger.Info("server stopped gracefully")
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/drewlesueur/tts-inventory-getter/internal/auth"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/discovery"
	"github.com/drewlesueur/tts-inventory-getter/internal/inventoryapi"
	"github.com/drewlesueur/tts-inventory-getter/internal/metrics"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
	"github.com/drewlesueur/tts-inventory-getter/internal/sites"
	"github.com/drewlesueur/tts-inventory-getter/internal/store"
)

type Server struct {
	cfg       config.Config
	logger    *zap.Logger
	scraper   scrape.Service
	sites     config.Loader
	store     store.ResultStore
	metrics   *metrics.Metrics
	discover  *discovery.Client
	invClient *inventoryapi.Client

	dailyUpsertMu      sync.Mutex
	dailyUpsertRunning bool
	dailyUpsertJob     func()
}

func NewServer(cfg config.Config, logger *zap.Logger, scraper scrape.Service, sites config.Loader, st store.ResultStore, mt *metrics.Metrics, discover *discovery.Client, invClient *inventoryapi.Client) *Server {
	return &Server{cfg: cfg, logger: logger, scraper: scraper, sites: sites, store: st, metrics: mt, discover: discover, invClient: invClient}
}

func (s *Server) SetDailyUpsertJob(job func()) {
	s.dailyUpsertMu.Lock()
	defer s.dailyUpsertMu.Unlock()
	s.dailyUpsertJob = job
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.Handle("GET /metrics", promhttp.Handler())

	v1 := http.NewServeMux()
	v1.HandleFunc("POST /v1/scrape/once", s.handleScrapeOnce)
	v1.HandleFunc("GET /v1/results/", s.handleGetResult)
	v1.HandleFunc("DELETE /v1/results", s.handleClearResults)
	v1.HandleFunc("DELETE /v1/site-config-cache", s.handleClearSiteConfigCache)
	v1.HandleFunc("POST /v1/scrape/discover-flow", s.handleDiscover)
	v1.HandleFunc("POST /v1/scrape/daily-upsert", s.handleDailyUpsertCron)
	v1.HandleFunc("POST /v1/cron/daily-upsert", s.handleDailyUpsertCron)
	v1.HandleFunc("POST /v1/manual-load/daily-upsert", s.handleDailyUpsertCron)
	v1.HandleFunc("POST /v1/taptosign/upsert", s.handleTapToSignUpsert)

	protected := chain(v1,
		auth.APIKeyMiddleware(s.cfg.ServiceKey),
	)
	if s.cfg.EnableHMAC {
		protected = chain(protected, auth.HMACMiddleware(s.cfg.HMACSecret))
	}
	protected = chain(protected, bodyLimit(s.cfg.RequestBodyLimitMB<<20), rateLimit(s.cfg.RateLimitRPS, s.cfg.RateLimitBurst))

	mux.Handle("/v1/", protected)
	return mux
}

func (s *Server) handleTapToSignUpsert(w http.ResponseWriter, r *http.Request) {
	if s.invClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, model.ErrorResponse("INVENTORY_API_NOT_CONFIGURED", "inventory api client is not configured"))
		return
	}
	var req TapToSignUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if req.AccountID == "" || req.DealershipID == "" {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "accountId and dealershipId are required"))
		return
	}
	items := make([]model.InventoryItem, 0, len(req.Items))
	for _, it := range req.Items {
		if it.StockID != "" {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "no valid items to upsert (items must have stockId)"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.invClient.UpsertInventory(ctx, req.AccountID, req.DealershipID, items); err != nil {
		s.logger.Error("taptosign upsert failed", zap.String("accountId", req.AccountID), zap.String("dealershipId", req.DealershipID), zap.Error(err))
		writeJSON(w, http.StatusBadGateway, model.ErrorResponse("UPSERT_FAILED", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "count": len(items)})
}

func (s *Server) handleDailyUpsertCron(w http.ResponseWriter, _ *http.Request) {
	s.dailyUpsertMu.Lock()
	if s.dailyUpsertJob == nil {
		s.dailyUpsertMu.Unlock()
		writeJSON(w, http.StatusServiceUnavailable, model.ErrorResponse("JOB_NOT_CONFIGURED", "daily upsert job is not configured"))
		return
	}
	if s.dailyUpsertRunning {
		s.dailyUpsertMu.Unlock()
		writeJSON(w, http.StatusConflict, model.ErrorResponse("JOB_ALREADY_RUNNING", "daily upsert job is already running"))
		return
	}
	job := s.dailyUpsertJob
	jobID := uuid.NewString()
	s.dailyUpsertRunning = true
	s.dailyUpsertMu.Unlock()

	go func() {
		start := time.Now().UTC()
		s.logger.Info("manual daily upsert started", zap.String("jobId", jobID))
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("manual daily upsert panicked", zap.String("jobId", jobID), zap.Any("panic", recovered))
			}
			s.dailyUpsertMu.Lock()
			s.dailyUpsertRunning = false
			s.dailyUpsertMu.Unlock()
			s.logger.Info("manual daily upsert finished", zap.String("jobId", jobID), zap.Duration("duration", time.Since(start)))
		}()
		job()
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "job": "daily-upsert", "jobId": jobID})
}

func (s *Server) handleScrapeOnce(w http.ResponseWriter, r *http.Request) {
	var req ScrapeOnceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	// Log inbound scrape payload so deploy-only behavior differences (for example maxItems=15)
	// can be diagnosed from server logs quickly.
	s.logger.Info("scrape request payload",
		zap.String("dealershipId", req.DealershipID),
		zap.String("sourceUrl", req.SourceURL),
		zap.String("idempotencyKey", req.IdempotencyKey),
		zap.Any("options", req.Options),
		zap.Bool("hasSiteConfigOverride", req.SiteConfig != nil),
	)
	if req.DealershipID == "" || !validURL(req.SourceURL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "dealershipId and valid sourceUrl are required"))
		return
	}
	scopedIdempotencyKey := scopedIdempotencyKey(req.IdempotencyKey, req.DealershipID, req.SourceURL)
	if existing, err := s.store.FindByIdempotency(r.Context(), scopedIdempotencyKey); err == nil {
		if idempotencyTargetMatches(existing, req.DealershipID, req.SourceURL) {
			writeJSON(w, http.StatusOK, resultResponse(existing))
			return
		}
	}

	site, err := s.resolveSiteConfig(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("SITE_CONFIG_NOT_FOUND", err.Error()))
		return
	}
	site = s.applyCrawlLimits(site, req.Options)

	resultID := uuid.NewString()
	started := time.Now().UTC()
	resultRecord := model.ScrapeResult{ResultID: resultID, DealershipID: req.DealershipID, SourceURL: req.SourceURL, Status: model.RunStatusRunning, StartedAt: started, IdempotencyKey: scopedIdempotencyKey, ProgressStage: "accepted"}
	if err := s.store.UpsertResult(r.Context(), resultRecord); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}

	timeout := s.cfg.DefaultRunTimeout()
	if req.Options != nil && req.Options.RunTimeoutSec > 0 {
		timeout = time.Duration(req.Options.RunTimeoutSec) * time.Second
	}

	go s.runScrapeAsync(resultID, req.DealershipID, req.SourceURL, scopedIdempotencyKey, site, timeout, req.Options)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "resultId": resultID})
}

func (s *Server) applyCrawlLimits(site config.SiteConfig, opts *ScrapeOptions) config.SiteConfig {
	if site.ListPage.Pagination.MaxPages <= 0 {
		site.ListPage.Pagination.MaxPages = s.cfg.DefaultMaxPages
	}
	if site.ListPage.Pagination.ScrollMaxAttempts <= 0 {
		site.ListPage.Pagination.ScrollMaxAttempts = s.cfg.DefaultMaxScrollAttempts
	}
	if site.ListPage.Pagination.ClickMaxAttempts <= 0 {
		site.ListPage.Pagination.ClickMaxAttempts = s.cfg.DefaultMaxLoadMoreClicks
	}
	if opts == nil {
		return site
	}
	if opts.MaxPages > 0 {
		site.ListPage.Pagination.MaxPages = opts.MaxPages
	}
	if opts.MaxScrollAttempts > 0 {
		site.ListPage.Pagination.ScrollMaxAttempts = opts.MaxScrollAttempts
	}
	if opts.MaxLoadMoreClicks > 0 {
		site.ListPage.Pagination.ClickMaxAttempts = opts.MaxLoadMoreClicks
	}
	if opts.MaxItems > 0 {
		site.ListPage.MaxItems = opts.MaxItems
	}
	return site
}

func (s *Server) runScrapeAsync(resultID, dealershipID, sourceURL, idempotencyKey string, site config.SiteConfig, timeout time.Duration, opts *ScrapeOptions) {
	maxAttempts := s.cfg.ScrapeMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	backoffBase := s.cfg.ScrapeRetryBackoffSec
	if backoffBase <= 0 {
		backoffBase = 2
	}
	var finalRecord model.ScrapeResult
	var finalDur float64
	runStarted := time.Now().UTC()
	progressMu := sync.Mutex{}
	writeProgress := func(stage string, count int, attempt int) {
		progressMu.Lock()
		defer progressMu.Unlock()
		if !isStableInventoryCountStage(stage) {
			count = 0
		}
		record := model.ScrapeResult{
			ResultID:       resultID,
			DealershipID:   dealershipID,
			SourceURL:      sourceURL,
			Status:         model.RunStatusRunning,
			StartedAt:      runStarted,
			TotalItems:     count,
			SuccessItems:   count,
			AttemptCount:   attempt,
			IdempotencyKey: idempotencyKey,
			ProgressStage:  stage,
			IsRetrying:     false,
		}
		if err := s.store.UpsertResult(context.Background(), record); err != nil {
			s.logger.Error("result progress upsert failed", zap.String("resultId", resultID), zap.String("stage", stage), zap.Error(err))
		}
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		start := time.Now().UTC()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		scrapeOpts := scrape.Options{
			DealershipID:    dealershipID,
			SourceURL:       sourceURL,
			BrowserStrategy: "rod_first",
			Progress: func(progress scrape.Progress) {
				writeProgress(progress.Stage, progress.TotalItems, attempt)
			},
		}
		if opts != nil {
			if strings.TrimSpace(opts.BrowserStrategy) != "" {
				scrapeOpts.BrowserStrategy = opts.BrowserStrategy
			}
			if opts.EnableAIEnrichment != nil {
				scrapeOpts.EnableAIEnrichment = *opts.EnableAIEnrichment
			} else {
				scrapeOpts.EnableAIEnrichment = s.scraper.AIEnricher != nil
			}
		} else {
			scrapeOpts.EnableAIEnrichment = s.scraper.AIEnricher != nil
		}
		result := s.scraper.ScrapeOnceWithOptions(ctx, sourceURL, site, scrapeOpts)
		cancel()
		finalDur = time.Since(start).Seconds()
		inventoryCount := model.ScrapedInventoryCount(result.Items)

		record := model.ScrapeResult{
			ResultID:       resultID,
			DealershipID:   dealershipID,
			SourceURL:      sourceURL,
			Status:         model.RunStatusSuccess,
			StartedAt:      start,
			FinishedAt:     time.Now().UTC(),
			TotalItems:     inventoryCount,
			SuccessItems:   inventoryCount,
			FailedItems:    0,
			ErrorCount:     len(result.Errors),
			AttemptCount:   attempt,
			IdempotencyKey: idempotencyKey,
			Items:          result.Items,
			Errors:         result.Errors,
			IsRetrying:     false,
			ProgressStage:  "completed",
		}
		if len(result.Errors) > 0 {
			record.Status = model.RunStatusPartial
		}
		record.LastError = firstErrorMessage(result.Errors)
		if record.Status == model.RunStatusPartial {
			record.ProgressStage = "completed_with_errors"
		}
		finalRecord = record

		retryable := isRetryableScrapeFailure(result)
		if !retryable || attempt == maxAttempts {
			break
		}

		wait := time.Duration(backoffBase*(1<<(attempt-1))) * time.Second
		record.IsRetrying = true
		record.NextRetryAt = time.Now().UTC().Add(wait)
		record.ProgressStage = "retry_wait"
		if err := s.store.UpsertResult(context.Background(), record); err != nil {
			s.logger.Error("result store upsert failed", zap.String("resultId", resultID), zap.Error(err))
			return
		}
		time.Sleep(wait)
	}

	if err := s.store.UpsertResult(context.Background(), finalRecord); err != nil {
		s.logger.Error("result store upsert failed", zap.String("resultId", resultID), zap.Error(err))
		return
	}
	s.metrics.RunsTotal.WithLabelValues(string(finalRecord.Status)).Inc()
	s.metrics.RunDuration.WithLabelValues(dealershipID).Observe(finalDur)
	s.metrics.ItemsScraped.WithLabelValues(dealershipID).Add(float64(finalRecord.TotalItems))
	s.logger.Info("scrape completed", zap.String("resultId", resultID), zap.Int("attempts", finalRecord.AttemptCount), zap.Int("items", len(finalRecord.Items)), zap.Int("uniqueVinCount", finalRecord.TotalItems), zap.Int("errors", len(finalRecord.Errors)))
}

func firstErrorMessage(errs []model.StructuredError) string {
	if len(errs) == 0 {
		return ""
	}
	return errs[0].Message
}

func isRetryableScrapeFailure(result scrape.RunResult) bool {
	if len(result.Items) > 0 {
		return false
	}
	for _, e := range result.Errors {
		if e.Code == "SCRAPE_RENDER_FAILED" && isTransientErrorMessage(e.Message) {
			return true
		}
	}
	return false
}

func isTransientErrorMessage(msg string) bool {
	l := strings.ToLower(msg)
	if strings.Contains(l, "blocked redirect to local host") || strings.Contains(l, "blocked redirect to private host") {
		return false
	}
	return strings.Contains(l, "context deadline exceeded") ||
		strings.Contains(l, "client.timeout") ||
		strings.Contains(l, "timeout") ||
		strings.Contains(l, "temporary") ||
		strings.Contains(l, "connection reset") ||
		strings.Contains(l, "eof")
}

func (s *Server) handleGetResult(w http.ResponseWriter, r *http.Request) {
	resultID := strings.TrimPrefix(r.URL.Path, "/v1/results/")
	if resultID == "" || strings.Contains(resultID, "/") {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "resultId is required"))
		return
	}
	result, err := s.store.GetResult(r.Context(), resultID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, model.ErrorResponse("RESULT_NOT_FOUND", "result not found"))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, resultResponse(result))
}

func resultResponse(result model.ScrapeResult) map[string]any {
	scrapedCount := result.TotalItems
	computedScrapedCount := model.ScrapedInventoryCount(result.Items)
	if computedScrapedCount > 0 {
		scrapedCount = computedScrapedCount
	} else if scrapedCount == 0 && len(result.Items) > 0 {
		scrapedCount = len(result.Items)
	}
	if result.Status == model.RunStatusRunning {
		if !isStableInventoryCountStage(result.ProgressStage) {
			scrapedCount = 0
		}
		return map[string]any{
			"status":                "ok",
			"resultStatus":          result.Status,
			"progressStage":         result.ProgressStage,
			"scrapedInventoryCount": scrapedCount,
			"totalItems":            scrapedCount,
		}
	}
	return map[string]any{
		"status":                "ok",
		"resultId":              result.ResultID,
		"resultStatus":          result.Status,
		"progressStage":         result.ProgressStage,
		"scrapedInventoryCount": scrapedCount,
		"totalItems":            scrapedCount,
		"successItems":          result.SuccessItems,
		"failedItems":           result.FailedItems,
		"errorCount":            result.ErrorCount,
		"result":                result,
	}
}

func isStableInventoryCountStage(stage string) bool {
	switch stage {
	case "items_deduped", "details_completed", "details_deduped", "ai_progress", "ai_completed", "completed":
		return true
	default:
		return false
	}
}

func (s *Server) handleClearResults(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearResults(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "results cleared"})
}

func (s *Server) handleClearSiteConfigCache(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sourceURL := strings.TrimSpace(q.Get("sourceUrl"))
	clearAll := strings.EqualFold(strings.TrimSpace(q.Get("all")), "true")

	if clearAll {
		if err := s.sites.ClearCacheFiles(); err != nil {
			writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("SITE_CONFIG_CACHE_CLEAR_FAILED", err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "site config cache cleared"})
		return
	}

	if sourceURL == "" || !validURL(sourceURL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "valid sourceUrl is required, or set all=true"))
		return
	}
	key := sites.CacheKeyForSourceURL(sourceURL)
	if err := s.sites.DeleteByName(key); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("SITE_CONFIG_CACHE_DELETE_FAILED", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "site config cache deleted", "urlKey": key})
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.EnableCodexDiscovery || s.discover == nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("DISCOVERY_DISABLED", "codex discovery mode is disabled"))
		return
	}
	var req DiscoverFlowRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if !validURL(req.SourceURL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "valid sourceUrl is required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	proposed, err := s.discoverSiteConfig(ctx, req.SourceURL, req.DealershipID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, model.ErrorResponse("DISCOVERY_MODEL_FAILED", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "proposedConfig": proposed})
}

func (s *Server) resolveSiteConfig(ctx context.Context, req ScrapeOnceRequest) (config.SiteConfig, error) {
	if req.SiteConfig != nil {
		return *req.SiteConfig, nil
	}
	resolver := sites.Resolver{
		Loader:   s.sites,
		Discover: s.discover,
		Browser:  s.scraper.Browser,
		Fetcher:  s.scraper.Fetcher,
		Logger:   s.logger,
	}
	return resolver.Resolve(ctx, req.DealershipID, req.SourceURL)
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func validURL(raw string) bool {
	u, err := url.ParseRequestURI(raw)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func canonicalSourceURL(dealershipID, sourceURL string) string {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return sourceURL
	}
	host := strings.ToLower(u.Hostname())
	path := strings.TrimSuffix(strings.ToLower(u.Path), "/")
	if strings.EqualFold(dealershipID, "txtcharlie") && host == "www.txtcharlie.com" && path == "/inventory" {
		return "https://www.txtcharlie.com/find-vehicles-for-sale-in-ft-lauderdale-fl/"
	}
	return sourceURL
}

func idempotencyTargetMatches(existing model.ScrapeResult, dealershipID, sourceURL string) bool {
	if !strings.EqualFold(existing.DealershipID, dealershipID) {
		return false
	}
	existingURL := normalizeSourceURL(canonicalSourceURL(existing.DealershipID, existing.SourceURL))
	requestedURL := normalizeSourceURL(canonicalSourceURL(dealershipID, sourceURL))
	return existingURL == requestedURL
}

func normalizeSourceURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSpace(strings.ToLower(raw))
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(strings.ToLower(u.Path), "/")
	u.Fragment = ""
	return u.String()
}

func scopedIdempotencyKey(rawKey, dealershipID, sourceURL string) string {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s", rawKey, strings.ToLower(strings.TrimSpace(dealershipID)), normalizeSourceURL(canonicalSourceURL(dealershipID, sourceURL)))
}

func (s *Server) discoverSiteConfig(ctx context.Context, sourceURL, dealershipID string) (config.SiteConfig, error) {
	if s.discover == nil {
		return config.SiteConfig{}, fmt.Errorf("discovery client is not configured")
	}
	html := ""
	if s.scraper.Browser != nil {
		h, err := safeRender(ctx, s.scraper.Browser, sourceURL)
		if err == nil {
			html = h
		}
	}
	if html == "" {
		if s.scraper.Fetcher == nil {
			return config.SiteConfig{}, fmt.Errorf("fetcher is not configured")
		}
		h, err := s.scraper.Fetcher.Fetch(ctx, sourceURL)
		if err != nil {
			return config.SiteConfig{}, err
		}
		html = h
	}
	proposed, err := s.discover.Discover(ctx, sourceURL, html)
	if err != nil {
		return config.SiteConfig{}, err
	}
	if dealershipID != "" {
		proposed.Name = dealershipID
	}
	return proposed, nil
}

func safeRender(ctx context.Context, browser scrape.Browser, sourceURL string) (html string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("browser render panic: %v", r)
		}
	}()
	return browser.Render(ctx, sourceURL, config.SiteConfig{})
}

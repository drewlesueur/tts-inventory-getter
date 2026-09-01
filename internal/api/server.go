package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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
	cfg         config.Config
	logger      *zap.Logger
	scraper     scrape.Service
	sites       config.Loader
	store       store.ResultStore
	metrics     *metrics.Metrics
	discover    *discovery.Client
	invClient   *inventoryapi.Client
	cookieStore *scrape.CookieStore

	dailyUpsertMu      sync.Mutex
	dailyUpsertRunning bool
	dailyUpsertJob     func()
}

func NewServer(cfg config.Config, logger *zap.Logger, scraper scrape.Service, sites config.Loader, st store.ResultStore, mt *metrics.Metrics, discover *discovery.Client, invClient *inventoryapi.Client, cookieStore *scrape.CookieStore) *Server {
	return &Server{cfg: cfg, logger: logger, scraper: scraper, sites: sites, store: st, metrics: mt, discover: discover, invClient: invClient, cookieStore: cookieStore}
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
	v1.HandleFunc("POST /v1/scrape/once-and-result", s.handleScrapeOnceAndResult)
	v1.HandleFunc("POST /v1/scrape/run", s.handleScrapeRun)
	v1.HandleFunc("POST /v1/scrape/sync", s.handleScrapeSync)
	v1.HandleFunc("GET /v1/scrape/cache", s.handleGetCachedInventory)
	v1.HandleFunc("GET /v1/scrape/pending-sync", s.handlePendingSync)
	v1.HandleFunc("GET /v1/scrape/protected", s.handleListProtected)
	v1.HandleFunc("POST /v1/scrape/protected", s.handleFlagProtected)
	v1.HandleFunc("DELETE /v1/scrape/protected", s.handleUnflagProtected)
	v1.HandleFunc("POST /v1/cookies/datadome", s.handleSetDataDomeCookie)
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
	cacheKey := scrapeOnceCacheKey(req.DealershipID, req.SourceURL)
	existing, existingErr := s.store.FindByIdempotency(r.Context(), cacheKey)
	site, siteErr := s.resolveSiteConfig(r.Context(), req)
	if existingErr == nil {
		fresh := isFreshScrapeCache(existing, time.Now().UTC())
		if siteErr == nil {
			fresh = isFreshScrapeCacheForSite(existing, site, time.Now().UTC())
		}
		if fresh {
			writeJSON(w, http.StatusOK, resultResponse(existing))
			return
		}
	}
	if s.serveCacheOnlyScrapeOnce(w, r, req.DealershipID, req.SourceURL, cacheKey) {
		return
	}
	if siteErr != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("SITE_CONFIG_NOT_FOUND", siteErr.Error()))
		return
	}
	if err := s.store.DeleteIdempotency(r.Context(), cacheKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("CACHE_INVALIDATION_FAILED", err.Error()))
		return
	}
	site = s.applyCrawlLimits(site, req.Options)
	resultID := uuid.NewString()
	started := time.Now().UTC()
	resultRecord := model.ScrapeResult{ResultID: resultID, DealershipID: req.DealershipID, SourceURL: req.SourceURL, Status: model.RunStatusRunning, StartedAt: started, IdempotencyKey: cacheKey, ProgressStage: "accepted"}
	if err := s.store.UpsertResult(r.Context(), resultRecord); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}

	timeout := s.cfg.DefaultRunTimeout()
	if req.Options != nil && req.Options.RunTimeoutSec > 0 {
		timeout = time.Duration(req.Options.RunTimeoutSec) * time.Second
	}

	go s.runScrapeAsync(resultID, req.DealershipID, req.SourceURL, cacheKey, site, timeout, req.Options)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "resultId": resultID})
}

func (s *Server) handleScrapeOnceAndResult(w http.ResponseWriter, r *http.Request) {
	var req ScrapeOnceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	s.logger.Info("scrape-once-and-result request",
		zap.String("dealershipId", req.DealershipID),
		zap.String("sourceUrl", req.SourceURL),
		zap.Any("options", req.Options),
	)
	if req.DealershipID == "" || !validURL(req.SourceURL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "dealershipId and valid sourceUrl are required"))
		return
	}
	cacheKey := scrapeOnceCacheKey(req.DealershipID, req.SourceURL)
	existing, existingErr := s.store.FindByIdempotency(r.Context(), cacheKey)
	site, siteErr := s.resolveSiteConfig(r.Context(), req)
	if existingErr == nil {
		fresh := isFreshScrapeCache(existing, time.Now().UTC())
		if siteErr == nil {
			fresh = isFreshScrapeCacheForSite(existing, site, time.Now().UTC())
		}
		if fresh {
			writeJSON(w, http.StatusOK, resultResponse(existing))
			return
		}
	}
	if s.serveCacheOnlyScrapeOnce(w, r, req.DealershipID, req.SourceURL, cacheKey) {
		return
	}
	if siteErr != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("SITE_CONFIG_NOT_FOUND", siteErr.Error()))
		return
	}
	if err := s.store.DeleteIdempotency(r.Context(), cacheKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("CACHE_INVALIDATION_FAILED", err.Error()))
		return
	}
	site = s.applyCrawlLimits(site, req.Options)
	resultID := uuid.NewString()
	started := time.Now().UTC()
	resultRecord := model.ScrapeResult{ResultID: resultID, DealershipID: req.DealershipID, SourceURL: req.SourceURL, Status: model.RunStatusRunning, StartedAt: started, IdempotencyKey: cacheKey, ProgressStage: "accepted"}
	if err := s.store.UpsertResult(r.Context(), resultRecord); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}

	timeout := s.cfg.DefaultRunTimeout()
	if req.Options != nil && req.Options.RunTimeoutSec > 0 {
		timeout = time.Duration(req.Options.RunTimeoutSec) * time.Second
	}

	go s.runScrapeAsync(resultID, req.DealershipID, req.SourceURL, cacheKey, site, timeout, req.Options)

	// Poll until the scrape finishes (or the request context is cancelled).
	pollCtx, pollCancel := context.WithTimeout(r.Context(), timeout+30*time.Second)
	defer pollCancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-pollCtx.Done():
			writeJSON(w, http.StatusGatewayTimeout, model.ErrorResponse("TIMEOUT", "scrape did not finish within the allowed time"))
			return
		case <-ticker.C:
			result, err := s.store.GetResult(pollCtx, resultID)
			if err != nil {
				continue
			}
			if result.Status != model.RunStatusRunning {
				writeJSON(w, http.StatusOK, resultResponse(result))
				return
			}
		}
	}
}

func (s *Server) handleSetDataDomeCookie(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cookie string `json:"cookie"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if strings.TrimSpace(req.Cookie) == "" {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "cookie is required"))
		return
	}
	if s.cookieStore == nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("COOKIE_STORE_UNAVAILABLE", "cookie store not initialised"))
		return
	}
	if err := s.cookieStore.Set("datadome", strings.TrimSpace(req.Cookie)); err != nil {
		s.logger.Error("datadome cookie persist failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("PERSIST_FAILED", err.Error()))
		return
	}
	s.logger.Info("datadome cookie updated via API")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "datadome cookie updated"})
}

// handleScrapeSync accepts inventory scraped externally (e.g. on a local
// residential IP), caches it keyed by URL, and upserts it to the owning
// dealer/account. Cache-only syncs remain URL-scoped and deliberately do not
// resolve or persist account/dealership ownership.
func (s *Server) handleScrapeSync(w http.ResponseWriter, r *http.Request) {
	var req ScrapeSyncRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if !validURL(req.URL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "valid url is required"))
		return
	}

	// Keep only items with a stock id (matches the upsert requirement elsewhere).
	items := make([]model.InventoryItem, 0, len(req.Items))
	for _, it := range req.Items {
		if it.StockID != "" || it.Stock != "" {
			items = append(items, it)
		}
	}

	dealershipID, accountID := req.DealershipID, req.AccountID
	if req.SkipUpsert {
		dealershipID, accountID = "", ""
	} else if (dealershipID == "" || accountID == "") && s.invClient != nil {
		if d, a, ok := s.resolveDealerByURL(r.Context(), req.URL); ok {
			if dealershipID == "" {
				dealershipID = d
			}
			if accountID == "" {
				accountID = a
			}
		}
	}

	// Cache it (keyed by URL) so scheduled runs can serve from cache.
	cached := store.CachedInventory{
		SourceURL:    req.URL,
		DealershipID: dealershipID,
		AccountID:    accountID,
		Items:        items,
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.store.UpsertCachedInventory(r.Context(), cached); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("CACHE_STORE_FAILED", err.Error()))
		return
	}
	// A cache-only sync is the hybrid-worker signal that this source cannot be
	// scraped reliably from the cloud host. Persist that routing decision so
	// /v1/scrape/run serves this cache immediately instead of timing out on a
	// redundant live attempt. Protected flags are host-wide in shouldServeFromCache.
	if req.SkipUpsert {
		if err := s.store.FlagProtectedURL(r.Context(), store.ProtectedURL{
			SourceURL: req.URL,
			Reason:    "cache-only hybrid sync",
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("PROTECTED_URL_STORE_FAILED", err.Error()))
			return
		}
	}
	s.logger.Info("inventory synced to cache",
		zap.String("url", req.URL),
		zap.String("dealershipId", dealershipID),
		zap.String("accountId", accountID),
		zap.Int("items", len(items)),
	)

	upserted := false
	var upsertErr string
	if !req.SkipUpsert && s.invClient != nil && accountID != "" && dealershipID != "" && len(items) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if err := s.invClient.UpsertInventory(ctx, accountID, dealershipID, items); err != nil {
			upsertErr = err.Error()
			s.logger.Error("sync upsert failed", zap.String("accountId", accountID), zap.Error(err))
		} else {
			upserted = true
		}
	}

	resp := map[string]any{
		"status":       "ok",
		"url":          req.URL,
		"dealershipId": dealershipID,
		"accountId":    accountID,
		"cachedItems":  len(items),
		"upserted":     upserted,
	}
	if upsertErr != "" {
		resp["upsertError"] = upsertErr
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetCachedInventory returns the cached inventory for a URL (?url=...).
func (s *Server) handleGetCachedInventory(w http.ResponseWriter, r *http.Request) {
	sourceURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if !validURL(sourceURL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "valid url query param is required"))
		return
	}
	cached, err := s.store.GetCachedInventory(r.Context(), sourceURL)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, model.ErrorResponse("CACHE_NOT_FOUND", "no cached inventory for url"))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("CACHE_READ_FAILED", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"url":          cached.SourceURL,
		"dealershipId": cached.DealershipID,
		"accountId":    cached.AccountID,
		"itemCount":    len(cached.Items),
		"updatedAt":    cached.UpdatedAt,
		"items":        cached.Items,
	})
}

// handlePendingSync lists bot-protected URLs whose synced cache is missing or
// older than ?maxAgeHours (default 12). A local residential-IP worker polls
// this, scrapes each URL, and pushes results back via /v1/scrape/sync.
func (s *Server) handlePendingSync(w http.ResponseWriter, r *http.Request) {
	maxAge := 12 * time.Hour
	if v := strings.TrimSpace(r.URL.Query().Get("maxAgeHours")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxAge = time.Duration(n) * time.Hour
		}
	}
	protected, err := s.store.ListProtectedURLs(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	// Statically configured cache-only URLs need syncing too.
	seen := map[string]bool{}
	urls := make([]string, 0, len(protected)+len(s.cfg.CacheOnlyURLs))
	for _, p := range protected {
		k := store.NormalizeURLKey(p.SourceURL)
		if !seen[k] {
			seen[k] = true
			urls = append(urls, p.SourceURL)
		}
	}
	for _, u := range s.cfg.CacheOnlyURLs {
		k := store.NormalizeURLKey(u)
		if !seen[k] {
			seen[k] = true
			urls = append(urls, u)
		}
	}
	type pending struct {
		URL         string `json:"url"`
		CacheItems  int    `json:"cacheItems"`
		CacheAgeSec int64  `json:"cacheAgeSec"`
		HasCache    bool   `json:"hasCache"`
	}
	out := make([]pending, 0, len(urls))
	for _, u := range urls {
		cached, cerr := s.store.GetCachedInventory(r.Context(), u)
		if cerr == nil && time.Since(cached.UpdatedAt) < maxAge {
			continue // fresh enough
		}
		p := pending{URL: u}
		if cerr == nil {
			p.HasCache = true
			p.CacheItems = len(cached.Items)
			p.CacheAgeSec = int64(time.Since(cached.UpdatedAt).Seconds())
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "pending": out, "maxAgeHours": int(maxAge.Hours())})
}

func (s *Server) handleListProtected(w http.ResponseWriter, r *http.Request) {
	protected, err := s.store.ListProtectedURLs(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "protected": protected})
}

// handleFlagProtected manually marks a URL as bot-protected so it is served
// cache-first without waiting for a live scrape to fail.
func (s *Server) handleFlagProtected(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if !validURL(req.URL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "valid url is required"))
		return
	}
	if req.Reason == "" {
		req.Reason = "manually flagged"
	}
	if err := s.store.FlagProtectedURL(r.Context(), store.ProtectedURL{SourceURL: req.URL, Reason: req.Reason}); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	s.logger.Info("url manually flagged as bot-protected", zap.String("url", req.URL))
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "url": req.URL})
}

func (s *Server) handleUnflagProtected(w http.ResponseWriter, r *http.Request) {
	sourceURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if !validURL(sourceURL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "valid url query param is required"))
		return
	}
	if err := s.store.UnflagProtectedURL(r.Context(), sourceURL); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "url": sourceURL})
}

// resolveDealerByURL looks up the dealership/account that owns a URL from the
// inventory API page list.
func (s *Server) resolveDealerByURL(ctx context.Context, sourceURL string) (dealershipID, accountID string, ok bool) {
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pages, err := s.invClient.ListPages(lctx)
	if err != nil {
		s.logger.Warn("resolveDealerByURL list failed", zap.Error(err))
		return "", "", false
	}
	want := store.NormalizeURLKey(sourceURL)
	for _, p := range pages {
		if store.NormalizeURLKey(p.URL) == want {
			return p.DealershipID, p.AccountID, true
		}
	}
	return "", "", false
}

// isCacheOnly reports whether a URL must be served from cache (never live-scraped).
func (s *Server) isCacheOnly(rawURL string) bool {
	want := store.NormalizeURLKey(rawURL)
	// These DataDome-protected dealers are intentionally scraped by the local
	// residential-IP worker. The server must never attempt a live pull for them.
	for _, host := range []string{"jjsadobeauto.com", "saiautosale.com"} {
		if hostnameOf(rawURL) == host {
			return true
		}
	}
	for _, u := range s.cfg.CacheOnlyURLs {
		if store.NormalizeURLKey(u) == want {
			return true
		}
	}
	return false
}

// serveCacheOnlyScrapeOnce makes the scrape-once APIs honor the same synced
// inventory cache as /v1/scrape/run. Cache-only URLs never fall through to a
// live server scrape, even when the cached inventory is older than 24 hours;
// the local-sync worker is responsible for refreshing it.
func (s *Server) serveCacheOnlyScrapeOnce(w http.ResponseWriter, r *http.Request, dealershipID, sourceURL, cacheKey string) bool {
	if !s.isCacheOnly(sourceURL) {
		return false
	}
	cached, err := s.cachedInventoryFor(r.Context(), sourceURL)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, model.ErrorResponse("CACHE_NOT_FOUND", "url is local-sync only but no synced inventory exists yet"))
		return true
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("CACHE_READ_FAILED", err.Error()))
		return true
	}
	// The new result reuses cacheKey, and idempotency_key is uniquely indexed, so
	// release the key from whatever stale row still holds it — otherwise the
	// upsert (which conflict-resolves on result_id) trips the unique constraint.
	if err := s.store.DeleteIdempotency(r.Context(), cacheKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("CACHE_INVALIDATION_FAILED", err.Error()))
		return true
	}
	result := resultFromCache(uuid.NewString(), dealershipID, sourceURL, cacheKey, cached)
	if err := s.store.UpsertResult(r.Context(), result); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return true
	}
	writeJSON(w, http.StatusOK, resultResponse(result))
	return true
}

// shouldServeFromCache is the hybrid-mode gate: a URL is cache-first when it is
// statically configured (CACHE_ONLY_URLS) or its domain was flagged as
// bot-protected. Flags apply to the whole domain: once any URL on a host is
// DataDome-labeled, every URL on that host skips live scraping.
func (s *Server) shouldServeFromCache(ctx context.Context, rawURL string) bool {
	if s.isCacheOnly(rawURL) {
		return true
	}
	protected, err := s.store.IsProtectedURL(ctx, rawURL)
	if err != nil {
		s.logger.Warn("protected url lookup failed", zap.String("url", rawURL), zap.Error(err))
		return false
	}
	if protected {
		return true
	}
	host := hostnameOf(rawURL)
	if host == "" {
		return false
	}
	list, err := s.store.ListProtectedURLs(ctx)
	if err != nil {
		return false
	}
	for _, p := range list {
		if hostnameOf(p.SourceURL) == host {
			return true
		}
	}
	return false
}

func hostnameOf(rawURL string) string {
	return store.HostOf(rawURL)
}

// cachedInventoryFor looks up synced cache for a URL. The cache is URL-driven,
// not account-driven: an exact-URL miss falls back to the freshest cache entry
// on the same host, so www/non-www, http/https, and alternate inventory paths
// registered by different accounts all hit the same domain-wide cache.
func (s *Server) cachedInventoryFor(ctx context.Context, rawURL string) (store.CachedInventory, error) {
	cached, err := s.store.GetCachedInventory(ctx, rawURL)
	if err == nil {
		return cached, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.CachedInventory{}, err
	}
	if cached, herr := s.store.GetCachedInventoryByHost(ctx, hostnameOf(rawURL)); herr == nil {
		s.logger.Info("cache served via host fallback (hybrid)",
			zap.String("requestedUrl", rawURL), zap.String("cachedUrl", cached.SourceURL))
		return cached, nil
	}
	return store.CachedInventory{}, err
}

// isBotProtectionFailure reports whether scrape errors indicate the host is
// bot-blocked rather than a scraping/parsing problem.
func isBotProtectionFailure(errs []model.StructuredError) bool {
	return scrape.IsBotProtectionFailure(errs)
}

// flagProtected records that this URL cannot be live-scraped from this host, so
// future scrapes serve the synced cache until a local scraper pushes fresh data.
func (s *Server) flagProtected(ctx context.Context, sourceURL, reason string) {
	if err := s.store.FlagProtectedURL(ctx, store.ProtectedURL{SourceURL: sourceURL, Reason: reason}); err != nil {
		s.logger.Warn("flag protected url failed", zap.String("url", sourceURL), zap.Error(err))
		return
	}
	s.logger.Info("url flagged as bot-protected; serving from cache until synced",
		zap.String("url", sourceURL), zap.String("reason", tailOf(reason, 200)))
}

func (s *Server) unflagProtected(ctx context.Context, sourceURL string) {
	if err := s.store.UnflagProtectedURL(ctx, sourceURL); err == nil {
		s.logger.Info("url unflagged after successful live scrape", zap.String("url", sourceURL))
	}
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func (s *Server) handleScrapeRun(w http.ResponseWriter, r *http.Request) {
	var req ScrapeRunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if !validURL(req.URL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "valid url is required"))
		return
	}

	// Hybrid mode: cache-first URLs (statically configured or auto-flagged as
	// bot-protected on this host) are served from the synced cache.
	forceLive := req.Options != nil && req.Options.ForceLive != nil && *req.Options.ForceLive
	if !forceLive && s.shouldServeFromCache(r.Context(), req.URL) {
		cached, err := s.cachedInventoryFor(r.Context(), req.URL)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":    "ok",
				"url":       req.URL,
				"source":    "cache",
				"itemCount": len(cached.Items),
				"items":     cached.Items,
				"updatedAt": cached.UpdatedAt,
			})
			return
		}
		if !errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("CACHE_READ_FAILED", err.Error()))
			return
		}
		// Statically cache-only URLs never live-scrape; auto-flagged URLs fall
		// through to a live attempt when no cache exists yet (nothing to lose).
		if s.isCacheOnly(req.URL) {
			writeJSON(w, http.StatusNotFound, model.ErrorResponse("CACHE_NOT_FOUND", "url is cache-only but no synced inventory exists yet"))
			return
		}
	}

	dealershipID := req.DealershipID
	if dealershipID == "" {
		if u, err := url.Parse(req.URL); err == nil {
			dealershipID = u.Hostname()
		} else {
			dealershipID = req.URL
		}
	}

	timeout := s.cfg.DefaultRunTimeout()
	if req.TimeoutSec > 0 {
		timeout = time.Duration(req.TimeoutSec) * time.Second
	}
	if req.Options != nil && req.Options.RunTimeoutSec > 0 {
		timeout = time.Duration(req.Options.RunTimeoutSec) * time.Second
	}

	onceReq := ScrapeOnceRequest{
		DealershipID: dealershipID,
		SourceURL:    req.URL,
		Options:      req.Options,
	}
	site, err := s.resolveSiteConfig(r.Context(), onceReq)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("SITE_CONFIG_NOT_FOUND", err.Error()))
		return
	}
	site = s.applyCrawlLimits(site, req.Options)

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	scrapeOpts := scrape.Options{
		DealershipID: dealershipID,
		SourceURL:    req.URL,
		Cookies:      req.Cookies,
	}
	if req.Options != nil && strings.TrimSpace(req.Options.BrowserStrategy) != "" {
		scrapeOpts.BrowserStrategy = req.Options.BrowserStrategy
	}

	result := s.scraper.ScrapeOnceWithOptions(ctx, req.URL, site, scrapeOpts)

	// Hybrid mode: a failed live scrape never returns empty when a synced cache
	// exists. Bot-blocked failures additionally flag the URL for cache-first.
	if len(result.Items) == 0 {
		if isBotProtectionFailure(result.Errors) {
			s.flagProtected(r.Context(), req.URL, firstErrorMessage(result.Errors))
		}
		if cached, cerr := s.cachedInventoryFor(r.Context(), req.URL); cerr == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":    "ok",
				"url":       req.URL,
				"source":    "cache_fallback",
				"itemCount": len(cached.Items),
				"items":     cached.Items,
				"updatedAt": cached.UpdatedAt,
				"errors":    result.Errors,
			})
			return
		}
	} else if len(result.Items) > 0 && !s.isCacheOnly(req.URL) {
		s.unflagProtected(r.Context(), req.URL)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"url":       req.URL,
		"itemCount": len(result.Items),
		"items":     result.Items,
		"errors":    result.Errors,
	})
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
		// Exercise both browser engines across automatic retries. A site broken in
		// one renderer can recover without operator intervention over weekends.
		if attempt%2 == 0 {
			scrapeOpts.BrowserStrategy = "playwright_first"
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
		if len(result.Items) == 0 {
			record.Status = model.RunStatusFailed
			record.FailedItems = 1
			record.FailureReason = firstErrorMessage(result.Errors)
		} else if len(result.Errors) > 0 {
			record.Status = model.RunStatusPartial
		}
		record.LastError = firstErrorMessage(result.Errors)
		if record.Status == model.RunStatusPartial {
			record.ProgressStage = "completed_with_errors"
		} else if record.Status == model.RunStatusFailed {
			record.ProgressStage = "failed"
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

	// Scrape-once endpoints are live-only: never replace a failed live result
	// with previously cached products.
	bgCtx := context.Background()
	if len(finalRecord.Items) > 0 && !s.isCacheOnly(sourceURL) {
		s.unflagProtected(bgCtx, sourceURL)
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

// resultFromCache builds a completed scrape result from synced cached inventory.
func resultFromCache(resultID, dealershipID, sourceURL, idempotencyKey string, cached store.CachedInventory) model.ScrapeResult {
	now := time.Now().UTC()
	count := model.ScrapedInventoryCount(cached.Items)
	return model.ScrapeResult{
		ResultID:       resultID,
		DealershipID:   dealershipID,
		SourceURL:      sourceURL,
		Status:         model.RunStatusSuccess,
		StartedAt:      now,
		FinishedAt:     now,
		TotalItems:     count,
		SuccessItems:   count,
		AttemptCount:   1,
		IdempotencyKey: idempotencyKey,
		Items:          cached.Items,
		ProgressStage:  "completed_from_cache",
	}
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
		message := strings.ToLower(e.Message)
		if strings.Contains(message, "blocked redirect to local host") || strings.Contains(message, "blocked redirect to private host") {
			return false
		}
	}
	// Empty output is never a successful scrape. Retry render failures, selector
	// misses, bot challenges, and empty extraction with the alternate renderer.
	return true
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
	if err := s.store.ClearCachedInventory(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "results and product cache cleared"})
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

const scrapeOnceCacheTTL = 24 * time.Hour

func scrapeOnceCacheKey(dealershipID, sourceURL string) string {
	return fmt.Sprintf("scrape-once-cache|%s|%s", strings.ToLower(strings.TrimSpace(dealershipID)), normalizeSourceURL(canonicalSourceURL(dealershipID, sourceURL)))
}

func isFreshScrapeCache(result model.ScrapeResult, now time.Time) bool {
	if result.Status != model.RunStatusSuccess || len(result.Items) == 0 {
		return false
	}
	completedAt := result.FinishedAt
	if completedAt.IsZero() {
		completedAt = result.StartedAt
	}
	if completedAt.IsZero() || now.Before(completedAt) {
		return false
	}
	return now.Sub(completedAt) < scrapeOnceCacheTTL
}

func isFreshScrapeCacheForSite(result model.ScrapeResult, site config.SiteConfig, now time.Time) bool {
	if !isFreshScrapeCache(result, now) {
		return false
	}
	// A configured stock selector means the dealer publishes a real stock
	// number. Do not let an older VIN-fallback result mask a repaired template.
	if strings.TrimSpace(site.DetailPage.StockSelector) == "" && strings.TrimSpace(site.ListPage.StockSelector) == "" {
		return true
	}
	for _, item := range result.Items {
		stock := strings.TrimSpace(item.StockID)
		if stock == "" {
			stock = strings.TrimSpace(item.Stock)
		}
		vin := strings.TrimSpace(item.VIN)
		if stock == "" || (vin != "" && strings.EqualFold(stock, vin)) {
			return false
		}
	}
	return true
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

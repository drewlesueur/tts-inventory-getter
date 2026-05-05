package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/drewlesueur/tts-inventory-getter/internal/auth"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/discovery"
	"github.com/drewlesueur/tts-inventory-getter/internal/metrics"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
	"github.com/drewlesueur/tts-inventory-getter/internal/sites"
	"github.com/drewlesueur/tts-inventory-getter/internal/store"
)

type Server struct {
	cfg      config.Config
	logger   *zap.Logger
	scraper  scrape.Service
	sites    config.Loader
	store    store.ResultStore
	metrics  *metrics.Metrics
	discover *discovery.Client
}

func NewServer(cfg config.Config, logger *zap.Logger, scraper scrape.Service, sites config.Loader, st store.ResultStore, mt *metrics.Metrics, discover *discovery.Client) *Server {
	return &Server{cfg: cfg, logger: logger, scraper: scraper, sites: sites, store: st, metrics: mt, discover: discover}
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
	v1.HandleFunc("POST /v1/scrape/discover-flow", s.handleDiscover)

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

func (s *Server) handleScrapeOnce(w http.ResponseWriter, r *http.Request) {
	var req ScrapeOnceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if req.DealershipID == "" || !validURL(req.SourceURL) {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "dealershipId and valid sourceUrl are required"))
		return
	}
	if existing, err := s.store.FindByIdempotency(r.Context(), req.IdempotencyKey); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "resultId": existing.ResultID, "result": existing})
		return
	}

	site, err := s.resolveSiteConfig(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("SITE_CONFIG_NOT_FOUND", err.Error()))
		return
	}

	resultID := uuid.NewString()
	started := time.Now().UTC()
	resultRecord := model.ScrapeResult{ResultID: resultID, DealershipID: req.DealershipID, SourceURL: req.SourceURL, Status: model.RunStatusRunning, StartedAt: started, IdempotencyKey: req.IdempotencyKey}
	if err := s.store.UpsertResult(r.Context(), resultRecord); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}

	timeout := s.cfg.DefaultRunTimeout()
	if req.Options != nil && req.Options.RunTimeoutSec > 0 {
		timeout = time.Duration(req.Options.RunTimeoutSec) * time.Second
	}

	go s.runScrapeAsync(resultID, req.DealershipID, req.SourceURL, req.IdempotencyKey, site, timeout)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "resultId": resultID})
}

func (s *Server) runScrapeAsync(resultID, dealershipID, sourceURL, idempotencyKey string, site config.SiteConfig, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	result := s.scraper.ScrapeOnce(ctx, sourceURL, site)
	dur := time.Since(start).Seconds()

	record := model.ScrapeResult{
		ResultID:       resultID,
		DealershipID:   dealershipID,
		SourceURL:      sourceURL,
		Status:         model.RunStatusSuccess,
		StartedAt:      start.UTC(),
		FinishedAt:     time.Now().UTC(),
		TotalItems:     len(result.Items),
		SuccessItems:   len(result.Items),
		FailedItems:    0,
		ErrorCount:     len(result.Errors),
		IdempotencyKey: idempotencyKey,
		Items:          result.Items,
		Errors:         result.Errors,
	}
	if len(result.Errors) > 0 && len(result.Items) > 0 {
		record.Status = model.RunStatusPartial
	}
	if len(result.Items) == 0 {
		record.Status = model.RunStatusFailed
		record.FailureReason = "no inventory extracted"
	}

	if err := s.store.UpsertResult(context.Background(), record); err != nil {
		s.logger.Error("result store upsert failed", zap.String("resultId", resultID), zap.Error(err))
		return
	}
	s.metrics.RunsTotal.WithLabelValues(string(record.Status)).Inc()
	s.metrics.RunDuration.WithLabelValues(dealershipID).Observe(dur)
	s.metrics.ItemsScraped.WithLabelValues(dealershipID).Add(float64(len(result.Items)))
	s.logger.Info("scrape completed", zap.String("resultId", resultID), zap.Int("items", len(result.Items)), zap.Int("errors", len(result.Errors)))
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
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": result})
}

func (s *Server) handleClearResults(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearResults(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "results cleared"})
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

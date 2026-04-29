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

	"github.com/example/inventory-scraper/internal/auth"
	"github.com/example/inventory-scraper/internal/config"
	"github.com/example/inventory-scraper/internal/discovery"
	"github.com/example/inventory-scraper/internal/metrics"
	"github.com/example/inventory-scraper/internal/model"
	"github.com/example/inventory-scraper/internal/scrape"
	"github.com/example/inventory-scraper/internal/store"
)

type Server struct {
	cfg      config.Config
	logger   *zap.Logger
	scraper  scrape.Service
	sites    config.Loader
	store    store.RunStore
	metrics  *metrics.Metrics
	discover *discovery.Client
}

func NewServer(cfg config.Config, logger *zap.Logger, scraper scrape.Service, sites config.Loader, st store.RunStore, mt *metrics.Metrics, discover *discovery.Client) *Server {
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
	v1.HandleFunc("GET /v1/runs/", s.handleGetRun)
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
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "runId": existing.RunID, "summary": existing, "items": []any{}, "errors": []any{}})
		return
	}

	site, err := s.resolveSiteConfig(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("SITE_CONFIG_NOT_FOUND", err.Error()))
		return
	}

	runID := uuid.NewString()
	started := time.Now().UTC()
	summary := model.RunSummary{RunID: runID, DealershipID: req.DealershipID, SourceURL: req.SourceURL, Status: model.RunStatusRunning, StartedAt: started, IdempotencyKey: req.IdempotencyKey}
	_ = s.store.UpsertRun(r.Context(), summary)

	timeout := s.cfg.DefaultRunTimeout()
	if req.Options != nil && req.Options.RunTimeoutSec > 0 {
		timeout = time.Duration(req.Options.RunTimeoutSec) * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	start := time.Now()
	result := s.scraper.ScrapeOnceRaw(ctx, req.SourceURL, site)
	dur := time.Since(start).Seconds()

	summary.FinishedAt = time.Now().UTC()
	summary.TotalItems = len(result.Items)
	summary.SuccessItems = len(result.Items)
	summary.FailedItems = 0
	summary.ErrorCount = len(result.Errors)
	summary.Status = model.RunStatusSuccess
	if len(result.Errors) > 0 && len(result.Items) > 0 {
		summary.Status = model.RunStatusPartial
	}
	if len(result.Items) == 0 {
		summary.Status = model.RunStatusFailed
		summary.FailureReason = "no inventory extracted"
	}
	_ = s.store.UpsertRun(r.Context(), summary)

	s.metrics.RunsTotal.WithLabelValues(string(summary.Status)).Inc()
	s.metrics.RunDuration.WithLabelValues(req.DealershipID).Observe(dur)
	s.metrics.ItemsScraped.WithLabelValues(req.DealershipID).Add(float64(len(result.Items)))

	s.logger.Info("scrape completed", zap.String("runId", runID), zap.Int("items", len(result.Items)), zap.Int("errors", len(result.Errors)))
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "runId": runID, "summary": summary, "items": result.Items, "errors": result.Errors})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	if runID == "" || strings.Contains(runID, "/") {
		writeJSON(w, http.StatusBadRequest, model.ErrorResponse("INVALID_REQUEST", "runId is required"))
		return
	}
	run, err := s.store.GetRun(r.Context(), runID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, model.ErrorResponse("RUN_NOT_FOUND", "run not found"))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.ErrorResponse("STORE_ERROR", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "run": run})
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

func (s *Server) resolveSiteConfig(req ScrapeOnceRequest) (config.SiteConfig, error) {
	if req.SiteConfig != nil {
		return *req.SiteConfig, nil
	}
	return s.sites.LoadByName(req.DealershipID)
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

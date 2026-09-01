package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
	"github.com/drewlesueur/tts-inventory-getter/internal/sites"
	"github.com/drewlesueur/tts-inventory-getter/internal/store"
)

// blockedFetcher simulates a bot-protection wall (DataDome) on every fetch.
type blockedFetcher struct{}

func (blockedFetcher) Fetch(_ context.Context, _ string) (string, error) {
	return "", errors.New("[curl_cffi] blocked/error status=403 — datadome challenge still present after all bypass strategies")
}

func TestJJSAdobeScrapeOnceEndpointsAlwaysUseSyncedCache(t *testing.T) {
	const sourceURL = "https://www.jjsadobeauto.com/cars-for-sale"
	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 30}
	st := store.NewMemoryResultStore()
	if err := st.UpsertCachedInventory(context.Background(), store.CachedInventory{
		SourceURL: sourceURL,
		Items: []model.InventoryItem{
			{Title: "2021 Chevrolet Silverado 1500", StockID: "127974184"},
			{Title: "2023 Chevrolet Malibu RS", StockID: "127698256"},
		},
		// A stale synced cache must still be served; only local sync refreshes it.
		UpdatedAt: time.Now().UTC().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	server := NewServer(cfg, zap.NewNop(), scrape.Service{Fetcher: blockedFetcher{}}, config.NewLoader(t.TempDir()), st, testMetrics(), nil, nil, nil)
	router := server.Router()
	for _, path := range []string{"/v1/scrape/once", "/v1/scrape/once-and-result"} {
		body, _ := json.Marshal(map[string]any{
			"dealershipId": "jjs-adobe",
			"sourceUrl":    "https://jjsadobeauto.com/cars-for-sale/",
		})
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Service-Key", "k")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected cached 200, got %d body=%s", path, w.Code, w.Body.String())
		}
		if !bytes.Contains(w.Body.Bytes(), []byte(`"progressStage":"completed_from_cache"`)) ||
			!bytes.Contains(w.Body.Bytes(), []byte(`"totalItems":2`)) {
			t.Fatalf("%s: expected two cached items, body=%s", path, w.Body.String())
		}
	}
}

func TestJJSAdobeScrapeOnceUsesFreshResultCacheBeforeSyncedCache(t *testing.T) {
	const sourceURL = "https://www.jjsadobeauto.com/cars-for-sale"
	const dealerID = "jjs-adobe"
	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 30}
	st := store.NewMemoryResultStore()
	if err := st.UpsertResult(context.Background(), model.ScrapeResult{
		ResultID:       "existing-jjs-result",
		DealershipID:   dealerID,
		SourceURL:      sourceURL,
		Status:         model.RunStatusSuccess,
		StartedAt:      time.Now().UTC().Add(-time.Hour),
		FinishedAt:     time.Now().UTC().Add(-time.Hour),
		IdempotencyKey: scrapeOnceCacheKey(dealerID, sourceURL),
		Items:          []model.InventoryItem{{Title: "Cached JJS truck", StockID: "J1"}},
	}); err != nil {
		t.Fatalf("seed result cache: %v", err)
	}

	server := NewServer(cfg, zap.NewNop(), scrape.Service{Fetcher: blockedFetcher{}}, config.NewLoader(t.TempDir()), st, testMetrics(), nil, nil, nil)
	body, _ := json.Marshal(map[string]any{"dealershipId": dealerID, "sourceUrl": sourceURL})
	req := httptest.NewRequest(http.MethodPost, "/v1/scrape/once", bytes.NewReader(body))
	req.Header.Set("X-Service-Key", "k")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("existing-jjs-result")) {
		t.Fatalf("expected existing 24-hour result cache, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHybridScrapeRunFallsBackToCacheAndFlags(t *testing.T) {
	const sourceURL = "https://www.blocked-dealer.test/cars-for-sale"
	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 30}
	loader := config.NewLoader(t.TempDir())
	key := sites.CacheKeyForSourceURL(sourceURL)
	if err := loader.SaveByName(key, config.SiteConfig{
		Name: key, BaseURL: sourceURL,
		ListPage: config.ListPageConfig{CardSelector: ".vehicle-card"},
	}); err != nil {
		t.Fatalf("seed site config: %v", err)
	}

	st := store.NewMemoryResultStore()
	staleAt := time.Now().UTC().Add(-24 * time.Hour)
	if err := st.UpsertCachedInventory(context.Background(), store.CachedInventory{
		SourceURL: sourceURL,
		Items: []model.InventoryItem{
			{Title: "2020 Audi A4", StockID: "S1", VIN: "WAUENAF40LN000001"},
			{Title: "2021 BMW 330i", StockID: "S2", VIN: "3MW5R1J00M8000002"},
		},
		UpdatedAt: staleAt,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	svc := scrape.Service{Fetcher: blockedFetcher{}, Extractors: []scrape.Extractor{scrape.DOMExtractor{}}, Concurrency: 1}
	server := NewServer(cfg, zap.NewNop(), svc, loader, st, testMetrics(), nil, nil, nil)
	r := server.Router()

	runScrape := func() map[string]any {
		body, _ := json.Marshal(map[string]any{"url": sourceURL, "timeoutSec": 10})
		req := httptest.NewRequest(http.MethodPost, "/v1/scrape/run", bytes.NewReader(body))
		req.Header.Set("X-Service-Key", "k")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 got %d body=%s", w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("bad response json: %v", err)
		}
		return resp
	}

	// 1st call: live scrape fails bot-blocked -> served from cache, URL flagged.
	resp := runScrape()
	if resp["source"] != "cache_fallback" {
		t.Fatalf("expected cache_fallback source, got %v (resp=%v)", resp["source"], resp)
	}
	if int(resp["itemCount"].(float64)) != 2 {
		t.Fatalf("expected 2 cached items got %v", resp["itemCount"])
	}
	if flagged, _ := st.IsProtectedURL(context.Background(), sourceURL); !flagged {
		t.Fatalf("expected url flagged as protected after bot-blocked scrape")
	}

	// 2nd call: flagged -> cache served directly without a live attempt.
	resp = runScrape()
	if resp["source"] != "cache" {
		t.Fatalf("expected cache source on flagged url, got %v", resp["source"])
	}

	// Pending-sync lists the flagged url because its cache is 24h old.
	req := httptest.NewRequest(http.MethodGet, "/v1/scrape/pending-sync?maxAgeHours=12", nil)
	req.Header.Set("X-Service-Key", "k")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pending-sync expected 200 got %d body=%s", w.Code, w.Body.String())
	}
	var pendingResp struct {
		Pending []struct {
			URL      string `json:"url"`
			HasCache bool   `json:"hasCache"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pendingResp); err != nil {
		t.Fatalf("bad pending json: %v", err)
	}
	if len(pendingResp.Pending) != 1 || pendingResp.Pending[0].URL != sourceURL || !pendingResp.Pending[0].HasCache {
		t.Fatalf("expected 1 pending entry for %s with cache, got %+v", sourceURL, pendingResp.Pending)
	}

	// Simulate an unflagged cloud URL. A cache-only hybrid sync must flag it
	// again so /v1/scrape/run routes directly to the fresh cache.
	if err := st.UnflagProtectedURL(context.Background(), sourceURL); err != nil {
		t.Fatalf("unflag protected url: %v", err)
	}

	// Fresh sync via /v1/scrape/sync flags it and clears it from pending.
	syncBody, _ := json.Marshal(map[string]any{
		"url":        sourceURL,
		"skipUpsert": true,
		"items": []map[string]any{
			{"title": "2020 Audi A4", "stockId": "S1"},
			{"title": "2021 BMW 330i", "stockId": "S2"},
			{"title": "2019 Honda Civic", "stockId": "S3"},
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/scrape/sync", bytes.NewReader(syncBody))
	req.Header.Set("X-Service-Key", "k")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sync expected 200 got %d body=%s", w.Code, w.Body.String())
	}
	var syncResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &syncResp); err != nil {
		t.Fatalf("bad sync response json: %v", err)
	}
	if syncResp["accountId"] != "" || syncResp["dealershipId"] != "" || syncResp["upserted"] != false {
		t.Fatalf("cache-only sync must remain URL-scoped, got %+v", syncResp)
	}
	if flagged, _ := st.IsProtectedURL(context.Background(), sourceURL); !flagged {
		t.Fatal("cache-only sync must flag URL for cache-first scrape routing")
	}

	// The asynchronous scrape path used by /scrapeInventoryPage must use the
	// same protected-domain cache routing as /v1/scrape/run.
	onceBody, _ := json.Marshal(map[string]any{
		"dealershipId": "any-dealer",
		"sourceUrl":    sourceURL,
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/scrape/once", bytes.NewReader(onceBody))
	req.Header.Set("X-Service-Key", "k")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("protected scrape-once must return cache immediately, got %d body=%s", w.Code, w.Body.String())
	}
	var onceResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &onceResp); err != nil {
		t.Fatalf("bad scrape-once cache response: %v", err)
	}
	if onceResp["status"] != "ok" || onceResp["resultStatus"] != "success" || int(onceResp["totalItems"].(float64)) != 3 {
		t.Fatalf("expected completed 3-item scrape-once cache response, got %+v", onceResp)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/scrape/pending-sync?maxAgeHours=12", nil)
	req.Header.Set("X-Service-Key", "k")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	pendingResp.Pending = nil
	_ = json.Unmarshal(w.Body.Bytes(), &pendingResp)
	if len(pendingResp.Pending) != 0 {
		t.Fatalf("expected no pending after fresh sync, got %+v", pendingResp.Pending)
	}

	// And the flagged url now serves the fresh 3-item cache.
	resp = runScrape()
	if resp["source"] != "cache" || int(resp["itemCount"].(float64)) != 3 {
		t.Fatalf("expected 3-item cache serve after sync, got source=%v count=%v", resp["source"], resp["itemCount"])
	}
}

// The cache is URL-driven, not account-driven: a URL variant (non-www, http,
// different inventory path) registered by another account must hit the same
// domain-wide cache instead of live-scraping a bot-protected host.
func TestHybridCacheServesURLVariantsOnSameHost(t *testing.T) {
	const cachedURL = "https://www.blocked-dealer.test/cars-for-sale"
	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 30}
	loader := config.NewLoader(t.TempDir())

	st := store.NewMemoryResultStore()
	if err := st.UpsertCachedInventory(context.Background(), store.CachedInventory{
		SourceURL: cachedURL,
		Items: []model.InventoryItem{
			{Title: "2020 Audi A4", StockID: "S1", VIN: "WAUENAF40LN000001"},
			{Title: "2021 BMW 330i", StockID: "S2", VIN: "3MW5R1J00M8000002"},
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := st.FlagProtectedURL(context.Background(), store.ProtectedURL{SourceURL: cachedURL, Reason: "datadome"}); err != nil {
		t.Fatalf("flag protected: %v", err)
	}

	svc := scrape.Service{Fetcher: blockedFetcher{}, Extractors: []scrape.Extractor{scrape.DOMExtractor{}}, Concurrency: 1}
	server := NewServer(cfg, zap.NewNop(), svc, loader, st, testMetrics(), nil, nil, nil)
	r := server.Router()

	variants := []string{
		"https://blocked-dealer.test/cars-for-sale",      // no www
		"http://www.blocked-dealer.test/cars-for-sale",   // http
		"https://www.blocked-dealer.test/cars-for-sale/", // trailing slash
		"https://blocked-dealer.test/inventory",          // different path
	}
	for _, variant := range variants {
		body, _ := json.Marshal(map[string]any{"url": variant, "timeoutSec": 10})
		req := httptest.NewRequest(http.MethodPost, "/v1/scrape/run", bytes.NewReader(body))
		req.Header.Set("X-Service-Key", "k")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200 got %d body=%s", variant, w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: bad response json: %v", variant, err)
		}
		if resp["source"] != "cache" {
			t.Fatalf("%s: expected cache source, got %v (resp=%v)", variant, resp["source"], resp)
		}
		if int(resp["itemCount"].(float64)) != 2 {
			t.Fatalf("%s: expected 2 cached items got %v", variant, resp["itemCount"])
		}
	}
}

// A cache-only URL whose idempotency key is still held by an older, non-fresh
// result row must not blow up: idempotency_key is uniquely indexed while
// UpsertResult conflict-resolves on result_id, so serving from cache with a new
// result id used to fail with "UNIQUE constraint failed" (HTTP 500). TapToSign
// surfaced that as a 502 on /scrapeInventoryPage.
func TestScrapeOnceFromCacheReplacesStaleIdempotencyRow(t *testing.T) {
	const sourceURL = "https://www.jjsadobeauto.com/cars-for-sale"
	const dealerID = "test_dea_test_11223"
	ctx := context.Background()

	st, err := store.NewSQLiteResultStore(filepath.Join(t.TempDir(), "results.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	// An earlier live scrape failed and left the cache key on its row.
	if err := st.UpsertResult(ctx, model.ScrapeResult{
		ResultID:       "stale-failed-result",
		DealershipID:   dealerID,
		SourceURL:      sourceURL,
		Status:         model.RunStatusPartial,
		StartedAt:      time.Now().UTC().Add(-2 * time.Hour),
		FinishedAt:     time.Now().UTC().Add(-2 * time.Hour),
		IdempotencyKey: scrapeOnceCacheKey(dealerID, sourceURL),
		ProgressStage:  "completed_with_errors",
	}); err != nil {
		t.Fatalf("seed stale result: %v", err)
	}
	if err := st.UpsertCachedInventory(ctx, store.CachedInventory{
		SourceURL: sourceURL,
		Items: []model.InventoryItem{
			{Title: "2017 Buick Encore Preferred", StockID: "124310070"},
			{Title: "2021 Chevrolet Silverado 1500", StockID: "127974184"},
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 30}
	router := NewServer(cfg, zap.NewNop(), scrape.Service{Fetcher: blockedFetcher{}}, config.NewLoader(t.TempDir()), st, testMetrics(), nil, nil, nil).Router()

	// Repeat the call: every request mints a new result id against the same key.
	for attempt := 1; attempt <= 2; attempt++ {
		body, _ := json.Marshal(map[string]any{"dealershipId": dealerID, "sourceUrl": sourceURL})
		req := httptest.NewRequest(http.MethodPost, "/v1/scrape/once", bytes.NewReader(body))
		req.Header.Set("X-Service-Key", "k")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d body=%s", attempt, w.Code, w.Body.String())
		}
		if !bytes.Contains(w.Body.Bytes(), []byte(`"progressStage":"completed_from_cache"`)) ||
			!bytes.Contains(w.Body.Bytes(), []byte(`"totalItems":2`)) {
			t.Fatalf("attempt %d: expected 2 cached items, body=%s", attempt, w.Body.String())
		}
	}
}

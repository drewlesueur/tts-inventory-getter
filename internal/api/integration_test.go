package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

type fixtureFetcher struct {
	list string
	d1   string
	d2   string
}

func TestSiteConfigCacheDeleteBySourceURL(t *testing.T) {
	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 60}
	dir := t.TempDir()
	loader := config.NewLoader(dir)
	key := sites.CacheKeyForSourceURL("https://www.idealcarsaz.com/used-cars-in-mesa-az/")
	if err := loader.SaveByName(key, config.SiteConfig{
		Name:    key,
		BaseURL: "https://www.idealcarsaz.com/used-cars-in-mesa-az/",
		ListPage: config.ListPageConfig{
			CardSelector: ".vehicle-card",
		},
	}); err != nil {
		t.Fatalf("seed cache save failed: %v", err)
	}

	server := NewServer(cfg, zap.NewNop(), scrape.Service{}, loader, store.NewMemoryResultStore(), testMetrics(), nil)
	r := server.Router()

	req := httptest.NewRequest(http.MethodDelete, "/v1/site-config-cache?sourceUrl=https://www.idealcarsaz.com/used-cars-in-mesa-az/", nil)
	req.Header.Set("X-Service-Key", "k")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", w.Code, w.Body.String())
	}

	cachePath := filepath.Join(dir, "urlkey_dXJsOjp3d3cuaWRlYWxjYXJzYXouY29tL3VzZWQtY2Fycy1pbi1tZXNhLWF6.yaml")
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("expected cache file deleted, stat err=%v", err)
	}
}

func TestSiteConfigCacheClearAll(t *testing.T) {
	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 60}
	dir := t.TempDir()
	loader := config.NewLoader(dir)
	_ = loader.SaveByName("url::a.test/inventory", config.SiteConfig{Name: "url::a.test/inventory", BaseURL: "https://a.test/inventory", ListPage: config.ListPageConfig{CardSelector: ".vehicle-card"}})
	_ = loader.SaveByName("url::b.test/inventory", config.SiteConfig{Name: "url::b.test/inventory", BaseURL: "https://b.test/inventory", ListPage: config.ListPageConfig{CardSelector: ".vehicle-card"}})

	server := NewServer(cfg, zap.NewNop(), scrape.Service{}, loader, store.NewMemoryResultStore(), testMetrics(), nil)
	r := server.Router()

	req := httptest.NewRequest(http.MethodDelete, "/v1/site-config-cache?all=true", nil)
	req.Header.Set("X-Service-Key", "k")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", w.Code, w.Body.String())
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no cache files after clear-all, got %d", len(files))
	}
}

func (f fixtureFetcher) Fetch(_ context.Context, url string) (string, error) {
	switch {
	case url == "https://dealer.test/inventory":
		return f.list, nil
	case url == "https://dealer.test/inventory/car-1":
		return f.d1, nil
	case url == "https://dealer.test/inventory/car-2":
		return f.d2, nil
	default:
		return "", nil
	}
}

func TestScrapeOnceEndpoint(t *testing.T) {
	list, _ := os.ReadFile("../../test/fixtures/inventory_list.html")
	d1, _ := os.ReadFile("../../test/fixtures/detail_car_1.html")
	d2, _ := os.ReadFile("../../test/fixtures/detail_car_2.html")

	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 60}
	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.StockSelector = ".stock"
	site.ListPage.PriceSelector = ".price"
	site.ListPage.ImageSelector = "img"
	site.DetailPage.ImageSelectors = []string{".gallery img"}

	svc := scrape.Service{Fetcher: fixtureFetcher{list: string(list), d1: string(d1), d2: string(d2)}, DetailFetcher: scrape.HTMLDetailFetcher{Fetcher: fixtureFetcher{list: string(list), d1: string(d1), d2: string(d2)}}, Extractors: []scrape.Extractor{scrape.LoopHTMLExtractor{}, scrape.DOMExtractor{}}, Concurrency: 2}
	server := NewServer(cfg, zap.NewNop(), svc, config.Loader{}, store.NewMemoryResultStore(), testMetrics(), nil)
	r := server.Router()

	payload := map[string]any{"dealershipId": "x", "sourceUrl": "https://dealer.test/inventory", "siteConfig": site}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/scrape/once", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", "k")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("resultId")) {
		t.Fatalf("expected resultId in response: %s", w.Body.String())
	}
}

func TestScrapeOnceEndpoint_IdempotencyDoesNotReuseAcrossDifferentSourceURL(t *testing.T) {
	cfg := config.Config{ServiceKey: "k", RequestBodyLimitMB: 2, RateLimitRPS: 20, RateLimitBurst: 20, DefaultRunTimeoutSec: 60}
	mem := store.NewMemoryResultStore()
	site := config.SiteConfig{}
	svc := scrape.Service{}
	server := NewServer(cfg, zap.NewNop(), svc, config.Loader{}, mem, testMetrics(), nil)
	r := server.Router()

	err := mem.UpsertResult(context.Background(), model.ScrapeResult{
		ResultID:       "existing-result",
		DealershipID:   "test_del_test_12333",
		SourceURL:      "https://www.idealcarsaz.com/used-cars-in-mesa-az",
		Status:         model.RunStatusSuccess,
		StartedAt:      time.Now().UTC(),
		IdempotencyKey: "run-2026-05-11",
	})
	if err != nil {
		t.Fatalf("seed upsert failed: %v", err)
	}

	payload := map[string]any{
		"dealershipId":   "test_del_test_12333",
		"sourceUrl":      "https://www.txtcharlie.com/inventory/",
		"siteConfig":     site,
		"idempotencyKey": "run-2026-05-11",
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/scrape/once", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", "k")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for different sourceUrl with same idempotency key, got %d body=%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("existing-result")) {
		t.Fatalf("expected not to reuse previous result for different sourceUrl: %s", w.Body.String())
	}
}

func TestApplyCrawlLimits_UsesDefaultsAndOverrides(t *testing.T) {
	srv := NewServer(config.Config{
		DefaultMaxPages:          20,
		DefaultMaxScrollAttempts: 8,
		DefaultMaxLoadMoreClicks: 20,
		DefaultMaxItems:          200,
	}, zap.NewNop(), scrape.Service{}, config.Loader{}, store.NewMemoryResultStore(), testMetrics(), nil)

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site = srv.applyCrawlLimits(site, &ScrapeOptions{
		MaxPages:          5,
		MaxScrollAttempts: 3,
		MaxLoadMoreClicks: 9,
		MaxItems:          50,
	})

	if site.ListPage.Pagination.MaxPages != 5 {
		t.Fatalf("expected maxPages override to apply, got %d", site.ListPage.Pagination.MaxPages)
	}
	if site.ListPage.Pagination.ScrollMaxAttempts != 3 {
		t.Fatalf("expected maxScrollAttempts override to apply, got %d", site.ListPage.Pagination.ScrollMaxAttempts)
	}
	if site.ListPage.Pagination.ClickMaxAttempts != 9 {
		t.Fatalf("expected maxLoadMoreClicks override to apply, got %d", site.ListPage.Pagination.ClickMaxAttempts)
	}
	if site.ListPage.MaxItems != 50 {
		t.Fatalf("expected maxItems override to apply, got %d", site.ListPage.MaxItems)
	}
}

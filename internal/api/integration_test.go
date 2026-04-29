package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"go.uber.org/zap"

	"github.com/example/inventory-scraper/internal/config"
	"github.com/example/inventory-scraper/internal/metrics"
	"github.com/example/inventory-scraper/internal/scrape"
	"github.com/example/inventory-scraper/internal/store"
)

type fixtureFetcher struct {
	list string
	d1   string
	d2   string
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
	server := NewServer(cfg, zap.NewNop(), svc, config.Loader{}, store.NewMemoryRunStore(), metrics.New(), nil)
	r := server.Router()

	payload := map[string]any{"dealershipId": "x", "sourceUrl": "https://dealer.test/inventory", "siteConfig": site}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/scrape/once", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", "k")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("STK-001")) {
		t.Fatalf("expected stock in response: %s", w.Body.String())
	}
}

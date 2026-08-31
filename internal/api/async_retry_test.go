package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/metrics"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
	"github.com/drewlesueur/tts-inventory-getter/internal/store"
)

var (
	testMetricsOnce sync.Once
	testMetricsInst *metrics.Metrics
)

func testMetrics() *metrics.Metrics {
	testMetricsOnce.Do(func() {
		testMetricsInst = metrics.New()
	})
	return testMetricsInst
}

type seqFetcher struct {
	calls int
}

func (f *seqFetcher) Fetch(_ context.Context, _ string) (string, error) {
	f.calls++
	if f.calls == 1 {
		return "", errors.New("Get \"https://x\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)")
	}
	return `<div class="vehicle-card"><a href="/inventory/car-1">Car</a><h2>2020 Audi A4</h2><span class="stock">STK-1</span><span class="price">$100</span><img src="https://dealer.test/vehicle-1.jpg"/></div>`, nil
}

type alwaysErrFetcher struct{ err error }

func (f alwaysErrFetcher) Fetch(_ context.Context, _ string) (string, error) { return "", f.err }

type emptyThenInventoryFetcher struct{ calls int }

func (f *emptyThenInventoryFetcher) Fetch(_ context.Context, _ string) (string, error) {
	f.calls++
	if f.calls == 1 {
		return "<html><body>No vehicles rendered yet</body></html>", nil
	}
	return `<div class="vehicle-card"><a href="/inventory/car-1">Car</a><h2>2020 Audi A4</h2><span class="stock">STK-1</span><span class="price">$100</span><img src="https://dealer.test/vehicle-1.jpg"/></div>`, nil
}

func testSiteConfig() config.SiteConfig {
	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.StockSelector = ".stock"
	site.ListPage.PriceSelector = ".price"
	site.ListPage.ImageSelector = "img"
	return site
}

func TestRunScrapeAsync_RetriesThenSuccess(t *testing.T) {
	f := &seqFetcher{}
	svc := scrape.Service{Fetcher: f, Extractors: []scrape.Extractor{scrape.DOMExtractor{}}, Concurrency: 1}
	cfg := config.Config{ScrapeMaxAttempts: 3, ScrapeRetryBackoffSec: 1, DefaultRunTimeoutSec: 60}
	st := store.NewMemoryResultStore()
	s := NewServer(cfg, zap.NewNop(), svc, config.Loader{}, st, testMetrics(), nil, nil, nil)

	s.runScrapeAsync("r1", "d1", "https://dealer.test/inventory", "idem-1", testSiteConfig(), 10*time.Second, nil)

	r, err := st.GetResult(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.AttemptCount != 2 {
		t.Fatalf("expected attemptCount=2 got %d", r.AttemptCount)
	}
	if r.Status != "success" {
		t.Fatalf("expected success got %s", r.Status)
	}
	if len(r.Items) == 0 {
		t.Fatalf("expected items after retry success")
	}
}

func TestRunScrapeAsync_ExhaustsRetries(t *testing.T) {
	svc := scrape.Service{Fetcher: alwaysErrFetcher{err: errors.New("context deadline exceeded")}, Extractors: []scrape.Extractor{scrape.DOMExtractor{}}, Concurrency: 1}
	cfg := config.Config{ScrapeMaxAttempts: 3, ScrapeRetryBackoffSec: 1, DefaultRunTimeoutSec: 60}
	st := store.NewMemoryResultStore()
	s := NewServer(cfg, zap.NewNop(), svc, config.Loader{}, st, testMetrics(), nil, nil, nil)

	s.runScrapeAsync("r2", "d1", "https://dealer.test/inventory", "idem-2", testSiteConfig(), 5*time.Second, nil)

	r, err := st.GetResult(context.Background(), "r2")
	if err != nil {
		t.Fatal(err)
	}
	if r.AttemptCount != 3 {
		t.Fatalf("expected attemptCount=3 got %d", r.AttemptCount)
	}
	if r.Status != "failed" {
		t.Fatalf("expected failed got %s", r.Status)
	}
	if r.LastError == "" {
		t.Fatalf("expected lastError to be set")
	}
}

func TestRunScrapeAsync_NonRetryableFailure(t *testing.T) {
	svc := scrape.Service{Fetcher: alwaysErrFetcher{err: errors.New("blocked redirect to local host: http://127.0.0.1")}, Extractors: []scrape.Extractor{scrape.DOMExtractor{}}, Concurrency: 1}
	cfg := config.Config{ScrapeMaxAttempts: 3, ScrapeRetryBackoffSec: 1, DefaultRunTimeoutSec: 60}
	st := store.NewMemoryResultStore()
	s := NewServer(cfg, zap.NewNop(), svc, config.Loader{}, st, testMetrics(), nil, nil, nil)

	s.runScrapeAsync("r3", "d1", "https://dealer.test/inventory", "idem-3", testSiteConfig(), 5*time.Second, nil)

	r, err := st.GetResult(context.Background(), "r3")
	if err != nil {
		t.Fatal(err)
	}
	if r.AttemptCount != 1 {
		t.Fatalf("expected attemptCount=1 got %d", r.AttemptCount)
	}
	if r.Status != "failed" {
		t.Fatalf("expected failed got %s", r.Status)
	}
}

func TestRunScrapeAsync_RetriesEmptyExtractionThenRecovers(t *testing.T) {
	f := &emptyThenInventoryFetcher{}
	svc := scrape.Service{Fetcher: f, Extractors: []scrape.Extractor{scrape.DOMExtractor{}}, Concurrency: 1}
	cfg := config.Config{ScrapeMaxAttempts: 2, ScrapeRetryBackoffSec: 1, DefaultRunTimeoutSec: 60}
	st := store.NewMemoryResultStore()
	s := NewServer(cfg, zap.NewNop(), svc, config.Loader{}, st, testMetrics(), nil, nil, nil)

	s.runScrapeAsync("r4", "d1", "https://dealer.test/inventory", "idem-4", testSiteConfig(), 5*time.Second, nil)

	r, err := st.GetResult(context.Background(), "r4")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "success" || r.AttemptCount != 2 || len(r.Items) != 1 {
		t.Fatalf("expected second-attempt recovery, got status=%s attempts=%d items=%d errors=%+v", r.Status, r.AttemptCount, len(r.Items), r.Errors)
	}
}

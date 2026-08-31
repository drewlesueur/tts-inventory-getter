package scrape

import (
	"context"
	"errors"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type fallbackBrowser struct{ calls int }

func (b *fallbackBrowser) Render(_ context.Context, _ string, _ config.SiteConfig) (string, error) {
	b.calls++
	if b.calls == 1 {
		return "", errors.New("initial browser render failed")
	}
	return `<div class="vehicle-card"><a href="/inventory/car-1">Car</a><h2>2020 Audi A4</h2><span class="stock">STK-1</span><span class="price">$100</span><img src="https://dealer.test/vehicle-1.jpg"/></div>`, nil
}

type timeoutFetcher struct{}

func (timeoutFetcher) Fetch(_ context.Context, _ string) (string, error) {
	return "", errors.New("Get \"https://x\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)")
}

type staticFetcher struct{ html string }

func (f staticFetcher) Fetch(_ context.Context, _ string) (string, error) {
	return f.html, nil
}

type cookieAwareStaticFetcher struct{ html string }

func (f cookieAwareStaticFetcher) Fetch(_ context.Context, _ string) (string, error) {
	return f.html, nil
}

func (f cookieAwareStaticFetcher) FetchWithCookie(_ context.Context, _, _ string) (string, error) {
	return f.html, nil
}

type staticBrowser struct {
	html  string
	calls int
}

func (b *staticBrowser) Render(_ context.Context, _ string, _ config.SiteConfig) (string, error) {
	b.calls++
	return b.html, nil
}

type duplicateURLExtractor struct{}

func (duplicateURLExtractor) Extract(_ context.Context, _ string, _ string, _ config.SiteConfig) ([]model.InventoryItem, []model.StructuredError) {
	return []model.InventoryItem{
		{URL: "/inventory/car-1", Title: "2020 Audi A4"},
		{URL: "https://dealer.test/inventory/car-1", Title: "2020 Audi A4"},
	}, nil
}

type selectorNoiseExtractor struct{}

func (selectorNoiseExtractor) Extract(_ context.Context, _ string, _ string, _ config.SiteConfig) ([]model.InventoryItem, []model.StructuredError) {
	return []model.InventoryItem{{StockID: "NUMBER"}, {StockID: "OLDEST"}, {StockID: "NEWEST"}}, nil
}

func TestScrapeOnce_BrowserFallbackAfterHTTPTimeout(t *testing.T) {
	b := &fallbackBrowser{}
	s := Service{Browser: b, Fetcher: timeoutFetcher{}, Extractors: []Extractor{DOMExtractor{}}, Concurrency: 1}
	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.StockSelector = ".stock"
	site.ListPage.PriceSelector = ".price"
	site.ListPage.ImageSelector = "img"

	res := s.ScrapeOnce(context.Background(), "https://dealer.test/inventory", site)
	if len(res.Items) == 0 {
		t.Fatalf("expected items from browser fallback")
	}
	for _, e := range res.Errors {
		if e.Code == "SCRAPE_RENDER_FAILED" {
			t.Fatalf("expected no SCRAPE_RENDER_FAILED, got %+v", e)
		}
	}
}

func TestScrapeOnce_BrowserFallbackForUnhydratedHTTPShell(t *testing.T) {
	browser := &staticBrowser{html: `<div class="vehicle-card"><a href="/inventory/123/">Truck</a><h2>2022 Ford F-550</h2><span class="price">$45,000</span><img src="https://dealer.test/truck.webp"/></div>`}
	s := Service{
		Browser:     browser,
		Fetcher:     cookieAwareStaticFetcher{html: `<html><body><div id="app"></div></body></html>`},
		Extractors:  []Extractor{DOMExtractor{}},
		Concurrency: 1,
	}
	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.PriceSelector = ".price"
	site.ListPage.ImageSelector = "img"

	res := s.ScrapeOnce(context.Background(), "https://dealer.test/inventory/", site)
	if browser.calls != 1 {
		t.Fatalf("expected browser fallback, got %d calls", browser.calls)
	}
	if len(res.Items) != 1 || res.Items[0].Title != "2022 Ford F-550" {
		t.Fatalf("expected hydrated browser item, got %#v", res.Items)
	}
}

func TestScrapeOnce_DedupesRelativeAndAbsoluteURLs(t *testing.T) {
	s := Service{
		Fetcher:     staticFetcher{html: "<html></html>"},
		Extractors:  []Extractor{duplicateURLExtractor{}},
		Concurrency: 1,
	}
	res := s.ScrapeOnce(context.Background(), "https://dealer.test/inventory", config.SiteConfig{})
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 unique item, got %d", len(res.Items))
	}
	if res.Items[0].URL != "https://dealer.test/inventory/car-1" {
		t.Fatalf("unexpected normalized URL: %s", res.Items[0].URL)
	}
}

func TestScrapeOnce_ReportsProgressCounts(t *testing.T) {
	s := Service{
		Fetcher:     staticFetcher{html: "<html></html>"},
		Extractors:  []Extractor{duplicateURLExtractor{}},
		Concurrency: 1,
	}
	progress := make([]Progress, 0)
	res := s.ScrapeOnceWithOptions(context.Background(), "https://dealer.test/inventory", config.SiteConfig{}, Options{
		Progress: func(p Progress) {
			progress = append(progress, p)
		},
	})
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 unique item, got %d", len(res.Items))
	}
	var sawExtracted, sawCompleted bool
	for _, p := range progress {
		if p.Stage == "items_extracted" && p.TotalItems == 2 {
			sawExtracted = true
		}
		if p.Stage == "completed" && p.TotalItems == 1 {
			sawCompleted = true
		}
	}
	if !sawExtracted || !sawCompleted {
		t.Fatalf("expected extracted and completed progress counts, got %#v", progress)
	}
}

func TestScrapeOnce_FiltersSelectorNoiseThatLooksLikeStock(t *testing.T) {
	site := config.SiteConfig{}
	site.ListPage.CardSelector = "li"
	site.ListPage.StockSelector = ".stock"
	html := `<ul><li><span class="stock">NUMBER</span></li><li><span class="stock">OLDEST</span></li><li><span class="stock">NEWEST</span></li></ul>`
	svc := Service{Fetcher: staticFetcher{html: html}, Extractors: []Extractor{selectorNoiseExtractor{}}}

	res := svc.ScrapeOnce(context.Background(), "https://dealer.test/inventory", site)
	if len(res.Items) != 0 {
		t.Fatalf("expected selector noise to be rejected, got %+v", res.Items)
	}
	var invalid, noItems bool
	for _, err := range res.Errors {
		invalid = invalid || err.Code == "INVALID_ITEMS_FILTERED"
		noItems = noItems || err.Code == "NO_ITEMS"
	}
	if !invalid || !noItems {
		t.Fatalf("expected INVALID_ITEMS_FILTERED and NO_ITEMS, got %+v", res.Errors)
	}
}

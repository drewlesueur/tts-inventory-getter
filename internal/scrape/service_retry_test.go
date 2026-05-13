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

type duplicateURLExtractor struct{}

func (duplicateURLExtractor) Extract(_ context.Context, _ string, _ string, _ config.SiteConfig) ([]model.InventoryItem, []model.StructuredError) {
	return []model.InventoryItem{
		{URL: "/inventory/car-1", Title: "2020 Audi A4"},
		{URL: "https://dealer.test/inventory/car-1", Title: "2020 Audi A4"},
	}, nil
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

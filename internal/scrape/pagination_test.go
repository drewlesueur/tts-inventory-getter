package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

type pageFetcher struct {
	pages map[string]string
}

func (f pageFetcher) Fetch(_ context.Context, url string) (string, error) {
	if h, ok := f.pages[url]; ok {
		return h, nil
	}
	return "", nil
}

func TestScrapeOnce_UsesPaginationNextLinks(t *testing.T) {
	page1 := `<html><head><link rel="next" href="/used-cars-in-mesa-az/page/2"></head><body><div class="vehicle-card"><a href="/vehicle-details/car-1/">Car 1</a><h2>2020 Audi A4</h2><span class="stock">S1</span><span class="price">$10</span><img src="https://dealer.test/vehicle-1.jpg"/></div></body></html>`
	page2 := `<html><body><div class="vehicle-card"><a href="/vehicle-details/car-2/">Car 2</a><h2>2021 BMW 330i</h2><span class="stock">S2</span><span class="price">$20</span><img src="https://dealer.test/vehicle-2.jpg"/></div></body></html>`

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.StockSelector = ".stock"
	site.ListPage.PriceSelector = ".price"
	site.ListPage.ImageSelector = "img"
	site.ListPage.Pagination.MaxPages = 3
	site.ListPage.Pagination.NextSelector = "link[rel='next']"

	svc := Service{
		Fetcher:       pageFetcher{pages: map[string]string{"https://dealer.test/used-cars-in-mesa-az": page1, "https://dealer.test/used-cars-in-mesa-az/page/2": page2}},
		DetailFetcher: HTMLDetailFetcher{Fetcher: pageFetcher{}},
		Extractors:    []Extractor{DOMExtractor{}},
		Concurrency:   1,
	}

	res := svc.ScrapeOnce(context.Background(), "https://dealer.test/used-cars-in-mesa-az", site)
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items from paginated pages got %d errs=%+v", len(res.Items), res.Errors)
	}
}

func TestScrapeOnce_ReportsCardCountsWhileCollectingPages(t *testing.T) {
	page1 := `<html><head><link rel="next" href="/used-cars-in-mesa-az/page/2"></head><body><div class="vehicle-card"><a href="/vehicle-details/car-1/">Car 1</a><h2>2020 Audi A4</h2></div></body></html>`
	page2 := `<html><body><div class="vehicle-card"><a href="/vehicle-details/car-2/">Car 2</a><h2>2021 BMW 330i</h2></div><div class="vehicle-card"><a href="/vehicle-details/car-3/">Car 3</a><h2>2022 Lexus RX</h2></div></body></html>`

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.Pagination.MaxPages = 3
	site.ListPage.Pagination.NextSelector = "link[rel='next']"

	svc := Service{
		Fetcher:       pageFetcher{pages: map[string]string{"https://dealer.test/used-cars-in-mesa-az": page1, "https://dealer.test/used-cars-in-mesa-az/page/2": page2}},
		DetailFetcher: HTMLDetailFetcher{Fetcher: pageFetcher{}},
		Extractors:    []Extractor{DOMExtractor{}},
		Concurrency:   1,
	}

	progress := make([]Progress, 0)
	res := svc.ScrapeOnceWithOptions(context.Background(), "https://dealer.test/used-cars-in-mesa-az", site, Options{
		Progress: func(p Progress) {
			progress = append(progress, p)
		},
	})
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 items from paginated pages got %d errs=%+v", len(res.Items), res.Errors)
	}

	var sawFirstPage, sawSecondPage bool
	for _, p := range progress {
		if p.Stage == "pages_collected" && p.TotalItems == 1 {
			sawFirstPage = true
		}
		if p.Stage == "pages_collected" && p.TotalItems == 3 {
			sawSecondPage = true
		}
	}
	if !sawFirstPage || !sawSecondPage {
		t.Fatalf("expected page collection counts 1 and 3, got %#v", progress)
	}
}

func TestScrapeOnce_RespectsDetectedInventoryTotal(t *testing.T) {
	page1 := `<html><body>
<div class="inventory-summary">Showing 1-1 of 1 vehicles</div>
<div class="vehicle-card"><a href="/vehicle-details/car-1/">Car 1</a><h2>2020 Audi A4</h2><span class="stock">S1</span><span class="price">$10</span><img src="https://dealer.test/vehicle-1.jpg"/></div>
<a rel="next" href="/used-cars-in-mesa-az/page/2">Next</a>
</body></html>`
	page2 := `<html><body>
<div class="vehicle-card"><a href="/vehicle-details/car-2/">Car 2</a><h2>2021 BMW 330i</h2><span class="stock">S2</span><span class="price">$20</span><img src="https://dealer.test/vehicle-2.jpg"/></div>
</body></html>`

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.StockSelector = ".stock"
	site.ListPage.PriceSelector = ".price"
	site.ListPage.ImageSelector = "img"
	site.ListPage.TotalSelector = ".inventory-summary"
	site.ListPage.Pagination.MaxPages = 3

	svc := Service{
		Fetcher:       pageFetcher{pages: map[string]string{"https://dealer.test/used-cars-in-mesa-az": page1, "https://dealer.test/used-cars-in-mesa-az/page/2": page2}},
		DetailFetcher: HTMLDetailFetcher{Fetcher: pageFetcher{}},
		Extractors:    []Extractor{DOMExtractor{}},
		Concurrency:   1,
	}

	res := svc.ScrapeOnce(context.Background(), "https://dealer.test/used-cars-in-mesa-az", site)
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 item due to detected total cap got %d errs=%+v", len(res.Items), res.Errors)
	}
}

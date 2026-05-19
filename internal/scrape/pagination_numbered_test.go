package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

func TestScrapeOnce_UsesNumberedPaginationLinks(t *testing.T) {
	page1 := `<html><body>
<nav class="pagination"><a href="/used-cars-in-mesa-az/page/2">2</a></nav>
<div class="vehicle-card"><a href="/vehicle-details/car-1/">Car 1</a><h2>2020 Audi A4</h2><span class="stock">S1</span><span class="price">$10</span><img src="https://dealer.test/vehicle-1.jpg"/></div>
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
	site.ListPage.Pagination.MaxPages = 3

	svc := Service{
		Fetcher:       pageFetcher{pages: map[string]string{"https://dealer.test/used-cars-in-mesa-az": page1, "https://dealer.test/used-cars-in-mesa-az/page/2": page2}},
		DetailFetcher: HTMLDetailFetcher{Fetcher: pageFetcher{}},
		Extractors:    []Extractor{DOMExtractor{}},
		Concurrency:   1,
	}

	res := svc.ScrapeOnce(context.Background(), "https://dealer.test/used-cars-in-mesa-az", site)
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items from numbered pagination got %d errs=%+v", len(res.Items), res.Errors)
	}
}


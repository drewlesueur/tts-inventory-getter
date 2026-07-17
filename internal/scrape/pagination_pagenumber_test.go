package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// DealerCenter/carsforsale-platform sites paginate via a JS form POST; the only
// crawlable signal is "Page X of Y" text plus GET ?PageNumber=N support.
func TestScrapeOnce_SynthesizesPageNumberParamPagination(t *testing.T) {
	card := func(n, title string) string {
		return `<li class="vehicle-snapshot"><h3 class="vehicle-snapshot__title"><a href="/details/car-` + n + `/12345` + n + `">` + title + `</a></h3><div class="vehicle-snapshot__main-info-item"><div class="vehicle-snapshot__main-info">$10,000</div></div><img src="https://dealer.test/car-` + n + `.jpg"/></li>`
	}
	pagination := func(cur string) string {
		return `<ul class="inventory-pagination list-inline"><li class="inventory-pagination__numbers">Page ` + cur + ` of 3</li><li class="inventory-pagination__next"><a href="/cars-for-sale" class="data-button-next-page">Next</a></li></ul>`
	}
	page1 := `<html><body><ul>` + card("1", "2020 Audi A4") + `</ul>` + pagination("1") + `</body></html>`
	page2 := `<html><body><ul>` + card("2", "2021 BMW 330i") + `</ul>` + pagination("2") + `</body></html>`
	page3 := `<html><body><ul>` + card("3", "2019 Honda Civic") + `</ul><ul class="inventory-pagination"><li>Page 3 of 3</li></ul></body></html>`

	site := config.SiteConfig{}
	site.ListPage.CardSelector = "li.vehicle-snapshot"
	site.ListPage.TitleSelector = ".vehicle-snapshot__title"
	site.ListPage.URLSelector = "a[href*='/details/']"
	site.ListPage.PriceSelector = ".vehicle-snapshot__main-info"
	site.ListPage.ImageSelector = "img"
	site.ListPage.Pagination.MaxPages = 5

	svc := Service{
		Fetcher: pageFetcher{pages: map[string]string{
			"https://dealer.test/cars-for-sale":              page1,
			"https://dealer.test/cars-for-sale?PageNumber=2&PageSize=100": page2,
			"https://dealer.test/cars-for-sale?PageNumber=3&PageSize=100": page3,
		}},
		DetailFetcher: HTMLDetailFetcher{Fetcher: pageFetcher{}},
		Extractors:    []Extractor{DOMExtractor{}},
		Concurrency:   1,
	}

	res := svc.ScrapeOnce(context.Background(), "https://dealer.test/cars-for-sale", site)
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 items across PageNumber pages got %d errs=%+v", len(res.Items), res.Errors)
	}
}

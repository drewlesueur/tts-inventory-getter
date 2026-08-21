package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// DealerCarSearch sites paginate via a JS form POST (changePage/applyFiltersApi)
// with no crawlable hrefs. The only signals are the "Page: X of Y" pager text
// and GET ?page=N support — ?PageNumber=N is ignored and re-serves page 1.
func TestScrapeOnce_SynthesizesDealerCarSearchPageURLs(t *testing.T) {
	card := func(n, stock, title string) string {
		return `<div class="i08r-invBox">` +
			`<h4 class="i08r_vehicleTitle"><a href="/vdp/2419383` + n + `/Used-` + title + `-for-sale">` + title + `</a></h4>` +
			`<p class="i08r_optStock"><label>Stock #:</label> ` + stock + `</p>` +
			`<p class="i08r_optMileage"><label>Mileage:</label> 93,821</p>` +
			`<div class="i08r_priceWrap"><span class="price-2">$29,995.00</span></div>` +
			`<img class="i08r_mainImg" data-src="https://imagescdn.dealercarsearch.com/Media/952/car` + n + `.jpg"/>` +
			`</div>`
	}
	pager := func(cur string) string {
		return `<div class="i08r_pager update-pager">` +
			`<button onClick="changePage(this)" value="0">Prev</button>` +
			`<span class="pager-summary">Page: ` + cur + ` of 3 (3 vehicles)</span>` +
			`<button onClick="changePage(this)" value="2">Next</button></div>`
	}
	page := func(n, stock, title, cur string) string {
		return `<html><body>` + card(n, stock, title) + pager(cur) + `</body></html>`
	}

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".i08r-invBox"
	site.ListPage.TitleSelector = ".i08r_vehicleTitle"
	site.ListPage.URLSelector = "a[href*='/vdp/']"
	site.ListPage.StockSelector = ".i08r_optStock"
	site.ListPage.PriceSelector = ".i08r_priceWrap .price-2"
	site.ListPage.MileageSelector = ".i08r_optMileage"
	site.ListPage.ImageSelector = "img.i08r_mainImg"

	svc := Service{
		Fetcher: pageFetcher{pages: map[string]string{
			"https://fiuzamotors.test/newandusedcars":        page("1", "L1032599", "2022-Acura-MDX", "1"),
			"https://fiuzamotors.test/newandusedcars?page=2": page("2", "L2007522", "2012-Audi-A3", "2"),
			"https://fiuzamotors.test/newandusedcars?page=3": page("3", "L2011579A", "2014-Audi-A4", "3"),
		}},
		DetailFetcher: HTMLDetailFetcher{Fetcher: pageFetcher{}},
		Extractors:    []Extractor{DOMExtractor{}},
		Concurrency:   1,
	}

	res := svc.ScrapeOnce(context.Background(), "https://fiuzamotors.test/newandusedcars", site)
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 items across ?page=N pages got %d errs=%+v", len(res.Items), res.Errors)
	}
	// The label must be stripped from the mileage cell.
	for _, it := range res.Items {
		if it.Mileage != "93,821" {
			t.Fatalf("expected mileage 93,821 got %q", it.Mileage)
		}
	}
}

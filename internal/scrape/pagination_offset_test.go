package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// DealerFire pages with <link rel="next" href="?limit=20&offset=N">. The link
// was already found by the selector sweep but discarded by the URL filter,
// which only recognized page=/pagesize=//page/ forms.
func TestScrapeOnce_FollowsOffsetLimitNextLinks(t *testing.T) {
	card := func(n, vin string) string {
		return `<div class="inventory-item js-vehicle-item">` +
			`<h6 class="inventory-item_vehicle-title"><a class="js-vehicle-item-link" href="/used-car-` + n + `-id-` + n + `">Car ` + n + `</a></h6>` +
			`<span class="inventory-item_sub-title_item"># STK` + n + `</span>` +
			`<div class="inventory-item_vehicle-highlights"><ul><li>10,00` + n + ` mi.</li></ul></div>` +
			`<a class="js-carfax" data-vin="` + vin + `"></a>` +
			`<div class="price_value">$1` + n + `,000</div>` +
			`<img class="inventory-item_slider_img" src="https://cdn.test/` + n + `.jpg">` +
			`</div>`
	}
	base := "https://dealer.test/used-vehicles"
	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".inventory-item.js-vehicle-item"
	site.ListPage.TitleSelector = ".inventory-item_vehicle-title"
	site.ListPage.URLSelector = "a.js-vehicle-item-link"
	site.ListPage.StockSelector = ".inventory-item_sub-title_item"
	site.ListPage.PriceSelector = ".price_value"
	site.ListPage.MileageSelector = ".inventory-item_vehicle-highlights li"
	site.ListPage.ImageSelector = "img.inventory-item_slider_img"

	svc := Service{
		Fetcher: pageFetcher{pages: map[string]string{
			base: `<html><head><link rel="next" href="/used-vehicles?limit=20&offset=20"></head><body>` +
				card("1", "1FTER4HH3SLE29762") + `</body></html>`,
			base + "?limit=20&offset=20": `<html><head><link rel="next" href="/used-vehicles?limit=20&offset=40"></head><body>` +
				card("2", "5YJSA1E67TF558569") + `</body></html>`,
			// last page: no rel=next, which ends the walk
			base + "?limit=20&offset=40": `<html><body>` + card("3", "JN8AZ2KRXET352725") + `</body></html>`,
		}},
		DetailFetcher: HTMLDetailFetcher{Fetcher: pageFetcher{}},
		Extractors:    []Extractor{DOMExtractor{}},
		Concurrency:   1,
	}
	res := svc.ScrapeOnce(context.Background(), base, site)
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 items across the offset walk got %d errs=%+v", len(res.Items), res.Errors)
	}
	for _, it := range res.Items {
		if it.VIN == "" {
			t.Fatalf("VIN not read from data-vin: %+v", it)
		}
		// "# STK1" must not keep its hash.
		if it.StockID == "" || it.StockID[0] == '#' {
			t.Fatalf("stock id = %q", it.StockID)
		}
	}
}

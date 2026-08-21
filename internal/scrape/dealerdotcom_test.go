package scrape

import (
	"context"
	"strings"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

const ddcStateFixture = `<html><body>
<li class="box box-border vehicle-card vehicle-card-detailed" data-index="0">
  <h2 class="vehicle-card-title"><a href="/used/Tesla/2026-Tesla-Model-S-abc.htm"><span>2026 Tesla Model S Plaid</span></a></h2>
  <span class="price-value">$151,988</span>
</li>
<script type="text/javascript">
DDC.WS.state['ws-inv-data'] = DDC.WS.state['ws-inv-data'] || {};
DDC.WS.state['ws-inv-data']['inventory-data-bus2'] = {"WIS":{
 "pageInfo":{"totalCount":325,"pageSize":48,"pageStart":0},
 "inventory":[{
   "vin":"5YJSA1E67TF558569","stockNumber":"P558569","year":2026,
   "make":"Tesla","model":"Model S","trim":"Plaid",
   "title":["2026 Tesla","Model S Plaid"],
   "link":"/used/Tesla/2026-Tesla-Model-S-abc.htm",
   "bodyStyle":"Hatchback","fuelType":"Electric","condition":"Used",
   "images":[{"uri":"https://pictures.dealer.com/k/a.jpg"}],
   "pricing":{"retailPrice":"$151,988","dprice":[{"value":"$151,988","isFinalPrice":true}]},
   "attributes":[
     {"name":"odometer","value":"5,227 miles"},
     {"name":"engine","value":"Other"},
     {"name":"transmission","value":"Automatic"},
     {"name":"normalDriveLine","value":"AWD"},
     {"name":"exteriorColor","value":"Frost Blue"}],
   "trackingAttributes":[
     {"name":"cityFuelEconomy","value":"114.0"},
     {"name":"highwayFuelEconomy","value":"105.0"}]
 }]}};
</script>
</body></html>`

// The DDC card markup carries no VIN and little detail; everything real lives in
// the widget view model, so the blob must win over the rendered card.
func TestExpandDealerDotComInventory_UsesWidgetViewModel(t *testing.T) {
	out := expandDealerDotComInventory(context.Background(),
		"https://www.kianmotors.com/used-inventory/index.htm", ddcStateFixture)
	if out == ddcStateFixture {
		t.Fatalf("page was not expanded")
	}

	site := config.SiteConfig{}
	site.ListPage.CardSelector = "li.vehicle-card-detailed"
	items, errs := NextDataExtractor{}.Extract(context.Background(), out,
		"https://www.kianmotors.com/used-inventory/index.htm", site)
	if len(items) != 1 {
		t.Fatalf("expected 1 item got %d errs=%+v", len(items), errs)
	}
	it := items[0]
	if it.VIN != "5YJSA1E67TF558569" {
		t.Fatalf("vin = %q", it.VIN)
	}
	if it.StockID != "P558569" {
		t.Fatalf("stock = %q", it.StockID)
	}
	if it.Price != "$151,988" {
		t.Fatalf("price = %q", it.Price)
	}
	// The trailing unit is trimmed so the value matches other sites' bare figure.
	if it.Mileage != "5,227" {
		t.Fatalf("mileage = %q", it.Mileage)
	}
	if it.DriveType != "AWD" || it.Transmission != "Automatic" || it.Color != "Frost Blue" {
		t.Fatalf("detail fields wrong: drive=%q trans=%q color=%q", it.DriveType, it.Transmission, it.Color)
	}
	if !strings.HasSuffix(it.URL, "/used/Tesla/2026-Tesla-Model-S-abc.htm") {
		t.Fatalf("url = %q", it.URL)
	}
}

// totalCount/pageSize drive the walk; the site's own links are ellipsis-elided.
func TestExtractDealerDotComPageURLs_CoversEveryOffset(t *testing.T) {
	out := expandDealerDotComInventory(context.Background(),
		"https://www.kianmotors.com/used-inventory/index.htm", ddcStateFixture)
	urls := extractDealerDotComPageURLs("https://www.kianmotors.com/used-inventory/index.htm", out)
	// 325 over pages of 48 => 7 pages => 6 more after the first.
	want := []string{"start=48", "start=96", "start=144", "start=192", "start=240", "start=288"}
	if len(urls) != len(want) {
		t.Fatalf("expected %d page urls got %d: %v", len(want), len(urls), urls)
	}
	for i, w := range want {
		if !strings.Contains(urls[i], w) {
			t.Fatalf("url[%d] = %q, want %s", i, urls[i], w)
		}
	}
}

func TestExpandDealerDotComInventory_IgnoresUnrelatedPages(t *testing.T) {
	in := `<html><body><li class="vehicle-card">no widget state here</li></body></html>`
	if out := expandDealerDotComInventory(context.Background(), "https://dealer.test/x", in); out != in {
		t.Fatalf("unrelated page was rewritten")
	}
}

// Hydration is racy; a page without the blob must fall through to DOM selectors.
func TestExpandDealerDotComInventory_KeepsHTMLWhenStateEmpty(t *testing.T) {
	in := `<html><body><li class="vehicle-card-detailed">x</li>
<script>DDC.WS.state['ws-inv-data']['inventory-data-bus2'] = {"WIS":{"pageInfo":{},"inventory":[]}};</script>
</body></html>`
	if out := expandDealerDotComInventory(context.Background(), "https://dealer.test/x", in); out != in {
		t.Fatalf("empty-inventory page was rewritten")
	}
}

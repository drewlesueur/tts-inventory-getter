package scrape

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// Dealer eProcess pricing is a <dl> whose rows differ between used and new
// cars. Used ends on the dealer price, but NEW cards append a conditional-offer
// rebate AFTER it, so ":last-of-type" read "- $2,250" as the price on 222 of
// hornemazdaavondale's 298 vehicles. The sale price is the bold row in both.
func TestEProcessConfigsReadTheBoldSalePrice(t *testing.T) {
	card := func(rows string) string {
		return `<div class="srp_vehicle_item_container">` +
			`<div class="multi_widget_1628_1"><h2><a href="/auto/x/1/">A CAR</a></h2></div>` +
			`<a data-vin="JM1BL1TG1D1779942" href="/auto/x/1/">vdp</a>` +
			`<table class="srp_details"><tr><td>Stock #</td><td class="details-overview_data">K1</td></tr>` +
			`<tr><td>Mileage</td><td class="details-overview_data">10,000</td></tr></table>` +
			`<dl>` + rows + `</dl></div>`
	}
	used := card(
		`<dt>Retail Price</dt><dd class="vehicle_price" style="color:#000;">$15,790</dd>` +
			`<dt>Discount</dt><dd class="vehicle_price price_negative">- $4,791</dd>` +
			`<dt>Doc Fee</dt><dd class="vehicle_price">+$999</dd>` +
			`<dt>Dealer Price</dt><dd class="vehicle_price" style="color:#000;font-weight: bold;">$11,998</dd>`)
	// The trailing rebate is what broke :last-of-type.
	newCar := card(
		`<dt>MSRP</dt><dd class="vehicle_price">$37,160</dd>` +
			`<dt>Rebate</dt><dd class="vehicle_price price_negative">- $1,000</dd>` +
			`<dt>Doc Fee</dt><dd class="vehicle_price">+$999</dd>` +
			`<dt>Everyone Price</dt><dd class="vehicle_price" style="color:#000;font-weight: bold;">$35,687</dd>` +
			`<dt>Conditional Offers</dt><dd class="vehicle_price price_negative">- $2,250</dd>`)

	site, err := (config.Loader{}).LoadByPath(filepath.Join("..", "..", "configs", "sites",
		"urlkey_dXJsOjp3d3cuaG9ybmVtYXpkYWF2b25kYWxlLmNvbS9zZWFyY2g.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, html, want string }{
		{"used", used, "$11,998"},
		{"new with trailing rebate", newCar, "$35,687"},
	} {
		items, errs := (DOMExtractor{}).Extract(context.Background(), tc.html, site.BaseURL, site)
		if len(errs) != 0 || len(items) != 1 {
			t.Fatalf("%s: items=%+v errs=%+v", tc.name, items, errs)
		}
		if items[0].Price != tc.want {
			t.Fatalf("%s: price = %q, want %q", tc.name, items[0].Price, tc.want)
		}
	}
}

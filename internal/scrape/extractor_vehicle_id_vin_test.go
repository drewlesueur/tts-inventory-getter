package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// DealerCenter's "layout-6" listing (pandaautogallery) carries the VIN only on
// the card container, prefixed — data-vehicle-id="vehicle-id-<VIN>". The regex
// fallback sees card text, not attributes, so without stripping that prefix the
// whole page extracts with no VIN at all.
func TestExtractVINFromPrefixedVehicleIDAttr(t *testing.T) {
	site := config.SiteConfig{BaseURL: "https://dealer.test/inventory/"}
	site.ListPage.CardSelector = ".vehicle-container"
	site.ListPage.TitleSelector = "a.vehicle-title"
	site.ListPage.URLSelector = "a.vehicle-title"

	html := `<div class="vehicle-container" data-vehicle-id="vehicle-id-WDCYC7HJ0KX303526">
  <a class="vehicle-title" href="/inventory/mercedes-benz/g-class/303526/">2019 MERCEDES-BENZ G-CLASS</a>
  <span class="vehicle-price-value">$132,700 <a href="#">*Fees Not Disclosed</a></span>
</div>`

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, site.BaseURL, site)
	if len(errs) != 0 || len(items) != 1 {
		t.Fatalf("expected one vehicle, got items=%+v errs=%+v", items, errs)
	}
	if items[0].VIN != "WDCYC7HJ0KX303526" {
		t.Fatalf("VIN = %q, want the prefix stripped", items[0].VIN)
	}
	// The fee link must not be glued onto the price by .Text().
	if p := items[0].Price; p != "" && p != "$132,700" {
		t.Fatalf("price = %q, want just the figure", p)
	}
}

// A non-VIN value in that attribute must not become a VIN.
func TestPrefixedVehicleIDAttrIgnoresNonVIN(t *testing.T) {
	site := config.SiteConfig{BaseURL: "https://dealer.test/inventory/"}
	site.ListPage.CardSelector = ".vehicle-container"
	site.ListPage.TitleSelector = "a.vehicle-title"
	site.ListPage.URLSelector = "a.vehicle-title"

	html := `<div class="vehicle-container" data-vehicle-id="vehicle-id-12345">
  <a class="vehicle-title" href="/inventory/x/12345/">2019 Some Car</a>
</div>`
	items, _ := (DOMExtractor{}).Extract(context.Background(), html, site.BaseURL, site)
	if len(items) == 1 && items[0].VIN != "" {
		t.Fatalf("VIN = %q, want empty for a non-VIN id", items[0].VIN)
	}
}

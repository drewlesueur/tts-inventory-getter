package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

func TestDOMExtractor_PrefersDetailURLOverNonVehicleLinks(t *testing.T) {
	html := `<div class="ds-vehicle-list-item">
		<a href="https://www.google.com/maps/dir/?api=1&destination=Dealer">Map</a>
		<a href="/pre-owned-cars/detail/2016-Acura-MDX/1454461">View Details</a>
		<meta itemprop="sku" content="26027" />
		<meta itemprop="name" content="2016 Acura MDX w/Advance" />
	</div>`

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".ds-vehicle-list-item"
	site.ListPage.TitleSelector = "h4"
	site.ListPage.URLSelector = "a"

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, "https://dealer.test/pre-owned-cars", site)
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %+v", errs)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].URL != "https://dealer.test/pre-owned-cars/detail/2016-Acura-MDX/1454461" {
		t.Fatalf("unexpected url: %q", items[0].URL)
	}
}


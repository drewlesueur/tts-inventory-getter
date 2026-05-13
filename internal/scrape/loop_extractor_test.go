package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

func TestLoopHTMLExtractor_ParsesPriceAndMileage(t *testing.T) {
	html := `<div data-elementor-type="loop-item" class="vehicle type-vehicle stock-a1b2 make-toyota model-camry">
<a href="/vehicle-details/2020-toyota-camry-a1b2/">View</a>
<img src="https://dealer.test/v1.jpg" />
<div>$24,995</div>
<div>65,432 miles</div>
</div>`

	items, errs := (LoopHTMLExtractor{}).Extract(context.Background(), html, "https://dealer.test/inventory", config.SiteConfig{})
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %+v", errs)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Price != "$24,995" {
		t.Fatalf("expected price $24,995, got %q", items[0].Price)
	}
	if items[0].Mileage != "65,432 mi" {
		t.Fatalf("expected mileage 65,432 mi, got %q", items[0].Mileage)
	}
}

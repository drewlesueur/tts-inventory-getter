package scrape

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// A Space Auto SRP serves only empty ".vehicle card" skeletons; the inventory
// lives behind a POST to the search API. Verify the shell is recognized and
// replaced with the full feed rather than being mistaken for a hydrated page.
func TestExpandSpaceAutoInventory_ReplacesSkeletonShell(t *testing.T) {
	var gotDealership, gotBody string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDealership = r.Header.Get("dealership")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 1,
			"vehicles": []map[string]any{{
				"vin": "1N4BL4CV1MN409847", "stock": "MN409847", "year": 2021,
				"make": "Nissan", "model": "Altima", "trim": "2.5 SR",
				"price": 20000, "mileage": 62234, "color": "Gray", "body": "Sedan",
				"driveline": "FWD", "transmission": "CVT",
				"photos": []string{"https://assets.test/a.jpg"},
			}},
		})
	}))
	defer api.Close()

	origin := spaceAutoSearchURL
	spaceAutoSearchURL = api.URL + "/vehicles/search"
	defer func() { spaceAutoSearchURL = origin }()

	shell := `<html><body>
		<div class="vehicle-grid skeleton">
			<div class="vehicle card"><div class="vehicle-price"></div></div>
			<div class="vehicle card"><div class="vehicle-price"></div></div>
		</div>
		<script>var $space_id = 'choiceautomotive';</script>
		<script src="https://search-api.space.auto/x.js"></script>
	</body></html>`

	out := expandSpaceAutoInventory(context.Background(), "https://dealer.test/cars/", shell)
	if out == shell {
		t.Fatalf("shell was not expanded")
	}
	if gotDealership != "choiceautomotive" {
		t.Fatalf("dealership header = %q", gotDealership)
	}
	if !strings.Contains(gotBody, `"limit":500`) {
		t.Fatalf("unexpected request body: %s", gotBody)
	}

	site := config_SiteForSpaceAuto()
	items, errs := DOMExtractor{}.Extract(context.Background(), out, "https://dealer.test/cars/", site)
	if len(items) != 1 {
		t.Fatalf("expected 1 item got %d errs=%+v", len(items), errs)
	}
	it := items[0]
	if it.Title != "2021 Nissan Altima 2.5 SR" {
		t.Fatalf("title = %q", it.Title)
	}
	if it.Price != "$20,000" {
		t.Fatalf("price = %q", it.Price)
	}
	if it.Mileage != "62,234 mi" {
		t.Fatalf("mileage = %q", it.Mileage)
	}
	if it.StockID != "MN409847" {
		t.Fatalf("stock = %q", it.StockID)
	}
	// No vehicles.js on this shell, so the VIN-based VDP path is the fallback.
	if !strings.HasSuffix(it.URL, "/vehicle/1n4bl4cv1mn409847/") {
		t.Fatalf("url = %q", it.URL)
	}
}

// A page with no Space Auto markers must be handed back untouched.
func TestExpandSpaceAutoInventory_IgnoresUnrelatedPages(t *testing.T) {
	in := `<html><body><div class="vehicle card">nothing to see</div></body></html>`
	if out := expandSpaceAutoInventory(context.Background(), "https://dealer.test/cars/", in); out != in {
		t.Fatalf("unrelated page was rewritten")
	}
}

func config_SiteForSpaceAuto() (site config.SiteConfig) {
	site.ListPage.CardSelector = ".vehicle.card"
	site.ListPage.TitleSelector = ".vehicle-title"
	site.ListPage.URLSelector = "a[href*='/vehicle/']"
	site.ListPage.StockSelector = ".vehicle-stock"
	site.ListPage.PriceSelector = ".vehicle-price"
	site.ListPage.MileageSelector = ".vehicle-mileage"
	site.ListPage.ImageSelector = "img.vehicle-image"
	return site
}

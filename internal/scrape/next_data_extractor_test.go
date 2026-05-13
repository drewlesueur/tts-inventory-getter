package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

func TestNextDataExtractor_ExtractsVehicleObjects(t *testing.T) {
	html := `<!doctype html><html><body><script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"inventory":{"results":[{"year":2023,"make":"Ferrari","model":"Purosangue","stock_no":"6604","vin":"ZSG06VTA9P0301099","url":"/vehicle-details/2023-ferrari-purosangue-6604/","primary_image":"https://example.com/f1.jpg","images":["https://example.com/f1.jpg","https://example.com/f2.jpg"],"price":486399}]}}}}</script></body></html>`

	items, errs := (NextDataExtractor{}).Extract(context.Background(), html, "https://www.idealcarsaz.com/used-cars-in-mesa-az", config.SiteConfig{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item got %d", len(items))
	}
	it := items[0]
	if it.StockID != "6604" {
		t.Fatalf("expected stockId=6604 got %q", it.StockID)
	}
	if it.Make != "Ferrari" || it.Model != "Purosangue" || it.Year != "2023" {
		t.Fatalf("unexpected make/model/year: %+v", it)
	}
	if it.VIN != "ZSG06VTA9P0301099" {
		t.Fatalf("unexpected vin: %q", it.VIN)
	}
	if it.URL == "" || it.PrimaryImage == "" || len(it.Images) == 0 {
		t.Fatalf("expected url/image fields populated: %+v", it)
	}
}

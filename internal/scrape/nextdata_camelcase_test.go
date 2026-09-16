package scrape

import (
	"context"
	"strings"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// primemotorco's Next.js payload uses camelCase keys and ships the gallery as
// one comma-separated string. An exact key lookup missed "stockNumber" for all
// 148 vehicles (so NormalizeItem substituted the VIN as the stock id) and only
// the single "photo" thumbnail survived out of up to 42 images.
func TestNextDataExtractor_CamelCaseKeysAndJoinedPhotos(t *testing.T) {
	html := `<!doctype html><html><body><script id="__NEXT_DATA__" type="application/json">` +
		`{"props":{"pageProps":{"initialVehicles":[{"year":"2017","make":"Lamborghini","model":"Huracan",` +
		`"price":220995,"odometer":"37235","exteriorColor":"Black","bodyType":"Coupe",` +
		`"stockNumber":"A05504","vin":"ZHWUC1ZF4HLA05504","photo":"https://ex.com/1.jpg",` +
		`"allPhotos":"https://ex.com/1.jpg,https://ex.com/2.jpg,https://ex.com/3.jpg"}]}}}` +
		`</script></body></html>`

	items, errs := (NextDataExtractor{}).Extract(context.Background(), html, "https://www.primemotorco.com/inventory/", config.SiteConfig{})
	if len(errs) != 0 || len(items) != 1 {
		t.Fatalf("items=%+v errs=%+v", items, errs)
	}
	it := items[0]
	if it.StockID != "A05504" {
		t.Fatalf("stockId = %q, want the camelCase stockNumber A05504 (not the VIN)", it.StockID)
	}
	if it.VIN != "ZHWUC1ZF4HLA05504" {
		t.Fatalf("vin = %q", it.VIN)
	}
	if it.Color != "Black" || it.BodyType != "Coupe" {
		t.Fatalf("camelCase spec keys missed: color=%q bodyType=%q", it.Color, it.BodyType)
	}
	if len(it.Images) != 3 {
		t.Fatalf("images = %d, want all 3 from the comma-separated allPhotos", len(it.Images))
	}
}

// Snake_case payloads must keep working unchanged.
func TestNextDataExtractor_StillHandlesSnakeCase(t *testing.T) {
	html := `<script id="__NEXT_DATA__" type="application/json">` +
		`{"props":{"pageProps":{"inventory":{"results":[{"year":2023,"make":"Ferrari","model":"Purosangue",` +
		`"stock_no":"6604","vin":"ZSG06VTA9P0301099","primary_image":"https://ex.com/f1.jpg","price":486399}]}}}}` +
		`</script>`
	items, _ := (NextDataExtractor{}).Extract(context.Background(), html, "https://dealer.test/inv", config.SiteConfig{})
	if len(items) != 1 || items[0].StockID != "6604" {
		t.Fatalf("snake_case regression: %+v", items)
	}
}

// The page links are client-side only, so the walk has to come from the blob's
// page/pageCount. 148 vehicles at 20 per page = 8 pages.
func TestExtractNextDataPageURLs_SynthesizesFromPageCount(t *testing.T) {
	html := `<script id="__NEXT_DATA__" type="application/json">` +
		`{"props":{"pageProps":{"page":1,"pageSize":20,"pageCount":8,"total":148}}}</script>`
	got := extractNextDataPageURLs("https://www.primemotorco.com/inventory/", html)
	if len(got) != 7 {
		t.Fatalf("expected pages 2-8, got %d: %v", len(got), got)
	}
	if !strings.HasSuffix(got[0], "page=2") || !strings.HasSuffix(got[6], "page=8") {
		t.Fatalf("unexpected page urls: %v", got)
	}
	// A later page must not re-emit the pages already walked.
	if rest := extractNextDataPageURLs("https://www.primemotorco.com/inventory/?page=7", html); len(rest) != 1 {
		t.Fatalf("expected only page 8 from page 7, got %v", rest)
	}
	// Single-page inventories yield nothing.
	if none := extractNextDataPageURLs("https://dealer.test/inv", `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"page":1,"pageCount":1}}}</script>`); none != nil {
		t.Fatalf("expected no pages, got %v", none)
	}
}

package scrape

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestStricklandConfigExtractsOnlyVehicleCards(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "sites", "urlkey_dXJsOjp3d3cuc3RyaWNrbGFuZGF1dG8uY29tL2ludmVudG9yeQ.yaml")
	site, err := (config.Loader{}).LoadByPath(path)
	if err != nil {
		t.Fatalf("load Strickland config: %v", err)
	}
	html := `<p id="inv-total">45 vehicles available</p>
<div data-mlr="vehicle-grid">
  <div data-mlr="vehicle-card">
    <a href="/inventory/2024-tesla-model-y-tes6"><img title="2024 Tesla Model Y at Strickland Auto" src="//d3vd6h5tjc937b.cloudfront.net/car.jpg"></a>
    <h3 data-mlr="vehicle-card-title">2024 Tesla Model Y Long Range</h3>
    <p data-mlr="vehicle-card-price">$28,995</p>
  </div>
</div>
<nav aria-label="Pagination"><a href="/inventory?page=2">Next</a></nav>
<div class="bg-white overflow-hidden">navigation noise</div>`

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, site.BaseURL, site)
	if len(errs) != 0 || len(items) != 1 {
		t.Fatalf("expected one vehicle, got items=%+v errors=%+v", items, errs)
	}
	it := items[0]
	if it.Title != "2024 Tesla Model Y Long Range" || it.Price != "$28,995" || it.URL != "https://www.stricklandauto.com/inventory/2024-tesla-model-y-tes6" {
		t.Fatalf("unexpected extracted vehicle: %+v", it)
	}
	if got := detectInventoryTotal(html, site); got != 45 {
		t.Fatalf("inventory total = %d, want 45", got)
	}
	next := extractNextPageURLs(site.BaseURL, html, site)
	if len(next) != 1 || next[0] != "https://www.stricklandauto.com/inventory?page=2" {
		t.Fatalf("next pages = %v", next)
	}
}

func TestStricklandDetailPopulatesStockVINAndImages(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "sites", "urlkey_dXJsOjp3d3cuc3RyaWNrbGFuZGF1dG8uY29tL2ludmVudG9yeQ.yaml")
	site, err := (config.Loader{}).LoadByPath(path)
	if err != nil {
		t.Fatal(err)
	}
	html := `<div x-data="{ active: 'image' }"><img title="2024 Tesla Model Y at Strickland Auto" src="//d3vd6h5tjc937b.cloudfront.net/car.jpg"></div>
	<p class="text-xs text-gray-400 mt-2">Stock #TES6</p><p class="text-xs text-gray-400 mt-3">VIN: 7SAYGDEE7RF163837</p>`
	it, err := populateDetailsFromHTML(context.Background(), nil, model.InventoryItem{URL: "https://www.stricklandauto.com/inventory/2024-tesla-model-y-tes6"}, site, html)
	if err != nil {
		t.Fatal(err)
	}
	it = NormalizeItem(site.BaseURL, it)
	if it.StockID != "TES6" || it.VIN != "7SAYGDEE7RF163837" || len(it.Images) == 0 {
		t.Fatalf("detail extraction incomplete: %+v", it)
	}
}

// Run with RUN_LIVE_STRICKLAND=1 to validate the permanent template against
// the public MotorLot pages without involving the API database or upsert jobs.
func TestLiveStricklandConfig(t *testing.T) {
	if os.Getenv("RUN_LIVE_STRICKLAND") != "1" {
		t.Skip("set RUN_LIVE_STRICKLAND=1 to run the live scrape")
	}
	path := filepath.Join("..", "..", "configs", "sites", "urlkey_dXJsOjp3d3cuc3RyaWNrbGFuZGF1dG8uY29tL2ludmVudG9yeQ.yaml")
	site, err := (config.Loader{}).LoadByPath(path)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := NewHTTPFetcherWithTimeout(30 * time.Second)
	svc := Service{
		Fetcher:       fetcher,
		DetailFetcher: HTMLDetailFetcher{Fetcher: fetcher},
		Extractors:    []Extractor{LoopHTMLExtractor{}, DOMExtractor{}, NextDataExtractor{}, RegexExtractor{}},
		Concurrency:   6,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	res := svc.ScrapeOnce(ctx, site.BaseURL, site)
	if len(res.Items) < 40 {
		t.Fatalf("expected at least 40 live vehicles, got %d errors=%+v", len(res.Items), res.Errors)
	}
	missingStock, missingVIN, missingImages := 0, 0, 0
	for _, it := range res.Items {
		if it.StockID == "" {
			missingStock++
		}
		if it.VIN == "" {
			missingVIN++
		}
		if len(it.Images) == 0 {
			missingImages++
		}
	}
	if missingStock != 0 || missingVIN != 0 || missingImages != 0 {
		t.Fatalf("incomplete live inventory: items=%d missingStock=%d missingVIN=%d missingImages=%d errors=%+v", len(res.Items), missingStock, missingVIN, missingImages, res.Errors)
	}
	t.Logf("live scrape passed: items=%d errors=%d", len(res.Items), len(res.Errors))
}

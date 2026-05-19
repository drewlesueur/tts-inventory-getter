package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestPopulateDetailsFromHTML_ExtractsStockAndVINFromDetailText(t *testing.T) {
	html := `<html><body>
	<div class="specs">
		<span>Stock #: 26049</span>
		<span>VIN: 2C4RC1DG7LR264368</span>
	</div>
	</body></html>`

	site := config.SiteConfig{}
	site.Regex.Stock = []string{`(?i)stock\s*#?[:\-]?\s*([a-z0-9\-]+)`}
	site.Regex.VIN = []string{`\b([A-HJ-NPR-Z0-9]{17})\b`}

	item := model.InventoryItem{URL: "https://dealer.test/detail/1"}
	out, err := populateDetailsFromHTML(context.Background(), nil, item, site, html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.StockID != "26049" {
		t.Fatalf("expected stockId=26049 got %q", out.StockID)
	}
	if out.VIN != "2C4RC1DG7LR264368" {
		t.Fatalf("expected vin=2C4RC1DG7LR264368 got %q", out.VIN)
	}
}


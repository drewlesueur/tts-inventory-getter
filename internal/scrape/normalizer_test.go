package scrape

import (
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestNormalizeItem(t *testing.T) {
	in := model.InventoryItem{Title: " 2021   Toyota   Camry ", URL: "/inventory/a", Images: []string{"/img/a.jpg"}}
	out := NormalizeItem("https://dealer.test", in)
	if out.Year != "2021" {
		t.Fatalf("expected year 2021 got %s", out.Year)
	}
	if out.Make != "Toyota" {
		t.Fatalf("expected make Toyota got %s", out.Make)
	}
	if out.URL != "https://dealer.test/inventory/a" {
		t.Fatalf("unexpected url %s", out.URL)
	}
	if out.PrimaryImage != "https://dealer.test/img/a.jpg" {
		t.Fatalf("unexpected primary image %s", out.PrimaryImage)
	}
}

func TestDedupe(t *testing.T) {
	items := []model.InventoryItem{{URL: "u1"}, {URL: "u1"}, {StockID: "s1", Images: []string{"i1"}}, {StockID: "s1", Images: []string{"i1"}}}
	out := Dedupe(items)
	if len(out) != 2 {
		t.Fatalf("expected 2 got %d", len(out))
	}
}

func TestNormalizeItem_NormalizesStockIDLabel(t *testing.T) {
	in := model.InventoryItem{StockID: " Stock # 26027 "}
	out := NormalizeItem("https://dealer.test", in)
	if out.StockID != "26027" {
		t.Fatalf("expected stock 26027 got %q", out.StockID)
	}
}

func TestNormalizeItem_UsesNumericListingIDWhenStockIsMissing(t *testing.T) {
	tests := []struct {
		name    string
		stockID string
	}{
		{name: "empty"},
		{name: "not available", stockID: "N/A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := model.InventoryItem{
				StockID: tt.stockID,
				URL:     "/used-trucks/for-sale/example/2022/2191308/",
			}
			out := NormalizeItem("https://dealer.test/inventory/", in)
			if out.StockID != "2191308" {
				t.Fatalf("expected listing ID fallback 2191308 got %q", out.StockID)
			}
		})
	}
}

func TestNormalizeItem_DoesNotUseNonNumericURLSlugAsStockID(t *testing.T) {
	in := model.InventoryItem{URL: "/inventory/example-truck/"}
	out := NormalizeItem("https://dealer.test", in)
	if out.StockID != "" {
		t.Fatalf("expected empty stock ID got %q", out.StockID)
	}
}

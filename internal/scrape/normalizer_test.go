package scrape

import (
	"testing"

	"github.com/example/inventory-scraper/internal/model"
)

func TestNormalizeItem(t *testing.T) {
	in := model.InventoryItem{Title: " 2021   Toyota   Camry ", URL: "/inventory/a", Images: []string{"/img/a.jpg"}}
	out := NormalizeItem("https://dealer.test", in)
	if out.Year != "2021" { t.Fatalf("expected year 2021 got %s", out.Year) }
	if out.Make != "Toyota" { t.Fatalf("expected make Toyota got %s", out.Make) }
	if out.URL != "https://dealer.test/inventory/a" { t.Fatalf("unexpected url %s", out.URL) }
	if out.PrimaryImage != "https://dealer.test/img/a.jpg" { t.Fatalf("unexpected primary image %s", out.PrimaryImage) }
}

func TestDedupe(t *testing.T) {
	items := []model.InventoryItem{{URL: "u1"}, {URL: "u1"}, {StockID: "s1", Images: []string{"i1"}}, {StockID: "s1", Images: []string{"i1"}}}
	out := Dedupe(items)
	if len(out) != 2 { t.Fatalf("expected 2 got %d", len(out)) }
}

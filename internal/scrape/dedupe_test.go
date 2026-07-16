package scrape

import (
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestDedupe_MergesDuplicateItemsAndKeepsImage(t *testing.T) {
	items := []model.InventoryItem{
		{StockID: "C73921", Title: "2020 Ford F-150"},
		{StockID: "C73921", PrimaryImage: "https://static.overfuel.com/dealers/ideal-cars/image/new_arrival_graphic_ideal_cars.webp?w=1920&q=80", Images: []string{"https://static.overfuel.com/dealers/ideal-cars/image/new_arrival_graphic_ideal_cars.webp?w=1920&q=80"}},
	}
	out := Dedupe(items)
	if len(out) != 1 {
		t.Fatalf("expected 1 item, got %d", len(out))
	}
	if out[0].PrimaryImage == "" {
		t.Fatalf("expected merged item to keep primary image")
	}
	if len(out[0].Images) == 0 {
		t.Fatalf("expected merged item to keep image list")
	}
}

func TestDedupe_PrefersStockKeyOverVINToMergeVariants(t *testing.T) {
	items := []model.InventoryItem{
		{
			StockID:      "26027",
			Title:        "2016 Acura MDX w/Advance",
			URL:          "https://dealer.test/pre-owned-cars/detail/2016-Acura-MDX/1454461",
			PrimaryImage: "https://images.dealersync.com/a.jpg?width=450",
			Images:       []string{"https://images.dealersync.com/a.jpg?width=450"},
		},
		{
			StockID: "26027",
			VIN:     "5FRYD4H95GB010521",
			URL:     "https://dealer.test/pre-owned-cars/detail/2016-Acura-MDX/1454461",
			Images:  []string{"https://images.dealersync.com/a.jpg"},
		},
	}

	out := Dedupe(items)
	if len(out) != 1 {
		t.Fatalf("expected 1 unique item, got %d", len(out))
	}
	if out[0].StockID != "26027" {
		t.Fatalf("expected stockId 26027, got %q", out[0].StockID)
	}
	if out[0].VIN != "5FRYD4H95GB010521" {
		t.Fatalf("expected VIN merged, got %q", out[0].VIN)
	}
	if len(out[0].Images) < 2 {
		t.Fatalf("expected merged images from both variants, got %d", len(out[0].Images))
	}
}

func TestDedupe_KeepsItemWithNoStockVINURLOrImage(t *testing.T) {
	// A car with no stock, VIN, URL, or image (e.g. its detail fetch dropped the
	// URL) must NOT be silently discarded — it has to survive on a content key.
	items := []model.InventoryItem{
		{StockID: "11434", Title: "2011 Chevrolet Impala LT", VIN: "2G1WG5EK9B1303954"},
		{Title: "2011 Mitsubishi Lancer", Year: "2011", Make: "Mitsubishi", Model: "Lancer", Mileage: "98,000", Price: "$6,995"},
	}
	out := Dedupe(items)
	if len(out) != 2 {
		t.Fatalf("expected 2 items (keyless car retained), got %d", len(out))
	}
	var sawLancer bool
	for _, it := range out {
		if it.Title == "2011 Mitsubishi Lancer" {
			sawLancer = true
		}
	}
	if !sawLancer {
		t.Fatalf("expected the no-ID Lancer to survive dedupe, got %#v", out)
	}
}

func TestDedupe_ReplacesPriceOnlyTitleWithVehicleTitle(t *testing.T) {
	items := []model.InventoryItem{
		{StockID: "7026", Title: "$666,399", Price: "$666,399", URL: "https://dealer.test/vehicle-details/2024-lamborghini-revuelto-7026/"},
		{StockID: "7026", Title: "2024 Lamborghini Revuelto", URL: "https://dealer.test/vehicle-details/2024-lamborghini-revuelto-7026/"},
	}

	out := Dedupe(items)
	if len(out) != 1 {
		t.Fatalf("expected 1 unique item, got %d", len(out))
	}
	if out[0].Title != "2024 Lamborghini Revuelto" {
		t.Fatalf("expected vehicle title to replace price title, got %q", out[0].Title)
	}
}

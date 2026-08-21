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

// Title splitting yields only the first model token, so a structured source that
// knows the full name must win the merge.
func TestDedupe_PrefersFullMultiWordModelName(t *testing.T) {
	fromTitle := model.InventoryItem{StockID: "P558569", Title: "2026 Tesla Model S Plaid", Make: "Tesla", Model: "Model"}
	fromAPI := model.InventoryItem{StockID: "P558569", Make: "Tesla", Model: "Model S", VIN: "5YJSA1E67TF558569"}
	out := Dedupe([]model.InventoryItem{fromTitle, fromAPI})
	if len(out) != 1 {
		t.Fatalf("expected 1 merged item got %d", len(out))
	}
	if out[0].Model != "Model S" {
		t.Fatalf("model = %q, want \"Model S\"", out[0].Model)
	}
}

// A different model that merely shares a prefix must not be overwritten.
func TestDedupe_KeepsUnrelatedModelWithSharedPrefix(t *testing.T) {
	a := model.InventoryItem{StockID: "X1", Model: "Civic"}
	b := model.InventoryItem{StockID: "X1", Model: "Civics"}
	out := Dedupe([]model.InventoryItem{a, b})
	if out[0].Model != "Civic" {
		t.Fatalf("model = %q, want \"Civic\"", out[0].Model)
	}
}

// Images served by an endpoint keep their identity in the query, which
// canonicalURLKey strips — so an image key must not merge distinct vehicles.
func TestDedupe_DoesNotMergeOnQueryOnlyImageEndpoint(t *testing.T) {
	base := "https://service.test/images/GetEvoxImage?vin="
	items := []model.InventoryItem{
		{Title: "2026 Ford Maverick XL", Price: "$29,483", PrimaryImage: base + "AAA"},
		{Title: "2026 Ford Bronco Sport", Price: "$30,073", PrimaryImage: base + "BBB"},
		{Title: "2026 Ford Escape Active", Price: "$31,100", PrimaryImage: base + "CCC"},
	}
	if out := Dedupe(items); len(out) != 3 {
		t.Fatalf("expected 3 items got %d", len(out))
	}
}

// A real image file path is still a usable identity.
func TestDedupe_StillMergesOnIdenticalImageFile(t *testing.T) {
	items := []model.InventoryItem{
		{Title: "2020 Audi A4", PrimaryImage: "https://img.test/a/car-1.jpg?w=300"},
		{Title: "2020 Audi A4", Price: "$10,000", PrimaryImage: "https://img.test/a/car-1.jpg?w=900"},
	}
	out := Dedupe(items)
	if len(out) != 1 {
		t.Fatalf("expected 1 merged item got %d", len(out))
	}
	if out[0].Price != "$10,000" {
		t.Fatalf("merge lost the price: %q", out[0].Price)
	}
}

// A bogus stock value shared across a page (an empty "Stock #" cell that
// captured the model year) must not merge distinct vehicles.
func TestDedupe_NeverMergesDifferentVINs(t *testing.T) {
	items := []model.InventoryItem{
		{Title: "2018 Honda CR-V", StockID: "2018", VIN: "7FARW2H52LE004353", Price: "$14,991"},
		{Title: "2018 Subaru Outback", StockID: "2018", VIN: "4S4BSANC2J3376066", Price: "$15,499"},
		{Title: "2018 Mazda CX-5", StockID: "2018", VIN: "JM3KFBDM4J0329501", Price: "$16,200"},
	}
	out := Dedupe(items)
	if len(out) != 3 {
		t.Fatalf("expected 3 distinct vehicles got %d", len(out))
	}
	// The same VIN seen twice still merges.
	dup := []model.InventoryItem{
		{StockID: "A1", VIN: "7FARW2H52LE004353", Title: "2018 Honda CR-V"},
		{StockID: "A1", VIN: "7FARW2H52LE004353", Price: "$14,991"},
	}
	if merged := Dedupe(dup); len(merged) != 1 || merged[0].Price != "$14,991" {
		t.Fatalf("same VIN should merge: %+v", merged)
	}
}

package model

import "testing"

func TestInventoryCountPrefersUniqueVINs(t *testing.T) {
	items := []InventoryItem{
		{StockID: "A1", VIN: "5FRYD4H95GB010521"},
		{StockID: "A2", VIN: "5fryd4h95gb010521"},
		{StockID: "B1", VIN: "1HGCM82633A123456"},
	}
	if got := InventoryCountByUniqueVIN(items); got != 2 {
		t.Fatalf("expected 2 unique VINs, got %d", got)
	}
	if got := InventoryCount(items); got != 2 {
		t.Fatalf("expected inventory count to prefer unique VINs, got %d", got)
	}
	if got := InventoryIdentityCount(items); got != 2 {
		t.Fatalf("expected identity count to prefer unique VIN keys, got %d", got)
	}
}

func TestInventoryCountUsesUniqueStockIDsWithoutVINs(t *testing.T) {
	items := []InventoryItem{
		{StockID: "A1"},
		{StockID: "a1"},
		{StockID: "B1"},
	}
	if got := InventoryCountByUniqueVIN(items); got != 0 {
		t.Fatalf("expected 0 unique VINs, got %d", got)
	}
	if got := InventoryCountByUniqueStockID(items); got != 2 {
		t.Fatalf("expected 2 unique stock IDs, got %d", got)
	}
	if got := InventoryCount(items); got != 2 {
		t.Fatalf("expected stock ID identity count, got %d", got)
	}
}

func TestInventoryCountFallsBackToItemCountWithoutIdentifiers(t *testing.T) {
	items := []InventoryItem{
		{Title: "Car A"},
		{Title: "Car B"},
	}
	if got := InventoryIdentityCount(items); got != 0 {
		t.Fatalf("expected 0 identity keys, got %d", got)
	}
	if got := InventoryCount(items); got != 2 {
		t.Fatalf("expected item count fallback without identifiers, got %d", got)
	}
}

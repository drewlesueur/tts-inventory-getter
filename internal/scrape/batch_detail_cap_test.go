package scrape

import (
	"context"
	"strconv"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

// The page cap used to truncate silently, so every vehicle past it came back
// with no stock number while the run still reported success. 217 vehicles
// against the old default of 150 is exactly how 67 listings lost their stock id.
func TestSelectDetailURLs_ReportsWhatTheCapDropped(t *testing.T) {
	items := make([]model.InventoryItem, 0, 217)
	for i := 0; i < 217; i++ {
		items = append(items, model.InventoryItem{Title: "car", URL: "https://d.test/v/" + strconv.Itoa(i)})
	}
	urls, dropped := selectDetailURLs(items, 150)
	if len(urls) != 150 {
		t.Fatalf("fetched %d urls, want 150", len(urls))
	}
	if dropped != 67 {
		t.Fatalf("dropped = %d, want 67", dropped)
	}
	// Uncapped, nothing is left behind.
	if urls, dropped = selectDetailURLs(items, 0); len(urls) != 217 || dropped != 0 {
		t.Fatalf("uncapped: urls=%d dropped=%d", len(urls), dropped)
	}
}

// Already-complete listings must not consume the cap.
func TestSelectDetailURLs_SkipsCompleteAndDuplicateItems(t *testing.T) {
	complete := model.InventoryItem{
		Title: "2026 Ford Maverick XL", URL: "https://d.test/v/1",
		VIN: "3FTTW8BA6TRA65715", StockID: "106912", Price: "$29,483",
		Mileage: "2,717", PrimaryImage: "https://d.test/a.jpg",
	}
	needy := model.InventoryItem{Title: "car", URL: "https://d.test/v/2"}
	dup := needy

	urls, dropped := selectDetailURLs([]model.InventoryItem{complete, needy, dup}, 150)
	if len(urls) != 1 || urls[0] != "https://d.test/v/2" {
		t.Fatalf("urls = %v, want just the incomplete listing", urls)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
}

// A run that could not cover every vehicle must never return a nil error.
func TestBatchDetailFetcher_SurfacesCapTruncation(t *testing.T) {
	complete := model.InventoryItem{
		Title: "x", URL: "https://d.test/v/1", VIN: "3FTTW8BA6TRA65715",
		StockID: "106912", Price: "$1", Mileage: "1", PrimaryImage: "https://d.test/a.jpg",
	}
	b := &BatchDetailFetcher{ScriptPath: "/nonexistent", PythonBin: "/nonexistent", MaxPages: 150}
	out, err := b.PrefetchAndPopulate(context.Background(),
		[]model.InventoryItem{complete, complete}, config.SiteConfig{})
	if err != nil {
		t.Fatalf("nothing needed fetching, got %v", err)
	}
	if len(out) != 2 || out[0].StockID != "106912" {
		t.Fatalf("items altered: %+v", out)
	}
}

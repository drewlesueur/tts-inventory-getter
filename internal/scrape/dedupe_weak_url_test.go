package scrape

import (
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

// Sites that render cards AND ship a JSON blob emit each vehicle twice. The
// blob copy keys on its stock number while the card copy — carrying neither
// stock nor VIN — keys on the URL, so the two never collide: drivenmotion.com
// returned 826 items for 413 cars.
func TestDedupeMergesWeakCardCopyIntoBlobCopy(t *testing.T) {
	const vdp = "https://www.drivenmotion.com/used-cars/2015-aston-martin-rapide-s-SCFHMDBS4FGF04519"
	got := Dedupe([]model.InventoryItem{
		{URL: vdp, Title: "2015 Aston Martin Rapide S", Price: "$84,984"}, // card copy
		{URL: vdp, Title: "2015 Aston Martin Rapide S Sedan 4D", StockID: "C3106AI",
			VIN: "SCFHMDBS4FGF04519", Price: "84984", Mileage: "12098"}, // blob copy
	})
	if len(got) != 1 {
		t.Fatalf("expected the two copies to merge into 1 item, got %d", len(got))
	}
	if got[0].VIN != "SCFHMDBS4FGF04519" || got[0].StockID != "C3106AI" {
		t.Errorf("identified fields lost in merge: %+v", got[0])
	}
}

// Two genuinely different vehicles must never be merged just because both lack
// identity fields and neither may swallow an identified one.
func TestDedupeWeakURLMergeKeepsDistinctVehicles(t *testing.T) {
	got := Dedupe([]model.InventoryItem{
		{URL: "https://d.test/a", StockID: "A1", VIN: "1HGCM82633A004352"},
		{URL: "https://d.test/b", StockID: "B2", VIN: "JH4KA9650MC000111"},
	})
	if len(got) != 2 {
		t.Fatalf("distinct vehicles were merged: %d items", len(got))
	}

	// A weak item on a URL nobody strong claims stays on its own.
	got = Dedupe([]model.InventoryItem{
		{URL: "https://d.test/a", StockID: "A1"},
		{URL: "https://d.test/orphan", Title: "2020 Ford F-150"},
	})
	if len(got) != 2 {
		t.Fatalf("orphan weak item was dropped: %+v", got)
	}
}

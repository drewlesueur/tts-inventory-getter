package scrape

import (
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestNormalizeItem_KeepsPlaceholderPrimaryImage(t *testing.T) {
	item := NormalizeItem("https://dealer.test/inventory", model.InventoryItem{
		PrimaryImage: "https://dealer.test/images/picture-placeholder-coming-soon.jpg",
		Images:       []string{"https://dealer.test/images/picture-placeholder-coming-soon.jpg"},
	})
	if item.PrimaryImage == "" {
		t.Fatalf("expected placeholder image to be kept as primary image")
	}
}

func TestNormalizeItem_KeepsNewArrivalPrimaryImage(t *testing.T) {
	item := NormalizeItem("https://dealer.test/inventory", model.InventoryItem{
		PrimaryImage: "https://dealer.test/assets/new-arrival",
		Images:       []string{"https://dealer.test/assets/new-arrival"},
	})
	if item.PrimaryImage == "" {
		t.Fatalf("expected new-arrival image to be kept as primary image")
	}
}

func TestNormalizeItem_KeepsNewArrivalUnderscorePrimaryImage(t *testing.T) {
	item := NormalizeItem("https://www.idealcarsaz.com/used-cars-in-mesa-az/", model.InventoryItem{
		PrimaryImage: "https://static.overfuel.com/dealers/ideal-cars/image/new_arrival_graphic_ideal_cars.webp?w=1920&q=80",
		Images:       []string{"https://static.overfuel.com/dealers/ideal-cars/image/new_arrival_graphic_ideal_cars.webp?w=1920&q=80"},
	})
	if item.PrimaryImage == "" {
		t.Fatalf("expected new_arrival placeholder image to be kept as primary image")
	}
}

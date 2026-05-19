package scrape

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

func TestDOMExtractor_UsesSrcsetForPrimaryImage(t *testing.T) {
	html := `<div class="vehicle-card">
		<a href="/vehicle-details/car-1/">View</a>
		<h2>2019 Nissan Versa</h2>
		<span class="stock">C73921</span>
		<img srcset="https://dealer.test/new-arrival.jpg 640w, https://dealer.test/new-arrival-2.jpg 1024w" />
	</div>`

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.StockSelector = ".stock"
	site.ListPage.ImageSelector = "img"

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, "https://dealer.test/inventory", site)
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %+v", errs)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].PrimaryImage != "https://dealer.test/new-arrival.jpg" {
		t.Fatalf("unexpected primary image: %q", items[0].PrimaryImage)
	}
}

func TestDOMExtractor_PicksBestImageFromCard(t *testing.T) {
	html := `<div class="vehicle-card">
		<a href="/vehicle-details/car-2/">View</a>
		<h2>2020 Nissan Kicks</h2>
		<span class="stock">C39575</span>
		<img src="https://dealer.test/assets/logo.svg" alt="logo" />
		<img alt="New arrival - photos coming soon" src="https://static.overfuel.com/dealers/ideal-cars/image/new_arrival_graphic_ideal_cars.webp?w=1920&q=80" />
	</div>`

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.StockSelector = ".stock"
	site.ListPage.ImageSelector = "img"

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, "https://dealer.test/inventory", site)
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %+v", errs)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].PrimaryImage == "" {
		t.Fatalf("expected primary image to be selected")
	}
	if items[0].PrimaryImage != "https://static.overfuel.com/dealers/ideal-cars/image/new_arrival_graphic_ideal_cars.webp?w=1920&q=80" {
		t.Fatalf("unexpected primary image: %q", items[0].PrimaryImage)
	}
}

func TestDOMExtractor_UsesBackgroundImageWhenImgMissing(t *testing.T) {
	html := `<div class="vehicle-card" style="background-image: url('https://static.overfuel.com/dealers/ideal-cars/image/new_arrival_graphic_ideal_cars.webp?w=1920&q=80');">
		<a href="/vehicle-details/car-3/">View</a>
		<h2>2019 Kia Soul</h2>
		<span class="stock">C01374</span>
	</div>`

	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".vehicle-card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.StockSelector = ".stock"
	site.ListPage.ImageSelector = "img"

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, "https://dealer.test/inventory", site)
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %+v", errs)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].PrimaryImage == "" {
		t.Fatalf("expected primary image from background-image")
	}
}

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

func TestPopulateDetailsFromHTML_ExtractsElementorGalleryLinks(t *testing.T) {
	html := `<html><body>
	<div class="elementor-widget-gallery">
		<a class="e-gallery-item elementor-gallery-item" href="https://www.txtcharlie.com/wp-content/uploads/2026/05/lamborghini-7026-1.jpg">
			<div class="e-gallery-image" data-thumbnail="https://www.txtcharlie.com/wp-content/uploads/2026/05/lamborghini-7026-1.jpg" data-width="1920" data-height="1280"></div>
		</a>
		<a class="e-gallery-item elementor-gallery-item" href="https://www.txtcharlie.com/wp-content/uploads/2026/05/lamborghini-7026-2.jpg">
			<div class="e-gallery-image" data-thumbnail="https://www.txtcharlie.com/wp-content/uploads/2026/05/lamborghini-7026-2.jpg" data-width="1920" data-height="1280"></div>
		</a>
	</div>
	</body></html>`

	site := config.SiteConfig{}
	site.DetailPage.ImageSelectors = []string{".e-gallery-image[data-thumbnail]"}

	item := model.InventoryItem{URL: "https://www.txtcharlie.com/vehicle-details/2024-lamborghini-revuelto-7026/"}
	out, err := populateDetailsFromHTML(context.Background(), NewImageSizeCache(), item, site, html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Images) != 2 {
		t.Fatalf("expected two gallery images, got %#v", out.Images)
	}
}

func TestPopulateDetailsFromHTML_ExtractsDealerSyncLazyGalleryImages(t *testing.T) {
	html := `<html><body>
	<div id="ds-vdp-photos">
		<a href="//images.dealersync.com/3174/Photos/1499788/first.jpg?format=webp" data-lightbox="vehicle-images">
			<img src="data:image/gif;base64,placeholder" data-src="//images.dealersync.com/3174/Photos/1499788/first.jpg" alt="Image #1" />
		</a>
		<a href="//images.dealersync.com/3174/Photos/1499788/second.jpg?format=webp" data-lightbox="vehicle-images">
			<img src="data:image/gif;base64,placeholder" data-src="//images.dealersync.com/3174/Photos/1499788/second.jpg" alt="Image #2" />
		</a>
	</div>
	</body></html>`

	site := config.SiteConfig{}
	site.DetailPage.ImageSelectors = []string{"#ds-vdp-photos img[data-src]"}

	item := model.InventoryItem{URL: "https://www.tucsonusedcarsandtrucks.com/pre-owned-cars/detail/2014-Cadillac-CTS-Sedan/1499788"}
	out, err := populateDetailsFromHTML(context.Background(), nil, item, site, html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Images) != 2 {
		t.Fatalf("expected two lazy-loaded gallery images, got %#v", out.Images)
	}
	if out.Images[0] != "https://images.dealersync.com/3174/Photos/1499788/first.jpg" &&
		out.Images[1] != "https://images.dealersync.com/3174/Photos/1499788/first.jpg" {
		t.Fatalf("expected resolved DealerSync gallery image, got %#v", out.Images)
	}
}

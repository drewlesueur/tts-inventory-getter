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

func TestPopulateDetailsFromHTML_ExtractsRichSpecsFromLabelGrid(t *testing.T) {
	html := `<html><body>
	<section>
		<div><strong>Drivetrain</strong><span>FWD</span></div>
		<div><strong>Fuel Type</strong><span>Gasoline</span></div>
		<div><strong>Fuel Capacity</strong><span>13 gallons</span></div>
		<div><strong>Front Wheel</strong><span>18.0 x 7.0</span></div>
		<div><strong>Rear Wheel</strong><span>18.0 x 7.0</span></div>
		<div><strong>Front Tire</strong><span>215/45R18 89W</span></div>
		<div><strong>Rear Tire</strong><span>215/45R18 89W</span></div>
		<div><strong>Passengers</strong><span>5</span></div>
	</section>
	</body></html>`

	item := model.InventoryItem{URL: "https://www.idealcarsaz.com/inventory/used-2018-mazda-mazda3"}
	out, err := populateDetailsFromHTML(context.Background(), nil, item, config.SiteConfig{}, html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.DriveType != "FWD" {
		t.Fatalf("expected driveType=FWD got %q", out.DriveType)
	}
	if out.FuelType != "Gasoline" || out.FuelCapacity != "13 gallons" {
		t.Fatalf("expected fuel fields, got fuelType=%q fuelCapacity=%q", out.FuelType, out.FuelCapacity)
	}
	if out.FrontWheel != "18.0 x 7.0" || out.RearWheel != "18.0 x 7.0" {
		t.Fatalf("expected wheel fields, got front=%q rear=%q", out.FrontWheel, out.RearWheel)
	}
	if out.FrontTire != "215/45R18 89W" || out.RearTire != "215/45R18 89W" {
		t.Fatalf("expected tire fields, got front=%q rear=%q", out.FrontTire, out.RearTire)
	}
	if out.PassengerCapacity != "5" {
		t.Fatalf("expected passengerCapacity=5 got %q", out.PassengerCapacity)
	}
}

func TestPopulateDetailsFromHTML_ExtractsMileageFromMilesLabel(t *testing.T) {
	html := `<html><body>
	<aside class="summary">
		<div><span>Miles:</span> <strong>110,779</strong></div>
		<div><span>Stock #:</span> <strong>26027</strong></div>
		<div><span>VIN:</span> <strong>5FRYD4H95GB010521</strong></div>
	</aside>
	</body></html>`

	item := model.InventoryItem{URL: "https://www.tucsonusedcarsandtrucks.com/pre-owned-cars/detail/2016-Acura-MDX/1454461"}
	out, err := populateDetailsFromHTML(context.Background(), nil, item, config.SiteConfig{}, html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Mileage != "110,779" {
		t.Fatalf("expected mileage=110,779 got %q", out.Mileage)
	}
}

func TestPopulateDetailsFromHTML_ExtractsRichSpecsFromFactTiles(t *testing.T) {
	html := `<html><body>
	<div class="facts">
		<div>V12</div>
		<div>COUPE</div>
		<div>3,042 MILES</div>
		<div>17 MPG</div>
		<div>12 CYLINDER</div>
		<div>8 SPEED DUAL CLUTCH</div>
		<div>AWD DRIVE TRAIN</div>
		<div>26 HWY MPG</div>
		<div>19 CITY MPG</div>
	</div>
	</body></html>`

	item := model.InventoryItem{URL: "https://www.txtcharlie.com/vehicle-details/2024-lamborghini-revuelto-7026/"}
	out, err := populateDetailsFromHTML(context.Background(), nil, item, config.SiteConfig{}, html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Engine != "V12" {
		t.Fatalf("expected engine=V12 got %q", out.Engine)
	}
	if out.BodyType != "COUPE" {
		t.Fatalf("expected bodyType=COUPE got %q", out.BodyType)
	}
	if out.Mileage != "3,042 miles" {
		t.Fatalf("expected mileage=3,042 miles got %q", out.Mileage)
	}
	if out.FuelEconomy != "17 MPG" {
		t.Fatalf("expected fuelEconomy=17 MPG got %q", out.FuelEconomy)
	}
	if out.MilesPerGallon != "17 MPG" || out.MilesPerLiter != "4.49 mi/L" {
		t.Fatalf("expected mpg/mpl conversion, got mpg=%q mpl=%q", out.MilesPerGallon, out.MilesPerLiter)
	}
	if out.Cylinders != "12 CYLINDER" {
		t.Fatalf("expected cylinders=12 CYLINDER got %q", out.Cylinders)
	}
	if out.Transmission != "8 SPEED DUAL CLUTCH" {
		t.Fatalf("expected transmission=8 SPEED DUAL CLUTCH got %q", out.Transmission)
	}
	if out.DriveType != "AWD DRIVE TRAIN" {
		t.Fatalf("expected driveType=AWD DRIVE TRAIN got %q", out.DriveType)
	}
	if out.HighwayMPG != "26 MPG" || out.CityMPG != "19 MPG" {
		t.Fatalf("expected city/highway mpg, got city=%q highway=%q", out.CityMPG, out.HighwayMPG)
	}
	if out.HighwayMPL != "6.87 mi/L" || out.CityMPL != "5.02 mi/L" {
		t.Fatalf("expected city/highway mpl, got city=%q highway=%q", out.CityMPL, out.HighwayMPL)
	}
}

func TestFindVINInTextSkipsCSSClassTokens(t *testing.T) {
	text := `classHeaderNested VIN 3ALACWFCXKDKB9140`
	if got := findVINInText(text); got != "3ALACWFCXKDKB9140" {
		t.Fatalf("expected real VIN, got %q", got)
	}
	if got := findVINInText("classHeaderNested"); got != "" {
		t.Fatalf("expected CSS token to be rejected, got %q", got)
	}
}

// goquery's .Text() includes <style> contents, so the spec sweeps were mining
// CSS: an inline font stack with "Apple Color Emoji" became color: Emoji.
func TestPopulateDetailsFromHTML_IgnoresStyleAndScriptText(t *testing.T) {
	html := `<html><head><style>
	  :root{--font-family-sans-serif:-apple-system,"Segoe UI",Roboto,"Apple Color Emoji","Noto Color Emoji"}
	</style></head><body>
	  <script>var transmission = "Engine: bogus";</script>
	  <div class="stock-number-field"><span class="stock">Stock: </span><span>106912</span></div>
	  <div><span>Mileage: </span><span>2,717 Miles</span></div>
	</body></html>`

	site := config.SiteConfig{}
	site.DetailPage.StockSelector = ".stock-number-field"

	out, err := populateDetailsFromHTML(context.Background(), nil,
		model.InventoryItem{URL: "https://dealer.test/viewdetails/new/x/y"}, site, html)
	if err != nil {
		t.Fatal(err)
	}
	out = NormalizeItem("https://dealer.test", out)
	if out.StockID != "106912" {
		t.Fatalf("stock = %q", out.StockID)
	}
	if out.Color != "" {
		t.Fatalf("color mined from CSS: %q", out.Color)
	}
	if out.Mileage != "2,717 miles" {
		t.Fatalf("mileage = %q", out.Mileage)
	}
}

// Detail fetching runs for every site, so it must not fire a request when the
// listing already carries everything the detail page would supply.
func TestDetailFetchWouldAddNothing(t *testing.T) {
	complete := model.InventoryItem{
		Title: "2026 Ford Maverick XL", URL: "https://d.test/v/1",
		VIN: "3FTTW8BA6TRA65715", StockID: "106912",
		Price: "$29,483", Mileage: "2,717", PrimaryImage: "https://d.test/a.jpg",
	}
	if !detailFetchWouldAddNothing(complete) {
		t.Fatal("a complete listing should not trigger a detail fetch")
	}
	// Any missing core field must still trigger one.
	for name, mutate := range map[string]func(*model.InventoryItem){
		"vin":     func(i *model.InventoryItem) { i.VIN = "" },
		"stock":   func(i *model.InventoryItem) { i.StockID = "" },
		"price":   func(i *model.InventoryItem) { i.Price = "" },
		"mileage": func(i *model.InventoryItem) { i.Mileage = "" },
		"image":   func(i *model.InventoryItem) { i.PrimaryImage = "" },
		"title":   func(i *model.InventoryItem) { i.Title = "" },
	} {
		it := complete
		mutate(&it)
		if detailFetchWouldAddNothing(it) {
			t.Fatalf("missing %s should still trigger a detail fetch", name)
		}
	}
	// Specs are refinements, not triggers.
	noSpecs := complete
	noSpecs.Engine, noSpecs.Color, noSpecs.DriveType = "", "", ""
	if !detailFetchWouldAddNothing(noSpecs) {
		t.Fatal("absent specs should not force a detail fetch")
	}
}

// An empty "Stock # " cell concatenates with the following text, so the model
// year gets captured as the stock number.
func TestFindStockIDInText_RejectsImplausibleCaptures(t *testing.T) {
	for _, in := range []string{
		"Stock # 2018 Honda CR-V EX", "Stock # ", "Stock Number:", "Stock #",
		"Stock # CERTIFIED PRE-OWNED", "Stock Number", // empty cell running into the next word
	} {
		if got := findStockIDInText(in); got != "" {
			t.Fatalf("%q -> %q, want empty", in, got)
		}
	}
	for in, want := range map[string]string{
		"Stock # K14394A": "K14394A",
		"Stock: P558569":  "P558569",
		"Stock 106912":    "106912",
	} {
		if got := findStockIDInText(in); got != want {
			t.Fatalf("%q -> %q, want %q", in, got, want)
		}
	}
}

package scrape

import (
	"strings"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestNormalizeItem(t *testing.T) {
	in := model.InventoryItem{Title: " 2021   Toyota   Camry ", URL: "/inventory/a", Images: []string{"/img/a.jpg"}}
	out := NormalizeItem("https://dealer.test", in)
	if out.Year != "2021" {
		t.Fatalf("expected year 2021 got %s", out.Year)
	}
	if out.Make != "Toyota" {
		t.Fatalf("expected make Toyota got %s", out.Make)
	}
	if out.URL != "https://dealer.test/inventory/a" {
		t.Fatalf("unexpected url %s", out.URL)
	}
	if out.PrimaryImage != "https://dealer.test/img/a.jpg" {
		t.Fatalf("unexpected primary image %s", out.PrimaryImage)
	}
}

func TestDedupe(t *testing.T) {
	items := []model.InventoryItem{{URL: "u1"}, {URL: "u1"}, {StockID: "s1", Images: []string{"i1"}}, {StockID: "s1", Images: []string{"i1"}}}
	out := Dedupe(items)
	if len(out) != 2 {
		t.Fatalf("expected 2 got %d", len(out))
	}
}

func TestNormalizeItem_NormalizesStockIDLabel(t *testing.T) {
	in := model.InventoryItem{StockID: " Stock # 26027 "}
	out := NormalizeItem("https://dealer.test", in)
	if out.StockID != "26027" {
		t.Fatalf("expected stock 26027 got %q", out.StockID)
	}
}

// DealerCarSearch renders the mileage cell as "<label>Mileage:</label> 93,821",
// so the selector text arrives carrying the field label.
func TestNormalizeItem_StripsMileageLabel(t *testing.T) {
	for _, raw := range []string{"Mileage: 93,821", " Miles - 93,821 ", "Odometer 93,821", "93,821"} {
		out := NormalizeItem("https://dealer.test", model.InventoryItem{Mileage: raw})
		if out.Mileage != "93,821" {
			t.Fatalf("mileage %q normalized to %q, want 93,821", raw, out.Mileage)
		}
	}
}

func TestNormalizeItem_UsesNumericListingIDWhenStockIsMissing(t *testing.T) {
	tests := []struct {
		name    string
		stockID string
	}{
		{name: "empty"},
		{name: "not available", stockID: "N/A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := model.InventoryItem{
				StockID: tt.stockID,
				URL:     "/used-trucks/for-sale/example/2022/2191308/",
			}
			out := NormalizeItem("https://dealer.test/inventory/", in)
			if out.StockID != "2191308" {
				t.Fatalf("expected listing ID fallback 2191308 got %q", out.StockID)
			}
		})
	}
}

func TestNormalizeItem_DoesNotUseNonNumericURLSlugAsStockID(t *testing.T) {
	in := model.InventoryItem{URL: "/inventory/example-truck/"}
	out := NormalizeItem("https://dealer.test", in)
	if out.StockID != "" {
		t.Fatalf("expected empty stock ID got %q", out.StockID)
	}
}

// Vue/Alpine SSR pages bind the real image to :src and ship no plain src.
func TestNormalizeItem_ReadsVueBoundImageSrc(t *testing.T) {
	for _, tc := range []struct{ attr, want string }{
		{`'https://img.test/a.jpg'`, "https://img.test/a.jpg"},
		{`"/media/b.jpg"`, "/media/b.jpg"},
		{`getImage(vin)`, ""},
		{`'https://img.test/' + vin`, ""},
	} {
		if got := unquoteBoundAttr(tc.attr); got != tc.want {
			t.Fatalf("unquoteBoundAttr(%q) = %q, want %q", tc.attr, got, tc.want)
		}
	}
}

// Many platforms put the VIN in the VDP slug and nowhere in the card markup.
func TestNormalizeItem_TakesVINFromURLPath(t *testing.T) {
	in := model.InventoryItem{URL: "/viewdetails/new/3fttw8ba6tra65715/2026-ford-maverick-crew-cab-pickup?type=cash"}
	out := NormalizeItem("https://dealer.test", in)
	if out.VIN != "3FTTW8BA6TRA65715" {
		t.Fatalf("vin = %q", out.VIN)
	}
}

// An ordinary slug must not be mistaken for a VIN, and a real VIN must win.
func TestNormalizeItem_IgnoresNonVINSlugs(t *testing.T) {
	for _, u := range []string{
		"/inventory/2026-ford-bronco-sport/",
		"/used/tesla/model-s-plaid-hatchback/",
	} {
		out := NormalizeItem("https://dealer.test", model.InventoryItem{URL: u})
		if out.VIN != "" {
			t.Fatalf("url %q produced vin %q", u, out.VIN)
		}
	}
	// An explicit VIN is never overwritten.
	out := NormalizeItem("https://dealer.test", model.InventoryItem{
		VIN: "5YJSA1E67TF558569", URL: "/viewdetails/new/3fttw8ba6tra65715/x"})
	if out.VIN != "5YJSA1E67TF558569" {
		t.Fatalf("existing vin was overwritten: %q", out.VIN)
	}
}

// Detail pages render specs as "<span>Engine: </span><span>value</span>", so the
// extracted text carries the label.
func TestNormalizeItem_StripsSpecFieldLabels(t *testing.T) {
	in := model.InventoryItem{
		Engine:       "Engine: Intercooled Turbo Premium Gasoline I-4 2.0 L/122",
		Transmission: "Transmission: CVT",
		DriveType:    "Drive Type: AWD",
		Cylinders:    "Cylinders: 6",
		FuelType:     "Fuel Type: Gasoline",
		Color:        "Exterior Color: Frost Blue",
		BodyType:     "Body Style: Hatchback",
	}
	out := NormalizeItem("https://dealer.test", in)
	for field, got := range map[string]string{
		"engine":       out.Engine,
		"transmission": out.Transmission,
		"driveType":    out.DriveType,
		"fuelType":     out.FuelType,
		"color":        out.Color,
		"bodyType":     out.BodyType,
	} {
		if strings.Contains(strings.ToLower(got), ":") {
			t.Fatalf("%s kept its label: %q", field, got)
		}
	}
	if out.Engine != "Intercooled Turbo Premium Gasoline I-4 2.0 L/122" {
		t.Fatalf("engine = %q", out.Engine)
	}
	// "Drive Train" (two words) is a distinct label from "Drive Type"/"Drivetrain".
	if got := NormalizeItem("https://dealer.test", model.InventoryItem{DriveType: "Drive Train: AWD"}).DriveType; got != "AWD" {
		t.Fatalf("drive train label kept: %q", got)
	}
	if out.DriveType != "AWD" || out.Color != "Frost Blue" {
		t.Fatalf("drive=%q color=%q", out.DriveType, out.Color)
	}
}

// The colon is required, so a genuine value is never truncated.
func TestNormalizeItem_KeepsSpecValuesWithoutLabels(t *testing.T) {
	in := model.InventoryItem{Engine: "Turbo Gasoline I-4", Transmission: "Automatic", Color: "Gray"}
	out := NormalizeItem("https://dealer.test", in)
	if out.Engine != "Turbo Gasoline I-4" || out.Transmission != "Automatic" || out.Color != "Gray" {
		t.Fatalf("values altered: %q %q %q", out.Engine, out.Transmission, out.Color)
	}
}

// Cards often render the stock cell as a bare "# K14394A".
func TestNormalizeItem_StripsBareHashFromStockID(t *testing.T) {
	for raw, want := range map[string]string{
		"# K14394A":       "K14394A",
		"#K14394A":        "K14394A",
		"Stock # K14394A": "K14394A",
		"K14394A":         "K14394A",
	} {
		got := NormalizeItem("https://d.test", model.InventoryItem{StockID: raw}).StockID
		if got != want {
			t.Fatalf("stock %q -> %q, want %q", raw, got, want)
		}
	}
}

// A stock cell that renders only its label must yield no stock id: dedupeKey
// prefers stock, so a shared placeholder would merge a whole page into one item.
func TestNormalizeItem_RejectsValuelessStockCell(t *testing.T) {
	for _, raw := range []string{"Stock # ", "Stock #", "Stock", "#", " - ", "", "Stock Number:"} {
		if got := NormalizeItem("https://d.test", model.InventoryItem{StockID: raw}).StockID; got != "" {
			t.Fatalf("stock %q -> %q, want empty", raw, got)
		}
	}
	// Real values are untouched.
	for raw, want := range map[string]string{"K14394A": "K14394A", "# K14394A": "K14394A", "Stock # P558569": "P558569"} {
		if got := NormalizeItem("https://d.test", model.InventoryItem{StockID: raw}).StockID; got != want {
			t.Fatalf("stock %q -> %q, want %q", raw, got, want)
		}
	}
}

// Thumbor-style CDNs serve photos from an opaque, extension-less key. Treating
// those as "not a vehicle image" discarded every photo on such a site.
func TestIsLikelyVehicleImageURL_AcceptsExtensionlessCDNKeys(t *testing.T) {
	for _, u := range []string{
		"https://d3vd6h5tjc937b.cloudfront.net/fit-in/640x480/filters:quality(72)/6u4opg1b8nmkajvso270s9s8p394",
		"https://cdn.test/resize/800x600/abc123",
	} {
		if !isLikelyVehicleImageURL(u) {
			t.Fatalf("rejected a CDN vehicle image: %s", u)
		}
	}
	// Junk must still be rejected.
	for _, u := range []string{
		"https://cdn.test/fit-in/100x100/logo_header",
		"https://cdn.test/assets/icon-phone.svg",
		"",
	} {
		if isLikelyVehicleImageURL(u) {
			t.Fatalf("accepted junk: %s", u)
		}
	}
}

// goquery .Text() glues sibling nodes together, so the next field's label can
// end up inside a value: "WhiteMiles: 43K" for what is really just "White".
func TestNormalizeItem_TruncatesValueAtNextFieldLabel(t *testing.T) {
	for raw, want := range map[string]string{
		"WhiteMiles: 43K":                 "White",
		"Exterior Color: WhiteMiles: 43K": "White",
		"AWDTransmission: Automatic":      "AWD",
		"White":                           "White",
		"Frost Blue":                      "Frost Blue",
	} {
		if got := NormalizeItem("https://d.test", model.InventoryItem{Color: raw}).Color; got != want {
			t.Fatalf("color %q -> %q, want %q", raw, got, want)
		}
	}
	// A real value containing a colon-free hyphenated token is untouched.
	if got := NormalizeItem("https://d.test", model.InventoryItem{Engine: "2.3L EcoBoost I-4"}).Engine; got != "2.3L EcoBoost I-4" {
		t.Fatalf("engine altered: %q", got)
	}
}

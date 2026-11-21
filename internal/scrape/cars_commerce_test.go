package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

type carsCommerceFixtureTransport func(*http.Request) (*http.Response, error)

func (f carsCommerceFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCarsCommerceUnstockedInTransit(t *testing.T) {
	const source = "https://dealer.example/new-vehicles/"
	configuredSite := config.SiteConfig{BaseURL: source, CarsCommerce: &config.CarsCommerceConfig{
		CCID: "1234", SearchKey: "fixture-search-key", VehicleType: "New",
	}}
	const stocked = `{"vin":"4S4GUHD68T3726848","stock":"6N5625","type":"New","vdp_url":"/inventory/new-vehicle"}`
	const unstocked = `{"vin":"4S4SLDA61T3152538","stock":"","type":"New","vdp_url":"/inventory/in-transit-vehicle","in_transit":"yes"}`
	const failure = "CARS_COMMERCE_FETCH_FAILED"
	for _, tc := range []struct {
		name       string
		optIn      bool
		secondPage string
		wantItems  int
		wantCode   string
	}{
		{"explicit in-transit exclusion", true, unstocked, 1, "CARS_COMMERCE_UNSTOCKED_IN_TRANSIT"},
		{"other dealer retains strict validation", false, unstocked, 0, failure},
		{"missing stock without in-transit status", true, strings.ReplaceAll(unstocked, "yes", "unknown"), 0, failure},
		{"duplicate VIN across pages", true, strings.ReplaceAll(unstocked, "4S4SLDA61T3152538", "4S4GUHD68T3726848"), 0, failure},
		{"stock collides with importer key", true, strings.ReplaceAll(unstocked, `"stock":""`, `"stock":"6N-5625"`), 0, failure},
		{"VIN is not assigned stock", true, strings.ReplaceAll(unstocked, `"stock":""`, `"stock":"4S4SLDA61T3152538"`), 0, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuredSite.CarsCommerce.SkipUnstockedInTransit = tc.optIn
			original := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = original })
			pages := 0
			http.DefaultTransport = carsCommerceFixtureTransport(func(req *http.Request) (*http.Response, error) {
				var query struct {
					Page   int      `json:"page"`
					Fields []string `json:"requestedFields"`
				}
				if err := json.NewDecoder(req.Body).Decode(&query); err != nil {
					t.Fatal(err)
				}
				pages++
				if query.Page != pages || strings.Contains(strings.Join(query.Fields, ","), "in_transit") != tc.optIn {
					t.Fatalf("unexpected provider request: %+v", query)
				}
				row, page := stocked, "1"
				if pages == 2 {
					row, page = tc.secondPage, "2"
				}
				body := `{"data":{"listings":[` + row + `]},"meta":{"pagination":{"current_page":` + page + `,"total_pages":2,"total":2,"count":1}}}`
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			result := (Service{}).ScrapeOnce(context.Background(), source, configuredSite)
			if pages != 2 || len(result.Items) != tc.wantItems || len(result.Errors) != 1 || result.Errors[0].Code != tc.wantCode {
				t.Fatalf("pages = %d, items = %d, errors = %+v", pages, len(result.Items), result.Errors)
			}
			if tc.wantItems == 1 && (result.Items[0].StockID != "6N5625" || result.Items[0].Website != result.Items[0].URL || !strings.Contains(result.Errors[0].Message, "1 of 2")) {
				t.Fatalf("stock number or exclusion count lost: %+v", result)
			}
		})
	}
}

func TestCarsCommerceVehicleTypeFacet(t *testing.T) {
	for _, tc := range []struct {
		name         string
		exact        bool
		vehicleTypes []string
		wantFacet    string
		responseType string
		wantError    bool
	}{
		{"legacy new", false, []string{"New"}, "type_slug", "New", false},
		{"legacy used", false, []string{"Used", "Certified Used"}, "type_slug", "Certified Used", false},
		{"exact new", true, []string{"New"}, "type", "New", false},
		{"courtesy row in exact response", true, []string{"New"}, "type", "CTP", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalTransport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			http.DefaultTransport = carsCommerceFixtureTransport(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				var search struct {
					FacetFilters map[string][]string `json:"facetFilters"`
				}
				if err := json.NewDecoder(req.Body).Decode(&search); err != nil {
					t.Fatal(err)
				}
				want := map[string][]string{tc.wantFacet: tc.vehicleTypes}
				if !reflect.DeepEqual(search.FacetFilters, want) {
					t.Fatalf("facets: got %v, want %v", search.FacetFilters, want)
				}
				body := fmt.Sprintf(`{"data":{"listings":[{"type":%q,"vin":"1GKEMPKS3VJ135707","stock":"TEST-STOCK","vdp_url":"https://example.test/inventory/vehicle/"}]},
					"meta":{"pagination":{"total":1,"count":1,"current_page":1,"total_pages":1}}}`, tc.responseType)
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			source := "https://example.test/new-vehicles/"
			site := config.SiteConfig{
				BaseURL: source,
				CarsCommerce: &config.CarsCommerceConfig{
					CCID: "1", SearchKey: "fixture-key", VehicleTypes: tc.vehicleTypes, ExactVehicleType: tc.exact,
				},
			}
			result := scrapeCarsCommerce(context.Background(), source, site, Options{})
			if tc.wantError {
				if len(result.Items) != 0 || len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Message, "outside") {
					t.Fatalf("out-of-category response yielded %d items and errors %v", len(result.Items), result.Errors)
				}
				return
			}
			if len(result.Items) != 1 || len(result.Errors) != 0 {
				t.Fatalf("expected one item with no errors, got %d items and errors %v", len(result.Items), result.Errors)
			}
		})
	}
}

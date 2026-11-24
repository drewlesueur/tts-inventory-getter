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

type carsCommerceTestTransport func(*http.Request) (*http.Response, error)

func (transport carsCommerceTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestCarsCommerceStockSortAndPageIdentity(t *testing.T) {
	const firstVIN = "5XYRKDJF0VG509793"
	const secondVIN = "KNDNC5KA4V6202828"
	const thirdVIN = "3GTPUCEKXPG293474"
	cases := []struct {
		name         string
		source       string
		primarySort  string
		primaryOrder string
		secondVIN    string
		secondStock  string
		failure      string
		tieBreaker   bool
		secondTotal  int
		excludeConflicts bool
	}{
		{name: "stock sort for tied prices", source: "https://www.seinerkiaslc.com/used-vehicles/", primarySort: "low_price", primaryOrder: "asc", secondVIN: secondVIN, secondStock: "K002", tieBreaker: true, secondTotal: 2},
		{name: "stock sort for tied model years", source: "https://www.seinerkiaslc.com/used-vehicles/?idx=year_high_to_low", primarySort: "year", primaryOrder: "desc", secondVIN: secondVIN, secondStock: "K002", tieBreaker: true, secondTotal: 2},
		{name: "existing dealer keeps one sort", source: "https://www.rivertonacura.com/new-vehicles/", primarySort: "low_price", primaryOrder: "asc", secondVIN: secondVIN, secondStock: "K002", secondTotal: 2},
		{name: "repeated VIN", secondVIN: firstVIN, secondStock: "K002", secondTotal: 2, tieBreaker: true, failure: "missing or duplicate vehicle identity"},
		{name: "different VIN with repeated stock number", secondVIN: secondVIN, secondStock: "K001", secondTotal: 2, tieBreaker: true, failure: "missing or duplicate vehicle identity"},
		{name: "exclude both rows sharing stock across pages", secondVIN: secondVIN, secondStock: "k-001", secondTotal: 3, tieBreaker: true, excludeConflicts: true},
		{name: "invalid VIN", secondVIN: "NOT-A-VIN", secondStock: "K002", secondTotal: 2, tieBreaker: true, failure: "missing or duplicate vehicle identity"},
		{name: "missing stock number", secondVIN: secondVIN, secondTotal: 2, tieBreaker: true, failure: "missing or duplicate vehicle identity"},
		{name: "changing pagination total", secondVIN: secondVIN, secondStock: "K002", secondTotal: 3, tieBreaker: true, failure: "incomplete or changing"},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			source := scenario.source
			if source == "" {
				source = "https://www.seinerkiaslc.com/used-vehicles/"
			}
			site := config.SiteConfig{BaseURL: strings.SplitN(source, "?", 2)[0], CarsCommerce: &config.CarsCommerceConfig{
				CCID: "5355869", SearchKey: "fixture-key", VehicleType: "Used",
				StockSortTieBreaker: scenario.tieBreaker, ExcludeConflictingStock: scenario.excludeConflicts,
			}}
			originalTransport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			pages := 0
			fixtureRow := func(vin, stock string) string {
				return fmt.Sprintf(`{"vin":%q,"stock":%q,"type":"Used","vdp_url":%q}`, vin, stock, "/inventory/"+vin+"/")
			}
			http.DefaultTransport = carsCommerceTestTransport(func(req *http.Request) (*http.Response, error) {
				var request struct {
					Page int
					Sort []map[string]string
				}
				if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
					return nil, err
				}
				pages++
				if request.Page != pages {
					t.Fatalf("got page %d, want %d", request.Page, pages)
				}
				if scenario.primarySort != "" {
					want := []map[string]string{{"field": scenario.primarySort, "order": scenario.primaryOrder}}
					if scenario.tieBreaker {
						want = append(want, map[string]string{"field": "stock", "order": "asc"})
					}
					if !reflect.DeepEqual(request.Sort, want) {
						t.Fatalf("got sort %v, want %v", request.Sort, want)
					}
				}
				count, total := 1, 2
				rows := fixtureRow(firstVIN, "K001")
				if scenario.excludeConflicts {
					total = 3
					if pages == 1 {
						count = 2
						rows += "," + fixtureRow(thirdVIN, "K003")
					}
				}
				if pages == 2 {
					rows, total = fixtureRow(scenario.secondVIN, scenario.secondStock), scenario.secondTotal
				}
				payload := fmt.Sprintf(`{"data":{"listings":[%s]},"meta":{"pagination":{"total":%d,"count":%d,"current_page":%d,"total_pages":2}}}`, rows, total, count, pages)
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			result := scrapeCarsCommerce(context.Background(), source, site, Options{})
			if scenario.excludeConflicts {
				if pages != 2 || len(result.Items) != 1 || result.Items[0].VIN != thirdVIN || result.Items[0].StockID != "K003" || len(result.Errors) != 2 {
					t.Fatalf("got %d pages, items %v and errors %v", pages, result.Items, result.Errors)
				}
				for index, vin := range []string{firstVIN, secondVIN} {
					issue := result.Errors[index]
					if issue.Code != "DUPLICATE_IDENTITY" || !strings.Contains(issue.ItemURL, vin) {
						t.Fatalf("unexpected conflict: %+v", issue)
					}
				}
				return
			}
			if scenario.failure != "" {
				if pages != 2 || len(result.Items) != 0 || len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Message, scenario.failure) {
					t.Fatalf("got %d pages, %d items and errors %v, want %q", pages, len(result.Items), result.Errors, scenario.failure)
				}
				return
			}
			if pages != 2 || len(result.Errors) != 0 || len(result.Items) != 2 {
				t.Fatalf("got %d pages, %d items and errors %v", pages, len(result.Items), result.Errors)
			}
			if result.Items[0].VIN != firstVIN || result.Items[0].StockID != "K001" ||
				result.Items[1].VIN != secondVIN || result.Items[1].StockID != "K002" {
				t.Fatalf("unexpected identity values: %+v", result.Items)
			}
		})
	}
}

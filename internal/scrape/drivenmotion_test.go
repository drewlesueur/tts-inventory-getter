package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDrivenMotionStoreFor(t *testing.T) {
	cases := []struct {
		url    string
		wantID int
		wantOK bool
	}{
		{"https://www.drivenmotion.com/inventory/thornton", 183, true},
		{"https://www.drivenmotion.com/inventory/greeley/", 184, true},
		{"https://www.drivenmotion.com/inventory/rio-rancho", 185, true},
		{"https://www.drivenmotion.com/inventory", 0, false},        // all-stores URL
		{"https://www.drivenmotion.com/inventory/denver", 0, false}, // unknown store
	}
	for _, c := range cases {
		id, _, ok := drivenMotionStoreFor(c.url)
		if ok != c.wantOK || id != c.wantID {
			t.Errorf("%s => id=%d ok=%v, want id=%d ok=%v", c.url, id, ok, c.wantID, c.wantOK)
		}
	}
}

// The store split lives or dies on the dealer_id[] filter: a bare dealer_id=
// is ignored by the site and returns every store.
func TestFetchDrivenMotionUsesBracketFilterAndExcludesOthers(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI())
		if r.URL.Query().Get("page") != "1" {
			// Second page: empty, ends the walk.
			_, _ = w.Write([]byte(`<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"inventory":{"meta":{"total":2},"results":[]}}}}</script>`))
			return
		}
		_, _ = w.Write([]byte(`<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"inventory":{"meta":{"total":2},"results":[
			{"vin":"1HGCM82633A004352","stocknumber":"T1","year":2020,"make":"Ford","model":"F-150","price":30000,"mileage":1000,"status":"active","dealer_id":183,"url":"/used-cars/a","photos":["https://img/1.jpg"]},
			{"vin":"JH4KA9650MC000111","stocknumber":"G1","year":2019,"make":"Kia","model":"Rio","price":9000,"mileage":50000,"status":"active","dealer_id":184,"url":"/used-cars/b","photos":[]},
			{"vin":"5NMSH13E87H038309","stocknumber":"S1","year":2018,"make":"Jeep","model":"Compass","price":12000,"mileage":40000,"status":"sold","dealer_id":183,"url":"/used-cars/c","photos":[]},
			{"vin":"WAUC8AFC0HN036568","stocknumber":"W1","year":2017,"make":"Audi","model":"A6","price":13000,"mileage":70000,"status":"active","wholesale":1,"dealer_id":183,"url":"/used-cars/d","photos":[]}
		]}}}}</script>`))
	}))
	defer srv.Close()

	got, err := fetchDrivenMotionInventoryHTML(context.Background(), srv.URL+"/inventory/thornton")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(asked) == 0 || !strings.Contains(asked[0], "dealer_id%5B%5D=183") {
		t.Errorf("expected the bracket filter dealer_id[]=183, asked %v", asked)
	}
	if n := strings.Count(got, `class="srp-card"`); n != 1 {
		t.Fatalf("expected only the 1 active Thornton car, got %d cards", n)
	}
	if !strings.Contains(got, "1HGCM82633A004352") {
		t.Error("the Thornton vehicle is missing")
	}
	for name, vin := range map[string]string{
		"another store's car": "JH4KA9650MC000111",
		"a sold car":          "5NMSH13E87H038309",
		"a wholesale car":     "WAUC8AFC0HN036568",
	} {
		if strings.Contains(got, vin) {
			t.Errorf("%s leaked into the store feed", name)
		}
	}
}

func TestFetchDrivenMotionUnknownStore(t *testing.T) {
	if _, err := fetchDrivenMotionInventoryHTML(context.Background(), "https://www.drivenmotion.com/inventory"); err == nil {
		t.Error("expected an error for the all-stores URL, got nil")
	}
}

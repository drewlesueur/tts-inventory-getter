package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const horneMotorsAPIBody = `{"vehicles":[
{"id":198,"vin":"5NMSH13E87H038309","stockNumber":"6182","year":2007,"make":"Hyundai","model":"Santa Fe","trim":"SE",
 "hornePrice":3800,"mileage":210883,"status":"available","exteriorColor":"Silver","drivetrain":"Front Wheel Drive",
 "photos":[{"url":"/public-objects/photos/abc","isPrimary":true,"sortOrder":0},{"url":"/public-objects/photos/def","sortOrder":1}]},
{"id":199,"vin":"1N4AL3AP5JC123456","stockNumber":"6221","year":2013,"make":"Kia","model":"Sorento","trim":"LX",
 "hornePrice":7995,"mileage":150000,"status":"available","photos":[]},
{"id":200,"vin":"JH4KA9650MC000111","stockNumber":"9999","year":2001,"make":"Acura","model":"TL","trim":"Base",
 "hornePrice":1500,"mileage":99999,"status":"sold","photos":[]}
],"total":3}`

func horneMotorsServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/inventory") {
			http.NotFound(w, r)
			return
		}
		// The sold filter must be requested, or the cache fills with sold cars
		// (the snbmotors SoldStatus trap).
		if r.URL.Query().Get("status") != "available" {
			t.Errorf("expected status=available, got %q", r.URL.RawQuery)
		}
		if r.URL.Query().Get("limit") != "500" {
			t.Errorf("expected limit=500, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestFetchHorneMotorsInventoryHTML(t *testing.T) {
	srv := horneMotorsServer(t, horneMotorsAPIBody, http.StatusOK)
	defer srv.Close()

	got, err := fetchHorneMotorsInventoryHTML(context.Background(), srv.URL+"/inventory")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := strings.Count(got, `class="hm-vehicle-card"`); n != 2 {
		t.Errorf("expected 2 available cards (sold one dropped), got %d", n)
	}
	if strings.Contains(got, "JH4KA9650MC000111") {
		t.Error("sold vehicle leaked into the card HTML")
	}
	for _, want := range []string{
		"5NMSH13E87H038309",
		`<span class="hm-stock">6182</span>`,
		`<span class="hm-price">$3800</span>`,
		`<span class="hm-mileage">210883</span>`,
		`href="/inventory/2007-hyundai-santa-fe-se-6182"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output", want)
		}
	}
	// Relative photo paths must be absolutised against the site origin.
	if !strings.Contains(got, srv.URL+"/public-objects/photos/abc") {
		t.Error("photo URL was not absolutised against the origin")
	}
	if !strings.Contains(got, `<script id="__NEXT_DATA__"`) {
		t.Error("missing __NEXT_DATA__ payload")
	}
}

func TestFetchHorneMotorsInventoryHTMLErrors(t *testing.T) {
	t.Run("empty inventory", func(t *testing.T) {
		srv := horneMotorsServer(t, `{"vehicles":[],"total":0}`, http.StatusOK)
		defer srv.Close()
		if _, err := fetchHorneMotorsInventoryHTML(context.Background(), srv.URL+"/inventory"); err == nil {
			t.Error("expected an error for an empty inventory, got nil")
		}
	})

	t.Run("api error status", func(t *testing.T) {
		srv := horneMotorsServer(t, `{}`, http.StatusInternalServerError)
		defer srv.Close()
		if _, err := fetchHorneMotorsInventoryHTML(context.Background(), srv.URL+"/inventory"); err == nil {
			t.Error("expected an error for a 500 response, got nil")
		}
	})
}

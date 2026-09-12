package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
)

func TestAvailableFilter(t *testing.T) {
	for _, page := range []int{1, 2, 10} {
		u, err := url.Parse(availablePageURL(page))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if q.Get("SoldStatus") != "AvailableVehicles" || q.Get("PageNumber") != strconv.Itoa(page) || q.Get("PageSize") != "100" {
			t.Fatalf("incorrect filtered pagination URL: %s", u)
		}
	}
	for _, tc := range []struct {
		html  string
		valid bool
	}{
		{`<select name="SoldStatus"><option selected value="AvailableVehicles">Available</option><option value="AllVehicles">All Statuses</option></select>`, true},
		{`<select name="SoldStatus"><option value="AvailableVehicles">Available</option><option value="AllVehicles">All Statuses</option></select>`, true},
		{`<select name="SoldStatus"><option value="AvailableVehicles">Available</option><option selected value="AllVehicles">All Statuses</option></select>`, false},
		{`<select name="SoldStatus"><option selected value="SoldVehicles">Sold</option></select>`, false},
		{`<p>Challenge</p>`, false},
	} {
		if err := verifyAvailableFilter(tc.html); (err == nil) != tc.valid {
			t.Errorf("filter validation returned %v for %s", err, tc.html)
		}
	}
}

func TestPagination(t *testing.T) {
	for _, tc := range []struct {
		name, html            string
		current, pages, total int
		bad                   bool
	}{
		{"normal", `<ul class="inventory-pagination"><li>Page 2 of 3</li></ul><input class="data-inventory-total-records" value="72">`, 2, 3, 72, false},
		{"single", `<div class="inventory-pagination">Page 1 of 1</div>`, 1, 1, 0, false},
		{"challenge", `<p>Please enable JS</p>`, 0, 0, 0, true},
		{"invalid", `<div class="inventory-pagination">Page 4 of 3</div>`, 0, 0, 0, true},
		{"bad total", `<div class="inventory-pagination">Page 1 of 2</div><input class="data-inventory-total-records" value="unknown">`, 0, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current, pages, total, err := pagination(tc.html)
			if (err != nil) != tc.bad || current != tc.current || pages != tc.pages || total != tc.total {
				t.Fatalf("got %d/%d total %d error %v", current, pages, total, err)
			}
		})
	}
}

func TestSNBParsers(t *testing.T) {
	html := `<li class="vehicle-snapshot"><h3 class="vehicle-snapshot__title"><a href="/details/used-2020-ford-f-150/123">2020 Ford F-150</a></h3><div class="vehicle-snapshot__main-info">$24,995</div><div class="mileage">65,000 miles</div></li>`
	items, errs := (scrape.DOMExtractor{}).Extract(context.Background(), html, sourceURL, site)
	if len(errs) != 0 || len(items) != 1 {
		t.Fatalf("items %v errors %v", items, errs)
	}
	if !validDetailURL(items[0].URL) || items[0].Year != "2020" {
		t.Fatalf("unexpected listing: %+v", items[0])
	}
	detail := `<div class="vdp-info-block__info-item"><div class="vdp-info-block__info-item-title">VIN</div><div class="vdp-info-block__info-item-description">1FTFW1ET1EFA12345</div></div>`
	item, err := (scrape.HTMLDetailFetcher{Fetcher: savedHTML(detail)}).FetchDetails(context.Background(), items[0], site)
	if err != nil || item.VIN != "1FTFW1ET1EFA12345" {
		t.Fatalf("detail: %+v error %v", item, err)
	}
}

func TestUploadCacheOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/scrape/sync" || r.Header.Get("X-Service-Key") != "test-key" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		var payload struct {
			URL        string
			Items      []model.InventoryItem
			SkipUpsert bool
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.URL != sourceURL || !payload.SkipUpsert || len(payload.Items) != 1 {
			t.Errorf("unexpected payload %+v", payload)
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	if err := upload(context.Background(), server.URL, "test-key", []model.InventoryItem{{Title: "Example"}}); err != nil {
		t.Fatal(err)
	}
}

func TestUploadRejectsFailureAndRedirect(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusInternalServerError, http.StatusTemporaryRedirect} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://127.0.0.1:1/should-not-follow")
			w.WriteHeader(code)
		}))
		err := upload(context.Background(), server.URL, "test-key", nil)
		server.Close()
		if err == nil {
			t.Fatalf("accepted HTTP %d", code)
		}
	}
}

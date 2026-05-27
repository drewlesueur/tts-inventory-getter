package inventoryapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestListPagesSupportsDataItemsEnvelope(t *testing.T) {
	client := listPagesClient(t, `{
		"status": 200,
		"message": "success",
		"data": {
			"items": [{"accountID":"account-1","dealershipId":"dealer-1","url":"https://dealer.test/inventory","ftp_sync":true,"scrape_sync":true,"scrapeFrequencyMinutes":10080,"schedule":{"type":"weekly"}}],
			"count": 1,
			"generatedAt": "2026-05-26T12:00:00Z"
		}
	}`)

	pages, err := client.ListPages(context.Background())
	if err != nil {
		t.Fatalf("ListPages returned error: %v", err)
	}
	if len(pages) != 1 || pages[0].AccountID != "account-1" || pages[0].DealershipID != "dealer-1" {
		t.Fatalf("unexpected pages: %#v", pages)
	}
	if pages[0].Schedule.Type != "weekly" {
		t.Fatalf("unexpected schedule: %#v", pages[0].Schedule)
	}
	if pages[0].ScrapeFrequencyMinutes != 10080 {
		t.Fatalf("unexpected frequency: %d", pages[0].ScrapeFrequencyMinutes)
	}
	if !pages[0].FTPSyncEnabled() || !pages[0].ScrapeSyncEnabled() {
		t.Fatalf("unexpected sync flags: ftp=%v scrape=%v", pages[0].FTPSync, pages[0].ScrapeSync)
	}
}

func TestListPagesSupportsLegacyDataArray(t *testing.T) {
	client := listPagesClient(t, `{
		"status": 200,
		"message": "success",
		"data": [{"accountID":"account-2","dealershipId":"dealer-2","url":"https://legacy.test/inventory"}]
	}`)

	pages, err := client.ListPages(context.Background())
	if err != nil {
		t.Fatalf("ListPages returned error: %v", err)
	}
	if len(pages) != 1 || pages[0].URL != "https://legacy.test/inventory" {
		t.Fatalf("unexpected pages: %#v", pages)
	}
	if pages[0].FTPSyncEnabled() {
		t.Fatalf("legacy page should not run FTP sync without explicit ftp_sync=true")
	}
	if !pages[0].ScrapeSyncEnabled() {
		t.Fatalf("legacy page should keep scraping when scrape_sync is omitted")
	}
}

func TestUpsertInventorySendsServiceKeyAndDealershipID(t *testing.T) {
	var gotHeader string
	var gotBody upsertRequest
	client := &Client{
		BaseURL:    "http://inventory.test",
		ServiceKey: "service-key",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			gotHeader = r.Header.Get("X-Service-Key")
			if r.URL.Path != "/upsertAccountInventory" {
				t.Errorf("unexpected request path: %s", r.URL.Path)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":200}`)),
			}, nil
		})},
	}

	err := client.UpsertInventory(context.Background(), "account-1", "dealer-1", []model.InventoryItem{{
		StockID: "STK-1001",
		Images:  []string{"https://example.com/images/accord-1.jpg"},
		Website: "https://example.com/inventory/accord",
	}})
	if err != nil {
		t.Fatalf("UpsertInventory returned error: %v", err)
	}
	if gotHeader != "service-key" {
		t.Fatalf("unexpected X-Service-Key: %q", gotHeader)
	}
	if gotBody.AccountID != "account-1" || gotBody.DealershipID != "dealer-1" {
		t.Fatalf("unexpected identifiers in body: %#v", gotBody)
	}
	if len(gotBody.Items) != 1 || gotBody.Items[0].StockID != "STK-1001" || len(gotBody.Items[0].Images) != 1 {
		t.Fatalf("unexpected items in body: %#v", gotBody.Items)
	}
}

func TestSyncAccountInventorySourcesSendsServiceKeyAndAccountID(t *testing.T) {
	var gotHeader string
	var gotBody syncAccountInventorySourcesRequest
	client := &Client{
		BaseURL:    "http://inventory.test",
		ServiceKey: "service-key",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			gotHeader = r.Header.Get("X-Service-Key")
			if r.URL.Path != "/syncAccountInventorySources" {
				t.Errorf("unexpected request path: %s", r.URL.Path)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":200}`)),
			}, nil
		})},
	}

	err := client.SyncAccountInventorySources(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("SyncAccountInventorySources returned error: %v", err)
	}
	if gotHeader != "service-key" {
		t.Fatalf("unexpected X-Service-Key: %q", gotHeader)
	}
	if gotBody.AccountID != "account-1" {
		t.Fatalf("unexpected accountID in body: %#v", gotBody)
	}
}

func listPagesClient(t *testing.T, payload string) *Client {
	t.Helper()
	return &Client{
		BaseURL: "http://inventory.test",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/getScrapePageURLList" {
				t.Errorf("unexpected request path: %s", r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(payload)),
			}, nil
		})},
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

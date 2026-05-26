package inventoryapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestListPagesSupportsDataItemsEnvelope(t *testing.T) {
	client := listPagesClient(t, `{
		"status": 200,
		"message": "success",
		"data": {
			"items": [{"accountID":"account-1","dealershipId":"dealer-1","url":"https://dealer.test/inventory","schedule":{"type":"weekly"}}],
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

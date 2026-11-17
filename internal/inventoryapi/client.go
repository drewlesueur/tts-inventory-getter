package inventoryapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
    "log"
    "github.com/google/uuid"
	"net/http"
	"strings"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type Client struct {
	BaseURL    string
	ServiceKey string
	HTTPClient *http.Client
}

type PageEntry struct {
	AccountID                     string `json:"accountID"`
	DealershipID                  string `json:"dealershipId"`
	URL                           string `json:"url"`
	FTPSync                       *bool  `json:"ftp_sync"`
	ScrapeSync                    *bool  `json:"scrape_sync"`
	ScrapeFrequencyMinutes        int    `json:"scrapeFrequencyMinutes"`
	SpreadsheetURL                string `json:"spreadsheetUrl"`
	SpreadsheetSync               *bool  `json:"spreadsheet_sync"`
	InventorySyncFrequencyMinutes int    `json:"inventorySyncFrequencyMinutes"`
	Schedule                      struct {
		Type string `json:"type"`
	} `json:"schedule"`
	SpreadsheetSchedule struct {
		Type string `json:"type"`
	} `json:"spreadsheetSchedule"`
}

func (p PageEntry) FTPSyncEnabled() bool {
	return p.FTPSync != nil && *p.FTPSync
}

func (p PageEntry) ScrapeSyncEnabled() bool {
	return p.ScrapeSync == nil || *p.ScrapeSync
}

func (p PageEntry) SpreadsheetSyncEnabled() bool {
	return p.SpreadsheetSync != nil && *p.SpreadsheetSync && strings.TrimSpace(p.SpreadsheetURL) != ""
}

type listResponse struct {
	Status  int         `json:"status"`
	Message string      `json:"message"`
	Data    pageEntries `json:"data"`
}

type pageEntries []PageEntry

func (p *pageEntries) UnmarshalJSON(data []byte) error {
	var entries []PageEntry
	if err := json.Unmarshal(data, &entries); err == nil {
		*p = entries
		return nil
	}

	var wrapped struct {
		Items []PageEntry `json:"items"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return fmt.Errorf("decode page entries: %w", err)
	}
	if wrapped.Items == nil {
		return fmt.Errorf("decode page entries: expected array or object containing items")
	}
	*p = wrapped.Items
	return nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) baseURL() string {
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) ListPages(ctx context.Context) ([]PageEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/api/inventory/sources", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Service-Key", c.ServiceKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("getScrapePageURLList status=%d body=%s", resp.StatusCode, string(body))
	}
	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return []PageEntry(out.Data), nil
}

type upsertRequest struct {
	AccountID    string                `json:"accountID"`
	DealershipID string                `json:"dealershipId"`
	Items        []model.InventoryItem `json:"items"`
    ActivityID string `json:"activityId,omitempty"`
    ActivityTrigger string `json:"activityTrigger,omitempty"`
    ActivitySourceURL string `json:"activitySourceUrl,omitempty"`
}

type syncAccountInventorySourcesRequest struct {
	AccountID string `json:"accountID"`
}

func (c *Client) SyncAccountInventorySources(ctx context.Context, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil
	}
	body, err := json.Marshal(syncAccountInventorySourcesRequest{AccountID: accountID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/api/inventory/sync", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", c.ServiceKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("syncAccountInventorySources status=%d body=%s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *Client) UpsertInventory(ctx context.Context, accountID, dealershipID string, items []model.InventoryItem) error {
    return c.UpsertInventoryWithActivity(ctx, accountID, dealershipID, items, "service", "")
}

func (c *Client) UpsertInventoryWithActivity(ctx context.Context, accountID, dealershipID string, items []model.InventoryItem, trigger, sourceURL string) (outcome error) {
    event := Activity{ID: uuid.NewString(), Phase: "import", Trigger: trigger, SourceURL: sourceURL, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: "success", ItemCount: len(items)}
    defer func() {
        if outcome != nil { if event.Status != "unknown" { event.Status = "failed" }; event.Message = outcome.Error(); event.ItemCount = 0 }
        if err := c.ReportActivity(dealershipID, accountID, event); err != nil { log.Printf("Inventory import activity report failed: %v", err) }
    }()

	if len(items) == 0 {
        event.Status = "skipped"; event.Message = "No usable inventory rows to import"
		return nil
	}
	body, err := json.Marshal(upsertRequest{AccountID: accountID, DealershipID: dealershipID, Items: items, ActivityID: event.ID, ActivityTrigger: trigger, ActivitySourceURL: sourceURL})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/upsertAccountInventory", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", c.ServiceKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
        event.Status = "unknown"
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body,4096))
		return fmt.Errorf("upsertAccountInventory status=%d body=%s", resp.StatusCode, string(b))
	}
    var result struct {
        Status json.RawMessage `json:"status"`
        Message string `json:"message"`
        Data struct { Created *int `json:"created"`; Updated *int `json:"updated"`; Skipped int `json:"skipped"` } `json:"data"`
    }
    if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil { event.Status = "unknown"; return fmt.Errorf("invalid inventory import acknowledgement: %w", err) }
    if string(result.Status) != "200" && string(result.Status) != `"200"` { return fmt.Errorf("inventory import status=%s: %s", result.Status, result.Message) }
    if result.Data.Created != nil && result.Data.Updated != nil { event.ItemCount = *result.Data.Created + *result.Data.Updated }
    if result.Data.Skipped > 0 { event.Status = "partial_success"; event.Message = fmt.Sprintf("%d rows skipped", result.Data.Skipped) }
    return nil
}

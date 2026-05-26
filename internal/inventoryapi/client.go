package inventoryapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type PageEntry struct {
	AccountID    string `json:"accountID"`
	DealershipID string `json:"dealershipId"`
	URL          string `json:"url"`
	Schedule     struct {
		Type string `json:"type"`
	} `json:"schedule"`
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/getScrapePageURLList", nil)
	if err != nil {
		return nil, err
	}
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
	AccountID string                `json:"accountID"`
	Items     []model.InventoryItem `json:"items"`
}

func (c *Client) UpsertInventory(ctx context.Context, accountID string, items []model.InventoryItem) error {
	if len(items) == 0 {
		return nil
	}
	body, err := json.Marshal(upsertRequest{AccountID: accountID, Items: items})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/upsertAccountInventory", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upsertAccountInventory status=%d body=%s", resp.StatusCode, string(b))
	}
	return nil
}

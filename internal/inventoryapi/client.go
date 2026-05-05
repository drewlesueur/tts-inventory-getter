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
}

type listResponse struct {
	Status  int         `json:"status"`
	Message string      `json:"message"`
	Data    []PageEntry `json:"data"`
}

type ImageUpdate struct {
	StockID string   `json:"stockId"`
	Images  []string `json:"images"`
}

type updateRequest struct {
	AccountID string        `json:"accountID"`
	Items     []ImageUpdate `json:"items"`
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
	return out.Data, nil
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

func (c *Client) UpdateImages(ctx context.Context, accountID string, items []ImageUpdate) error {
	if len(items) == 0 {
		return nil
	}
	body, err := json.Marshal(updateRequest{AccountID: accountID, Items: items})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/updateAccountInventoryImages", bytes.NewReader(body))
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
		return fmt.Errorf("updateAccountInventoryImages status=%d body=%s", resp.StatusCode, string(b))
	}
	return nil
}

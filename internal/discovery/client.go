package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/inventory-scraper/internal/config"
)

type Client struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

type discoveryResponse struct {
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

type proposal struct {
	ListPage struct {
		CardSelector    string `json:"cardSelector"`
		TitleSelector   string `json:"titleSelector"`
		URLSelector     string `json:"urlSelector"`
		StockSelector   string `json:"stockSelector"`
		PriceSelector   string `json:"priceSelector"`
		MileageSelector string `json:"mileageSelector"`
		ImageSelector   string `json:"imageSelector"`
	} `json:"listPage"`
	DetailPage struct {
		ImageSelectors []string `json:"imageSelectors"`
		VINSelector    string   `json:"vinSelector"`
	} `json:"detailPage"`
	Regex struct {
		Stock []string `json:"stock"`
		VIN   []string `json:"vin"`
	} `json:"regex"`
}

func (c Client) Discover(ctx context.Context, sourceURL, html string) (config.SiteConfig, error) {
	if c.APIKey == "" {
		return config.SiteConfig{}, fmt.Errorf("OPENAI_API_KEY missing")
	}
	if c.Model == "" {
		c.Model = "gpt-4.1-mini"
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.openai.com/v1/responses"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}

	html = truncate(html, 70000)
	input := "Analyze this dealership inventory HTML and propose robust CSS selectors for listing cards and detail-image extraction. Return JSON only. URL: " + sourceURL + "\nHTML:\n" + html

	payload := map[string]any{
		"model": c.Model,
		"input": []map[string]any{{
			"role":    "system",
			"content": []map[string]string{{"type": "input_text", "text": "You are an expert web scraper architect. Return only valid JSON."}},
		}, {
			"role":    "user",
			"content": []map[string]string{{"type": "input_text", "text": input}},
		}},
		"text": map[string]any{
			"format": map[string]any{"type": "json_object"},
		},
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(b))
	if err != nil {
		return config.SiteConfig{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return config.SiteConfig{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		return config.SiteConfig{}, fmt.Errorf("openai error: status=%d body=%s", resp.StatusCode, string(errBody))
	}

	var out discoveryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return config.SiteConfig{}, err
	}
	text := extractOutputText(out)
	if text == "" {
		return config.SiteConfig{}, fmt.Errorf("empty model response")
	}
	text = strings.TrimSpace(text)

	var p proposal
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		return config.SiteConfig{}, fmt.Errorf("invalid model json: %w", err)
	}

	site := config.SiteConfig{BaseURL: sourceURL}
	site.ListPage.CardSelector = fallback(p.ListPage.CardSelector, ".vehicle-card, .inventory-item, [class*='vehicle']")
	site.ListPage.TitleSelector = fallback(p.ListPage.TitleSelector, "h2, h3")
	site.ListPage.URLSelector = fallback(p.ListPage.URLSelector, "a")
	site.ListPage.StockSelector = fallback(p.ListPage.StockSelector, ".stock, [class*='stock']")
	site.ListPage.PriceSelector = fallback(p.ListPage.PriceSelector, ".price, [class*='price']")
	site.ListPage.MileageSelector = fallback(p.ListPage.MileageSelector, ".mileage, [class*='mileage']")
	site.ListPage.ImageSelector = fallback(p.ListPage.ImageSelector, "img")
	site.DetailPage.ImageSelectors = p.DetailPage.ImageSelectors
	if len(site.DetailPage.ImageSelectors) == 0 {
		site.DetailPage.ImageSelectors = []string{".gallery img", "img[data-src]", "img[src*='vehicle']"}
	}
	site.DetailPage.VINSelector = p.DetailPage.VINSelector
	site.Regex.Stock = p.Regex.Stock
	if len(site.Regex.Stock) == 0 {
		site.Regex.Stock = []string{`(?i)stock\\s*#?[:\\-]?\\s*([a-z0-9\\-]+)`}
	}
	site.Regex.VIN = p.Regex.VIN
	if len(site.Regex.VIN) == 0 {
		site.Regex.VIN = []string{`\\b([A-HJ-NPR-Z0-9]{17})\\b`}
	}

	return site, nil
}

func extractOutputText(r discoveryResponse) string {
	for _, o := range r.Output {
		for _, c := range o.Content {
			if c.Type == "output_text" || c.Text != "" {
				return c.Text
			}
		}
	}
	return ""
}

func fallback(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

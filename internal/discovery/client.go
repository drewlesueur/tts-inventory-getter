package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
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
		TotalSelector   string `json:"totalSelector"`
		MaxItems        int    `json:"maxItems"`
	} `json:"listPage"`
	Pagination struct {
		Type             string   `json:"type"`
		NextSelector     string   `json:"nextSelector"`
		LoadMoreSelector string   `json:"loadMoreSelector"`
		InfiniteScroll   bool     `json:"infiniteScroll"`
		ModeHint         string   `json:"modeHint"`
		Hints            []string `json:"hints"`
	} `json:"pagination"`
	DetailPage struct {
		ImageSelectors []string `json:"imageSelectors"`
		VINSelector    string   `json:"vinSelector"`
	} `json:"detailPage"`
	Regex struct {
		Stock []string `json:"stock"`
		VIN   []string `json:"vin"`
	} `json:"regex"`
	Discovery struct {
		Confidence float64  `json:"confidence"`
		Notes      string   `json:"notes"`
		TotalHints []string `json:"totalHints"`
	} `json:"discovery"`
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
	input := "Analyze this dealership inventory HTML and propose robust CSS selectors for listing and detail extraction, including pagination and inventory-total hints. Class names can be arbitrary, so infer selectors from structure and repeated item patterns, not only obvious names. Return JSON only. URL: " + sourceURL + "\nHTML:\n" + html

	payload := map[string]any{
		"model": c.Model,
		"input": []map[string]any{{
			"role":    "system",
			"content": []map[string]string{{"type": "input_text", "text": "You are an expert web scraper architect. Return only valid JSON. Prioritize resilient selectors that work even with random class names."}},
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
	site.ListPage.CardSelector = fallback(p.ListPage.CardSelector, inferCardSelector(html))
	site.ListPage.TitleSelector = fallback(p.ListPage.TitleSelector, "h2, h3")
	site.ListPage.URLSelector = fallback(p.ListPage.URLSelector, "a")
	site.ListPage.StockSelector = fallback(p.ListPage.StockSelector, "[data-stock], [data-stock-no], [itemprop='sku'], [class*='stock'], [id*='stock']")
	site.ListPage.PriceSelector = fallback(p.ListPage.PriceSelector, ".price, [class*='price']")
	site.ListPage.MileageSelector = fallback(p.ListPage.MileageSelector, ".mileage, [class*='mileage']")
	site.ListPage.ImageSelector = fallback(p.ListPage.ImageSelector, "img")
	site.ListPage.TotalSelector = strings.TrimSpace(p.ListPage.TotalSelector)
	site.ListPage.MaxItems = p.ListPage.MaxItems
	site.ListPage.Pagination.Type = strings.TrimSpace(p.Pagination.Type)
	site.ListPage.Pagination.NextSelector = strings.TrimSpace(p.Pagination.NextSelector)
	site.ListPage.Pagination.LoadMoreSelector = strings.TrimSpace(p.Pagination.LoadMoreSelector)
	site.ListPage.Pagination.InfiniteScroll = p.Pagination.InfiniteScroll
	site.ListPage.Pagination.ModeHint = strings.TrimSpace(p.Pagination.ModeHint)
	site.DetailPage.ImageSelectors = p.DetailPage.ImageSelectors
	if len(site.DetailPage.ImageSelectors) == 0 {
		site.DetailPage.ImageSelectors = []string{".gallery img", "img[data-src]", "img[src*='vehicle']"}
	}
	site.DetailPage.VINSelector = p.DetailPage.VINSelector
	site.Regex.Stock = p.Regex.Stock
	if len(site.Regex.Stock) == 0 {
		site.Regex.Stock = []string{`(?i)stock\s*#?[:\-]?\s*([a-z0-9\-]+)`}
	}
	site.Regex.VIN = p.Regex.VIN
	if len(site.Regex.VIN) == 0 {
		site.Regex.VIN = []string{`\b([A-HJ-NPR-Z0-9]{17})\b`}
	}
	site.Discovery.Confidence = p.Discovery.Confidence
	site.Discovery.Notes = strings.TrimSpace(p.Discovery.Notes)
	site.Discovery.TotalHints = append(site.Discovery.TotalHints, p.Discovery.TotalHints...)
	site.Discovery.PagingHints = append(site.Discovery.PagingHints, p.Pagination.Hints...)
	site.Discovery.DiscoveredAt = time.Now().UTC().Format(time.RFC3339)
	applyDealerSyncDiscoveryDefaults(html, &site)

	return site, nil
}

func applyDealerSyncDiscoveryDefaults(html string, site *config.SiteConfig) {
	if site == nil {
		return
	}
	if !strings.Contains(strings.ToLower(html), "ds-inventory-model") {
		return
	}
	if strings.TrimSpace(site.ListPage.CardSelector) == "" || strings.TrimSpace(site.ListPage.CardSelector) == "li" {
		site.ListPage.CardSelector = ".ds-vehicle-list-item, .srp-card, .vehicle-card, [class*='vehicle'][class*='item'], [class*='inventory'][class*='item']"
	}
	if strings.TrimSpace(site.ListPage.URLSelector) == "" || strings.TrimSpace(site.ListPage.URLSelector) == "a" {
		site.ListPage.URLSelector = "a[href*='/pre-owned-cars/detail/'], a[href*='/vehicle-details/'], a[href*='/inventory/']"
	}
	if strings.TrimSpace(site.ListPage.TitleSelector) == "" {
		site.ListPage.TitleSelector = "h1, h2, h3, h4, [itemprop='name']"
	}
	if len(site.ListPage.WaitSelectors) == 0 {
		site.ListPage.WaitSelectors = []string{
			".ds-vehicle-list-item, .srp-card, [class*='vehicle'][class*='item'], a[href*='/pre-owned-cars/detail/']",
		}
	}
	if strings.TrimSpace(site.ListPage.PriceSelector) == "" {
		site.ListPage.PriceSelector = ".price, [class*='price'], [class*='payment'], [class*='internet-price']"
	}
	site.ListPage.Pagination.InfiniteScroll = true
	if site.ListPage.Pagination.ScrollMaxAttempts <= 0 {
		site.ListPage.Pagination.ScrollMaxAttempts = 25
	}
	if site.ListPage.Pagination.ClickMaxAttempts <= 0 {
		site.ListPage.Pagination.ClickMaxAttempts = 30
	}
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

var validClassToken = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

func inferCardSelector(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ".inventory-item, article, li"
	}
	preferred := []string{
		"[data-listing-id]",
		"[data-vehicle-id]",
		"[itemtype*='Vehicle']",
		".inventory-item",
		".vehicle-card",
		"article",
		"li",
	}
	for _, sel := range preferred {
		nodes := doc.Find(sel)
		if nodes.Length() >= 2 && hasAnchorDensity(nodes) {
			return sel
		}
	}

	classCount := map[string]int{}
	doc.Find("div,article,li,section").Each(func(_ int, s *goquery.Selection) {
		if s.Find("a[href]").Length() == 0 {
			return
		}
		cls, ok := s.Attr("class")
		if !ok {
			return
		}
		for _, token := range strings.Fields(cls) {
			if !validClassToken.MatchString(token) {
				continue
			}
			classCount[token]++
		}
	})
	type kv struct {
		k string
		v int
	}
	top := make([]kv, 0, len(classCount))
	for k, v := range classCount {
		if v >= 3 {
			top = append(top, kv{k: k, v: v})
		}
	}
	sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
	if len(top) > 0 {
		limit := 2
		if len(top) < limit {
			limit = len(top)
		}
		out := make([]string, 0, limit)
		for i := 0; i < limit; i++ {
			out = append(out, "."+top[i].k)
		}
		return strings.Join(out, ", ")
	}
	return ".inventory-item, article, li"
}

func hasAnchorDensity(nodes *goquery.Selection) bool {
	total := nodes.Length()
	if total == 0 {
		return false
	}
	withAnchor := 0
	nodes.Each(func(_ int, s *goquery.Selection) {
		if s.Find("a[href]").Length() > 0 {
			withAnchor++
		}
	})
	return withAnchor >= 2 && withAnchor*2 >= total
}

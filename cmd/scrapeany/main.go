package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
)

type candidate struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}

type extractionResult struct {
	Website string          `json:"website"`
	Items   []extractedItem `json:"items"`
	Meta    extractionMeta  `json:"meta"`
}

type extractedItem struct {
	Name    string `json:"name"`
	Price   string `json:"price"`
	Summary string `json:"summary"`
	URL     string `json:"url"`
}

type extractionMeta struct {
	Notes      string `json:"notes"`
	Confidence string `json:"confidence"`
}

type responsesAPI struct {
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func main() {
	urlFlag := flag.String("url", "", "Target website URL")
	modelFlag := flag.String("model", getenv("OPENAI_MODEL", "gpt-5"), "OpenAI model")
	timeoutFlag := flag.Duration("timeout", 45*time.Second, "Page render timeout")
	maxFlag := flag.Int("max-candidates", 120, "Maximum candidate links/text blocks sent to AI")
	flag.Parse()

	if strings.TrimSpace(*urlFlag) == "" {
		exitErr(errors.New("-url is required"))
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		exitErr(errors.New("OPENAI_API_KEY is required"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	html, finalURL, err := renderWithRod(ctx, *urlFlag)
	if err != nil {
		exitErr(fmt.Errorf("render failed: %w", err))
	}

	candidates, err := collectCandidates(html, finalURL, *maxFlag)
	if err != nil {
		exitErr(fmt.Errorf("collect candidates failed: %w", err))
	}
	if len(candidates) == 0 {
		exitErr(errors.New("no candidates found in page"))
	}

	result, err := extractStructured(ctx, apiKey, *modelFlag, finalURL, candidates)
	if err != nil {
		exitErr(fmt.Errorf("structured extraction failed: %w", err))
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
}

func renderWithRod(ctx context.Context, targetURL string) (html string, finalURL string, err error) {
	browser, cancel := scrape.NewRodBrowser(true)
	defer cancel()
	html, err = browser.Render(ctx, targetURL, config.SiteConfig{})
	if err != nil {
		return "", "", err
	}
	return html, targetURL, nil
}

func collectCandidates(html, baseURL string, limit int) ([]candidate, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 120
	}

	spaceRe := regexp.MustCompile(`\s+`)
	items := make([]candidate, 0, limit)
	seen := map[string]bool{}

	doc.Find("a[href]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if len(items) >= limit {
			return false
		}
		href, ok := s.Attr("href")
		if !ok || strings.TrimSpace(href) == "" {
			return true
		}
		u := resolveURL(baseURL, href)
		if u == "" {
			return true
		}
		title := strings.TrimSpace(s.Text())
		contextText := strings.TrimSpace(s.Parent().Text())
		contextText = spaceRe.ReplaceAllString(contextText, " ")
		title = spaceRe.ReplaceAllString(title, " ")
		if title == "" && contextText == "" {
			return true
		}
		key := u + "|" + title + "|" + contextText
		if seen[key] {
			return true
		}
		seen[key] = true
		items = append(items, candidate{Title: title, URL: u, Text: truncate(contextText, 280)})
		return true
	})

	return items, nil
}

func extractStructured(ctx context.Context, apiKey, model, siteURL string, items []candidate) (extractionResult, error) {
	prompt := map[string]any{
		"site":       siteURL,
		"candidates": items,
		"task":       "Extract up to 30 concrete products/listings/articles that look most valuable to a buyer. Keep URL absolute.",
	}
	promptBytes, _ := json.Marshal(prompt)

	payload := map[string]any{
		"model": model,
		"input": []map[string]any{
			{
				"role": "system",
				"content": []map[string]string{{
					"type": "input_text",
					"text": "You extract structured website data. Return valid JSON only.",
				}},
			},
			{
				"role": "user",
				"content": []map[string]string{{
					"type": "input_text",
					"text": string(promptBytes),
				}},
			},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_schema",
				"name": "website_extraction",
				"schema": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"website": map[string]any{"type": "string"},
						"items": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"name":    map[string]any{"type": "string"},
									"price":   map[string]any{"type": "string"},
									"summary": map[string]any{"type": "string"},
									"url":     map[string]any{"type": "string"},
								},
								"required": []string{"name", "price", "summary", "url"},
							},
						},
						"meta": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"notes":      map[string]any{"type": "string"},
								"confidence": map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
							},
							"required": []string{"notes", "confidence"},
						},
					},
					"required": []string{"website", "items", "meta"},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(body))
	if err != nil {
		return extractionResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return extractionResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		return extractionResult{}, fmt.Errorf("openai responses error: status=%d body=%s", resp.StatusCode, string(errBody))
	}

	var parsed responsesAPI
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return extractionResult{}, err
	}
	text := extractText(parsed)
	if strings.TrimSpace(text) == "" {
		return extractionResult{}, errors.New("empty model output")
	}

	var out extractionResult
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return extractionResult{}, fmt.Errorf("invalid structured json: %w", err)
	}
	return out, nil
}

func extractText(r responsesAPI) string {
	for _, o := range r.Output {
		for _, c := range o.Content {
			if c.Type == "output_text" && strings.TrimSpace(c.Text) != "" {
				return c.Text
			}
		}
	}
	return ""
}

func resolveURL(baseURL, href string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	rel, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	return base.ResolveReference(rel).String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func getenv(k, fallback string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return fallback
	}
	return v
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

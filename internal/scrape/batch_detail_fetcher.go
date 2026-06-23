package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

// BatchDetailFetcher renders all detail pages in a single Camoufox session
// (one browser launch for N pages) via scripts/fetch_details.py, then extracts
// fields from each page's HTML. This avoids per-item browser launches and the
// DataDome-blocked Go-browser fallback that collapsed items.
type BatchDetailFetcher struct {
	ScriptPath  string
	PythonBin   string
	ImageSizes  *ImageSizeCache
	CookieStore *CookieStore
}

func NewBatchDetailFetcher(scriptPath, pythonBin string, sizes *ImageSizeCache, store *CookieStore) *BatchDetailFetcher {
	if pythonBin == "" {
		pythonBin = "python3"
	}
	return &BatchDetailFetcher{ScriptPath: scriptPath, PythonBin: pythonBin, ImageSizes: sizes, CookieStore: store}
}

type batchDetailResult struct {
	Results map[string]string `json:"results"`
	Cookie  string            `json:"cookie"`
	Error   string            `json:"error"`
}

// PrefetchAndPopulate fetches all detail pages in one session and returns items
// with detail fields populated. Items without a URL pass through unchanged.
func (b *BatchDetailFetcher) PrefetchAndPopulate(ctx context.Context, items []model.InventoryItem, site config.SiteConfig) ([]model.InventoryItem, error) {
	urls := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		if it.URL != "" && !seen[it.URL] {
			urls = append(urls, it.URL)
			seen[it.URL] = true
		}
	}
	if len(urls) == 0 {
		return items, nil
	}

	htmlByURL, err := b.fetchAll(ctx, urls)
	if err != nil {
		return items, err
	}

	for i := range items {
		html, ok := htmlByURL[items[i].URL]
		if !ok || strings.TrimSpace(html) == "" || isDataDomeChallenge(html) {
			continue
		}
		populated, perr := populateDetailsFromHTML(ctx, b.ImageSizes, items[i], site, html)
		if perr == nil {
			items[i] = populated
		}
	}
	return items, nil
}

func (b *BatchDetailFetcher) fetchAll(ctx context.Context, urls []string) (map[string]string, error) {
	input, _ := json.Marshal(urls)
	cmd := exec.CommandContext(ctx, b.PythonBin, b.ScriptPath)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("batch detail fetch failed: %w — %s", err, strings.TrimSpace(stderr.String()))
	}

	var result batchDetailResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("batch detail output parse failed: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("batch detail: %s", result.Error)
	}
	if result.Cookie != "" && b.CookieStore != nil {
		_ = b.CookieStore.Set("datadome", result.Cookie)
	}
	return result.Results, nil
}

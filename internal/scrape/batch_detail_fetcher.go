package scrape

import (
	"bufio"
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
	MaxPages    int // cap on detail pages fetched per run (0 = unlimited)
}

func NewBatchDetailFetcher(scriptPath, pythonBin string, sizes *ImageSizeCache, store *CookieStore) *BatchDetailFetcher {
	pythonBin = resolvePythonBin(pythonBin)
	return &BatchDetailFetcher{ScriptPath: scriptPath, PythonBin: pythonBin, ImageSizes: sizes, CookieStore: store}
}

// batchDetailLine is one NDJSON line streamed by fetch_details.py: either a
// per-page result, the final done+cookie line, or a fatal error. Legacy
// single-JSON output ({"results": {...}}) is also accepted via Results.
type batchDetailLine struct {
	URL     string            `json:"url"`
	HTML    string            `json:"html"`
	Done    bool              `json:"done"`
	Cookie  string            `json:"cookie"`
	Error   string            `json:"error"`
	Results map[string]string `json:"results"`
}

// selectDetailURLs picks the pages worth rendering and reports how many were
// cut by the cap. Items that already carry every detail field are skipped — the
// same gate the inline path uses — since spending a render on them adds nothing
// and, on a bot-protected host, is how a batch starts getting refused.
//
// dropped is returned rather than swallowed: truncating silently reads as a
// clean run while leaving every vehicle past the cap with no stock number.
func selectDetailURLs(items []model.InventoryItem, maxPages int) (urls []string, dropped int) {
	seen := map[string]bool{}
	urls = make([]string, 0, len(items))
	for _, it := range items {
		if it.URL == "" || seen[it.URL] || detailFetchWouldAddNothing(it) {
			continue
		}
		urls = append(urls, it.URL)
		seen[it.URL] = true
	}
	if maxPages > 0 && len(urls) > maxPages {
		dropped = len(urls) - maxPages
		urls = urls[:maxPages]
	}
	return urls, dropped
}

// PrefetchAndPopulate fetches all detail pages in one session and returns items
// with detail fields populated. Items without a URL pass through unchanged.
// On error (e.g. killed on deadline) it still populates from whatever pages
// were streamed before the failure and returns them alongside the error.
func (b *BatchDetailFetcher) PrefetchAndPopulate(ctx context.Context, items []model.InventoryItem, site config.SiteConfig) ([]model.InventoryItem, error) {
	urls, dropped := selectDetailURLs(items, b.MaxPages)
	if len(urls) == 0 {
		return items, nil
	}

	htmlByURL, fetchErr := b.fetchAll(ctx, urls)

	// DataDome makes individual detail-page fetches flaky: a single page can come
	// back blocked/empty even when its siblings succeed, leaving that vehicle with
	// no stock/VIN. Retry just the failed URLs once in a fresh session so one bad
	// page doesn't silently drop a car's detail fields. Skip when the first pass
	// already died (deadline/kill) — there is no time budget left for a retry.
	if fetchErr == nil && ctx.Err() == nil {
		retryURLs := make([]string, 0)
		for _, u := range urls {
			if html, ok := htmlByURL[u]; !ok || strings.TrimSpace(html) == "" || isDataDomeChallenge(html) {
				retryURLs = append(retryURLs, u)
			}
		}
		if len(retryURLs) > 0 {
			if retried, rerr := b.fetchAll(ctx, retryURLs); rerr == nil {
				for u, html := range retried {
					if strings.TrimSpace(html) != "" && !isDataDomeChallenge(html) {
						htmlByURL[u] = html
					}
				}
			}
		}
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
	if fetchErr == nil && dropped > 0 {
		fetchErr = fmt.Errorf("detail page cap reached: %d of %d vehicles were left without detail fields (raise DETAIL_MAX_PAGES, currently %d)",
			dropped, dropped+b.MaxPages, b.MaxPages)
	}
	return items, fetchErr
}

// fetchAll runs fetch_details.py and consumes its NDJSON stream. If the process
// dies (deadline kill, crash), everything streamed so far is returned alongside
// the error instead of being thrown away.
func (b *BatchDetailFetcher) fetchAll(ctx context.Context, urls []string) (map[string]string, error) {
	input, _ := json.Marshal(urls)
	args := []string{b.ScriptPath}
	if b.CookieStore != nil {
		if dd := strings.TrimSpace(b.CookieStore.Get()["datadome"]); dd != "" {
			args = append(args, dd)
		}
	}
	cmd := exec.CommandContext(ctx, b.PythonBin, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("batch detail stdout pipe failed: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("batch detail start failed: %w", err)
	}

	results := map[string]string{}
	scriptErr := ""
	done := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var l batchDetailLine
		if err := json.Unmarshal(line, &l); err != nil {
			continue
		}
		switch {
		case l.Error != "":
			scriptErr = l.Error
		case len(l.Results) > 0: // legacy single-JSON output
			for u, h := range l.Results {
				results[u] = h
			}
			done = true
			if l.Cookie != "" && b.CookieStore != nil {
				_ = b.CookieStore.Set("datadome", l.Cookie)
			}
		case l.Done:
			done = true
			if l.Cookie != "" && b.CookieStore != nil {
				_ = b.CookieStore.Set("datadome", l.Cookie)
			}
		case l.URL != "":
			results[l.URL] = l.HTML
		}
	}

	waitErr := cmd.Wait()
	if scriptErr != "" {
		return results, fmt.Errorf("batch detail: %s", scriptErr)
	}
	if waitErr != nil {
		return results, fmt.Errorf("batch detail fetch failed after %d/%d pages: %w — %s",
			len(results), len(urls), waitErr, tailString(stderr.String(), 2000))
	}
	if !done && len(results) == 0 {
		return results, fmt.Errorf("batch detail produced no output — %s", tailString(stderr.String(), 2000))
	}
	return results, nil
}

func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

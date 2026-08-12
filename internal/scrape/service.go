package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"go.uber.org/zap"
)

// CookieStore is a thread-safe cookie map.
// On startup it reads from .env; if a persist file exists it takes precedence.
// API updates are written to the persist file so they survive restarts.
type CookieStore struct {
	mu          sync.RWMutex
	cookies     map[string]string
	PersistPath string
}

func NewCookieStore(initial map[string]string) *CookieStore {
	c := &CookieStore{cookies: make(map[string]string)}
	for k, v := range initial {
		c.cookies[k] = v
	}
	return c
}

// LoadPersisted merges the persist file over the initial map (file wins).
// Call once at startup after setting PersistPath.
func (c *CookieStore) LoadPersisted() error {
	if c.PersistPath == "" {
		return nil
	}
	b, err := os.ReadFile(c.PersistPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	persisted := make(map[string]string)
	if err := json.Unmarshal(b, &persisted); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range persisted {
		c.cookies[k] = v
	}
	return nil
}

// Set updates a cookie in memory and writes the full map to PersistPath.
func (c *CookieStore) Set(name, value string) error {
	c.mu.Lock()
	c.cookies[name] = value
	snapshot := make(map[string]string, len(c.cookies))
	for k, v := range c.cookies {
		snapshot[k] = v
	}
	c.mu.Unlock()

	if c.PersistPath == "" {
		return nil
	}
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.PersistPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.PersistPath, b, 0o644)
}

func (c *CookieStore) Get() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.cookies))
	for k, v := range c.cookies {
		out[k] = v
	}
	return out
}

func (c *CookieStore) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cookies)
}

type Service struct {
	Browser            Browser
	AltBrowser         Browser
	Fetcher            Fetcher
	DetailFetcher      DetailFetcher
	BatchDetailFetcher *BatchDetailFetcher
	Extractors         []Extractor
	Concurrency        int
	AIEnricher         *AIEnricher
	Logger             *zap.Logger
	DefaultCookies     *CookieStore
}

type unsafeFetcher interface {
	FetchUnsafe(ctx context.Context, url string) (string, error)
}

func (s Service) ScrapeOnce(ctx context.Context, sourceURL string, site config.SiteConfig) RunResult {
	return s.ScrapeOnceWithOptions(ctx, sourceURL, site, Options{})
}

func (s Service) ScrapeOnceWithOptions(ctx context.Context, sourceURL string, site config.SiteConfig, opts Options) RunResult {
	reportProgress(opts, "started", 0)
	// Merge default cookies with per-request cookies (per-request takes precedence).
	cookies := opts.Cookies
	if s.DefaultCookies != nil && s.DefaultCookies.Len() > 0 {
		merged := s.DefaultCookies.Get()
		for k, v := range opts.Cookies {
			merged[k] = v
		}
		cookies = merged
	}
	firstHTML, renderErr := s.fetchListHTML(ctx, sourceURL, site, opts.BrowserStrategy, cookies)
	if renderErr != nil {
		reportProgress(opts, "render_failed", 0)
		return RunResult{Errors: []model.StructuredError{{Code: "SCRAPE_RENDER_FAILED", Message: renderErr.Error()}}}
	}
	reportProgress(opts, "list_fetched", countCards(firstHTML, site.ListPage.CardSelector))
	detectedTotal := detectInventoryTotal(firstHTML, site)
	effectiveMaxItems := effectiveMaxItems(site.ListPage.MaxItems, detectedTotal)
	if s.Logger != nil {
		s.Logger.Info("scrape pagination baseline",
			zap.String("sourceUrl", sourceURL),
			zap.Int("detectedTotal", detectedTotal),
			zap.Int("configuredMaxPages", site.ListPage.Pagination.MaxPages),
			zap.Int("effectiveMaxItems", effectiveMaxItems),
			zap.String("cardSelector", site.ListPage.CardSelector),
		)
	}
	optsWithCookies := opts
	optsWithCookies.Cookies = cookies
	pages, pageErrs := s.collectPaginatedHTML(ctx, sourceURL, firstHTML, site, effectiveMaxItems, opts.BrowserStrategy, optsWithCookies)
	reportProgress(opts, "pages_collected", countCollectedCards(pages, site.ListPage.CardSelector))

	allItems := make([]model.InventoryItem, 0)
	errs := make([]model.StructuredError, 0, len(pageErrs))
	errs = append(errs, pageErrs...)
	for _, page := range pages {
		haveStructuredItems := false
		for _, ex := range s.Extractors {
			if _, isRegex := ex.(RegexExtractor); isRegex && haveStructuredItems {
				continue
			}
			items, e := ex.Extract(ctx, page.html, page.url, site)
			if len(items) > 0 {
				if _, isRegex := ex.(RegexExtractor); !isRegex {
					haveStructuredItems = true
				}
			}
			allItems = append(allItems, items...)
			errs = append(errs, e...)
			reportItemsProgress(opts, "items_extracted", allItems)
		}
		if effectiveMaxItems > 0 && len(allItems) >= effectiveMaxItems {
			allItems = allItems[:effectiveMaxItems]
			reportItemsProgress(opts, "items_limited", allItems)
			break
		}
	}
	for i := range allItems {
		allItems[i] = NormalizeItem(sourceURL, allItems[i])
		allItems[i] = applyItemAliases(allItems[i], opts.DealershipID, opts.SourceURL)
	}
	allItems = Dedupe(allItems)
	reportItemsProgress(opts, "items_deduped", allItems)
	if len(allItems) > 0 {
		filteredErrs := make([]model.StructuredError, 0, len(errs))
		for _, e := range errs {
			if e.Code == "FALLBACK_EMPTY" {
				continue
			}
			filteredErrs = append(filteredErrs, e)
		}
		errs = filteredErrs
	}

	detailsWanted := len(site.DetailPage.ImageSelectors) > 0 || site.DetailPage.VINSelector != "" || site.DetailPage.StockSelector != ""
	if detailsWanted && s.BatchDetailFetcher != nil {
		// Render all detail pages in one Camoufox session (fast, DataDome-safe).
		// On error (e.g. run deadline killed the batch) populated still carries
		// details for every page fetched before the failure — keep those.
		populated, derr := s.BatchDetailFetcher.PrefetchAndPopulate(ctx, allItems, site)
		if populated != nil {
			allItems = populated
		}
		if derr != nil {
			errs = append(errs, model.StructuredError{Code: "DETAIL_FETCH_FAILED", Message: derr.Error()})
			if s.Logger != nil {
				s.Logger.Warn("batch detail fetch failed", zap.Error(derr))
			}
		}
		reportItemsProgress(opts, "details_completed", allItems)
	} else if detailsWanted {
		sem := make(chan struct{}, s.Concurrency)
		wg := sync.WaitGroup{}
		mu := sync.Mutex{}
		for idx := range allItems {
			if allItems[idx].URL == "" {
				continue
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer func() { <-sem }()
				dctx, cancel := context.WithTimeout(ctx, 25*time.Second)
				defer cancel()
				item, err := s.DetailFetcher.FetchDetails(dctx, allItems[i], site)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, model.StructuredError{Code: "DETAIL_FETCH_FAILED", Message: err.Error(), ItemURL: allItems[i].URL})
					reportItemsProgress(opts, "details_progress", allItems)
					return
				}
				allItems[i] = item
				reportItemsProgress(opts, "details_progress", allItems)
			}(idx)
		}
		wg.Wait()
		reportItemsProgress(opts, "details_completed", allItems)
	}
	for i := range allItems {
		allItems[i] = NormalizeItem(sourceURL, allItems[i])
		allItems[i] = applyItemAliases(allItems[i], opts.DealershipID, opts.SourceURL)
	}
	allItems = Dedupe(allItems)
	reportItemsProgress(opts, "details_deduped", allItems)

	if opts.EnableAIEnrichment && s.AIEnricher != nil {
		for i := range allItems {
			enriched, err := s.AIEnricher.Enrich(ctx, allItems[i], sourceURL)
			if err != nil {
				continue
			}
			allItems[i] = applyItemAliases(NormalizeItem(sourceURL, enriched), opts.DealershipID, opts.SourceURL)
			reportItemsProgress(opts, "ai_progress", allItems)
		}
		allItems = Dedupe(allItems)
		reportItemsProgress(opts, "ai_completed", allItems)
	}

	if len(allItems) == 0 {
		errs = append(errs, model.StructuredError{Code: "NO_ITEMS", Message: fmt.Sprintf("no inventory found for %s", sourceURL)})
	}
	reportItemsProgress(opts, "completed", allItems)
	return RunResult{Items: allItems, Errors: errs}
}

func reportItemsProgress(opts Options, stage string, items []model.InventoryItem) {
	reportProgress(opts, stage, model.ScrapedInventoryCount(items))
}

func reportProgress(opts Options, stage string, totalItems int) {
	if opts.Progress == nil {
		return
	}
	opts.Progress(Progress{Stage: stage, TotalItems: totalItems})
}

type scrapedPageHTML struct {
	url  string
	html string
}

func cookieHeader(cookies map[string]string) string {
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for k, v := range cookies {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func (s Service) fetchListHTML(ctx context.Context, pageURL string, site config.SiteConfig, strategy string, cookies map[string]string) (string, error) {
	// Primary path: the cookie-aware fetcher (CurlFetcher) self-heals — it tries
	// curl_cffi with the cookie first, then falls back to Camoufox (which bypasses
	// DataDome without any cookie). Always route through it when available, even
	// with no cookie, since Camoufox needs none.
	var html string
	var renderErr error
	source := "none"
	if strings.Contains(strings.ToLower(site.Discovery.Notes), "imotor") {
		if h, err := fetchIMotorInventoryHTML(ctx, pageURL); err == nil {
			if s.Logger != nil {
				s.Logger.Info("list html source", zap.String("url", pageURL), zap.String("source", "go_imotor_api"), zap.Int("cardCount", countCards(h, site.ListPage.CardSelector)))
			}
			return h, nil
		} else if s.Logger != nil {
			s.Logger.Warn("Go iMotor API fetch failed; falling back", zap.String("url", pageURL), zap.Error(err))
		}
	}

	if s.Fetcher != nil {
		if cf, ok := s.Fetcher.(interface {
			FetchWithCookie(context.Context, string, string) (string, error)
		}); ok {
			h, err := cf.FetchWithCookie(ctx, pageURL, cookieHeader(cookies))
			if err == nil {
				if isDataDomeChallenge(h) {
					return "", fmt.Errorf("datadome challenge still present after all bypass strategies")
				}
				cardCount := countCards(h, site.ListPage.CardSelector)
				// A successful HTTP response can still be only the server-rendered shell
				// for client-side inventory apps. If a card selector is configured but
				// absent, continue to a real browser so the inventory can hydrate.
				if site.ListPage.CardSelector == "" || cardCount > 0 {
					if s.Logger != nil {
						s.Logger.Info("list html source", zap.String("url", pageURL), zap.String("source", "curl_camoufox"), zap.Int("cardCount", cardCount))
					}
					return h, nil
				}
				renderErr = fmt.Errorf("HTTP response contained no configured inventory cards")
				if s.Logger != nil {
					s.Logger.Info("HTTP response is an unhydrated shell; falling back to browser", zap.String("url", pageURL), zap.String("cardSelector", site.ListPage.CardSelector))
				}
			}
			if err != nil {
				// The Python fetch layer failed (missing binary, script error, or
				// block). Don't give up: non-protected sites render fine in the Go
				// browsers below, so a Python misconfig must not break them.
				renderErr = fmt.Errorf("fetch failed: %w", err)
				if s.Logger != nil {
					s.Logger.Warn("python fetch layer failed; falling back to Go browsers", zap.String("url", pageURL), zap.Error(err))
				}
			}
		}
	}

	primary := s.Browser
	secondary := s.AltBrowser
	if strings.EqualFold(strings.TrimSpace(strategy), "rod_first") {
		primary, secondary = secondary, primary
	}
	for _, b := range []Browser{primary, secondary} {
		if b == nil {
			continue
		}
		h, err := b.Render(ctx, pageURL, site)
		if err != nil {
			renderErr = err
			if s.Logger != nil {
				s.Logger.Warn("browser render failed", zap.String("url", pageURL), zap.Error(err))
			}
			continue
		}
		html = h
		renderErr = nil
		source = "browser"
		html = expandIMotorInventory(ctx, pageURL, html)
		if b == primary && secondary != nil && site.ListPage.CardSelector != "" && countCards(html, site.ListPage.CardSelector) < 2 {
			continue
		}
		break
	}
	if html == "" {
		if s.Fetcher == nil {
			if renderErr != nil {
				return "", renderErr
			}
			return "", fmt.Errorf("no browser html and fetcher is nil")
		}
		renderErr = Retry(ctx, 1, func() error {
			h, err := s.Fetcher.Fetch(ctx, pageURL)
			if err != nil {
				return err
			}
			html = h
			source = "http_fetch"
			return nil
		})
		if renderErr != nil && isTimeoutLikeErr(renderErr) && s.Browser != nil {
			if h, err := s.Browser.Render(ctx, pageURL, site); err == nil {
				html = h
				renderErr = nil
				source = "browser_retry"
			}
		}
	}
	if renderErr != nil {
		return "", renderErr
	}
	if s.Logger != nil {
		cardCount := 0
		if site.ListPage.CardSelector != "" {
			cardCount = countCards(html, site.ListPage.CardSelector)
		}
		s.Logger.Info("list html source", zap.String("url", pageURL), zap.String("source", source), zap.Int("cardCount", cardCount))
	}
	return html, nil
}

func (s Service) collectPaginatedHTML(ctx context.Context, sourceURL, firstHTML string, site config.SiteConfig, maxItems int, strategy string, opts Options) ([]scrapedPageHTML, []model.StructuredError) {
	maxPages := site.ListPage.Pagination.MaxPages
	if maxPages <= 0 {
		maxPages = 20
	}
	if inferredPages := inferDealerSyncPageCount(firstHTML); inferredPages > maxPages {
		// Deploy environments sometimes end up with too-low maxPages (or stale config).
		// If DealerSync metadata clearly indicates more pages, expand safely.
		maxPages = inferredPages
		if maxPages > 200 {
			maxPages = 200
		}
	}
	if s.Logger != nil {
		s.Logger.Info("collect pagination plan",
			zap.String("sourceUrl", sourceURL),
			zap.Int("maxPages", maxPages),
			zap.Int("inferredDealerSyncPages", inferDealerSyncPageCount(firstHTML)),
		)
	}
	pages := []scrapedPageHTML{{url: sourceURL, html: firstHTML}}
	reportProgress(opts, "pages_collected", countCollectedCards(pages, site.ListPage.CardSelector))
	if maxPages == 1 {
		return pages, nil
	}
	seen := map[string]struct{}{sourceURL: {}}
	errs := make([]model.StructuredError, 0)
	idx := 0
	for idx < len(pages) && len(pages) < maxPages {
		cur := pages[idx]
		nextURLs := extractNextPageURLs(cur.url, cur.html, site)
		if len(nextURLs) == 0 {
			nextURLs = inferDealerSyncFallbackURLs(cur.url, cur.html, maxPages-len(pages))
		}
		if s.Logger != nil {
			s.Logger.Info("collect pagination step",
				zap.String("currentUrl", cur.url),
				zap.Int("currentPageIndex", idx),
				zap.Int("nextUrlCount", len(nextURLs)),
				zap.Int("pagesCollected", len(pages)),
			)
		}
		for _, nextURL := range nextURLs {
			if len(pages) >= maxPages {
				break
			}
			if _, ok := seen[nextURL]; ok {
				continue
			}
			var h string
			err := Retry(ctx, 2, func() error {
				pageHTML, ferr := s.fetchListHTML(ctx, nextURL, site, strategy, opts.Cookies)
				if ferr != nil {
					return ferr
				}
				if site.ListPage.CardSelector != "" && countCards(pageHTML, site.ListPage.CardSelector) == 0 {
					return fmt.Errorf("pagination page returned zero inventory cards")
				}
				// The DealerCenter platform sometimes ignores ?PageNumber=N and
				// re-serves page 1; treat a page-number mismatch as a failed
				// fetch so it retries instead of silently duplicating page 1.
				if want := pageNumberParam(nextURL); want > 1 {
					if got := currentPageFromHTML(pageHTML); got > 0 && got != want {
						return fmt.Errorf("requested page %d but got page %d", want, got)
					}
				}
				h = pageHTML
				return nil
			})
			if err != nil {
				errs = append(errs, model.StructuredError{Code: "PAGINATION_FETCH_FAILED", Message: err.Error(), ItemURL: nextURL})
				if s.Logger != nil {
					s.Logger.Warn("pagination fetch failed", zap.String("nextUrl", nextURL), zap.Error(err))
				}
				continue
			}
			seen[nextURL] = struct{}{}
			pages = append(pages, scrapedPageHTML{url: nextURL, html: h})
			reportProgress(opts, "pages_collected", countCollectedCards(pages, site.ListPage.CardSelector))
			if s.Logger != nil {
				s.Logger.Info("pagination page collected", zap.String("nextUrl", nextURL), zap.Int("pagesCollected", len(pages)))
			}
		}
		idx++
	}
	return pages, errs
}

func countCollectedCards(pages []scrapedPageHTML, selector string) int {
	if selector == "" {
		return 0
	}
	total := 0
	for _, page := range pages {
		total += countCards(page.html, selector)
	}
	return total
}

func countCards(html, selector string) int {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0
	}
	return doc.Find(selector).Length()
}

func effectiveMaxItems(configMax, detectedTotal int) int {
	switch {
	case configMax > 0 && detectedTotal > 0:
		if detectedTotal < configMax {
			return detectedTotal
		}
		return configMax
	case configMax > 0:
		return configMax
	case detectedTotal > 0:
		return detectedTotal
	default:
		return 0
	}
}

// Require a numeric range before "of" (e.g. "1 - 11 of 24") to avoid matching
// page-number patterns like "Page 1 of 1".
var inventoryTotalTextRe = regexp.MustCompile(`(?i)(?:\d+\s*[-–—]\s*\d+\s+of\s+|total\s+)([0-9][0-9,]{0,8})\s*(?:vehicles?|cars?|results?|inventory|listings?)?`)

func detectInventoryTotal(html string, site config.SiteConfig) int {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0
	}
	if site.ListPage.TotalSelector != "" {
		if n := parseTotalText(doc.Find(site.ListPage.TotalSelector).First().Text()); n > 0 {
			return n
		}
	}
	for _, sel := range site.Discovery.TotalHints {
		if n := parseTotalText(doc.Find(sel).First().Text()); n > 0 {
			return n
		}
	}
	if n := parseTotalText(doc.Text()); n > 0 {
		return n
	}
	if n := parsePositiveInt(doc.Find("#ds-inventory-model").First().AttrOr("data-results-total", "0")); n > 0 {
		return n
	}
	// DealerCenter/carsforsale-platform sites expose the total in a hidden input
	if n := parsePositiveInt(doc.Find("input.data-inventory-total-records").First().AttrOr("value", "0")); n > 0 {
		return n
	}
	return 0
}

func parseTotalText(text string) int {
	m := inventoryTotalTextRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	return parsePositiveInt(strings.ReplaceAll(m[1], ",", ""))
}

func extractNextPageURLs(pageURL, html string, site config.SiteConfig) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	selectors := []string{}
	if site.ListPage.Pagination.NextSelector != "" {
		selectors = append(selectors, site.ListPage.Pagination.NextSelector)
	}
	selectors = append(selectors,
		"link[rel='next']",
		"a[rel='next']",
		"a[aria-label='Next']",
		"a[aria-label='next']",
		"nav[class*='pagination'] a[href]",
		"ul[class*='pagination'] a[href]",
		"ul[class*='page'] a[href]",
		"a[class*='next'][href]",
		"a[href*='/page/']",
		"a[href*='page=']",
	)
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	for _, sel := range selectors {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			href, ok := s.Attr("href")
			if !ok || strings.TrimSpace(href) == "" {
				return
			}
			abs := absolutize(pageURL, href)
			if abs == "" {
				return
			}
			if _, ok := seen[abs]; ok {
				return
			}
			if !sameHost(pageURL, abs) {
				return
			}
			// keep only likely inventory/pagination links; avoid category/filter and media links
			if !looksLikePaginationOrInventoryURL(abs) {
				return
			}
			// a bare link back to the current listing path (no query) is a JS-driven
			// "Next" button, not a real next page — following it just re-fetches page 1
			if isBareSelfLink(pageURL, abs) {
				return
			}
			seen[abs] = struct{}{}
			out = append(out, abs)
		})
	}
	for _, next := range extractDealerSyncPageURLs(pageURL, doc) {
		if _, ok := seen[next]; ok {
			continue
		}
		seen[next] = struct{}{}
		out = append(out, next)
	}
	for _, next := range extractPageNumberParamURLs(pageURL, doc) {
		if _, ok := seen[next]; ok {
			continue
		}
		seen[next] = struct{}{}
		out = append(out, next)
	}
	return out
}

// no trailing \b: goquery .Text() concatenates nodes without whitespace, so the
// total may run into following text ("Page 1 of 3Next")
var pageXofYRe = regexp.MustCompile(`(?i)\bpage\s+(\d+)\s+of\s+(\d+)`)

// extractPageNumberParamURLs handles DealerCenter/carsforsale-platform dealer
// sites whose pagination is a JS form POST with no crawlable page hrefs. The
// pagination widget shows "Page X of Y" and the server also accepts GET
// ?PageNumber=N, so we synthesize every remaining page URL from that text.
// Emitting all pages (not just the next) means one bad fetch can't halt the
// walk — the site sometimes ignores PageNumber and re-serves page 1.
func extractPageNumberParamURLs(pageURL string, doc *goquery.Document) []string {
	pagText := doc.Find("[class*='pagination']").Text()
	m := pageXofYRe.FindStringSubmatch(pagText)
	if len(m) < 3 {
		return nil
	}
	cur := parsePositiveInt(m[1])
	total := parsePositiveInt(m[2])
	if cur <= 0 || total <= 1 || cur >= total {
		return nil
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	out := make([]string, 0, total-cur)
	for p := cur + 1; p <= total; p++ {
		next := *u
		q := next.Query()
		q.Set("PageNumber", strconv.Itoa(p))
		// The platform's own invSize() script rewrites any URL lacking PageSize
		// via location.replace(url+"?PageSize=100"), mangling ?PageNumber=N into
		// a malformed double-? URL that resets to page 1 when rendered in a
		// browser. Including PageSize keeps that script inert.
		if q.Get("PageSize") == "" {
			q.Set("PageSize", "100")
		}
		next.RawQuery = q.Encode()
		out = append(out, next.String())
	}
	return out
}

// pageNumberParam returns the PageNumber query value of a URL, 0 if absent.
func pageNumberParam(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	return parsePositiveInt(u.Query().Get("PageNumber"))
}

// currentPageFromHTML reads the "Page X of Y" widget, 0 if not present.
func currentPageFromHTML(html string) int {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0
	}
	m := pageXofYRe.FindStringSubmatch(doc.Find("[class*='pagination']").Text())
	if len(m) < 3 {
		return 0
	}
	return parsePositiveInt(m[1])
}

func extractDealerSyncPageURLs(pageURL string, doc *goquery.Document) []string {
	model := doc.Find("#ds-inventory-model").First()
	if model.Length() == 0 {
		return nil
	}
	total := parsePositiveInt(model.AttrOr("data-results-total", "0"))
	count := parsePositiveInt(model.AttrOr("data-results-count", "0"))
	if total <= 0 || count <= 0 || total <= count {
		return nil
	}

	u, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	q := u.Query()
	aiPage := parsePositiveInt(q.Get("ai_page"))
	maxPage := (total + count - 1) / count
	next := aiPage + 1
	if next >= maxPage {
		return nil
	}
	q.Set("ai_page", strconv.Itoa(next))
	u.RawQuery = q.Encode()
	return []string{u.String()}
}

func inferDealerSyncPageCount(html string) int {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0
	}
	model := doc.Find("#ds-inventory-model").First()
	if model.Length() == 0 {
		return 0
	}
	total := parsePositiveInt(model.AttrOr("data-results-total", "0"))
	count := parsePositiveInt(model.AttrOr("data-results-count", "0"))
	if total <= 0 || count <= 0 {
		return 0
	}
	pages := (total + count - 1) / count
	if pages < 1 {
		return 1
	}
	return pages
}

func inferDealerSyncFallbackURLs(pageURL, html string, remaining int) []string {
	if remaining <= 0 {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	model := doc.Find("#ds-inventory-model").First()
	if model.Length() == 0 {
		return nil
	}
	total := parsePositiveInt(model.AttrOr("data-results-total", "0"))
	count := parsePositiveInt(model.AttrOr("data-results-count", "0"))
	if total <= 0 || count <= 0 || total <= count {
		return nil
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	q := u.Query()
	curPage := parsePositiveInt(q.Get("ai_page"))
	totalPages := (total + count - 1) / count
	if totalPages <= 1 {
		return nil
	}
	out := make([]string, 0, remaining)
	for p := curPage + 1; p < totalPages && len(out) < remaining; p++ {
		next := *u
		nq := next.Query()
		nq.Set("ai_page", strconv.Itoa(p))
		next.RawQuery = nq.Encode()
		out = append(out, next.String())
	}
	return out
}

func parsePositiveInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func looksLikePaginationOrInventoryURL(u string) bool {
	l := strings.ToLower(u)
	if strings.Contains(l, "/vehicle-details/") {
		return false
	}
	return strings.Contains(l, "/page/") ||
		strings.Contains(l, "page=") ||
		strings.Contains(l, "pagenum=") ||
		strings.Contains(l, "pagesize=") ||
		strings.Contains(l, "/inventory") ||
		strings.Contains(l, "/used-cars") ||
		strings.Contains(l, "/cars-for-sale")
}

func isBareSelfLink(pageURL, candidate string) bool {
	cu, err := url.Parse(candidate)
	if err != nil || cu.RawQuery != "" {
		return false
	}
	pu, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.Trim(cu.EscapedPath(), "/"), strings.Trim(pu.EscapedPath(), "/"))
}

func sameHost(baseURL, candidate string) bool {
	bu, berr := url.Parse(baseURL)
	cu, cerr := url.Parse(candidate)
	if berr != nil || cerr != nil {
		return true
	}
	return strings.EqualFold(bu.Hostname(), cu.Hostname())
}

// IsBotProtectionFailure reports whether scrape errors indicate the host is
// bot-blocked (DataDome challenge, 403/500 block, all bypass strategies failed)
// rather than a scraping/parsing problem.
func IsBotProtectionFailure(errs []model.StructuredError) bool {
	for _, e := range errs {
		if e.Code != "SCRAPE_RENDER_FAILED" && e.Code != "PAGINATION_FETCH_FAILED" {
			continue
		}
		msg := strings.ToLower(e.Message)
		if strings.Contains(msg, "datadome") ||
			strings.Contains(msg, "captcha-delivery") ||
			strings.Contains(msg, "blocked") ||
			strings.Contains(msg, "all strategies failed") {
			return true
		}
	}
	return false
}

func isTimeoutLikeErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "deadline exceeded") ||
		strings.Contains(s, "client.timeout") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "temporary") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "eof")
}

func (s Service) ScrapeOnceRaw(ctx context.Context, sourceURL string, site config.SiteConfig) RunResult {
	var html string
	var fetchErr error

	if s.Browser != nil {
		h, err := s.Browser.Render(ctx, sourceURL, site)
		if err == nil {
			html = h
		}
	}
	if html != "" {
		fetchErr = nil
	} else {
		fetchErr = Retry(ctx, 1, func() error {
			if uf, ok := s.Fetcher.(unsafeFetcher); ok {
				h, err := uf.FetchUnsafe(ctx, sourceURL)
				if err != nil {
					return err
				}
				html = h
				return nil
			}
			h, err := s.Fetcher.Fetch(ctx, sourceURL)
			if err != nil {
				return err
			}
			html = h
			return nil
		})
	}
	if fetchErr != nil {
		return RunResult{Errors: []model.StructuredError{{Code: "SCRAPE_RENDER_FAILED", Message: fetchErr.Error()}}}
	}

	allItems := make([]model.InventoryItem, 0)
	errs := make([]model.StructuredError, 0)
	haveStructuredItems := false
	for _, ex := range s.Extractors {
		if _, isRegex := ex.(RegexExtractor); isRegex && haveStructuredItems {
			continue
		}
		items, e := ex.Extract(ctx, html, sourceURL, site)
		if len(items) > 0 {
			if _, isRegex := ex.(RegexExtractor); !isRegex {
				haveStructuredItems = true
			}
		}
		allItems = append(allItems, items...)
		errs = append(errs, e...)
	}
	for i := range allItems {
		allItems[i] = NormalizeItem(sourceURL, allItems[i])
		allItems[i] = applyItemAliases(allItems[i], "", sourceURL)
	}
	allItems = Dedupe(allItems)

	if len(allItems) > 0 {
		filteredErrs := make([]model.StructuredError, 0, len(errs))
		for _, e := range errs {
			if e.Code == "FALLBACK_EMPTY" {
				continue
			}
			filteredErrs = append(filteredErrs, e)
		}
		errs = filteredErrs
	}
	if len(allItems) == 0 {
		errs = append(errs, model.StructuredError{Code: "NO_ITEMS", Message: fmt.Sprintf("no inventory found for %s", sourceURL)})
	}
	return RunResult{Items: allItems, Errors: errs}
}

func applyItemAliases(item model.InventoryItem, dealershipID, sourceURL string) model.InventoryItem {
	if item.Stock == "" {
		item.Stock = item.StockID
	}
	if item.Website == "" {
		item.Website = sourceURL
	}
	if item.DealerID == "" {
		item.DealerID = dealershipID
	}
	if len(item.PhotoURLs) == 0 {
		item.PhotoURLs = item.Images
	}
	if item.VehicleListPrice == "" {
		item.VehicleListPrice = item.Price
	}
	if item.Style == "" {
		item.Style = inferStyle(item.Title)
	}
	return item
}

func inferStyle(title string) string {
	t := strings.TrimSpace(strings.ToLower(title))
	switch {
	case strings.Contains(t, "sedan"):
		return "sedan"
	case strings.Contains(t, "coupe"):
		return "coupe"
	case strings.Contains(t, "hatch"):
		return "hatchback"
	case strings.Contains(t, "truck"), strings.Contains(t, "pickup"):
		return "truck"
	case strings.Contains(t, "suv"), strings.Contains(t, "crossover"):
		return "suv"
	case strings.Contains(t, "van"), strings.Contains(t, "minivan"):
		return "van"
	default:
		return ""
	}
}

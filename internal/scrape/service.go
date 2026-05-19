package scrape

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type Service struct {
	Browser       Browser
	AltBrowser    Browser
	Fetcher       Fetcher
	DetailFetcher DetailFetcher
	Extractors    []Extractor
	Concurrency   int
	AIEnricher    *AIEnricher
}

type unsafeFetcher interface {
	FetchUnsafe(ctx context.Context, url string) (string, error)
}

func (s Service) ScrapeOnce(ctx context.Context, sourceURL string, site config.SiteConfig) RunResult {
	return s.ScrapeOnceWithOptions(ctx, sourceURL, site, Options{})
}

func (s Service) ScrapeOnceWithOptions(ctx context.Context, sourceURL string, site config.SiteConfig, opts Options) RunResult {
	firstHTML, renderErr := s.fetchListHTML(ctx, sourceURL, site, opts.BrowserStrategy)
	if renderErr != nil {
		return RunResult{Errors: []model.StructuredError{{Code: "SCRAPE_RENDER_FAILED", Message: renderErr.Error()}}}
	}
	detectedTotal := detectInventoryTotal(firstHTML, site)
	effectiveMaxItems := effectiveMaxItems(site.ListPage.MaxItems, detectedTotal)
	pages, pageErrs := s.collectPaginatedHTML(ctx, sourceURL, firstHTML, site, effectiveMaxItems, opts.BrowserStrategy)

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
		}
		if effectiveMaxItems > 0 && len(allItems) >= effectiveMaxItems {
			allItems = allItems[:effectiveMaxItems]
			break
		}
	}
	for i := range allItems {
		allItems[i] = NormalizeItem(sourceURL, allItems[i])
		allItems[i] = applyItemAliases(allItems[i], opts.DealershipID, opts.SourceURL)
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

	sem := make(chan struct{}, s.Concurrency)
	wg := sync.WaitGroup{}
	mu := sync.Mutex{}
	for idx := range allItems {
		if allItems[idx].URL == "" {
			continue
		}
		if len(site.DetailPage.ImageSelectors) == 0 && site.DetailPage.VINSelector == "" && site.DetailPage.StockSelector == "" {
			break
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
				return
			}
			allItems[i] = item
		}(idx)
	}
	wg.Wait()
	for i := range allItems {
		allItems[i] = NormalizeItem(sourceURL, allItems[i])
		allItems[i] = applyItemAliases(allItems[i], opts.DealershipID, opts.SourceURL)
	}
	allItems = Dedupe(allItems)

	if opts.EnableAIEnrichment && s.AIEnricher != nil {
		for i := range allItems {
			enriched, err := s.AIEnricher.Enrich(ctx, allItems[i], sourceURL)
			if err != nil {
				continue
			}
			allItems[i] = applyItemAliases(NormalizeItem(sourceURL, enriched), opts.DealershipID, opts.SourceURL)
		}
		allItems = Dedupe(allItems)
	}

	if len(allItems) == 0 {
		errs = append(errs, model.StructuredError{Code: "NO_ITEMS", Message: fmt.Sprintf("no inventory found for %s", sourceURL)})
	}
	return RunResult{Items: allItems, Errors: errs}
}

type scrapedPageHTML struct {
	url  string
	html string
}

func (s Service) fetchListHTML(ctx context.Context, pageURL string, site config.SiteConfig, strategy string) (string, error) {
	var html string
	var renderErr error

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
			continue
		}
		html = h
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
			return nil
		})
		if renderErr != nil && isTimeoutLikeErr(renderErr) && s.Browser != nil {
			if h, err := s.Browser.Render(ctx, pageURL, site); err == nil {
				html = h
				renderErr = nil
			}
		}
	}
	if renderErr != nil {
		return "", renderErr
	}
	return html, nil
}

func (s Service) collectPaginatedHTML(ctx context.Context, sourceURL, firstHTML string, site config.SiteConfig, maxItems int, strategy string) ([]scrapedPageHTML, []model.StructuredError) {
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
	pages := []scrapedPageHTML{{url: sourceURL, html: firstHTML}}
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
		for _, nextURL := range nextURLs {
			if len(pages) >= maxPages {
				break
			}
			if _, ok := seen[nextURL]; ok {
				continue
			}
			var h string
			err := Retry(ctx, 2, func() error {
				pageHTML, ferr := s.fetchListHTML(ctx, nextURL, site, strategy)
				if ferr != nil {
					return ferr
				}
				h = pageHTML
				return nil
			})
			if err != nil {
				errs = append(errs, model.StructuredError{Code: "PAGINATION_FETCH_FAILED", Message: err.Error(), ItemURL: nextURL})
				continue
			}
			seen[nextURL] = struct{}{}
			pages = append(pages, scrapedPageHTML{url: nextURL, html: h})
		}
		idx++
	}
	return pages, errs
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

var inventoryTotalTextRe = regexp.MustCompile(`(?i)(?:showing\s+\d+\s*[-–]\s*\d+\s+of|of|total)\s*([0-9][0-9,]{0,8})\s*(?:vehicles?|cars?|results?|inventory)?`)

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
	return out
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
		strings.Contains(l, "/inventory") ||
		strings.Contains(l, "/used-cars")
}

func sameHost(baseURL, candidate string) bool {
	bu, berr := url.Parse(baseURL)
	cu, cerr := url.Parse(candidate)
	if berr != nil || cerr != nil {
		return true
	}
	return strings.EqualFold(bu.Hostname(), cu.Hostname())
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

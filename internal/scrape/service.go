package scrape

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type Service struct {
	Browser       Browser
	Fetcher       Fetcher
	DetailFetcher DetailFetcher
	Extractors    []Extractor
	Concurrency   int
}

type unsafeFetcher interface {
	FetchUnsafe(ctx context.Context, url string) (string, error)
}

func (s Service) ScrapeOnce(ctx context.Context, sourceURL string, site config.SiteConfig) RunResult {
	firstHTML, renderErr := s.fetchListHTML(ctx, sourceURL, site)
	if renderErr != nil {
		return RunResult{Errors: []model.StructuredError{{Code: "SCRAPE_RENDER_FAILED", Message: renderErr.Error()}}}
	}
	pages, pageErrs := s.collectPaginatedHTML(ctx, sourceURL, firstHTML, site)

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
	}
	for i := range allItems {
		allItems[i] = NormalizeItem(sourceURL, allItems[i])
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
		if len(site.DetailPage.ImageSelectors) == 0 && site.DetailPage.VINSelector == "" {
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

	if len(allItems) == 0 {
		errs = append(errs, model.StructuredError{Code: "NO_ITEMS", Message: fmt.Sprintf("no inventory found for %s", sourceURL)})
	}
	return RunResult{Items: allItems, Errors: errs}
}

type scrapedPageHTML struct {
	url  string
	html string
}

func (s Service) fetchListHTML(ctx context.Context, pageURL string, site config.SiteConfig) (string, error) {
	var html string
	var renderErr error

	if s.Browser != nil {
		h, err := s.Browser.Render(ctx, pageURL, site)
		if err == nil {
			html = h
		} else {
			renderErr = err
		}
	}
	if html == "" {
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

func (s Service) collectPaginatedHTML(ctx context.Context, sourceURL, firstHTML string, site config.SiteConfig) ([]scrapedPageHTML, []model.StructuredError) {
	maxPages := site.ListPage.Pagination.MaxPages
	if maxPages <= 0 {
		maxPages = 20
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
		for _, nextURL := range nextURLs {
			if len(pages) >= maxPages {
				break
			}
			if _, ok := seen[nextURL]; ok {
				continue
			}
			seen[nextURL] = struct{}{}
			h, err := s.fetchListHTML(ctx, nextURL, site)
			if err != nil {
				errs = append(errs, model.StructuredError{Code: "PAGINATION_FETCH_FAILED", Message: err.Error(), ItemURL: nextURL})
				continue
			}
			pages = append(pages, scrapedPageHTML{url: nextURL, html: h})
		}
		idx++
	}
	return pages, errs
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
	selectors = append(selectors, "link[rel='next']", "a[rel='next']", "a[aria-label='Next']", "a[aria-label='next']")
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
			seen[abs] = struct{}{}
			out = append(out, abs)
		})
	}
	return out
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

package scrape

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/example/inventory-scraper/internal/config"
	"github.com/example/inventory-scraper/internal/model"
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
	var html string
	var renderErr error

	renderErr = Retry(ctx, 3, func() error {
		if s.Browser != nil {
			h, err := s.Browser.Render(ctx, sourceURL, site)
			if err == nil {
				html = h
				return nil
			}
		}
		h, err := s.Fetcher.Fetch(ctx, sourceURL)
		if err != nil {
			return err
		}
		html = h
		return nil
	})

	if renderErr != nil {
		return RunResult{Errors: []model.StructuredError{{Code: "SCRAPE_RENDER_FAILED", Message: renderErr.Error()}}}
	}

	allItems := make([]model.InventoryItem, 0)
	errs := make([]model.StructuredError, 0)
	for _, ex := range s.Extractors {
		items, e := ex.Extract(ctx, html, sourceURL, site)
		allItems = append(allItems, items...)
		errs = append(errs, e...)
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
	}

	if len(allItems) == 0 {
		errs = append(errs, model.StructuredError{Code: "NO_ITEMS", Message: fmt.Sprintf("no inventory found for %s", sourceURL)})
	}
	return RunResult{Items: allItems, Errors: errs}
}

func (s Service) ScrapeOnceRaw(ctx context.Context, sourceURL string, site config.SiteConfig) RunResult {
	var html string
	var fetchErr error

	fetchErr = Retry(ctx, 3, func() error {
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
	if fetchErr != nil {
		return RunResult{Errors: []model.StructuredError{{Code: "SCRAPE_RENDER_FAILED", Message: fetchErr.Error()}}}
	}

	allItems := make([]model.InventoryItem, 0)
	errs := make([]model.StructuredError, 0)
	for _, ex := range s.Extractors {
		items, e := ex.Extract(ctx, html, sourceURL, site)
		allItems = append(allItems, items...)
		errs = append(errs, e...)
	}
	allItems = Dedupe(allItems)
	for i := range allItems {
		allItems[i] = NormalizeItem(sourceURL, allItems[i])
	}

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

package scrape

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/example/inventory-scraper/internal/config"
)

type ChromeBrowser struct {
	allocCtx context.Context
}

func NewChromeBrowser(headless bool) (*ChromeBrowser, context.CancelFunc) {
	opts := chromedp.DefaultExecAllocatorOptions[:]
	if headless {
		opts = append(opts, chromedp.Headless, chromedp.DisableGPU)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	cancel := func() { cancelAlloc() }
	return &ChromeBrowser{allocCtx: allocCtx}, cancel
}

func (b *ChromeBrowser) Render(ctx context.Context, urlStr string, site config.SiteConfig) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	tabCtx, cancelTab := chromedp.NewContext(b.allocCtx)
	defer cancelTab()

	// Phase 1: navigate and read final URL immediately to fail fast on bad redirects.
	var finalURL string
	if err := chromedp.Run(tabCtx,
		chromedp.Navigate(urlStr),
		chromedp.Sleep(800*time.Millisecond),
		chromedp.Location(&finalURL),
	); err != nil {
		return "", fmt.Errorf("browser navigation failed: %w", err)
	}
	if u, err := url.Parse(finalURL); err == nil {
		if err := rejectUnsafeURL(u); err != nil {
			return "", err
		}
	}

	// Optional wait: do not fail the run if selectors are absent.
	if len(site.ListPage.WaitSelectors) > 0 {
		_ = chromedp.Run(tabCtx, chromedp.WaitVisible(site.ListPage.WaitSelectors[0], chromedp.ByQuery))
	}

	tasks := []chromedp.Action{}
	if site.ListPage.Pagination.InfiniteScroll {
		for i := 0; i < site.ListPage.Pagination.ScrollMaxAttempts; i++ {
			tasks = append(tasks, chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil), chromedp.Sleep(700*time.Millisecond))
		}
	}
	var html string
	tasks = append(tasks, chromedp.OuterHTML("html", &html, chromedp.ByQuery))

	if err := chromedp.Run(tabCtx, tasks...); err != nil {
		return "", fmt.Errorf("browser render failed: %w", err)
	}
	select {
	case <-timeoutCtx.Done():
		return "", timeoutCtx.Err()
	default:
	}
	return html, nil
}

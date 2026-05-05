package scrape

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
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
	tabCtx, cancelTab := chromedp.NewContext(b.allocCtx)
	defer cancelTab()
	timeoutCtx, cancel := context.WithTimeout(tabCtx, 45*time.Second)
	defer cancel()

	// Phase 1: navigate and read final URL immediately to fail fast on bad redirects.
	var finalURL string
	if err := chromedp.Run(timeoutCtx,
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

	waitForListSelector := func() {
		// Keep selector waits short so missing selectors don't consume most of the render timeout.
		if len(site.ListPage.WaitSelectors) == 0 {
			return
		}
		waitCtx, waitCancel := context.WithTimeout(tabCtx, 4*time.Second)
		defer waitCancel()
		_ = chromedp.Run(waitCtx, chromedp.WaitVisible(site.ListPage.WaitSelectors[0], chromedp.ByQuery))
	}
	waitForListSelector()

	capturePage := func() (string, error) {
		tasks := []chromedp.Action{}
		if site.ListPage.Pagination.InfiniteScroll {
			for i := 0; i < site.ListPage.Pagination.ScrollMaxAttempts; i++ {
				tasks = append(tasks, chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil), chromedp.Sleep(700*time.Millisecond))
			}
		}
		var pageHTML string
		tasks = append(tasks, chromedp.OuterHTML("html", &pageHTML, chromedp.ByQuery))
		if err := chromedp.Run(timeoutCtx, tasks...); err != nil {
			return "", err
		}
		return pageHTML, nil
	}

	capPages := site.ListPage.Pagination.MaxPages
	if capPages < 1 {
		capPages = 1
	}
	pages := make([]string, 0, capPages)
	pageHTML, err := capturePage()
	if err != nil {
		return "", fmt.Errorf("browser render failed: %w", err)
	}
	pages = append(pages, pageHTML)

	if site.ListPage.Pagination.Type == "next" && site.ListPage.Pagination.NextSelector != "" && site.ListPage.Pagination.MaxPages > 1 {
		nextClickJS := fmt.Sprintf(`(() => {
			const next = document.querySelector(%q);
			if (!next) return false;
			next.scrollIntoView({behavior: "instant", block: "center"});
			next.click();
			return true;
		})()`, site.ListPage.Pagination.NextSelector)

		for page := 2; page <= site.ListPage.Pagination.MaxPages; page++ {
			var clicked bool
			if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(nextClickJS, &clicked)); err != nil || !clicked {
				break
			}
			if err := chromedp.Run(timeoutCtx, chromedp.Sleep(700*time.Millisecond)); err != nil {
				break
			}
			waitForListSelector()
			pageHTML, err := capturePage()
			if err != nil {
				break
			}
			pages = append(pages, pageHTML)
		}
	}

	html := strings.Join(pages, "\n")
	select {
	case <-timeoutCtx.Done():
		return "", timeoutCtx.Err()
	default:
	}
	return html, nil
}

package scrape

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

type ChromeBrowser struct {
	allocCtx context.Context
}

func chromeBrowserErrorf(format string, args ...any) {
	if format == "unhandled node event %T" && len(args) == 1 {
		if _, ok := args[0].(*dom.EventAdoptedStyleSheetsModified); ok {
			return
		}
	}
	log.Printf("ERROR: "+format, args...)
}

func NewChromeBrowser(headless bool) (*ChromeBrowser, context.CancelFunc) {
	opts := chromedp.DefaultExecAllocatorOptions[:]
	if p := detectChromeExecPath(); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	if headless {
		opts = append(opts, chromedp.Headless, chromedp.DisableGPU)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	cancel := func() { cancelAlloc() }
	return &ChromeBrowser{allocCtx: allocCtx}, cancel
}

func detectChromeExecPath() string {
	candidates := []string{
		strings.TrimSpace(os.Getenv("CHROME_BIN")),
		strings.TrimSpace(os.Getenv("CHROME_PATH")),
		"/usr/bin/google-chrome",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "brave-browser", "microsoft-edge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func (b *ChromeBrowser) Render(ctx context.Context, urlStr string, site config.SiteConfig) (string, error) {
	tabCtx, cancelTab := chromedp.NewContext(b.allocCtx, chromedp.WithErrorf(chromeBrowserErrorf))
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

	scrollToBottom := func() error {
		if !site.ListPage.Pagination.InfiniteScroll {
			return nil
		}
		scrollAttempts := site.ListPage.Pagination.ScrollMaxAttempts
		if scrollAttempts <= 0 {
			scrollAttempts = 8
		}
		tasks := make([]chromedp.Action, 0, scrollAttempts*2)
		for i := 0; i < scrollAttempts; i++ {
			tasks = append(tasks, chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil), chromedp.Sleep(700*time.Millisecond))
		}
		return chromedp.Run(timeoutCtx, tasks...)
	}

	clickLoadMoreUntilExhausted := func() error {
		loadMoreSelector := strings.TrimSpace(site.ListPage.Pagination.LoadMoreSelector)
		if loadMoreSelector == "" {
			if strings.EqualFold(site.ListPage.Pagination.Type, "load_more") || strings.EqualFold(site.ListPage.Pagination.Type, "loadmore") {
				loadMoreSelector = site.ListPage.Pagination.NextSelector
			}
		}

		maxClicks := site.ListPage.Pagination.ClickMaxAttempts
		if maxClicks <= 0 {
			maxClicks = 20
		}

		lastCount := -1
		cardSelector := strings.TrimSpace(site.ListPage.CardSelector)
		for i := 0; i < maxClicks; i++ {
			before := -1
			if cardSelector != "" {
				countJS := fmt.Sprintf(`document.querySelectorAll(%q).length`, cardSelector)
				if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(countJS, &before)); err != nil {
					before = -1
				}
			}
			var clicked bool
			clickJS := ""
			if loadMoreSelector != "" {
				clickJS = fmt.Sprintf(`(() => {
					const btn = document.querySelector(%q);
					if (!btn) return false;
					const style = window.getComputedStyle(btn);
					if (style.display === "none" || style.visibility === "hidden") return false;
					if (btn.disabled || btn.getAttribute("aria-disabled") === "true") return false;
					btn.scrollIntoView({behavior: "instant", block: "center"});
					btn.click();
					return true;
				})()`, loadMoreSelector)
			} else {
				// Auto-detect "load more" controls when a selector is not provided.
				clickJS = `(() => {
					const candidates = Array.from(document.querySelectorAll('button, a, [role="button"]'));
					const re = /(load|show|view)\s+more|more\s+(results|vehicles|inventory|cars|listings)/i;
					for (const el of candidates) {
						const text = (el.textContent || '').trim();
						if (!re.test(text)) continue;
						const style = window.getComputedStyle(el);
						if (style.display === "none" || style.visibility === "hidden") continue;
						if (el.disabled || el.getAttribute("aria-disabled") === "true") continue;
						el.scrollIntoView({behavior: "instant", block: "center"});
						el.click();
						return true;
					}
					return false;
				})()`
			}
			if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(clickJS, &clicked)); err != nil || !clicked {
				break
			}
			if err := chromedp.Run(timeoutCtx, chromedp.Sleep(900*time.Millisecond)); err != nil {
				break
			}
			waitForListSelector()
			if err := scrollToBottom(); err != nil {
				return err
			}
			if cardSelector == "" {
				continue
			}
			after := -1
			countJS := fmt.Sprintf(`document.querySelectorAll(%q).length`, cardSelector)
			if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(countJS, &after)); err != nil {
				continue
			}
			if after <= before || after == lastCount {
				break
			}
			lastCount = after
		}
		return nil
	}

	capturePage := func() (string, error) {
		if err := scrollToBottom(); err != nil {
			return "", err
		}
		tasks := []chromedp.Action{}
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

	if err := clickLoadMoreUntilExhausted(); err == nil {
		if pageHTML, err = capturePage(); err == nil {
			pages[0] = pageHTML
		}
	}

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

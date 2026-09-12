// snb-sync scrapes SNB Motors through an existing Chrome CDP session.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
)

const sourceURL = "https://www.snbmotors.com/cars-for-sale"

var pagePattern = regexp.MustCompile(`(?i)page\s+(\d+)\s+of\s+(\d+)`)

var site = config.SiteConfig{
	BaseURL: sourceURL,
	ListPage: config.ListPageConfig{
		CardSelector: "li.vehicle-snapshot", TitleSelector: ".vehicle-snapshot__title",
		URLSelector: "a[href*='/details/']", StockSelector: "[data-stock], [itemprop='sku'], [class*='stock']",
		PriceSelector: ".vehicle-snapshot__main-info, [class*='price']", MileageSelector: ".mileage, [class*='mileage']", ImageSelector: "img",
	},
	DetailPage: config.DetailPageConfig{
		ImageSelectors: []string{"[class*='gallery'] img", "img[data-src*='carsforsale.com']", "img[src*='carsforsale.com']"},
		VINSelector:    "[itemprop='vehicleIdentificationNumber'], [data-vin]", StockSelector: "[itemprop='sku'], [data-stock], [class*='stock']",
	},
}

type snapshot struct {
	URL       string                `json:"url"`
	ScrapedAt time.Time             `json:"scrapedAt"`
	Complete  bool                  `json:"complete"`
	ItemCount int                   `json:"itemCount"`
	Errors    []string              `json:"errors"`
	Items     []model.InventoryItem `json:"items"`
}

// Feed browser HTML to the existing detail parser without any HTTP fallback.
type savedHTML string

func (h savedHTML) Fetch(context.Context, string) (string, error) { return string(h), nil }

func main() {
	log.SetFlags(log.Ltime)
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	cdp := flag.String("cdp", env("CDP_URL", "http://127.0.0.1:9222"), "existing Chrome debugging endpoint (localhost only)")
	output := flag.String("output", "scrape-snbmotors-"+time.Now().Format("20060102T150405")+".json", "local result file, including partial results on failure")
	dry := flag.Bool("dry-run", false, "save inventory without uploading")
	flag.Parse()
	seconds, err := strconv.Atoi(env("TIMEOUT_SEC", "600"))
	if err != nil || seconds <= 0 {
		return errors.New("TIMEOUT_SEC must be a positive integer")
	}
	endpoint, err := url.Parse(*cdp)
	if err != nil || endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "ws") || !localHost(endpoint.Hostname()) {
		return errors.New("CDP_URL must be a localhost http:// or ws:// endpoint")
	}
	cloud, key := strings.TrimRight(os.Getenv("CLOUD_URL"), "/"), os.Getenv("SERVICE_KEY")
	if !*dry {
		u, err := url.Parse(cloud)
		if err != nil || u == nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || key == "" {
			return errors.New("CLOUD_URL (http/https) and SERVICE_KEY are required unless -dry-run is used")
		}
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, time.Duration(seconds)*time.Second)
	defer cancel()
	alloc, release := chromedp.NewRemoteAllocator(ctx, *cdp)
	defer release()
	tab, closeTab := chromedp.NewContext(alloc)
	defer closeTab()
	log.Print("[chrome] connecting; a dedicated scrape tab will open. Complete any challenge in that tab.")
	if err := chromedp.Run(tab); err != nil {
		return fmt.Errorf("connect to Chrome (start it with remote debugging first): %w", err)
	}

	result := snapshot{URL: sourceURL, ScrapedAt: time.Now().UTC(), Items: []model.InventoryItem{}, Errors: []string{}}
	defer func() {
		if runErr != nil {
			result.Errors = append(result.Errors, runErr.Error())
		}
		result.ItemCount = len(result.Items)
		if err := save(*output, result); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	seen := map[string]bool{}
	pages, expected := 1, 0
	for page := 1; page <= pages; page++ {
		address := availablePageURL(page)
		html, err := fetchPage(tab, address, "li.vehicle-snapshot", fmt.Sprintf("list page %d", page))
		if err != nil {
			return err
		}
		if err := verifyAvailableFilter(html); err != nil {
			return err
		}
		current, total, count, err := pagination(html)
		if err != nil {
			return err
		}
		if current != page {
			return fmt.Errorf("requested page %d but received page %d; refusing partial sync", page, current)
		}
		if page == 1 {
			pages, expected = total, count
			log.Printf("[filter] Available: %d vehicles across %d pages", expected, pages)
		} else if total != pages || count != expected {
			return errors.New("inventory totals changed during scraping; rerun to get a consistent inventory")
		}
		items, extractionErrors := (scrape.DOMExtractor{}).Extract(tab, html, address, site)
		if len(extractionErrors) != 0 || len(items) == 0 {
			return fmt.Errorf("page %d has no usable inventory or extraction errors", page)
		}
		for _, item := range items {
			if !validDetailURL(item.URL) {
				return fmt.Errorf("unexpected detail URL %q", item.URL)
			}
			if seen[item.URL] {
				return fmt.Errorf("repeated vehicle %s on page %d; refusing partial sync", item.URL, page)
			}
			seen[item.URL] = true
			result.Items = append(result.Items, item)
		}
		log.Printf("[list] page %d/%d: %d vehicles; %d unique collected", page, pages, len(items), len(result.Items))
	}
	if expected > 0 && len(result.Items) != expected {
		return fmt.Errorf("collected %d vehicles, site reports %d; refusing partial sync", len(result.Items), expected)
	}
	for i, item := range result.Items {
		html, err := fetchPage(tab, item.URL, ".vdp-info-block__info-item-title, [itemprop='vehicleIdentificationNumber']", fmt.Sprintf("detail %d/%d", i+1, len(result.Items)))
		if err != nil {
			return err
		}
		// Listing normalization may synthesize stockId from the URL. Let the
		// detail page supply the actual dealer stock number before falling back.
		detailItem := item
		detailItem.StockID = ""
		updated, err := (scrape.HTMLDetailFetcher{Fetcher: savedHTML(html)}).FetchDetails(tab, detailItem, site)
		if err != nil {
			return fmt.Errorf("parse details %s: %w", item.URL, err)
		}
		if updated.VIN == "" && updated.StockID == "" {
			return fmt.Errorf("detail page %s lacks VIN and stock number; refusing partial sync", item.URL)
		}
		updated = scrape.NormalizeItem(item.URL, updated)
		result.Items[i] = updated
		log.Printf("[detail] %d/%d: %s; %d photos", i+1, len(result.Items), updated.Title, len(updated.Images))
	}
	result.Complete, result.ItemCount = true, len(result.Items)
	if err := save(*output, result); err != nil {
		return err
	}
	log.Printf("[log] saved %d vehicles to %s", len(result.Items), *output)
	if *dry {
		log.Print("[done] dry run; no upload")
		return nil
	}
	log.Printf("[cloud] syncing %d vehicles to %s", len(result.Items), cloud)
	if err := upload(ctx, cloud, key, result.Items); err != nil {
		return fmt.Errorf("upload failed (inventory saved at %s): %w", *output, err)
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func localHost(host string) bool { return host == "localhost" || host == "127.0.0.1" || host == "::1" }

// Keep the original sourceURL as the cloud cache key, but always filter crawls.
func availablePageURL(page int) string {
	return fmt.Sprintf("%s?SoldStatus=AvailableVehicles&PageNumber=%d&PageSize=100", sourceURL, page)
}

func verifyAvailableFilter(html string) error {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return err
	}
	options := doc.Find("select[name='SoldStatus']").First().Find("option")
	selected := options.Filter("[selected]").First()
	if selected.Length() == 0 {
		selected = options.First()
	}
	if selected.AttrOr("value", "") != "AvailableVehicles" {
		return errors.New("site did not retain the Available status filter; refusing to sync unfiltered inventory")
	}
	return nil
}

func validDetailURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() == "www.snbmotors.com" && strings.HasPrefix(u.Path, "/details/")
}

func fetchPage(tab context.Context, address, selector, label string) (string, error) {
	ctx, cancel := context.WithTimeout(tab, 120*time.Second)
	defer cancel()
	started := time.Now()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				log.Printf("[%s] waiting %s; check the Chrome tab for a challenge", label, time.Since(started).Round(time.Second))
			}
		}
	}()
	log.Printf("[%s] loading %s", label, address)
	var html string
	err := chromedp.Run(ctx, chromedp.Navigate(address), chromedp.WaitReady(selector, chromedp.ByQuery), chromedp.OuterHTML("html", &html, chromedp.ByQuery))
	if err != nil {
		return "", fmt.Errorf("%s: %w (no inventory will be uploaded)", label, err)
	}
	log.Printf("[%s] read %s HTML bytes in %s", label, strconv.Itoa(len(html)), time.Since(started).Round(time.Millisecond))
	return html, nil
}

func pagination(html string) (current, pages, total int, err error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0, 0, 0, err
	}
	m := pagePattern.FindStringSubmatch(doc.Find("[class*='pagination']").Text())
	if len(m) != 3 {
		return 0, 0, 0, errors.New("missing 'Page X of Y' pagination; refusing to guess completeness")
	}
	current, _ = strconv.Atoi(m[1])
	pages, _ = strconv.Atoi(m[2])
	if current < 1 || pages < current || pages > 1000 {
		return 0, 0, 0, errors.New("invalid pagination counts")
	}
	raw := strings.ReplaceAll(doc.Find("input.data-inventory-total-records").First().AttrOr("value", ""), ",", "")
	if raw != "" {
		total, err = strconv.Atoi(raw)
		if err != nil || total < 1 {
			return 0, 0, 0, errors.New("invalid inventory total")
		}
	}
	return current, pages, total, nil
}

func save(path string, result snapshot) error {
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func upload(ctx context.Context, cloud, key string, items []model.InventoryItem) error {
	b, err := json.Marshal(struct {
		URL        string                `json:"url"`
		Items      []model.InventoryItem `json:"items"`
		SkipUpsert bool                  `json:"skipUpsert"`
	}{sourceURL, items, true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cloud+"/v1/scrape/sync", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", key)
	client := &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	log.Printf("[cloud] %s", body)
	return nil
}

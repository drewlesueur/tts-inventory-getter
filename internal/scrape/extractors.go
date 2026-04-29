package scrape

import (
	"context"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/example/inventory-scraper/internal/config"
	"github.com/example/inventory-scraper/internal/model"
)

type DOMExtractor struct{}

type LoopHTMLExtractor struct{}

type RegexExtractor struct{}

func (d DOMExtractor) Extract(_ context.Context, html, pageURL string, site config.SiteConfig) ([]model.InventoryItem, []model.StructuredError) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, []model.StructuredError{{Code: "PARSE_DOM", Message: err.Error()}}
	}
	items := make([]model.InventoryItem, 0)
	doc.Find(site.ListPage.CardSelector).Each(func(_ int, s *goquery.Selection) {
		item := model.InventoryItem{}
		item.Title = s.Find(site.ListPage.TitleSelector).First().Text()
		if href, ok := s.Find(site.ListPage.URLSelector).First().Attr("href"); ok {
			item.URL = href
		}
		if img, ok := s.Find(site.ListPage.ImageSelector).First().Attr("src"); ok {
			item.PrimaryImage = img
			item.Images = []string{img}
		}
		item.StockID = s.Find(site.ListPage.StockSelector).First().Text()
		item.Price = s.Find(site.ListPage.PriceSelector).First().Text()
		item.Mileage = s.Find(site.ListPage.MileageSelector).First().Text()
		items = append(items, NormalizeItem(pageURL, item))
	})
	return items, nil
}

func (l LoopHTMLExtractor) Extract(_ context.Context, html, pageURL string, _ config.SiteConfig) ([]model.InventoryItem, []model.StructuredError) {
	const marker = `<div data-elementor-type="loop-item"`
	starts := make([]int, 0, 128)
	for i := 0; ; {
		idx := strings.Index(html[i:], marker)
		if idx == -1 {
			break
		}
		abs := i + idx
		starts = append(starts, abs)
		i = abs + len(marker)
	}
	if len(starts) == 0 {
		return nil, nil
	}

	items := make([]model.InventoryItem, 0, len(starts))
	for i, start := range starts {
		end := len(html)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		segment := html[start:end]

		classes := ""
		if m := regexp.MustCompile(`(?is)<div[^>]+class="([^"]+)"`).FindStringSubmatch(segment); len(m) > 1 {
			classes = m[1]
		}
		if !strings.Contains(classes, "vehicle type-vehicle") {
			continue
		}

		item := model.InventoryItem{}
		if m := regexp.MustCompile(`(?is)href="([^"]*vehicle-details/[^"]+)"`).FindStringSubmatch(segment); len(m) > 1 {
			item.URL = m[1]
		}
		if m := regexp.MustCompile(`(?is)<img[^>]+data-src="([^"]+)"`).FindStringSubmatch(segment); len(m) > 1 {
			item.PrimaryImage = m[1]
			item.Images = []string{m[1]}
		} else if m := regexp.MustCompile(`(?is)<img[^>]+src="([^"]+)"`).FindStringSubmatch(segment); len(m) > 1 && !strings.HasPrefix(m[1], "data:image/") {
			item.PrimaryImage = m[1]
			item.Images = []string{m[1]}
		}
		if m := regexp.MustCompile(`(?is)STOCK#\s*([A-Za-z0-9\-]+)`).FindStringSubmatch(segment); len(m) > 1 {
			item.StockID = m[1]
		}
		if m := regexp.MustCompile(`(?is)<h[1-4][^>]*>\s*<a[^>]*href="[^"]*vehicle-details/[^"]+"[^>]*>([^<]+)</a>`).FindStringSubmatch(segment); len(m) > 1 {
			item.Title = m[1]
		} else if m := regexp.MustCompile(`(?is)<a[^>]*href="[^"]*vehicle-details/[^"]+"[^>]*>([^<]+)</a>`).FindStringSubmatch(segment); len(m) > 1 {
			item.Title = m[1]
		}

		item = NormalizeItem(pageURL, item)
		if item.URL == "" && item.StockID == "" && item.Title == "" && item.PrimaryImage == "" {
			continue
		}
		items = append(items, item)
	}

	return Dedupe(items), nil
}

func (r RegexExtractor) Extract(_ context.Context, html, pageURL string, site config.SiteConfig) ([]model.InventoryItem, []model.StructuredError) {
	var items []model.InventoryItem
	var errs []model.StructuredError
	for _, pat := range site.Regex.Stock {
		re := regexp.MustCompile(pat)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			it := NormalizeItem(pageURL, model.InventoryItem{StockID: m[1]})
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		errs = append(errs, model.StructuredError{Code: "FALLBACK_EMPTY", Message: "regex fallback found no items"})
	}
	return items, errs
}

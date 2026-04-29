package scrape

import (
	"context"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/example/inventory-scraper/internal/config"
	"github.com/example/inventory-scraper/internal/model"
)

type HTMLDetailFetcher struct{ Fetcher Fetcher }

func (d HTMLDetailFetcher) FetchDetails(ctx context.Context, item model.InventoryItem, site config.SiteConfig) (model.InventoryItem, error) {
	if item.URL == "" {
		return item, nil
	}
	html, err := d.Fetcher.Fetch(ctx, item.URL)
	if err != nil {
		return item, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return item, err
	}
	imgSet := map[string]struct{}{}
	for _, sel := range site.DetailPage.ImageSelectors {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			if src, ok := s.Attr("src"); ok && src != "" && isLikelyVehicleImageURL(src) {
				imgSet[src] = struct{}{}
			}
			if src, ok := s.Attr("data-src"); ok && src != "" && isLikelyVehicleImageURL(src) {
				imgSet[src] = struct{}{}
			}
		})
	}
	imgs := make([]string, 0, len(imgSet))
	for img := range imgSet {
		imgs = append(imgs, img)
	}
	if len(imgs) > 0 {
		item.Images = imgs
		item.PrimaryImage = imgs[0]
	}
	if site.DetailPage.VINSelector != "" && item.VIN == "" {
		item.VIN = doc.Find(site.DetailPage.VINSelector).First().Text()
	}
	return NormalizeItem(item.URL, item), nil
}

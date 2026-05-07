package scrape

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type HTMLDetailFetcher struct {
	Fetcher    Fetcher
	Browser    Browser
	ImageSizes *ImageSizeCache
}

func (d HTMLDetailFetcher) FetchDetails(ctx context.Context, item model.InventoryItem, site config.SiteConfig) (model.InventoryItem, error) {
	if item.URL == "" {
		return item, nil
	}
	html, err := d.Fetcher.Fetch(ctx, item.URL)
	if err != nil {
		if d.Browser == nil {
			return item, err
		}
		rendered, renderErr := d.Browser.Render(ctx, item.URL, site)
		if renderErr != nil {
			return item, err
		}
		html = rendered
	}
	return populateDetailsFromHTML(ctx, d.ImageSizes, item, site, html)
}

func populateDetailsFromHTML(ctx context.Context, sizeCache *ImageSizeCache, item model.InventoryItem, site config.SiteConfig, html string) (model.InventoryItem, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return item, err
	}
	imgSet := map[string]struct{}{}
	for _, sel := range site.DetailPage.ImageSelectors {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			if !imgAttrSizeOK(s) {
				return
			}
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
	if len(imgs) > 0 && sizeCache != nil {
		imgs = filterByImageSize(ctx, sizeCache, imgs, minImageDimension)
	}
	if len(imgs) > 0 {
		item.Images = imgs
		if item.PrimaryImage == "" {
			item.PrimaryImage = imgs[0]
		}
	}
	if site.DetailPage.VINSelector != "" && item.VIN == "" {
		item.VIN = doc.Find(site.DetailPage.VINSelector).First().Text()
	}
	if item.VIN == "" {
		doc.Find("li, div, tr, p, span, dt, dd").EachWithBreak(func(_ int, s *goquery.Selection) bool {
			label := strings.ToUpper(clean(s.Text()))
			if label == "VIN" || strings.HasPrefix(label, "VIN ") || strings.Contains(label, " VIN ") {
				parent := s.Parent()
				candidate := clean(parent.Text())
				if candidate == "" {
					candidate = clean(s.Next().Text())
				}
				if m := findVINInText(candidate); m != "" {
					item.VIN = m
					return false
				}
				// Try nearby siblings to catch label/value split markup.
				s.NextAll().EachWithBreak(func(_ int, sib *goquery.Selection) bool {
					if m := findVINInText(clean(sib.Text())); m != "" {
						item.VIN = m
						return false
					}
					return true
				})
				if item.VIN != "" {
					return false
				}
			}
			return true
		})
	}
	if item.VIN == "" {
		patterns := site.Regex.VIN
		if len(patterns) == 0 {
			patterns = []string{`\b([A-HJ-NPR-Z0-9]{17})\b`}
		}
		for _, pat := range patterns {
			re, err := regexp.Compile(pat)
			if err != nil {
				continue
			}
			if m := re.FindStringSubmatch(html); len(m) > 1 {
				item.VIN = m[1]
				break
			}
		}
	}
	return NormalizeItem(item.URL, item), nil
}

func findVINInText(text string) string {
	re := regexp.MustCompile(`\b([A-HJ-NPR-Z0-9]{17})\b`)
	if m := re.FindStringSubmatch(strings.ToUpper(text)); len(m) > 1 {
		return m[1]
	}
	return ""
}

func imgAttrSizeOK(s *goquery.Selection) bool {
	w, _ := strconv.Atoi(strings.TrimSpace(s.AttrOr("width", "")))
	h, _ := strconv.Atoi(strings.TrimSpace(s.AttrOr("height", "")))
	if w > 0 && w < minImageDimension {
		return false
	}
	if h > 0 && h < minImageDimension {
		return false
	}
	return true
}

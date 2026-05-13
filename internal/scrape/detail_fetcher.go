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
	fillCommonVehicleFields(&item, doc, html)
	return NormalizeItem(item.URL, item), nil
}

func findVINInText(text string) string {
	re := regexp.MustCompile(`\b([A-HJ-NPR-Z0-9]{17})\b`)
	if m := re.FindStringSubmatch(strings.ToUpper(text)); len(m) > 1 {
		return m[1]
	}
	return ""
}

func fillCommonVehicleFields(item *model.InventoryItem, doc *goquery.Document, html string) {
	kv := map[string]string{}

	doc.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		cells := tr.Find("th,td")
		if cells.Length() >= 2 {
			k := clean(strings.ToLower(cells.First().Text()))
			v := clean(cells.Eq(1).Text())
			if k != "" && v != "" {
				kv[k] = v
			}
		}
	})
	doc.Find("dt").Each(func(_ int, dt *goquery.Selection) {
		k := clean(strings.ToLower(dt.Text()))
		v := clean(dt.Next().Text())
		if k != "" && v != "" {
			kv[k] = v
		}
	})

	if item.Make == "" {
		item.Make = pickValueByLabel(kv, "make")
	}
	if item.Model == "" {
		item.Model = pickValueByLabel(kv, "model")
	}
	if item.Year == "" {
		item.Year = pickValueByLabel(kv, "year")
	}
	if item.Color == "" {
		item.Color = pickValueByLabel(kv, "color", "exterior color", "ext color")
	}

	if item.Color == "" {
		re := regexp.MustCompile(`(?i)\b(?:exterior\s+color|color)\b[:\s]+([a-z0-9][a-z0-9\s\-\/]{1,40})`)
		if m := re.FindStringSubmatch(html); len(m) > 1 {
			item.Color = clean(m[1])
		}
	}
}

func pickValueByLabel(kv map[string]string, labels ...string) string {
	for _, want := range labels {
		want = strings.ToLower(strings.TrimSpace(want))
		for k, v := range kv {
			if k == want || strings.HasPrefix(k, want+":") || strings.Contains(k, want) {
				return v
			}
		}
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

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
	imgSet := map[string]bool{}
	for _, sel := range site.DetailPage.ImageSelectors {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			if !imgAttrSizeOK(s) {
				return
			}
			if src := detailImageURL(s); src != "" && isLikelyVehicleImageURL(src) {
				imgSet[src] = imgSet[src] || imgAttrSizeKnownLarge(s)
			}
		})
	}
	imgs := make([]string, 0, len(imgSet))
	probeImgs := make([]string, 0, len(imgSet))
	for img, knownLarge := range imgSet {
		imgs = append(imgs, img)
		if !knownLarge {
			probeImgs = append(probeImgs, img)
		}
	}
	if len(probeImgs) > 0 && sizeCache != nil {
		probed := filterByImageSize(ctx, sizeCache, probeImgs, minImageDimension)
		keep := make(map[string]struct{}, len(probed))
		for _, img := range probed {
			keep[img] = struct{}{}
		}
		filtered := imgs[:0]
		for _, img := range imgs {
			if imgSet[img] {
				filtered = append(filtered, img)
				continue
			}
			if _, ok := keep[img]; ok {
				filtered = append(filtered, img)
			}
		}
		imgs = filtered
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
	if site.DetailPage.StockSelector != "" && item.StockID == "" {
		item.StockID = doc.Find(site.DetailPage.StockSelector).First().Text()
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
	if item.StockID == "" {
		doc.Find("li, div, tr, p, span, dt, dd").EachWithBreak(func(_ int, s *goquery.Selection) bool {
			label := strings.ToUpper(clean(s.Text()))
			if label == "STOCK" || strings.Contains(label, "STOCK #") || strings.Contains(label, "STOCK:") {
				candidate := clean(s.Parent().Text())
				if candidate == "" {
					candidate = clean(s.Next().Text())
				}
				if m := findStockIDInText(candidate); m != "" {
					item.StockID = m
					return false
				}
				s.NextAll().EachWithBreak(func(_ int, sib *goquery.Selection) bool {
					if m := findStockIDInText(clean(sib.Text())); m != "" {
						item.StockID = m
						return false
					}
					return true
				})
				if item.StockID != "" {
					return false
				}
			}
			return true
		})
	}
	if item.StockID == "" {
		patterns := site.Regex.Stock
		if len(patterns) == 0 {
			patterns = []string{`(?i)stock\s*#?[:\-]?\s*([a-z0-9\-]+)`}
		}
		for _, pat := range patterns {
			re, err := regexp.Compile(pat)
			if err != nil {
				continue
			}
			if m := re.FindStringSubmatch(html); len(m) > 1 {
				item.StockID = m[1]
				break
			}
		}
	}
	fillCommonVehicleFields(&item, doc, html)
	return NormalizeItem(item.URL, item), nil
}

func detailImageURL(s *goquery.Selection) string {
	if src := firstNonEmptyImageAttr(s); src != "" {
		return src
	}
	for _, attr := range []string{"href", "data-thumbnail", "data-bg", "data-background-image"} {
		if src := strings.TrimSpace(s.AttrOr(attr, "")); src != "" {
			return src
		}
	}
	return urlFromStyle(s.AttrOr("style", ""))
}

func findVINInText(text string) string {
	re := regexp.MustCompile(`\b([A-HJ-NPR-Z0-9]{17})\b`)
	if m := re.FindStringSubmatch(strings.ToUpper(text)); len(m) > 1 {
		return m[1]
	}
	return ""
}

func findStockIDInText(text string) string {
	re := regexp.MustCompile(`(?i)\bstock\s*#?[:\-]?\s*([a-z0-9\-]+)\b`)
	if m := re.FindStringSubmatch(strings.TrimSpace(text)); len(m) > 1 {
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
	w := imageAttrDimension(s, "width")
	h := imageAttrDimension(s, "height")
	if w > 0 && w < minImageDimension {
		return false
	}
	if h > 0 && h < minImageDimension {
		return false
	}
	return true
}

func imgAttrSizeKnownLarge(s *goquery.Selection) bool {
	return imageAttrDimension(s, "width") >= minImageDimension && imageAttrDimension(s, "height") >= minImageDimension
}

func imageAttrDimension(s *goquery.Selection, name string) int {
	for _, attr := range []string{name, "data-" + name} {
		if n, err := strconv.Atoi(strings.TrimSpace(s.AttrOr(attr, ""))); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type DOMExtractor struct{}

type LoopHTMLExtractor struct{}

type RegexExtractor struct{}
type NextDataExtractor struct{}

var (
	loopClassRe      = regexp.MustCompile(`(?is)<div[^>]+class="([^"]+)"`)
	loopVehicleURLRe = regexp.MustCompile(`(?is)href="([^"]*vehicle-details/[^"]+)"`)
	loopDataSrcImgRe = regexp.MustCompile(`(?is)<img[^>]+data-src="([^"]+)"`)
	loopSrcImgRe     = regexp.MustCompile(`(?is)<img[^>]+src="([^"]+)"`)
	loopStockTextRe  = regexp.MustCompile(`(?is)STOCK#\s*([A-Za-z0-9\-]+)`)
	loopStockClassRe = regexp.MustCompile(`(?:^|\s)stock-([a-zA-Z0-9\-]+)(?:\s|$)`)
	loopTitleHRe     = regexp.MustCompile(`(?is)<h[1-4][^>]*>\s*<a[^>]*href="[^"]*vehicle-details/[^"]+"[^>]*>([^<]+)</a>`)
	loopTitleAnyRe   = regexp.MustCompile(`(?is)<a[^>]*href="[^"]*vehicle-details/[^"]+"[^>]*>([^<]+)</a>`)
	loopMakeClassRe  = regexp.MustCompile(`(?:^|\s)make-([a-zA-Z0-9\-]+)(?:\s|$)`)
	loopModelClassRe = regexp.MustCompile(`(?:^|\s)model-([a-zA-Z0-9\-]+)(?:\s|$)`)
	loopTitlePartsRe = regexp.MustCompile(`^\s*((?:19|20)\d{2})?\s*([A-Za-z0-9]+)?\s*(.*)$`)
	loopPriceRe      = regexp.MustCompile(`(?is)\$\s*([0-9][0-9,]*)`)
	loopMileageRe    = regexp.MustCompile(`(?is)([0-9][0-9,]*)\s*(?:miles?|mi\b)`)
)

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
		if m := loopClassRe.FindStringSubmatch(segment); len(m) > 1 {
			classes = m[1]
		}
		if !strings.Contains(classes, "vehicle type-vehicle") {
			continue
		}

		item := model.InventoryItem{}
		if m := loopVehicleURLRe.FindStringSubmatch(segment); len(m) > 1 {
			item.URL = m[1]
		}
		if m := loopDataSrcImgRe.FindStringSubmatch(segment); len(m) > 1 {
			item.PrimaryImage = m[1]
			item.Images = []string{m[1]}
		} else if m := loopSrcImgRe.FindStringSubmatch(segment); len(m) > 1 && !strings.HasPrefix(m[1], "data:image/") {
			item.PrimaryImage = m[1]
			item.Images = []string{m[1]}
		}
		if m := loopStockTextRe.FindStringSubmatch(segment); len(m) > 1 {
			item.StockID = m[1]
		}
		if item.StockID == "" {
			if m := loopStockClassRe.FindStringSubmatch(classes); len(m) > 1 {
				item.StockID = strings.ToUpper(strings.ReplaceAll(m[1], "-", ""))
			}
		}
		if m := loopTitleHRe.FindStringSubmatch(segment); len(m) > 1 {
			item.Title = m[1]
		} else if m := loopTitleAnyRe.FindStringSubmatch(segment); len(m) > 1 {
			item.Title = m[1]
		}
		if m := loopPriceRe.FindStringSubmatch(segment); len(m) > 1 {
			item.Price = "$" + m[1]
		}
		if m := loopMileageRe.FindStringSubmatch(segment); len(m) > 1 {
			item.Mileage = m[1] + " mi"
		}

		item = NormalizeItem(pageURL, item)
		if (item.Make == "" || item.Model == "") && item.Title != "" {
			if m := loopTitlePartsRe.FindStringSubmatch(item.Title); len(m) == 4 {
				if item.Year == "" {
					item.Year = clean(m[1])
				}
				if item.Make == "" {
					item.Make = clean(m[2])
				}
				if item.Model == "" {
					item.Model = clean(m[3])
				}
			}
		}
		if item.Make == "" {
			if m := loopMakeClassRe.FindStringSubmatch(classes); len(m) > 1 {
				item.Make = clean(strings.ReplaceAll(m[1], "-", " "))
			}
		}
		if item.Model == "" {
			if m := loopModelClassRe.FindStringSubmatch(classes); len(m) > 1 {
				item.Model = clean(strings.ReplaceAll(m[1], "-", " "))
			}
		}
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

func (n NextDataExtractor) Extract(_ context.Context, html, pageURL string, _ config.SiteConfig) ([]model.InventoryItem, []model.StructuredError) {
	const marker = `<script id="__NEXT_DATA__" type="application/json">`
	start := strings.Index(html, marker)
	if start == -1 {
		return nil, nil
	}
	start += len(marker)
	end := strings.Index(html[start:], `</script>`)
	if end == -1 {
		return nil, []model.StructuredError{{Code: "NEXT_DATA_PARSE", Message: "next data script end not found"}}
	}
	raw := strings.TrimSpace(html[start : start+end])
	if raw == "" {
		return nil, nil
	}

	var root any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil, []model.StructuredError{{Code: "NEXT_DATA_PARSE", Message: err.Error()}}
	}

	items := make([]model.InventoryItem, 0, 256)
	seen := map[string]struct{}{}
	walkForVehicleMaps(root, func(m map[string]any) {
		item, ok := vehicleMapToItem(pageURL, m)
		if !ok {
			return
		}
		key := item.URL + "|" + item.StockID + "|" + item.VIN + "|" + item.Title
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		items = append(items, NormalizeItem(pageURL, item))
	})

	if len(items) == 0 {
		return nil, nil
	}
	return Dedupe(items), nil
}

func walkForVehicleMaps(v any, fn func(map[string]any)) {
	switch t := v.(type) {
	case map[string]any:
		if looksLikeVehicleMap(t) {
			fn(t)
		}
		for _, vv := range t {
			walkForVehicleMaps(vv, fn)
		}
	case []any:
		for _, vv := range t {
			walkForVehicleMaps(vv, fn)
		}
	}
}

func looksLikeVehicleMap(m map[string]any) bool {
	makeV := pickString(m, "make")
	modelV := pickString(m, "model")
	yearV := pickYearString(m)
	stock := pickString(m, "stock", "stock_no", "stocknumber", "stock_number", "stockid")
	url := pickURL(m)
	score := 0
	if makeV != "" {
		score++
	}
	if modelV != "" {
		score++
	}
	if yearV != "" {
		score++
	}
	if stock != "" {
		score++
	}
	if url != "" {
		score++
	}
	return score >= 3
}

func vehicleMapToItem(pageURL string, m map[string]any) (model.InventoryItem, bool) {
	it := model.InventoryItem{}
	it.Make = pickString(m, "make")
	it.Model = pickString(m, "model")
	it.Year = pickYearString(m)
	it.StockID = pickString(m, "stock", "stock_no", "stocknumber", "stock_number", "stockid")
	it.VIN = pickString(m, "vin", "vin_number")
	it.Mileage = pickString(m, "mileage", "miles", "odometer")
	it.Price = pickPriceString(m)
	it.Engine = pickString(m, "engine", "engine_description", "engine_type", "motor")
	it.Transmission = pickString(m, "transmission", "transmission_description", "trans")
	it.DriveType = pickString(m, "drive_type", "drivetrain", "drive", "wheel_drive")
	it.Color = pickString(m, "color", "exterior_color", "ext_color")
	it.URL = pickURL(m)

	if title := pickString(m, "title", "name", "vehicle_title"); title != "" {
		it.Title = title
	} else {
		parts := []string{it.Year, it.Make, it.Model}
		it.Title = strings.TrimSpace(strings.Join(parts, " "))
	}

	imgs := pickImageList(m)
	if len(imgs) > 0 {
		it.Images = imgs
		it.PrimaryImage = imgs[0]
	}

	normalized := NormalizeItem(pageURL, it)
	if normalized.URL == "" && normalized.StockID == "" && normalized.Title == "" {
		return model.InventoryItem{}, false
	}
	return normalized, true
}

func pickURL(m map[string]any) string {
	candidates := []string{"url", "vehicle_url", "vdp_url", "detail_url", "permalink", "link", "slug"}
	for _, k := range candidates {
		if s := pickString(m, k); s != "" {
			if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
				return s
			}
		}
	}
	return ""
}

func pickPriceString(m map[string]any) string {
	for _, k := range []string{"price", "internet_price", "special_price", "list_price", "sale_price"} {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return t
				}
			case float64:
				if t > 0 {
					return fmt.Sprintf("%.0f", t)
				}
			}
		}
	}
	return ""
}

func pickYearString(m map[string]any) string {
	if s := pickString(m, "year", "model_year"); s != "" {
		re := regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
		if y := re.FindString(s); y != "" {
			return y
		}
		if len(s) == 4 {
			return s
		}
	}
	for _, k := range []string{"year", "model_year"} {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				n := int(t)
				if n >= 1900 && n <= 2099 {
					return strconv.Itoa(n)
				}
			}
		}
	}
	return ""
}

func pickString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return t
				}
			case float64:
				return fmt.Sprintf("%.0f", t)
			}
		}
	}
	return ""
}

func pickImageList(m map[string]any) []string {
	out := make([]string, 0, 4)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "/") {
			return
		}
		out = append(out, s)
	}
	for _, key := range []string{"primary_image", "primary_photo", "image", "image_url", "photo"} {
		add(pickString(m, key))
	}
	for _, key := range []string{"images", "photos", "gallery"} {
		v, ok := m[key]
		if !ok {
			continue
		}
		arr, ok := v.([]any)
		if !ok {
			continue
		}
		for _, el := range arr {
			switch tt := el.(type) {
			case string:
				add(tt)
			case map[string]any:
				for _, k := range []string{"url", "src", "image", "photo"} {
					add(pickString(tt, k))
				}
			}
		}
	}
	return uniqueStrings(out)
}

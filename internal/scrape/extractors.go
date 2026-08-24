package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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
	cssURLRe         = regexp.MustCompile(`url\((?:'([^']+)'|"([^"]+)"|([^'")]+))\)`)
	priceAmountRe    = regexp.MustCompile(`\$\s*([0-9][0-9,]*(?:\.[0-9]{2})?)`)
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
		if href := chooseBestCardURL(s, site.ListPage.URLSelector); href != "" {
			item.URL = href
		}
		if img := pickCardImage(s, site.ListPage.ImageSelector); img != "" {
			item.PrimaryImage = img
			item.Images = []string{img}
		}
		item.StockID = s.Find(site.ListPage.StockSelector).First().Text()
		item.Price = s.Find(site.ListPage.PriceSelector).First().Text()
		if strings.EqualFold(clean(item.Price), "today's price") || strings.EqualFold(clean(item.Price), "todays price") || !priceAmountRe.MatchString(item.Price) {
			if p := extractBestPriceText(s.Text()); p != "" {
				item.Price = p
			}
		}
		item.Mileage = s.Find(site.ListPage.MileageSelector).First().Text()
		if item.URL == "" {
			item.URL = firstAttr(s, "meta[itemprop='url']", "content")
		}
		if item.Title == "" {
			item.Title = firstAttr(s, "meta[itemprop='name']", "content")
		}
		if item.Title == "" {
			// DealerCenter (DWS widgets) renders the title only as text glued to a
			// duplicate hidden span; the modal container's data attribute is clean.
			item.Title = firstAttr(s, "[data-dws-title]", "data-dws-title")
		}
		if item.StockID == "" {
			item.StockID = firstAttr(s, "meta[itemprop='sku']", "content")
		}
		if item.StockID == "" {
			item.StockID = firstAttr(s, "[data-vehicle-stock-no]", "data-vehicle-stock-no")
		}
		if item.VIN == "" {
			item.VIN = firstAttr(s, "meta[itemprop='vehicleIdentificationNumber']", "content")
		}
		if item.VIN == "" {
			// Carfax/history widgets routinely carry the VIN on the card even when
			// nothing else does, which saves a detail fetch per vehicle.
			item.VIN = validVINCandidate(firstAttr(s, "[data-vin]", "data-vin"))
		}
		if item.VIN == "" {
			// DealerCenter cards carry the VIN on the media-modal container.
			item.VIN = validVINCandidate(firstAttr(s, "[data-vehicle-vin]", "data-vehicle-vin"))
		}
		normalized := NormalizeItem(pageURL, item)
		if !looksLikeUsefulListing(normalized) {
			return
		}
		items = append(items, normalized)
	})
	return items, nil
}

func extractBestPriceText(cardText string) string {
	matches := priceAmountRe.FindAllStringSubmatch(cardText, -1)
	if len(matches) == 0 {
		return ""
	}
	// Prefer the first amount shown in the card; for these dealer cards this maps to current/sale price.
	amt := strings.TrimSpace(matches[0][1])
	if amt == "" {
		return ""
	}
	return "$" + amt
}

func chooseBestCardURL(card *goquery.Selection, selector string) string {
	candidates := make([]string, 0, 8)
	card.Find(selector).Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok && strings.TrimSpace(href) != "" {
			candidates = append(candidates, href)
		}
	})
	best := ""
	bestScore := -999
	for _, href := range candidates {
		score := scoreListingURL(href)
		if score > bestScore {
			bestScore = score
			best = href
		}
	}
	if bestScore < 0 {
		return ""
	}
	return best
}

func scoreListingURL(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -100
	}
	l := strings.ToLower(raw)
	if strings.HasPrefix(l, "javascript:") || strings.HasPrefix(l, "mailto:") || strings.HasPrefix(l, "tel:") || l == "#" {
		return -100
	}
	if strings.Contains(l, "google.com/maps") || strings.Contains(l, "api=1&destination=") {
		return -100
	}
	score := 0
	if strings.Contains(l, "/pre-owned-cars/detail/") || strings.Contains(l, "/vehicle-details/") || strings.Contains(l, "/inventory/") {
		score += 10
	}
	if strings.HasPrefix(l, "/") {
		score += 2
	}
	if u, err := url.Parse(l); err == nil && u.Host != "" {
		if strings.Contains(u.Host, "google.") {
			score -= 10
		}
	}
	if strings.Contains(l, "compare") || strings.Contains(l, "schedule") || strings.Contains(l, "finance") || strings.Contains(l, "service") {
		score -= 3
	}
	return score
}

func looksLikeUsefulListing(it model.InventoryItem) bool {
	signals := 0
	if it.Title != "" {
		signals++
	}
	if it.StockID != "" {
		signals++
	}
	if it.Price != "" {
		signals++
	}
	if it.Mileage != "" {
		signals++
	}
	if it.PrimaryImage != "" {
		signals++
	}
	if it.VIN != "" {
		signals++
	}

	urlScore := scoreListingURL(it.URL)
	if urlScore >= 8 {
		return true
	}
	if urlScore >= 3 && signals >= 2 {
		return true
	}
	if it.StockID != "" && signals >= 2 {
		return true
	}
	if it.Title != "" && (it.Price != "" || it.PrimaryImage != "") {
		return true
	}
	return false
}

func firstAttr(s *goquery.Selection, selector, attr string) string {
	v, _ := s.Find(selector).First().Attr(attr)
	return strings.TrimSpace(v)
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
	it.Cylinders = pickString(m, "cylinders", "engine_cylinders", "cylinder")
	it.Horsepower = pickString(m, "horsepower", "hp", "engine_power")
	it.Torque = pickString(m, "torque")
	it.Transmission = pickString(m, "transmission", "transmission_description", "trans")
	it.TransmissionType = pickString(m, "transmission_type", "transmissiontype")
	it.DriveType = pickString(m, "drive_type", "drivetrain", "drive", "wheel_drive")
	it.FuelType = pickString(m, "fuel_type", "fuel", "fueltype")
	it.FuelCapacity = pickString(m, "fuel_capacity", "fuelcapacity", "fuel_tank_capacity")
	it.FuelEconomy = pickString(m, "fuel_economy", "mpg", "combined_mpg")
	it.MilesPerGallon = pickString(m, "miles_per_gallon", "milesPerGallon")
	it.MilesPerLiter = pickString(m, "miles_per_liter", "milesPerLiter")
	it.CityMPG = pickString(m, "city_mpg", "mpg_city")
	it.HighwayMPG = pickString(m, "highway_mpg", "hwy_mpg", "mpg_highway")
	it.CityMPL = pickString(m, "city_mpl", "cityMPL")
	it.HighwayMPL = pickString(m, "highway_mpl", "highwayMPL")
	it.BodyType = pickString(m, "body_type", "body", "bodystyle", "body_style")
	it.SeatInfo = pickString(m, "seat_info", "seats", "seating")
	it.PassengerCapacity = pickString(m, "passengers", "passenger_capacity", "seating_capacity")
	it.TireInfo = pickString(m, "tire_info", "tires", "tire")
	it.FrontTire = pickString(m, "front_tire", "fronttire")
	it.RearTire = pickString(m, "rear_tire", "reartire")
	it.WheelInfo = pickString(m, "wheel_info", "wheels", "wheel")
	it.FrontWheel = pickString(m, "front_wheel", "frontwheel")
	it.RearWheel = pickString(m, "rear_wheel", "rearwheel")
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

func firstNonEmptyImageAttr(s *goquery.Selection) string {
	// Lazy-load attributes come first: carousels often set src to a generic
	// placeholder and put the real image in data-lazy/data-src.
	// Vue/Alpine SSR markup binds the real URL to :src and leaves no plain src,
	// so those are checked last as a fallback.
	keys := []string{"data-lazy", "data-lazy-src", "data-src", "data-original", "data-image", "data-srcset", "srcset", "src", ":src", "v-bind:src", "x-bind:src"}
	for _, k := range keys {
		raw := strings.TrimSpace(s.AttrOr(k, ""))
		if raw == "" {
			continue
		}
		if strings.HasPrefix(k, ":") || strings.Contains(k, "bind:") {
			raw = unquoteBoundAttr(raw)
			if raw == "" {
				continue
			}
		}
		if strings.HasPrefix(strings.ToLower(raw), "data:image/") {
			continue
		}
		if isPlaceholderImageURL(raw) {
			continue
		}
		if k == "srcset" || k == "data-srcset" {
			if u := firstFromSrcset(raw); u != "" {
				return u
			}
			continue
		}
		return raw
	}
	return ""
}

// unquoteBoundAttr pulls the URL out of a framework-bound attribute whose value
// is a JS string literal ("'https://…'"). Anything else is an expression we
// cannot evaluate, so it is rejected rather than guessed at.
func unquoteBoundAttr(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) < 3 {
		return ""
	}
	q := v[0]
	if (q != '\'' && q != '"') || v[len(v)-1] != q {
		return ""
	}
	v = v[1 : len(v)-1]
	// A quote or + inside means concatenation, i.e. a computed expression.
	if strings.ContainsAny(v, "'\"+") {
		return ""
	}
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "/") {
		return ""
	}
	return v
}

// isPlaceholderImageURL filters out generic stock/placeholder graphics that
// carousels show before lazy-loading the real photo.
func isPlaceholderImageURL(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "sports_car_front_view") ||
		strings.Contains(l, "/images/nophoto") ||
		strings.Contains(l, "no-photo") ||
		strings.Contains(l, "placeholder")
}

func firstFromSrcset(raw string) string {
	for _, part := range strings.Split(raw, ",") {
		p := strings.Fields(strings.TrimSpace(part))
		if len(p) == 0 {
			continue
		}
		if p[0] != "" {
			return p[0]
		}
	}
	return ""
}

func pickCardImage(card *goquery.Selection, imgSelector string) string {
	candidates := card.Find(imgSelector)
	firstAny := ""
	firstFromComingSoonAlt := ""
	best := ""
	candidates.EachWithBreak(func(_ int, img *goquery.Selection) bool {
		src := firstNonEmptyImageAttr(img)
		if src == "" {
			return true
		}
		if firstAny == "" {
			firstAny = src
		}
		alt := strings.ToLower(strings.TrimSpace(img.AttrOr("alt", "")))
		if firstFromComingSoonAlt == "" && (strings.Contains(alt, "coming soon") || strings.Contains(alt, "new arrival")) {
			firstFromComingSoonAlt = src
		}
		if isLikelyVehicleImageURL(src) {
			best = src
			return false
		}
		return true
	})
	if best != "" {
		return best
	}
	if firstFromComingSoonAlt != "" {
		return firstFromComingSoonAlt
	}
	if bg := firstBackgroundImageURL(card); bg != "" {
		return bg
	}
	return firstAny
}

func firstBackgroundImageURL(card *goquery.Selection) string {
	if u := urlFromStyle(card.AttrOr("style", "")); u != "" {
		return u
	}
	attrs := []string{"data-bg", "data-background-image", "data-image", "data-src"}
	for _, k := range attrs {
		if v := strings.TrimSpace(card.AttrOr(k, "")); v != "" {
			return v
		}
	}
	var out string
	card.Find("[style*='background-image'], [data-bg], [data-background-image], [data-image], [data-src]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if u := urlFromStyle(s.AttrOr("style", "")); u != "" {
			out = u
			return false
		}
		for _, k := range attrs {
			if v := strings.TrimSpace(s.AttrOr(k, "")); v != "" {
				out = v
				return false
			}
		}
		return true
	})
	return out
}

func urlFromStyle(style string) string {
	style = strings.TrimSpace(style)
	if style == "" {
		return ""
	}
	if m := cssURLRe.FindStringSubmatch(style); len(m) > 2 {
		for _, g := range m[1:] {
			if strings.TrimSpace(g) != "" {
				return strings.TrimSpace(g)
			}
		}
	}
	return ""
}

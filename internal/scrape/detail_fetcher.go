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
	// carsforsale.com platform structure: label/value pairs in sibling divs
	// (.vdp-info-block__info-item-title / .vdp-info-block__info-item-description).
	// Parse these first — they are clean and authoritative, avoiding the heuristics
	// below that can mistake a label like "Stock #" for its value.
	vdp := parseVDPInfoBlocks(doc)
	if item.VIN == "" {
		if v := findVINInText(vdp["vin"]); v != "" {
			item.VIN = v
		}
	}
	if item.StockID == "" {
		if s := vdp["stock #"]; s != "" {
			item.StockID = s
		} else if s := vdp["stock"]; s != "" {
			item.StockID = s
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

// parseVDPInfoBlocks reads the carsforsale.com vehicle-detail spec pairs,
// returning a lowercased label -> value map (e.g. "engine" -> "ECOTEC 1.6L...").
func parseVDPInfoBlocks(doc *goquery.Document) map[string]string {
	out := map[string]string{}
	doc.Find(".vdp-info-block__info-item-title").Each(func(_ int, s *goquery.Selection) {
		label := strings.ToLower(clean(s.Text()))
		val := clean(s.Next().Text())
		if val == "" {
			val = clean(s.NextAll().Filter(".vdp-info-block__info-item-description").First().Text())
		}
		if label != "" && val != "" {
			out[label] = val
		}
	})
	return out
}

func fillCommonVehicleFields(item *model.InventoryItem, doc *goquery.Document, html string) {
	kv := map[string]string{}

	// Seed with the clean carsforsale.com spec pairs first.
	for k, v := range parseVDPInfoBlocks(doc) {
		kv[k] = v
	}

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
	doc.Find("li, div, p, span").Each(func(_ int, s *goquery.Selection) {
		text := clean(s.Text())
		if text == "" || len(text) > 160 {
			return
		}
		if k, v := splitSpecText(text); k != "" && v != "" {
			kv[k] = v
		}
	})
	doc.Find("li, div, p, span, strong, b").Each(func(_ int, s *goquery.Selection) {
		label := normalizeSpecLabel(s.Text())
		if label == "" {
			return
		}
		if v := clean(s.Next().Text()); v != "" && normalizeSpecLabel(v) == "" {
			kv[label] = v
			return
		}
		if v := clean(s.NextAll().First().Text()); v != "" && normalizeSpecLabel(v) == "" {
			kv[label] = v
		}
	})
	doc.Find("li, div, p, span").Each(func(_ int, s *goquery.Selection) {
		text := clean(s.Text())
		if text == "" || len(text) > 80 {
			return
		}
		applySpecTile(item, text)
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
		// Prefer exterior color; fall back to generic "color" but never interior.
		item.Color = pickValueByLabel(kv, "exterior color", "ext color")
		if item.Color == "" {
			if v := kv["color"]; v != "" {
				item.Color = v
			}
		}
	}
	if item.Mileage == "" {
		item.Mileage = pickValueByLabel(kv, "mileage", "miles", "odometer")
	}
	if item.Engine == "" {
		item.Engine = pickValueByLabel(kv, "engine", "engine type", "engine description")
	}
	if item.Cylinders == "" {
		item.Cylinders = pickValueByLabel(kv, "cylinders", "cylinder")
	}
	if item.Horsepower == "" {
		item.Horsepower = pickValueByLabel(kv, "horsepower", "hp")
	}
	if item.Torque == "" {
		item.Torque = pickValueByLabel(kv, "torque")
	}
	if item.Transmission == "" {
		item.Transmission = pickValueByLabel(kv, "transmission")
	}
	if item.TransmissionType == "" {
		item.TransmissionType = pickValueByLabel(kv, "transmission type")
	}
	if item.DriveType == "" || strings.Contains(strings.ToLower(item.DriveType), "drivetrain") {
		if v := pickValueByLabel(kv, "drivetrain", "drive train", "drive type", "drive"); v != "" {
			item.DriveType = v
		}
	}
	if item.FuelType == "" {
		item.FuelType = pickValueByLabel(kv, "fuel type", "fuel")
	}
	if item.FuelCapacity == "" {
		item.FuelCapacity = pickValueByLabel(kv, "fuel capacity", "fuel tank capacity")
	}
	if item.FuelEconomy == "" {
		item.FuelEconomy = pickValueByLabel(kv, "fuel economy", "combined mpg", "mpg")
	}
	if item.CityMPG == "" {
		item.CityMPG = pickValueByLabel(kv, "city mpg", "city")
	}
	if item.HighwayMPG == "" {
		item.HighwayMPG = pickValueByLabel(kv, "highway mpg", "hwy mpg", "hwy", "highway")
	}
	if item.BodyType == "" {
		item.BodyType = pickValueByLabel(kv, "body type", "body style", "body", "style")
	}
	if item.Style == "" {
		item.Style = pickValueByLabel(kv, "style", "trim")
	}
	if item.SeatInfo == "" {
		item.SeatInfo = pickValueByLabel(kv, "seat info", "seats", "seating")
	}
	if item.PassengerCapacity == "" {
		item.PassengerCapacity = pickValueByLabel(kv, "passengers", "passenger capacity", "seating capacity")
	}
	if item.TireInfo == "" {
		item.TireInfo = pickValueByLabel(kv, "tire", "tires")
	}
	if item.FrontTire == "" {
		item.FrontTire = pickValueByLabel(kv, "front tire")
	}
	if item.RearTire == "" {
		item.RearTire = pickValueByLabel(kv, "rear tire")
	}
	if item.WheelInfo == "" {
		item.WheelInfo = pickValueByLabel(kv, "wheel", "wheels")
	}
	if item.FrontWheel == "" {
		item.FrontWheel = pickValueByLabel(kv, "front wheel")
	}
	if item.RearWheel == "" {
		item.RearWheel = pickValueByLabel(kv, "rear wheel")
	}

	if item.Color == "" {
		re := regexp.MustCompile(`(?i)\b(?:exterior\s+color|color)\b[:\s]+([a-z0-9][a-z0-9\s\-\/]{1,40})`)
		if m := re.FindStringSubmatch(html); len(m) > 1 {
			item.Color = clean(m[1])
		}
	}
}

func splitSpecText(text string) (string, string) {
	text = clean(text)
	for _, sep := range []string{":", "#"} {
		parts := strings.SplitN(text, sep, 2)
		if len(parts) != 2 {
			continue
		}
		k := normalizeSpecLabel(parts[0])
		v := clean(parts[1])
		if k != "" && v != "" {
			return k, v
		}
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return "", ""
	}
	for i := 1; i < len(fields); i++ {
		k := normalizeSpecLabel(strings.Join(fields[:i], " "))
		if k != "" {
			return k, clean(strings.Join(fields[i:], " "))
		}
	}
	return "", ""
}

func normalizeSpecLabel(raw string) string {
	s := strings.ToLower(clean(raw))
	s = strings.Trim(s, " :-#")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	s = multiSpace.ReplaceAllString(s, " ")
	switch s {
	case "vin", "stock", "stock number", "stock no", "stock id", "stock #",
		"make", "model", "year", "color", "exterior color", "ext color",
		"mileage", "miles", "odometer",
		"engine", "engine type", "engine description", "cylinders", "cylinder",
		"horsepower", "hp", "torque", "transmission", "transmission type",
		"drivetrain", "drive train", "drive type", "drive",
		"fuel", "fuel type", "fuel capacity", "fuel tank capacity", "fuel economy",
		"combined mpg", "mpg", "city mpg", "city", "highway mpg", "hwy mpg", "hwy", "highway",
		"body", "body type", "body style", "style", "trim",
		"seat info", "seats", "seating", "passengers", "passenger capacity", "seating capacity",
		"tire", "tires", "front tire", "rear tire", "wheel", "wheels", "front wheel", "rear wheel":
		return s
	default:
		return ""
	}
}

func applySpecTile(item *model.InventoryItem, text string) {
	upper := strings.ToUpper(clean(text))
	lower := strings.ToLower(upper)
	if item.Mileage == "" {
		if m := regexp.MustCompile(`(?i)\b([0-9][0-9,]*)\s*(miles?|mi)\b`).FindStringSubmatch(text); len(m) > 2 {
			item.Mileage = m[1] + " " + strings.ToLower(m[2])
		}
	}
	if item.DriveType == "" {
		switch {
		case strings.Contains(upper, "AWD") || strings.Contains(lower, "all wheel drive"):
			item.DriveType = text
		case strings.Contains(upper, "FWD") || strings.Contains(lower, "front wheel drive"):
			item.DriveType = text
		case strings.Contains(upper, "RWD") || strings.Contains(lower, "rear wheel drive"):
			item.DriveType = text
		case strings.Contains(upper, "4WD") || strings.Contains(lower, "four wheel drive"):
			item.DriveType = text
		}
	}
	if item.Transmission == "" {
		if regexp.MustCompile(`(?i)\b([0-9]{1,2}\s*speed|automatic|manual|dual\s+clutch|cvt)\b`).MatchString(text) {
			item.Transmission = text
		}
	}
	if item.TransmissionType == "" {
		switch {
		case strings.Contains(lower, "automatic"):
			item.TransmissionType = "Automatic"
		case strings.Contains(lower, "manual"):
			item.TransmissionType = "Manual"
		}
	}
	if item.Engine == "" {
		if regexp.MustCompile(`(?i)\b(v[0-9]{1,2}|i[0-9]|[0-9](?:\.[0-9])?\s*l(?:iter)?|[0-9]+\s*cylinders?)\b`).MatchString(text) {
			item.Engine = text
		}
	}
	if item.Cylinders == "" {
		if m := regexp.MustCompile(`(?i)\b([0-9]{1,2}\s*cylinders?)\b`).FindStringSubmatch(text); len(m) > 1 {
			item.Cylinders = m[1]
		}
	}
	if item.FuelEconomy == "" {
		if m := regexp.MustCompile(`(?i)\b([0-9]{1,3}\s*mpg)\b`).FindStringSubmatch(text); len(m) > 1 {
			item.FuelEconomy = m[1]
		}
	}
	if item.CityMPG == "" {
		if m := regexp.MustCompile(`(?i)\b([0-9]{1,3})\s*city\s*mpg\b|\bcity\s*mpg\s*([0-9]{1,3})\b`).FindStringSubmatch(text); len(m) > 0 {
			item.CityMPG = firstRegexGroup(m) + " MPG"
		}
	}
	if item.HighwayMPG == "" {
		if m := regexp.MustCompile(`(?i)\b([0-9]{1,3})\s*(?:hwy|highway)\s*mpg\b|\b(?:hwy|highway)\s*mpg\s*([0-9]{1,3})\b`).FindStringSubmatch(text); len(m) > 0 {
			item.HighwayMPG = firstRegexGroup(m) + " MPG"
		}
	}
	if item.BodyType == "" && isLikelyBodyType(upper) {
		item.BodyType = text
	}
}

func firstRegexGroup(groups []string) string {
	for _, g := range groups[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

func isLikelyBodyType(upper string) bool {
	switch strings.TrimSpace(upper) {
	case "SEDAN", "COUPE", "CONVERTIBLE", "HATCHBACK", "WAGON", "SUV", "TRUCK", "VAN", "MINIVAN", "ROADSTER":
		return true
	default:
		return false
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

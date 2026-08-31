package scrape

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

var multiSpace = regexp.MustCompile(`\s+`)
var yearTitle = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
var imageSizeHintRe = regexp.MustCompile(`-(\d+)x(\d+)\.(?:jpg|jpeg|png|webp)(?:[?#]|$)`)
var stockTokenRe = regexp.MustCompile(`(?i)\bstock\s*#?[:\-]?\s*([a-z0-9\-]+)\b`)
var listingIDPathRe = regexp.MustCompile(`/([0-9]+)/*$`)

// bareStockLabelRe matches a stock cell that carries only its label and no
// value ("Stock #", "Stock", "#"). Platforms that have no stock numbers still
// render the label, and keeping it would give every card the same stock id —
// which dedupeKey prefers, collapsing a whole page into one item.
var bareStockLabelRe = regexp.MustCompile(`(?i)^\s*(?:stock\s*(?:number|no\.?|#)?)?\s*[#:\-]*\s*$`)

// Many dealer platforms put the VIN in the VDP slug
// (/viewdetails/new/3fttw8ba6tra65715/2026-ford-maverick). Matching a whole path
// segment, then validating, keeps ordinary slugs from being read as VINs.
var vinPathSegmentRe = regexp.MustCompile(`(?i)/([A-HJ-NPR-Z0-9]{17})(?:/|$)`)

// Some platforms render the mileage cell as "<label>Mileage:</label> 93,821",
// so the selector text carries the field label. Strip it so the value matches
// the bare-number form every other site yields.
// Detail pages render specs as "<span>Engine: </span><span>value</span>", so the
// extracted text arrives with the field label attached. The trailing colon is
// required, which keeps genuine values from being truncated.
var specLabelPrefixRe = regexp.MustCompile(`(?i)^\s*(?:engine(?:\s*type|\s*description)?|transmission(?:\s*type)?|trans|drive\s*type|drive\s*line|drive\s*train|driveline|drivetrain|fuel\s*type|body\s*(?:type|style)|exterior\s*color|interior\s*color|ext\s*color|color|cylinders?|horsepower|torque)\s*:\s*`)

// specTrailingLabelRe finds the start of a *following* field's label embedded in
// a value. goquery .Text() concatenates sibling nodes with no separator, so a
// colour cell can arrive as "WhiteMiles: 43K" — the next field ran into it.
var specTrailingLabelRe = regexp.MustCompile(`(?i)(miles|mileage|odometer|engine|transmission|drive\s*(?:type|train|line)|stock|vin|price|exterior\s*color|interior\s*color|color)\s*:`)

var mileageLabelRe = regexp.MustCompile(`(?i)^\s*(?:mileage|miles|odometer)\s*[:#\-]?\s*`)

const minImageDimension = 300

func NormalizeItem(baseURL string, item model.InventoryItem) model.InventoryItem {
	item.Title = clean(item.Title)
	item.StockID = normalizeStockID(item.StockID)
	item.Price = clean(item.Price)
	item.Mileage = normalizeMileage(item.Mileage)
	item.Color = stripSpecLabel(item.Color)
	item.Engine = stripSpecLabel(item.Engine)
	item.Cylinders = stripSpecLabel(item.Cylinders)
	item.Horsepower = stripSpecLabel(item.Horsepower)
	item.Torque = stripSpecLabel(item.Torque)
	item.Transmission = stripSpecLabel(item.Transmission)
	item.TransmissionType = stripSpecLabel(item.TransmissionType)
	item.DriveType = stripSpecLabel(item.DriveType)
	item.FuelType = stripSpecLabel(item.FuelType)
	item.FuelCapacity = clean(item.FuelCapacity)
	item.FuelEconomy = clean(item.FuelEconomy)
	item.MilesPerGallon = clean(item.MilesPerGallon)
	item.MilesPerLiter = clean(item.MilesPerLiter)
	item.CityMPG = clean(item.CityMPG)
	item.HighwayMPG = clean(item.HighwayMPG)
	item.CityMPL = clean(item.CityMPL)
	item.HighwayMPL = clean(item.HighwayMPL)
	item.BodyType = stripSpecLabel(item.BodyType)
	item.SeatInfo = clean(item.SeatInfo)
	item.PassengerCapacity = clean(item.PassengerCapacity)
	item.TireInfo = clean(item.TireInfo)
	item.FrontTire = clean(item.FrontTire)
	item.RearTire = clean(item.RearTire)
	item.WheelInfo = clean(item.WheelInfo)
	item.FrontWheel = clean(item.FrontWheel)
	item.RearWheel = clean(item.RearWheel)
	item.VIN = strings.ToUpper(clean(item.VIN))
	item.URL = absolutize(baseURL, item.URL)
	if item.VIN == "" {
		item.VIN = vinFromURLPath(item.URL)
	}
	if isMissingStockID(item.StockID) {
		// VIN is the stable cross-system identifier when a dealer does not publish
		// its own stock number. Fall back to a numeric listing id only when neither
		// a stock number nor a valid VIN is available.
		if vin := validVINCandidate(item.VIN); vin != "" {
			item.StockID = vin
		} else {
			item.StockID = listingIDFromURL(item.URL)
		}
	}
	item.PrimaryImage = absolutize(baseURL, item.PrimaryImage)
	if !isLikelyVehicleImageURL(item.PrimaryImage) {
		item.PrimaryImage = ""
	}
	filtered := make([]string, 0, len(item.Images))
	for i, img := range item.Images {
		item.Images[i] = absolutize(baseURL, img)
		if isLikelyVehicleImageURL(item.Images[i]) {
			filtered = append(filtered, item.Images[i])
		}
	}
	item.Images = uniqueStrings(filtered)
	if item.PrimaryImage == "" && len(item.Images) > 0 {
		item.PrimaryImage = item.Images[0]
	}
	if item.Year == "" {
		if m := yearTitle.FindString(item.Title); m != "" {
			item.Year = m
		}
	}
	item = fillFuelEconomyConversions(item)
	if item.Make == "" || item.Model == "" {
		parts := strings.Fields(item.Title)
		if len(parts) >= 3 {
			if item.Make == "" {
				item.Make = parts[1]
			}
			if item.Model == "" {
				item.Model = parts[2]
			}
		}
	}
	return item
}

func fillFuelEconomyConversions(item model.InventoryItem) model.InventoryItem {
	if item.FuelEconomy != "" && item.MilesPerGallon == "" {
		item.MilesPerGallon = normalizeMPGValue(item.FuelEconomy)
	}
	if item.MilesPerGallon != "" && item.MilesPerLiter == "" {
		item.MilesPerLiter = mpgToMPL(item.MilesPerGallon)
	}
	if item.CityMPG != "" && item.CityMPL == "" {
		item.CityMPL = mpgToMPL(item.CityMPG)
	}
	if item.HighwayMPG != "" && item.HighwayMPL == "" {
		item.HighwayMPL = mpgToMPL(item.HighwayMPG)
	}
	return item
}

func normalizeMPGValue(raw string) string {
	if n, ok := firstNumber(raw); ok {
		return fmt.Sprintf("%g MPG", n)
	}
	return raw
}

func mpgToMPL(raw string) string {
	if n, ok := firstNumber(raw); ok && n > 0 {
		return fmt.Sprintf("%.2f mi/L", n/3.785411784)
	}
	return ""
}

func firstNumber(raw string) (float64, bool) {
	re := regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)`)
	if m := re.FindStringSubmatch(raw); len(m) > 1 {
		n, err := strconv.ParseFloat(m[1], 64)
		return n, err == nil
	}
	return 0, false
}

func clean(s string) string {
	s = strings.TrimSpace(s)
	s = multiSpace.ReplaceAllString(s, " ")
	return s
}

func absolutize(base, raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err == nil && u.IsAbs() {
		return raw
	}
	b, err := url.Parse(base)
	if err != nil {
		return raw
	}
	r, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return b.ResolveReference(r).String()
}

func isLikelyVehicleImageURL(raw string) bool {
	if raw == "" {
		return false
	}
	u := strings.ToLower(raw)
	if strings.HasPrefix(u, "data:image/") {
		return false
	}
	if m := imageSizeHintRe.FindStringSubmatch(u); len(m) == 3 {
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		if (w > 0 && w < minImageDimension) || (h > 0 && h < minImageDimension) {
			return false
		}
	}
	noise := []string{
		"/logo", "logo_", "seal", "icon-", "icon_", "favicon", "sprite",
		"dealerrater", "calendly", "/css/", "/js/", ".svg",
	}
	for _, n := range noise {
		if strings.Contains(u, n) {
			return false
		}
	}
	positive := []string{
		"vehicle", "inventory", "wp-content/uploads", "stock", "vin=",
		"new-arrival", "new_arrival", "photos-coming-soon", "coming-soon", "picture-coming-soon", "no-photo",
		".jpg", ".jpeg", ".png", ".webp",
		// Image CDNs that serve from an opaque, extension-less key. Thumbor
		// ("/fit-in/640x480/filters:quality(72)/<key>") is the common one; without
		// these every photo on such a site is discarded as "not a vehicle image".
		"/fit-in/", "filters:", "/resize/", "cloudfront.net", "/photos/",
	}
	for _, p := range positive {
		if strings.Contains(u, p) {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// vinFromURLPath returns the VIN embedded in a listing URL's path, "" if none.
func vinFromURLPath(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	path := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		path = u.Path
	}
	for _, m := range vinPathSegmentRe.FindAllStringSubmatch(path, -1) {
		if vin := validVINCandidate(m[1]); vin != "" {
			return vin
		}
	}
	return ""
}

// stripSpecLabel removes a leading "Field:" prefix from a detail-page value.
func stripSpecLabel(raw string) string {
	v := clean(specLabelPrefixRe.ReplaceAllString(clean(raw), ""))
	// Cut anything from where the next field's label begins. Position 0 is left
	// alone: the prefix strip above already handled a leading label, and a value
	// that is nothing but a label should stay empty rather than be kept whole.
	if loc := specTrailingLabelRe.FindStringIndex(v); loc != nil && loc[0] > 0 {
		v = clean(v[:loc[0]])
	}
	return v
}

func normalizeMileage(raw string) string {
	return clean(mileageLabelRe.ReplaceAllString(clean(raw), ""))
}

func normalizeStockID(raw string) string {
	s := clean(raw)
	if s == "" {
		return ""
	}
	if m := stockTokenRe.FindStringSubmatch(s); len(m) > 1 {
		// "Stock Number:" captures "Number" — the rest of the label, not a value.
		if v := clean(m[1]); !isStockLabelWord(v) {
			return strings.ToUpper(v)
		}
	}
	// Cards often render the stock cell as just "# K14394A".
	s = clean(strings.TrimPrefix(s, "#"))
	if bareStockLabelRe.MatchString(s) {
		return ""
	}
	return strings.ToUpper(s)
}

// isStockLabelWord reports whether a captured token is really part of the
// field label rather than a stock number.
func isStockLabelWord(v string) bool {
	switch strings.ToLower(strings.Trim(v, " .:#-")) {
	case "", "number", "num", "no":
		return true
	}
	return false
}

func isMissingStockID(stockID string) bool {
	switch strings.ToUpper(clean(stockID)) {
	case "", "N/A", "NA", "NONE", "—", "-":
		return true
	default:
		return false
	}
}

func listingIDFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if match := listingIDPathRe.FindStringSubmatch(u.Path); len(match) > 1 {
		return match[1]
	}
	return ""
}

package scrape

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

var priceOnlyTitleRe = regexp.MustCompile(`^\$?\s*\d[\d,]*(?:\.\d{2})?$`)

func Dedupe(items []model.InventoryItem) []model.InventoryItem {
	seen := make(map[string]int, len(items))
	out := make([]model.InventoryItem, 0, len(items))
	for _, it := range items {
		k := dedupeKey(it)
		if k == "" {
			continue
		}
		if idx, ok := seen[k]; ok {
			// Two different VINs are two different vehicles, whatever the
			// stock/url/image key says. Without this, a single bogus stock value
			// shared across a page (an empty "Stock #" cell that captured the
			// model year, say) merges the whole listing into a handful of items.
			if vinsConflict(out[idx], it) {
				k = "vin:" + strings.ToUpper(strings.TrimSpace(it.VIN))
				if idx2, ok2 := seen[k]; ok2 {
					out[idx2] = mergeInventoryItem(out[idx2], it)
					continue
				}
				seen[k] = len(out)
				out = append(out, it)
				continue
			}
			out[idx] = mergeInventoryItem(out[idx], it)
			continue
		}
		seen[k] = len(out)
		out = append(out, it)
	}
	return out
}

// vinsConflict reports whether both items carry a VIN and the VINs differ.
func vinsConflict(a, b model.InventoryItem) bool {
	av := strings.ToUpper(strings.TrimSpace(a.VIN))
	bv := strings.ToUpper(strings.TrimSpace(b.VIN))
	return av != "" && bv != "" && av != bv
}

func mergeInventoryItem(base, cand model.InventoryItem) model.InventoryItem {
	if base.URL == "" {
		base.URL = cand.URL
	}
	if base.Title == "" || (looksLikePriceOnlyTitle(base.Title) && !looksLikePriceOnlyTitle(cand.Title)) {
		base.Title = cand.Title
	}
	if base.Year == "" {
		base.Year = cand.Year
	}
	if base.Make == "" {
		base.Make = cand.Make
	}
	// A title-derived model keeps only the first token ("Model" from "Model S"),
	// so let a candidate that extends it word-for-word win — structured sources
	// carry the full name where the title split could not.
	if base.Model == "" || extendsModelName(base.Model, cand.Model) {
		base.Model = cand.Model
	}
	if base.Color == "" {
		base.Color = cand.Color
	}
	if base.VIN == "" {
		base.VIN = cand.VIN
	}
	if base.Price == "" {
		base.Price = cand.Price
	}
	if base.Mileage == "" {
		base.Mileage = cand.Mileage
	}
	if base.Engine == "" {
		base.Engine = cand.Engine
	}
	if base.Cylinders == "" {
		base.Cylinders = cand.Cylinders
	}
	if base.Horsepower == "" {
		base.Horsepower = cand.Horsepower
	}
	if base.Torque == "" {
		base.Torque = cand.Torque
	}
	if base.Transmission == "" {
		base.Transmission = cand.Transmission
	}
	if base.TransmissionType == "" {
		base.TransmissionType = cand.TransmissionType
	}
	if base.DriveType == "" {
		base.DriveType = cand.DriveType
	}
	if base.FuelType == "" {
		base.FuelType = cand.FuelType
	}
	if base.FuelCapacity == "" {
		base.FuelCapacity = cand.FuelCapacity
	}
	if base.FuelEconomy == "" {
		base.FuelEconomy = cand.FuelEconomy
	}
	if base.MilesPerGallon == "" {
		base.MilesPerGallon = cand.MilesPerGallon
	}
	if base.MilesPerLiter == "" {
		base.MilesPerLiter = cand.MilesPerLiter
	}
	if base.CityMPG == "" {
		base.CityMPG = cand.CityMPG
	}
	if base.HighwayMPG == "" {
		base.HighwayMPG = cand.HighwayMPG
	}
	if base.CityMPL == "" {
		base.CityMPL = cand.CityMPL
	}
	if base.HighwayMPL == "" {
		base.HighwayMPL = cand.HighwayMPL
	}
	if base.BodyType == "" {
		base.BodyType = cand.BodyType
	}
	if base.SeatInfo == "" {
		base.SeatInfo = cand.SeatInfo
	}
	if base.PassengerCapacity == "" {
		base.PassengerCapacity = cand.PassengerCapacity
	}
	if base.TireInfo == "" {
		base.TireInfo = cand.TireInfo
	}
	if base.FrontTire == "" {
		base.FrontTire = cand.FrontTire
	}
	if base.RearTire == "" {
		base.RearTire = cand.RearTire
	}
	if base.WheelInfo == "" {
		base.WheelInfo = cand.WheelInfo
	}
	if base.FrontWheel == "" {
		base.FrontWheel = cand.FrontWheel
	}
	if base.RearWheel == "" {
		base.RearWheel = cand.RearWheel
	}
	if base.PrimaryImage == "" {
		base.PrimaryImage = cand.PrimaryImage
	}
	base.Images = uniqueStrings(append(base.Images, cand.Images...))
	if base.PrimaryImage == "" && len(base.Images) > 0 {
		base.PrimaryImage = base.Images[0]
	}
	return base
}

func looksLikePriceOnlyTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	return priceOnlyTitleRe.MatchString(title)
}

// extendsModelName reports whether cand is base plus at least one further word,
// e.g. "Model S" over "Model". Case-insensitive; a bare prefix like "Mode" does
// not qualify, so unrelated models never overwrite each other.
func extendsModelName(base, cand string) bool {
	b := strings.TrimSpace(base)
	c := strings.TrimSpace(cand)
	if b == "" || c == "" || len(c) <= len(b) {
		return false
	}
	if !strings.EqualFold(c[:len(b)], b) {
		return false
	}
	return c[len(b)] == ' '
}

func dedupeKey(it model.InventoryItem) string {
	stock := strings.ToUpper(strings.TrimSpace(it.StockID))
	if stock == "" {
		stock = strings.ToUpper(strings.TrimSpace(it.Stock))
	}
	if stock != "" {
		return "stock:" + stock
	}
	vin := strings.ToUpper(strings.TrimSpace(it.VIN))
	if vin != "" {
		return "vin:" + vin
	}
	u := canonicalURLKey(it.URL)
	if u != "" {
		return "url:" + u
	}
	// canonicalURLKey drops the query, so an image served by an endpoint rather
	// than a file ("/images/GetEvoxImage?vin=…") canonicalizes identically for
	// every vehicle and would collapse a whole page into one item. Only trust an
	// image key when the path itself names a file.
	img := canonicalURLKey(it.PrimaryImage)
	if img == "" && len(it.Images) > 0 {
		img = canonicalURLKey(it.Images[0])
	}
	if img != "" && pathNamesAFile(img) {
		return "img:" + img
	}
	// Last resort: a content key so an item with no stock/VIN/URL/image (e.g. a
	// listing whose detail fetch dropped its URL) is never silently discarded.
	// Distinct enough to avoid merging different vehicles; stable across the two
	// Dedupe passes since these fields don't change between them.
	if c := contentKey(it); c != "" {
		return "content:" + c
	}
	return ""
}

func contentKey(it model.InventoryItem) string {
	parts := []string{it.Title, it.Year, it.Make, it.Model, it.Mileage, it.Price}
	joined := strings.ToLower(strings.TrimSpace(strings.Join(parts, "|")))
	// "|||||" (all empty) carries no identity — treat as empty.
	if strings.Trim(joined, "|") == "" {
		return ""
	}
	return joined
}

// pathNamesAFile reports whether the URL's last path segment looks like a file
// (has an extension) rather than an endpoint whose identity lives in the query.
func pathNamesAFile(rawURL string) bool {
	path := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		path = u.Path
	}
	last := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		last = path[i+1:]
	}
	return strings.Contains(last, ".")
}

func canonicalURLKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSuffix(strings.ToLower(raw), "/")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if parsed.Path != "/" {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	}
	return strings.ToLower(parsed.String())
}

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
			out[idx] = mergeInventoryItem(out[idx], it)
			continue
		}
		seen[k] = len(out)
		out = append(out, it)
	}
	return out
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
	if base.Model == "" {
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
	img := canonicalURLKey(it.PrimaryImage)
	if img == "" && len(it.Images) > 0 {
		img = canonicalURLKey(it.Images[0])
	}
	if img != "" {
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

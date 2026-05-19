package scrape

import (
	"net/url"
	"strings"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

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
	if base.Title == "" {
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
	if base.Transmission == "" {
		base.Transmission = cand.Transmission
	}
	if base.DriveType == "" {
		base.DriveType = cand.DriveType
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
	return ""
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

package scrape

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/example/inventory-scraper/internal/model"
)

var multiSpace = regexp.MustCompile(`\s+`)
var yearTitle = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

func NormalizeItem(baseURL string, item model.InventoryItem) model.InventoryItem {
	item.Title = clean(item.Title)
	item.StockID = clean(item.StockID)
	item.Price = clean(item.Price)
	item.Mileage = clean(item.Mileage)
	item.VIN = strings.ToUpper(clean(item.VIN))
	item.URL = absolutize(baseURL, item.URL)
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
	noise := []string{
		"/logo", "logo_", "seal", "icon-", "icon_", "favicon", "sprite", "placeholder",
		"dealerrater", "calendly", "/css/", "/js/", ".svg",
	}
	for _, n := range noise {
		if strings.Contains(u, n) {
			return false
		}
	}
	positive := []string{
		"vehicle", "inventory", "wp-content/uploads", "stock", "vin=",
		".jpg", ".jpeg", ".png", ".webp",
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

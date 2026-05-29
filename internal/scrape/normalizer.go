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

const minImageDimension = 300

func NormalizeItem(baseURL string, item model.InventoryItem) model.InventoryItem {
	item.Title = clean(item.Title)
	item.StockID = normalizeStockID(item.StockID)
	item.Price = clean(item.Price)
	item.Mileage = clean(item.Mileage)
	item.Engine = clean(item.Engine)
	item.Cylinders = clean(item.Cylinders)
	item.Horsepower = clean(item.Horsepower)
	item.Torque = clean(item.Torque)
	item.Transmission = clean(item.Transmission)
	item.TransmissionType = clean(item.TransmissionType)
	item.DriveType = clean(item.DriveType)
	item.FuelType = clean(item.FuelType)
	item.FuelCapacity = clean(item.FuelCapacity)
	item.FuelEconomy = clean(item.FuelEconomy)
	item.MilesPerGallon = clean(item.MilesPerGallon)
	item.MilesPerLiter = clean(item.MilesPerLiter)
	item.CityMPG = clean(item.CityMPG)
	item.HighwayMPG = clean(item.HighwayMPG)
	item.CityMPL = clean(item.CityMPL)
	item.HighwayMPL = clean(item.HighwayMPL)
	item.BodyType = clean(item.BodyType)
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

func normalizeStockID(raw string) string {
	s := clean(raw)
	if s == "" {
		return ""
	}
	if m := stockTokenRe.FindStringSubmatch(s); len(m) > 1 {
		return strings.ToUpper(clean(m[1]))
	}
	return strings.ToUpper(s)
}

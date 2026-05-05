package scrape

import (
	"net/url"
	"strings"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func Dedupe(items []model.InventoryItem) []model.InventoryItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]model.InventoryItem, 0, len(items))
	for _, it := range items {
		k := dedupeKey(it)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok { continue }
		seen[k] = struct{}{}
		out = append(out, it)
	}
	return out
}

func dedupeKey(it model.InventoryItem) string {
	stock := strings.ToUpper(strings.TrimSpace(it.StockID))
	if stock != "" {
		return "stock:" + stock
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

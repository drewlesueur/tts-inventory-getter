package scrape

import "github.com/example/inventory-scraper/internal/model"

func Dedupe(items []model.InventoryItem) []model.InventoryItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]model.InventoryItem, 0, len(items))
	for _, it := range items {
		k := it.URL
		if k == "" {
			if len(it.Images) > 0 {
				k = it.StockID + "|" + it.Images[0]
			} else {
				k = it.StockID
			}
		}
		if _, ok := seen[k]; ok { continue }
		seen[k] = struct{}{}
		out = append(out, it)
	}
	return out
}

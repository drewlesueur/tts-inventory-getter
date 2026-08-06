package model

import "strings"

func InventoryCountByUniqueVIN(items []InventoryItem) int {
	return countUniqueInventoryKeys(items, func(item InventoryItem) string {
		return "vin:" + strings.ToUpper(strings.TrimSpace(item.VIN))
	})
}

func InventoryCountByUniqueStockID(items []InventoryItem) int {
	return countUniqueInventoryKeys(items, func(item InventoryItem) string {
		stock := strings.ToUpper(strings.TrimSpace(item.StockID))
		if stock == "" {
			stock = strings.ToUpper(strings.TrimSpace(item.Stock))
		}
		return "stock:" + stock
	})
}

func InventoryIdentityCount(items []InventoryItem) int {
	seen := uniqueInventoryIdentities(items)
	return len(seen)
}

func InventoryCount(items []InventoryItem) int {
	if count := InventoryIdentityCount(items); count > 0 {
		return count
	}
	return len(items)
}

func ScrapedInventoryCount(items []InventoryItem) int {
	return InventoryCount(items)
}

func uniqueInventoryIdentities(items []InventoryItem) map[string]struct{} {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		vin := strings.ToUpper(strings.TrimSpace(item.VIN))
		if vin != "" {
			seen["vin:"+vin] = struct{}{}
			continue
		}
		stock := strings.ToUpper(strings.TrimSpace(item.StockID))
		if stock == "" {
			stock = strings.ToUpper(strings.TrimSpace(item.Stock))
		}
		if stock != "" {
			seen["stock:"+stock] = struct{}{}
		}
	}
	return seen
}

func countUniqueInventoryKeys(items []InventoryItem, keyFn func(InventoryItem) string) int {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := strings.TrimSpace(keyFn(item))
		if strings.HasSuffix(key, ":") {
			continue
		}
		if key != "" {
			seen[key] = struct{}{}
		}
	}
	return len(seen)
}

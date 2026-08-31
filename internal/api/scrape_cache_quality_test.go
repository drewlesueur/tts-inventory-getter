package api

import (
	"testing"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestFreshScrapeCacheForSiteRejectsVINFallbackWhenDealerStockConfigured(t *testing.T) {
	now := time.Now().UTC()
	result := model.ScrapeResult{
		Status:     model.RunStatusSuccess,
		FinishedAt: now.Add(-time.Hour),
		Items: []model.InventoryItem{{
			StockID: "7SAYGDEE7RF163837",
			VIN:     "7SAYGDEE7RF163837",
		}},
	}
	site := config.SiteConfig{DetailPage: config.DetailPageConfig{StockSelector: "p.stock"}}
	if isFreshScrapeCacheForSite(result, site, now) {
		t.Fatal("VIN-fallback cache must be invalid when the template requires dealer stock")
	}
	result.Items[0].StockID = "TES6"
	if !isFreshScrapeCacheForSite(result, site, now) {
		t.Fatal("real dealer stock should keep a fresh cache usable")
	}
}

func TestFreshScrapeCacheForSiteAllowsVINFallbackWhenNoDealerStockConfigured(t *testing.T) {
	now := time.Now().UTC()
	result := model.ScrapeResult{
		Status:     model.RunStatusSuccess,
		FinishedAt: now.Add(-time.Hour),
		Items:      []model.InventoryItem{{StockID: "7SAYGDEE7RF163837", VIN: "7SAYGDEE7RF163837"}},
	}
	if !isFreshScrapeCacheForSite(result, config.SiteConfig{}, now) {
		t.Fatal("VIN fallback is valid for sites that publish no dealer stock")
	}
}

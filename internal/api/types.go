package api

import (
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type ScrapeOnceRequest struct {
	DealershipID   string             `json:"dealershipId" binding:"required"`
	SourceURL      string             `json:"sourceUrl" binding:"required,url"`
	SiteConfig     *config.SiteConfig `json:"siteConfig,omitempty"`
	Options        *ScrapeOptions     `json:"options,omitempty"`
	IdempotencyKey string             `json:"idempotencyKey,omitempty"`
}

type ScrapeOptions struct {
	RunTimeoutSec      int    `json:"runTimeoutSec,omitempty"`
	UseBrowser         *bool  `json:"useBrowser,omitempty"`
	UseCodexDiscovery  *bool  `json:"useCodexDiscovery,omitempty"`
	BrowserStrategy    string `json:"browserStrategy,omitempty"`
	EnableAIEnrichment *bool  `json:"enableAIEnrichment,omitempty"`
	MaxPages           int    `json:"maxPages,omitempty"`
	MaxScrollAttempts  int    `json:"maxScrollAttempts,omitempty"`
	MaxLoadMoreClicks  int    `json:"maxLoadMoreClicks,omitempty"`
	MaxItems           int    `json:"maxItems,omitempty"`
	// ForceLive makes /v1/scrape/run bypass the cache-only/bot-protected
	// cache-first path and always scrape live. The hybrid local worker sets it:
	// its whole job is producing the fresh result the cloud's cache serves, and
	// without it the local server 404s on the hardcoded cache-only domains.
	ForceLive *bool `json:"forceLive,omitempty"`
}

type DiscoverFlowRequest struct {
	DealershipID string `json:"dealershipId,omitempty"`
	SourceURL    string `json:"sourceUrl" binding:"required,url"`
}

type TapToSignUpsertRequest struct {
	AccountID    string                `json:"accountId"`
	DealershipID string                `json:"dealershipId"`
	Items        []model.InventoryItem `json:"items"`
}

// ScrapeRunRequest is the request body for POST /v1/scrape/run.
// dealershipId is optional; defaults to the URL hostname.
type ScrapeRunRequest struct {
	URL          string            `json:"url"`
	DealershipID string            `json:"dealershipId,omitempty"`
	TimeoutSec   int               `json:"timeoutSec,omitempty"`
	Options      *ScrapeOptions    `json:"options,omitempty"`
	Cookies      map[string]string `json:"cookies,omitempty"`
}

// ScrapeSyncRequest is the body for POST /v1/scrape/sync — a local scraper pushes
// already-scraped inventory for a URL. The cloud caches it and upserts to the
// dealer/account that owns the URL. dealershipId/accountId are optional; if omitted
// they are resolved from the inventory API by matching the URL.
type ScrapeSyncRequest struct {
	URL          string                `json:"url"`
	DealershipID string                `json:"dealershipId,omitempty"`
	AccountID    string                `json:"accountId,omitempty"`
	Items        []model.InventoryItem `json:"items"`
	SkipUpsert   bool                  `json:"skipUpsert,omitempty"`
}

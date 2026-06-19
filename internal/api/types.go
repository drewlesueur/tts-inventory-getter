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

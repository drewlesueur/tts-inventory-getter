package api

import "github.com/example/inventory-scraper/internal/config"

type ScrapeOnceRequest struct {
	DealershipID   string             `json:"dealershipId" binding:"required"`
	SourceURL      string             `json:"sourceUrl" binding:"required,url"`
	SiteConfig     *config.SiteConfig `json:"siteConfig,omitempty"`
	Options        *ScrapeOptions     `json:"options,omitempty"`
	IdempotencyKey string             `json:"idempotencyKey,omitempty"`
}

type ScrapeOptions struct {
	RunTimeoutSec     int   `json:"runTimeoutSec,omitempty"`
	UseBrowser        *bool `json:"useBrowser,omitempty"`
	UseCodexDiscovery *bool `json:"useCodexDiscovery,omitempty"`
}

type DiscoverFlowRequest struct {
	DealershipID string `json:"dealershipId,omitempty"`
	SourceURL    string `json:"sourceUrl" binding:"required,url"`
}

package scrape

import (
	"context"

	"github.com/example/inventory-scraper/internal/config"
	"github.com/example/inventory-scraper/internal/model"
)

type Options struct {
	RunTimeoutSec int  `json:"runTimeoutSec,omitempty"`
	UseBrowser    *bool `json:"useBrowser,omitempty"`
}

type Browser interface {
	Render(ctx context.Context, url string, site config.SiteConfig) (string, error)
}

type Fetcher interface {
	Fetch(ctx context.Context, url string) (string, error)
}

type DetailFetcher interface {
	FetchDetails(ctx context.Context, item model.InventoryItem, site config.SiteConfig) (model.InventoryItem, error)
}

type Extractor interface {
	Extract(ctx context.Context, html, pageURL string, site config.SiteConfig) ([]model.InventoryItem, []model.StructuredError)
}

type RunResult struct {
	Items  []model.InventoryItem
	Errors []model.StructuredError
}

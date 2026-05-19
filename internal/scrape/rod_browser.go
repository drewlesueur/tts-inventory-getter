package scrape

import (
	"context"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// RodBrowser currently delegates to ChromeBrowser implementation.
// This keeps fallback plumbing stable while preserving Browser interface usage.
type RodBrowser struct {
	chrome *ChromeBrowser
}

func NewRodBrowser(headless bool) (*RodBrowser, context.CancelFunc) {
	b, cancel := NewChromeBrowser(headless)
	return &RodBrowser{chrome: b}, cancel
}

func (r RodBrowser) Render(ctx context.Context, urlStr string, site config.SiteConfig) (string, error) {
	return r.chrome.Render(ctx, urlStr, site)
}

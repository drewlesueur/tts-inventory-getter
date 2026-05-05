package sites

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/discovery"
)

type Renderer interface {
	Render(ctx context.Context, url string, site config.SiteConfig) (string, error)
}

type Fetcher interface {
	Fetch(ctx context.Context, url string) (string, error)
}

type Resolver struct {
	Loader   config.Loader
	Discover *discovery.Client
	Browser  Renderer
	Fetcher  Fetcher
	Logger   *zap.Logger
}

func (r Resolver) Resolve(ctx context.Context, dealershipID, sourceURL string) (config.SiteConfig, error) {
	if site, err := r.Loader.LoadByName(dealershipID); err == nil {
		return site, nil
	}
	if r.Discover == nil {
		return config.SiteConfig{}, fmt.Errorf("site config not cached and Codex discovery disabled: %s", dealershipID)
	}
	var html string
	if r.Browser != nil {
		if h, err := safeRender(ctx, r.Browser, sourceURL); err == nil {
			html = h
		}
	}
	if html == "" {
		if r.Fetcher == nil {
			return config.SiteConfig{}, fmt.Errorf("no fetcher available for discovery")
		}
		h, err := r.Fetcher.Fetch(ctx, sourceURL)
		if err != nil {
			return config.SiteConfig{}, fmt.Errorf("discovery fetch failed: %w", err)
		}
		html = h
	}
	proposed, err := r.Discover.Discover(ctx, sourceURL, html)
	if err != nil {
		return config.SiteConfig{}, fmt.Errorf("discovery failed: %w", err)
	}
	if dealershipID != "" {
		proposed.Name = dealershipID
	}
	r.Loader.Cache(dealershipID, proposed)
	if r.Logger != nil {
		r.Logger.Info("site config discovered and cached", zap.String("dealershipId", dealershipID))
	}
	return proposed, nil
}

func safeRender(ctx context.Context, browser Renderer, sourceURL string) (html string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("browser render panic: %v", rec)
		}
	}()
	return browser.Render(ctx, sourceURL, config.SiteConfig{})
}

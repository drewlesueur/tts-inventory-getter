package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

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

const siteConfigCacheTTL = 7 * 24 * time.Hour

func (r Resolver) Resolve(ctx context.Context, dealershipID, sourceURL string) (config.SiteConfig, error) {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return config.SiteConfig{}, fmt.Errorf("sourceUrl is required")
	}
	urlKey := cacheKeyForSourceURL(sourceURL)
	if site, err := r.Loader.LoadByName(urlKey); err == nil {
		if r.Loader.IsCacheFresh(urlKey, siteConfigCacheTTL) {
			return site, nil
		}
		if r.Logger != nil {
			r.Logger.Info("site config cache expired, rediscovering",
				zap.String("urlKey", urlKey),
				zap.String("sourceURL", sourceURL),
				zap.Duration("ttl", siteConfigCacheTTL),
			)
		}
	}
	if r.Discover == nil {
		return config.SiteConfig{}, fmt.Errorf("site config not cached for url=%s and Codex discovery disabled", sourceURL)
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
	if proposed.Name == "" {
		proposed.Name = urlKey
	}
	r.Loader.Cache(urlKey, proposed)
	if err := r.Loader.SaveByName(urlKey, proposed); err != nil && r.Logger != nil {
		r.Logger.Warn("failed to persist url-keyed site config", zap.String("urlKey", urlKey), zap.Error(err))
	}
	if r.Logger != nil {
		proposedJSON, _ := json.MarshalIndent(proposed, "", "  ")
		r.Logger.Info("site config discovered and cached",
			zap.String("dealershipId", dealershipID),
			zap.String("urlKey", urlKey),
			zap.String("sourceURL", sourceURL),
			zap.ByteString("proposedConfig", proposedJSON),
		)
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

func cacheKeyForSourceURL(sourceURL string) string {
	u, err := url.Parse(sourceURL)
	if err != nil || u.Host == "" {
		return "url::" + strings.ToLower(strings.TrimSpace(sourceURL))
	}
	host := strings.ToLower(u.Hostname())
	path := strings.Trim(strings.ToLower(u.EscapedPath()), "/")
	if path == "" {
		path = "_root"
	}
	return "url::" + host + "/" + path
}

func CacheKeyForSourceURL(sourceURL string) string {
	return cacheKeyForSourceURL(sourceURL)
}

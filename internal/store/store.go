package store

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type ResultStore interface {
	UpsertResult(ctx context.Context, result model.ScrapeResult) error
	GetResult(ctx context.Context, resultID string) (model.ScrapeResult, error)
	FindByIdempotency(ctx context.Context, key string) (model.ScrapeResult, error)
	ClearIdempotency(ctx context.Context) error
	ClearResults(ctx context.Context) error

	// Cached inventory pushed from an external (e.g. local) scraper for a source URL.
	UpsertCachedInventory(ctx context.Context, c CachedInventory) error
	GetCachedInventory(ctx context.Context, sourceURL string) (CachedInventory, error)
}

// CachedInventory is inventory synced from an external scraper, keyed by source URL.
type CachedInventory struct {
	SourceURL    string                `json:"sourceUrl"`
	DealershipID string                `json:"dealershipId"`
	AccountID    string                `json:"accountId"`
	Items        []model.InventoryItem `json:"items"`
	UpdatedAt    time.Time             `json:"updatedAt"`
}

var ErrNotFound = errors.New("not found")

// NormalizeURLKey produces a stable cache key from a source URL.
func NormalizeURLKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path != "/" {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}
	u.Fragment = ""
	return u.String()
}

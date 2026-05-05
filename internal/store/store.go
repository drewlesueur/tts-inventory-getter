package store

import (
	"context"
	"errors"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type ResultStore interface {
	UpsertResult(ctx context.Context, result model.ScrapeResult) error
	GetResult(ctx context.Context, resultID string) (model.ScrapeResult, error)
	FindByIdempotency(ctx context.Context, key string) (model.ScrapeResult, error)
	ClearIdempotency(ctx context.Context) error
	ClearResults(ctx context.Context) error
}

var ErrNotFound = errors.New("not found")

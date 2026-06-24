package store

import (
	"context"
	"sync"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type MemoryResultStore struct {
	mu     sync.RWMutex
	byID   map[string]model.ScrapeResult
	byIdem map[string]string
	cache  map[string]CachedInventory
}

func NewMemoryResultStore() *MemoryResultStore {
	return &MemoryResultStore{byID: map[string]model.ScrapeResult{}, byIdem: map[string]string{}, cache: map[string]CachedInventory{}}
}

func (m *MemoryResultStore) UpsertCachedInventory(_ context.Context, c CachedInventory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[NormalizeURLKey(c.SourceURL)] = c
	return nil
}

func (m *MemoryResultStore) GetCachedInventory(_ context.Context, sourceURL string) (CachedInventory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.cache[NormalizeURLKey(sourceURL)]
	if !ok {
		return CachedInventory{}, ErrNotFound
	}
	return c, nil
}

func (m *MemoryResultStore) UpsertResult(_ context.Context, result model.ScrapeResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID[result.ResultID] = result
	if result.IdempotencyKey != "" {
		m.byIdem[result.IdempotencyKey] = result.ResultID
	}
	return nil
}

func (m *MemoryResultStore) GetResult(_ context.Context, resultID string) (model.ScrapeResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.byID[resultID]
	if !ok {
		return model.ScrapeResult{}, ErrNotFound
	}
	return r, nil
}

func (m *MemoryResultStore) FindByIdempotency(_ context.Context, key string) (model.ScrapeResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rid, ok := m.byIdem[key]
	if !ok {
		return model.ScrapeResult{}, ErrNotFound
	}
	return m.byID[rid], nil
}

func (m *MemoryResultStore) ClearIdempotency(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byIdem = map[string]string{}
	for id, r := range m.byID {
		if r.IdempotencyKey != "" {
			r.IdempotencyKey = ""
			m.byID[id] = r
		}
	}
	return nil
}

func (m *MemoryResultStore) ClearResults(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID = map[string]model.ScrapeResult{}
	m.byIdem = map[string]string{}
	return nil
}

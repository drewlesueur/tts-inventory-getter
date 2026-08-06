package store

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type MemoryResultStore struct {
	mu        sync.RWMutex
	byID      map[string]model.ScrapeResult
	byIdem    map[string]string
	cache     map[string]CachedInventory
	protected map[string]ProtectedURL
}

func NewMemoryResultStore() *MemoryResultStore {
	return &MemoryResultStore{byID: map[string]model.ScrapeResult{}, byIdem: map[string]string{}, cache: map[string]CachedInventory{}, protected: map[string]ProtectedURL{}}
}

func (m *MemoryResultStore) FlagProtectedURL(_ context.Context, p ProtectedURL) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.FlaggedAt.IsZero() {
		p.FlaggedAt = time.Now().UTC()
	}
	m.protected[NormalizeURLKey(p.SourceURL)] = p
	return nil
}

func (m *MemoryResultStore) UnflagProtectedURL(_ context.Context, sourceURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.protected, NormalizeURLKey(sourceURL))
	return nil
}

func (m *MemoryResultStore) IsProtectedURL(_ context.Context, sourceURL string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.protected[NormalizeURLKey(sourceURL)]
	return ok, nil
}

func (m *MemoryResultStore) ListProtectedURLs(_ context.Context) ([]ProtectedURL, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ProtectedURL, 0, len(m.protected))
	for _, p := range m.protected {
		out = append(out, p)
	}
	return out, nil
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

func (m *MemoryResultStore) ClearCachedInventory(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache = map[string]CachedInventory{}
	return nil
}

func (m *MemoryResultStore) GetCachedInventoryByHost(_ context.Context, host string) (CachedInventory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if host == "" {
		return CachedInventory{}, ErrNotFound
	}
	var best CachedInventory
	found := false
	for _, c := range m.cache {
		if HostOf(c.SourceURL) != host {
			continue
		}
		if !found || c.UpdatedAt.After(best.UpdatedAt) {
			best = c
			found = true
		}
	}
	if !found {
		return CachedInventory{}, ErrNotFound
	}
	return best, nil
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

func (m *MemoryResultStore) DeleteIdempotency(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if resultID, ok := m.byIdem[key]; ok {
		delete(m.byIdem, key)
		result := m.byID[resultID]
		result.IdempotencyKey = ""
		m.byID[resultID] = result
	}
	return nil
}

func (m *MemoryResultStore) ClearIdempotency(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, id := range m.byIdem {
		if strings.HasPrefix(key, "scrape-once-cache|") {
			continue
		}
		delete(m.byIdem, key)
		r := m.byID[id]
		if r.IdempotencyKey == key {
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

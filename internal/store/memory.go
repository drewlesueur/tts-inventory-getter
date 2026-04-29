package store

import (
	"context"
	"sync"

	"github.com/example/inventory-scraper/internal/model"
)

type MemoryRunStore struct {
	mu     sync.RWMutex
	byRun  map[string]model.RunSummary
	byIdem map[string]string
}

func NewMemoryRunStore() *MemoryRunStore {
	return &MemoryRunStore{byRun: map[string]model.RunSummary{}, byIdem: map[string]string{}}
}

func (m *MemoryRunStore) UpsertRun(_ context.Context, run model.RunSummary) error {
	m.mu.Lock(); defer m.mu.Unlock()
	m.byRun[run.RunID] = run
	if run.IdempotencyKey != "" { m.byIdem[run.IdempotencyKey] = run.RunID }
	return nil
}

func (m *MemoryRunStore) GetRun(_ context.Context, runID string) (model.RunSummary, error) {
	m.mu.RLock(); defer m.mu.RUnlock()
	r, ok := m.byRun[runID]
	if !ok { return model.RunSummary{}, ErrNotFound }
	return r, nil
}

func (m *MemoryRunStore) FindByIdempotency(_ context.Context, key string) (model.RunSummary, error) {
	m.mu.RLock(); defer m.mu.RUnlock()
	rid, ok := m.byIdem[key]
	if !ok { return model.RunSummary{}, ErrNotFound }
	return m.byRun[rid], nil
}

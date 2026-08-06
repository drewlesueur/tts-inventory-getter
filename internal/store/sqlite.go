package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	_ "modernc.org/sqlite"
)

type SQLiteResultStore struct{ db *sql.DB }

func NewSQLiteResultStore(dsn string) (*SQLiteResultStore, error) {
	dir := filepath.Dir(dsn)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &SQLiteResultStore{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteResultStore) Close() error { return s.db.Close() }

func (s *SQLiteResultStore) init() error {
	q := `
CREATE TABLE IF NOT EXISTS scrape_results (
  result_id TEXT PRIMARY KEY,
  dealership_id TEXT NOT NULL,
  source_url TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  total_items INTEGER NOT NULL DEFAULT 0,
  success_items INTEGER NOT NULL DEFAULT 0,
  failed_items INTEGER NOT NULL DEFAULT 0,
  failure_reason TEXT,
  error_count INTEGER NOT NULL DEFAULT 0,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  is_retrying INTEGER NOT NULL DEFAULT 0,
  next_retry_at TEXT,
  progress_stage TEXT,
  idempotency_key TEXT,
  items_json TEXT NOT NULL DEFAULT '[]',
  errors_json TEXT NOT NULL DEFAULT '[]'
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scrape_results_idempotency ON scrape_results(idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != '';
CREATE TABLE IF NOT EXISTS cached_inventory (
  source_url TEXT PRIMARY KEY,
  dealership_id TEXT,
  account_id TEXT,
  items_json TEXT NOT NULL DEFAULT '[]',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS protected_urls (
  source_url TEXT PRIMARY KEY,
  reason TEXT,
  flagged_at TEXT NOT NULL
);
`
	if _, err := s.db.Exec(q); err != nil {
		return err
	}
	// Backward-compatible migrations for existing DBs.
	migrations := []string{
		`ALTER TABLE scrape_results ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE scrape_results ADD COLUMN last_error TEXT`,
		`ALTER TABLE scrape_results ADD COLUMN is_retrying INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE scrape_results ADD COLUMN next_retry_at TEXT`,
		`ALTER TABLE scrape_results ADD COLUMN progress_stage TEXT`,
	}
	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	return nil
}

func (s *SQLiteResultStore) UpsertResult(ctx context.Context, result model.ScrapeResult) error {
	items, err := json.Marshal(result.Items)
	if err != nil {
		return err
	}
	errs, err := json.Marshal(result.Errors)
	if err != nil {
		return err
	}
	const q = `
INSERT INTO scrape_results (
  result_id, dealership_id, source_url, status, started_at, finished_at,
  total_items, success_items, failed_items, failure_reason, error_count, attempt_count, last_error, is_retrying, next_retry_at,
  progress_stage, idempotency_key, items_json, errors_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(result_id) DO UPDATE SET
  dealership_id=excluded.dealership_id,
  source_url=excluded.source_url,
  status=excluded.status,
  started_at=excluded.started_at,
  finished_at=excluded.finished_at,
  total_items=excluded.total_items,
  success_items=excluded.success_items,
  failed_items=excluded.failed_items,
  failure_reason=excluded.failure_reason,
  error_count=excluded.error_count,
  attempt_count=excluded.attempt_count,
  last_error=excluded.last_error,
  is_retrying=excluded.is_retrying,
  next_retry_at=excluded.next_retry_at,
  progress_stage=excluded.progress_stage,
  idempotency_key=excluded.idempotency_key,
  items_json=excluded.items_json,
  errors_json=excluded.errors_json
`
	finishedAt := ""
	if !result.FinishedAt.IsZero() {
		finishedAt = result.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	nextRetryAt := ""
	if !result.NextRetryAt.IsZero() {
		nextRetryAt = result.NextRetryAt.UTC().Format(time.RFC3339Nano)
	}
	isRetrying := 0
	if result.IsRetrying {
		isRetrying = 1
	}
	_, err = s.db.ExecContext(ctx, q,
		result.ResultID,
		result.DealershipID,
		result.SourceURL,
		string(result.Status),
		result.StartedAt.UTC().Format(time.RFC3339Nano),
		finishedAt,
		result.TotalItems,
		result.SuccessItems,
		result.FailedItems,
		result.FailureReason,
		result.ErrorCount,
		result.AttemptCount,
		result.LastError,
		isRetrying,
		nextRetryAt,
		result.ProgressStage,
		result.IdempotencyKey,
		string(items),
		string(errs),
	)
	return err
}

func (s *SQLiteResultStore) GetResult(ctx context.Context, resultID string) (model.ScrapeResult, error) {
	const q = `SELECT result_id, dealership_id, source_url, status, started_at, finished_at, total_items, success_items, failed_items, failure_reason, error_count, attempt_count, last_error, is_retrying, next_retry_at, progress_stage, idempotency_key, items_json, errors_json FROM scrape_results WHERE result_id = ?`
	return s.scanOne(ctx, q, resultID)
}

func (s *SQLiteResultStore) ClearIdempotency(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scrape_results SET idempotency_key = '' WHERE idempotency_key IS NOT NULL AND idempotency_key != '' AND idempotency_key NOT LIKE 'scrape-once-cache|%'`)
	return err
}

func (s *SQLiteResultStore) ClearResults(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scrape_results`)
	return err
}

func (s *SQLiteResultStore) UpsertCachedInventory(ctx context.Context, c CachedInventory) error {
	items, err := json.Marshal(c.Items)
	if err != nil {
		return err
	}
	updatedAt := c.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	const q = `
INSERT INTO cached_inventory (source_url, dealership_id, account_id, items_json, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(source_url) DO UPDATE SET
  dealership_id=excluded.dealership_id,
  account_id=excluded.account_id,
  items_json=excluded.items_json,
  updated_at=excluded.updated_at`
	_, err = s.db.ExecContext(ctx, q,
		NormalizeURLKey(c.SourceURL),
		c.DealershipID,
		c.AccountID,
		string(items),
		updatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteResultStore) GetCachedInventory(ctx context.Context, sourceURL string) (CachedInventory, error) {
	const q = `SELECT source_url, dealership_id, account_id, items_json, updated_at FROM cached_inventory WHERE source_url = ?`
	var out CachedInventory
	var itemsJSON, updatedAt string
	err := s.db.QueryRowContext(ctx, q, NormalizeURLKey(sourceURL)).Scan(
		&out.SourceURL, &out.DealershipID, &out.AccountID, &itemsJSON, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return CachedInventory{}, ErrNotFound
	}
	if err != nil {
		return CachedInventory{}, err
	}
	if err := json.Unmarshal([]byte(itemsJSON), &out.Items); err != nil {
		return CachedInventory{}, err
	}
	if updatedAt != "" {
		out.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	}
	return out, nil
}

func (s *SQLiteResultStore) ClearCachedInventory(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cached_inventory`)
	return err
}

func (s *SQLiteResultStore) GetCachedInventoryByHost(ctx context.Context, host string) (CachedInventory, error) {
	if host == "" {
		return CachedInventory{}, ErrNotFound
	}
	const q = `SELECT source_url, dealership_id, account_id, items_json, updated_at FROM cached_inventory ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return CachedInventory{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var out CachedInventory
		var itemsJSON, updatedAt string
		if err := rows.Scan(&out.SourceURL, &out.DealershipID, &out.AccountID, &itemsJSON, &updatedAt); err != nil {
			return CachedInventory{}, err
		}
		if HostOf(out.SourceURL) != host {
			continue
		}
		if err := json.Unmarshal([]byte(itemsJSON), &out.Items); err != nil {
			return CachedInventory{}, err
		}
		if updatedAt != "" {
			out.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		}
		return out, nil
	}
	if err := rows.Err(); err != nil {
		return CachedInventory{}, err
	}
	return CachedInventory{}, ErrNotFound
}

func (s *SQLiteResultStore) FlagProtectedURL(ctx context.Context, p ProtectedURL) error {
	flaggedAt := p.FlaggedAt
	if flaggedAt.IsZero() {
		flaggedAt = time.Now().UTC()
	}
	const q = `
INSERT INTO protected_urls (source_url, reason, flagged_at)
VALUES (?, ?, ?)
ON CONFLICT(source_url) DO UPDATE SET
  reason=excluded.reason,
  flagged_at=excluded.flagged_at`
	_, err := s.db.ExecContext(ctx, q, NormalizeURLKey(p.SourceURL), p.Reason, flaggedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteResultStore) UnflagProtectedURL(ctx context.Context, sourceURL string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM protected_urls WHERE source_url = ?`, NormalizeURLKey(sourceURL))
	return err
}

func (s *SQLiteResultStore) IsProtectedURL(ctx context.Context, sourceURL string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM protected_urls WHERE source_url = ?`, NormalizeURLKey(sourceURL)).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteResultStore) ListProtectedURLs(ctx context.Context) ([]ProtectedURL, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_url, reason, flagged_at FROM protected_urls ORDER BY flagged_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ProtectedURL, 0)
	for rows.Next() {
		var p ProtectedURL
		var flaggedAt string
		if err := rows.Scan(&p.SourceURL, &p.Reason, &flaggedAt); err != nil {
			return nil, err
		}
		if flaggedAt != "" {
			p.FlaggedAt, _ = time.Parse(time.RFC3339Nano, flaggedAt)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *SQLiteResultStore) FindByIdempotency(ctx context.Context, key string) (model.ScrapeResult, error) {
	if key == "" {
		return model.ScrapeResult{}, ErrNotFound
	}
	const q = `SELECT result_id, dealership_id, source_url, status, started_at, finished_at, total_items, success_items, failed_items, failure_reason, error_count, attempt_count, last_error, is_retrying, next_retry_at, progress_stage, idempotency_key, items_json, errors_json FROM scrape_results WHERE idempotency_key = ? ORDER BY started_at DESC LIMIT 1`
	return s.scanOne(ctx, q, key)
}

func (s *SQLiteResultStore) DeleteIdempotency(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scrape_results SET idempotency_key = '' WHERE idempotency_key = ?`, key)
	return err
}

func (s *SQLiteResultStore) scanOne(ctx context.Context, query string, arg any) (model.ScrapeResult, error) {
	var out model.ScrapeResult
	var status string
	var startedAt, finishedAt, nextRetryAt string
	var itemsJSON, errorsJSON string
	var progressStage sql.NullString
	var isRetrying int
	err := s.db.QueryRowContext(ctx, query, arg).Scan(
		&out.ResultID,
		&out.DealershipID,
		&out.SourceURL,
		&status,
		&startedAt,
		&finishedAt,
		&out.TotalItems,
		&out.SuccessItems,
		&out.FailedItems,
		&out.FailureReason,
		&out.ErrorCount,
		&out.AttemptCount,
		&out.LastError,
		&isRetrying,
		&nextRetryAt,
		&progressStage,
		&out.IdempotencyKey,
		&itemsJSON,
		&errorsJSON,
	)
	if err == sql.ErrNoRows {
		return model.ScrapeResult{}, ErrNotFound
	}
	if err != nil {
		return model.ScrapeResult{}, err
	}
	out.Status = model.RunStatus(status)
	if progressStage.Valid {
		out.ProgressStage = progressStage.String
	}
	if out.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
		return model.ScrapeResult{}, fmt.Errorf("invalid started_at: %w", err)
	}
	if finishedAt != "" {
		if out.FinishedAt, err = time.Parse(time.RFC3339Nano, finishedAt); err != nil {
			return model.ScrapeResult{}, fmt.Errorf("invalid finished_at: %w", err)
		}
	}
	out.IsRetrying = isRetrying == 1
	if nextRetryAt != "" {
		if out.NextRetryAt, err = time.Parse(time.RFC3339Nano, nextRetryAt); err != nil {
			return model.ScrapeResult{}, fmt.Errorf("invalid next_retry_at: %w", err)
		}
	}
	if err := json.Unmarshal([]byte(itemsJSON), &out.Items); err != nil {
		return model.ScrapeResult{}, err
	}
	if err := json.Unmarshal([]byte(errorsJSON), &out.Errors); err != nil {
		return model.ScrapeResult{}, err
	}
	return out, nil
}

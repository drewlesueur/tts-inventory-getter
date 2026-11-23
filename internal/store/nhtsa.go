package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/google/uuid"
)

var ErrNHTSAConflict = errors.New("idempotency key already used for another VIN")
var ErrNHTSAQueueFull = errors.New("lookup queue is full")

// NHTSA jobs share the existing SQLite database, but are independent of inventory cache clearing.
func (s *SQLiteResultStore) InitNHTSA(ctx context.Context) error {
	// Serialize SQLite writes across inventory and lookup workers.
	s.db.SetMaxOpenConns(1)
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS nhtsa_lookups (
 result_id TEXT PRIMARY KEY, dealership_id TEXT NOT NULL, idempotency_key TEXT NOT NULL,
 vin TEXT NOT NULL, status TEXT NOT NULL, created_at INTEGER NOT NULL, lookup_at INTEGER,
 error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '', pdf BLOB,
 UNIQUE(dealership_id,idempotency_key));
 CREATE INDEX IF NOT EXISTS nhtsa_status ON nhtsa_lookups(status,created_at);`)
	return err
}

func (s *SQLiteResultStore) CreateNHTSA(ctx context.Context, dealer, vin, key string) (model.NHTSALookup, error) {
	// One atomic statement enforces queue capacity even across server processes.
	_, err := s.db.ExecContext(ctx, `INSERT INTO nhtsa_lookups(result_id,dealership_id,idempotency_key,vin,status,created_at)
 SELECT ?,?,?,?,'pending',? WHERE (SELECT count(*) FROM nhtsa_lookups WHERE status IN ('pending','running')) < 100
 ON CONFLICT(dealership_id,idempotency_key) DO NOTHING`, uuid.NewString(), dealer, key, vin, time.Now().Unix())
	if err != nil {
		return model.NHTSALookup{}, err
	}
	r, err := scanNHTSA(s.db.QueryRowContext(ctx, `SELECT result_id,dealership_id,vin,status,lookup_at,error_code,error_message FROM nhtsa_lookups WHERE dealership_id=? AND idempotency_key=?`, dealer, key))
	if errors.Is(err, ErrNotFound) {
		return r, ErrNHTSAQueueFull
	}
	if err == nil && r.VIN != vin {
		return r, ErrNHTSAConflict
	}
	return r, err
}

type nhtsaScanner interface{ Scan(...any) error }

func scanNHTSA(row nhtsaScanner) (model.NHTSALookup, error) {
	var r model.NHTSALookup
	var ts sql.NullInt64
	var code, msg string
	err := row.Scan(&r.ResultID, &r.DealershipID, &r.VIN, &r.Status, &ts, &code, &msg)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if ts.Valid {
		t := time.Unix(ts.Int64, 0).UTC()
		r.LookupDate = &t
	}
	if code != "" {
		r.Error = &model.NHTSAError{Code: code, Message: msg}
	}
	return r, err
}
func (s *SQLiteResultStore) GetNHTSA(ctx context.Context, id, dealer string) (model.NHTSALookup, error) {
	return scanNHTSA(s.db.QueryRowContext(ctx, `SELECT result_id,dealership_id,vin,status,lookup_at,error_code,error_message FROM nhtsa_lookups WHERE result_id=? AND dealership_id=?`, id, dealer))
}
func (s *SQLiteResultStore) NHTSAPDF(ctx context.Context, id, dealer string) ([]byte, error) {
	var pdf []byte
	err := s.db.QueryRowContext(ctx, `SELECT pdf FROM nhtsa_lookups WHERE result_id=? AND dealership_id=? AND status='completed'`, id, dealer).Scan(&pdf)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return pdf, err
}
func (s *SQLiteResultStore) ClaimNHTSA(ctx context.Context) (model.NHTSALookup, error) {
	// Global concurrency of one, including when multiple service instances share this database.
	return scanNHTSA(s.db.QueryRowContext(ctx, `UPDATE nhtsa_lookups SET status='running',lookup_at=?
 WHERE result_id=(SELECT result_id FROM nhtsa_lookups WHERE status='pending' ORDER BY created_at LIMIT 1)
 AND NOT EXISTS(SELECT 1 FROM nhtsa_lookups WHERE status='running')
 RETURNING result_id,dealership_id,vin,status,lookup_at,error_code,error_message`, time.Now().Unix()))
}
func (s *SQLiteResultStore) FinishNHTSA(ctx context.Context, id string, pdf []byte, failure *model.NHTSAError) error {
	status, code, msg := "completed", "", ""
	if failure != nil {
		status, code, msg = "failed", failure.Code, failure.Message
		pdf = nil
	}
	if failure == nil && (len(pdf) == 0 || len(pdf) >= model.NHTSAMaxPDF) {
		return errors.New("invalid PDF size")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE nhtsa_lookups SET status=?,error_code=?,error_message=?,pdf=? WHERE result_id=? AND status='running'`, status, code, msg, pdf, id)
	return err
}
func (s *SQLiteResultStore) MaintainNHTSA(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nhtsa_lookups SET status='failed',error_code='WORKER_INTERRUPTED',error_message='Lookup worker stopped before completion' WHERE status='running' AND lookup_at < ?`, time.Now().Add(-5*time.Minute).Unix())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM nhtsa_lookups WHERE status IN ('completed','failed') AND created_at < ?`, time.Now().Add(-7*24*time.Hour).Unix())
	return err
}

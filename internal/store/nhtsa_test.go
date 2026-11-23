package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNHTSADurableQueue(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.db")
	s, err := NewSQLiteResultStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// SQLite's normal connection pool is restricted here to make concurrent requests wait.
	s.db.SetMaxOpenConns(1)
	if err = s.InitNHTSA(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.CreateNHTSA(ctx, "a", "1FMUK7DHXSGA27345", "same")
			if e != nil {
				t.Error(e)
			}
			ids <- r.ResultID
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for v := range ids {
		if id != "" && id != v {
			t.Fatal("duplicate jobs")
		}
		id = v
	}
	if err = s.ClearIdempotency(ctx); err != nil {
		t.Fatal(err)
	}
	same, err := s.CreateNHTSA(ctx, "a", "1FMUK7DHXSGA27345", "same")
	if err != nil || same.ResultID != id {
		t.Fatal("inventory maintenance affected NHTSA")
	}
	s.Close()
	s, err = NewSQLiteResultStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job, err := s.ClaimNHTSA(ctx)
	if err != nil || job.ResultID != id {
		t.Fatal("pending job lost on restart", err)
	}
	s.db.Exec(`UPDATE nhtsa_lookups SET lookup_at=? WHERE result_id=?`, time.Now().Add(-6*time.Minute).Unix(), id)
	if err = s.MaintainNHTSA(ctx); err != nil {
		t.Fatal(err)
	}
	job, err = s.GetNHTSA(ctx, id, "a")
	if err != nil || job.Status != "failed" || job.Error.Code != "WORKER_INTERRUPTED" {
		t.Fatal(job, err)
	}
	s.db.Exec(`UPDATE nhtsa_lookups SET created_at=? WHERE result_id=?`, time.Now().Add(-8*24*time.Hour).Unix(), id)
	if err = s.MaintainNHTSA(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetNHTSA(ctx, id, "a"); err != ErrNotFound {
		t.Fatal("retention failed", err)
	}
	for i := 0; i < 100; i++ {
		if _, err = s.CreateNHTSA(ctx, "a", "1FMUK7DHXSGA27345", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateNHTSA(ctx, "a", "1FMUK7DHXSGA27345", "overflow"); err != ErrNHTSAQueueFull {
		t.Fatal(err)
	}
	if _, err = s.CreateNHTSA(ctx, "a", "1FMUK7DHXSGA27345", "0"); err != nil {
		t.Fatal("duplicate at capacity", err)
	}
}

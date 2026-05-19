package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoaderIsCacheFresh(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader(dir)
	cfg := SiteConfig{
		Name:    "url::dealer.test/inventory",
		BaseURL: "https://dealer.test/inventory",
		ListPage: ListPageConfig{
			CardSelector: ".vehicle-card",
		},
	}
	key := "url::dealer.test/inventory"
	if err := loader.SaveByName(key, cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if !loader.IsCacheFresh(key, 7*24*time.Hour) {
		t.Fatalf("expected fresh cache file")
	}

	// Force file to look old and verify freshness check expires it.
	p := filepath.Join(dir, encodeCacheFilename(key)+".yaml")
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("chtimes failed: %v", err)
	}
	if loader.IsCacheFresh(key, 7*24*time.Hour) {
		t.Fatalf("expected cache file to be stale after ttl")
	}
}


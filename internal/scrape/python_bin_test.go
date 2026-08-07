package scrape

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePythonBinFallsBackToPython3(t *testing.T) {
	dir := t.TempDir()
	python3 := filepath.Join(dir, "python3")
	if err := os.WriteFile(python3, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake python3: %v", err)
	}
	t.Setenv("PATH", dir)
	if got := resolvePythonBin("python3.11"); got != "python3" {
		t.Fatalf("expected python3 fallback, got %q", got)
	}
	if got := NewBatchDetailFetcher("details.py", "python3.11", nil, nil).PythonBin; got != "python3" {
		t.Fatalf("expected batch fetcher to use python3, got %q", got)
	}
	if got := NewCurlFetcher("fetch.py", "python3.11", nil).PythonBin; got != "python3" {
		t.Fatalf("expected curl fetcher to use python3, got %q", got)
	}
}

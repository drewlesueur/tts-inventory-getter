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

func TestResolvePythonBinUsesProvisionedVenv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PATH", dir)
	venv := filepath.Join(dir, ".venv")
	python := filepath.Join(venv, "bin", "python")
	if err := os.MkdirAll(filepath.Dir(python), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(python, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolvePythonBin(""); got != "python3" {
		t.Fatalf("incomplete venv selected: %q", got)
	}
	if err := os.WriteFile(filepath.Join(venv, "pyvenv.cfg"), []byte("home = /usr/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolvePythonBin("python3.11"); got != python {
		t.Fatalf("expected provisioned venv, got %q", got)
	}
	if got := resolvePythonBin("/bin/sh"); got != "/bin/sh" {
		t.Fatalf("explicit executable lost precedence: %q", got)
	}
}

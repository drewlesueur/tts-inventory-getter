package scrape

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserSlotCancellationAndCleanup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "slot")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := acquireBrowserSlot(context.Background(), dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = acquireBrowserSlot(context.Background(), dir, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("overlapping lease returned %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = acquireBrowserSlot(ctx, dir, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquisition returned %v", err)
	}
	release(true)
	release, err = acquireBrowserSlot(context.Background(), dir, time.Second)
	if err != nil {
		t.Fatalf("normal cleanup did not release slot: %v", err)
	}
	release(false)
	if _, err := acquireBrowserSlot(context.Background(), dir, time.Second); err == nil {
		t.Fatal("unfinished lease admitted another browser")
	}
}

func TestBrowserSlotInteroperatesWithPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	dir := filepath.Join(t.TempDir(), "slot")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := acquireBrowserSlot(ctx, dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The Python child uses the same lock protocol with a private fixture file.
	pythonCheck := `
import os, sys
sys.path.insert(0, sys.argv[1])
from browser_slot import _try_lock
fd = os.open(sys.argv[2], os.O_RDWR)
try:
    assert not _try_lock(fd)
finally:
    os.close(fd)
`
	cmd := exec.CommandContext(ctx, python, "-I", "-B", "-c", pythonCheck, "../../scripts", filepath.Join(dir, "browser.lock"))
	out, err := cmd.CombinedOutput()
	release(true)
	if err != nil {
		t.Fatalf("Python ignored shared lock: %v %s", err, out)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "browser.lock")); err != nil || len(content) != 0 {
		t.Fatalf("clean release did not clear marker: %q %v", content, err)
	}
	pythonCleanup := `
import asyncio, os, sys
sys.path.insert(0, sys.argv[1])
import browser_slot

browser_slot._open_lock = lambda: os.open(sys.argv[2], os.O_RDWR)

class FakeBrowser:
    async def __aenter__(self):
        return self

    async def __aexit__(self, kind, value, traceback):
        assert kind is ValueError and str(value) == "navigation failure"
        assert os.stat(sys.argv[2]).st_size > 0

async def check():
    try:
        async with browser_slot.guarded_async_browser(FakeBrowser):
            raise ValueError("navigation failure")
    except ValueError:
        pass
    assert os.stat(sys.argv[2]).st_size == 0

asyncio.run(check())
`
	cmd = exec.CommandContext(ctx, python, "-I", "-B", "-c", pythonCleanup, "../../scripts", filepath.Join(dir, "browser.lock"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Python did not release after cleanup: %v %s", err, out)
	}
}

func TestBrowserChildCancellationReapsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "exec sleep 30")
	manageChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled process unexpectedly succeeded")
	}
	if !cleanChildProcessGroup(cmd) {
		t.Fatal("cancelled process group survived")
	}
	if browserCommandClean(ctx, cmd, nil) {
		t.Fatal("cancelled browser command should retain the recovery marker")
	}
}

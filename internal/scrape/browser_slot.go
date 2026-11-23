package scrape

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const browserSlotWait = 10 * time.Second

// AcquireBrowserSlot serializes browser work across Go and Python processes
// running as the scraper account. Call it only at a browser-owning leaf.
func AcquireBrowserSlot(ctx context.Context) (release func(clean bool), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir := filepath.Join("/tmp", "tts-scraper-browser-"+strconv.Itoa(os.Geteuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create browser lock directory: %w", err)
	}
	return acquireBrowserSlot(ctx, dir, browserSlotWait)
}

func acquireBrowserSlot(ctx context.Context, dir string, wait time.Duration) (func(bool), error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect browser lock directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return nil, errors.New("browser lock directory must be owned by the scraper account with mode 0700")
	}

	fd, err := syscall.Open(filepath.Join(dir, "browser.lock"), syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open browser lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), "browser.lock")
	success := false
	defer func() {
		if !success {
			_ = file.Close()
		}
	}()

	deadline, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := deadline.Err(); err != nil {
			return nil, fmt.Errorf("wait for browser slot: %w", err)
		}
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return nil, fmt.Errorf("lock browser slot: %w", err)
		}
		select {
		case <-deadline.Done():
		case <-ticker.C:
		}
	}
	if err := deadline.Err(); err != nil {
		return nil, fmt.Errorf("wait for browser slot: %w", err)
	}
	state, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect browser lock: %w", err)
	}
	if !state.Mode().IsRegular() || state.Mode().Perm() != 0o600 || int(state.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return nil, errors.New("browser lock must be a private regular file owned by the scraper account")
	}
	if state.Size() > 0 {
		return nil, errors.New("browser slot has an unfinished lease; verify browser cleanup before clearing the lock file")
	}
	// A holder killed before cleanup leaves a marker and the next job fails closed.
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		return nil, fmt.Errorf("mark browser slot active: %w", err)
	}
	success = true
	return func(clean bool) {
		if clean {
			_ = file.Truncate(0)
		}
		_ = file.Close()
	}, nil
}

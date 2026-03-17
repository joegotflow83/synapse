package lock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const (
	lockFileName   = ".synapse.lock"
	DefaultTimeout = 5 * time.Second
	retryDelay     = 50 * time.Millisecond
)

// LockPath returns the lock file path for a given directory.
func LockPath(dir string) string {
	return filepath.Join(dir, lockFileName)
}

// AcquireExclusive acquires an exclusive (write) lock on the directory.
// The caller must call Unlock() on the returned Flock when done.
func AcquireExclusive(dir string, timeout time.Duration) (*flock.Flock, error) {
	lockPath := LockPath(dir)
	if err := ensureLockFileExists(lockPath); err != nil {
		return nil, fmt.Errorf("lock setup: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fl := flock.New(lockPath)
	ok, err := fl.TryLockContext(ctx, retryDelay)
	if err != nil {
		return nil, fmt.Errorf("exclusive lock failed: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("exclusive lock timeout after %s", timeout)
	}
	return fl, nil
}

// AcquireShared acquires a shared (read) lock on the directory.
// The caller must call Unlock() on the returned Flock when done.
func AcquireShared(dir string, timeout time.Duration) (*flock.Flock, error) {
	lockPath := LockPath(dir)
	if err := ensureLockFileExists(lockPath); err != nil {
		return nil, fmt.Errorf("lock setup: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fl := flock.New(lockPath)
	ok, err := fl.TryRLockContext(ctx, retryDelay)
	if err != nil {
		return nil, fmt.Errorf("shared lock failed: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("shared lock timeout after %s", timeout)
	}
	return fl, nil
}

func ensureLockFileExists(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

package lock

import (
	"os"
	"sync"
	"testing"
	"time"
)

func TestAcquireExclusive(t *testing.T) {
	dir := t.TempDir()
	fl, err := AcquireExclusive(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("AcquireExclusive failed: %v", err)
	}
	defer fl.Unlock()

	if !fl.Locked() {
		t.Fatal("expected lock to be held")
	}
}

func TestAcquireShared(t *testing.T) {
	dir := t.TempDir()
	fl, err := AcquireShared(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("AcquireShared failed: %v", err)
	}
	defer fl.Unlock()

	if !fl.RLocked() {
		t.Fatal("expected shared lock to be held")
	}
}

func TestExclusiveBlocksExclusive(t *testing.T) {
	dir := t.TempDir()
	fl1, err := AcquireExclusive(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("first exclusive lock failed: %v", err)
	}
	defer fl1.Unlock()

	// Second exclusive lock should timeout quickly
	_, err = AcquireExclusive(dir, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected second exclusive lock to fail")
	}
}

func TestSharedAllowsConcurrentShared(t *testing.T) {
	dir := t.TempDir()
	fl1, err := AcquireShared(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("first shared lock failed: %v", err)
	}
	defer fl1.Unlock()

	fl2, err := AcquireShared(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("second shared lock should succeed: %v", err)
	}
	defer fl2.Unlock()
}

func TestExclusiveBlocksShared(t *testing.T) {
	dir := t.TempDir()
	fl1, err := AcquireExclusive(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("exclusive lock failed: %v", err)
	}
	defer fl1.Unlock()

	_, err = AcquireShared(dir, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected shared lock to fail while exclusive is held")
	}
}

func TestUnlockReleasesExclusive(t *testing.T) {
	dir := t.TempDir()
	fl1, err := AcquireExclusive(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("first exclusive lock failed: %v", err)
	}
	fl1.Unlock()

	// Should succeed after unlock
	fl2, err := AcquireExclusive(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("second exclusive lock after unlock failed: %v", err)
	}
	defer fl2.Unlock()
}

func TestLockFileCreated(t *testing.T) {
	dir := t.TempDir()
	fl, err := AcquireExclusive(dir, DefaultTimeout)
	if err != nil {
		t.Fatalf("lock failed: %v", err)
	}
	defer fl.Unlock()

	lockPath := LockPath(dir)
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Fatalf("lock file not created at %s", lockPath)
	}
}

func TestConcurrentExclusiveLocks(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var acquired int
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fl, err := AcquireExclusive(dir, 2*time.Second)
			if err != nil {
				return
			}
			mu.Lock()
			acquired++
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			fl.Unlock()
		}()
	}
	wg.Wait()

	if acquired == 0 {
		t.Fatal("no goroutine acquired the lock")
	}
}

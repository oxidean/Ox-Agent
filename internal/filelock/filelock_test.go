package filelock

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "test.lock")
}

// TestAcquireCreatesParents verifies Acquire materializes missing parent
// directories rather than requiring callers to pre-create the locks dir.
func TestAcquireCreatesParents(t *testing.T) {
	path := lockPath(t)
	l, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer l.Release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file missing after Acquire: %v", err)
	}
}

// TestAcquireExcludesConcurrentHolder verifies a held lock blocks a second
// acquisition until timeout — the core mutual-exclusion property.
func TestAcquireExcludesConcurrentHolder(t *testing.T) {
	path := lockPath(t)
	held, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer held.Release()
	start := time.Now()
	if _, err := Acquire(path, 150*time.Millisecond); err == nil {
		t.Fatal("second Acquire succeeded while lock held; want contention")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("second Acquire returned after %s; want it to wait for the timeout", elapsed)
	}
}

// TestReleaseReacquires verifies Release frees the lock for the next
// acquirer.
func TestReleaseReacquires(t *testing.T) {
	path := lockPath(t)
	held, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	second, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire after Release error = %v", err)
	}
	defer second.Release()
}

// TestClosedDescriptorReleases simulates a holder dying mid-critical-
// section: closing the underlying descriptor must drop the lock so a
// waiter proceeds immediately — no stale window, no reclamation.
func TestClosedDescriptorReleases(t *testing.T) {
	path := lockPath(t)
	held, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	// Simulate process death: the descriptor closes without Release().
	if err := held.f.Close(); err != nil {
		t.Fatalf("simulated crash close: %v", err)
	}
	waiter, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire after holder death error = %v; kernel lock must auto-release", err)
	}
	defer waiter.Release()
}

// TestLeftoverLockFileDoesNotBlock verifies a lock *file* left behind by a
// dead process is not held state — existence of the path must not gate
// acquisition.
func TestLeftoverLockFileDoesNotBlock(t *testing.T) {
	path := lockPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("stale-holder\n"), 0o644); err != nil {
		t.Fatalf("plant leftover lock file: %v", err)
	}
	l, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire over leftover lock file error = %v", err)
	}
	defer l.Release()
}

// TestWithSerializes verifies concurrent With calls run their critical
// sections one at a time. An atomic occupancy counter detects true
// concurrent entry: the race detector cannot see flock's happens-before
// (a syscall, not a sync primitive), so exclusion is proven by occupancy
// never exceeding one rather than by unsynchronized shared writes.
func TestWithSerializes(t *testing.T) {
	path := lockPath(t)
	const n = 16
	var wg sync.WaitGroup
	var inFlight atomic.Int32
	var overlapped atomic.Bool
	visited := make([]int, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- With(path, DefaultTimeout, func() error {
				if cur := inFlight.Add(1); cur > 1 {
					overlapped.Store(true)
				}
				visited[i] = i
				inFlight.Add(-1)
				return nil
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("With() error = %v", err)
		}
	}
	if overlapped.Load() {
		t.Fatal("critical sections overlapped; lock did not serialize")
	}
	for i, v := range visited {
		if v != i {
			t.Fatalf("visited[%d] = %d; critical section lost", i, v)
		}
	}
}

// TestWithReleasesOnFnError verifies With drops the lock when fn fails so
// error paths cannot strand the lock.
func TestWithReleasesOnFnError(t *testing.T) {
	path := lockPath(t)
	if err := With(path, time.Second, func() error { return os.ErrInvalid }); err == nil {
		t.Fatal("With() returned nil for failing fn")
	}
	l, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire after failed With error = %v; lock stranded", err)
	}
	defer l.Release()
}

// TestReleaseIsIdempotent verifies a double Release is a no-op rather than
// an error on a closed descriptor.
func TestReleaseIsIdempotent(t *testing.T) {
	path := lockPath(t)
	l, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release() error = %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
}

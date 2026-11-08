// Package filelock provides cross-process mutual exclusion through OS file
// locks, replacing the lock-file heuristics that raced on stale-lock
// reclamation. A kernel lock is bound to an open file description: it is
// released automatically when the holder exits — even on crash or kill —
// so there is no stale state to reclaim and no stat-then-remove window a
// rival can steal a fresh lock through.
package filelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultTimeout preserves the lock-wait contract the previous lock-file
// implementation offered callers.
const DefaultTimeout = 10 * time.Second

// pollInterval is the retry cadence between non-blocking lock attempts.
const pollInterval = 10 * time.Millisecond

// ErrUnsupported reports that the platform has no file-lock implementation.
var ErrUnsupported = errors.New("filelock: unsupported platform")

// Lock is a held exclusive OS-level lock on a file.
type Lock struct {
	f *os.File
}

// Acquire opens path — creating it and any missing parent directories —
// and takes an exclusive OS lock on it, polling until timeout elapses.
// The lock file itself is never removed: under kernel locking an existing
// file is not held state, and unlinking while waiters sleep would swap the
// inode they contended on.
func Acquire(path string, timeout time.Duration) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := lock(f)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if !isLocked(err) {
			f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("timed out waiting for lock %s", path)
		}
		time.Sleep(pollInterval)
	}
}

// Release unlocks and closes the lock. It is safe to call more than once.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	unlockErr := unlock(f)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// With runs fn while holding an exclusive OS lock on path.
func With(path string, timeout time.Duration, fn func() error) error {
	l, err := Acquire(path, timeout)
	if err != nil {
		return err
	}
	defer l.Release()
	return fn()
}

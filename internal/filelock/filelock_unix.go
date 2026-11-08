//go:build unix

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lock attempts a non-blocking exclusive flock on the file descriptor.
func lock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

// unlock drops the flock explicitly; closing the descriptor would also
// release it.
func unlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

// isLocked reports contention — the lock is held by another open file
// description.
func isLocked(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK)
}

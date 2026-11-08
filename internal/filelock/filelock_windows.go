//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockedRange covers the entire possible file extent so every process
// contends on the same byte range.
const (
	lockedRangeLow  = 0xFFFFFFFF
	lockedRangeHigh = 0xFFFFFFFF
)

// lock attempts a non-blocking exclusive LockFileEx on the file handle.
// LOCKFILE_FAIL_IMMEDIATELY makes it return ERROR_LOCK_VIOLATION on
// contention instead of blocking without a timeout path.
func lock(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockedRangeLow, lockedRangeHigh, &ol)
}

// unlock releases the same byte range lock() acquired.
func unlock(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockedRangeLow, lockedRangeHigh, &ol)
}

// isLocked reports contention — the range is locked by another handle.
func isLocked(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

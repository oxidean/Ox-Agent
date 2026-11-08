//go:build !unix && !windows

package filelock

import "os"

// lock fails every acquisition so unsupported platforms report
// ErrUnsupported wrapped by Acquire rather than a fake success.
func lock(f *os.File) error {
	return ErrUnsupported
}

func unlock(f *os.File) error {
	return nil
}

func isLocked(err error) bool {
	return false
}

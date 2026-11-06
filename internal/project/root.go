package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotFound reports that no ancestor of a start path contains .ox-agent.
var ErrNotFound = errors.New("could not find .ox-agent")

// Find locates the nearest ancestor of start containing a .ox-agent
// directory, so project commands work from any subdirectory.
func Find(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if current, err = filepath.EvalSymlinks(current); err != nil {
		return "", fmt.Errorf("resolving %s: %w", start, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, ".ox-agent")); err == nil {
			return current, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("checking %s: %w", filepath.Join(current, ".ox-agent"), err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("%w from %s", ErrNotFound, start)
		}
		current = parent
	}
}

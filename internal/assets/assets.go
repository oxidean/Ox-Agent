// Package assets exposes the official Ox-Agent asset tree embedded in the
// binary and materializes it into project .ox-agent directories.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:embedded
var tree embed.FS

const root = "embedded"

// Materialize expands the embedded asset tree under base, returning the
// relative slash paths of every file written.
func Materialize(base string) (written []string, err error) {
	err = fs.WalkDir(tree, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, root+"/")
		if rel == path || rel == "" {
			return nil
		}
		target := filepath.Join(base, filepath.FromSlash(rel))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := tree.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading embedded asset %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	})
	return written, err
}

// ReadFile reads rel from the embedded tree.
func ReadFile(rel string) ([]byte, error) {
	return tree.ReadFile(root + "/" + rel)
}

// List returns the relative slash paths of every file under dir in the
// embedded tree ("" lists the whole tree).
func List(dir string) ([]string, error) {
	base := root
	if dir != "" {
		base += "/" + dir
	}
	var out []string
	err := fs.WalkDir(tree, base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			out = append(out, strings.TrimPrefix(path, root+"/"))
		}
		return nil
	})
	return out, err
}

// Package assets exposes the official Ox-Agent asset tree embedded in the
// binary and materializes it into project .ox-agent directories.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed all:embedded
var tree embed.FS

// executables.txt records which canonical asset files carry the executable
// bit — embed.FS drops file modes, so sync-assets.sh emits this manifest
// alongside the embedded tree. It lives outside embedded/ so Materialize
// never ships it into projects.
//
//go:embed executables.txt
var executablesManifest string

var executables = func() map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(executablesManifest, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set
}()

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
		if err := WriteAsset(target, rel, data); err != nil {
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

// IsExecutable reports whether rel is a canonical asset recorded with the
// executable bit in executables.txt.
func IsExecutable(rel string) bool {
	return executables[rel]
}

// Executables returns the sorted canonical asset paths recorded as
// executable.
func Executables() []string {
	out := make([]string, 0, len(executables))
	for rel := range executables {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// WriteAsset writes data to target as the canonical asset rel: mode 0755
// when the manifest records rel as executable, 0644 otherwise. The mode is
// applied explicitly because os.WriteFile only honors its perm argument on
// newly created files.
func WriteAsset(target, rel string, data []byte) error {
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return err
	}
	want := os.FileMode(0o644)
	if IsExecutable(rel) {
		want = 0o755
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if info.Mode().Perm() == want {
		return nil
	}
	return os.Chmod(target, want)
}

package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindWalksUp verifies project discovery resolves the root from nested subdirectories.
func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	nested := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}
	got, err := Find(nested)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolving root: %v", err)
	}
	if got, err = filepath.EvalSymlinks(got); err != nil || got != want {
		t.Fatalf("Find() = %q; want %q", got, want)
	}
	if _, err := Find(t.TempDir()); err == nil {
		t.Skip("temporary directory resides inside an Ox-Agent project")
	}
}

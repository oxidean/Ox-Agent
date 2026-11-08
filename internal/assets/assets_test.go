package assets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMaterializeAppliesExecBits verifies files recorded executable in the
// manifest materialize 0755 and everything else 0644.
func TestMaterializeAppliesExecBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bits are not representable on Windows")
	}
	base := t.TempDir()
	written, err := Materialize(base)
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if len(executables) == 0 {
		t.Fatal("test requires at least one executable asset in the manifest")
	}
	for _, rel := range written {
		info, err := os.Stat(filepath.Join(base, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		want := os.FileMode(0o644)
		if IsExecutable(rel) {
			want = 0o755
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", rel, got, want)
		}
	}
}

// TestExecutablesManifestCoversEmbedded guards against the manifest
// drifting from the embedded tree: every recorded path must ship.
func TestExecutablesManifestCoversEmbedded(t *testing.T) {
	embedded, err := List("")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	set := make(map[string]bool, len(embedded))
	for _, rel := range embedded {
		set[rel] = true
	}
	for _, rel := range Executables() {
		if !set[rel] {
			t.Errorf("manifest records %s executable but the embedded tree does not ship it", rel)
		}
	}
}

// TestWriteAssetFixesExistingMode verifies WriteAsset resets the mode of a
// pre-existing file — os.WriteFile alone would keep the old bits.
func TestWriteAssetFixesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bits are not representable on Windows")
	}
	if len(executables) == 0 {
		t.Skip("no executable assets recorded")
	}
	rel := Executables()[0]
	target := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAsset(target, rel, []byte("#!/bin/sh\n")); err != nil {
		t.Fatalf("WriteAsset: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %o, want 755", got)
	}
}

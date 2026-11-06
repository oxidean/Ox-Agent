package assets

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLockRoundTrip verifies WriteLock covers every embedded file and
// CheckLock reports a clean tree.
func TestLockRoundTrip(t *testing.T) {
	base := t.TempDir()
	if _, err := Materialize(base); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if err := WriteLock(base); err != nil {
		t.Fatalf("WriteLock() error = %v", err)
	}
	missing, changed, unlisted, stale, err := CheckLock(base)
	if err != nil {
		t.Fatalf("CheckLock() error = %v", err)
	}
	if len(missing) != 0 || len(changed) != 0 {
		t.Fatalf("clean tree reported drift: missing=%v changed=%v", missing, changed)
	}
	if len(unlisted) != 0 {
		t.Fatalf("clean tree reported unlisted files: %v", unlisted)
	}
	if len(stale) != 0 {
		t.Fatalf("clean tree reported stale files: %v", stale)
	}
}

// TestCheckLockDetectsDrift verifies missing, modified, and locally added
// files are each classified correctly.
func TestCheckLockDetectsDrift(t *testing.T) {
	base := t.TempDir()
	if _, err := Materialize(base); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if err := WriteLock(base); err != nil {
		t.Fatalf("WriteLock() error = %v", err)
	}
	gone := filepath.Join(base, "skills", "adr", "SKILL.md")
	if err := os.Remove(gone); err != nil {
		t.Fatalf("removing asset: %v", err)
	}
	drifted := filepath.Join(base, "workflows", "normal.yaml")
	if err := os.WriteFile(drifted, []byte("name: drifted\n"), 0o644); err != nil {
		t.Fatalf("modifying asset: %v", err)
	}
	extra := filepath.Join(base, "skills", "local-skill")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatalf("creating extension dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extra, "skill.yaml"), []byte("name: local\n"), 0o644); err != nil {
		t.Fatalf("writing extension file: %v", err)
	}
	missing, changed, unlisted, stale, err := CheckLock(base)
	if err != nil {
		t.Fatalf("CheckLock() error = %v", err)
	}
	if len(missing) != 1 || missing[0] != "skills/adr/SKILL.md" {
		t.Fatalf("missing = %v", missing)
	}
	if len(changed) != 1 || changed[0] != "workflows/normal.yaml" {
		t.Fatalf("changed = %v", changed)
	}
	if len(unlisted) != 1 || unlisted[0] != "skills/local-skill/skill.yaml" {
		t.Fatalf("unlisted = %v", unlisted)
	}
	if len(stale) != 0 {
		t.Fatalf("stale = %v", stale)
	}
}

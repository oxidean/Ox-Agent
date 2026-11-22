package project

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// indexWarnings returns the report's warnings mentioning the context index.
func indexWarnings(report *DoctorReport) []string {
	var out []string
	for _, w := range report.Warnings {
		if strings.Contains(w, "index") {
			out = append(out, w)
		}
	}
	return out
}

// writeTestIndex writes a context/index.yaml covering the given files with
// their real hashes.
func writeTestIndex(t *testing.T, root string, files map[string]string) {
	t.Helper()
	base := Base(root)
	var doc struct {
		SchemaVersion int    `yaml:"schema_version"`
		GeneratedAt   string `yaml:"generated_at"`
		Files         []struct {
			Path   string `yaml:"path"`
			SHA256 string `yaml:"sha256"`
			Bytes  int    `yaml:"bytes"`
			Kind   string `yaml:"kind"`
		} `yaml:"files"`
	}
	doc.SchemaVersion = 1
	doc.GeneratedAt = "2026-01-01T00:00:00Z"
	for path, content := range files {
		sum := sha256.Sum256([]byte(content))
		doc.Files = append(doc.Files, struct {
			Path   string `yaml:"path"`
			SHA256 string `yaml:"sha256"`
			Bytes  int    `yaml:"bytes"`
			Kind   string `yaml:"kind"`
		}{Path: path, SHA256: hex.EncodeToString(sum[:]), Bytes: len(content), Kind: "other"})
	}
	data, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "context", "index.yaml"), data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
}

// TestDoctorIndexMissingWarns verifies an absent index with indexable
// files produces a warning — the seeded project files are indexable, so
// this is the fresh-scaffold state until init seeds the index.
func TestDoctorIndexMissingWarns(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	report := Doctor(root)
	if warnings := indexWarnings(report); len(warnings) == 0 {
		t.Fatal("no missing-index warning on seeded scaffold")
	}
}

// TestDoctorIndexStaleWarns verifies a file changed since the index was
// written is reported as drift.
func TestDoctorIndexStaleWarns(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	writeTestIndex(t, root, map[string]string{
		"context/specs/README.md": "stale content",
	})
	report := Doctor(root)
	warnings := indexWarnings(report)
	if len(warnings) == 0 || !strings.Contains(warnings[0], "stale") {
		t.Fatalf("index warnings = %v", warnings)
	}
}

// TestDoctorIndexFreshSilent verifies an accurate index produces no
// index warnings.
func TestDoctorIndexFreshSilent(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	readme, err := os.ReadFile(filepath.Join(base, "context", "specs", "README.md"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	projectYAML, err := os.ReadFile(filepath.Join(base, "context", "project.yaml"))
	if err != nil {
		t.Fatalf("read project.yaml: %v", err)
	}
	writeTestIndex(t, root, map[string]string{
		"context/specs/README.md": string(readme),
		"context/project.yaml":    string(projectYAML),
	})
	report := Doctor(root)
	if warnings := indexWarnings(report); len(warnings) != 0 {
		t.Fatalf("fresh index still warned: %v", warnings)
	}
}

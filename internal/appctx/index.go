// Package appctx builds the project context index and compiles work-item
// packets — the bounded context contracts handed to skills.
package appctx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// IndexEntry describes one context file in the index.
type IndexEntry struct {
	Path   string `yaml:"path" json:"path"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	Bytes  int64  `yaml:"bytes" json:"bytes"`
	Kind   string `yaml:"kind" json:"kind"`
}

// Index mirrors context/index.yaml: the catalog of loadable project context.
type Index struct {
	SchemaVersion int          `yaml:"schema_version" json:"schema_version"`
	GeneratedAt   string       `yaml:"generated_at" json:"generated_at"`
	Files         []IndexEntry `yaml:"files" json:"files"`
}

// indexRoots are the .ox-agent subtrees the index catalogs.
var indexRoots = []string{"context", "discussions"}

// indexPath returns the context index file path under root.
func indexPath(root string) string {
	return filepath.Join(root, ".ox-agent", "context", "index.yaml")
}

// kindOf classifies an indexed path into its context kind.
func kindOf(rel string) string {
	switch {
	case rel == "context/project.yaml":
		return "project"
	case filepath.Dir(rel) == "context/standards" || hasPrefix(rel, "context/standards/"):
		return "standard"
	case hasPrefix(rel, "context/decisions/"):
		return "decision"
	case hasPrefix(rel, "context/specs/"):
		return "spec"
	case hasPrefix(rel, "discussions/"):
		return "discussion"
	default:
		return "other"
	}
}

// hasPrefix is filepath.ToSlash-aware strings.HasPrefix.
func hasPrefix(path, prefix string) bool {
	return len(path) > len(prefix) && path[:len(prefix)] == prefix
}

// Build scans the context roots under root and returns the index.
func Build(root string) (*Index, error) {
	base := filepath.Join(root, ".ox-agent")
	index := &Index{SchemaVersion: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, dir := range indexRoots {
		rootDir := filepath.Join(base, dir)
		err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == "context/index.yaml" {
				return nil
			}
			// Dot-prefixed paths are sentinels and OS noise, not context:
			// indexing a .gitkeep (or anything under a hidden directory)
			// would hand skills a placeholder as a standard or a thread.
			if strings.Contains("/"+rel, "/.") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			index.Files = append(index.Files, IndexEntry{
				Path:   rel,
				SHA256: hex.EncodeToString(sum[:]),
				Bytes:  info.Size(),
				Kind:   kindOf(rel),
			})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Slice(index.Files, func(i, j int) bool { return index.Files[i].Path < index.Files[j].Path })
	return index, nil
}

// Write persists the index to context/index.yaml and returns the delta
// against the previous index (added, changed, removed counts).
func Write(root string, index *Index) (added, changed, removed int, err error) {
	previous, loadErr := Load(root)
	data, err := yaml.Marshal(index)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("encoding context index: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(indexPath(root)), 0o755); err != nil {
		return 0, 0, 0, err
	}
	if err := os.WriteFile(indexPath(root), data, 0o644); err != nil {
		return 0, 0, 0, fmt.Errorf("writing context index: %w", err)
	}
	if loadErr != nil {
		return len(index.Files), 0, 0, nil
	}
	old := map[string]IndexEntry{}
	for _, f := range previous.Files {
		old[f.Path] = f
	}
	seen := map[string]bool{}
	for _, f := range index.Files {
		seen[f.Path] = true
		if o, ok := old[f.Path]; !ok {
			added++
		} else if o.SHA256 != f.SHA256 {
			changed++
		}
	}
	for path := range old {
		if !seen[path] {
			removed++
		}
	}
	return added, changed, removed, nil
}

// Load reads context/index.yaml under root.
func Load(root string) (*Index, error) {
	data, err := os.ReadFile(indexPath(root))
	if err != nil {
		return nil, err
	}
	var index Index
	if err := yaml.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parsing context index: %w", err)
	}
	return &index, nil
}

// Entries returns the index entries of one kind; loading the index fresh
// when it does not exist.
func Entries(root, kind string) ([]IndexEntry, error) {
	index, err := Load(root)
	if err != nil {
		index, err = Build(root)
		if err != nil {
			return nil, err
		}
	}
	var out []IndexEntry
	for _, f := range index.Files {
		// Stored indexes written before dotfile filtering can still carry
		// sentinel entries — filter on read too, not only at Build time.
		// ToSlash keeps the rule working on hand-edited or foreign-written
		// indexes that use backslash separators.
		p := filepath.ToSlash(f.Path)
		if f.Kind == kind && p != "context/index.yaml" && !strings.Contains("/"+p, "/.") {
			out = append(out, f)
		}
	}
	return out, nil
}

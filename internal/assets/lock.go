package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LockPath is the .ox-agent file recording materialized asset hashes.
const LockPath = "assets.lock"

// Lock records the sha256 digest of every materialized asset file so doctor
// can detect missing, modified, or locally added files.
type Lock struct {
	SchemaVersion int               `json:"schema_version"`
	Files         map[string]string `json:"files"`
}

// WriteLock hashes every embedded asset and writes the lock file under base.
// Hashes are taken from the embedded tree, the source of truth for assets.
func WriteLock(base string) error {
	files, err := List("")
	if err != nil {
		return err
	}
	lock := Lock{SchemaVersion: 1, Files: make(map[string]string, len(files))}
	for _, rel := range files {
		data, err := ReadFile(rel)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		lock.Files[rel] = "sha256:" + hex.EncodeToString(sum[:])
	}
	out, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding asset lock: %w", err)
	}
	if err := os.WriteFile(filepath.Join(base, LockPath), append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing asset lock: %w", err)
	}
	return nil
}

// LoadLock reads the asset lock file under base.
func LoadLock(base string) (*Lock, error) {
	data, err := os.ReadFile(filepath.Join(base, LockPath))
	if err != nil {
		return nil, err
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing asset lock: %w", err)
	}
	// Drop keys that could escape base when joined — a tampered lock must
	// not steer reads, stats, or removals outside .ox-agent. WriteLock only
	// ever records local embedded paths, so nothing legitimate is lost.
	for rel := range lock.Files {
		if !filepath.IsLocal(rel) {
			delete(lock.Files, rel)
		}
	}
	return &lock, nil
}

// LockedTarget joins a lock key under base and reports whether the resolved
// path stays inside resolvedBase (EvalSymlinks(base)). IsLocal cannot see
// symlinks, so callers must use this before reading, stating, or removing a
// lock-derived path. Non-local keys and paths that do not exist or resolve
// outside the project are rejected.
func LockedTarget(resolvedBase, base, rel string) (string, bool) {
	if !filepath.IsLocal(rel) {
		return "", false
	}
	target := filepath.Join(base, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", false
	}
	if resolved != resolvedBase && !strings.HasPrefix(resolved, resolvedBase+string(os.PathSeparator)) {
		return "", false
	}
	return target, true
}

// CheckLock compares the lock under base with the files on disk. missing are
// locked files absent on disk; changed are locked files whose digest differs;
// unlisted are on-disk files in asset directories that the lock does not
// cover (extension assets added locally — informational, not a failure);
// stale are locked files still on disk that the running binary's embedded
// tree no longer ships (previously owned — 'init --repair --sync' removes
// them when untouched).
func CheckLock(base string) (missing, changed, unlisted, stale []string, err error) {
	lock, err := LoadLock(base)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("resolving %s: %w", base, err)
	}
	for rel, want := range lock.Files {
		target, ok := LockedTarget(resolvedBase, base, rel)
		if !ok {
			// Absent, non-local, or escaping — the owned file is not
			// verifiably present inside the project.
			missing = append(missing, rel)
			continue
		}
		data, err := os.ReadFile(target)
		if err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, rel)
				continue
			}
			return nil, nil, nil, nil, err
		}
		sum := sha256.Sum256(data)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != want {
			changed = append(changed, rel)
		}
	}
	embedded, err := List("")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ships := make(map[string]bool, len(embedded))
	for _, rel := range embedded {
		ships[rel] = true
	}
	for rel := range lock.Files {
		if ships[rel] {
			continue
		}
		if _, ok := LockedTarget(resolvedBase, base, rel); ok {
			stale = append(stale, rel)
		}
	}
	dirs := []string{"skills", "schemas", "workflows", "pipelines", "lifecycle", "integrations"}
	for _, dir := range dirs {
		root := filepath.Join(base, dir)
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return nil
			}
			if _, ok := lock.Files[filepath.ToSlash(rel)]; !ok {
				unlisted = append(unlisted, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	return missing, changed, unlisted, stale, nil
}

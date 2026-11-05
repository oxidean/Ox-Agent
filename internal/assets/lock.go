package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	return &lock, nil
}

// CheckLock compares the lock under base with the files on disk. missing are
// locked files absent on disk; changed are locked files whose digest differs;
// unlisted are on-disk files in asset directories that the lock does not
// cover (extension assets added locally — informational, not a failure).
func CheckLock(base string) (missing, changed, unlisted []string, err error) {
	lock, err := LoadLock(base)
	if err != nil {
		return nil, nil, nil, err
	}
	for rel, want := range lock.Files {
		data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, rel)
				continue
			}
			return nil, nil, nil, err
		}
		sum := sha256.Sum256(data)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != want {
			changed = append(changed, rel)
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
	return missing, changed, unlisted, nil
}

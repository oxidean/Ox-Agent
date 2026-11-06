// remote_lock.go records provenance for remote-installed skills: a git
// source pinned to a resolved commit SHA plus a sha256 digest of every
// installed file, so integrity checks verify what was reviewed is what runs.
package assets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RemoteLockPath is the .ox-agent file recording remote skill provenance.
const RemoteLockPath = "remote-skills.lock"

// RemoteLock is the provenance manifest for remote-installed skills.
type RemoteLock struct {
	SchemaVersion int                     `json:"schema_version"`
	Skills        map[string]*RemoteSkill `json:"skills"`
}

// RemoteSkill records one remote-installed skill's provenance and file
// digests. Files keys are base-relative slash paths (skills/<dir>/<rel>)
// matching the asset lock's addressing.
type RemoteSkill struct {
	Dir         string            `json:"-"`    // materialized directory name (the lock map key)
	Name        string            `json:"name"` // manifest name, e.g. acme/reviewer
	Source      string            `json:"source"`
	Path        string            `json:"path,omitempty"`
	Ref         string            `json:"ref,omitempty"`
	SHA         string            `json:"sha"`
	InstalledAt string            `json:"installed_at"`
	Files       map[string]string `json:"files"`
}

// LoadRemoteLock reads the remote-skills lock under base, returning an
// empty lock when none exists.
func LoadRemoteLock(base string) (*RemoteLock, error) {
	data, err := os.ReadFile(filepath.Join(base, RemoteLockPath))
	if err != nil {
		if os.IsNotExist(err) {
			return &RemoteLock{SchemaVersion: 1, Skills: map[string]*RemoteSkill{}}, nil
		}
		return nil, err
	}
	var lock RemoteLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", RemoteLockPath, err)
	}
	if lock.Skills == nil {
		lock.Skills = map[string]*RemoteSkill{}
	}
	for dir, entry := range lock.Skills {
		entry.Dir = dir
	}
	return &lock, nil
}

// WriteRemoteLock persists the remote-skills lock under base.
func WriteRemoteLock(base string, lock *RemoteLock) error {
	lock.SchemaVersion = 1
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, RemoteLockPath), append(data, '\n'), 0o644)
}

// RemoteFiles returns the union of base-relative paths owned by remote
// skills, for excluding them from unlisted-asset reports.
func RemoteFiles(base string) map[string]bool {
	out := map[string]bool{}
	lock, err := LoadRemoteLock(base)
	if err != nil {
		return out
	}
	for _, entry := range lock.Skills {
		for rel := range entry.Files {
			out[rel] = true
		}
	}
	return out
}

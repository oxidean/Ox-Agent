// remote.go implements skills install/uninstall for remote sources. A
// remote skill is a git checkout pinned to a resolved commit SHA: the lock
// at .ox-agent/remote-skills.lock records provenance (source, requested
// ref, resolved SHA) plus a sha256 digest of every installed file, so
// doctor and skills check verify what was reviewed is what runs.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/oxidean/Ox-Agent/internal/contract"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"gopkg.in/yaml.v3"
)

// RemoteSkill aliases the assets provenance record for callers in this
// package.
type RemoteSkill = assets.RemoteSkill

// RemoteLockPath re-exports the provenance lock filename.
const RemoteLockPath = assets.RemoteLockPath

// InstallOptions parameterizes Install.
type InstallOptions struct {
	Ref   string // branch, tag, or commit to check out (default: remote HEAD)
	Path  string // subdirectory of the source repository containing the skill package
	Name  string // directory name override (default: manifest name's last segment)
	Force bool   // replace an existing remote install of the same directory
}

// RemoteOf returns the provenance entry for a skill directory, or nil.
func RemoteOf(root, dir string) *RemoteSkill {
	lock, err := assets.LoadRemoteLock(project.Base(root))
	if err != nil {
		return nil
	}
	return lock.Skills[dir]
}

// dirName matches the directory namespace used by materialized skills.
var dirName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Install fetches a skill package from a git source, pins it to a resolved
// commit SHA, and materializes it under .ox-agent/skills/<name> with a
// provenance entry in remote-skills.lock. Embedded (ox-agent-owned) skill
// directories can never be overwritten; an existing directory is replaced
// only when the lock already owns it and force is set.
func Install(root, source string, opts InstallOptions) (*RemoteSkill, error) {
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("skills install requires a git source URL")
	}
	base := project.Base(root)
	tmp, err := os.MkdirTemp("", "ox-agent-skill-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cloneDir := filepath.Join(tmp, "src")
	if err := gitClone(source, opts.Ref, cloneDir); err != nil {
		return nil, err
	}
	sha, err := gitOutput(cloneDir, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolving source commit: %w", err)
	}
	pkgDir := cloneDir
	if opts.Path != "" {
		pkgDir = filepath.Join(cloneDir, filepath.FromSlash(opts.Path))
		// The package path must stay inside the checkout — neither a
		// lexical escape (../..) nor a symlinked component may resolve
		// outside it.
		resolvedClone, err := filepath.EvalSymlinks(cloneDir)
		if err != nil {
			return nil, err
		}
		resolvedPkg, err := filepath.EvalSymlinks(pkgDir)
		if err != nil {
			return nil, fmt.Errorf("package path %q not found in source: %w", opts.Path, err)
		}
		if resolvedPkg != resolvedClone && !strings.HasPrefix(resolvedPkg, resolvedClone+string(os.PathSeparator)) {
			return nil, fmt.Errorf("package path %q escapes the source repository", opts.Path)
		}
		pkgDir = resolvedPkg
	}
	manifestData, err := os.ReadFile(filepath.Join(pkgDir, "skill.yaml"))
	if err != nil {
		return nil, fmt.Errorf("source package has no skill.yaml at %q", opts.Path)
	}
	var m manifest
	if err := yaml.Unmarshal(manifestData, &m); err != nil {
		return nil, fmt.Errorf("parsing source skill.yaml: %w", err)
	}
	// A skill that fails its own manifest contract never materializes.
	if err := contract.ValidateYAML("skill-manifest", manifestData); err != nil {
		return nil, fmt.Errorf("source skill.yaml fails the manifest contract: %w", err)
	}
	if _, err := os.Stat(filepath.Join(pkgDir, "SKILL.md")); err != nil {
		return nil, fmt.Errorf("source package has no SKILL.md at %q", opts.Path)
	}
	dir := opts.Name
	if dir == "" {
		dir = NormalizeName(m.Name)
	}
	if !dirName.MatchString(dir) {
		return nil, fmt.Errorf("invalid skill directory name %q (want [a-z0-9-])", dir)
	}
	emb, err := embeddedDirs()
	if err != nil {
		return nil, err
	}
	if _, owned := emb[dir]; owned {
		return nil, fmt.Errorf("%q is an ox-agent-owned skill directory; remote installs cannot shadow it", dir)
	}
	lock, err := assets.LoadRemoteLock(base)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(base, "skills", dir)
	if _, err := os.Stat(target); err == nil {
		if _, remote := lock.Skills[dir]; !remote {
			return nil, fmt.Errorf("%s already exists and is not remote-installed; remove it manually to replace", target)
		}
		if !opts.Force {
			return nil, fmt.Errorf("remote skill %q is already installed at %s; pass --force to re-pin", dir, lock.Skills[dir].SHA)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// Stage into .ox-agent so the final rename is same-filesystem.
	stage := filepath.Join(base, "skills", ".install-"+dir)
	if err := os.RemoveAll(stage); err != nil {
		return nil, err
	}
	if err := copyPackage(pkgDir, stage); err != nil {
		os.RemoveAll(stage)
		return nil, fmt.Errorf("staging skill package: %w", err)
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	if err := project.ConfinedTarget(resolvedBase, target); err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	if err := os.RemoveAll(target); err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	if err := os.Rename(stage, target); err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	files, err := hashTree(base, target)
	if err != nil {
		return nil, err
	}
	entry := &RemoteSkill{
		Dir:         dir,
		Name:        m.Name,
		Source:      source,
		Path:        opts.Path,
		Ref:         opts.Ref,
		SHA:         sha,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Files:       files,
	}
	lock.Skills[dir] = entry
	if err := assets.WriteRemoteLock(base, lock); err != nil {
		return nil, err
	}
	if err := state.Append(root, state.Event{
		Type:  "skills.installed",
		Actor: "cli",
		Data:  map[string]any{"name": entry.Name, "dir": dir, "source": source, "ref": opts.Ref, "sha": sha},
	}); err != nil {
		return nil, err
	}
	return entry, nil
}

// Uninstall removes a remote-installed skill directory and its lock entry.
// Embedded and project-local skills are refused.
func Uninstall(root, dir string) error {
	dir = NormalizeName(dir)
	base := project.Base(root)
	lock, err := assets.LoadRemoteLock(base)
	if err != nil {
		return err
	}
	entry, remote := lock.Skills[dir]
	if !remote {
		return fmt.Errorf("%q is not remote-installed; only skills recorded in %s can be uninstalled", dir, RemoteLockPath)
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return err
	}
	target := filepath.Join(base, "skills", dir)
	if err := project.ConfinedTarget(resolvedBase, target); err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	delete(lock.Skills, dir)
	if err := assets.WriteRemoteLock(base, lock); err != nil {
		return err
	}
	return state.Append(root, state.Event{
		Type:  "skills.uninstalled",
		Actor: "cli",
		Data:  map[string]any{"name": entry.Name, "dir": dir, "source": entry.Source},
	})
}

// CheckRemote verifies every remote-installed skill's files against its
// locked digests: missing files and modified files are findings, extra
// files are informational.
func CheckRemote(root string) ([]Finding, error) {
	base := project.Base(root)
	lock, err := assets.LoadRemoteLock(base)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, dir := range sortedKeys(lock.Skills) {
		entry := lock.Skills[dir]
		onDisk, err := hashTree(base, filepath.Join(base, "skills", dir))
		switch {
		case os.IsNotExist(err):
			findings = append(findings, Finding{Skill: dir, Level: "error",
				Message: fmt.Sprintf("remote skill missing on disk (installed from %s@%s)", entry.Source, short(entry.SHA))})
			continue
		case err != nil:
			return nil, err
		}
		var changed, missing []string
		for rel, want := range entry.Files {
			got, ok := onDisk[rel]
			switch {
			case !ok:
				missing = append(missing, rel)
			case got != want:
				changed = append(changed, rel)
			}
		}
		var extra []string
		for rel := range onDisk {
			if _, ok := entry.Files[rel]; !ok {
				extra = append(extra, rel)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			findings = append(findings, Finding{Skill: dir, Level: "error",
				Message: fmt.Sprintf("remote skill files missing: %v", missing)})
		}
		if len(changed) > 0 {
			sort.Strings(changed)
			findings = append(findings, Finding{Skill: dir, Level: "warning",
				Message: fmt.Sprintf("remote skill files drifted from pinned %s: %v", short(entry.SHA), changed)})
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			findings = append(findings, Finding{Skill: dir, Level: "warning",
				Message: fmt.Sprintf("untracked files in remote skill: %v", extra)})
		}
	}
	return findings, nil
}

// gitClone clones source into dir. With no ref it takes a shallow clone of
// the remote HEAD; with a ref it first tries a shallow branch/tag clone,
// falling back to a full clone + checkout so commit SHAs resolve too.
func gitClone(source, ref, dir string) error {
	if ref == "" {
		return gitRun("", "clone", "--depth", "1", source, dir)
	}
	if err := gitRun("", "clone", "--depth", "1", "--branch", ref, source, dir); err == nil {
		return nil
	}
	if err := gitRun("", "clone", source, dir); err != nil {
		return err
	}
	return gitRun(dir, "checkout", ref)
}

// gitRun executes a git command, surfacing stderr on failure.
func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitOutput executes a git command and returns trimmed stdout.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// copyPackage copies a skill package tree, dereferencing symlinks that
// resolve inside the package and rejecting any that escape it.
func copyPackage(src, dst string) error {
	resolvedSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	return filepath.WalkDir(resolvedSrc, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(resolvedSrc, path)
		if err != nil {
			return err
		}
		// VCS metadata is never part of the skill package.
		if entry.IsDir() && (rel == ".git" || rel == ".hg" || rel == ".svn") {
			return fs.SkipDir
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if resolved != resolvedSrc && !strings.HasPrefix(resolved, resolvedSrc+string(os.PathSeparator)) {
			return fmt.Errorf("%s is a symlink escaping the package", rel)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// hashTree records sha256 digests of every file under dir as base-relative
// slash paths, matching the asset lock's key convention.
func hashTree(base, dir string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files[filepath.ToSlash(rel)] = "sha256:" + hex.EncodeToString(sum[:])
		return nil
	})
	return files, err
}

// sortedKeys returns map keys in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// short trims a SHA for display.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

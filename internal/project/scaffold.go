package project

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/oxidean/Ox-Agent/internal/buildinfo"
	"github.com/oxidean/Ox-Agent/internal/contract"
	"golang.org/x/mod/semver"
)

// defaultStages, defaultSkills, and defaultStageSkills seed a new project's
// config.yaml with the standard lifecycle profile and bundled skill set.
var defaultStages = []string{"discovery", "brainstorming", "specification", "planning", "implementation", "verification", "shipping", "retrospective"}

var defaultSkills = []string{
	"discover-context", "brainstorm", "specify", "adr", "multi-stage-plan",
	"tdd", "implementation", "code-review", "verify", "discussion",
	"progress", "shipping", "retrospective",
}

var defaultStageSkills = map[string][]string{
	"discovery":      {"discover-context"},
	"brainstorming":  {"brainstorm", "adr"},
	"specification":  {"specify", "adr"},
	"planning":       {"multi-stage-plan"},
	"implementation": {"tdd", "implementation"},
	"verification":   {"code-review", "verify"},
	"shipping":       {"shipping"},
	"retrospective":  {"retrospective", "progress"},
}

// renameScaffold publishes a fully staged scaffold; a var so tests can inject failures.
var renameScaffold = os.Rename

// fallbackRequiredVersion pins the binary range a scaffold declares when the
// running binary's version cannot be parsed (dev builds).
const fallbackRequiredVersion = ">=0.1.0 <0.2.0"

// requiredVersionRange derives the >=X.Y.0 <X.(Y+1).0 compatibility range a
// fresh scaffold pins from the running binary's minor line. The pin moves
// with the binary so scaffolds never declare a range the toolchain fails.
func requiredVersionRange() string {
	v := "v" + strings.TrimPrefix(buildinfo.Version, "v")
	if !semver.IsValid(v) {
		return fallbackRequiredVersion
	}
	mm := strings.TrimPrefix(semver.MajorMinor(v), "v")
	parts := strings.SplitN(mm, ".", 2)
	if len(parts) != 2 {
		return fallbackRequiredVersion
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return fallbackRequiredVersion
	}
	floor := fmt.Sprintf("%s.%d.0", parts[0], minor)
	if semver.Prerelease(v) != "" {
		// A prerelease binary predates its minor's .0 release; floor the
		// range at the running version so it satisfies its own pin.
		floor = strings.TrimPrefix(v, "v")
	}
	return fmt.Sprintf(">=%s <%s.%d.0", floor, parts[0], minor+1)
}

// scaffoldDirs lists the directories every project scaffold provides.
var scaffoldDirs = []string{
	"skills", "work/active", "work/archived", "context/standards",
	"context/decisions", "lifecycle", "workflows", "pipelines",
	"discussions", "state", "state/snapshots", "integrations",
	"runtime/packets", "runtime/runs", "runtime/cache", "runtime/locks",
}

// Init materializes a new .ox-agent project scaffold at dir.
// AlreadyInitializedError reports that .ox-agent/ already exists.
type AlreadyInitializedError struct {
	Path string
}

// Error implements error.
func (e *AlreadyInitializedError) Error() string {
	return fmt.Sprintf("%s already exists; refusing to overwrite it", e.Path)
}

// Is allows errors.Is(err, ErrAlreadyInitialized) checks.
func (e *AlreadyInitializedError) Is(target error) bool {
	return target == ErrAlreadyInitialized
}

// ErrAlreadyInitialized matches any AlreadyInitializedError.
var ErrAlreadyInitialized = &AlreadyInitializedError{}

func Init(dir string) (err error) {
	finalBase := Base(dir)
	if _, err := os.Stat(finalBase); err == nil {
		return fmt.Errorf("%w", &AlreadyInitializedError{Path: finalBase})
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := newStagingDir(dir)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staging)
		}
	}()
	base := staging
	for _, dir := range scaffoldDirs {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if _, err := assets.Materialize(base); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving project root: %w", err)
	}
	project := filepath.Base(absRoot)
	framework := FrameworkConfig{SchemaVersion: 1}
	framework.Framework.Name = "ox-agent"
	framework.Framework.Distribution = "standalone"
	framework.Framework.Binary = "ox-agent"
	framework.Framework.RequiredVersion = requiredVersionRange()
	framework.Project.Name = project
	if err := writeYAML(filepath.Join(base, "framework.yaml"), framework); err != nil {
		return err
	}
	settings := Settings{SchemaVersion: 1}
	settings.Project.Name = project
	settings.Workflow.DefaultProfile = "normal"
	settings.Workflow.Profiles = map[string][]string{"normal": defaultStages}
	settings.Skills.Enabled = defaultSkills
	settings.Skills.StageSkills = defaultStageSkills
	if err := writeYAML(filepath.Join(base, "config.yaml"), settings); err != nil {
		return err
	}
	policy := Policy{SchemaVersion: 1}
	policy.Autonomy.ProfileChanges = "managed"
	policy.Autonomy.AllowDowngrade = false
	policy.Approvals.ProductionDeploy = "human"
	policy.Approvals.DestructiveChange = "human"
	policy.Approvals.PolicyChange = "human"
	policy.Budgets.MaxParallelAgents = 4
	policy.Budgets.MaxChildDepth = 1
	policy.Budgets.MaxFixCycles = 3
	if err := writeYAML(filepath.Join(base, "policy.yaml"), policy); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(base, "state", "events.ndjson"), nil, 0o644); err != nil {
		return fmt.Errorf("creating event journal: %w", err)
	}
	if err := writeSeeds(base, project); err != nil {
		return err
	}
	if err := renameScaffold(base, finalBase); err != nil {
		return fmt.Errorf("publishing project scaffold: %w", err)
	}
	return nil
}

// newStagingDir creates a uniquely named staging directory under root with
// mode 0755 subject to the process umask, retrying on name collisions.
func newStagingDir(root string) (string, error) {
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(root, fmt.Sprintf(".ox-agent.tmp-%08x", rand.Uint32()))
		if err := os.Mkdir(candidate, 0o755); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", fmt.Errorf("creating temporary scaffold: %w", err)
		}
		return candidate, nil
	}
	return "", errors.New("creating temporary scaffold: no unique name available")
}

// writeSeeds writes generated project files that are not static assets:
// the .gitignore, the asset lock, and the project context file.
func writeSeeds(base, project string) error {
	gitignore := "# Generated ox-agent runtime state; do not commit.\nruntime/\n"
	if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		return fmt.Errorf("writing .gitignore: %w", err)
	}
	gitattributes := "# The append-only event journal merges cleanly across branches with the union driver.\nstate/events.ndjson merge=union\n"
	if err := os.WriteFile(filepath.Join(base, ".gitattributes"), []byte(gitattributes), 0o644); err != nil {
		return fmt.Errorf("writing .gitattributes: %w", err)
	}
	if err := assets.WriteLock(base); err != nil {
		return err
	}
	ctx := struct {
		SchemaVersion int      `yaml:"schema_version"`
		Name          string   `yaml:"name"`
		Standards     []string `yaml:"standards"`
	}{SchemaVersion: 1, Name: project, Standards: []string{}}
	if err := writeYAML(filepath.Join(base, "context", "project.yaml"), ctx); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(base, "README.md"), []byte(scaffoldReadme), 0o644); err != nil {
		return fmt.Errorf("writing README.md: %w", err)
	}
	return nil
}

// scaffoldReadme orients humans and agents landing in .ox-agent without
// harness integration — it is the in-tree pointer a bare init leaves behind.
const scaffoldReadme = `# .ox-agent — project state managed by ox-agent

This directory is the project-local AI-SDLC runtime for ` + "`ox-agent`" + `. Work items,
skills, contracts, and the event journal live here.

- ` + "`framework.yaml`" + `, ` + "`config.yaml`" + `, ` + "`policy.yaml`" + ` — the project's contract files; these are yours to edit.
- ` + "`skills/`" + `, ` + "`schemas/`" + `, ` + "`workflows/`" + `, ` + "`pipelines/`" + `, ` + "`lifecycle/`" + `, ` + "`integrations/`" + ` —
  materialized assets tracked by ` + "`assets.lock`" + `; do not edit in place. Add
  extension skills as new directories under ` + "`skills/`" + `.
- ` + "`work/`" + `, ` + "`state/`" + `, ` + "`discussions/`" + ` — live project state; committed so context
  travels through PRs. ` + "`state/events.ndjson`" + ` is the append-only journal.
- ` + "`runtime/`" + ` — generated packets and runs; gitignored.

` + "`ox-agent doctor`" + ` validates this setup. ` + "`ox-agent integrate`" + ` wires it into
agent harnesses (a managed ` + "`AGENTS.md`" + ` block plus ` + "`/.agents/skills`" + ` and
` + "`/.claude/skills`" + ` links) so the materialized skills are discoverable during
agentic work — without it, nothing outside this directory advertises the toolkit.
`

// ConfinedTarget verifies that writing target stays inside resolvedBase: a
// dangling symlink at target itself is removed rather than written through,
// and the deepest existing ancestor of target's directory must resolve under
// resolvedBase. Repair and skills-update writes must pass through this so a
// symlink planted inside .ox-agent cannot redirect writes (or MkdirAll)
// outside the project. Callers that preserve existing files must Stat-skip
// before confining so valid symlinks are never reached here.
func ConfinedTarget(resolvedBase, target string) error {
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("removing symlinked path %s: %w", target, err)
		}
	}
	ancestor := filepath.Dir(target)
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("no existing ancestor for %s", target)
		}
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", ancestor, err)
	}
	if resolved != resolvedBase && !strings.HasPrefix(resolved, resolvedBase+string(os.PathSeparator)) {
		return fmt.Errorf("%s resolves outside %s; refusing to write", target, resolvedBase)
	}
	return nil
}

// RepairReport summarizes what Repair restored in an existing .ox-agent.
type RepairReport struct {
	DirsCreated   []string
	FilesRestored []string
	FilesSynced   []string // drifted asset files rewritten (--sync)
	FilesRemoved  []string // owned files the binary no longer ships (--sync)
	SeedsWritten  []string
	PinUpdated    string // framework.yaml required_version range rewritten to (--sync)
}

// Repair restores missing scaffold directories, missing materialized asset
// files, and missing generated seed files in an existing project. With sync
// false it never overwrites files that already exist; doctor reports drifted
// content. With sync true it reconciles the project against the running
// binary: drifted asset files are rewritten, files the binary no longer
// ships are removed when untouched locally, and a required_version pin that
// excludes the running binary is reset to the binary's derived range.
func Repair(root string, sync bool) (*RepairReport, error) {
	base := Base(root)
	if _, err := os.Stat(base); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s does not exist; run init without --repair", base)
		}
		return nil, err
	}
	report := &RepairReport{}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", base, err)
	}
	// Snapshot the previous lock before any writes so files this binary no
	// longer ships can be identified as stale owned files. A missing or
	// corrupt lock leaves no provenance baseline — stale detection skips.
	var oldLock *assets.Lock
	if sync {
		if lock, err := assets.LoadLock(base); err == nil {
			oldLock = lock
		}
	}
	for _, dir := range scaffoldDirs {
		path := filepath.Join(base, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// A missing path may sit under a symlinked ancestor (or be a
			// dangling symlink); confine before MkdirAll so directory
			// creation cannot escape the project.
			if err := ConfinedTarget(resolvedBase, path); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return nil, fmt.Errorf("creating %s: %w", dir, err)
			}
			report.DirsCreated = append(report.DirsCreated, dir)
		} else if err != nil {
			return nil, err
		}
	}
	files, err := assets.List("")
	if err != nil {
		return nil, err
	}
	embedded := make(map[string]bool, len(files))
	for _, rel := range files {
		embedded[rel] = true
	}
	for _, rel := range files {
		target := filepath.Join(base, filepath.FromSlash(rel))
		data, err := assets.ReadFile(rel)
		if err != nil {
			return nil, err
		}
		disk, err := os.ReadFile(target)
		switch {
		case err == nil:
			if !sync || bytes.Equal(disk, data) {
				continue
			}
			if err := ConfinedTarget(resolvedBase, target); err != nil {
				return nil, err
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return nil, err
			}
			report.FilesSynced = append(report.FilesSynced, rel)
		case !os.IsNotExist(err):
			return nil, err
		default:
			if err := ConfinedTarget(resolvedBase, target); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return nil, err
			}
			report.FilesRestored = append(report.FilesRestored, rel)
		}
	}
	// Remove files the old lock owned that this binary no longer ships —
	// only while the on-disk copy still matches the old lock hash. Locally
	// modified stale files are kept and surface as unlisted extensions.
	if oldLock != nil {
		for rel, wantHash := range oldLock.Files {
			if embedded[rel] {
				continue
			}
			target := filepath.Join(base, filepath.FromSlash(rel))
			disk, err := os.ReadFile(target)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			sum := sha256.Sum256(disk)
			if "sha256:"+hex.EncodeToString(sum[:]) != wantHash {
				continue
			}
			if err := ConfinedTarget(resolvedBase, target); err != nil {
				return nil, err
			}
			if err := os.Remove(target); err != nil {
				return nil, err
			}
			report.FilesRemoved = append(report.FilesRemoved, rel)
		}
		sort.Strings(report.FilesRemoved)
	}
	// Reconcile the required_version pin with the running binary: a pin
	// that excludes this binary fails doctor's version gate outright.
	if sync {
		if framework, err := LoadFramework(root); err == nil {
			ok, serr := contract.Satisfies(buildinfo.Version, framework.Framework.RequiredVersion)
			if serr != nil || !ok {
				target := filepath.Join(base, "framework.yaml")
				if err := ConfinedTarget(resolvedBase, target); err != nil {
					return nil, err
				}
				framework.Framework.RequiredVersion = requiredVersionRange()
				if err := writeYAML(target, framework); err != nil {
					return nil, err
				}
				report.PinUpdated = framework.Framework.RequiredVersion
			}
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(abs)
	seeds := map[string]func() error{
		".gitignore": func() error {
			return os.WriteFile(filepath.Join(base, ".gitignore"), []byte("# Generated ox-agent runtime state; do not commit.\nruntime/\n"), 0o644)
		},
		".gitattributes": func() error {
			return os.WriteFile(filepath.Join(base, ".gitattributes"), []byte("# The append-only event journal merges cleanly across branches with the union driver.\nstate/events.ndjson merge=union\n"), 0o644)
		},
		"assets.lock": func() error { return assets.WriteLock(base) },
		"README.md": func() error {
			return os.WriteFile(filepath.Join(base, "README.md"), []byte(scaffoldReadme), 0o644)
		},
		"state/events.ndjson": func() error { return os.WriteFile(filepath.Join(base, "state", "events.ndjson"), nil, 0o644) },
		"framework.yaml": func() error {
			framework := FrameworkConfig{SchemaVersion: 1}
			framework.Framework.Name = "ox-agent"
			framework.Framework.Distribution = "standalone"
			framework.Framework.Binary = "ox-agent"
			framework.Framework.RequiredVersion = requiredVersionRange()
			framework.Project.Name = name
			return writeYAML(filepath.Join(base, "framework.yaml"), framework)
		},
		"config.yaml": func() error {
			settings := Settings{SchemaVersion: 1}
			settings.Project.Name = name
			settings.Workflow.DefaultProfile = "normal"
			settings.Workflow.Profiles = map[string][]string{"normal": defaultStages}
			settings.Skills.Enabled = defaultSkills
			settings.Skills.StageSkills = defaultStageSkills
			return writeYAML(filepath.Join(base, "config.yaml"), settings)
		},
		"policy.yaml": func() error {
			policy := Policy{SchemaVersion: 1}
			policy.Autonomy.ProfileChanges = "managed"
			policy.Autonomy.AllowDowngrade = false
			policy.Approvals.ProductionDeploy = "human"
			policy.Approvals.DestructiveChange = "human"
			policy.Approvals.PolicyChange = "human"
			policy.Budgets.MaxParallelAgents = 4
			policy.Budgets.MaxChildDepth = 1
			policy.Budgets.MaxFixCycles = 3
			return writeYAML(filepath.Join(base, "policy.yaml"), policy)
		},
		"context/project.yaml": func() error {
			ctx := struct {
				SchemaVersion int      `yaml:"schema_version"`
				Name          string   `yaml:"name"`
				Standards     []string `yaml:"standards"`
			}{SchemaVersion: 1, Name: name, Standards: []string{}}
			return writeYAML(filepath.Join(base, "context", "project.yaml"), ctx)
		},
	}
	for _, rel := range []string{".gitignore", ".gitattributes", "assets.lock", "README.md", "state/events.ndjson", "framework.yaml", "config.yaml", "policy.yaml", "context/project.yaml"} {
		target := filepath.Join(base, filepath.FromSlash(rel))
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := ConfinedTarget(resolvedBase, target); err != nil {
			return nil, err
		}
		if err := seeds[rel](); err != nil {
			return nil, err
		}
		report.SeedsWritten = append(report.SeedsWritten, rel)
	}
	// Refresh the lock so it describes the running binary's embedded
	// manifest; its content is deterministic, and a stale or corrupt lock
	// would keep doctor warning about drift the repair already resolved.
	lockTarget := filepath.Join(base, assets.LockPath)
	if err := ConfinedTarget(resolvedBase, lockTarget); err != nil {
		return nil, err
	}
	if err := assets.WriteLock(base); err != nil {
		return nil, err
	}
	return report, nil
}

package project

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"

	"strconv"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/oxidean/Ox-Agent/internal/buildinfo"
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
	return fmt.Sprintf(">=%s.%d.0 <%s.%d.0", parts[0], minor, parts[0], minor+1)
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
	gitattributes := "# Append-only journals merge cleanly across branches with the union driver.\nstate/events.ndjson merge=union\ndiscussions/*.yaml merge=union\n"
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
	return nil
}

// RepairReport summarizes what Repair restored in an existing .ox-agent.
type RepairReport struct {
	DirsCreated   []string
	FilesRestored []string
	SeedsWritten  []string
}

// Repair restores missing scaffold directories, missing materialized asset
// files, and missing generated seed files in an existing project. It never
// overwrites files that already exist; doctor reports drifted content.
func Repair(root string) (*RepairReport, error) {
	base := Base(root)
	if _, err := os.Stat(base); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s does not exist; run init without --repair", base)
		}
		return nil, err
	}
	report := &RepairReport{}
	for _, dir := range scaffoldDirs {
		path := filepath.Join(base, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
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
	for _, rel := range files {
		target := filepath.Join(base, filepath.FromSlash(rel))
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		data, err := assets.ReadFile(rel)
		if err != nil {
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
			return os.WriteFile(filepath.Join(base, ".gitattributes"), []byte("# Append-only journals merge cleanly across branches with the union driver.\nstate/events.ndjson merge=union\ndiscussions/*.yaml merge=union\n"), 0o644)
		},
		"assets.lock":         func() error { return assets.WriteLock(base) },
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
	for _, rel := range []string{".gitignore", ".gitattributes", "assets.lock", "state/events.ndjson", "framework.yaml", "config.yaml", "policy.yaml", "context/project.yaml"} {
		target := filepath.Join(base, filepath.FromSlash(rel))
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := seeds[rel](); err != nil {
			return nil, err
		}
		report.SeedsWritten = append(report.SeedsWritten, rel)
	}
	return report, nil
}

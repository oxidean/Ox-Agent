package project

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"

	"github.com/oxidean/Ox-Agent/internal/assets"
)

// defaultStages, defaultSkills, and defaultStageSkills seed a new project's
// config.yaml with the standard lifecycle profile and bundled skill set.
var defaultStages = []string{"discover", "brainstorm", "specify", "plan", "implement", "verify", "ship"}

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

// requiredVersion pins the binary range a fresh scaffold declares
// compatibility with.
const requiredVersion = ">=0.2.0 <0.3.0"

// scaffoldDirs lists the directories every project scaffold provides.
var scaffoldDirs = []string{
	"skills", "work/active", "work/archived", "context/standards",
	"lifecycle", "workflows", "pipelines", "state", "integrations",
	"runtime/packets", "runtime/runs", "runtime/cache", "runtime/locks",
}

// Init materializes a new .ox-agent project scaffold at dir.
func Init(dir string) (err error) {
	finalBase := Base(dir)
	if _, err := os.Stat(finalBase); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite it", finalBase)
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
	framework.Framework.RequiredVersion = requiredVersion
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
	if err := renameScaffold(base, finalBase); err != nil {
		return fmt.Errorf("publishing project scaffold: %w", err)
	}
	return nil
}

// Doctor verifies that root contains a valid Ox-Agent project layout.
func Doctor(root string) error {
	base := Base(root)
	framework, err := LoadFramework(root)
	if err != nil {
		return fmt.Errorf("reading .ox-agent/framework.yaml: %w", err)
	}
	if framework.SchemaVersion != 1 || framework.Framework.Name != "ox-agent" {
		return errors.New("unsupported or invalid Ox-Agent framework configuration")
	}
	for _, file := range []string{"framework.yaml", "config.yaml", "policy.yaml", "state/events.ndjson", "work/active", "skills", "workflows", "pipelines"} {
		if _, err := os.Stat(filepath.Join(base, file)); err != nil {
			return fmt.Errorf("missing .ox-agent/%s: %w", file, err)
		}
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

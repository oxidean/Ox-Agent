package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// version is the build version, injected via -ldflags at release time.
var version = "dev"

//go:embed all:assets
var embeddedAssets embed.FS

// projectConfig mirrors the .ox-agent/framework.yaml schema.
type projectConfig struct {
	SchemaVersion int `yaml:"schema_version"`
	Framework     struct {
		Name            string `yaml:"name"`
		Distribution    string `yaml:"distribution"`
		Binary          string `yaml:"binary"`
		RequiredVersion string `yaml:"required_version"`
	} `yaml:"framework"`
	Project struct {
		Name string `yaml:"name"`
	} `yaml:"project"`
}

// projectSettings mirrors the .ox-agent/config.yaml project-configuration
// schema; framework identity lives in framework.yaml.
type projectSettings struct {
	SchemaVersion int `yaml:"schema_version"`
	Project       struct {
		Name        string `yaml:"name"`
		TestCommand string `yaml:"test_command"`
	} `yaml:"project"`
	Workflow struct {
		DefaultProfile string              `yaml:"default_profile"`
		Profiles       map[string][]string `yaml:"profiles"`
	} `yaml:"workflow"`
	Skills struct {
		Enabled     []string            `yaml:"enabled"`
		StageSkills map[string][]string `yaml:"stage_skills"`
	} `yaml:"skills"`
}

// policyConfig mirrors the .ox-agent/policy.yaml schema.
type policyConfig struct {
	SchemaVersion int `yaml:"schema_version"`
	Autonomy      struct {
		ProfileChanges string `yaml:"profile_changes"`
		AllowDowngrade bool   `yaml:"allow_downgrade"`
	} `yaml:"autonomy"`
	Approvals struct {
		ProductionDeploy  string `yaml:"production_deploy"`
		DestructiveChange string `yaml:"destructive_change"`
		PolicyChange      string `yaml:"policy_change"`
	} `yaml:"approvals"`
	Budgets struct {
		MaxParallelAgents int `yaml:"max_parallel_agents"`
		MaxChildDepth     int `yaml:"max_child_depth"`
		MaxFixCycles      int `yaml:"max_fix_cycles"`
	} `yaml:"budgets"`
}

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

// main dispatches ox-agent subcommands.
func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "--help" {
		printHelp()
		return
	}
	if os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Println(version)
		return
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = initProject(".")
	case "doctor":
		err = withProjectRoot(doctor)
	case "start":
		err = withProjectRoot(func(root string) error {
			return start(root, strings.Join(os.Args[2:], " "))
		})
	case "status":
		err = withProjectRoot(status)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// printHelp writes the CLI usage summary to stdout.
func printHelp() {
	fmt.Println(`ox-agent - repository-local AI-SDLC

Usage:
  ox-agent version
  ox-agent init
  ox-agent doctor
  ox-agent start <title>
  ox-agent status`)
}

// initProject materializes a new .ox-agent project scaffold at root.
func initProject(root string) (err error) {
	finalBase := filepath.Join(root, ".ox-agent")
	if _, err := os.Stat(finalBase); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite it", finalBase)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := newStagingDir(root)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staging)
		}
	}()
	base := staging
	dirs := []string{
		"skills", "work/active", "work/archived", "context/standards",
		"lifecycle", "workflows", "pipelines", "state", "integrations",
		"runtime/packets", "runtime/runs", "runtime/cache", "runtime/locks",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := materializeAssets(base); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolving project root: %w", err)
	}
	project := filepath.Base(absRoot)
	config := projectConfig{SchemaVersion: 1}
	config.Framework.Name = "ox-agent"
	config.Framework.Distribution = "standalone"
	config.Framework.Binary = "ox-agent"
	config.Framework.RequiredVersion = ">=0.2.0 <0.3.0"
	config.Project.Name = project
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encoding framework configuration: %w", err)
	}
	if err := os.WriteFile(filepath.Join(base, "framework.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("writing framework configuration: %w", err)
	}
	settings := projectSettings{SchemaVersion: 1}
	settings.Project.Name = project
	settings.Workflow.DefaultProfile = "normal"
	settings.Workflow.Profiles = map[string][]string{"normal": defaultStages}
	settings.Skills.Enabled = defaultSkills
	settings.Skills.StageSkills = defaultStageSkills
	if err := writeYAML(filepath.Join(base, "config.yaml"), settings); err != nil {
		return err
	}
	policy := policyConfig{SchemaVersion: 1}
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
	fmt.Printf("Initialized %s\n", finalBase)
	return nil
}

// writeYAML marshals value and writes it to path with mode 0644.
func writeYAML(path string, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// findProjectRoot locates the nearest ancestor of start containing a
// .ox-agent directory, matching the discovery behavior of the legacy CLI.
func findProjectRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, ".ox-agent")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("could not find .ox-agent from %s", start)
		}
		current = parent
	}
}

// withProjectRoot resolves the enclosing project's root and invokes fn with
// it, so project commands work from any subdirectory.
func withProjectRoot(fn func(string) error) error {
	root, err := findProjectRoot(".")
	if err != nil {
		return err
	}
	return fn(root)
}

// renameScaffold publishes a fully staged scaffold; a var so tests can inject failures.
var renameScaffold = os.Rename

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

// materializeAssets expands the embedded asset tree under base.
func materializeAssets(base string) error {
	return fs.WalkDir(embeddedAssets, "assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative := strings.TrimPrefix(path, "assets/")
		if relative == "assets" || relative == "" {
			return nil
		}
		target := filepath.Join(base, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(embeddedAssets, path)
		if err != nil {
			return fmt.Errorf("reading embedded asset %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// doctor verifies that root contains a valid Ox-Agent project layout.
func doctor(root string) error {
	base := filepath.Join(root, ".ox-agent")
	configData, err := os.ReadFile(filepath.Join(base, "framework.yaml"))
	if err != nil {
		return fmt.Errorf("reading .ox-agent/framework.yaml: %w", err)
	}
	var config projectConfig
	if err := yaml.Unmarshal(configData, &config); err != nil {
		return fmt.Errorf("parsing .ox-agent/framework.yaml: %w", err)
	}
	if config.SchemaVersion != 1 || config.Framework.Name != "ox-agent" {
		return errors.New("unsupported or invalid Ox-Agent framework configuration")
	}
	for _, file := range []string{"framework.yaml", "config.yaml", "policy.yaml", "state/events.ndjson", "work/active", "skills", "workflows", "pipelines"} {
		if _, err := os.Stat(filepath.Join(base, file)); err != nil {
			return fmt.Errorf("missing .ox-agent/%s: %w", file, err)
		}
	}
	fmt.Printf("Ox-Agent doctor: healthy (binary %s)\n", version)
	return nil
}

// start creates a work item in .ox-agent/work/active with the next available
// WI-NNNN identifier, writes its index.json, and records a work_item.created
// lifecycle event. ID allocation and directory creation are serialized across
// concurrent processes.
func start(root, title string) error {
	if strings.TrimSpace(title) == "" {
		return errors.New("start requires a work-item title")
	}
	workRoot := filepath.Join(root, ".ox-agent", "work")
	var id, dir string
	created := false
	err := withWorkItemLock(workRoot, func() error {
		next, err := nextWorkItemNumber(workRoot)
		if err != nil {
			return err
		}
		id = fmt.Sprintf("WI-%04d", next)
		dir = filepath.Join(workRoot, "active", id+"-"+slugify(title))
		if err := os.Mkdir(dir, 0o755); err != nil {
			return fmt.Errorf("creating work item: %w", err)
		}
		created = true
		now := time.Now().UTC().Format(time.RFC3339)
		index := map[string]any{"schema_version": 1, "id": id, "title": title, "state": "intake", "profile": "normal", "current_stage": "intake", "created_at": now}
		data, err := json.MarshalIndent(index, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding work item: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.json"), append(data, '\n'), 0o644); err != nil {
			return fmt.Errorf("writing work item: %w", err)
		}
		return nil
	})
	if err != nil {
		if created {
			if removeErr := os.RemoveAll(dir); removeErr != nil {
				return fmt.Errorf("%w (rolling back %s also failed: %v)", err, dir, removeErr)
			}
		}
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		rel = dir
	}
	if err := appendEvent(root, lifecycleEvent{
		Type:       "work_item.created",
		WorkItemID: id,
		Actor:      "cli",
		Data:       map[string]any{"title": title, "directory": filepath.ToSlash(rel)},
	}); err != nil {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			return fmt.Errorf("%w (rolling back %s also failed: %v)", err, dir, removeErr)
		}
		return err
	}
	fmt.Printf("%s: %s\n", id, title)
	return nil
}

// nonSlugChars matches title characters that are not valid in a work-item slug.
var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a work-item title into a filesystem-safe directory suffix.
func slugify(title string) string {
	slug := strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		slug = "work-item"
	}
	return slug
}

// nextWorkItemNumber returns the lowest unallocated work-item number across
// the active and archived buckets.
func nextWorkItemNumber(workRoot string) (int, error) {
	highest := 0
	for _, bucket := range []string{"active", "archived"} {
		entries, err := os.ReadDir(filepath.Join(workRoot, bucket))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return 0, fmt.Errorf("reading %s work items: %w", bucket, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "WI-") {
				continue
			}
			digits := name[3:]
			if i := strings.IndexByte(digits, '-'); i >= 0 {
				digits = digits[:i]
			}
			if n, err := strconv.Atoi(digits); err == nil && n > highest {
				highest = n
			}
		}
	}
	return highest + 1, nil
}

// allocationLockStaleAfter bounds how long an orphaned allocation lock file is
// trusted before a waiting process reclaims it.
const allocationLockStaleAfter = 30 * time.Second

// withWorkItemLock serializes work-item ID allocation across concurrent
// ox-agent processes. The scan-and-create critical section runs while holding
// an atomic lock file so two start commands cannot claim the same WI-NNNN
// number under different directory slugs.
func withWorkItemLock(workRoot string, fn func() error) error {
	lockPath := filepath.Join(workRoot, ".allocate.lock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(lock, "pid=%d\n", os.Getpid())
			lock.Close()
			defer os.Remove(lockPath)
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("acquiring work item lock: %w", err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > allocationLockStaleAfter {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for work item allocation lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// lifecycleEvent is one entry in the .ox-agent/state/events.ndjson journal.
type lifecycleEvent struct {
	SchemaVersion int            `json:"schema_version"`
	EventID       string         `json:"event_id"`
	Timestamp     string         `json:"timestamp"`
	Type          string         `json:"type"`
	WorkItemID    string         `json:"work_item_id"`
	Actor         string         `json:"actor"`
	Data          map[string]any `json:"data,omitempty"`
}

// appendEvent appends a lifecycle event to the project's event journal.
func appendEvent(root string, event lifecycleEvent) error {
	event.SchemaVersion = 1
	event.EventID = fmt.Sprintf("evt_%d_%06x", time.Now().UnixMilli(), rand.Uint64N(1<<24))
	event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encoding lifecycle event: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(root, ".ox-agent", "state", "events.ndjson"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening event journal: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("appending lifecycle event: %w", err)
	}
	return nil
}

// status lists the active work items for the project at root.
func status(root string) error {
	entries, err := os.ReadDir(filepath.Join(root, ".ox-agent", "work", "active"))
	if err != nil {
		return fmt.Errorf("reading active work items: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			fmt.Printf("active: %s\n", entry.Name())
		}
	}
	return nil
}

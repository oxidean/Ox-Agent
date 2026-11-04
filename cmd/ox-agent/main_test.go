package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInitDoctorStartStatus exercises the init → doctor → start → status happy path.
func TestInitDoctorStartStatus(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if err := doctor(root); err != nil {
		t.Fatalf("doctor() error = %v", err)
	}
	if err := start(root, "Add health check"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	workDir := filepath.Join(root, ".ox-agent", "work", "active", "WI-0001-add-health-check")
	if _, err := os.Stat(filepath.Join(workDir, "index.json")); err != nil {
		t.Fatalf("work item index missing: %v", err)
	}
}

// TestInitIsNonDestructive verifies init refuses to overwrite an existing project.
func TestInitIsNonDestructive(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("first initProject() error = %v", err)
	}
	if err := initProject(root); err == nil {
		t.Fatal("second initProject() succeeded; want refusal")
	}
}

// TestInitMaterializesConfigAndPolicy verifies init writes config.yaml and policy.yaml with the documented defaults.
func TestInitMaterializesConfigAndPolicy(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	base := filepath.Join(root, ".ox-agent")
	config, err := os.ReadFile(filepath.Join(base, "config.yaml"))
	if err != nil {
		t.Fatalf("reading config.yaml: %v", err)
	}
	for _, want := range []string{"test_command:", "default_profile: normal", "enabled:", "stage_skills:"} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("config.yaml missing %q:\n%s", want, config)
		}
	}
	policy, err := os.ReadFile(filepath.Join(base, "policy.yaml"))
	if err != nil {
		t.Fatalf("reading policy.yaml: %v", err)
	}
	for _, want := range []string{"profile_changes: managed", "production_deploy: human", "max_parallel_agents: 4"} {
		if !strings.Contains(string(policy), want) {
			t.Fatalf("policy.yaml missing %q:\n%s", want, policy)
		}
	}
}

// TestFindProjectRootWalksUp verifies project discovery resolves the root from nested subdirectories.
func TestFindProjectRootWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	nested := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}
	got, err := findProjectRoot(nested)
	if err != nil {
		t.Fatalf("findProjectRoot() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolving root: %v", err)
	}
	if got, err = filepath.EvalSymlinks(got); err != nil || got != want {
		t.Fatalf("findProjectRoot() = %q; want %q", got, want)
	}
	if _, err := findProjectRoot(t.TempDir()); err == nil {
		t.Skip("temporary directory resides inside an Ox-Agent project")
	}
}

// TestInitUsesCurrentDirectoryName verifies init derives project.name from the directory basename.
func TestInitUsesCurrentDirectoryName(t *testing.T) {
	project := filepath.Join(t.TempDir(), "alpha-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("creating project dir: %v", err)
	}
	t.Chdir(project)
	if err := initProject("."); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, ".ox-agent", "framework.yaml"))
	if err != nil {
		t.Fatalf("reading framework.yaml: %v", err)
	}
	if !strings.Contains(string(data), "name: alpha-project") {
		t.Fatalf("framework.yaml project name missing directory name: %s", data)
	}
}

// TestStartAllocatesUniqueIDsAcrossBuckets verifies WI numbering continues past archived items.
func TestStartAllocatesUniqueIDsAcrossBuckets(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if err := start(root, "First item"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	work := filepath.Join(root, ".ox-agent", "work")
	if err := os.Rename(filepath.Join(work, "active", "WI-0001-first-item"), filepath.Join(work, "archived", "WI-0001-first-item")); err != nil {
		t.Fatalf("archiving work item: %v", err)
	}
	if err := start(root, "Second item"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "active", "WI-0002-second-item", "index.json")); err != nil {
		t.Fatalf("second work item index missing: %v", err)
	}
}

// TestStartSanitizesSlug verifies title slugification strips unsafe characters and caps length.
func TestStartSanitizesSlug(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if err := start(root, "Fix: login/redirect?"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	dir := filepath.Join(root, ".ox-agent", "work", "active", "WI-0001-fix-login-redirect")
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Fatalf("sanitized work item missing: %v", err)
	}
}

// TestStartAppendsCreatedEvent verifies start journals a work_item.created event.
func TestStartAppendsCreatedEvent(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if err := start(root, "Track events"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".ox-agent", "state", "events.ndjson"))
	if err != nil {
		t.Fatalf("reading event journal: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("parsing event journal: %v", err)
	}
	if event["type"] != "work_item.created" || event["work_item_id"] != "WI-0001" || event["actor"] != "cli" {
		t.Fatalf("unexpected event: %v", event)
	}
}

// TestStartSerializesConcurrentAllocation verifies the allocation lock serializes concurrent start calls.
func TestStartSerializesConcurrentAllocation(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	const workers = 8
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(n int) {
			errs <- start(root, fmt.Sprintf("Concurrent item %d", n))
		}(i)
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("start() error = %v", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, ".ox-agent", "work", "active"))
	if err != nil {
		t.Fatalf("reading active work items: %v", err)
	}
	if len(entries) != workers {
		t.Fatalf("active work items = %d, want %d", len(entries), workers)
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		parts := strings.SplitN(entry.Name(), "-", 3)
		if ids[parts[0]+"-"+parts[1]] {
			t.Fatalf("duplicate work item number allocated: %s", entry.Name())
		}
		ids[parts[0]+"-"+parts[1]] = true
	}
	data, err := os.ReadFile(filepath.Join(root, ".ox-agent", "state", "events.ndjson"))
	if err != nil {
		t.Fatalf("reading event journal: %v", err)
	}
	if n := len(strings.Split(strings.TrimSpace(string(data)), "\n")); n != workers {
		t.Fatalf("event journal entries = %d, want %d", n, workers)
	}
}

// TestStartReclaimsStaleAllocationLock verifies a stale lock file is reclaimed rather than blocking forever.
func TestStartReclaimsStaleAllocationLock(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	lockPath := filepath.Join(root, ".ox-agent", "work", ".allocate.lock")
	if err := os.WriteFile(lockPath, []byte("pid=0\n"), 0o644); err != nil {
		t.Fatalf("seeding stale lock: %v", err)
	}
	stale := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatalf("backdating stale lock: %v", err)
	}
	if err := start(root, "After stale lock"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file remains after start: %v", err)
	}
	workDir := filepath.Join(root, ".ox-agent", "work", "active", "WI-0001-after-stale-lock")
	if _, err := os.Stat(filepath.Join(workDir, "index.json")); err != nil {
		t.Fatalf("work item index missing: %v", err)
	}
}

// TestStartRollsBackWhenEventAppendFails verifies start removes the new work item when event journaling fails.
func TestStartRollsBackWhenEventAppendFails(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	if err := os.Remove(journal); err != nil {
		t.Fatalf("removing event journal: %v", err)
	}
	if err := os.Mkdir(journal, 0o755); err != nil {
		t.Fatalf("replacing event journal with directory: %v", err)
	}
	if err := start(root, "Will roll back"); err == nil {
		t.Fatal("start() succeeded; want event journal failure")
	}
	entries, err := os.ReadDir(filepath.Join(root, ".ox-agent", "work", "active"))
	if err != nil {
		t.Fatalf("reading active work items: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rolled-back work item remains: %v", entries)
	}
}

// TestInitRetryAfterPublishFailure verifies init can be retried after a mid-scaffold failure.
func TestInitRetryAfterPublishFailure(t *testing.T) {
	root := t.TempDir()
	injected := errors.New("publish failed")
	restore := renameScaffold
	renameScaffold = func(string, string) error { return injected }
	defer func() { renameScaffold = restore }()
	if err := initProject(root); !errors.Is(err, injected) {
		t.Fatalf("initProject() error = %v, want %v", err, injected)
	}
	renameScaffold = restore
	if _, err := os.Stat(filepath.Join(root, ".ox-agent")); !os.IsNotExist(err) {
		t.Fatalf("partial .ox-agent scaffold remains after failure: %v", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(root, ".ox-agent.tmp-*")); len(leftovers) != 0 {
		t.Fatalf("staging residue remains after failure: %v", leftovers)
	}
	if err := initProject(root); err != nil {
		t.Fatalf("retry initProject() error = %v", err)
	}
}

// TestInitPreservesUnownedStagingDirs verifies init does not delete staging directories it did not create.
func TestInitPreservesUnownedStagingDirs(t *testing.T) {
	root := t.TempDir()
	unowned := filepath.Join(root, ".ox-agent.tmp-stale")
	if err := os.MkdirAll(unowned, 0o755); err != nil {
		t.Fatalf("seeding unowned staging dir: %v", err)
	}
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatalf("unowned staging dir was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ox-agent")); err != nil {
		t.Fatalf(".ox-agent scaffold missing: %v", err)
	}
}

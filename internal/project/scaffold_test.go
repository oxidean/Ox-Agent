package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/assets"
)

// TestInitDoctor verifies init produces a scaffold doctor accepts.
func TestInitDoctor(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if report := Doctor(root); !report.Healthy() {
		t.Fatalf("Doctor() unhealthy: %+v", report.Checks)
	}
}

// TestInitIsNonDestructive verifies init refuses to overwrite an existing project.
func TestInitIsNonDestructive(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("first Init() error = %v", err)
	}
	if err := Init(root); err == nil {
		t.Fatal("second Init() succeeded; want refusal")
	}
}

// TestInitMaterializesConfigAndPolicy verifies init writes config.yaml and policy.yaml with the documented defaults.
func TestInitMaterializesConfigAndPolicy(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
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

// TestInitUsesCurrentDirectoryName verifies init derives project.name from the directory basename.
func TestInitUsesCurrentDirectoryName(t *testing.T) {
	project := filepath.Join(t.TempDir(), "alpha-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("creating project dir: %v", err)
	}
	t.Chdir(project)
	if err := Init("."); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, ".ox-agent", "framework.yaml"))
	if err != nil {
		t.Fatalf("reading framework.yaml: %v", err)
	}
	if !strings.Contains(string(data), "name: alpha-project") {
		t.Fatalf("framework.yaml project name missing directory name: %s", data)
	}
}

// TestInitRetryAfterPublishFailure verifies init can be retried after a mid-scaffold failure.
func TestInitRetryAfterPublishFailure(t *testing.T) {
	root := t.TempDir()
	injected := errors.New("publish failed")
	restore := renameScaffold
	renameScaffold = func(string, string) error { return injected }
	defer func() { renameScaffold = restore }()
	if err := Init(root); !errors.Is(err, injected) {
		t.Fatalf("Init() error = %v, want %v", err, injected)
	}
	renameScaffold = restore
	if _, err := os.Stat(filepath.Join(root, ".ox-agent")); !os.IsNotExist(err) {
		t.Fatalf("partial .ox-agent scaffold remains after failure: %v", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(root, ".ox-agent.tmp-*")); len(leftovers) != 0 {
		t.Fatalf("staging residue remains after failure: %v", leftovers)
	}
	if err := Init(root); err != nil {
		t.Fatalf("retry Init() error = %v", err)
	}
}

// TestInitPreservesUnownedStagingDirs verifies init does not delete staging directories it did not create.
func TestInitPreservesUnownedStagingDirs(t *testing.T) {
	root := t.TempDir()
	unowned := filepath.Join(root, ".ox-agent.tmp-stale")
	if err := os.MkdirAll(unowned, 0o755); err != nil {
		t.Fatalf("seeding unowned staging dir: %v", err)
	}
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatalf("unowned staging dir was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ox-agent")); err != nil {
		t.Fatalf(".ox-agent scaffold missing: %v", err)
	}
}

// TestRepairRestoresMissingPieces verifies --repair fills deleted scaffold
// dirs, assets, and seed files without touching existing content.
func TestRepairRestoresMissingPieces(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	for _, rel := range []string{
		"skills/adr/SKILL.md", "config.yaml", ".gitignore",
		"state/events.ndjson", "context/project.yaml", "assets.lock",
	} {
		if err := os.Remove(filepath.Join(base, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("removing %s: %v", rel, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(base, "discussions")); err != nil {
		t.Fatalf("removing discussions dir: %v", err)
	}
	report, err := Repair(root, false)
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if len(report.DirsCreated) == 0 || len(report.FilesRestored) == 0 || len(report.SeedsWritten) == 0 {
		t.Fatalf("incomplete repair report: %+v", report)
	}
	for _, rel := range []string{"skills/adr/SKILL.md", "config.yaml", "assets.lock", "discussions"} {
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s not restored: %v", rel, err)
		}
	}
}

// TestRepairDoesNotOverwrite verifies --repair preserves locally modified files.
func TestRepairDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	custom := filepath.Join(Base(root), "config.yaml")
	if err := os.WriteFile(custom, []byte("custom: true\n"), 0o644); err != nil {
		t.Fatalf("writing custom config: %v", err)
	}
	if _, err := Repair(root, false); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	data, err := os.ReadFile(custom)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if string(data) != "custom: true\n" {
		t.Fatalf("custom config overwritten: %s", data)
	}
}

// TestInitWritesLockAndGitignore verifies init emits the asset lock and
// .gitignore covering the runtime directory.
func TestInitWritesLockAndGitignore(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	gitignore, err := os.ReadFile(filepath.Join(base, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), "runtime/") {
		t.Fatalf(".gitignore missing runtime entry: %s", gitignore)
	}
	if _, err := os.Stat(filepath.Join(base, "assets.lock")); err != nil {
		t.Fatalf("assets.lock missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "lifecycle", "workflow.yaml")); err != nil {
		t.Fatalf("lifecycle asset missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "integrations", "manual.yaml")); err != nil {
		t.Fatalf("integration asset missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "context", "project.yaml")); err != nil {
		t.Fatalf("context/project.yaml missing: %v", err)
	}
}

// TestInitWritesReadme verifies init seeds the .ox-agent README pointer and
// Repair restores it when missing.
func TestInitWritesReadme(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	readme := filepath.Join(Base(root), "README.md")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("README.md missing after Init(): %v", err)
	}
	if !strings.Contains(string(data), "ox-agent integrate") {
		t.Fatal("README.md does not point at integrate")
	}
	if err := os.Remove(readme); err != nil {
		t.Fatalf("removing README.md: %v", err)
	}
	report, err := Repair(root, false)
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if _, err := os.Stat(readme); err != nil {
		t.Fatalf("README.md not restored by Repair(): %v", err)
	}
	found := false
	for _, seed := range report.SeedsWritten {
		if seed == "README.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("README.md missing from SeedsWritten: %v", report.SeedsWritten)
	}
}

// TestDoctorWarnsUnintegrated verifies doctor warns about missing agent
// discovery wiring without failing health, and the warning clears after
// Integrate.
func TestDoctorWarnsUnintegrated(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	report := Doctor(root)
	if !report.Healthy() {
		t.Fatalf("Doctor() unhealthy on fresh init: %+v", report.Checks)
	}
	warned := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "integrate") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("Doctor() did not warn about missing integration: %v", report.Warnings)
	}
	if _, err := Integrate(root); err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	report = Doctor(root)
	for _, w := range report.Warnings {
		if strings.Contains(w, "integrate") {
			t.Fatalf("integration warning persisted after Integrate(): %v", w)
		}
	}
}

// TestRepairSyncRewritesDrift verifies --sync rewrites materialized assets
// that drifted from the embedded tree while plain repair leaves them.
func TestRepairSyncRewritesDrift(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	target := filepath.Join(Base(root), "pipelines", "ship.yaml")
	if err := os.WriteFile(target, []byte("# local edit\n"), 0o644); err != nil {
		t.Fatalf("corrupting asset: %v", err)
	}
	if _, err := Repair(root, false); err != nil {
		t.Fatalf("Repair(false) error = %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "# local edit\n" {
		t.Fatal("Repair(false) overwrote a drifted file")
	}
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(true) error = %v", err)
	}
	found := false
	for _, rel := range report.FilesSynced {
		if rel == "pipelines/ship.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pipelines/ship.yaml missing from FilesSynced: %v", report.FilesSynced)
	}
	if data, _ := os.ReadFile(target); string(data) == "# local edit\n" {
		t.Fatal("Repair(true) did not rewrite the drifted file")
	}
}

// TestRepairSyncRefreshesLock verifies a successful sync rewrites assets.lock
// so doctor stops warning about drift the sync already corrected.
func TestRepairSyncRefreshesLock(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	lock, err := assets.LoadLock(base)
	if err != nil {
		t.Fatalf("LoadLock() error = %v", err)
	}
	for rel := range lock.Files {
		lock.Files[rel] = "sha256:stale"
		break
	}
	out, _ := json.MarshalIndent(lock, "", "  ")
	if err := os.WriteFile(filepath.Join(base, assets.LockPath), out, 0o644); err != nil {
		t.Fatalf("staleness injection: %v", err)
	}
	if _, err := Repair(root, true); err != nil {
		t.Fatalf("Repair(true) error = %v", err)
	}
	missing, changed, _, err := assets.CheckLock(base)
	if err != nil {
		t.Fatalf("CheckLock() error = %v", err)
	}
	if len(missing) > 0 || len(changed) > 0 {
		t.Fatalf("lock still stale after Repair(true): missing=%v changed=%v", missing, changed)
	}
}

// TestRepairWritesStayInsideBase verifies repair never writes through a
// symlink planted inside .ox-agent: a symlinked asset file is replaced by a
// real file, and a symlinked parent directory fails the repair outright
// instead of redirecting writes outside the project.
func TestRepairWritesStayInsideBase(t *testing.T) {
	t.Run("symlinked asset file", func(t *testing.T) {
		root := t.TempDir()
		if err := Init(root); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		outside := filepath.Join(t.TempDir(), "victim.txt")
		if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
			t.Fatalf("writing outside file: %v", err)
		}
		target := filepath.Join(Base(root), "skills", "adr", "SKILL.md")
		if err := os.Remove(target); err != nil {
			t.Fatalf("removing asset: %v", err)
		}
		if err := os.Symlink(outside, target); err != nil {
			t.Fatalf("planting symlink: %v", err)
		}
		if _, err := Repair(root, true); err != nil {
			t.Fatalf("Repair(true) error = %v", err)
		}
		if data, _ := os.ReadFile(outside); string(data) != "keep me" {
			t.Fatal("repair wrote through the symlink to an outside file")
		}
		info, err := os.Lstat(target)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("symlinked asset was not replaced by a real file")
		}
	})

	t.Run("symlinked parent dir", func(t *testing.T) {
		root := t.TempDir()
		if err := Init(root); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		outside := t.TempDir()
		if err := os.RemoveAll(filepath.Join(Base(root), "skills")); err != nil {
			t.Fatalf("removing skills dir: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(Base(root), "skills")); err != nil {
			t.Fatalf("planting dir symlink: %v", err)
		}
		if _, err := Repair(root, true); err == nil {
			t.Fatal("Repair(true) succeeded through a symlinked parent dir")
		}
		entries, err := os.ReadDir(outside)
		if err != nil {
			t.Fatalf("reading outside dir: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("repair wrote outside the project: %v", entries)
		}
	})

	t.Run("symlinked ancestor with missing child", func(t *testing.T) {
		root := t.TempDir()
		if err := Init(root); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		outside := t.TempDir()
		stateDir := filepath.Join(Base(root), "state")
		if err := os.RemoveAll(stateDir); err != nil {
			t.Fatalf("removing state dir: %v", err)
		}
		if err := os.Symlink(outside, stateDir); err != nil {
			t.Fatalf("planting dir symlink: %v", err)
		}
		// state resolves outside and exists, so the dirs loop skips it;
		// state/snapshots is missing and must not be created through the
		// symlink.
		if _, err := Repair(root, false); err == nil {
			t.Fatal("Repair(false) created a missing dir through a symlinked ancestor")
		}
		entries, err := os.ReadDir(outside)
		if err != nil {
			t.Fatalf("reading outside dir: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("repair created directories outside the project: %v", entries)
		}
	})
}

// TestIntegrationGapsWrongTarget verifies IntegrationGaps reports a discovery
// link that resolves to an existing directory other than the materialized
// skill tree.
func TestIntegrationGapsWrongTarget(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if _, err := Integrate(root); err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if gaps := IntegrationGaps(root); len(gaps) != 0 {
		t.Fatalf("unexpected gaps after Integrate(): %v", gaps)
	}
	foreign := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatalf("creating foreign dir: %v", err)
	}
	link := filepath.Join(root, ".agents", "skills")
	if err := os.Remove(link); err != nil {
		t.Fatalf("removing link: %v", err)
	}
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatalf("repointing link: %v", err)
	}
	gaps := IntegrationGaps(root)
	found := false
	for _, g := range gaps {
		if strings.Contains(g, ".agents") && strings.Contains(g, "wrong target") {
			found = true
		}
	}
	if !found {
		t.Fatalf("foreign link not reported as a gap: %v", gaps)
	}
}

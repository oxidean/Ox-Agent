package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"skills/adr/SKILL.md", "config.yaml", ".gitignore", "README.md",
		"state/events.ndjson", "context/project.yaml", "assets.lock",
	} {
		if err := os.Remove(filepath.Join(base, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("removing %s: %v", rel, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(base, "discussions")); err != nil {
		t.Fatalf("removing discussions dir: %v", err)
	}
	report, err := Repair(root)
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if len(report.DirsCreated) == 0 || len(report.FilesRestored) == 0 || len(report.SeedsWritten) == 0 {
		t.Fatalf("incomplete repair report: %+v", report)
	}
	for _, rel := range []string{"skills/adr/SKILL.md", "config.yaml", "assets.lock", "discussions", "README.md"} {
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
	if _, err := Repair(root); err != nil {
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

// TestInitWritesReadme verifies the scaffold leaves an in-tree pointer doc so
// a bare init is still discoverable inside .ox-agent/ itself.
func TestInitWritesReadme(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(Base(root), "README.md"))
	if err != nil {
		t.Fatalf("README.md missing: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "ox-agent integrate") {
		t.Fatal("README.md does not point at integrate")
	}
}

// TestRepairConfinesWritesToProject verifies Repair cannot be redirected by a
// symlink planted inside .ox-agent: a valid symlinked asset is preserved, a
// dangling symlinked asset is replaced by a real file, and a symlinked parent
// directory fails the repair instead of redirecting writes outside the
// project.
func TestRepairConfinesWritesToProject(t *testing.T) {
	t.Run("valid symlinked asset is preserved", func(t *testing.T) {
		root := t.TempDir()
		if err := Init(root); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		base := Base(root)
		outside := filepath.Join(t.TempDir(), "victim.txt")
		if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
			t.Fatalf("writing outside file: %v", err)
		}
		target := filepath.Join(base, "skills", "adr", "SKILL.md")
		if err := os.Remove(target); err != nil {
			t.Fatalf("removing asset: %v", err)
		}
		if err := os.Symlink(outside, target); err != nil {
			t.Fatalf("planting symlink: %v", err)
		}
		if _, err := Repair(root); err != nil {
			t.Fatalf("Repair() error = %v", err)
		}
		info, err := os.Lstat(target)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("valid symlinked asset was replaced; want preserved")
		}
	})

	t.Run("dangling symlinked asset is replaced", func(t *testing.T) {
		root := t.TempDir()
		if err := Init(root); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		base := Base(root)
		outside := filepath.Join(t.TempDir(), "victim.txt")
		target := filepath.Join(base, "skills", "adr", "SKILL.md")
		if err := os.Remove(target); err != nil {
			t.Fatalf("removing asset: %v", err)
		}
		if err := os.Symlink(outside, target); err != nil {
			t.Fatalf("planting symlink: %v", err)
		}
		if _, err := Repair(root); err != nil {
			t.Fatalf("Repair() error = %v", err)
		}
		if _, err := os.Stat(outside); !os.IsNotExist(err) {
			t.Fatal("repair wrote through the dangling symlink to an outside file")
		}
		info, err := os.Lstat(target)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("dangling symlinked asset was not replaced by a real file")
		}
	})

	t.Run("symlinked parent dir fails repair", func(t *testing.T) {
		root := t.TempDir()
		if err := Init(root); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		base := Base(root)
		outside := t.TempDir()
		skillsDir := filepath.Join(base, "skills")
		marker := filepath.Join(skillsDir, "adr", "SKILL.md")
		data, err := os.ReadFile(marker)
		if err != nil {
			t.Fatalf("reading asset for restore: %v", err)
		}
		if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), data, 0o644); err != nil {
			t.Fatalf("staging outside dir: %v", err)
		}
		if err := os.RemoveAll(skillsDir); err != nil {
			t.Fatalf("removing skills dir: %v", err)
		}
		if err := os.Symlink(outside, skillsDir); err != nil {
			t.Fatalf("symlinking skills dir: %v", err)
		}
		if _, err := Repair(root); err == nil {
			t.Fatal("Repair() succeeded through a symlinked parent dir; want refusal")
		}
	})
}

// TestDoctorWarnsWithoutIntegration verifies doctor nudges toward `integrate`
// when neither an AGENTS.md managed block nor .agents/skills exists — a bare
// init is otherwise invisible to agents scanning standard discovery paths.
func TestDoctorWarnsWithoutIntegration(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	report := Doctor(root)
	found := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "ox-agent integrate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no integration warning on bare init: %v", report.Warnings)
	}
	if _, err := Integrate(root); err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	report = Doctor(root)
	for _, w := range report.Warnings {
		if strings.Contains(w, "ox-agent integrate") {
			t.Fatalf("integration warning persists after integrate: %v", report.Warnings)
		}
	}
	if !report.Healthy() {
		t.Fatalf("doctor unhealthy after integrate: %+v", report.Checks)
	}
	// The remaining assertions manipulate the managed link directly; on hosts
	// where Integrate could not create it (Windows without Developer Mode)
	// there is nothing to resolve and nothing meaningful to assert.
	link := filepath.Join(root, ".agents", "skills")
	if _, err := os.Lstat(link); err != nil {
		t.Log("managed link unavailable; skipping link-resolution assertions")
		return
	}
	// A .agents/skills link that does not resolve to .ox-agent/skills is not
	// wiring: with no AGENTS.md block, doctor warns again.
	if err := os.Remove(link); err != nil {
		t.Fatalf("removing managed link: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatalf("removing AGENTS.md: %v", err)
	}
	if err := os.Symlink("../elsewhere", link); err != nil {
		t.Fatalf("creating foreign link: %v", err)
	}
	report = Doctor(root)
	found = false
	for _, w := range report.Warnings {
		if strings.Contains(w, "ox-agent integrate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no integration warning on foreign link: %v", report.Warnings)
	}
	// A link resolving to .ox-agent/skills counts even without AGENTS.md.
	if err := os.Remove(link); err != nil {
		t.Fatalf("removing foreign link: %v", err)
	}
	if err := os.Symlink(filepath.Join("..", ".ox-agent", "skills"), link); err != nil {
		t.Fatalf("creating managed link: %v", err)
	}
	report = Doctor(root)
	for _, w := range report.Warnings {
		if strings.Contains(w, "ox-agent integrate") {
			t.Fatalf("integration warning fires on a resolvable link: %v", report.Warnings)
		}
	}
}

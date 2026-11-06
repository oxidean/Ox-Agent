package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/oxidean/Ox-Agent/internal/buildinfo"
	"github.com/oxidean/Ox-Agent/internal/contract"
)

// symlinkOrSkip plants a fixture symlink, skipping the test on Windows hosts
// without symlink privilege (ERROR_PRIVILEGE_NOT_HELD) rather than failing —
// Developer Mode or elevation is not guaranteed there. Other errors fail so
// fixture mistakes stay visible.
func symlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatalf("creating symlink: %v", err)
	}
}

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
	report, err := Repair(root, false)
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
		symlinkOrSkip(t, outside, target)
		if _, err := Repair(root, false); err != nil {
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
		symlinkOrSkip(t, outside, target)
		if _, err := Repair(root, false); err != nil {
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
		symlinkOrSkip(t, outside, skillsDir)
		if _, err := Repair(root, false); err == nil {
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
	symlinkOrSkip(t, "../elsewhere", link)
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
	symlinkOrSkip(t, filepath.Join("..", ".ox-agent", "skills"), link)
	report = Doctor(root)
	for _, w := range report.Warnings {
		if strings.Contains(w, "ox-agent integrate") {
			t.Fatalf("integration warning fires on a resolvable link: %v", report.Warnings)
		}
	}
}

// setBinaryVersion pins buildinfo.Version for a test and restores it.
func setBinaryVersion(t *testing.T, version string) {
	t.Helper()
	old := buildinfo.Version
	buildinfo.Version = version
	t.Cleanup(func() { buildinfo.Version = old })
}

// plantStaleAsset writes rel under base with content and records content's
// hash in the asset lock as an owned file, simulating an asset this binary
// no longer ships.
func plantStaleAsset(t *testing.T, base, rel string, content []byte) {
	t.Helper()
	target := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("creating stale dir: %v", err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatalf("writing stale asset: %v", err)
	}
	lock, err := assets.LoadLock(base)
	if err != nil {
		t.Fatalf("loading lock: %v", err)
	}
	sum := sha256.Sum256(content)
	lock.Files[rel] = "sha256:" + hex.EncodeToString(sum[:])
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatalf("encoding lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, assets.LockPath), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("writing lock: %v", err)
	}
}

// TestRepairSyncRewritesDriftedAsset verifies --sync rewrites asset files
// that drifted from the embedded tree.
func TestRepairSyncRewritesDriftedAsset(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	target := filepath.Join(base, "skills", "README.md")
	if err := os.WriteFile(target, []byte("local drift\n"), 0o644); err != nil {
		t.Fatalf("drifting asset: %v", err)
	}
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	if len(report.FilesSynced) != 1 || report.FilesSynced[0] != "skills/README.md" {
		t.Fatalf("FilesSynced = %v", report.FilesSynced)
	}
	want, err := assets.ReadFile("skills/README.md")
	if err != nil {
		t.Fatalf("reading embedded asset: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading synced asset: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("synced file does not match embedded content")
	}
}

// TestRepairWithoutSyncPreservesDrift verifies plain --repair leaves drifted
// asset files alone.
func TestRepairWithoutSyncPreservesDrift(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	target := filepath.Join(Base(root), "skills", "README.md")
	if err := os.WriteFile(target, []byte("local drift\n"), 0o644); err != nil {
		t.Fatalf("drifting asset: %v", err)
	}
	report, err := Repair(root, false)
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if len(report.FilesSynced) != 0 || len(report.FilesRemoved) != 0 {
		t.Fatalf("non-sync repair touched files: %+v", report)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "local drift\n" {
		t.Fatal("drifted file overwritten without --sync")
	}
}

// TestRepairSyncRemovesStaleOwnedFile verifies --sync deletes files the lock
// owned but the embedded tree no longer ships, when unmodified on disk.
func TestRepairSyncRemovesStaleOwnedFile(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	plantStaleAsset(t, base, "skills/dropped/SKILL.md", []byte("dropped skill\n"))
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	if len(report.FilesRemoved) != 1 || report.FilesRemoved[0] != "skills/dropped/SKILL.md" {
		t.Fatalf("FilesRemoved = %v", report.FilesRemoved)
	}
	if _, err := os.Stat(filepath.Join(base, "skills", "dropped", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("stale asset still on disk")
	}
	lock, err := assets.LoadLock(base)
	if err != nil {
		t.Fatalf("loading refreshed lock: %v", err)
	}
	if _, ok := lock.Files["skills/dropped/SKILL.md"]; ok {
		t.Fatal("refreshed lock still lists the dropped asset")
	}
}

// TestRepairSyncKeepsLocallyModifiedStale verifies --sync keeps a stale
// owned file whose on-disk content no longer matches the lock hash.
func TestRepairSyncKeepsLocallyModifiedStale(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	plantStaleAsset(t, base, "skills/dropped/SKILL.md", []byte("original\n"))
	// Local modification after materialization changes the hash.
	if err := os.WriteFile(filepath.Join(base, "skills", "dropped", "SKILL.md"), []byte("edited locally\n"), 0o644); err != nil {
		t.Fatalf("editing stale asset: %v", err)
	}
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	if len(report.FilesRemoved) != 0 {
		t.Fatalf("modified stale asset removed: %v", report.FilesRemoved)
	}
	if _, err := os.Stat(filepath.Join(base, "skills", "dropped", "SKILL.md")); err != nil {
		t.Fatal("locally modified stale asset removed")
	}
}

// TestRepairSyncReconcilesVersionPin verifies --sync resets a
// required_version range that excludes the running binary.
func TestRepairSyncReconcilesVersionPin(t *testing.T) {
	setBinaryVersion(t, "0.3.0")
	root := t.TempDir()
	// Init runs under 0.3.0 so its pin satisfies the binary; then narrow it.
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	framework, err := LoadFramework(root)
	if err != nil {
		t.Fatalf("loading framework.yaml: %v", err)
	}
	framework.Framework.RequiredVersion = ">=0.1.0 <0.2.0"
	if err := writeYAML(filepath.Join(base, "framework.yaml"), framework); err != nil {
		t.Fatalf("narrowing pin: %v", err)
	}
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	if report.PinUpdated != ">=0.3.0 <0.4.0" {
		t.Fatalf("PinUpdated = %q", report.PinUpdated)
	}
	framework, err = LoadFramework(root)
	if err != nil {
		t.Fatalf("reloading framework.yaml: %v", err)
	}
	ok, err := contract.Satisfies(buildinfo.Version, framework.Framework.RequiredVersion)
	if err != nil || !ok {
		t.Fatalf("rewritten pin %q does not satisfy %s", framework.Framework.RequiredVersion, buildinfo.Version)
	}
}

// TestRepairSyncPreservesCompatiblePin verifies --sync leaves a
// required_version range that already covers the binary untouched.
func TestRepairSyncPreservesCompatiblePin(t *testing.T) {
	setBinaryVersion(t, "0.1.5")
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	if report.PinUpdated != "" {
		t.Fatalf("compatible pin rewritten to %q", report.PinUpdated)
	}
}

// TestRepairRefreshesCorruptLock verifies repair rewrites an unparsable
// asset lock from the embedded manifest even without --sync.
func TestRepairRefreshesCorruptLock(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	if err := os.WriteFile(filepath.Join(base, assets.LockPath), []byte("not json\n"), 0o644); err != nil {
		t.Fatalf("corrupting lock: %v", err)
	}
	if _, err := Repair(root, false); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if _, err := assets.LoadLock(base); err != nil {
		t.Fatalf("lock still invalid after repair: %v", err)
	}
}

// TestRepairPreservesStaleProvenance verifies a plain (non-sync) repair
// keeps previously-owned assets identifiable in the refreshed lock so a
// later --sync can still remove them, and doctor classifies them as stale
// rather than as local extensions.
func TestRepairPreservesStaleProvenance(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	plantStaleAsset(t, base, "skills/dropped/SKILL.md", []byte("dropped skill\n"))

	// Non-sync repair refreshes the lock but must not drop the ownership
	// record for the stale file.
	if _, err := Repair(root, false); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	lock, err := assets.LoadLock(base)
	if err != nil {
		t.Fatalf("loading lock: %v", err)
	}
	if _, ok := lock.Files["skills/dropped/SKILL.md"]; !ok {
		t.Fatal("non-sync repair dropped prior ownership from the lock")
	}

	// Doctor reports it as stale, not as an unlisted extension.
	_, _, unlisted, stale, err := assets.CheckLock(base)
	if err != nil {
		t.Fatalf("CheckLock() error = %v", err)
	}
	if len(stale) != 1 || stale[0] != "skills/dropped/SKILL.md" {
		t.Fatalf("stale = %v", stale)
	}
	for _, rel := range unlisted {
		if rel == "skills/dropped/SKILL.md" {
			t.Fatal("owned stale file reported as unlisted extension")
		}
	}

	// A later --sync still removes it.
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	if len(report.FilesRemoved) != 1 || report.FilesRemoved[0] != "skills/dropped/SKILL.md" {
		t.Fatalf("FilesRemoved = %v", report.FilesRemoved)
	}
}

// TestRepairReturnsLockReadError verifies a lock that exists but cannot be
// read fails repair instead of silently discarding provenance.
func TestRepairReturnsLockReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-bit fixtures are unreliable on Windows")
	}
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	lockPath := filepath.Join(Base(root), assets.LockPath)
	if err := os.Chmod(lockPath, 0o000); err != nil {
		t.Fatalf("chmod lock: %v", err)
	}
	if _, err := os.ReadFile(lockPath); err == nil {
		t.Skip("runner can read mode-0 files (CAP_DAC_OVERRIDE or elevated user)")
	}
	if _, err := Repair(root, false); err == nil {
		t.Fatal("Repair() succeeded with an unreadable lock")
	}
}

// writeLock persists lock as base's assets.lock.
func writeLock(t *testing.T, base string, lock *assets.Lock) {
	t.Helper()
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatalf("encoding lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, assets.LockPath), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("writing lock: %v", err)
	}
}

// TestRepairSyncProtectsUnownedCollision verifies --sync does not overwrite a
// file at an embedded asset path that the previous lock never owned — a
// local extension colliding with a newly shipped asset.
func TestRepairSyncProtectsUnownedCollision(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	// Simulate an asset shipped by a newer binary colliding with a
	// pre-existing local file: drop the path from the lock and put local
	// content on disk.
	lock, err := assets.LoadLock(base)
	if err != nil {
		t.Fatalf("loading lock: %v", err)
	}
	delete(lock.Files, "skills/README.md")
	writeLock(t, base, lock)
	target := filepath.Join(base, "skills", "README.md")
	if err := os.WriteFile(target, []byte("local extension\n"), 0o644); err != nil {
		t.Fatalf("writing local file: %v", err)
	}
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	for _, rel := range report.FilesSynced {
		if rel == "skills/README.md" {
			t.Fatal("sync overwrote a file the lock never owned")
		}
	}
	found := false
	for _, rel := range report.FilesConflicted {
		if rel == "skills/README.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("collision not reported: %v", report.FilesConflicted)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading local file: %v", err)
	}
	if string(got) != "local extension\n" {
		t.Fatal("local file overwritten by sync")
	}
	// The refreshed lock must not claim the path, or a later sync would
	// treat the file as ox-owned and overwrite it.
	lock, err = assets.LoadLock(base)
	if err != nil {
		t.Fatalf("loading refreshed lock: %v", err)
	}
	if _, ok := lock.Files["skills/README.md"]; ok {
		t.Fatal("refreshed lock claimed the unowned file")
	}
	_, _, unlisted, _, err := assets.CheckLock(base)
	if err != nil {
		t.Fatalf("CheckLock() error = %v", err)
	}
	found = false
	for _, rel := range unlisted {
		if rel == "skills/README.md" {
			found = true
		}
	}
	if !found {
		t.Fatal("unowned file not reported as unlisted extension")
	}
	// Repeated repairs — an intervening non-sync repair and a second
	// sync — keep protecting the file because the lock still lacks it.
	if _, err := Repair(root, false); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if _, err := Repair(root, true); err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	got, err = os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading local file: %v", err)
	}
	if string(got) != "local extension\n" {
		t.Fatal("local file overwritten by a later repair")
	}
}

// TestRepairSyncProtectsDriftWithoutBaseline verifies --sync protects
// drifted files when the lock baseline is missing or corrupt — nothing is
// provably ox-owned, so nothing may be overwritten.
func TestRepairSyncProtectsDriftWithoutBaseline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, lockPath string)
	}{
		{"missing lock", func(t *testing.T, lockPath string) {
			if err := os.Remove(lockPath); err != nil {
				t.Fatalf("removing lock: %v", err)
			}
		}},
		{"corrupt lock", func(t *testing.T, lockPath string) {
			if err := os.WriteFile(lockPath, []byte("not json\n"), 0o644); err != nil {
				t.Fatalf("corrupting lock: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := Init(root); err != nil {
				t.Fatalf("Init() error = %v", err)
			}
			base := Base(root)
			target := filepath.Join(base, "skills", "README.md")
			if err := os.WriteFile(target, []byte("local drift\n"), 0o644); err != nil {
				t.Fatalf("drifting asset: %v", err)
			}
			tc.plant(t, filepath.Join(base, assets.LockPath))
			report, err := Repair(root, true)
			if err != nil {
				t.Fatalf("Repair(sync) error = %v", err)
			}
			found := false
			for _, rel := range report.FilesConflicted {
				if rel == "skills/README.md" {
					found = true
				}
			}
			if !found {
				t.Fatalf("drifted file not reported as conflict: %v", report.FilesConflicted)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("reading drifted file: %v", err)
			}
			if string(got) != "local drift\n" {
				t.Fatal("drifted file overwritten without a usable baseline")
			}
			// The recovered lock must not claim the protected path.
			lock, err := assets.LoadLock(base)
			if err != nil {
				t.Fatalf("loading recovered lock: %v", err)
			}
			if _, ok := lock.Files["skills/README.md"]; ok {
				t.Fatal("recovered lock claimed the unowned file")
			}
		})
	}
}

// TestRepairSyncPreservesFrameworkExtensions verifies --sync rewrites only
// required_version when reconciling the pin, leaving schema-permitted
// extension fields (project.owner, etc.) intact.
func TestRepairSyncPreservesFrameworkExtensions(t *testing.T) {
	setBinaryVersion(t, "0.1.0")
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	target := filepath.Join(Base(root), "framework.yaml")
	content := "schema_version: 1\n" +
		"framework:\n" +
		"  name: ox-agent\n" +
		"  distribution: standalone\n" +
		"  binary: ox-agent\n" +
		"  required_version: \">=9.9.0 <9.10.0\"\n" +
		"project:\n" +
		"  name: demo\n" +
		"  owner: jesse\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("writing framework.yaml: %v", err)
	}
	report, err := Repair(root, true)
	if err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	if report.PinUpdated == "" {
		t.Fatal("incompatible pin not reconciled")
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading framework.yaml: %v", err)
	}
	if !strings.Contains(string(raw), "owner: jesse") {
		t.Fatalf("extension field dropped:\n%s", raw)
	}
	if !strings.Contains(string(raw), report.PinUpdated) {
		t.Fatalf("pin not updated:\n%s", raw)
	}
	framework, err := LoadFramework(root)
	if err != nil {
		t.Fatalf("reparsing framework.yaml: %v", err)
	}
	if framework.Framework.RequiredVersion != report.PinUpdated {
		t.Fatalf("pin = %q, want %q", framework.Framework.RequiredVersion, report.PinUpdated)
	}
}

// TestRepairSyncDropsModifiedStaleOwnership verifies a stale asset retained
// by --sync because of local modifications is reclassified as a local
// extension: the refreshed lock does not keep it flagged as owned stale.
func TestRepairSyncDropsModifiedStaleOwnership(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	base := Base(root)
	plantStaleAsset(t, base, "skills/dropped/SKILL.md", []byte("original\n"))
	if err := os.WriteFile(filepath.Join(base, "skills", "dropped", "SKILL.md"), []byte("edited locally\n"), 0o644); err != nil {
		t.Fatalf("editing stale asset: %v", err)
	}
	if _, err := Repair(root, true); err != nil {
		t.Fatalf("Repair(sync) error = %v", err)
	}
	lock, err := assets.LoadLock(base)
	if err != nil {
		t.Fatalf("loading refreshed lock: %v", err)
	}
	if _, ok := lock.Files["skills/dropped/SKILL.md"]; ok {
		t.Fatal("modified stale asset kept in the lock")
	}
	_, _, unlisted, stale, err := assets.CheckLock(base)
	if err != nil {
		t.Fatalf("CheckLock() error = %v", err)
	}
	for _, rel := range stale {
		if rel == "skills/dropped/SKILL.md" {
			t.Fatal("retained modification still reported as stale")
		}
	}
	found := false
	for _, rel := range unlisted {
		if rel == "skills/dropped/SKILL.md" {
			found = true
		}
	}
	if !found {
		t.Fatal("retained modification not reported as unlisted extension")
	}
}

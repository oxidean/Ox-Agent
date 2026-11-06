package skills

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/oxidean/Ox-Agent/internal/project"
)

// git runs git inside dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "commit.gpgsign=false",
		"-c", "user.email=ox-test@example.invalid",
		"-c", "user.name=ox-test",
		"-c", "init.defaultBranch=main",
	}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// makeRemoteRepo writes a skill package into a temp git repo and returns
// its path plus HEAD sha.
func makeRemoteRepo(t *testing.T, subdir string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	pkg := repo
	if subdir != "" {
		pkg = filepath.Join(repo, filepath.FromSlash(subdir))
	}
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "skill.yaml"), []byte(`schema_version: 1
name: acme/reviewer
kind: extension
description: remote test skill for provenance checks
version: 1.0.0
category: review
compatibility:
  ox_agent: '>=0.1.0 <0.3.0'
  skill_api: 1
inputs:
  schema: schemas/input.schema.json
outputs:
  schema: schemas/output.schema.json
evaluation:
  suite: evals/evals.yaml
  minimum_pass_rate: 0.9
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte(
		"---\nname: acme/reviewer\ndescription: test skill\n---\n# reviewer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkg, "schemas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "schemas", "input.schema.json"), []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "schemas", "output.schema.json"), []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkg, "evals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "evals", "evals.yaml"), []byte("schema_version: 1\nskill: acme/reviewer\ncases: []\nthresholds:\n  schema_pass_rate: 1.0\n  quality_pass_rate: 0.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-m", "initial")
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v\n%s", err, out)
	}
	return repo, strings.TrimSpace(string(out))
}

// TestInstallFromGitSource verifies a git source installs pinned to its
// resolved SHA with provenance and a journaled event.
func TestInstallFromGitSource(t *testing.T) {
	root := initProject(t)
	repo, sha := makeRemoteRepo(t, "")
	entry, err := Install(root, repo, InstallOptions{})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if entry.SHA != sha || entry.Dir != "reviewer" || entry.Source != repo {
		t.Fatalf("entry = %+v want sha %s dir reviewer", entry, sha)
	}
	target := filepath.Join(project.Base(root), "skills", "reviewer")
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not materialized: %v", err)
	}
	lock, err := assets.LoadRemoteLock(project.Base(root))
	if err != nil {
		t.Fatalf("LoadRemoteLock: %v", err)
	}
	got := lock.Skills["reviewer"]
	if got == nil || got.SHA != sha || len(got.Files) != 5 {
		t.Fatalf("lock entry = %+v", got)
	}
	list, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found *Skill
	for _, s := range list {
		if s.Dir == "reviewer" {
			found = s
		}
	}
	if found == nil || found.Source != "remote" || !strings.Contains(found.SourceDetail, repo) {
		t.Fatalf("List entry = %+v", found)
	}
}

// TestInstallWithPath verifies --path selects a package subdirectory.
func TestInstallWithPath(t *testing.T) {
	root := initProject(t)
	repo, _ := makeRemoteRepo(t, "skills/reviewer")
	entry, err := Install(root, repo, InstallOptions{Path: "skills/reviewer"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if entry.Path != "skills/reviewer" {
		t.Fatalf("entry.Path = %q", entry.Path)
	}
	if _, err := os.Stat(filepath.Join(project.Base(root), "skills", "reviewer", "skill.yaml")); err != nil {
		t.Fatalf("package not materialized from subdir: %v", err)
	}
}

// TestInstallRefPinsRequestedRef verifies --ref resolves the requested ref
// rather than remote HEAD.
func TestInstallRefPinsRequestedRef(t *testing.T) {
	root := initProject(t)
	repo, head := makeRemoteRepo(t, "")
	git(t, repo, "tag", "v1.0.0")
	if err := os.WriteFile(filepath.Join(repo, "SKILL.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-m", "second")
	entry, err := Install(root, repo, InstallOptions{Ref: "v1.0.0"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if entry.SHA != head {
		t.Fatalf("entry.SHA = %s, want tagged commit %s", entry.SHA, head)
	}
}

// TestInstallRefusesCollisions verifies embedded directories can never be
// shadowed, non-remote directories are never replaced, and re-install
// requires --force.
func TestInstallRefusesCollisions(t *testing.T) {
	root := initProject(t)
	repo, _ := makeRemoteRepo(t, "")
	if _, err := Install(root, repo, InstallOptions{Name: "specify"}); err == nil || !strings.Contains(err.Error(), "ox-agent-owned") {
		t.Fatalf("embedded shadow error = %v", err)
	}
	// A project-local directory is never silently replaced.
	projectSkill := filepath.Join(project.Base(root), "skills", "local-skill")
	if err := os.MkdirAll(projectSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, repo, InstallOptions{Name: "local-skill"}); err == nil || !strings.Contains(err.Error(), "not remote-installed") {
		t.Fatalf("project collision error = %v", err)
	}
	if _, err := Install(root, repo, InstallOptions{}); err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	if _, err := Install(root, repo, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("reinstall without force error = %v", err)
	}
	// Commit a new revision and re-pin with --force.
	git(t, repo, "commit", "--allow-empty", "-m", "bump")
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	newSHA := strings.TrimSpace(string(out))
	entry, err := Install(root, repo, InstallOptions{Force: true})
	if err != nil {
		t.Fatalf("Install(--force) error = %v", err)
	}
	if entry.SHA != newSHA {
		t.Fatalf("re-pin SHA = %s, want %s", entry.SHA, newSHA)
	}
}

// TestInstallRejectsBadPackages verifies package validation failures.
func TestInstallRejectsBadPackages(t *testing.T) {
	root := initProject(t)
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "commit", "--allow-empty", "-m", "empty")
	if _, err := Install(root, repo, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "skill.yaml") {
		t.Fatalf("missing package error = %v", err)
	}
	repo2, _ := makeRemoteRepo(t, "")
	if _, err := Install(root, repo2, InstallOptions{Name: "BAD NAME"}); err == nil || !strings.Contains(err.Error(), "invalid skill directory") {
		t.Fatalf("bad name error = %v", err)
	}
}

// TestUninstall verifies remote skills uninstall cleanly and non-remote
// directories are refused.
func TestUninstall(t *testing.T) {
	root := initProject(t)
	repo, _ := makeRemoteRepo(t, "")
	if _, err := Install(root, repo, InstallOptions{}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if err := Uninstall(root, "reviewer"); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(project.Base(root), "skills", "reviewer")); !os.IsNotExist(err) {
		t.Fatal("skill directory survived uninstall")
	}
	lock, err := assets.LoadRemoteLock(project.Base(root))
	if err != nil {
		t.Fatalf("LoadRemoteLock: %v", err)
	}
	if _, ok := lock.Skills["reviewer"]; ok {
		t.Fatal("lock entry survived uninstall")
	}
	if err := Uninstall(root, "specify"); err == nil || !strings.Contains(err.Error(), "not remote-installed") {
		t.Fatalf("embedded uninstall error = %v", err)
	}
}

// TestCheckRemoteDetectsDrift verifies file changes and deletions surface
// as findings against the pinned digests.
func TestCheckRemoteDetectsDrift(t *testing.T) {
	root := initProject(t)
	repo, _ := makeRemoteRepo(t, "")
	if _, err := Install(root, repo, InstallOptions{}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	findings, err := CheckRemote(root)
	if err != nil {
		t.Fatalf("CheckRemote: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("clean install produced findings: %v", findings)
	}
	target := filepath.Join(project.Base(root), "skills", "reviewer")
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(target, "skill.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err = CheckRemote(root)
	if err != nil {
		t.Fatalf("CheckRemote: %v", err)
	}
	var drifted, missing, extra bool
	for _, f := range findings {
		switch {
		case strings.Contains(f.Message, "drifted"):
			drifted = true
		case strings.Contains(f.Message, "missing"):
			missing = true
		case strings.Contains(f.Message, "untracked"):
			extra = true
		}
	}
	if !drifted || !missing || !extra {
		t.Fatalf("expected drifted+missing+untracked findings: %v", findings)
	}
}

// TestInstallRejectsEscapingSymlink verifies a package whose symlink
// resolves outside the package is refused.
func TestInstallRejectsEscapingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	root := initProject(t)
	repo, _ := makeRemoteRepo(t, "")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "escape.md")); err != nil {
		if errors.Is(err, syscall.Errno(1314)) {
			t.Skip("symlinks unavailable")
		}
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-m", "add link")
	if _, err := Install(root, repo, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "escaping the package") {
		t.Fatalf("escaping symlink error = %v", err)
	}
}

// TestInstallRejectsEscapingPath verifies --path cannot resolve outside the
// checkout, whether lexically (../..) or through a symlinked component.
func TestInstallRejectsEscapingPath(t *testing.T) {
	root := initProject(t)
	repo, _ := makeRemoteRepo(t, "")
	for _, path := range []string{"..", "../..", "../../.."} {
		if _, err := Install(root, repo, InstallOptions{Path: path}); err == nil || !strings.Contains(err.Error(), "escapes the source repository") {
			t.Fatalf("Install(path=%q) error = %v, want escape rejection", path, err)
		}
	}
}

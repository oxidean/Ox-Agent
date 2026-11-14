package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitOrSkip returns the git binary or skips — doctor's source-control check
// shells git, so these tests need a real one.
func gitOrSkip(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	return git
}

// runGit executes git inside dir, failing the test on error.
func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// findCheck returns the named check, or nil when absent.
func findCheck(report *DoctorReport, name string) *Check {
	for i := range report.Checks {
		if report.Checks[i].Name == name {
			return &report.Checks[i]
		}
	}
	return nil
}

// hasWarning reports whether any doctor warning contains substr.
func hasWarning(report *DoctorReport, substr string) bool {
	for _, w := range report.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// TestDoctorSourceControlNotARepo verifies a project outside git gets the
// nudge as a warning, never a failed check.
func TestDoctorSourceControlNotARepo(t *testing.T) {
	gitOrSkip(t)
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	report := Doctor(root)
	if !report.Healthy() {
		t.Fatalf("unhealthy outside git: %+v", report.Checks)
	}
	if !hasWarning(report, "work tree") {
		t.Fatalf("no source-control warning outside git: %v", report.Warnings)
	}
}

// TestDoctorSourceControlIgnored verifies a repo-level ignore rule covering
// .ox-agent fails the source-control check — the spec trail would never
// commit, which is a defect, not a preference.
func TestDoctorSourceControlIgnored(t *testing.T) {
	git := gitOrSkip(t)
	root := t.TempDir()
	runGit(t, git, root, "init", "-q")
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".ox-agent/\n"), 0o644); err != nil {
		t.Fatalf("writing .gitignore: %v", err)
	}
	report := Doctor(root)
	check := findCheck(report, "source control")
	if check == nil || check.OK {
		t.Fatalf("ignored .ox-agent not flagged: %+v", report.Checks)
	}
	if !strings.Contains(check.Detail, ".ox-agent") {
		t.Fatalf("check detail missing paths: %s", check.Detail)
	}
}

// TestDoctorSourceControlGlobRule verifies a contents-only glob rule
// (`.ox-agent/work/**`) still fails the check — dir-level probes miss it,
// so the check must probe artifact paths beneath the protected dirs.
func TestDoctorSourceControlGlobRule(t *testing.T) {
	git := gitOrSkip(t)
	root := t.TempDir()
	runGit(t, git, root, "init", "-q")
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".ox-agent/work/**\n"), 0o644); err != nil {
		t.Fatalf("writing .gitignore: %v", err)
	}
	report := Doctor(root)
	check := findCheck(report, "source control")
	if check == nil || check.OK {
		t.Fatalf("contents-only glob not flagged: %+v", report.Checks)
	}
}

// TestDoctorSourceControlTrackedIgnored verifies a rule added after the
// trail is staged still fails — plain check-ignore skips indexed paths, so
// the check must evaluate rules independently of the index.
func TestDoctorSourceControlTrackedIgnored(t *testing.T) {
	git := gitOrSkip(t)
	root := t.TempDir()
	runGit(t, git, root, "init", "-q")
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	runGit(t, git, root, "add", ".ox-agent")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".ox-agent/state/\n"), 0o644); err != nil {
		t.Fatalf("writing .gitignore: %v", err)
	}
	report := Doctor(root)
	check := findCheck(report, "source control")
	if check == nil || check.OK {
		t.Fatalf("rule over tracked paths not flagged: %+v", report.Checks)
	}
}

// TestDoctorSourceControlPerDirRule verifies a rule covering a single
// sentinel dir is caught — only the per-dir .gitkeep probes reach paths
// like `.ox-agent/state/snapshots/`, which carry no other artifact probe.
func TestDoctorSourceControlPerDirRule(t *testing.T) {
	git := gitOrSkip(t)
	for _, rule := range []string{
		".ox-agent/state/snapshots/",
		".ox-agent/work/archived/",
		".ox-agent/discussions/",
		".ox-agent/state/input/",
	} {
		root := t.TempDir()
		runGit(t, git, root, "init", "-q")
		if err := Init(root); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(rule+"\n"), 0o644); err != nil {
			t.Fatalf("writing .gitignore: %v", err)
		}
		check := findCheck(Doctor(root), "source control")
		if check == nil || check.OK {
			t.Fatalf("per-dir rule %q not flagged", rule)
		}
	}
}

// TestDoctorFreshClone verifies a git clone of a committed project passes
// doctor: .gitkeep sentinels carry the required-but-initially-empty dirs,
// and the gitignored runtime/ tree is never required.
func TestDoctorFreshClone(t *testing.T) {
	git := gitOrSkip(t)
	src := t.TempDir()
	runGit(t, git, src, "init", "-q")
	if err := Init(src); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	runGit(t, git, src, "add", "-A")
	runGit(t, git, src, "-c", "user.email=ox-agent@example.invalid", "-c", "user.name=ox-agent test", "commit", "-qm", "scaffold")
	parent := t.TempDir()
	runGit(t, git, parent, "clone", "-q", src, "clone")
	report := Doctor(filepath.Join(parent, "clone"))
	if !report.Healthy() {
		t.Fatalf("fresh clone unhealthy: %+v", report.Checks)
	}
}

// TestDoctorSourceControlUntracked verifies an inited-but-uncommitted project
// passes the check with the commit nudge as a warning, and that staging
// .ox-agent clears it.
func TestDoctorSourceControlUntracked(t *testing.T) {
	git := gitOrSkip(t)
	root := t.TempDir()
	runGit(t, git, root, "init", "-q")
	if err := Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	report := Doctor(root)
	check := findCheck(report, "source control")
	if check == nil || !check.OK {
		t.Fatalf("untracked project should pass, not fail: %+v", report.Checks)
	}
	if !hasWarning(report, "tracked by git") {
		t.Fatalf("no untracked-trail warning: %v", report.Warnings)
	}
	runGit(t, git, root, "add", ".ox-agent")
	report = Doctor(root)
	if hasWarning(report, "tracked by git") {
		t.Fatalf("untracked warning persists after git add: %v", report.Warnings)
	}
	if !report.Healthy() {
		t.Fatalf("unhealthy after tracking: %+v", report.Checks)
	}
}

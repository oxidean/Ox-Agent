package repochange

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCleanBaselineRequiresCleanGitCommit(t *testing.T) {
	root := initRepo(t, map[string]string{"tracked.txt": "initial"})
	if baseline := CleanBaseline(root); len(baseline) != 40 && len(baseline) != 64 {
		t.Fatalf("CleanBaseline() = %q, want a commit ID", baseline)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if baseline := CleanBaseline(root); baseline != "" {
		t.Fatalf("dirty CleanBaseline() = %q, want empty", baseline)
	}
	if err := git(root, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if baseline := CleanBaseline(root); baseline != "" {
		t.Fatalf("staged CleanBaseline() = %q, want empty", baseline)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if baseline := CleanBaseline(root); baseline != "" {
		t.Fatalf("untracked CleanBaseline() = %q, want empty", baseline)
	}
}

func TestCleanBaselineUnavailableWithoutRepositoryOrCommit(t *testing.T) {
	root := t.TempDir()
	if baseline := CleanBaseline(root); baseline != "" {
		t.Fatalf("non-Git CleanBaseline() = %q, want empty", baseline)
	}
	if err := git(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if baseline := CleanBaseline(root); baseline != "" {
		t.Fatalf("unborn CleanBaseline() = %q, want empty", baseline)
	}
}

func TestChangedPathsIncludesTrackedUntrackedDeletesAndRenames(t *testing.T) {
	root := initRepo(t, map[string]string{
		"covered/modify.go":      "old",
		"covered/delete.go":      "delete",
		"covered/rename.go":      "rename",
		"generated/tracked.go":   "generated",
		".ox-agent/work/item.md": "work",
		".ox-agent/state/events": "state",
		".ox-agent/runtime/run":  "runtime",
		".gitignore":             "ignored/\n",
	})
	baseline := CleanBaseline(root)
	if baseline == "" {
		t.Fatal("baseline unavailable for clean repository")
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("covered/modify.go", "modified")
	write("covered/committed.go", "committed")
	if err := git(root, "add", "covered/committed.go"); err != nil {
		t.Fatal(err)
	}
	if err := git(root, "-c", "user.name=Ox-Agent Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "later change"); err != nil {
		t.Fatal(err)
	}
	write("covered/staged.go", "staged")
	if err := git(root, "add", "covered/staged.go"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "covered/delete.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "covered/rename.go"), filepath.Join(root, "covered/renamed.go")); err != nil {
		t.Fatal(err)
	}
	if err := git(root, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	write("covered/untracked.go", "untracked")
	write(".ox-agent/work/new.md", "work")
	write(".ox-agent/state/new.ndjson", "state")
	write(".ox-agent/runtime/new.json", "runtime")
	write("ignored/new.bin", "ignored")
	write("generated/tracked.go", "regenerated")

	got, err := ChangedPaths(root, baseline)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	want := []string{
		"covered/committed.go",
		"covered/delete.go",
		"covered/modify.go",
		"covered/rename.go",
		"covered/renamed.go",
		"covered/staged.go",
		"covered/untracked.go",
		"generated/tracked.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedPaths() = %v, want %v", got, want)
	}
}

func TestChangedPathsUsesProjectRelativePathsInNestedWorktree(t *testing.T) {
	repo := initRepo(t, map[string]string{
		"root.go":                        "initial",
		"project/internal/tracked.go":    "initial",
		"project/.ox-agent/work/item.md": "work",
	})
	project := filepath.Join(repo, "project")
	baseline := CleanBaseline(project)
	if baseline == "" {
		t.Fatal("baseline unavailable for clean nested project")
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/tracked.go", "changed")
	write("internal/new.go", "untracked")
	write(".ox-agent/work/item.md", "changed work")
	write(".ox-agent/work/new.md", "untracked work")
	if err := os.WriteFile(filepath.Join(repo, "root.go"), []byte("changed outside project"), 0o644); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(repo, "sibling")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "untracked.go"), []byte("outside project"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ChangedPaths(project, baseline)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	want := []string{"internal/new.go", "internal/tracked.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedPaths() = %v, want project-relative %v", got, want)
	}
}

func TestChangedPathsRejectsInvalidOrMissingBaseline(t *testing.T) {
	root := initRepo(t, map[string]string{"tracked.txt": "initial"})
	for _, baseline := range []string{"--help", strings.Repeat("0", 40)} {
		if _, err := ChangedPaths(root, baseline); err == nil {
			t.Errorf("ChangedPaths(%q) succeeded, want error", baseline)
		}
	}
}

func initRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := git(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := git(root, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := git(root, "-c", "user.name=Ox-Agent Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	return root
}

func git(root string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

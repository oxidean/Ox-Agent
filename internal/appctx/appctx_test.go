package appctx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
)

// initProject scaffolds a temp project for context tests.
func initProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := project.Init(root); err != nil {
		t.Fatalf("project.Init() error = %v", err)
	}
	return root
}

// TestIndexClassifiesKinds verifies the index covers context and discussions
// files with the right kinds.
func TestIndexClassifiesKinds(t *testing.T) {
	root := initProject(t)
	base := filepath.Join(root, ".ox-agent")
	for path, content := range map[string]string{
		"context/standards/base.md": "# standards\n",
		"context/decisions/0001.md": "# use go\n",
		"discussions/d-001.yaml":    "thread_id: d-001\n",
		"context/notes/scratch.txt": "misc\n",
	} {
		full := filepath.Join(base, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	index, err := Build(root)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	kinds := map[string]string{}
	for _, f := range index.Files {
		kinds[f.Path] = f.Kind
	}
	want := map[string]string{
		"context/project.yaml":      "project",
		"context/standards/base.md": "standard",
		"context/decisions/0001.md": "decision",
		"discussions/d-001.yaml":    "discussion",
		"context/notes/scratch.txt": "other",
	}
	for path, kind := range want {
		if kinds[path] != kind {
			t.Fatalf("kind(%s) = %q, want %q", path, kinds[path], kind)
		}
	}
	if _, changed, _, err := Write(root, index); err != nil || changed != 0 {
		t.Fatalf("first Write() = changed %d, err %v; want 0 additions counted as new", changed, err)
	}
}

// TestCompileBuildsContract verifies the compiled packet satisfies the
// packet contract fields.
func TestCompileBuildsContract(t *testing.T) {
	root := initProject(t)
	item, err := state.Start(root, "packet target")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	packet, err := Compile(root, item, "specify", "specification", 4000, "runtime/runs/run_x/output.json")
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if packet.Skill != "ox-agent/specify" || packet.WorkItemID != item.ID || packet.Stage != "specification" {
		t.Fatalf("packet identity wrong: %+v", packet)
	}
	if packet.Constraints.TokenBudget != 4000 || packet.Output.Schema == "" || packet.Output.Path == "" {
		t.Fatalf("packet constraints/output wrong: %+v", packet)
	}
	data, err := packet.Bytes()
	if err != nil {
		t.Fatalf("Bytes() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("packet not JSON: %v", err)
	}
	if _, err := Compile(root, item, "no-such-skill", "specification", 0, ""); err == nil {
		t.Fatal("Compile() with unknown skill succeeded; want error")
	}
}

// TestCompileAcceptsNamespacedSkill verifies Compile resolves the namespaced
// skill reference form packets and events use.
func TestCompileAcceptsNamespacedSkill(t *testing.T) {
	root := initProject(t)
	item, err := state.Start(root, "namespaced skill")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	packet, err := Compile(root, item, "ox-agent/specify", "specification", 0, "")
	if err != nil {
		t.Fatalf("Compile() with namespaced skill error = %v", err)
	}
	if packet.Skill != "ox-agent/specify" || packet.SkillDir != "skills/specify" {
		t.Fatalf("packet skill fields wrong: %+v", packet)
	}
}

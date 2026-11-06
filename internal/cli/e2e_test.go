package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/state"
)

// TestEndToEndLifecycle exercises the full product loop in-process against a
// real temp project: init → integrate → start → per-stage run/collect →
// advance → human-gated ship → pipelines → skills surfaces → doctor → events.
func TestEndToEndLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, file := range []string{".gitignore", ".gitattributes", "assets.lock"} {
		if _, err := os.Stat(filepath.Join(dir, ".ox-agent", file)); err != nil {
			t.Fatalf("init missing %s: %v", file, err)
		}
	}
	if _, err := runCLI(t, "integrate"); err != nil {
		t.Fatalf("integrate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("integrate did not write AGENTS.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents", "skills")); err != nil {
		t.Fatalf("integrate did not materialize .agents/skills: %v", err)
	}

	// A git work tree lets the shipped transition record provenance.
	git(t, dir, "init", "-q")
	git(t, dir, "-c", "user.email=e2e@test", "-c", "user.name=e2e", "commit", "-qm", "seed", "--allow-empty")

	out, err := runCLI(t, "start", "E2E feature")
	if err != nil || !strings.Contains(out, "WI-0001") {
		t.Fatalf("start: %q, %v", out, err)
	}
	wi := "WI-0001"

	// Walk the profile stages: pump every dispatch, advance between stages,
	// cross the human-gated ship edge with an approver.
	for guard := 0; guard < 40; guard++ {
		item := mustItem(t, dir, wi)
		if item.CurrentStage == "retrospective" {
			break
		}
		pumpStageRuns(t, dir, wi)
		if _, err := runCLI(t, "advance", wi); err != nil {
			if !strings.Contains(err.Error(), "no lifecycle path") && !strings.Contains(err.Error(), "approval") {
				t.Fatalf("advance: %v", err)
			}
			if _, err := runCLI(t, "transition", wi, "shipped", "--by", "tester"); err != nil {
				t.Fatalf("transition shipped: %v", err)
			}
		}
	}
	if item := mustItem(t, dir, wi); item.State != "retrospecting" {
		t.Fatalf("state = %q, want retrospecting", item.State)
	}
	pumpStageRuns(t, dir, wi) // retrospective stage skills

	// Pipelines: review and ship drive their own step DAGs.
	pumpPipeline(t, dir, wi, "review")
	pumpPipeline(t, dir, wi, "ship")

	// Skills surfaces.
	out, err = runCLI(t, "skills", "list")
	if err != nil || !strings.Contains(out, "discover-context") || !strings.Contains(out, "current") {
		t.Fatalf("skills list: %q, %v", out, err)
	}
	out, err = runCLI(t, "skills", "check")
	if err != nil {
		t.Fatalf("skills check: %q, %v", out, err)
	}
	// Drift a skill file: update reports it skipped, --yes restores it.
	skillDoc := filepath.Join(dir, ".ox-agent", "skills", "specify", "SKILL.md")
	data, _ := os.ReadFile(skillDoc)
	if err := os.WriteFile(skillDoc, append(data, "drift\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "skills", "update")
	if err != nil || !strings.Contains(out, "skipped") {
		t.Fatalf("skills update dry-run: %q, %v", out, err)
	}
	if _, err := runCLI(t, "skills", "update", "--yes"); err != nil {
		t.Fatalf("skills update --yes: %v", err)
	}
	out, err = runCLI(t, "skills", "list")
	if err != nil || !strings.Contains(out, "current") {
		t.Fatalf("skills list after update: %q, %v", out, err)
	}

	// Drift a non-skill asset: init --repair --sync rewrites it.
	schema := filepath.Join(dir, ".ox-agent", "schemas", "config.schema.json")
	data, _ = os.ReadFile(schema)
	if err := os.WriteFile(schema, append(data, "\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "init", "--repair", "--sync")
	if err != nil || !strings.Contains(out, "synced asset: schemas/config.schema.json") {
		t.Fatalf("init --repair --sync: %q, %v", out, err)
	}

	// Doctor is healthy and the journal tells the whole story.
	out, err = runCLI(t, "doctor")
	if err != nil || !strings.Contains(out, "healthy") {
		t.Fatalf("doctor: %q, %v", out, err)
	}
	out, err = runCLI(t, "events")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, want := range []string{"work_item.created", "work_item.transitioned", "skill.completed", "pipeline.completed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("events missing %q:\n%s", want, out)
		}
	}
	out, err = runCLI(t, "events", wi, "--type", "work_item.transitioned", "--json")
	if err != nil || !strings.Contains(out, `"to":"shipped"`) || !strings.Contains(out, "git_branch") {
		t.Fatalf("filtered events: %q, %v", out, err)
	}
}

// pumpStageRuns dispatches and collects every skill the current stage still
// needs. collect auto-advances on stage completion; gate failures surface as
// collect errors the caller resolves via transition.
func pumpStageRuns(t *testing.T, root, wi string) {
	t.Helper()
	for guard := 0; guard < 8; guard++ {
		out, err := runCLI(t, "run", wi)
		if err != nil {
			if strings.Contains(err.Error(), "already collected") {
				return
			}
			t.Fatalf("run: %v", err)
		}
		runID := ""
		for _, field := range strings.Fields(out) {
			if strings.HasPrefix(field, "run_") {
				runID = field
				break
			}
		}
		if runID == "" {
			t.Fatalf("run output has no run id:\n%s", out)
		}
		writeRunOutput(t, root, runID)
		if _, err := runCLI(t, "run", "collect", runID); err != nil {
			if !strings.Contains(err.Error(), "no lifecycle path") && !strings.Contains(err.Error(), "approval") {
				t.Fatalf("collect %s: %v", runID, err)
			}
		}
	}
}

// pumpPipeline runs a pipeline to completion: invoke, collect pending runs,
// repeat until the command reports the pipeline complete.
func pumpPipeline(t *testing.T, root, wi, name string) {
	t.Helper()
	for guard := 0; guard < 20; guard++ {
		out, err := runCLI(t, name, wi)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(out, "complete") {
			return
		}
		for _, runID := range pendingRuns(t, root) {
			writeRunOutput(t, root, runID)
			if _, err := runCLI(t, "run", "collect", runID); err != nil {
				t.Fatalf("collect %s: %v", runID, err)
			}
		}
	}
	t.Fatalf("pipeline %s did not complete", name)
}

// pendingRuns returns run ids whose run.json still reads pending.
func pendingRuns(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".ox-agent", "runtime", "runs"))
	if err != nil {
		t.Fatalf("reading runs: %v", err)
	}
	var out []string
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, ".ox-agent", "runtime", "runs", entry.Name(), "run.json"))
		if err != nil {
			continue
		}
		var run struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &run) == nil && run.Status == "pending" {
			out = append(out, entry.Name())
		}
	}
	return out
}

// writeRunOutput drops a schema-valid output.json into the run directory,
// matching the run's declared skill.
func writeRunOutput(t *testing.T, root, runID string) {
	t.Helper()
	runDir := filepath.Join(root, ".ox-agent", "runtime", "runs", runID)
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatalf("reading run.json: %v", err)
	}
	var run struct {
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatalf("parsing run.json: %v", err)
	}
	output, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"invocation_id":  "e2e-" + runID,
		"skill":          run.Skill,
		"status":         "complete",
		"verdict":        "pass",
		"summary":        "e2e",
		"artifacts":      []any{},
		"findings":       []any{},
		"next_action":    map[string]any{"type": "advance_stage", "reason": "ship it"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "output.json"), output, 0o644); err != nil {
		t.Fatalf("writing output.json: %v", err)
	}
}

// mustItem resolves a work item or fails the test.
func mustItem(t *testing.T, root, ref string) *state.WorkItem {
	t.Helper()
	item, err := state.Resolve(root, ref)
	if err != nil {
		t.Fatalf("resolve %s: %v", ref, err)
	}
	return item
}

// git runs git inside dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

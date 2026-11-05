package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
)

// initProject scaffolds a temp project and opens one work item.
func initProject(t *testing.T) (string, *state.WorkItem) {
	t.Helper()
	root := t.TempDir()
	if err := project.Init(root); err != nil {
		t.Fatalf("project.Init() error = %v", err)
	}
	item, err := state.Start(root, "harness target")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	return root, item
}

// writeOutput drops a skill output document into the run directory.
func writeOutput(t *testing.T, root, runID string, output map[string]any) {
	t.Helper()
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("encoding output: %v", err)
	}
	path := filepath.Join(runsDir(root), runID, "output.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing output.json: %v", err)
	}
}

// validOutput returns a schema-valid skill output for testing.
func validOutput(skill string) map[string]any {
	return map[string]any{
		"schema_version": 1,
		"invocation_id":  "inv_test",
		"skill":          "ox-agent/" + skill,
		"status":         "complete",
		"verdict":        "pass",
		"summary":        "done",
		"artifacts":      []any{},
		"findings":       []any{},
		"next_action":    map[string]any{"type": "advance_stage"},
	}
}

// TestDispatchCollectRoundtrip verifies a manual run dispatches a packet and
// collects a valid output, advancing the work item.
func TestDispatchCollectRoundtrip(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if run.Status != "pending" || run.Adapter != "manual" {
		t.Fatalf("run = %+v", run)
	}
	if _, err := os.Stat(filepath.Join(run.Dir, "packet.json")); err != nil {
		t.Fatalf("packet not written: %v", err)
	}
	writeOutput(t, root, run.RunID, validOutput("specify"))
	run, output, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "collected" {
		t.Fatalf("run status = %s", run.Status)
	}
	if output["verdict"] != "pass" {
		t.Fatalf("output verdict = %v", output["verdict"])
	}
	events, err := state.EventsFor(root, item.ID, 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	var invoked, completed bool
	for _, e := range events {
		if e.Type == "skill.invoked" {
			invoked = true
		}
		if e.Type == "skill.completed" {
			completed = true
		}
	}
	if !invoked || !completed {
		t.Fatalf("expected skill.invoked + skill.completed events: %v", events)
	}
}

// TestCollectRejectsInvalidOutput verifies a schema-invalid output fails the
// run and journals skill.failed.
func TestCollectRejectsInvalidOutput(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, run.RunID, map[string]any{"nonsense": true})
	run, _, err = Collect(root, run.RunID)
	if err == nil {
		t.Fatal("Collect() on invalid output succeeded; want error")
	}
	if run.Status != "failed" {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
}

// TestNextSkillSkipsCollected verifies stage skill resolution skips skills
// with collected runs.
func TestNextSkillSkipsCollected(t *testing.T) {
	root, item := initProject(t)
	skills := map[string][]string{"specification": {"specify", "adr"}}
	skill, err := NextSkill(root, item, "specification", skills)
	if err != nil || skill != "specify" {
		t.Fatalf("NextSkill() = %q, %v; want specify", skill, err)
	}
	run, err := Dispatch(root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, run.RunID, validOutput("specify"))
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	skill, err = NextSkill(root, item, "specification", skills)
	if err != nil || skill != "adr" {
		t.Fatalf("NextSkill() = %q, %v; want adr", skill, err)
	}
}

// TestCollectBlocksOnBlockedVerdict verifies a blocked skill output blocks
// the work item.
func TestCollectBlocksOnBlockedVerdict(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["verdict"] = "blocked"
	out["status"] = "blocked"
	out["next_action"] = map[string]any{"type": "escalate", "reason": "spec unclear"}
	writeOutput(t, root, run.RunID, out)
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if item.State != "blocked" || item.BlockedFrom == "" {
		t.Fatalf("item = %+v, want blocked", item)
	}
}

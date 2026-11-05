package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
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
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
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
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
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
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
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

// TestCollectKeepsStageUntilAllSkillsCollected verifies a passing collect on a
// multi-skill stage leaves the stage so remaining skills stay selectable, and
// the stage advances only once the last skill collects.
func TestCollectKeepsStageUntilAllSkillsCollected(t *testing.T) {
	root, item := initProject(t)
	item.CurrentStage = "specification"
	item.State = "specifying"
	if err := item.Save(); err != nil {
		t.Fatalf("seeding stage: %v", err)
	}
	skills := map[string][]string{"specification": {"specify", "adr"}}

	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, run.RunID, validOutput("specify"))
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if item.CurrentStage != "specification" {
		t.Fatalf("stage advanced early to %q with adr uncollected", item.CurrentStage)
	}
	skill, err := NextSkill(root, item, "specification", skills)
	if err != nil || skill != "adr" {
		t.Fatalf("NextSkill() = %q, %v; want adr", skill, err)
	}

	run, err = Dispatch(context.Background(), root, item, DispatchRequest{Skill: "adr", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, run.RunID, validOutput("adr"))
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if item.CurrentStage != "planning" {
		t.Fatalf("stage = %q after last skill collected, want planning", item.CurrentStage)
	}
}

// TestPipelineAdvancesOnceOnCompletion verifies pipeline steps do not advance
// the stage individually; the stage moves once when the pipeline completes.
func TestPipelineAdvancesOnceOnCompletion(t *testing.T) {
	root, item := initProject(t)
	dispatch := func(skill, stepID string) *Run {
		t.Helper()
		run, err := Dispatch(context.Background(), root, item, DispatchRequest{
			Skill: skill, Stage: "verification", Pipeline: "review", StepID: stepID})
		if err != nil {
			t.Fatalf("Dispatch(%s) error = %v", stepID, err)
		}
		return run
	}
	first := dispatch("code-review", "review")
	writeOutput(t, root, first.RunID, validOutput("code-review"))
	if _, _, err := Collect(root, first.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	item, err := state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if item.CurrentStage != "intake" {
		t.Fatalf("stage moved to %q mid-pipeline; want intake until pipeline completes", item.CurrentStage)
	}

	second := dispatch("verify", "verify")
	writeOutput(t, root, second.RunID, validOutput("verify"))
	if _, _, err := Collect(root, second.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if item.CurrentStage != "discovery" {
		t.Fatalf("stage = %q after pipeline completion, want discovery (single advance)", item.CurrentStage)
	}
}

// TestDispatchRejectsUnknownAdapter verifies an adapter outside the supported
// set fails before any run artifacts are written. Protocol adapters are added
// per consumer (named integrations), never as generic placeholders.
func TestDispatchRejectsUnknownAdapter(t *testing.T) {
	root, item := initProject(t)
	integrations := filepath.Join(root, ".ox-agent", "integrations")
	if err := os.WriteFile(filepath.Join(integrations, "api.yaml"), []byte(
		"name: api\nadapter: api\n"), 0o644); err != nil {
		t.Fatalf("writing api integration: %v", err)
	}
	_, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "api"})
	if err == nil {
		t.Fatal("Dispatch() with api adapter succeeded; want error")
	}
	if !strings.Contains(err.Error(), "unknown adapter") {
		t.Fatalf("Dispatch() error = %q, want unknown adapter", err)
	}
	entries, readErr := os.ReadDir(runsDir(root))
	if readErr == nil && len(entries) != 0 {
		t.Fatalf("run artifacts written despite rejected adapter: %v", entries)
	}
}

// TestSpawnCLIResolvesRunDirPlaceholders verifies {packet}/{output}/{run_dir}
// expand to paths under the real run directory so the spawned command (whose
// working directory is the project root, not .ox-agent) can reach them.
// spawn-cli runs through `sh -c`, which only exists on Unix platforms.
func TestSpawnCLIResolvesRunDirPlaceholders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("spawn-cli runs commands through sh -c, which is unavailable on Windows")
	}
	root, item := initProject(t)
	integrations := filepath.Join(root, ".ox-agent", "integrations")
	if err := os.WriteFile(filepath.Join(integrations, "copy.yaml"), []byte(
		"name: copy\nadapter: spawn-cli\ncommand: 'cp \"{packet}\" \"{output}\"'\ntimeout_seconds: 30\n"), 0o644); err != nil {
		t.Fatalf("writing spawn integration: %v", err)
	}
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "copy"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if run.Adapter != "spawn-cli" {
		t.Fatalf("run adapter = %s", run.Adapter)
	}
	if _, err := os.Stat(filepath.Join(run.Dir, "output.json")); err != nil {
		t.Fatalf("spawned command did not write output.json into the run dir: %v", err)
	}
}

// TestCollectRejectsUnknownNextAction verifies an unrecognized
// next_action.type fails the run instead of silently following the verdict.
func TestCollectRejectsUnknownNextAction(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["next_action"] = map[string]any{"type": "advance-stage"}
	writeOutput(t, root, run.RunID, out)
	if _, _, err := Collect(root, run.RunID); err == nil {
		t.Fatal("Collect() with unknown next_action.type succeeded; want error")
	}
}

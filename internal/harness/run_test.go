package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/input"
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
		"next_action":    map[string]any{"type": "advance_stage", "reason": "unit of work done"},
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

// TestCollectRetryLeavesSkillRedispatchable verifies a needs_rework/retry
// output finishes the run without counting it as done: the skill stays
// selectable for another pass and the work item does not advance.
func TestCollectRetryLeavesSkillRedispatchable(t *testing.T) {
	root, item := initProject(t)
	skills := map[string][]string{"specification": {"specify", "adr"}}
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["verdict"] = "needs_rework"
	out["next_action"] = map[string]any{"type": "retry", "reason": "gaps found"}
	writeOutput(t, root, run.RunID, out)
	run, _, err = Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "retry" {
		t.Fatalf("run status = %s; want retry", run.Status)
	}
	skill, err := NextSkill(root, item, "specification", skills)
	if err != nil || skill != "specify" {
		t.Fatalf("NextSkill() = %q, %v; want specify re-selectable", skill, err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if item.CurrentStage != "intake" {
		t.Fatalf("stage advanced on retry: %s", item.CurrentStage)
	}
}

// TestCollectEscalateBeatsRetry verifies a rework verdict combined with an
// escalate action blocks the work item while keeping the run unresolved:
// the run is recorded as retry so the skill stays dispatchable once the
// item is unblocked.
func TestCollectEscalateBeatsRetry(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["verdict"] = "needs_rework"
	out["next_action"] = map[string]any{"type": "escalate", "reason": "needs human decision"}
	writeOutput(t, root, run.RunID, out)
	run, _, err = Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "retry" {
		t.Fatalf("run status = %s; want retry so the skill stays unresolved", run.Status)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if item.State != "blocked" {
		t.Fatalf("escalated item state = %s; want blocked", item.State)
	}
}

// TestUnblockLeavesEscalatedSkillRedispatchable verifies an escalated rework
// run does not count as done after the work item is unblocked: the skill
// stays selectable and the stage is not treated as complete.
func TestUnblockLeavesEscalatedSkillRedispatchable(t *testing.T) {
	root, item := initProject(t)
	item.CurrentStage = "specification"
	item.State = "specifying"
	if err := item.Save(); err != nil {
		t.Fatalf("seeding stage: %v", err)
	}
	skills := map[string][]string{"specification": {"specify"}}
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["verdict"] = "needs_rework"
	out["next_action"] = map[string]any{"type": "escalate", "reason": "needs human decision"}
	writeOutput(t, root, run.RunID, out)
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := item.Unblock(root, "cli", "human resolved the question"); err != nil {
		t.Fatalf("Unblock() error = %v", err)
	}
	skill, err := NextSkill(root, item, "specification", skills)
	if err != nil || skill != "specify" {
		t.Fatalf("NextSkill() = %q, %v; want specify re-selectable after unblock", skill, err)
	}
	complete, err := stageComplete(root, item)
	if err != nil {
		t.Fatalf("stageComplete() error = %v", err)
	}
	if complete {
		t.Fatal("stage reported complete while the escalated skill is unresolved")
	}
}

// TestUnblockPreservesBlockedRetry verifies a blocked run that also
// requested retry does not count as done after the work item is unblocked:
// the run records retry, the blocked transition still applies, and the
// skill stays selectable.
func TestUnblockPreservesBlockedRetry(t *testing.T) {
	root, item := initProject(t)
	item.CurrentStage = "specification"
	item.State = "specifying"
	if err := item.Save(); err != nil {
		t.Fatalf("seeding stage: %v", err)
	}
	skills := map[string][]string{"specification": {"specify"}}
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["status"] = "blocked"
	out["next_action"] = map[string]any{"type": "retry", "reason": "missing spec detail, needs another pass"}
	writeOutput(t, root, run.RunID, out)
	run, _, err = Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "retry" {
		t.Fatalf("run status = %s; want retry so the skill stays unresolved", run.Status)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if item.State != "blocked" {
		t.Fatalf("blocked output item state = %s; want blocked", item.State)
	}
	if err := item.Unblock(root, "cli", "human supplied the detail"); err != nil {
		t.Fatalf("Unblock() error = %v", err)
	}
	skill, err := NextSkill(root, item, "specification", skills)
	if err != nil || skill != "specify" {
		t.Fatalf("NextSkill() = %q, %v; want specify re-selectable after unblock", skill, err)
	}
	complete, err := stageComplete(root, item)
	if err != nil {
		t.Fatalf("stageComplete() error = %v", err)
	}
	if complete {
		t.Fatal("stage reported complete while the blocked retry is unresolved")
	}
}

// TestLatestRunOutcomeWins verifies completion checks consider only the
// newest run per skill: a skill that collected once but was re-dispatched
// and escalated to rework is unresolved again after unblock.
func TestLatestRunOutcomeWins(t *testing.T) {
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

	retry, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("re-Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["verdict"] = "needs_rework"
	out["next_action"] = map[string]any{"type": "escalate", "reason": "needs human decision"}
	writeOutput(t, root, retry.RunID, out)
	if _, _, err := Collect(root, retry.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := item.Unblock(root, "cli", "human resolved the question"); err != nil {
		t.Fatalf("Unblock() error = %v", err)
	}
	skill, err := NextSkill(root, item, "specification", skills)
	if err != nil || skill != "specify" {
		t.Fatalf("NextSkill() = %q, %v; want specify re-selectable despite older collected run", skill, err)
	}
	complete, err := stageComplete(root, item)
	if err != nil {
		t.Fatalf("stageComplete() error = %v", err)
	}
	if complete {
		t.Fatal("stage reported complete while specify's latest run is unresolved")
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

// TestCollectRejectsUnknownNextAction verifies collect fails a run whose
// output declares an out-of-contract next_action.type — schema validation
// catches it before any outcome is applied.
func TestCollectRejectsUnknownNextAction(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["next_action"] = map[string]any{"type": "bogus_action"}
	writeOutput(t, root, run.RunID, out)
	run, _, err = Collect(root, run.RunID)
	if err == nil {
		t.Fatal("Collect() accepted an unknown next_action.type; want error")
	}
	if run.Status != "failed" {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
}

// TestApplyOutcomeRejectsUnknownAction verifies the defense-in-depth check:
// even if schema validation were bypassed, an unknown next_action.type cannot
// ride a pass verdict into a stage advance.
func TestApplyOutcomeRejectsUnknownAction(t *testing.T) {
	root, item := initProject(t)
	run := &Run{RunID: "run_test", Skill: "ox-agent/specify", Stage: "specification"}
	out := validOutput("specify")
	out["next_action"] = map[string]any{"type": "bogus_action"}
	if err := applyOutcome(root, item, run, out); err == nil {
		t.Fatal("applyOutcome() accepted unknown next_action.type; want error")
	}
	if item.CurrentStage != "intake" {
		t.Fatalf("work item advanced on bogus action: stage = %s", item.CurrentStage)
	}
}

// TestCollectRejectsMissingActionType verifies next_action without a type
// fails schema validation.
func TestCollectRejectsMissingActionType(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["next_action"] = map[string]any{}
	writeOutput(t, root, run.RunID, out)
	if _, _, err := Collect(root, run.RunID); err == nil {
		t.Fatal("Collect() accepted next_action without type; want error")
	}
}

// TestCollectEscalateFilesInputRequest verifies an escalated run blocks the
// work item AND files a durable clarification request — the question
// survives the turn in async hosts.
func TestCollectEscalateFilesInputRequest(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["status"] = "blocked"
	out["verdict"] = "blocked"
	out["next_action"] = map[string]any{"type": "escalate", "reason": "need a product decision"}
	writeOutput(t, root, run.RunID, out)
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	reloaded, err := state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if reloaded.State != "blocked" {
		t.Fatalf("state = %s, want blocked", reloaded.State)
	}
	pending, issues := input.Pending(root)
	if len(issues) > 0 {
		t.Fatalf("Pending() issues = %v", issues)
	}
	if len(pending) != 1 || pending[0].Kind != input.KindClarification || pending[0].RunID != run.RunID {
		t.Fatalf("pending = %+v", pending)
	}
	// A second escalation with the same reason dedupes onto the same request.
	out["next_action"] = map[string]any{"type": "escalate", "reason": "need a product decision"}
	if err := fileEscalationRequest(root, reloaded, run, "need a product decision"); err != nil {
		t.Fatalf("fileEscalationRequest() error = %v", err)
	}
	pending, _ = input.Pending(root)
	if len(pending) != 1 {
		t.Fatalf("dedupe failed: pending = %d", len(pending))
	}
}

// TestCollectGateFilesApprovalRequest verifies a stage completion that lands
// on a human-gated lifecycle edge files an approval request and reports
// success — the collected outcome applied; only advancement waits.
func TestCollectGateFilesApprovalRequest(t *testing.T) {
	root, item := initProject(t)
	// Gate a stage-boundary edge in the project lifecycle — the embedded
	// lifecycle only gates shipping→shipped, which no stage maps to.
	lifePath := filepath.Join(root, ".ox-agent", "lifecycle", "workflow.yaml")
	life, err := os.ReadFile(lifePath)
	if err != nil {
		t.Fatalf("reading lifecycle: %v", err)
	}
	// The checkout may carry CRLF endings on Windows — normalize before
	// patching so the edge match does not silently no-op.
	normalized := strings.ReplaceAll(string(life), "\r\n", "\n")
	gated := strings.Replace(normalized,
		"  - from: discovering\n    to: brainstorming\n",
		"  - from: discovering\n    to: brainstorming\n    gate: human\n", 1)
	if gated == normalized {
		t.Fatal("lifecycle gate not applied — edge text did not match")
	}
	if err := os.WriteFile(lifePath, []byte(gated), 0o644); err != nil {
		t.Fatalf("writing lifecycle: %v", err)
	}
	item.State = "discovering"
	item.CurrentStage = "discovery"
	if err := item.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "discover-context", Stage: "discovery"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, run.RunID, validOutput("discover-context"))
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() must succeed when advancement parks at a gate: %v", err)
	}
	reloaded, err := state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if reloaded.State != "discovering" {
		t.Fatalf("state = %s, want discovering (awaiting approval)", reloaded.State)
	}
	req, err := input.FindPendingAction(root, item.ID, "transition", "brainstorming")
	if err != nil {
		t.Fatalf("FindPendingAction() error = %v", err)
	}
	if req == nil {
		t.Fatal("no approval request filed for the gated edge")
	}
	// Approving it drives the real gated path forward.
	if _, err := input.Approve(root, req.RequestID, "jesse"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	reloaded, _ = state.Resolve(root, item.ID)
	if reloaded.State != "brainstorming" {
		t.Fatalf("state = %s after approve, want brainstorming", reloaded.State)
	}
}

// TestCollectAwaitInputFilesRequest verifies next_action.type await_input
// files a durable request, keeps the run re-dispatchable (retry), records
// the request on the run, and leaves the work item unblocked — even when
// the skill pairs it with status "blocked" (blocked on input, not work).
func TestCollectAwaitInputFilesRequest(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["status"] = "blocked"
	out["verdict"] = "blocked"
	out["next_action"] = map[string]any{
		"type":    "await_input",
		"reason":  "which direction should the spec take?",
		"options": []any{"cli-first", "api-first", "manual"},
	}
	writeOutput(t, root, run.RunID, out)
	gotRun, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if gotRun.Status != "retry" {
		t.Fatalf("run status = %s, want retry (dispatchable)", gotRun.Status)
	}
	if gotRun.AwaitingInput == "" {
		t.Fatal("run.AwaitingInput empty — request ID not recorded")
	}
	reloaded, err := state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if reloaded.State == "blocked" {
		t.Fatal("await_input marked the item blocked — it must not")
	}
	pending, issues := input.Pending(root)
	if len(issues) > 0 {
		t.Fatalf("Pending() issues = %v", issues)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	req := pending[0]
	if req.RequestID != gotRun.AwaitingInput {
		t.Fatalf("request %s != run.AwaitingInput %s", req.RequestID, gotRun.AwaitingInput)
	}
	if req.Kind != input.KindDecision || len(req.Options) != 3 {
		t.Fatalf("request = kind %s options %v, want decision with 3 options", req.Kind, req.Options)
	}
	// The skill stays dispatchable — the human's answer unblocks a re-run.
	skill, err := NextSkill(root, reloaded, reloaded.CurrentStage, map[string][]string{reloaded.CurrentStage: {"specify"}})
	if err != nil || skill != "specify" {
		t.Fatalf("NextSkill = %q, %v — await_input skill must stay dispatchable", skill, err)
	}
}

// TestCollectAwaitInputDedupes verifies a repeated await_input with the
// same question reuses the pending request, and that an answered request
// lets the next collect proceed.
func TestCollectAwaitInputDedupes(t *testing.T) {
	root, item := initProject(t)
	ask := func() *Run {
		run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		out := validOutput("specify")
		out["next_action"] = map[string]any{"type": "await_input", "reason": "confirm the scope?"}
		writeOutput(t, root, run.RunID, out)
		got, _, err := Collect(root, run.RunID)
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		return got
	}
	first := ask()
	second := ask()
	pending, _ := input.Pending(root)
	if len(pending) != 1 {
		t.Fatalf("dedupe failed: pending = %d", len(pending))
	}
	if first.AwaitingInput != second.AwaitingInput {
		t.Fatalf("dedupe ids differ: %s vs %s", first.AwaitingInput, second.AwaitingInput)
	}
}

// TestCollectRetryAfterOutcomeFailure verifies a failed outcome filing
// restores the run to pending so collection retries instead of stranding
// the run non-collectable.
func TestCollectRetryAfterOutcomeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based failure injection is unreliable on Windows")
	}
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["next_action"] = map[string]any{"type": "await_input", "reason": "which way?"}
	writeOutput(t, root, run.RunID, out)
	// Make request filing fail: read-only input dir.
	inputDir := filepath.Join(root, ".ox-agent", "state", "input")
	if err := os.MkdirAll(inputDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(inputDir, 0o555); err != nil {
		t.Fatalf("chmod ro: %v", err)
	}
	// Root with CAP_DAC_OVERRIDE ignores mode bits — probe the actual
	// write instead of assuming chmod took effect.
	probe := filepath.Join(inputDir, ".probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err == nil {
		os.Remove(probe)
		t.Skip("directory remains writable after chmod — running with elevated privileges")
	}
	if _, _, err := Collect(root, run.RunID); err == nil {
		t.Fatal("Collect() succeeded despite filing failure")
	}
	reloaded, err := LoadRun(root, run.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if reloaded.Status != "pending" {
		t.Fatalf("run status = %s, want pending (retryable)", reloaded.Status)
	}
	if err := os.Chmod(inputDir, 0o755); err != nil {
		t.Fatalf("chmod rw: %v", err)
	}
	got, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("retry Collect() error = %v", err)
	}
	if got.AwaitingInput == "" {
		t.Fatal("retried collect did not file the request")
	}
}

// TestDispatchEmitsResolvedWorkItemPaths verifies the packet carries the
// resolved work-item and artifact directories so skills never derive them
// (#52) — and that dispatch creates the artifacts directory.
func TestDispatchEmitsResolvedWorkItemPaths(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(run.Dir, "packet.json"))
	if err != nil {
		t.Fatalf("reading packet: %v", err)
	}
	var packet struct {
		WorkItemDir  string `json:"work_item_dir"`
		ArtifactsDir string `json:"artifacts_dir"`
	}
	if err := json.Unmarshal(data, &packet); err != nil {
		t.Fatalf("parsing packet: %v", err)
	}
	wantDir := "work/active/" + filepath.Base(item.Dir)
	if packet.WorkItemDir != wantDir {
		t.Fatalf("work_item_dir = %q, want %q", packet.WorkItemDir, wantDir)
	}
	if packet.ArtifactsDir != wantDir+"/artifacts" {
		t.Fatalf("artifacts_dir = %q, want %q", packet.ArtifactsDir, wantDir+"/artifacts")
	}
	if _, err := os.Stat(filepath.Join(root, ".ox-agent", filepath.FromSlash(packet.ArtifactsDir))); err != nil {
		t.Fatalf("artifacts dir not created at dispatch: %v", err)
	}
}

// TestCollectRejectsUnsafeArtifactPaths verifies declared artifact paths
// that would corrupt committed trees fail the run like a schema error —
// re-dispatchable (#52).
func TestCollectRejectsUnsafeArtifactPaths(t *testing.T) {
	cases := map[string]any{
		"glob star":      "WI-0005*/plan.md",
		"glob object":    map[string]any{"path": "WI-000?/spec.md"},
		"windows colon":  "work/active/x/plan:1.md",
		"traversal":      "../escape.md",
		"backslash path": `work\..\..\escape.md`,
		"backslash sep":  `work\active\x\plan.md`,
		"trailing dot":   "work/active/x/plan./spec.md",
		"trailing space": "work/active/x/plan /spec.md",
		"control char":   "work/active/x/plan\x01.md",
		"windows pipe":   "work/active/x/a|b.md",
		"windows lt/gt":  "work/active/x/a<b>.md",
		"windows quote":  "work/active/x/a\"b.md",
		"absolute":       "/tmp/plan.md",
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			root, item := initProject(t)
			run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
			if err != nil {
				t.Fatalf("Dispatch() error = %v", err)
			}
			out := validOutput("specify")
			out["artifacts"] = []any{bad}
			writeOutput(t, root, run.RunID, out)
			run, _, err = Collect(root, run.RunID)
			if err == nil {
				t.Fatalf("Collect() accepted artifact %v; want error", bad)
			}
			if run.Status != "failed" {
				t.Fatalf("run status = %s, want failed", run.Status)
			}
		})
	}
}

// TestCollectAcceptsValidArtifactPaths verifies legitimate artifact paths
// still collect — the guard targets unsafe paths only.
func TestCollectAcceptsValidArtifactPaths(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["artifacts"] = []any{
		"work/active/WI-0001-x/artifacts/spec.md",
		map[string]any{"path": "work/active/WI-0001-x/artifacts/plan.md"},
		// "."-segments are clean no-ops the Windows guard must not reject
		// (#53 review finding on validateArtifactPaths).
		"work/active/./artifacts/review.md",
	}
	writeOutput(t, root, run.RunID, out)
	run, _, err = Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "collected" {
		t.Fatalf("run status = %s, want collected", run.Status)
	}
}

// TestCollectReplaysPendingEventAfterJournalFailure covers the failure
// where the run record persisted but the skill.failed append failed: a
// failed run is never collected again, so the event rides on the record
// and the next collect for the same work item replays it (#53).
func TestCollectReplaysPendingEventAfterJournalFailure(t *testing.T) {
	root, item := initProject(t)
	runA, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	// Block journal appends: a directory where events.ndjson belongs
	// makes every open fail on all platforms.
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	if err := os.Remove(journal); err != nil {
		t.Fatalf("removing journal: %v", err)
	}
	if err := os.Mkdir(journal, 0o755); err != nil {
		t.Fatalf("blocking journal path: %v", err)
	}
	out := validOutput("specify")
	out["artifacts"] = []any{"WI-0005*/plan.md"}
	writeOutput(t, root, runA.RunID, out)
	if _, _, err := Collect(root, runA.RunID); err == nil {
		t.Fatal("Collect() succeeded despite blocked journal; want error")
	}
	reloaded, err := LoadRun(root, runA.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if reloaded.Status != "failed" {
		t.Fatalf("run status = %s, want failed", reloaded.Status)
	}
	if reloaded.PendingEvent != "skill.failed" {
		t.Fatalf("pending_event = %q, want skill.failed", reloaded.PendingEvent)
	}
	// Restore the journal, then collect another run for the same item —
	// the reconcile step must replay run A's dropped event.
	if err := os.Remove(journal); err != nil {
		t.Fatalf("restoring journal path: %v", err)
	}
	runB, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "verify", Stage: "verification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, runB.RunID, validOutput("verify"))
	if _, _, err := Collect(root, runB.RunID); err != nil {
		t.Fatalf("Collect() after journal restore error = %v", err)
	}
	reloaded, err = LoadRun(root, runA.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if reloaded.PendingEvent != "" {
		t.Fatalf("pending_event = %q after replay, want cleared", reloaded.PendingEvent)
	}
	events, err := state.EventsFor(root, item.ID, 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	var replayed bool
	for _, ev := range events {
		if ev.Type == "skill.failed" && ev.Data["run_id"] == runA.RunID {
			replayed = true
		}
	}
	if !replayed {
		t.Fatal("journal lacks skill.failed event for run A after replay")
	}
}

// TestReconcileDedupesAlreadyJournaledEvent verifies a pending event whose
// append succeeded but whose marker-clear save failed is not journaled a
// second time by the next collect (#53).
func TestReconcileDedupesAlreadyJournaledEvent(t *testing.T) {
	root, item := initProject(t)
	runA, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	// Simulate the append-succeeded/save-failed window: the marker is set
	// AND the event is already in the journal.
	runA.PendingEvent = "skill.failed"
	runA.PendingEventData = map[string]any{"run_id": runA.RunID, "skill": runA.Skill, "stage": runA.Stage, "error": "dropped once"}
	if err := runA.save(root); err != nil {
		t.Fatalf("seeding pending event: %v", err)
	}
	if err := state.Append(root, state.Event{
		Type:       "skill.failed",
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data:       runA.PendingEventData,
	}); err != nil {
		t.Fatalf("seeding journal: %v", err)
	}
	runB, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "verify", Stage: "verification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, runB.RunID, validOutput("verify"))
	if _, _, err := Collect(root, runB.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	reloaded, err := LoadRun(root, runA.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if reloaded.PendingEvent != "" {
		t.Fatalf("pending_event = %q, want cleared without re-append", reloaded.PendingEvent)
	}
	events, err := state.EventsFor(root, item.ID, 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	count := 0
	for _, ev := range events {
		if ev.Type == "skill.failed" && ev.Data["run_id"] == runA.RunID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("journal has %d skill.failed events for run A, want 1", count)
	}
}

// TestReconcileWarningsDoNotBlockCollect verifies that a pending-event
// replay that cannot clear its marker — the other run's record is
// unwritable — surfaces as a warning without aborting this collect (#53).
func TestReconcileWarningsDoNotBlockCollect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based failure injection is unreliable on Windows")
	}
	root, item := initProject(t)
	runA, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	// Give run A a durable pending event: fail its collect with a blocked
	// journal (a directory where events.ndjson belongs).
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	if err := os.Remove(journal); err != nil {
		t.Fatalf("removing journal: %v", err)
	}
	if err := os.Mkdir(journal, 0o755); err != nil {
		t.Fatalf("blocking journal path: %v", err)
	}
	out := validOutput("specify")
	out["artifacts"] = []any{"WI-0005*/plan.md"}
	writeOutput(t, root, runA.RunID, out)
	if _, _, err := Collect(root, runA.RunID); err == nil {
		t.Fatal("Collect() succeeded despite blocked journal; want error")
	}
	reloaded, err := LoadRun(root, runA.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if reloaded.PendingEvent != "skill.failed" {
		t.Fatalf("pending_event = %q, want skill.failed", reloaded.PendingEvent)
	}
	// Restore the journal, then make A's run record unwritable: the
	// replay append succeeds but the marker-clear save fails — a
	// warning, not a blocked collect. (Chmod the file, not the dir —
	// writes to an existing file ignore directory mode.)
	if err := os.Remove(journal); err != nil {
		t.Fatalf("restoring journal path: %v", err)
	}
	runJSON := filepath.Join(runA.Dir, "run.json")
	if err := os.Chmod(runJSON, 0o444); err != nil {
		t.Fatalf("chmod ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(runJSON, 0o644) })
	if f, err := os.OpenFile(runJSON, os.O_WRONLY|os.O_TRUNC, 0); err == nil {
		f.Close()
		t.Skip("file remains writable after chmod — running with elevated privileges")
	}
	runB, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "verify", Stage: "verification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, runB.RunID, validOutput("verify"))
	got, _, err := Collect(root, runB.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v — run A's bookkeeping must not block run B", err)
	}
	if got.Status != "collected" {
		t.Fatalf("run B status = %s, want collected", got.Status)
	}
	if len(got.ReconcileWarnings) == 0 {
		t.Fatal("expected reconcile warnings for run A's unwritable marker")
	}
	// A's event must still have been journaled exactly once.
	events, err := state.EventsFor(root, item.ID, 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	count := 0
	for _, ev := range events {
		if ev.Type == "skill.failed" && ev.Data["run_id"] == runA.RunID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("journal has %d skill.failed events for run A, want 1", count)
	}
}

// TestCollectRestoresPendingWhenOutcomeEventFails verifies a failed
// outcome-event append leaves an outcome-bearing run pending — the next
// collect re-applies the outcome rather than only replaying the journal
// event (#53).
func TestCollectRestoresPendingWhenOutcomeEventFails(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, run.RunID, validOutput("specify"))
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	if err := os.Remove(journal); err != nil {
		t.Fatalf("removing journal: %v", err)
	}
	if err := os.Mkdir(journal, 0o755); err != nil {
		t.Fatalf("blocking journal path: %v", err)
	}
	if _, _, err := Collect(root, run.RunID); err == nil {
		t.Fatal("Collect() succeeded despite blocked journal; want error")
	}
	reloaded, err := LoadRun(root, run.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if reloaded.Status != "pending" {
		t.Fatalf("run status = %s, want pending (outcome retryable)", reloaded.Status)
	}
	if reloaded.PendingEvent != "" {
		t.Fatalf("pending_event = %q, want empty — the pending status is the retry vehicle", reloaded.PendingEvent)
	}
	// Journal restored: the retry must append the event once and apply
	// the outcome.
	if err := os.Remove(journal); err != nil {
		t.Fatalf("restoring journal path: %v", err)
	}
	got, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("retry Collect() error = %v", err)
	}
	if got.Status != "collected" {
		t.Fatalf("run status = %s after retry, want collected", got.Status)
	}
	events, err := state.EventsFor(root, item.ID, 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	count := 0
	for _, ev := range events {
		if ev.Type == "skill.completed" && ev.Data["run_id"] == run.RunID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("journal has %d skill.completed events for the run, want 1", count)
	}
}

// TestRetriedCollectDoesNotDoubleJournal verifies the append-succeeded /
// outcome-failed window: the run is restored to pending, and the retry
// skips the already-journaled event instead of appending a duplicate
// while re-applying the outcome (#53).
func TestRetriedCollectDoesNotDoubleJournal(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["status"] = "blocked"
	out["verdict"] = "blocked"
	out["next_action"] = map[string]any{"type": "await_input", "reason": "which option?"}
	writeOutput(t, root, run.RunID, out)

	// Journal appends work, but the input-request filing fails: the state
	// dir holds a regular file where the input directory belongs.
	inputDir := filepath.Join(root, ".ox-agent", "state", "input")
	if err := os.RemoveAll(inputDir); err != nil {
		t.Fatalf("removing input dir: %v", err)
	}
	if err := os.WriteFile(inputDir, []byte("blocked"), 0o644); err != nil {
		t.Fatalf("blocking input dir: %v", err)
	}
	if _, _, err := Collect(root, run.RunID); err == nil {
		t.Fatal("Collect() succeeded despite blocked input filing; want error")
	}
	reloaded, err := LoadRun(root, run.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if reloaded.Status != "pending" {
		t.Fatalf("run status = %s, want pending (outcome retryable)", reloaded.Status)
	}
	countEvents := func() int {
		events, err := state.EventsFor(root, item.ID, 0)
		if err != nil {
			t.Fatalf("EventsFor() error = %v", err)
		}
		count := 0
		for _, ev := range events {
			if ev.Type == "skill.completed" && ev.Data["run_id"] == run.RunID {
				count++
			}
		}
		return count
	}
	if got := countEvents(); got != 1 {
		t.Fatalf("journal has %d skill.completed events, want 1 from the first attempt", got)
	}

	// Input dir restored: the retry must NOT re-journal the event but
	// MUST apply the outcome (file the durable request).
	if err := os.Remove(inputDir); err != nil {
		t.Fatalf("restoring input dir: %v", err)
	}
	got, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("retry Collect() error = %v", err)
	}
	if got.Status != "retry" {
		t.Fatalf("run status = %s after retry, want retry (await_input parks the run)", got.Status)
	}
	if got.AwaitingInput == "" {
		t.Fatal("AwaitingInput empty — the durable request was not filed on retry")
	}
	if got := countEvents(); got != 1 {
		t.Fatalf("journal has %d skill.completed events after retry, want 1 (no duplicate)", got)
	}
}

// TestRetriedCollectRejectsOutcomeTypeChange covers the revised-output
// replay: a run journaled skill.completed whose output.json is rewritten
// to failed must refuse rather than journal skill.failed alongside it —
// the outcome lookup checks BOTH event types (#53).
func TestRetriedCollectRejectsOutcomeTypeChange(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["status"] = "blocked"
	out["verdict"] = "blocked"
	out["next_action"] = map[string]any{"type": "await_input", "reason": "which option?"}
	writeOutput(t, root, run.RunID, out)
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	// Simulate the pending-restore state: outcome journaled, run pending.
	reloaded, err := LoadRun(root, run.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	reloaded.Status = "pending"
	if err := reloaded.save(root); err != nil {
		t.Fatalf("restoring pending: %v", err)
	}

	// Revise the outcome: blocked+await_input → failed+needs_rework. The
	// schema-valid pairing still yields the skill.failed event type.
	out["status"] = "failed"
	out["verdict"] = "needs_rework"
	out["next_action"] = map[string]any{"type": "retry", "reason": "gave up"}
	writeOutput(t, root, run.RunID, out)

	if _, _, err := Collect(root, run.RunID); err == nil {
		t.Fatal("Collect() accepted a revised outcome over the journaled skill.completed")
	} else if !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("Collect() error = %v, want drift refusal", err)
	}

	events, err := state.EventsFor(root, item.ID, 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	completed, failed := 0, 0
	for _, ev := range events {
		if ev.Data["run_id"] != run.RunID {
			continue
		}
		switch ev.Type {
		case "skill.completed":
			completed++
		case "skill.failed":
			failed++
		}
	}
	if completed != 1 || failed != 0 {
		t.Fatalf("journal has %d completed + %d failed for the run, want 1 + 0 (revised outcome refused)", completed, failed)
	}
	if reloaded2, _ := LoadRun(root, run.RunID); reloaded2.Status != "pending" {
		t.Fatalf("run status = %s after drift refusal, want pending (re-dispatchable)", reloaded2.Status)
	}
}

// TestRetriedCollectRejectsChangedInputRequest covers request-field drift:
// a retried collect whose output now asks a different question must not
// stack a second pending request for the same run (#53).
func TestRetriedCollectRejectsChangedInputRequest(t *testing.T) {
	root, item := initProject(t)
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	out := validOutput("specify")
	out["status"] = "blocked"
	out["verdict"] = "blocked"
	out["next_action"] = map[string]any{"type": "await_input", "reason": "which option?"}
	writeOutput(t, root, run.RunID, out)
	got, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got.AwaitingInput == "" {
		t.Fatal("AwaitingInput empty — the durable request was not filed")
	}

	// Pending-restore: journaled outcome + filed request, run pending.
	reloaded, err := LoadRun(root, run.RunID)
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	reloaded.Status = "pending"
	if err := reloaded.save(root); err != nil {
		t.Fatalf("restoring pending: %v", err)
	}

	// Same verdict/action/status — only the question changed.
	out["next_action"] = map[string]any{"type": "await_input", "reason": "a different question entirely"}
	writeOutput(t, root, run.RunID, out)

	if _, _, err := Collect(root, run.RunID); err == nil {
		t.Fatal("Collect() filed a different question over the run's pending request")
	} else if !strings.Contains(err.Error(), "refusing to file") {
		t.Fatalf("Collect() error = %v, want request drift refusal", err)
	}
	pending, perrs := input.Pending(root)
	if len(perrs) > 0 {
		t.Fatalf("Pending() errors = %v", perrs)
	}
	if len(pending) != 1 {
		t.Fatalf("pending requests = %d, want 1 (no stacked request)", len(pending))
	}
}

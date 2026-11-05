package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxidean/Ox-Agent/internal/appctx"
	"github.com/oxidean/Ox-Agent/internal/contract"
	"github.com/oxidean/Ox-Agent/internal/pipeline"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
)

// Run is one dispatched skill invocation, mirroring run.schema.json.
type Run struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	WorkItemID    string `json:"work_item_id"`
	Skill         string `json:"skill"`
	Stage         string `json:"stage"`
	Adapter       string `json:"adapter"`
	Status        string `json:"status"`
	PacketPath    string `json:"packet_path"`
	Pipeline      string `json:"pipeline,omitempty"`
	StepID        string `json:"step_id,omitempty"`
	CreatedAt     string `json:"created_at"`
	CollectedAt   string `json:"collected_at,omitempty"`
	Error         string `json:"error,omitempty"`
	Dir           string `json:"-"`
}

// DispatchRequest describes one skill invocation to dispatch.
type DispatchRequest struct {
	Skill       string
	Stage       string
	Integration string
	Pipeline    string
	StepID      string
	Budget      int
}

// runsDir returns the runtime/runs directory under root.
func runsDir(root string) string {
	return filepath.Join(root, ".ox-agent", "runtime", "runs")
}

// save persists run.json into the run directory.
func (r *Run) save(root string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runsDir(root), r.RunID, "run.json"), append(data, '\n'), 0o644)
}

// LoadRun reads a run record by id.
func LoadRun(root, runID string) (*Run, error) {
	data, err := os.ReadFile(filepath.Join(runsDir(root), runID, "run.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no run %q", runID)
		}
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("parsing run %s: %w", runID, err)
	}
	run.Dir = filepath.Join(runsDir(root), runID)
	return &run, nil
}

// PendingRuns lists run ids awaiting collection.
func PendingRuns(root string) ([]string, error) {
	entries, err := os.ReadDir(runsDir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run, err := LoadRun(root, entry.Name())
		if err == nil && run.Status == "pending" {
			out = append(out, run.RunID)
		}
	}
	return out, nil
}

// NextSkill picks the first stage_skills entry for the stage that has no
// collected run on this work item yet.
func NextSkill(root string, item *state.WorkItem, stage string, skills map[string][]string) (string, error) {
	candidates := skills[stage]
	if len(candidates) == 0 {
		return "", fmt.Errorf("no skills mapped to stage %q", stage)
	}
	collected := collectedSkills(root, item.ID, stage)
	for _, skill := range candidates {
		if !collected["ox-agent/"+skill] && !collected[skill] {
			return skill, nil
		}
	}
	return "", fmt.Errorf("all %d skill(s) for stage %q already collected", len(candidates), stage)
}

// Dispatch compiles the packet and creates a pending run. For manual
// integrations the operator completes the run offline and collects it later.
func Dispatch(ctx context.Context, root string, item *state.WorkItem, req DispatchRequest) (*Run, error) {
	integration, err := Resolve(root, req.Integration)
	if err != nil {
		return nil, err
	}
	switch integration.Adapter {
	case "manual", "spawn-cli":
	default:
		return nil, fmt.Errorf("integration %q has unknown adapter %q (supported adapters: manual, spawn-cli)", integration.Name, integration.Adapter)
	}
	runID := fmt.Sprintf("run_%d_%06x", time.Now().UnixMilli(), rand.Uint64N(1<<24))
	rel := "runtime/runs/" + runID
	packet, err := appctx.Compile(root, item, req.Skill, req.Stage, req.Budget, rel+"/output.json")
	if err != nil {
		return nil, err
	}
	runDir := filepath.Join(root, ".ox-agent", filepath.FromSlash(rel))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, err
	}
	data, err := packet.Bytes()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(runDir, "packet.json"), append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	run := &Run{
		SchemaVersion: 1,
		RunID:         runID,
		WorkItemID:    item.ID,
		Skill:         packet.Skill,
		Stage:         req.Stage,
		Adapter:       integration.Adapter,
		Status:        "pending",
		PacketPath:    rel + "/packet.json",
		Pipeline:      req.Pipeline,
		StepID:        req.StepID,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Dir:           runDir,
	}
	if err := run.save(root); err != nil {
		return nil, err
	}
	if err := state.Append(root, state.Event{
		Type:       "skill.invoked",
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data:       map[string]any{"run_id": runID, "skill": packet.Skill, "stage": req.Stage, "adapter": integration.Adapter, "pipeline": req.Pipeline, "step_id": req.StepID},
	}); err != nil {
		return nil, err
	}
	if integration.Adapter == "spawn-cli" {
		if err := run.spawn(ctx, root, integration); err != nil {
			return nil, err
		}
	}
	return run, nil
}

// Collect validates a pending run's output.json against the skill's output
// schema, journals the outcome, and applies the declared next_action.
func Collect(root, runID string) (*Run, map[string]any, error) {
	run, err := LoadRun(root, runID)
	if err != nil {
		return nil, nil, err
	}
	if run.Status != "pending" {
		return nil, nil, fmt.Errorf("run %s is %s, not pending", runID, run.Status)
	}
	item, err := state.Resolve(root, run.WorkItemID)
	if err != nil {
		return nil, nil, err
	}
	outputPath := filepath.Join(run.Dir, "output.json")
	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, nil, fmt.Errorf("run %s has no output.json yet: %w", runID, err)
	}
	skill := strings.TrimPrefix(run.Skill, "ox-agent/")
	schemaPath := filepath.Join(root, ".ox-agent", "skills", skill, "schemas", "output.schema.json")
	if err := contract.ValidateFile(schemaPath, outputData); err != nil {
		run.Status = "failed"
		run.Error = "output failed schema validation: " + err.Error()
		_ = run.save(root)
		_ = state.Append(root, state.Event{
			Type:       "skill.failed",
			WorkItemID: item.ID,
			Actor:      "ox-agent",
			Data:       map[string]any{"run_id": runID, "skill": run.Skill, "stage": run.Stage, "error": run.Error},
		})
		return run, nil, fmt.Errorf("output for %s failed validation: %w", runID, err)
	}
	var output map[string]any
	if err := json.Unmarshal(outputData, &output); err != nil {
		return nil, nil, fmt.Errorf("parsing output.json: %w", err)
	}
	run.CollectedAt = time.Now().UTC().Format(time.RFC3339)
	status, _ := output["status"].(string)
	verdict, _ := output["verdict"].(string)
	if status == "failed" {
		run.Status = "failed"
		run.Error, _ = output["summary"].(string)
	} else {
		run.Status = "collected"
	}
	if err := run.save(root); err != nil {
		return nil, nil, err
	}
	eventType := "skill.completed"
	if run.Status == "failed" {
		eventType = "skill.failed"
	}
	if err := state.Append(root, state.Event{
		Type:       eventType,
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data:       map[string]any{"run_id": runID, "skill": run.Skill, "stage": run.Stage, "status": status, "verdict": verdict},
	}); err != nil {
		return run, output, err
	}
	if run.Status == "collected" {
		if err := applyOutcome(root, item, run, output); err != nil {
			return run, output, err
		}
	}
	return run, output, nil
}

// collectedSkills returns the skills with a collected run for the work item
// at the given stage.
func collectedSkills(root, itemID, stage string) map[string]bool {
	collected := map[string]bool{}
	entries, err := os.ReadDir(runsDir(root))
	if err != nil {
		return collected
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run, err := LoadRun(root, entry.Name())
		if err == nil && run.WorkItemID == itemID && run.Stage == stage && run.Status == "collected" {
			collected[run.Skill] = true
		}
	}
	return collected
}

// stageComplete reports whether every skill mapped to the work item's current
// stage has a collected run. Stages with no mapped skills complete on any
// collected run at that stage.
func stageComplete(root string, item *state.WorkItem) (bool, error) {
	settings, err := project.LoadSettings(root)
	if err != nil {
		return false, err
	}
	collected := collectedSkills(root, item.ID, item.CurrentStage)
	for _, skill := range settings.Skills.StageSkills[item.CurrentStage] {
		if !collected["ox-agent/"+skill] && !collected[skill] {
			return false, nil
		}
	}
	return true, nil
}

// pipelineComplete reports whether every step of the named pipeline has a
// collected run for the work item.
func pipelineComplete(root, name string, item *state.WorkItem) (bool, error) {
	pipe, err := pipeline.Load(root, name)
	if err != nil {
		return false, err
	}
	progress, err := pipeline.Progress(root, pipe, item)
	if err != nil {
		return false, err
	}
	if len(progress) == 0 {
		return false, nil
	}
	for _, step := range progress {
		if step.Status != "done" {
			return false, nil
		}
	}
	return true, nil
}

// applyOutcome applies the skill's verdict and next_action to the work item:
// blocked verdicts block the item, rework stays, and pass verdicts advance
// the stage once the stage's unit of work is done — every mapped skill for a
// single-skill run, or the whole pipeline for a pipeline step. A pass on an
// incomplete unit leaves the stage so the remaining work stays selectable.
func applyOutcome(root string, item *state.WorkItem, run *Run, output map[string]any) error {
	status, _ := output["status"].(string)
	verdict, _ := output["verdict"].(string)
	next, _ := output["next_action"].(map[string]any)
	action, _ := next["type"].(string)
	reason, _ := next["reason"].(string)
	summary, _ := output["summary"].(string)
	if reason == "" {
		reason = summary
	}
	// Unknown action types fail loudly even when the verdict would
	// otherwise drive a transition — a typo'd action must not no-op.
	switch action {
	case "", "advance_stage", "complete", "retry", "escalate":
	default:
		return fmt.Errorf("unknown next_action.type %q", action)
	}
	switch {
	case status == "blocked" || verdict == "blocked" || action == "escalate":
		if item.State != "blocked" {
			return item.Transition(root, "blocked", "ox-agent", "", reason)
		}
		return nil
	case verdict == "needs_rework" || action == "retry":
		return nil
	case action == "advance_stage" || action == "complete" || action == "" || verdict == "pass" || verdict == "pass_with_notes":
		if run.Pipeline != "" {
			complete, err := pipelineComplete(root, run.Pipeline, item)
			if err != nil {
				return err
			}
			if !complete {
				return nil
			}
			_, err = item.Advance(root, "ox-agent")
			return err
		}
		if run.Stage != item.CurrentStage {
			return nil
		}
		complete, err := stageComplete(root, item)
		if err != nil {
			return err
		}
		if !complete {
			return nil
		}
		_, err = item.Advance(root, "ox-agent")
		return err
	}
	return nil
}

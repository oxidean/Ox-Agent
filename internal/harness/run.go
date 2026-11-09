package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/oxidean/Ox-Agent/internal/appctx"
	"github.com/oxidean/Ox-Agent/internal/contract"
	"github.com/oxidean/Ox-Agent/internal/input"
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
	AwaitingInput string `json:"awaiting_input,omitempty"`
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
	case "manual", "spawn-cli", "webhook":
	default:
		return nil, fmt.Errorf("integration %q has unknown adapter %q (supported adapters: manual, spawn-cli, webhook)", integration.Name, integration.Adapter)
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
	if packet.ArtifactsDir != "" {
		if err := os.MkdirAll(filepath.Join(root, ".ox-agent", filepath.FromSlash(packet.ArtifactsDir)), 0o755); err != nil {
			return nil, err
		}
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
	switch integration.Adapter {
	case "spawn-cli":
		if err := run.spawn(ctx, root, integration); err != nil {
			return nil, err
		}
	case "webhook":
		if err := run.webhook(ctx, root, integration); err != nil {
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
	if err := validateArtifactPaths(output["artifacts"]); err != nil {
		// Declared artifact paths land in committed work-item directories:
		// a path like "WI-0005*/plan.md" creates a literal-star directory
		// that fails Windows checkout outright (#52). Fail like a schema
		// error so the run is re-dispatchable after the skill corrects it.
		run.Status = "failed"
		run.Error = "invalid artifact path: " + err.Error()
		_ = run.save(root)
		_ = state.Append(root, state.Event{
			Type:       "skill.failed",
			WorkItemID: item.ID,
			Actor:      "ox-agent",
			Data:       map[string]any{"run_id": runID, "skill": run.Skill, "stage": run.Stage, "error": run.Error},
		})
		return run, output, fmt.Errorf("invalid artifact path for %s: %w", runID, err)
	}
	run.CollectedAt = time.Now().UTC().Format(time.RFC3339)
	status, _ := output["status"].(string)
	verdict, _ := output["verdict"].(string)
	next, _ := output["next_action"].(map[string]any)
	action, _ := next["type"].(string)
	// Blocking outcomes must reach applyOutcome so the work item
	// transitions to blocked, even when the run itself must not count
	// as collected.
	blocking := status == "blocked" || verdict == "blocked" || action == "escalate"
	needsInput := action == "await_input"
	switch {
	case status == "failed":
		run.Status = "failed"
		run.Error, _ = output["summary"].(string)
	case needsInput:
		// Awaiting a human answer: the run is finished for this turn but
		// unresolved, so the skill stays dispatchable once the durable
		// request is answered — the item is NOT blocked.
		run.Status = "retry"
	case blocking && (verdict == "needs_rework" || action == "retry"):
		// Blocking rework or retry stays unresolved: retry keeps the skill
		// dispatchable (NextSkill, stageComplete, and pipeline progress
		// all exclude it) after the item is unblocked.
		run.Status = "retry"
	case blocking:
		run.Status = "collected"
	case verdict == "needs_rework" || action == "retry":
		// A rework/retry outcome is finished but not done: the run must not
		// count for skill selection or pipeline progress so another pass
		// stays dispatchable.
		run.Status = "retry"
	default:
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
		Data:       map[string]any{"run_id": runID, "skill": run.Skill, "stage": run.Stage, "status": status, "verdict": verdict, "next_action": action},
	}); err != nil {
		return run, output, err
	}
	if run.Status == "collected" || blocking || needsInput {
		if err := applyOutcome(root, item, run, output); err != nil {
			// Restore the run to pending so collect stays retryable: a
			// failed filing (input request, journal) must not strand the
			// run — filings dedupe, so the retry reuses them safely.
			run.Status = "pending"
			if saveErr := run.save(root); saveErr != nil {
				return run, output, fmt.Errorf("%w (also failed to restore pending status: %v)", err, saveErr)
			}
			return run, output, err
		}
	}
	return run, output, nil
}

// unsafeArtifactChars matches characters that are legal on POSIX filesystems
// but invalid in committed paths checked out on Windows — plus ASCII control
// characters, which are invalid everywhere.
var unsafeArtifactChars = regexp.MustCompile(`[*?"<>|:` + "\x00-\x1f" + `]`)

// validateArtifactPaths checks declared artifact paths from a collected
// output: each must be a clean relative path (filepath.IsLocal), free of
// characters Windows checkouts reject, and free of trailing-dot or
// trailing-space segments (also invalid on Windows). Entries shaped like
// {path: ...} validate their path field; other shapes are the schema's job.
func validateArtifactPaths(raw any) error {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	for i, entry := range list {
		var p string
		switch v := entry.(type) {
		case string:
			p = v
		case map[string]any:
			p, _ = v["path"].(string)
		default:
			continue
		}
		if p == "" {
			continue
		}
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return fmt.Errorf("artifacts[%d] %q is not a clean relative path", i, p)
		}
		if unsafeArtifactChars.MatchString(p) {
			return fmt.Errorf("artifacts[%d] %q contains characters invalid on Windows checkouts", i, p)
		}
		for _, seg := range strings.Split(p, "/") {
			if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
				return fmt.Errorf("artifacts[%d] %q has a segment ending in a dot or space, invalid on Windows", i, p)
			}
		}
	}
	return nil
}

// collectedSkills returns the skills with a collected run for the work item
// at the given stage. Only the latest run per skill counts: a skill that
// collected once but was later retried is unresolved until its newest run
// collects.
func collectedSkills(root, itemID, stage string) map[string]bool {
	collected := map[string]bool{}
	entries, err := os.ReadDir(runsDir(root))
	if err != nil {
		return collected
	}
	latest := map[string]*Run{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run, err := LoadRun(root, entry.Name())
		if err == nil && run.WorkItemID == itemID && run.Stage == stage && laterRun(run, latest[run.Skill]) {
			latest[run.Skill] = run
		}
	}
	for skill, run := range latest {
		if run.Status == "collected" {
			collected[skill] = true
		}
	}
	return collected
}

// laterRun reports whether run is more recent than current, ordering by
// creation time with the run ID as the same-second tie-break.
func laterRun(run, current *Run) bool {
	if current == nil {
		return true
	}
	if run.CreatedAt != current.CreatedAt {
		return run.CreatedAt > current.CreatedAt
	}
	return run.RunID > current.RunID
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
	// The output schema constrains next_action.type; reject unknown values
	// here too so an out-of-contract action can never ride a pass verdict.
	switch action {
	case "", "advance_stage", "retry", "escalate", "await_input", "complete":
	default:
		return fmt.Errorf("unknown next_action.type %q", action)
	}
	switch {
	case action == "await_input":
		// Checked before the blocked branch: a skill may pair await_input
		// with status "blocked" (blocked on input) — that is a question,
		// not a work blockage, so the item stays unblocked.
		return fileAwaitInputRequest(root, item, run, next, reason)
	case status == "blocked" || verdict == "blocked" || action == "escalate":
		// File the durable request FIRST: the question must outlive the
		// turn even when the item can no longer be marked blocked (e.g.
		// already shipped) — otherwise the escalation evaporates.
		if err := fileEscalationRequest(root, item, run, reason); err != nil {
			return err
		}
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
			return fileGateOrErr(root, err)
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
		return fileGateOrErr(root, err)
	default:
		return fmt.Errorf("unknown next_action.type %q", action)
	}
}

// fileEscalationRequest files a durable clarification request for a blocked
// or escalated run, deduplicating on work-item + question so repeated
// escalations reuse one pending request instead of stacking.
func fileEscalationRequest(root string, item *state.WorkItem, run *Run, reason string) error {
	question := reason
	if question == "" {
		question = fmt.Sprintf("%s run %s escalated; how should %s proceed?", run.Skill, run.RunID, item.ID)
	}
	_, err := fileInputRequest(root, item.ID, run.RunID, question, nil)
	return err
}

// fileAwaitInputRequest files the durable request for a skill's
// next_action.type await_input — a clarification, or a decision when the
// output declares answer options. The request ID is persisted on the run
// so collect, status, and re-dispatch can all surface it.
func fileAwaitInputRequest(root string, item *state.WorkItem, run *Run, next map[string]any, reason string) error {
	question := reason
	if question == "" {
		question = fmt.Sprintf("%s run %s needs input for %s", run.Skill, run.RunID, item.ID)
	}
	var options []string
	if raw, ok := next["options"].([]any); ok {
		for _, o := range raw {
			if s, ok := o.(string); ok && s != "" {
				options = append(options, s)
			}
		}
	}
	req, err := fileInputRequest(root, item.ID, run.RunID, question, options)
	if err != nil {
		return err
	}
	run.AwaitingInput = req.RequestID
	return run.save(root)
}

// fileInputRequest files a clarification (or decision, with ≥2 options)
// request, deduplicating on work-item + question so repeated filings reuse
// one pending request instead of stacking.
func fileInputRequest(root, workItemID, runID, question string, options []string) (*input.Request, error) {
	// Best-effort dedupe: unreadable files cannot match this question
	// anyway, and a fresh request is safer than a failed collect.
	pending, _ := input.Pending(root)
	for _, r := range pending {
		if r.WorkItemID == workItemID && r.Question == question {
			return r, nil // already on file
		}
	}
	kind := input.KindClarification
	if len(options) >= 2 {
		kind = input.KindDecision
	}
	return input.Create(root, input.Request{
		Kind:       kind,
		WorkItemID: workItemID,
		RunID:      runID,
		Question:   question,
		Options:    options,
	})
}

// fileGateOrErr converts an ApprovalRequiredError into a durable approval
// request and reports success — the collected outcome applied cleanly; only
// the advancement waits on the human. Non-gate errors pass through.
func fileGateOrErr(root string, err error) error {
	var gate *state.ApprovalRequiredError
	if !errors.As(err, &gate) {
		return err
	}
	op := gate.Op
	if op == "" {
		op = "transition"
	}
	_, err = input.Create(root, input.Request{
		Kind:       input.KindApproval,
		WorkItemID: gate.ID,
		Question:   fmt.Sprintf("Approve %s of %s (%s → %s)?", op, gate.ID, gate.From, gate.To),
		Action:     &input.Action{Type: op, WorkItemID: gate.ID, Target: gate.To},
	})
	return err
}

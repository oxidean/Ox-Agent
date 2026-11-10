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
	"slices"
	"strings"
	"time"

	"github.com/oxidean/Ox-Agent/internal/appctx"
	"github.com/oxidean/Ox-Agent/internal/contract"
	"github.com/oxidean/Ox-Agent/internal/filelock"
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
	// PendingEvent holds a journal event whose append failed after the
	// run was already persisted. A failed run is never collected again,
	// so the type and payload ride on the record until a later collect
	// for the same work item retries the append (#53).
	PendingEvent     string         `json:"pending_event,omitempty"`
	PendingEventData map[string]any `json:"pending_event_data,omitempty"`
	// ReconcileWarnings carries non-fatal pending-event replay failures
	// seen during this collect. The marker stays on the other run's
	// record and a later collect retries it — one run's broken
	// bookkeeping must not block an unrelated run's collection (#53).
	ReconcileWarnings []string `json:"-"`
	Dir               string   `json:"-"`
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
// rememberPendingEvent stores a standard skill.failed payload for
// replay; callers re-save after invoking it.
func (r *Run) rememberPendingEvent(eventType string) {
	r.PendingEvent = eventType
	r.PendingEventData = map[string]any{"run_id": r.RunID, "skill": r.Skill, "stage": r.Stage, "error": r.Error}
}

// reconcilePendingEvents retries journal appends that failed after their
// runs were already persisted. A failed run is never collected again, so
// without this the event is lost for good once journal access returns.
// Per-run failures are skipped — the marker stays on that run's record
// for the next collect — and returned as warnings so one run's broken
// bookkeeping can never block an unrelated run's collection (#53).
func reconcilePendingEvents(root, itemID string) []string {
	var warnings []string
	entries, err := os.ReadDir(runsDir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{fmt.Sprintf("listing run dirs for pending-event replay: %v", err)}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		r, err := LoadRun(root, entry.Name())
		if err != nil || r.WorkItemID != itemID || r.PendingEvent == "" {
			continue
		}
		runID, _ := r.PendingEventData["run_id"].(string)
		// A save failure after a successful append leaves the marker
		// set: checking the journal first keeps the replay idempotent.
		seen, seenErr := journalHasEvent(root, itemID, r.PendingEvent, runID)
		switch {
		case seenErr != nil:
			warnings = append(warnings, fmt.Sprintf("run %s: checking journal for replayed event: %v", r.RunID, seenErr))
			continue
		case seen:
			// Already journaled — fall through to clear the marker.
		default:
			if err := state.Append(root, state.Event{
				Type:       r.PendingEvent,
				WorkItemID: r.WorkItemID,
				Actor:      "ox-agent",
				Data:       r.PendingEventData,
			}); err != nil {
				warnings = append(warnings, fmt.Sprintf("run %s: replaying %s: %v", r.RunID, r.PendingEvent, err))
				continue
			}
		}
		r.PendingEvent, r.PendingEventData = "", nil
		if err := r.save(root); err != nil {
			warnings = append(warnings, fmt.Sprintf("run %s: clearing replayed event marker: %v", r.RunID, err))
		}
	}
	return warnings
}

// journalHasEvent reports whether an event of eventType tagged with the
// run id is already in the item's journal. A replayed append must not
// duplicate the entry when an earlier append succeeded but the
// marker-clearing save failed (#53).
func journalHasEvent(root, itemID, eventType, runID string) (bool, error) {
	ev, err := journalFindEvent(root, itemID, eventType, runID)
	return ev != nil, err
}

// journalFindEvent returns the item's journaled event of eventType tagged
// with runID, or nil when absent. The returned record is what a retrying
// collect must re-apply — the file on disk may have changed since it was
// journaled (#53).
func journalFindEvent(root, itemID, eventType, runID string) (*state.Event, error) {
	if runID == "" {
		return nil, nil
	}
	events, err := state.EventsFor(root, itemID, 0)
	if err != nil {
		return nil, err
	}
	for i := range events {
		if events[i].Type == eventType && events[i].Data["run_id"] == runID {
			return &events[i], nil
		}
	}
	return nil, nil
}

// outcomeJournaled reports whether the item's journal already holds an
// outcome event — skill.completed or skill.failed — tagged with runID.
// A validation-failure collect must not append skill.failed beside a
// skill.completed journaled before an output.json rewrite: one run,
// one outcome event (#53).
func outcomeJournaled(root, itemID, runID string) (bool, error) {
	for _, eventType := range []string{"skill.completed", "skill.failed"} {
		ev, err := journalFindEvent(root, itemID, eventType, runID)
		if err != nil {
			return false, err
		}
		if ev != nil {
			return true, nil
		}
	}
	return false, nil
}

// deferForJournaledOutcome reports whether a collect-time output rejection
// must leave the run pending instead of failing it, returning a non-empty
// explanation when it does. When the journal already holds this run's
// outcome, the event stays authoritative: a failed run is terminal, so
// marking it failed would strand the journaled outcome unapplied forever —
// the operator corrects output.json and re-collects instead (#53). The run
// record is saved so reconcile warnings stay durable; an unreadable journal
// is treated as unknown, keeping the failure path the authority.
func deferForJournaledOutcome(root string, run *Run) string {
	journaledOutcome, err := outcomeJournaled(root, run.WorkItemID, run.RunID)
	if err != nil || !journaledOutcome {
		return ""
	}
	saveErr := run.save(root)
	return fmt.Sprintf("outcome already journaled — run kept pending so a corrected output.json can re-collect (save: %v)", saveErr)
}

// journalFailureOnce appends skill.failed unless the journal already holds
// an outcome event for the run. An unreadable journal is treated as unknown,
// so the append path stays the authority (#53).
func journalFailureOnce(root string, item *state.WorkItem, run *Run) error {
	journaled, jerr := outcomeJournaled(root, item.ID, run.RunID)
	if jerr == nil && journaled {
		return nil
	}
	return state.Append(root, state.Event{
		Type:       "skill.failed",
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data:       map[string]any{"run_id": run.RunID, "skill": run.Skill, "stage": run.Stage, "error": run.Error},
	})
}

func Collect(root, runID string) (*Run, map[string]any, error) {
	run, err := LoadRun(root, runID)
	if err != nil {
		return nil, nil, err
	}
	// Serialize collection per work item: two concurrent collects for the
	// same item could both replay a pending event or both apply an
	// outcome. The kernel lock serializes the whole critical section
	// (#53).
	lockPath := filepath.Join(root, ".ox-agent", "runtime", "locks", "collect-"+run.WorkItemID+".lock")
	var output map[string]any
	err = filelock.With(lockPath, filelock.DefaultTimeout, func() error {
		// Reload under the lock: the snapshot taken before queuing can be
		// stale by the time the lock is granted — a sibling collector may
		// have applied this run's outcome already. Acting on the stale
		// record would apply the completed outcome a second time (#53).
		fresh, lerr := LoadRun(root, runID)
		if lerr != nil {
			return lerr
		}
		run = fresh
		var cerr error
		run, output, cerr = collectLocked(root, run)
		return cerr
	})
	return run, output, err
}

// collectLocked is Collect's critical section, run under the per-item
// collect lock.
func collectLocked(root string, run *Run) (*Run, map[string]any, error) {
	// Replay events that could not be journaled on an earlier collect for
	// this work item: the journal is the audit and replay source, so a
	// missing skill.failed must not be lost just because that run is no
	// longer pending. Replay failures are non-fatal — the marker stays
	// on the other run's record for a later retry.
	run.ReconcileWarnings = reconcilePendingEvents(root, run.WorkItemID)
	runID := run.RunID
	if run.Status != "pending" {
		// Return the run so the CLI can still surface the reconcile
		// warnings — a replay failure on an already-failed run is exactly
		// the case where the "not pending" error alone hides the recovery
		// failure (#53).
		return run, nil, fmt.Errorf("run %s is %s, not pending", runID, run.Status)
	}
	item, err := state.Resolve(root, run.WorkItemID)
	if err != nil {
		return run, nil, err
	}
	outputPath := filepath.Join(run.Dir, "output.json")
	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		return run, nil, fmt.Errorf("run %s has no output.json yet: %w", runID, err)
	}
	skill := strings.TrimPrefix(run.Skill, "ox-agent/")
	schemaPath := filepath.Join(root, ".ox-agent", "skills", skill, "schemas", "output.schema.json")
	if err := contract.ValidateFile(schemaPath, outputData); err != nil {
		if deferred := deferForJournaledOutcome(root, run); deferred != "" {
			return run, nil, fmt.Errorf("output for %s failed validation: %w (%s)", runID, err, deferred)
		}
		run.Status = "failed"
		run.Error = "output failed schema validation: " + err.Error()
		saveErr := run.save(root)
		var appendErr error
		if saveErr == nil {
			// Journal only once the run record is durable: a failed save
			// leaves the run pending on disk, so a later collect replays
			// the whole outcome — appending now would double the event.
			// An outcome already journaled for this run is kept as the
			// record: a completed run restored to pending that re-fails
			// must not gain a second event (#53). An unreadable journal
			// is unknown — the append path stays the authority.
			appendErr = journalFailureOnce(root, item, run)
		}
		if appendErr != nil {
			run.rememberPendingEvent("skill.failed")
			saveErr = run.save(root)
		}
		return run, nil, fmt.Errorf("output for %s failed validation: %w (record failures: save=%v event=%v)", runID, err, saveErr, appendErr)
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
		if deferred := deferForJournaledOutcome(root, run); deferred != "" {
			return run, output, fmt.Errorf("invalid artifact path for %s: %w (%s)", runID, err, deferred)
		}
		run.Status = "failed"
		run.Error = "invalid artifact path: " + err.Error()
		saveErr := run.save(root)
		var appendErr error
		if saveErr == nil {
			// Same durability + dedupe contract as the schema-failure
			// path above (#53).
			appendErr = journalFailureOnce(root, item, run)
		}
		if appendErr != nil {
			run.rememberPendingEvent("skill.failed")
			if saveErr == nil {
				saveErr = run.save(root)
			}
		}
		// Surface persistence failures alongside the path error: a run
		// that stayed pending or failed without its event must be visible,
		// not silently reconcilable.
		return run, output, fmt.Errorf("invalid artifact path for %s: %w (record failures: save=%v event=%v)", runID, err, saveErr, appendErr)
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
		return run, nil, err
	}
	eventType := "skill.completed"
	if run.Status == "failed" {
		eventType = "skill.failed"
	}
	eventData := map[string]any{"run_id": runID, "skill": run.Skill, "stage": run.Stage, "status": status, "verdict": verdict, "next_action": action}
	// A retried collect (restored to pending after the append succeeded
	// but the outcome failed to apply) must not journal the event
	// twice. An unreadable journal is treated as unknown: the append
	// path below stays the authority and keeps failures retryable.
	// Look up EITHER outcome event: a run journaled skill.completed whose
	// output.json was later revised to failed (or vice versa) must not
	// journal the second type alongside it — checking only the current
	// type would miss the recorded outcome entirely (#53).
	journaled, _ := journalFindEvent(root, run.WorkItemID, eventType, runID)
	if journaled == nil {
		otherType := "skill.failed"
		if eventType == "skill.failed" {
			otherType = "skill.completed"
		}
		journaled, _ = journalFindEvent(root, run.WorkItemID, otherType, runID)
	}
	if journaled == nil {
		if err := state.Append(root, state.Event{
			Type:       eventType,
			WorkItemID: item.ID,
			Actor:      "ox-agent",
			Data:       eventData,
		}); err != nil {
			if run.Status == "collected" || blocking || needsInput {
				// The outcome still has to run — restoring pending keeps the
				// whole tail (journal append + applyOutcome) retryable. An
				// event-only replay would journal the result but never
				// advance, unblock, or file the input request (#53).
				run.Status = "pending"
			} else {
				// Terminal runs are never collected again: the pending
				// event is the retry vehicle for the journal record itself.
				run.PendingEvent = eventType
				run.PendingEventData = eventData
			}
			if saveErr := run.save(root); saveErr != nil {
				return run, output, fmt.Errorf("%w (also failed to persist run state: %v)", err, saveErr)
			}
			return run, output, err
		}
	} else {
		// The journal already holds this run's outcome: the retry must
		// apply what was recorded, not whatever output.json holds now.
		// A drifted file means the outcome changed post-journal — refuse
		// rather than apply an outcome the journal never saw, and restore
		// pending so a corrected output.json can re-collect (#53).
		if journaled.Type != eventType ||
			journaled.Data["verdict"] != verdict ||
			journaled.Data["next_action"] != action ||
			journaled.Data["status"] != status {
			run.Status = "pending"
			saveErr := run.save(root)
			return run, output, fmt.Errorf(
				"run %s output changed since %s was journaled (journaled verdict=%v next_action=%v status=%v; file has verdict=%v next_action=%v status=%v) — restore the journaled output or re-dispatch (restore pending: %v)",
				runID, journaled.Type,
				journaled.Data["verdict"], journaled.Data["next_action"], journaled.Data["status"],
				verdict, action, status, saveErr)
		}
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
// but invalid in committed paths checked out on Windows — plus the backslash
// (a Windows separator, so "a\..\..\x" traverses there while looking like a
// flat filename to a POSIX collect) and ASCII control characters.
var unsafeArtifactChars = regexp.MustCompile(`[*?"<>|:\\` + "\x00-\x1f" + `]`)

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
			// A "." segment is a clean no-op that filepath.IsLocal allows;
			// only real segments carrying a trailing dot or space break a
			// Windows checkout (#53).
			if seg == "." {
				continue
			}
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
		if runID != "" && r.RunID == runID {
			// One request per run: the journaled filing is authoritative —
			// a retried collect whose output now asks something different
			// must refuse, not stack a second pending request (#53).
			if r.Question != question || !slices.Equal(r.Options, options) {
				return nil, fmt.Errorf(
					"run %s already filed request %s (question %q, options %v); refusing to file different question %q options %v — answer the pending request or restore the journaled output",
					runID, r.RequestID, r.Question, r.Options, question, options,
				)
			}
			return r, nil
		}
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

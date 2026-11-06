// Package input manages durable human-input requests under
// .ox-agent/state/input/, per input-request.schema.json. Requests persist
// on disk so async-chat agents never block on synchronous prompts: file a
// request, end the turn, and let the human answer whenever they respond.
package input

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oxidean/Ox-Agent/internal/state"
	"gopkg.in/yaml.v3"
)

// Request kinds — the shapes a human answer can take.
const (
	KindApproval      = "approval"      // yes/no — approve executes Action
	KindClarification = "clarification" // free-text answer
	KindDecision      = "decision"      // choose one of Options
)

// Request statuses.
const (
	StatusPending   = "pending"
	StatusAnswered  = "answered"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
)

// Action is the pending operation an approval request executes on
// --approve. Only lifecycle operations are supported.
type Action struct {
	Type       string `yaml:"type" json:"type"` // transition | archive
	WorkItemID string `yaml:"work_item_id" json:"work_item_id"`
	Target     string `yaml:"target" json:"target"`
}

// Request mirrors input-request.schema.json.
type Request struct {
	SchemaVersion int      `yaml:"schema_version" json:"schema_version"`
	RequestID     string   `yaml:"request_id" json:"request_id"`
	WorkItemID    string   `yaml:"work_item_id,omitempty" json:"work_item_id,omitempty"`
	RunID         string   `yaml:"run_id,omitempty" json:"run_id,omitempty"`
	Kind          string   `yaml:"kind" json:"kind"`
	Question      string   `yaml:"question" json:"question"`
	Options       []string `yaml:"options,omitempty" json:"options,omitempty"`
	Action        *Action  `yaml:"action,omitempty" json:"action,omitempty"`
	Status        string   `yaml:"status" json:"status"`
	Answer        string   `yaml:"answer,omitempty" json:"answer,omitempty"`
	CreatedBy     string   `yaml:"created_by" json:"created_by"`
	AnsweredBy    string   `yaml:"answered_by,omitempty" json:"answered_by,omitempty"`
	CreatedAt     string   `yaml:"created_at" json:"created_at"`
	AnsweredAt    string   `yaml:"answered_at,omitempty" json:"answered_at,omitempty"`
	ExpiresAt     string   `yaml:"expires_at,omitempty" json:"expires_at,omitempty"`

	path string // absolute file location; never serialized
}

// idPattern constrains request IDs to the generated shape so CLI-supplied
// IDs can never resolve outside state/input.
var idPattern = regexp.MustCompile(`^IN-[0-9]+$`)

// createLockStaleAfter bounds how long an orphaned create lock file is
// honored before a waiting process reclaims it.
const createLockStaleAfter = 30 * time.Second

func dir(root string) string {
	return filepath.Join(root, ".ox-agent", "state", "input")
}

// withLock serializes the dedupe + ID-allocation + save + journal critical
// section across concurrent ox-agent processes, the same pattern
// withWorkItemLock uses for work-item allocation.
func withLock(root string, fn func() error) error {
	if err := os.MkdirAll(dir(root), 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(dir(root), ".create.lock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(lock, "pid=%d\n", os.Getpid())
			lock.Close()
			defer os.Remove(lockPath)
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("acquiring input lock: %w", err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > createLockStaleAfter {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for input create lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// Create files a new input request, journaling input.requested. For
// approval requests carrying an Action, an identical pending request is
// returned instead of stacking duplicates — repeated gate hits
// idempotently reuse the same request. The dedupe check, ID allocation,
// file write and journal append run under a lockfile so concurrent
// processes cannot claim the same ID or duplicate a pending approval.
func Create(root string, req Request) (_ *Request, err error) {
	switch req.Kind {
	case KindApproval, KindClarification, KindDecision:
	default:
		return nil, fmt.Errorf("kind must be approval, clarification, or decision")
	}
	if strings.TrimSpace(req.Question) == "" {
		return nil, fmt.Errorf("question is required")
	}
	if req.Kind == KindDecision && len(req.Options) < 2 {
		return nil, fmt.Errorf("decision requests need at least two --option values")
	}
	if req.Kind == KindApproval {
		if req.Action == nil {
			return nil, fmt.Errorf("approval requests need an action")
		}
		switch req.Action.Type {
		case "transition", "archive":
		default:
			return nil, fmt.Errorf("unsupported action type %q", req.Action.Type)
		}
	}
	if req.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, req.ExpiresAt); err != nil {
			return nil, fmt.Errorf("expires_at must be RFC3339: %w", err)
		}
	}
	var created *Request
	err = withLock(root, func() error {
		if req.Kind == KindApproval {
			existing, err := FindPendingAction(root, req.Action.WorkItemID, req.Action.Type, req.Action.Target)
			if err != nil {
				return err
			}
			if existing != nil {
				created = existing
				return nil
			}
		}
		req.SchemaVersion = 1
		req.RequestID = nextID(root)
		req.Status = StatusPending
		if req.CreatedBy == "" {
			req.CreatedBy = "ox-agent"
		}
		req.CreatedAt = now()
		req.path = filepath.Join(dir(root), req.RequestID+".yaml")
		if err := req.save(); err != nil {
			return err
		}
		eventData := map[string]any{"request_id": req.RequestID, "kind": req.Kind, "question": req.Question}
		if req.RunID != "" {
			eventData["run_id"] = req.RunID
		}
		if req.Action != nil {
			eventData["action"] = fmt.Sprintf("%s %s → %s", req.Action.Type, req.Action.WorkItemID, req.Action.Target)
		}
		if err := state.Append(root, state.Event{
			Type:       "input.requested",
			WorkItemID: req.WorkItemID,
			Actor:      req.CreatedBy,
			Data:       eventData,
		}); err != nil {
			_ = os.Remove(req.path)
			return err
		}
		created = &req
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// nextID returns the lowest free IN-NNNN number.
func nextID(root string) string {
	entries, err := os.ReadDir(dir(root))
	if err != nil {
		return "IN-0001"
	}
	used := map[int]bool{}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".yaml")
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "IN-")); err == nil {
			used[n] = true
		}
	}
	for n := 1; ; n++ {
		if !used[n] {
			return fmt.Sprintf("IN-%04d", n)
		}
	}
}

// Load reads a request, applying lazy expiry: a pending request past its
// expires_at is marked expired, saved, and journaled once.
func Load(root, id string) (*Request, error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid request id %q", id)
	}
	path := filepath.Join(dir(root), id+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("input request %s not found", id)
		}
		return nil, err
	}
	var req Request
	if err := yaml.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", id, err)
	}
	req.path = path
	if err := req.refreshExpiry(root); err != nil {
		return nil, err
	}
	return &req, nil
}

// List returns all loadable requests, oldest first, applying lazy expiry.
// Files that fail to load are reported in the issues slice rather than
// failing the whole listing — one corrupt file must not hide every other
// request. Issues name the offending file so it can be repaired (doctor's
// schema glob flags it too).
func List(root string) ([]*Request, []error) {
	entries, err := os.ReadDir(dir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{err}
	}
	var out []*Request
	var issues []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		req, err := Load(root, strings.TrimSuffix(e.Name(), ".yaml"))
		if err != nil {
			issues = append(issues, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		out = append(out, req)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestID < out[j].RequestID })
	return out, issues
}

// Pending returns loadable requests still awaiting a human answer, plus
// per-file load issues.
func Pending(root string) ([]*Request, []error) {
	all, issues := List(root)
	var out []*Request
	for _, r := range all {
		if r.Status == StatusPending {
			out = append(out, r)
		}
	}
	return out, issues
}

// FindPendingAction returns the pending approval request for an identical
// action, or nil — the dedupe key that keeps repeated gate hits from
// stacking duplicate requests. Unloadable files are surfaced as an error
// for approvals: silently skipping a possibly-pending file could let a
// duplicate approval slip through.
func FindPendingAction(root, workItemID, actionType, target string) (*Request, error) {
	pending, issues := Pending(root)
	if len(issues) > 0 {
		return nil, fmt.Errorf("cannot verify pending approvals — unloadable request files: %v", issues)
	}
	for _, r := range pending {
		if r.Kind == KindApproval && r.Action != nil &&
			r.Action.WorkItemID == workItemID && r.Action.Type == actionType && r.Action.Target == target {
			return r, nil
		}
	}
	return nil, nil
}

// refreshExpiry lazily marks a pending request expired once its deadline
// passes, journaling input.expired on the first observation.
func (r *Request) refreshExpiry(root string) error {
	if r.Status != StatusPending || r.ExpiresAt == "" {
		return nil
	}
	// An unparseable expires_at never expires the request — Create rejects
	// bad values at write time, and an existing oddity should not poison
	// every List/Load caller.
	deadline, err := time.Parse(time.RFC3339, r.ExpiresAt)
	if err != nil || time.Now().UTC().Before(deadline) {
		return nil
	}
	r.Status = StatusExpired
	if err := r.save(); err != nil {
		return err
	}
	return state.Append(root, state.Event{
		Type:       "input.expired",
		WorkItemID: r.WorkItemID,
		Actor:      "ox-agent",
		Data:       map[string]any{"request_id": r.RequestID},
	})
}

func (r *Request) save() error {
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, data, 0o644)
}

// finish records a terminal outcome. The journal append runs BEFORE the
// file save so a mid-finish crash leaves a pending file that a retry can
// close — journaledOutcome dedupes the event on retry. The reverse order
// would leave a terminal file whose event could never be recovered.
func (r *Request) finish(root, status, actor, answer string) error {
	journaled, err := journaledOutcome(root, r.RequestID, status)
	if err != nil {
		return err
	}
	if !journaled {
		if err := state.Append(root, state.Event{
			Type:       "input.answered",
			WorkItemID: r.WorkItemID,
			Actor:      actor,
			Data: map[string]any{
				"request_id": r.RequestID,
				"outcome":    status,
				"answer":     answer,
			},
		}); err != nil {
			return err
		}
	}
	r.Status = status
	r.AnsweredBy = actor
	r.AnsweredAt = now()
	r.Answer = answer
	return r.save()
}

// journaledOutcome reports whether an input.answered event for this
// request and outcome already exists — the dedupe that makes a retried
// finish idempotent.
func journaledOutcome(root, id, outcome string) (bool, error) {
	events, err := state.ReadEvents(root)
	if err != nil {
		return false, err
	}
	for _, e := range events {
		if e.Type == "input.answered" && e.Data["request_id"] == id && e.Data["outcome"] == outcome {
			return true, nil
		}
	}
	return false, nil
}

// Respond records a free-text or option answer. Decision answers must
// match a declared option.
func Respond(root, id, actor, answer string) (*Request, error) {
	req, err := Load(root, id)
	if err != nil {
		return nil, err
	}
	if req.Status != StatusPending {
		return nil, fmt.Errorf("input request %s is %s, not pending", id, req.Status)
	}
	if strings.TrimSpace(answer) == "" {
		return nil, fmt.Errorf("answer is required")
	}
	if req.Kind == KindDecision {
		ok := false
		for _, opt := range req.Options {
			if answer == opt {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("answer must be one of: %s", strings.Join(req.Options, ", "))
		}
	}
	if err := req.finish(root, StatusAnswered, actor, answer); err != nil {
		return nil, err
	}
	return req, nil
}

// Approve executes the request's pending action through the normal
// lifecycle path — the human's answer arrives late, so the gated work
// proceeds exactly as if --by had been passed originally.
func Approve(root, id, actor string) (*Request, error) {
	req, err := Load(root, id)
	if err != nil {
		return nil, err
	}
	if req.Status != StatusPending {
		return nil, fmt.Errorf("input request %s is %s, not pending", id, req.Status)
	}
	if req.Kind != KindApproval {
		return nil, fmt.Errorf("input request %s is %s, not an approval", id, req.Kind)
	}
	if req.Action == nil {
		return nil, fmt.Errorf("approval request %s has no action to execute", id)
	}
	item, err := state.Resolve(root, req.Action.WorkItemID)
	if err != nil {
		return nil, err
	}
	reason := "approved via " + id
	// Idempotent against a prior attempt that executed the action but
	// failed before recording the outcome: if the work item already sits
	// at the requested end state, the action is done — skip re-execution
	// and just finalize the request.
	switch req.Action.Type {
	case "transition":
		if item.State != req.Action.Target {
			err = item.Transition(root, req.Action.Target, actor, actor, reason)
		}
	case "archive":
		if item.State != "archived" {
			err = item.Archive(root, actor, actor, reason)
		}
	default:
		err = fmt.Errorf("unsupported action type %q", req.Action.Type)
	}
	if err != nil {
		return nil, err
	}
	if err := req.finish(root, StatusApproved, actor, "approved"); err != nil {
		return nil, err
	}
	return req, nil
}

// Reject closes an approval request without executing the action.
func Reject(root, id, actor, reason string) (*Request, error) {
	req, err := Load(root, id)
	if err != nil {
		return nil, err
	}
	if req.Status != StatusPending {
		return nil, fmt.Errorf("input request %s is %s, not pending", id, req.Status)
	}
	if err := req.finish(root, StatusRejected, actor, reason); err != nil {
		return nil, err
	}
	return req, nil
}

// Cancel withdraws a pending request.
func Cancel(root, id, actor string) (*Request, error) {
	req, err := Load(root, id)
	if err != nil {
		return nil, err
	}
	if req.Status != StatusPending {
		return nil, fmt.Errorf("input request %s is %s, not pending", id, req.Status)
	}
	req.Status = StatusCancelled
	req.AnsweredBy = actor
	req.AnsweredAt = now()
	if err := req.save(); err != nil {
		return nil, err
	}
	if err := state.Append(root, state.Event{
		Type:       "input.cancelled",
		WorkItemID: req.WorkItemID,
		Actor:      actor,
		Data:       map[string]any{"request_id": req.RequestID},
	}); err != nil {
		return nil, err
	}
	return req, nil
}

// ErrWaitTimeout reports a Wait that exhausted its Timeout with the
// request still pending — a normal async condition, not a failure:
// callers poll again next turn instead of treating it as expired.
var ErrWaitTimeout = errors.New("input request still pending")

// WaitOptions controls the exponential-backoff poll in Wait.
type WaitOptions struct {
	Interval    time.Duration // first poll gap (default 5s)
	Factor      float64       // backoff multiplier (default 2)
	MaxInterval time.Duration // poll-gap ceiling (default 60s)
	Timeout     time.Duration // overall cap; 0 = until terminal or ctx done
}

// Wait polls a request with exponential backoff until it reaches a
// terminal status, the timeout elapses, or ctx is cancelled. The backoff
// bounds each turn's cost — async agents call it briefly per turn while
// daemons can let it sit.
func Wait(ctx context.Context, root, id string, opts WaitOptions) (*Request, error) {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	if opts.Factor <= 1 {
		opts.Factor = 2
	}
	if opts.MaxInterval <= 0 {
		opts.MaxInterval = time.Minute
	}
	deadline := time.Now().Add(opts.Timeout)
	gap := opts.Interval
	for {
		req, err := Load(root, id)
		if err != nil {
			return nil, err
		}
		if req.Status != StatusPending {
			return req, nil
		}
		if opts.Timeout > 0 && !time.Now().Before(deadline) {
			return req, fmt.Errorf("%w: %s", ErrWaitTimeout, id)
		}
		wait := gap
		if opts.Timeout > 0 {
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
		}
		select {
		case <-ctx.Done():
			return req, ctx.Err()
		case <-time.After(wait):
		}
		gap = time.Duration(math.Min(float64(gap)*opts.Factor, float64(opts.MaxInterval)))
	}
}

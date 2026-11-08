package input

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
)

// setup initializes a project and returns its root.
func setup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := project.Init(root); err != nil {
		t.Fatalf("project.Init() error = %v", err)
	}
	return root
}

// eventsOfType returns journal events of a given type.
func eventsOfType(t *testing.T, root, typ string) []state.Event {
	t.Helper()
	events, err := state.ReadEvents(root)
	if err != nil {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	var out []state.Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func TestCreateClarification(t *testing.T) {
	root := setup(t)
	req, err := Create(root, Request{Kind: KindClarification, WorkItemID: "WI-0001", Question: "which direction?"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if req.RequestID != "IN-0001" || req.Status != StatusPending {
		t.Fatalf("got %v", req)
	}
	if got := eventsOfType(t, root, "input.requested"); len(got) != 1 {
		t.Fatalf("input.requested events = %d, want 1", len(got))
	}
}

func TestCreateValidation(t *testing.T) {
	root := setup(t)
	cases := []Request{
		{Kind: "bogus", Question: "q"},
		{Kind: KindClarification, Question: "  "},
		{Kind: KindDecision, Question: "q", Options: []string{"only"}},
		{Kind: KindApproval, Question: "q"},
	}
	for i, req := range cases {
		if _, err := Create(root, req); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestApprovalDedupe(t *testing.T) {
	root := setup(t)
	action := &Action{Type: "transition", WorkItemID: "WI-0001", Target: "shipped"}
	first, err := Create(root, Request{Kind: KindApproval, Question: "approve?", Action: action})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := Create(root, Request{Kind: KindApproval, Question: "approve?", Action: action})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if first.RequestID != second.RequestID {
		t.Fatalf("dedupe failed: %s vs %s", first.RequestID, second.RequestID)
	}
	if got := eventsOfType(t, root, "input.requested"); len(got) != 1 {
		t.Fatalf("dedupe journaled twice: %d events", len(got))
	}
}

func TestRespondClarification(t *testing.T) {
	root := setup(t)
	Create(root, Request{Kind: KindClarification, Question: "q"})
	req, err := Respond(root, "IN-0001", "jesse", "go left")
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if req.Status != StatusAnswered || req.Answer != "go left" || req.AnsweredBy != "jesse" {
		t.Fatalf("got %+v", req)
	}
	if _, err := Respond(root, "IN-0001", "jesse", "again"); err == nil {
		t.Fatal("respond on terminal request must fail")
	}
	if got := eventsOfType(t, root, "input.answered"); len(got) != 1 {
		t.Fatalf("input.answered events = %d", len(got))
	}
}

func TestRespondDecisionOptions(t *testing.T) {
	root := setup(t)
	Create(root, Request{Kind: KindDecision, Question: "pick", Options: []string{"a", "b"}})
	if _, err := Respond(root, "IN-0001", "jesse", "c"); err == nil {
		t.Fatal("out-of-options answer must fail")
	}
	req, err := Respond(root, "IN-0001", "jesse", "b")
	if err != nil || req.Status != StatusAnswered {
		t.Fatalf("Respond() = %+v, %v", req, err)
	}
}

// gatedItem returns a work item parked at shipping, before the gated edge.
func gatedItem(t *testing.T, root string) *state.WorkItem {
	t.Helper()
	item, err := state.Start(root, "gated work")
	if err != nil {
		t.Fatalf("state.Start() error = %v", err)
	}
	item.State = "shipping"
	if err := item.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return item
}

func TestApproveExecutesTransition(t *testing.T) {
	root := setup(t)
	item := gatedItem(t, root)
	Create(root, Request{
		Kind:       KindApproval,
		WorkItemID: item.ID,
		Question:   "ship it?",
		Action:     &Action{Type: "transition", WorkItemID: item.ID, Target: "shipped"},
	})
	req, err := Approve(root, "IN-0001", "jesse")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if req.Status != StatusApproved {
		t.Fatalf("status = %s", req.Status)
	}
	reloaded, err := state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if reloaded.State != "shipped" {
		t.Fatalf("work item state = %s, want shipped", reloaded.State)
	}
	// The approval ran the real gated path — approver is on the event.
	var approved bool
	for _, e := range eventsOfType(t, root, "work_item.transitioned") {
		if e.Data["approved_by"] == "jesse" {
			approved = true
		}
	}
	if !approved {
		t.Fatal("no transitioned event with approved_by=jesse")
	}
}

func TestApproveArchive(t *testing.T) {
	root := setup(t)
	item, err := state.Start(root, "archive me")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	Create(root, Request{
		Kind:       KindApproval,
		WorkItemID: item.ID,
		Question:   "archive?",
		Action:     &Action{Type: "archive", WorkItemID: item.ID, Target: "archived"},
	})
	if _, err := Approve(root, "IN-0001", "jesse"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
}

func TestApproveRejectsNonApproval(t *testing.T) {
	root := setup(t)
	Create(root, Request{Kind: KindClarification, Question: "q"})
	if _, err := Approve(root, "IN-0001", "jesse"); err == nil {
		t.Fatal("approve on clarification must fail")
	}
}

func TestRejectAndCancel(t *testing.T) {
	root := setup(t)
	Create(root, Request{Kind: KindClarification, Question: "a"})
	Create(root, Request{Kind: KindClarification, Question: "b"})
	if req, err := Reject(root, "IN-0001", "jesse", "not now"); err != nil || req.Status != StatusRejected {
		t.Fatalf("Reject() = %+v, %v", req, err)
	}
	if req, err := Cancel(root, "IN-0002", "jesse"); err != nil || req.Status != StatusCancelled {
		t.Fatalf("Cancel() = %+v, %v", req, err)
	}
	if got := eventsOfType(t, root, "input.cancelled"); len(got) != 1 {
		t.Fatalf("input.cancelled = %d", len(got))
	}
	if pending, _ := Pending(root); len(pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(pending))
	}
}

func TestLazyExpiry(t *testing.T) {
	root := setup(t)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	Create(root, Request{Kind: KindClarification, Question: "q", ExpiresAt: past})
	req, err := Load(root, "IN-0001")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if req.Status != StatusExpired {
		t.Fatalf("status = %s, want expired", req.Status)
	}
	if got := eventsOfType(t, root, "input.expired"); len(got) != 1 {
		t.Fatalf("input.expired = %d", len(got))
	}
	// Second load observes an already-terminal request — no double journal.
	Load(root, "IN-0001")
	if got := eventsOfType(t, root, "input.expired"); len(got) != 1 {
		t.Fatalf("expiry double-journaled: %d", len(got))
	}
	if _, err := Respond(root, "IN-0001", "jesse", "late"); err == nil {
		t.Fatal("respond on expired must fail")
	}
}

func TestWaitBackoff(t *testing.T) {
	root := setup(t)
	Create(root, Request{Kind: KindClarification, Question: "q"})
	// Fast path: already answered returns immediately.
	Respond(root, "IN-0001", "jesse", "done")
	req, err := Wait(context.Background(), root, "IN-0001", WaitOptions{Interval: time.Millisecond})
	if err != nil || req.Status != StatusAnswered {
		t.Fatalf("Wait() = %+v, %v", req, err)
	}
	// Timeout path: bounded wait on a pending request errors.
	Create(root, Request{Kind: KindClarification, Question: "unanswered"})
	start := time.Now()
	_, err = Wait(context.Background(), root, "IN-0002", WaitOptions{
		Interval: 5 * time.Millisecond, MaxInterval: 10 * time.Millisecond, Timeout: 30 * time.Millisecond,
	})
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("Wait() error = %v, want ErrWaitTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("wait ran too long: %s", elapsed)
	}
	// Cancellation path: ctx done interrupts the backoff.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if _, err := Wait(ctx, root, "IN-0002", WaitOptions{Interval: time.Hour}); err == nil {
		t.Fatal("cancelled wait must return an error")
	}
}

func TestFindPendingAction(t *testing.T) {
	root := setup(t)
	if found, _ := FindPendingAction(root, "WI-0001", "transition", "shipped"); found != nil {
		t.Fatal("no requests yet — must return nil")
	}
	Create(root, Request{
		Kind:     KindApproval,
		Question: "q",
		Action:   &Action{Type: "transition", WorkItemID: "WI-0001", Target: "shipped"},
	})
	if found, _ := FindPendingAction(root, "WI-0001", "transition", "shipped"); found == nil {
		t.Fatal("pending action not found")
	}
	if found, _ := FindPendingAction(root, "WI-0001", "archive", "archived"); found != nil {
		t.Fatal("different action must not match")
	}
}

// TestWaitTimeoutReportsPending verifies a bounded Wait expiring returns
// the request and the ErrWaitTimeout sentinel — a normal async signal,
// not a failure — so callers can poll again next turn.
func TestWaitTimeoutReportsPending(t *testing.T) {
	root := setup(t)
	req, err := Create(root, Request{Kind: KindClarification, Question: "still thinking?"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := Wait(context.Background(), root, req.RequestID, WaitOptions{Interval: time.Millisecond, Timeout: 30 * time.Millisecond})
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("Wait() error = %v, want ErrWaitTimeout", err)
	}
	if got == nil || got.Status != StatusPending {
		t.Fatalf("Wait() request = %+v, want pending request", got)
	}
}

// TestConcurrentStaleReclaimCreates verifies a leftover create-lock file
// never blocks: under kernel locking a dead holder's file is not held
// state, and every concurrent Create still lands a unique request.
func TestConcurrentStaleReclaimCreates(t *testing.T) {
	root := setup(t)
	// Plant a leftover create-lock file as a dead holder would leave it.
	lockPath := filepath.Join(locksDir(root), "input-create.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("dead-holder\n"), 0o644); err != nil {
		t.Fatalf("plant lock: %v", err)
	}
	const n = 8
	var wg sync.WaitGroup
	ids := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, err := Create(root, Request{Kind: KindClarification, Question: fmt.Sprintf("q%d", i)})
			if err != nil {
				errs <- err
				return
			}
			ids <- req.RequestID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("Create() error = %v", err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate request id %s — lock raced", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("created %d requests, want %d", len(seen), n)
	}
}

// TestConcurrentApproveExecutesOnce verifies the request lock serializes
// lifecycle changes: parallel approvers cannot double-execute the action
// or double-record the outcome.
func TestConcurrentApproveExecutesOnce(t *testing.T) {
	root := setup(t)
	item, err := state.Start(root, "approval target")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	req, err := Create(root, Request{
		Kind:       KindApproval,
		WorkItemID: item.ID,
		Question:   "ship it?",
		Action:     &Action{Type: "transition", WorkItemID: item.ID, Target: "discovering"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	const n = 5
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Approve(root, req.RequestID, fmt.Sprintf("user%d", i))
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d approvers succeeded, want exactly 1", succeeded)
	}
	transitions := 0
	for _, e := range eventsOfType(t, root, "work_item.transitioned") {
		if e.Data["to"] == "discovering" {
			transitions++
		}
	}
	if transitions != 1 {
		t.Fatalf("discovering transition executed %d times, want 1", transitions)
	}
}

// TestRequestLockRejectsTraversalIDs verifies an ID like ../../x can never
// mint a lock file outside runtime/locks — validation precedes filelock.With.
func TestRequestLockRejectsTraversalIDs(t *testing.T) {
	root := setup(t)
	for _, id := range []string{"../../x", "..%2F..%2Fx", "IN-", "IN-XXXX"} {
		if _, err := Respond(root, id, "me", "answer"); err == nil {
			t.Fatalf("Respond(%q) succeeded; want invalid-id error", id)
		}
	}
	outside := filepath.Join(locksDir(root), "..", "x.lock")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("lock escaped locks dir: %s exists", outside)
	}
}

// TestNextIDScansJournal verifies IDs recorded in journal events are never
// reissued even when their request file is gone — reuse would let
// journaledEvent match the old request's terminal event.
func TestNextIDScansJournal(t *testing.T) {
	root := setup(t)
	// IN-0001's file is gone but its terminal event survives — allocating
	// it again would let journaledEvent match the old outcome.
	if err := state.Append(root, state.Event{
		Type:       "input.answered",
		WorkItemID: "WI-0001",
		Data:       map[string]any{"request_id": "IN-0001", "outcome": "answered"},
	}); err != nil {
		t.Fatalf("journal: %v", err)
	}
	req, err := Create(root, Request{Kind: KindClarification, Question: "id?"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if req.RequestID != "IN-0002" {
		t.Fatalf("allocated %s, want IN-0002 (IN-0001 survives in journal)", req.RequestID)
	}
}

// TestGateReuseBackfillsWorkItem verifies a pending approval created
// without work-item scope gets it backfilled when a gate reuses it —
// preserving the append-only event history (no new requested event).
func TestGateReuseBackfillsWorkItem(t *testing.T) {
	root := setup(t)
	item, err := state.Start(root, "gate reuse")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// Plant a legacy-scope-less approval request directly on disk.
	if err := os.MkdirAll(dir(root), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `schema_version: 1
request_id: IN-0001
kind: approval
status: pending
question: ship it?
created_by: ox-agent
created_at: "2026-01-01T00:00:00Z"
action:
  type: transition
  work_item_id: ` + item.ID + `
  target: discovering
`
	if err := os.WriteFile(filepath.Join(dir(root), "IN-0001.yaml"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	got, err := Create(root, Request{
		Kind:       KindApproval,
		WorkItemID: item.ID,
		Question:   "ship it?",
		Action:     &Action{Type: "transition", WorkItemID: item.ID, Target: "discovering"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.RequestID != "IN-0001" {
		t.Fatalf("dedupe failed — created %s instead of reusing IN-0001", got.RequestID)
	}
	if got.WorkItemID != item.ID {
		t.Fatalf("WorkItemID = %q, want %q backfilled", got.WorkItemID, item.ID)
	}
	if n := len(eventsOfType(t, root, "input.requested")); n != 0 {
		t.Fatalf("reuse journaled %d input.requested events — history must stay untouched", n)
	}
	// The backfilled scope is journaled durably as input.scoped — packet
	// compile relies on it to prove ownership when the request file
	// itself becomes unloadable.
	scoped := eventsOfType(t, root, "input.scoped")
	if len(scoped) != 1 {
		t.Fatalf("input.scoped events = %d, want 1", len(scoped))
	}
	if scoped[0].WorkItemID != item.ID || scoped[0].Data["request_id"] != "IN-0001" {
		t.Fatalf("input.scoped event = %+v", scoped[0])
	}
	// A second dedupe hit is a no-op: scope already set, journal stays at 1.
	if _, err := Create(root, Request{
		Kind:       KindApproval,
		WorkItemID: item.ID,
		Question:   "ship it?",
		Action:     &Action{Type: "transition", WorkItemID: item.ID, Target: "discovering"},
	}); err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if n := len(eventsOfType(t, root, "input.scoped")); n != 1 {
		t.Fatalf("input.scoped events after re-dedupe = %d, want 1", n)
	}
}

// TestBackfillRecoveryAfterInterruptedSave verifies a retry after a crash
// between the input.scoped journal append and the file save: the event is
// not duplicated and the scope still lands on the file.
func TestBackfillRecoveryAfterInterruptedSave(t *testing.T) {
	root := setup(t)
	item, err := state.Start(root, "interrupted backfill")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := os.MkdirAll(dir(root), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `schema_version: 1
request_id: IN-0001
kind: approval
status: pending
question: ship it?
created_by: ox-agent
created_at: "2026-01-01T00:00:00Z"
action:
  type: transition
  work_item_id: ` + item.ID + `
  target: discovering
`
	if err := os.WriteFile(filepath.Join(dir(root), "IN-0001.yaml"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	// Simulate the crash point: input.scoped already journaled, file
	// still unscoped.
	if err := state.Append(root, state.Event{
		Type:       "input.scoped",
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data:       map[string]any{"request_id": "IN-0001"},
	}); err != nil {
		t.Fatalf("journal: %v", err)
	}
	got, err := Create(root, Request{
		Kind:       KindApproval,
		WorkItemID: item.ID,
		Question:   "ship it?",
		Action:     &Action{Type: "transition", WorkItemID: item.ID, Target: "discovering"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.WorkItemID != item.ID {
		t.Fatalf("WorkItemID = %q, want %q backfilled", got.WorkItemID, item.ID)
	}
	if n := len(eventsOfType(t, root, "input.scoped")); n != 1 {
		t.Fatalf("input.scoped events = %d, want exactly 1 — the retry must not re-journal", n)
	}
}

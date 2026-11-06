package input

import (
	"context"
	"strings"
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
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Wait() error = %v, want timeout", err)
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

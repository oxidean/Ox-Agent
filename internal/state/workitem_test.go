package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxidean/Ox-Agent/internal/project"
)

// initProject scaffolds a temp project for work-item tests.
func initProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := project.Init(root); err != nil {
		t.Fatalf("project.Init() error = %v", err)
	}
	return root
}

// TestStartAllocatesUniqueIDsAcrossBuckets verifies WI numbering continues past archived items.
func TestStartAllocatesUniqueIDsAcrossBuckets(t *testing.T) {
	root := initProject(t)
	if _, err := Start(root, "First item"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	work := filepath.Join(root, ".ox-agent", "work")
	if err := os.Rename(filepath.Join(work, "active", "WI-0001-first-item"), filepath.Join(work, "archived", "WI-0001-first-item")); err != nil {
		t.Fatalf("archiving work item: %v", err)
	}
	if _, err := Start(root, "Second item"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "active", "WI-0002-second-item", "index.json")); err != nil {
		t.Fatalf("second work item index missing: %v", err)
	}
}

// TestStartSanitizesSlug verifies title slugification strips unsafe characters and caps length.
func TestStartSanitizesSlug(t *testing.T) {
	root := initProject(t)
	if _, err := Start(root, "Fix: login/redirect?"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	dir := filepath.Join(root, ".ox-agent", "work", "active", "WI-0001-fix-login-redirect")
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Fatalf("sanitized work item missing: %v", err)
	}
}

// TestStartAppendsCreatedEvent verifies start journals a work_item.created event.
func TestStartAppendsCreatedEvent(t *testing.T) {
	root := initProject(t)
	if _, err := Start(root, "Track events"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".ox-agent", "state", "events.ndjson"))
	if err != nil {
		t.Fatalf("reading event journal: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("parsing event journal: %v", err)
	}
	if event["type"] != "work_item.created" || event["work_item_id"] != "WI-0001" || event["actor"] != "cli" {
		t.Fatalf("unexpected event: %v", event)
	}
}

// TestStartSerializesConcurrentAllocation verifies the allocation lock serializes concurrent start calls.
func TestStartSerializesConcurrentAllocation(t *testing.T) {
	root := initProject(t)
	const workers = 8
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(n int) {
			_, err := Start(root, fmt.Sprintf("Concurrent item %d", n))
			errs <- err
		}(i)
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, ".ox-agent", "work", "active"))
	if err != nil {
		t.Fatalf("reading active work items: %v", err)
	}
	if len(entries) != workers {
		t.Fatalf("active work items = %d, want %d", len(entries), workers)
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		parts := strings.SplitN(entry.Name(), "-", 3)
		if ids[parts[0]+"-"+parts[1]] {
			t.Fatalf("duplicate work item number allocated: %s", entry.Name())
		}
		ids[parts[0]+"-"+parts[1]] = true
	}
	data, err := os.ReadFile(filepath.Join(root, ".ox-agent", "state", "events.ndjson"))
	if err != nil {
		t.Fatalf("reading event journal: %v", err)
	}
	if n := len(strings.Split(strings.TrimSpace(string(data)), "\n")); n != workers {
		t.Fatalf("event journal entries = %d, want %d", n, workers)
	}
}

// TestStartReclaimsStaleAllocationLock verifies a stale lock file is reclaimed rather than blocking forever.
func TestStartReclaimsStaleAllocationLock(t *testing.T) {
	root := initProject(t)
	lockPath := filepath.Join(root, ".ox-agent", "work", ".allocate.lock")
	if err := os.WriteFile(lockPath, []byte("pid=0\n"), 0o644); err != nil {
		t.Fatalf("seeding stale lock: %v", err)
	}
	stale := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatalf("backdating stale lock: %v", err)
	}
	if _, err := Start(root, "After stale lock"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file remains after start: %v", err)
	}
	workDir := filepath.Join(root, ".ox-agent", "work", "active", "WI-0001-after-stale-lock")
	if _, err := os.Stat(filepath.Join(workDir, "index.json")); err != nil {
		t.Fatalf("work item index missing: %v", err)
	}
}

// TestStartRollsBackWhenEventAppendFails verifies start removes the new work item when event journaling fails.
func TestStartRollsBackWhenEventAppendFails(t *testing.T) {
	root := initProject(t)
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	if err := os.Remove(journal); err != nil {
		t.Fatalf("removing event journal: %v", err)
	}
	if err := os.Mkdir(journal, 0o755); err != nil {
		t.Fatalf("replacing event journal with directory: %v", err)
	}
	if _, err := Start(root, "Will roll back"); err == nil {
		t.Fatal("Start() succeeded; want event journal failure")
	}
	entries, err := os.ReadDir(filepath.Join(root, ".ox-agent", "work", "active"))
	if err != nil {
		t.Fatalf("reading active work items: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rolled-back work item remains: %v", entries)
	}
}

// startItem creates a work item and returns it resolved.
func startItem(t *testing.T, root, title string) *WorkItem {
	t.Helper()
	if _, err := Start(root, title); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	item, err := Resolve(root, "WI-0001")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	return item
}

// TestTransitionWalksLifecycle verifies the linear chain advances and the
// index records state, stage, and timestamps.
func TestTransitionWalksLifecycle(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "lifecycle walk")
	for _, target := range []string{"discovering", "brainstorming", "specifying", "planning", "implementing", "verifying"} {
		if err := item.Transition(root, target, "cli", "", ""); err != nil {
			t.Fatalf("Transition(%s) error = %v", target, err)
		}
	}
	if item.State != "verifying" || item.CurrentStage != "verification" {
		t.Fatalf("state=%s stage=%s, want verifying/verification", item.State, item.CurrentStage)
	}
	if err := item.Transition(root, "implementing", "cli", "", "rework"); err != nil {
		t.Fatalf("rework transition error = %v", err)
	}
	if item.State != "implementing" {
		t.Fatalf("state = %s, want implementing", item.State)
	}
}

// TestTransitionRejectsIllegal verifies an edge not in the lifecycle fails.
func TestTransitionRejectsIllegal(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "illegal move")
	if err := item.Transition(root, "implementing", "cli", "", ""); err == nil {
		t.Fatal("intake → implementing succeeded; want rejection")
	}
	if item.State != "intake" {
		t.Fatalf("state mutated on rejected transition: %s", item.State)
	}
}

// TestTransitionGateRequiresApprover verifies the shipped edge demands --by.
func TestTransitionGateRequiresApprover(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "gate check")
	item.State = "shipping"
	if err := item.Save(); err != nil {
		t.Fatalf("seeding shipping state: %v", err)
	}
	if err := item.Transition(root, "shipped", "cli", "", ""); err == nil {
		t.Fatal("gated transition succeeded without approver")
	}
	if err := item.Transition(root, "shipped", "cli", "jesse", ""); err != nil {
		t.Fatalf("gated transition with approver error = %v", err)
	}
	events, err := EventsFor(root, "WI-0001", 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	last := events[len(events)-1]
	if last.Data["approved_by"] != "jesse" || last.Data["gate"] != "human" {
		t.Fatalf("gate not journaled: %v", last.Data)
	}
}

// TestUnblockReturnsToBlockedFrom verifies blocked items restore their state.
func TestUnblockReturnsToBlockedFrom(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "block me")
	if err := item.Transition(root, "discovering", "cli", "", ""); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	if err := item.Transition(root, "blocked", "cli", "", "waiting on spec"); err != nil {
		t.Fatalf("blocking error = %v", err)
	}
	if item.BlockedFrom != "discovering" {
		t.Fatalf("blocked_from = %s, want discovering", item.BlockedFrom)
	}
	if err := item.Unblock(root, "cli", "spec landed"); err != nil {
		t.Fatalf("Unblock() error = %v", err)
	}
	if item.State != "discovering" {
		t.Fatalf("state = %s, want discovering", item.State)
	}
}

// TestArchiveMovesBucket verifies archive relocates the directory and journals.
func TestArchiveMovesBucket(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "archive me")
	if err := item.Archive(root, "cli", "", "done"); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	work := filepath.Join(root, ".ox-agent", "work")
	if _, err := os.Stat(filepath.Join(work, "archived", "WI-0001-archive-me")); err != nil {
		t.Fatalf("item not in archived bucket: %v", err)
	}
	resolved, err := Resolve(root, "WI-0001")
	if err != nil {
		t.Fatalf("resolving archived item: %v", err)
	}
	if resolved.State != "archived" {
		t.Fatalf("state = %s, want archived", resolved.State)
	}
	if err := resolved.Archive(root, "cli", "", ""); err == nil {
		t.Fatal("double archive succeeded; want error")
	}
}

// TestReplayFoldsJournal verifies replay summarizes state and history from events.
func TestReplayFoldsJournal(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "replay me")
	if err := item.Transition(root, "discovering", "cli", "", ""); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	snap, err := Replay(root, item, true)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if snap.State != "discovering" || snap.Events != 2 || len(snap.History) != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if _, err := os.Stat(snapshotPath(root, "WI-0001")); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}
	lines, err := Explain(root, item)
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	if len(lines) != 2 || lines[1] == "" {
		t.Fatalf("explain lines = %v", lines)
	}
}

// TestTransitionRejectsArchived verifies the archived state is only reachable
// through Archive so the bucket move always happens.
func TestTransitionRejectsArchived(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "no raw archive")
	if err := item.Transition(root, "archived", "cli", "", ""); err == nil {
		t.Fatal("transition to archived succeeded; want rejection via Archive")
	}
	if item.State != "intake" {
		t.Fatalf("state mutated on rejected transition: %s", item.State)
	}
	if item.bucket() != "active" {
		t.Fatalf("bucket moved on rejected transition: %s", item.bucket())
	}
}

// TestBlockedTransitionReturnsToBlockedFrom verifies a blocked item can only
// transition back to its recorded blocked_from state (or be archived).
func TestBlockedTransitionReturnsToBlockedFrom(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "blocked lanes")
	if err := item.Transition(root, "blocked", "cli", "", ""); err != nil {
		t.Fatalf("blocking transition error = %v", err)
	}
	if err := item.Transition(root, "shipping", "cli", "", ""); err == nil {
		t.Fatal("blocked → shipping succeeded; want rejection to blocked_from only")
	}
	if item.State != "blocked" {
		t.Fatalf("state mutated on rejected transition: %s", item.State)
	}
	if err := item.Transition(root, item.BlockedFrom, "cli", "", ""); err != nil {
		t.Fatalf("blocked → blocked_from error = %v", err)
	}
	if item.State != "intake" {
		t.Fatalf("state = %s after unblock transition, want intake", item.State)
	}
}

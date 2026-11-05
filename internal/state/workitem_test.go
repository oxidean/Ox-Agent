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

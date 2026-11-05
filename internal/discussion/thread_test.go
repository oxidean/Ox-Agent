package discussion

import (
	"testing"

	"github.com/oxidean/Ox-Agent/internal/project"
)

// TestThreadLifecycle covers open → add → summarize → close and the
// append-only source hash advancing at each mutation.
func TestThreadLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := project.Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	thread, err := Open(root, "rollout strategy", "agent")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if thread.ThreadID != "d-001" || thread.Status != "active" {
		t.Fatalf("unexpected thread: %+v", thread)
	}
	first := thread.SourceHash
	if err := thread.Add(root, "agent", "prefer phased rollout"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if thread.SourceHash == first {
		t.Fatal("source hash did not advance after append")
	}
	if err := thread.Summarize(root, "phased rollout approved", []string{"phase 1 next week"}, nil); err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if thread.SummaryVersion != 2 || len(thread.SummarizedMessages) != 2 {
		t.Fatalf("summary state wrong: %+v", thread)
	}
	if err := thread.Close(root, "shipped"); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if thread.Status != "resolved" {
		t.Fatalf("status = %q", thread.Status)
	}
	if err := thread.Add(root, "agent", "too late"); err == nil {
		t.Fatal("Add() succeeded on a resolved thread")
	}
	reloaded, err := Load(root, "d-001")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.Status != "resolved" || reloaded.Conclusion != "shipped" {
		t.Fatalf("reloaded thread wrong: %+v", reloaded)
	}
	threads, err := List(root)
	if err != nil || len(threads) != 1 {
		t.Fatalf("List() = %v threads, %v", len(threads), err)
	}
}

// TestLoadRejectsEmptyID verifies an empty thread reference errors instead of
// panicking on the id[0] index.
func TestLoadRejectsEmptyID(t *testing.T) {
	if _, err := Load(t.TempDir(), ""); err == nil {
		t.Fatal("Load() with empty id succeeded; want error")
	}
}

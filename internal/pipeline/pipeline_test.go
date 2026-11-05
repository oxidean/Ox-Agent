package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
	item, err := state.Start(root, "pipeline target")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	item, err = state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	return root, item
}

// seedRun writes a run.json record for a pipeline step.
func seedRun(t *testing.T, root, id, wi, pipeline, stepID, status string) {
	t.Helper()
	seedRunAt(t, root, id, wi, pipeline, stepID, status, "t")
}

// seedRunAt writes a run.json record with an explicit creation time so tests
// can control which of a step's runs is the latest.
func seedRunAt(t *testing.T, root, id, wi, pipeline, stepID, status, createdAt string) {
	t.Helper()
	dir := filepath.Join(root, ".ox-agent", "runtime", "runs", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}
	data, _ := json.Marshal(map[string]any{
		"schema_version": 1, "run_id": id, "work_item_id": wi,
		"skill": "ox-agent/x", "stage": "s", "adapter": "manual",
		"status": status, "packet_path": "p", "pipeline": pipeline, "step_id": stepID,
		"created_at": createdAt,
	})
	if err := os.WriteFile(filepath.Join(dir, "run.json"), data, 0o644); err != nil {
		t.Fatalf("write run.json: %v", err)
	}
}

// TestProgressTracksSteps verifies done/pending/ready/waiting classification
// against the plan pipeline's dependencies.
func TestProgressTracksSteps(t *testing.T) {
	root, item := initProject(t)
	pipe, err := Load(root, "plan")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	progress, err := Progress(root, pipe, item)
	if err != nil {
		t.Fatalf("Progress() error = %v", err)
	}
	status := map[string]string{}
	for _, s := range progress {
		status[s.Step.ID] = s.Status
	}
	if status["discover"] != "ready" || status["brainstorm"] != "ready" {
		t.Fatalf("initial ready set wrong: %v", status)
	}
	if status["specify"] != "waiting" || status["validate"] != "waiting" {
		t.Fatalf("dependent steps should wait: %v", status)
	}
	seedRun(t, root, "run_a", item.ID, "plan", "discover", "collected")
	seedRun(t, root, "run_b", item.ID, "plan", "brainstorm", "pending")
	progress, err = Progress(root, pipe, item)
	if err != nil {
		t.Fatalf("Progress() error = %v", err)
	}
	for _, s := range progress {
		status[s.Step.ID] = s.Status
	}
	if status["discover"] != "done" || status["brainstorm"] != "pending" || status["specify"] != "waiting" {
		t.Fatalf("progress wrong after partial completion: %v", status)
	}
	seedRun(t, root, "run_c", item.ID, "plan", "brainstorm", "collected")
	progress, _ = Progress(root, pipe, item)
	for _, s := range progress {
		status[s.Step.ID] = s.Status
	}
	if status["specify"] != "ready" {
		t.Fatalf("specify should be ready once deps done: %v", status)
	}
	// A retry-status run is finished but not done: the step returns to
	// ready so another pass can be dispatched.
	seedRun(t, root, "run_d", item.ID, "plan", "specify", "retry")
	progress, _ = Progress(root, pipe, item)
	for _, s := range progress {
		status[s.Step.ID] = s.Status
	}
	if status["specify"] != "ready" {
		t.Fatalf("retry step should stay ready, got %s", status["specify"])
	}
}

// TestLoadRejectsBadDeps verifies pipeline loading rejects unknown depends_on.
func TestLoadRejectsBadDeps(t *testing.T) {
	root, _ := initProject(t)
	bad := "name: bad\nversion: 1\nsteps:\n  - id: a\n    skill: x\n    depends_on: [ghost]\n"
	if err := os.WriteFile(filepath.Join(root, ".ox-agent", "pipelines", "bad.yaml"), []byte(bad), 0o644); err != nil {
		t.Fatalf("writing bad pipeline: %v", err)
	}
	if _, err := Load(root, "bad"); err == nil {
		t.Fatal("Load() accepted unknown depends_on")
	}
}

// TestProgressUsesLatestStepRun verifies a step's newest run decides its
// status: a step that collected and was later retried drops back to ready
// rather than staying done.
func TestProgressUsesLatestStepRun(t *testing.T) {
	root, item := initProject(t)
	pipe, err := Load(root, "plan")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	seedRunAt(t, root, "run_old", item.ID, "plan", "discover", "collected", "2026-01-01T00:00:00Z")
	seedRunAt(t, root, "run_new", item.ID, "plan", "discover", "retry", "2026-01-01T00:01:00Z")
	progress, err := Progress(root, pipe, item)
	if err != nil {
		t.Fatalf("Progress() error = %v", err)
	}
	for _, s := range progress {
		if s.Step.ID == "discover" && s.Status != "ready" {
			t.Fatalf("discover step = %q; want ready while its latest run is retry", s.Status)
		}
	}
}

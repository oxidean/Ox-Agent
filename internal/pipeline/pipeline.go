// Package pipeline loads ordered skill pipelines and tracks their progress
// against a work item's collected runs.
package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/oxidean/Ox-Agent/internal/state"
	"gopkg.in/yaml.v3"
)

// Step is one node in a pipeline's execution DAG.
type Step struct {
	ID        string   `yaml:"id"`
	Skill     string   `yaml:"skill"`
	Stage     string   `yaml:"stage"`
	DependsOn []string `yaml:"depends_on"`
}

// Pipeline mirrors one .ox-agent/pipelines/<name>.yaml file.
type Pipeline struct {
	Name    string `yaml:"name"`
	Version int    `yaml:"version"`
	Steps   []Step `yaml:"steps"`
}

// dir returns the pipelines directory under root.
func dir(root string) string {
	return filepath.Join(root, ".ox-agent", "pipelines")
}

// Load parses .ox-agent/pipelines/<name>.yaml.
func Load(root, name string) (*Pipeline, error) {
	path := filepath.Join(dir(root), name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no pipeline %q", name)
		}
		return nil, err
	}
	var pipe Pipeline
	if err := yaml.Unmarshal(data, &pipe); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	ids := map[string]bool{}
	for _, step := range pipe.Steps {
		ids[step.ID] = true
	}
	for _, step := range pipe.Steps {
		for _, dep := range step.DependsOn {
			if !ids[dep] {
				return nil, fmt.Errorf("pipeline %s: step %s depends on unknown step %s", name, step.ID, dep)
			}
		}
	}
	return &pipe, nil
}

// List returns the names of all pipelines in the project.
func List(root string) ([]string, error) {
	entries, err := os.ReadDir(dir(root))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() {
			out = append(out, entry.Name()[:len(entry.Name())-len(filepath.Ext(entry.Name()))])
		}
	}
	sort.Strings(out)
	return out, nil
}

// StepStatus is one step's progress within a pipeline run.
type StepStatus struct {
	Step   Step
	Status string // done, pending, ready, waiting
	RunID  string
}

// Progress computes every step's status for the work item from run records.
func Progress(root string, pipe *Pipeline, item *state.WorkItem) ([]StepStatus, error) {
	done := map[string]string{}
	pending := map[string]string{}
	entries, err := os.ReadDir(filepath.Join(root, ".ox-agent", "runtime", "runs"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	// Only the latest run per step decides its status: a step that
	// collected once but was later retried is not done until the newest
	// run collects.
	latest := map[string]runRecord{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run, err := loadRun(filepath.Join(root, ".ox-agent", "runtime", "runs", entry.Name()))
		if err != nil {
			continue
		}
		if run.WorkItemID != item.ID || run.Pipeline != pipe.Name || run.StepID == "" {
			continue
		}
		cur, ok := latest[run.StepID]
		if !ok || run.CreatedAt > cur.CreatedAt || (run.CreatedAt == cur.CreatedAt && run.RunID > cur.RunID) {
			latest[run.StepID] = *run
		}
	}
	for stepID, run := range latest {
		if run.Status == "collected" {
			done[stepID] = run.RunID
		} else if run.Status == "pending" {
			pending[stepID] = run.RunID
		}
	}
	var out []StepStatus
	for _, step := range pipe.Steps {
		status := StepStatus{Step: step}
		switch {
		case done[step.ID] != "":
			status.Status, status.RunID = "done", done[step.ID]
		case pending[step.ID] != "":
			status.Status, status.RunID = "pending", pending[step.ID]
		default:
			ready := true
			for _, dep := range step.DependsOn {
				if done[dep] == "" {
					ready = false
					break
				}
			}
			if ready {
				status.Status = "ready"
			} else {
				status.Status = "waiting"
			}
		}
		out = append(out, status)
	}
	return out, nil
}

// loadRun reads one run.json without depending on the harness package.
func loadRun(dir string) (*runRecord, error) {
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return nil, err
	}
	var run runRecord
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", dir, err)
	}
	return &run, nil
}

// runRecord is the subset of run.json the executor reads.
type runRecord struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Pipeline   string `json:"pipeline"`
	StepID     string `json:"step_id"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

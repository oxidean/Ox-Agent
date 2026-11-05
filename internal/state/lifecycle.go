package state

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Lifecycle mirrors .ox-agent/lifecycle/workflow.yaml: the legal work-item
// states and the transitions permitted between them.
type Lifecycle struct {
	SchemaVersion int      `yaml:"schema_version"`
	States        []string `yaml:"states"`
	Transitions   []struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
		Gate string `yaml:"gate"`
	} `yaml:"transitions"`
}

// terminalStates cannot leave through any transition, including wildcards.
var terminalStates = map[string]bool{"shipped": true, "archived": true}

// stageToState maps profile stages (nouns) onto lifecycle states (gerunds).
var stageToState = map[string]string{
	"discovery":      "discovering",
	"brainstorming":  "brainstorming",
	"specification":  "specifying",
	"planning":       "planning",
	"implementation": "implementing",
	"verification":   "verifying",
	"shipping":       "shipping",
}

// stateToStage is the inverse of stageToState.
var stateToStage = func() map[string]string {
	out := make(map[string]string, len(stageToState))
	for stage, state := range stageToState {
		out[state] = stage
	}
	return out
}()

// LoadLifecycle parses .ox-agent/lifecycle/workflow.yaml under root.
func LoadLifecycle(root string) (*Lifecycle, error) {
	path := filepath.Join(root, ".ox-agent", "lifecycle", "workflow.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var life Lifecycle
	if err := yaml.Unmarshal(data, &life); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &life, nil
}

// Find reports whether the transition from→to is legal and returns the gate
// required on the edge, if any. A "*" endpoint matches any non-terminal state.
func (l *Lifecycle) Find(from, to string) (gate string, ok bool) {
	for _, tr := range l.Transitions {
		fromOK := tr.From == from || (tr.From == "*" && !terminalStates[from] && from != tr.To)
		toOK := tr.To == to || (tr.To == "*" && !terminalStates[to] && to != tr.From)
		if fromOK && toOK && from != to {
			return tr.Gate, true
		}
	}
	return "", false
}

// LegalTargets lists the states reachable from from in one transition.
func (l *Lifecycle) LegalTargets(from string) []string {
	var targets []string
	seen := map[string]bool{}
	for _, state := range l.States {
		if _, ok := l.Find(from, state); ok && !seen[state] {
			targets = append(targets, state)
			seen[state] = true
		}
	}
	return targets
}

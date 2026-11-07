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

// stageToState maps profile stages (nouns) onto lifecycle states (gerunds).
var stageToState = map[string]string{
	"discovery":      "discovering",
	"brainstorming":  "brainstorming",
	"specification":  "specifying",
	"planning":       "planning",
	"implementation": "implementing",
	"verification":   "verifying",
	"shipping":       "shipping",
	"retrospective":  "retrospecting",
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
// required on the edge, if any. A "*" source is constrained by target:
// nothing leaves archived, and shipped items cannot become blocked. The
// symbolic "blocked_from" target resolves against blockedFrom — the state the
// work item recorded when it entered blocked — so a blocked item can only
// return to where it was blocked from (terminal gates still apply there).
// Wildcard targets are not supported: a "*" in to never matches.
func (l *Lifecycle) Find(from, to, blockedFrom string) (gate string, ok bool) {
	for _, tr := range l.Transitions {
		fromOK := tr.From == from || (tr.From == "*" && wildcardFrom(from, to))
		var toOK bool
		switch tr.To {
		case "blocked_from":
			toOK = from == "blocked" && blockedFrom != "" && to == blockedFrom
		case "*":
			toOK = false
		default:
			toOK = tr.To == to
		}
		if fromOK && toOK && from != to {
			return tr.Gate, true
		}
	}
	return "", false
}

// wildcardFrom reports whether a "*" source edge may leave from.
func wildcardFrom(from, to string) bool {
	if from == "archived" {
		return false
	}
	if to == "blocked" && from == "shipped" {
		return false
	}
	return true
}

// LegalTargets lists the states reachable from from in one transition.
// blockedFrom carries the work item's recorded blocked_from state so the
// blocked return edge resolves to the origin state.
func (l *Lifecycle) LegalTargets(from, blockedFrom string) []string {
	var targets []string
	seen := map[string]bool{}
	for _, state := range l.States {
		if _, ok := l.Find(from, state, blockedFrom); ok && !seen[state] {
			targets = append(targets, state)
			seen[state] = true
		}
	}
	return targets
}

package state

import (
	"testing"
)

// testLifecycle mirrors the embedded lifecycle/workflow.yaml edges.
func testLifecycle() *Lifecycle {
	life := &Lifecycle{SchemaVersion: 1, States: []string{
		"intake", "discovering", "shipping", "shipped", "blocked", "archived",
	}}
	life.Transitions = []struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
		Gate string `yaml:"gate"`
	}{
		{From: "intake", To: "discovering"},
		{From: "shipping", To: "shipped", Gate: "human"},
		{From: "*", To: "blocked"},
		{From: "blocked", To: "blocked_from"},
		{From: "*", To: "archived"},
	}
	return life
}

// TestFindResolvesBlockedFrom verifies the symbolic blocked_from target only
// reaches the recorded origin state.
func TestFindResolvesBlockedFrom(t *testing.T) {
	life := testLifecycle()
	if _, ok := life.Find("blocked", "shipping", "shipping"); !ok {
		t.Fatal("blocked → blocked_from origin rejected")
	}
	if _, ok := life.Find("blocked", "discovering", "shipping"); ok {
		t.Fatal("blocked → non-origin state accepted")
	}
	if _, ok := life.Find("blocked", "shipping", ""); ok {
		t.Fatal("blocked → state accepted with no recorded blocked_from")
	}
}

// TestFindRejectsTerminalViaBlocked verifies the blocked return edge cannot
// reach terminal states even when a wildcard edge would have allowed it.
func TestFindRejectsTerminalViaBlocked(t *testing.T) {
	life := testLifecycle()
	if _, ok := life.Find("blocked", "shipped", "shipping"); ok {
		t.Fatal("blocked → shipped accepted; human gate bypassed")
	}
	if _, ok := life.Find("blocked", "blocked", "shipping"); ok {
		t.Fatal("blocked → blocked accepted")
	}
}

// TestFindRejectsWildcardTargets verifies a "*" target edge never matches —
// wildcard targets are not supported.
func TestFindRejectsWildcardTargets(t *testing.T) {
	life := testLifecycle()
	life.Transitions = append(life.Transitions, struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
		Gate string `yaml:"gate"`
	}{From: "discovering", To: "*"})
	if _, ok := life.Find("discovering", "shipping", ""); ok {
		t.Fatal("wildcard target edge matched")
	}
}

// TestLegalTargetsFromBlocked verifies only the recorded origin (and archive)
// are reachable from blocked.
func TestLegalTargetsFromBlocked(t *testing.T) {
	life := testLifecycle()
	targets := life.LegalTargets("blocked", "shipping")
	got := map[string]bool{}
	for _, target := range targets {
		got[target] = true
	}
	if !got["shipping"] || !got["archived"] {
		t.Fatalf("blocked targets missing origin/archive: %v", targets)
	}
	if got["shipped"] || got["discovering"] || got["intake"] {
		t.Fatalf("blocked reached unreachable states: %v", targets)
	}
}

// TestBlockedCannotReachShipped verifies the human-gate bypass end to end:
// shipping → blocked → shipped must be rejected because blocked only returns
// to its recorded origin.
func TestBlockedCannotReachShipped(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "gate bypass")
	item.State = "shipping"
	if err := item.Save(); err != nil {
		t.Fatalf("seeding shipping state: %v", err)
	}
	if err := item.Transition(root, "blocked", "cli", "", "hold"); err != nil {
		t.Fatalf("blocking error = %v", err)
	}
	if err := item.Transition(root, "shipped", "cli", "jesse", ""); err == nil {
		t.Fatal("blocked → shipped succeeded; human gate bypassed")
	}
	if item.State != "blocked" {
		t.Fatalf("state mutated on rejected transition: %s", item.State)
	}
	if err := item.Transition(root, "shipping", "cli", "", "resume"); err != nil {
		t.Fatalf("return to blocked_from error = %v", err)
	}
	if err := item.Transition(root, "shipped", "cli", "jesse", ""); err != nil {
		t.Fatalf("gated shipped transition error = %v", err)
	}
}

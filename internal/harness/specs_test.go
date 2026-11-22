package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/appctx"
	"github.com/oxidean/Ox-Agent/internal/input"
	"github.com/oxidean/Ox-Agent/internal/state"
)

// writeCoveredSpec drops a spec with `covers:` front matter into
// context/specs and returns its index path.
func writeCoveredSpec(t *testing.T, root, name string, covers ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("---\ncovers:\n")
	for _, c := range covers {
		b.WriteString("  - " + c + "\n")
	}
	b.WriteString("---\n# spec\n")
	path := filepath.Join(root, ".ox-agent", "context", "specs", name)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write covered spec: %v", err)
	}
}

// setEnforcement rewrites the project's policy.yaml with the given
// specs.enforcement value.
func setEnforcement(t *testing.T, root, enforcement string) {
	t.Helper()
	policy := "schema_version: 1\nautonomy: {profile_changes: managed, allow_downgrade: false}\napprovals: {production_deploy: human, destructive_change: human, policy_change: human}\nbudgets: {max_parallel_agents: 4, max_child_depth: 1, max_fix_cycles: 3}\nspecs: {enforcement: " + enforcement + "}\n"
	if err := os.WriteFile(filepath.Join(root, ".ox-agent", "policy.yaml"), []byte(policy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
}

// specEvents returns the journal's spec.* events for the work item.
func specEvents(t *testing.T, root, itemID string) []state.Event {
	t.Helper()
	events, err := state.EventsFor(root, itemID, 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	var out []state.Event
	for _, e := range events {
		if strings.HasPrefix(e.Type, "spec.") {
			out = append(out, e)
		}
	}
	return out
}

// dispatchSpecRun dispatches a specify run and writes the given output.
func dispatchSpecRun(t *testing.T, root string, item *state.WorkItem, output map[string]any) *Run {
	t.Helper()
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	writeOutput(t, root, run.RunID, output)
	return run
}

func TestMatchSpecGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"internal/**", "internal/cli/x.go", true},
		{"internal/**", "internal/x.go", true},
		{"internal/**", "other/x.go", false},
		{"*.go", "a/b.go", false},
		{"**/*.go", "a/b.go", true},
		{"**/*.go", "b.go", true},
		{"src/**/x.go", "src/x.go", true},
		{"src/**/x.go", "src/a/b/x.go", true},
		{"docs/*.md", "docs/a.md", true},
		{"docs/*.md", "docs/a/b.md", false},
		{"internal/harness/*.go", "internal/harness/run.go", true},
		{"internal/harness/*.go", "internal/harness/run_test.go", true},
		{"internal/**", `internal\cli\x.go`, true},
	} {
		if got := matchSpecGlob(tc.pattern, tc.name); got != tc.want {
			t.Fatalf("matchSpecGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// TestCollectWarnJournalsSpecDrift verifies the default warn policy:
// findings journal spec.drift and surface as warnings, but the passing
// outcome still applies.
func TestCollectWarnJournalsSpecDrift(t *testing.T) {
	root, item := initProject(t)
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "collected" {
		t.Fatalf("run status = %s, want collected under warn", run.Status)
	}
	if len(run.SpecWarnings) == 0 {
		t.Fatal("no spec warnings surfaced")
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 1 || events[0].Type != "spec.drift" {
		t.Fatalf("spec events = %v", events)
	}
	if events[0].Data["enforcement"] != "warn" {
		t.Fatalf("enforcement = %v", events[0].Data["enforcement"])
	}
}

// TestCollectConsultedSpecPasses verifies a declared consultation
// suppresses the coverage finding and journals spec.checked.
func TestCollectConsultedSpecPasses(t *testing.T) {
	root, item := initProject(t)
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	output["spec_impact"] = map[string]any{
		"consulted": []any{"context/specs/api.md"},
		"conforms":  true,
	}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "collected" {
		t.Fatalf("run status = %s", run.Status)
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 1 || events[0].Type != "spec.checked" {
		t.Fatalf("spec events = %v", events)
	}
}

// TestCollectDeclaredDriftIsFinding verifies spec_impact drift with an
// unresolved resolution produces a finding; an "updated" resolution does
// not.
func TestCollectDeclaredDriftIsFinding(t *testing.T) {
	root, item := initProject(t)
	output := validOutput("specify")
	output["spec_impact"] = map[string]any{
		"consulted": []any{"context/specs/api.md"},
		"drift": []any{
			map[string]any{"spec": "context/specs/api.md", "summary": "widened retry cap", "resolution": "proposed"},
			map[string]any{"spec": "context/specs/other.md", "summary": "already updated", "resolution": "updated"},
		},
	}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "collected" {
		t.Fatalf("run status = %s (warn)", run.Status)
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 1 || events[0].Type != "spec.drift" {
		t.Fatalf("spec events = %v", events)
	}
	findings, _ := events[0].Data["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("findings = %v — only the unresolved drift should be a finding", findings)
	}
}

// TestCollectStrictBlocksAndFilesDecision verifies strict enforcement
// parks a passing outcome: the run goes retry, a durable decision request
// is filed, and the outcome does not apply.
func TestCollectStrictBlocksAndFilesDecision(t *testing.T) {
	root, item := initProject(t)
	setEnforcement(t, root, "strict")
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "retry" {
		t.Fatalf("run status = %s, want retry under strict", run.Status)
	}
	if run.AwaitingInput == "" {
		t.Fatal("strict block filed no request")
	}
	pending, _ := input.Pending(root)
	if len(pending) != 1 || pending[0].Kind != input.KindDecision {
		t.Fatalf("pending requests = %v", pending)
	}
	if len(pending[0].Options) < 2 {
		t.Fatalf("decision options = %v", pending[0].Options)
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 1 || events[0].Type != "spec.drift" || events[0].Data["enforcement"] != "strict" {
		t.Fatalf("spec events = %v", events)
	}
	// The held outcome must not advance the item.
	reloaded, err := state.Resolve(root, item.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if reloaded.CurrentStage != item.CurrentStage {
		t.Fatalf("stage advanced past strict block: %s", reloaded.CurrentStage)
	}
}

// TestCollectStrictKeepsAwaitInput verifies strict findings on an
// await_input output do not file a second request — the skill's own
// question is already parked.
func TestCollectStrictKeepsAwaitInput(t *testing.T) {
	root, item := initProject(t)
	setEnforcement(t, root, "strict")
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	output["next_action"] = map[string]any{"type": "await_input", "reason": "which quota?"}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "retry" {
		t.Fatalf("run status = %s", run.Status)
	}
	pending, _ := input.Pending(root)
	if len(pending) != 1 {
		t.Fatalf("pending = %v — strict must not stack a second request on await_input", pending)
	}
	if pending[0].Kind != input.KindClarification {
		t.Fatalf("request kind = %s, want the skill's clarification", pending[0].Kind)
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 1 || events[0].Type != "spec.drift" {
		t.Fatalf("spec events = %v", events)
	}
}

// TestCollectStrictReworkSkipsDecision verifies a needs_rework verdict
// under strict still journals drift but files no decision — the rework
// itself is the resolution path.
func TestCollectStrictReworkSkipsDecision(t *testing.T) {
	root, item := initProject(t)
	setEnforcement(t, root, "strict")
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["verdict"] = "needs_rework"
	output["artifacts"] = []any{"internal/cli/context.go"}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "retry" {
		t.Fatalf("run status = %s", run.Status)
	}
	pending, _ := input.Pending(root)
	if len(pending) != 0 {
		t.Fatalf("pending = %v — rework needs no spec decision", pending)
	}
}

// TestCollectOffSkipsEvaluation verifies enforcement off disables the
// whole feature — no findings, no warnings, no spec events.
func TestCollectOffSkipsEvaluation(t *testing.T) {
	root, item := initProject(t)
	setEnforcement(t, root, "off")
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "collected" || len(run.SpecWarnings) != 0 {
		t.Fatalf("run = status %s warnings %v", run.Status, run.SpecWarnings)
	}
	if events := specEvents(t, root, item.ID); len(events) != 0 {
		t.Fatalf("spec events under off = %v", events)
	}
}

// TestCoverageGlobIssueWarns verifies a malformed covers glob surfaces as
// a warning instead of silently disabling that spec's coverage.
func TestCoverageGlobIssueWarns(t *testing.T) {
	root, item := initProject(t)
	writeCoveredSpec(t, root, "api.md", "internal/[unclosed")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	found := false
	for _, w := range run.SpecWarnings {
		if strings.Contains(w, "invalid covers glob") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no invalid-glob warning in %v", run.SpecWarnings)
	}
}

// TestEntriesExposeCovers pins the index-to-eval contract: Entries must
// return covers alongside kind so coverage matching sees them.
func TestEntriesExposeCovers(t *testing.T) {
	root, _ := initProject(t)
	writeCoveredSpec(t, root, "api.md", "internal/**")
	index, err := appctx.Build(root)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, _, _, err := appctx.Write(root, index); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	entries, err := appctx.Entries(root, "spec")
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	for _, e := range entries {
		if e.Path == "context/specs/api.md" && len(e.Covers) == 1 && e.Covers[0] == "internal/**" {
			return
		}
	}
	t.Fatalf("covered spec missing from entries: %v", entries)
}

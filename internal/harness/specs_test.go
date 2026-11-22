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

// TestCollectConformsOmittedPasses verifies spec_impact declaring only
// consulted reads as a clean check — an omitted conforms is "no claim",
// not a contradiction.
func TestCollectConformsOmittedPasses(t *testing.T) {
	root, item := initProject(t)
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	output["spec_impact"] = map[string]any{
		"consulted": []any{"context/specs/api.md"},
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

func TestCollectResolvedDriftRecordsConsultedSpec(t *testing.T) {
	root, item := initProject(t)
	output := validOutput("specify")
	output["spec_impact"] = map[string]any{
		"drift": []any{map[string]any{
			"spec":       "context/specs/api.md",
			"summary":    "updated the spec to match",
			"resolution": "updated",
		}},
	}
	run := dispatchSpecRun(t, root, item, output)
	if _, _, err := Collect(root, run.RunID); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 1 || events[0].Type != "spec.checked" {
		t.Fatalf("spec events = %v", events)
	}
	consulted := anyStrings(events[0].Data["consulted"])
	if len(consulted) != 1 || consulted[0] != "context/specs/api.md" {
		t.Fatalf("checked consulted = %v", consulted)
	}
}

// TestCollectArtifactDotSlashMatches verifies a leading ./ on a declared
// artifact still matches its spec's covers glob.
func TestCollectArtifactDotSlashMatches(t *testing.T) {
	for _, artifact := range []string{"internal/cli/context.go", "./internal/cli/context.go"} {
		t.Run(artifact, func(t *testing.T) {
			root, item := initProject(t)
			writeCoveredSpec(t, root, "api.md", "internal/**")
			output := validOutput("specify")
			output["artifacts"] = []any{artifact}
			run := dispatchSpecRun(t, root, item, output)
			run, _, err := Collect(root, run.RunID)
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if len(run.SpecWarnings) == 0 {
				t.Fatalf("%q artifact produced no coverage finding", artifact)
			}
			events := specEvents(t, root, item.ID)
			if len(events) != 1 || events[0].Type != "spec.drift" {
				t.Fatalf("%q artifact missed coverage: spec events = %v", artifact, events)
			}
		})
	}
}

func TestCollectStrictRejectsInvalidCoverage(t *testing.T) {
	for name, content := range map[string]string{
		"scalar": "---\ncovers: internal/**\n---\n# spec\n",
		"glob":   "---\ncovers:\n  - internal/[unclosed\n---\n# spec\n",
		"empty":  "---\ncovers: []\n---\n# spec\n",
	} {
		t.Run(name, func(t *testing.T) {
			root, item := initProject(t)
			setEnforcement(t, root, "strict")
			specPath := filepath.Join(root, ".ox-agent", "context", "specs", "api.md")
			if err := os.WriteFile(specPath, []byte(content), 0o644); err != nil {
				t.Fatalf("write spec: %v", err)
			}
			output := validOutput("specify")
			output["artifacts"] = []any{"internal/cli/context.go"}
			run := dispatchSpecRun(t, root, item, output)
			collected, _, err := Collect(root, run.RunID)
			if err == nil || !strings.Contains(err.Error(), "strict enforcement") {
				t.Fatalf("Collect() error = %v, want strict coverage error", err)
			}
			if collected.Status != "pending" {
				t.Fatalf("run status = %s, want pending", collected.Status)
			}
		})
	}
}

func TestCollectStrictRejectsMissingSpecDirectory(t *testing.T) {
	root, item := initProject(t)
	setEnforcement(t, root, "strict")
	if err := os.RemoveAll(filepath.Join(root, ".ox-agent", "context", "specs")); err != nil {
		t.Fatalf("remove specs directory: %v", err)
	}
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	run := dispatchSpecRun(t, root, item, output)
	collected, _, err := Collect(root, run.RunID)
	if err == nil || !strings.Contains(err.Error(), "spec coverage unreadable") {
		t.Fatalf("Collect() error = %v, want strict missing-coverage error", err)
	}
	if collected.Status != "pending" {
		t.Fatalf("run status = %s, want pending", collected.Status)
	}
}

func TestCollectStrictRejectsInvalidGlobWithoutArtifacts(t *testing.T) {
	root, item := initProject(t)
	setEnforcement(t, root, "strict")
	specPath := filepath.Join(root, ".ox-agent", "context", "specs", "api.md")
	if err := os.WriteFile(specPath, []byte("---\ncovers:\n  - internal/[unclosed\n---\n# spec\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	output := validOutput("specify")
	output["artifacts"] = []any{}
	run := dispatchSpecRun(t, root, item, output)
	collected, _, err := Collect(root, run.RunID)
	if err == nil || !strings.Contains(err.Error(), "invalid spec coverage") {
		t.Fatalf("Collect() error = %v, want invalid coverage error", err)
	}
	if collected.Status != "pending" {
		t.Fatalf("run status = %s, want pending", collected.Status)
	}
}

// TestCollectStrictReleasesAfterAcceptance verifies the strict release
// path: a human answering the decision with "accept" releases later runs
// whose drift matches the same finding set — drift still journals, but
// no duplicate decision parks the run.
func TestCollectStrictReleasesAfterAcceptance(t *testing.T) {
	root, item := initProject(t)
	setEnforcement(t, root, "strict")
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}

	run1 := dispatchSpecRun(t, root, item, output)
	run1, _, err := Collect(root, run1.RunID)
	if err != nil {
		t.Fatalf("Collect() run1 error = %v", err)
	}
	if run1.AwaitingInput == "" {
		t.Fatal("run1 filed no decision")
	}
	if _, err := input.Respond(root, run1.AwaitingInput, "human", specDecisionAccept); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if _, _, err := Collect(root, run1.RunID); err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("Collect() held run error = %v, want not-pending error", err)
	}

	run2 := dispatchSpecRun(t, root, item, output)
	run2, _, err = Collect(root, run2.RunID)
	if err != nil {
		t.Fatalf("Collect() run2 error = %v", err)
	}
	if run2.Status != "collected" {
		t.Fatalf("run2 status = %s, want collected after acceptance", run2.Status)
	}
	if run2.AwaitingInput != "" {
		t.Fatalf("run2 parked a duplicate request: %s", run2.AwaitingInput)
	}
	pending, _ := input.Pending(root)
	if len(pending) != 0 {
		t.Fatalf("pending after release = %v", pending)
	}
	found := false
	for _, w := range run2.SpecWarnings {
		if strings.Contains(w, "released") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no release warning in %v", run2.SpecWarnings)
	}
}

// TestCollectStrictDedupesPendingDecision verifies a second run drifting
// identically while the decision is still pending reuses that request
// rather than stacking another.
func TestCollectStrictDedupesPendingDecision(t *testing.T) {
	root, item := initProject(t)
	setEnforcement(t, root, "strict")
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}

	run1 := dispatchSpecRun(t, root, item, output)
	run1, _, err := Collect(root, run1.RunID)
	if err != nil {
		t.Fatalf("Collect() run1 error = %v", err)
	}
	run2 := dispatchSpecRun(t, root, item, output)
	run2, _, err = Collect(root, run2.RunID)
	if err != nil {
		t.Fatalf("Collect() run2 error = %v", err)
	}
	if run2.AwaitingInput != run1.AwaitingInput {
		t.Fatalf("run2 request = %s, want deduped %s", run2.AwaitingInput, run1.AwaitingInput)
	}
	pending, _ := input.Pending(root)
	if len(pending) != 1 {
		t.Fatalf("pending = %v — identical drift must share one request", pending)
	}
}

func TestSpecDecisionQuestionDistinguishesPathBoundaries(t *testing.T) {
	item := &state.WorkItem{ID: "WI-0001"}
	onePath := []specFinding{{
		Kind:  "unacknowledged",
		Spec:  "context/specs/api.md",
		Paths: []string{"src/a src/b"},
	}}
	twoPaths := []specFinding{{
		Kind:  "unacknowledged",
		Spec:  "context/specs/api.md",
		Paths: []string{"src/a", "src/b"},
	}}
	if specDecisionQuestion(item, onePath) == specDecisionQuestion(item, twoPaths) {
		t.Fatal("different finding paths produced the same decision identity")
	}
	ordered := []specFinding{{
		Kind:  "unacknowledged",
		Spec:  "context/specs/api.md",
		Paths: []string{"src/a", "src/b"},
	}}
	reversed := []specFinding{{
		Kind:  "unacknowledged",
		Spec:  "context/specs/api.md",
		Paths: []string{"src/b", "src/a"},
	}}
	if specDecisionQuestion(item, ordered) != specDecisionQuestion(item, reversed) {
		t.Fatal("finding order changed the decision identity")
	}
}

func TestNormalizeSpecRefCleansPathSegments(t *testing.T) {
	for input, want := range map[string]string{
		"context/specs/./api.md":                  "context/specs/api.md",
		".ox-agent/context/specs/../specs/api.md": "context/specs/api.md",
		`context\specs\api.md`:                    "context/specs/api.md",
		"../../context/specs/api.md":              "",
	} {
		if got := normalizeSpecRef(input); got != want {
			t.Errorf("normalizeSpecRef(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestJournalSpecEventsDedupesAgainstLatestFindingSet(t *testing.T) {
	root, item := initProject(t)
	run := &Run{RunID: "RUN-0001", Skill: "specify"}
	evaluation := func(summary string) *specEvaluation {
		return &specEvaluation{
			declared:    true,
			enforcement: "warn",
			findings: []specFinding{{
				Kind:    "declared",
				Spec:    "context/specs/api.md",
				Summary: summary,
			}},
		}
	}
	for _, summary := range []string{"first", "second", "first"} {
		journalSpecEvents(root, item, run, evaluation(summary))
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 3 {
		t.Fatalf("spec events = %d, want 3", len(events))
	}
}

// TestCoverageReadsCoversFresh verifies coverage parses covers: from the
// spec file itself — a glob added after the last index build still
// catches the touch.
func TestCoverageReadsCoversFresh(t *testing.T) {
	root, item := initProject(t)
	writeCoveredSpec(t, root, "api.md", "docs/**")
	index, err := appctx.Build(root)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, _, _, err := appctx.Write(root, index); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	// Edit covers after the index was built — the stored index still
	// says docs/** but the file now governs internal/**.
	writeCoveredSpec(t, root, "api.md", "internal/**")
	output := validOutput("specify")
	output["artifacts"] = []any{"internal/cli/context.go"}
	run := dispatchSpecRun(t, root, item, output)
	run, _, err = Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 1 || events[0].Type != "spec.drift" {
		t.Fatalf("stale-index covers missed the touch: spec events = %v", events)
	}
}

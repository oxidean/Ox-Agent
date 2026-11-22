package harness

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/appctx"
	"github.com/oxidean/Ox-Agent/internal/input"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
)

// specFinding is one spec-reconciliation problem found while collecting a
// run: either drift the skill declared but left unresolved, or a spec the
// run's declared artifacts touched without the skill ever consulting it.
type specFinding struct {
	Kind    string   `json:"kind"` // "declared" | "unacknowledged"
	Spec    string   `json:"spec"`
	Summary string   `json:"summary,omitempty"`
	Paths   []string `json:"paths,omitempty"`
}

// specEvaluation is the collect-time read of a run's spec relationship:
// what the skill declared via spec_impact plus what coverage analysis
// found on its own.
type specEvaluation struct {
	declared    bool
	consulted   []string
	findings    []specFinding
	enforcement string
	globIssues  []string
}

// evaluateSpecImpact reads the run's spec_impact declaration and checks
// the declared artifacts against indexed spec coverage. An "off" policy
// skips evaluation entirely — no findings, no events. Errors here mean the
// policy or index is unreadable, which fails the collect so the problem
// surfaces instead of silently bypassing the gate.
func evaluateSpecImpact(root string, output map[string]any) (*specEvaluation, error) {
	policy, err := project.LoadPolicy(root)
	if err != nil {
		return nil, fmt.Errorf("cannot evaluate spec impact — %w", err)
	}
	eval := &specEvaluation{enforcement: policy.SpecEnforcement()}
	if eval.enforcement == "off" {
		return nil, nil
	}
	consulted := map[string]bool{}
	if raw, ok := output["spec_impact"].(map[string]any); ok {
		eval.declared = true
		for _, c := range anyStrings(raw["consulted"]) {
			if c = normalizeSpecRef(c); c != "" {
				consulted[c] = true
				eval.consulted = append(eval.consulted, c)
			}
		}
		sort.Strings(eval.consulted)
		conforms, _ := raw["conforms"].(bool)
		driftDeclared := false
		for _, entry := range anyMaps(raw["drift"]) {
			spec, _ := entry["spec"].(string)
			spec = normalizeSpecRef(spec)
			summary, _ := entry["summary"].(string)
			resolution, _ := entry["resolution"].(string)
			driftDeclared = true
			if spec != "" {
				// A declared drift counts as consulting that spec — the
				// skill clearly read it to know it drifts.
				consulted[spec] = true
			}
			if resolution != "updated" {
				// "proposed" and "unresolved" leave the spec of record
				// disagreeing with the change — a finding. Unknown
				// resolutions land here too: the contract enumerates the
				// values, so anything else is treated conservatively as
				// unresolved rather than trusted.
				eval.findings = append(eval.findings, specFinding{
					Kind: "declared", Spec: spec, Summary: summary,
				})
			}
		}
		if !conforms && !driftDeclared {
			eval.findings = append(eval.findings, specFinding{
				Kind: "declared", Summary: "spec_impact declares conforms=false with no drift detail",
			})
		}
	}
	eval.findings = append(eval.findings, eval.coverageFindings(root, output, consulted)...)
	return eval, nil
}

// coverageFindings intersects the run's declared artifact paths with the
// `covers:` globs of indexed specs. A spec whose globs match changed paths
// but that appears in neither consulted nor drift is an unacknowledged
// spec touch — the mechanical part of "keep specs in mind".
func (e *specEvaluation) coverageFindings(root string, output map[string]any, consulted map[string]bool) []specFinding {
	specs, err := appctx.Entries(root, "spec")
	if err != nil {
		// A missing index builds on demand; a read failure degrades to
		// no coverage rather than failing the collect — warn-level by
		// design, since enforcement must never hinge on index health
		// alone.
		e.globIssues = append(e.globIssues, fmt.Sprintf("spec coverage skipped — index unreadable: %v", err))
		return nil
	}
	var covered []appctx.IndexEntry
	for _, entry := range specs {
		if len(entry.Covers) > 0 {
			covered = append(covered, entry)
		}
	}
	if len(covered) == 0 {
		return nil
	}
	artifactPaths := artifactPaths(output["artifacts"])
	if len(artifactPaths) == 0 {
		return nil
	}
	var findings []specFinding
	for _, spec := range covered {
		var matched []string
		for _, glob := range spec.Covers {
			if _, err := path.Match(glob, ""); err != nil {
				e.globIssues = append(e.globIssues, fmt.Sprintf("%s declares invalid covers glob %q: %v", spec.Path, glob, err))
				continue
			}
			for _, artifact := range artifactPaths {
				if matchSpecGlob(glob, artifact) {
					matched = append(matched, artifact)
				}
			}
		}
		if len(matched) > 0 && !consulted[spec.Path] {
			sort.Strings(matched)
			findings = append(findings, specFinding{
				Kind:    "unacknowledged",
				Spec:    spec.Path,
				Summary: "artifacts touch covered paths but the spec was never consulted",
				Paths:   dedupeStrings(matched),
			})
		}
	}
	return findings
}

// journalSpecEvents records the evaluation in the item's journal, once
// per run: spec.checked when the skill declared impact and nothing
// drifted, spec.drift when findings exist. Append failures degrade to
// warnings — the events are the audit trail, but a telemetry write must
// not block the collect machinery itself.
func journalSpecEvents(root string, item *state.WorkItem, run *Run, eval *specEvaluation) {
	if eval == nil {
		return
	}
	eventType := ""
	data := map[string]any{}
	switch {
	case len(eval.findings) > 0:
		eventType = "spec.drift"
		findings := make([]any, len(eval.findings))
		for i, f := range eval.findings {
			entry := map[string]any{"kind": f.Kind, "spec": f.Spec}
			if f.Summary != "" {
				entry["summary"] = f.Summary
			}
			if len(f.Paths) > 0 {
				paths := make([]any, len(f.Paths))
				for j, p := range f.Paths {
					paths[j] = p
				}
				entry["paths"] = paths
			}
			findings[i] = entry
		}
		data["findings"] = findings
		data["enforcement"] = eval.enforcement
	case eval.declared:
		eventType = "spec.checked"
		data["consulted"] = anySlice(eval.consulted)
	default:
		return
	}
	data["run_id"] = run.RunID
	data["skill"] = run.Skill
	if journaled, _ := journalFindEvent(root, item.ID, eventType, run.RunID); journaled != nil {
		return
	}
	if err := state.Append(root, state.Event{
		Type:       eventType,
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data:       data,
	}); err != nil {
		run.SpecWarnings = append(run.SpecWarnings, fmt.Sprintf("journaling %s failed: %v", eventType, err))
	}
}

// specDecisionOptions are the resolutions a strict-mode spec-drift
// decision offers.
var specDecisionOptions = []string{
	"Update the spec(s) to match the change",
	"Accept the drift and proceed",
	"Rework the change to conform",
}

// fileSpecDecision files the durable decision request a strict-mode spec
// finding parks behind. The run stays "retry": dispatchable again once a
// human answers, never applying the held outcome — this is what "block
// changes" means in ox-agent idiom, the same durable-request pattern as
// await_input and gate approvals.
func fileSpecDecision(root string, item *state.WorkItem, run *Run, findings []specFinding) (*input.Request, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "spec drift in run %s (%s):", run.RunID, run.Skill)
	for _, f := range findings {
		switch {
		case f.Kind == "unacknowledged":
			fmt.Fprintf(&b, " %s touched covered paths %v without consultation;", f.Spec, f.Paths)
		case f.Spec != "":
			fmt.Fprintf(&b, " %s: %s;", f.Spec, f.Summary)
		default:
			fmt.Fprintf(&b, " %s;", f.Summary)
		}
	}
	b.WriteString(" resolve the spec of record before this outcome applies.")
	req, err := fileInputRequest(root, item.ID, run.RunID, b.String(), specDecisionOptions)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// normalizeSpecRef canonicalizes a spec_impact spec reference for
// comparison with index paths: slashes, no leading ./ or .ox-agent/.
func normalizeSpecRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "./")
	ref = strings.TrimPrefix(ref, ".ox-agent/")
	return strings.Trim(ref, "/")
}

// artifactPaths extracts the path strings from a declared artifacts list
// (bare strings or {path: ...} objects), for coverage matching.
func artifactPaths(raw any) []string {
	var out []string
	for _, entry := range toAnySlice(raw) {
		switch v := entry.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if p, ok := v["path"].(string); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// matchSpecGlob reports whether name matches a `covers:` glob. `*` and `?`
// match within a path segment (path.Match semantics); `**` as a whole
// segment matches zero or more segments. Backslashes are normalized so a
// Windows-produced artifact path still matches its forward-slash globs.
func matchSpecGlob(pattern, name string) bool {
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	name = strings.ReplaceAll(name, "\\", "/")
	return matchGlobSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// matchGlobSegments matches pattern segments against name segments,
// expanding `**` to zero or more whole segments.
func matchGlobSegments(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchGlobSegments(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pattern[0], name[0])
		if err != nil || !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}

// anyStrings reads a JSON-decoded []any of strings (or a []string) into
// a []string, dropping non-string entries.
func anyStrings(raw any) []string {
	var out []string
	for _, v := range toAnySlice(raw) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// anyMaps reads a JSON-decoded []any of objects into map entries.
func anyMaps(raw any) []map[string]any {
	var out []map[string]any
	for _, v := range toAnySlice(raw) {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// toAnySlice normalizes []any and typed slices to []any.
func toAnySlice(raw any) []any {
	switch v := raw.(type) {
	case []any:
		return v
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out
	default:
		return nil
	}
}

// anySlice converts []string to []any for event data maps.
func anySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// dedupeStrings removes duplicates from a sorted-or-unsorted string list.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

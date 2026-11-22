package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
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
	// released reports that a human already answered a decision for this
	// exact finding set with "accept" — the findings still journal as
	// spec.drift, but strict enforcement steps down to warn rather than
	// parking another identical decision behind the same question.
	released bool
	// coverageErr fails the collect when strict can't even read its
	// gate — an unreadable spec tree under strict is a blocked input,
	// not a pass.
	coverageErr error
}

// evaluateSpecImpact reads the run's spec_impact declaration and checks
// the declared artifacts against spec coverage. An "off" policy skips
// evaluation entirely — no findings, no events. Errors here mean the
// policy or coverage inputs are unreadable, which fails the collect so
// the problem surfaces instead of silently bypassing the gate.
func evaluateSpecImpact(root string, item *state.WorkItem, output map[string]any) (*specEvaluation, error) {
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
				if !consulted[c] {
					eval.consulted = append(eval.consulted, c)
					consulted[c] = true
				}
			}
		}
		// conforms is optional: only an explicit false declares
		// nonconformance — an omitted key reads as "no claim", not
		// "contradicts".
		conforms, hasConforms := raw["conforms"].(bool)
		driftFinding := false
		for _, entry := range anyMaps(raw["drift"]) {
			spec, _ := entry["spec"].(string)
			spec = normalizeSpecRef(spec)
			summary, _ := entry["summary"].(string)
			resolution, _ := entry["resolution"].(string)
			if spec != "" {
				// A declared drift counts as consulting that spec — the
				// skill clearly read it to know it drifts.
				if !consulted[spec] {
					eval.consulted = append(eval.consulted, spec)
					consulted[spec] = true
				}
			}
			if resolution != "updated" {
				driftFinding = true
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
		sort.Strings(eval.consulted)
		if hasConforms && !conforms && !driftFinding {
			eval.findings = append(eval.findings, specFinding{
				Kind: "declared", Summary: "spec_impact declares conforms=false without an unresolved drift entry",
			})
		}
	}
	eval.findings = append(eval.findings, eval.coverageFindings(root, output, consulted)...)
	if eval.coverageErr != nil {
		return nil, eval.coverageErr
	}
	// A previously accepted finding set releases under strict: the
	// decision a human already made stands for identical drift rather
	// than stacking a duplicate request.
	if eval.enforcement == "strict" && len(eval.findings) > 0 &&
		releasedByDecision(root, item.ID, specDecisionQuestion(item, eval.findings)) {
		eval.released = true
	}
	return eval, nil
}

// coverageFindings intersects the run's declared artifact paths with the
// `covers:` globs of indexed specs. A spec whose globs match changed paths
// but that appears in neither consulted nor drift is an unacknowledged
// spec touch — the mechanical part of "keep specs in mind".
func (e *specEvaluation) coverageFindings(root string, output map[string]any, consulted map[string]bool) []specFinding {
	// Coverage reads covers: straight from the spec files, not the
	// stored index — an index entry only refreshes on `context index`,
	// so index-driven enforcement could miss a glob added (or retain one
	// removed) since the last build. The spec files are the truth.
	specs, specIssues, err := appctx.CoveredSpecs(root)
	if err != nil {
		if e.enforcement == "strict" {
			// Strict fails closed: "can't read the gate's inputs" is not
			// "no covered specs" — the collect fails and stays retryable
			// rather than applying an unchecked outcome.
			e.coverageErr = fmt.Errorf("spec coverage unreadable under strict enforcement: %w", err)
			return nil
		}
		// Warn mode degrades to no coverage: enforcement must never
		// hinge on filesystem health alone.
		e.globIssues = append(e.globIssues, fmt.Sprintf("spec coverage skipped — specs unreadable: %v", err))
		return nil
	}
	e.globIssues = append(e.globIssues, specIssues...)
	if e.enforcement == "strict" && len(specIssues) > 0 {
		e.coverageErr = fmt.Errorf("invalid spec coverage under strict enforcement: %s", strings.Join(specIssues, "; "))
		return nil
	}
	if len(specs) == 0 {
		return nil
	}
	validGlobs := make(map[string][]string, len(specs))
	for _, spec := range specs {
		for _, glob := range spec.Covers {
			normalized := normalizeSpecPath(glob)
			if _, err := path.Match(normalized, ""); err != nil {
				issue := fmt.Sprintf("%s declares invalid covers glob %q: %v", spec.Path, glob, err)
				e.globIssues = append(e.globIssues, issue)
				if e.enforcement == "strict" {
					e.coverageErr = fmt.Errorf("invalid spec coverage under strict enforcement: %s", issue)
					return nil
				}
				continue
			}
			validGlobs[spec.Path] = append(validGlobs[spec.Path], normalized)
		}
	}
	artifactPaths := artifactPaths(output["artifacts"])
	if len(artifactPaths) == 0 {
		return nil
	}
	var findings []specFinding
	for _, spec := range specs {
		var matched []string
		for _, glob := range validGlobs[spec.Path] {
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
		data["enforcement"] = eval.enforcement
	default:
		return
	}
	data["run_id"] = run.RunID
	data["skill"] = run.Skill
	events, _ := state.ReadEvents(root)
	for i := len(events) - 1; i >= 0; i-- {
		journaled := events[i]
		if journaled.WorkItemID != item.ID || journaled.Data["run_id"] != run.RunID ||
			(journaled.Type != "spec.checked" && journaled.Type != "spec.drift") {
			continue
		}
		// Same type + same data = a retried collect replaying — skip it.
		// Compare with the latest spec event for this run so a finding set
		// that recurs after an intervening change is journaled again.
		if journaled.Type == eventType && reflect.DeepEqual(journaled.Data, data) {
			return
		}
		break
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
// decision offers. specDecisionAccept is the release answer: an
// answered request carrying it releases later runs whose drift matches
// the same question.
var specDecisionOptions = []string{
	"Update the spec(s) to match the change",
	"Accept the drift and proceed",
	"Rework the change to conform",
	"Consult the spec and re-run",
}

const specDecisionAccept = "Accept the drift and proceed"

// specDecisionQuestion builds the durable decision's question text from
// the finding set alone — never the run ID. A stable question is what
// makes the durable machinery work: filing dedupes across re-runs on
// work-item + question, and an answered "accept" request releases every
// later run whose drift hashes to the same text.
func specDecisionQuestion(item *state.WorkItem, findings []specFinding) string {
	canonical := append([]specFinding(nil), findings...)
	for i := range canonical {
		canonical[i].Paths = append([]string(nil), canonical[i].Paths...)
		sort.Strings(canonical[i].Paths)
		canonical[i].Paths = dedupeStrings(canonical[i].Paths)
	}
	sort.Slice(canonical, func(i, j int) bool {
		left, _ := json.Marshal(canonical[i])
		right, _ := json.Marshal(canonical[j])
		return string(left) < string(right)
	})
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)

	var b strings.Builder
	fmt.Fprintf(&b, "spec drift in %s:", item.ID)
	parts := make([]string, 0, len(findings))
	for _, f := range canonical {
		switch {
		case f.Kind == "unacknowledged":
			parts = append(parts, fmt.Sprintf("%s touched covered paths %v without consultation", f.Spec, f.Paths))
		case f.Spec != "":
			parts = append(parts, fmt.Sprintf("%s: %s", f.Spec, f.Summary))
		default:
			parts = append(parts, f.Summary)
		}
	}
	sort.Strings(parts)
	for _, p := range parts {
		fmt.Fprintf(&b, " %s;", p)
	}
	fmt.Fprintf(&b, " [finding-set:%s]", hex.EncodeToString(sum[:16]))
	b.WriteString(" resolve the spec of record before this outcome applies.")
	return b.String()
}

// releasedByDecision reports whether a human already answered this exact
// spec-drift question for the work item with "accept". That answer is the
// release path: the drift still journals, but strict enforcement steps
// down to warn rather than parking the next run behind a duplicate of a
// decision already made.
func releasedByDecision(root, workItemID, question string) bool {
	requests, _ := input.List(root)
	for _, r := range requests {
		if r == nil || r.WorkItemID != workItemID || r.Question != question {
			continue
		}
		if r.Status == input.StatusAnswered && r.Answer == specDecisionAccept {
			return true
		}
	}
	return false
}

// fileSpecDecision files the durable decision request a strict-mode spec
// finding parks behind. The run stays "retry": dispatchable again once a
// human answers, never applying the held outcome — this is what "block
// changes" means in ox-agent idiom, the same durable-request pattern as
// await_input and gate approvals.
func fileSpecDecision(root string, item *state.WorkItem, run *Run, findings []specFinding) (*input.Request, error) {
	req, err := fileInputRequest(root, item.ID, run.RunID, specDecisionQuestion(item, findings), specDecisionOptions)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// normalizeSpecRef canonicalizes a spec_impact spec reference for
// comparison with index paths: slashes, no leading ./ or .ox-agent/.
func normalizeSpecRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.ReplaceAll(ref, "\\", "/")
	ref = strings.TrimPrefix(ref, "./")
	ref = strings.TrimPrefix(ref, ".ox-agent/")
	ref = path.Clean(ref)
	if ref == "." || ref == ".." || strings.HasPrefix(ref, "../") {
		return ""
	}
	return strings.Trim(ref, "/")
}

// artifactPaths extracts the path strings from a declared artifacts list
// (bare strings or {path: ...} objects), for coverage matching.
func artifactPaths(raw any) []string {
	var out []string
	for _, entry := range toAnySlice(raw) {
		var p string
		switch v := entry.(type) {
		case string:
			p = v
		case map[string]any:
			p, _ = v["path"].(string)
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// matchSpecGlob reports whether name matches a `covers:` glob. `*` and `?`
// match within a path segment (path.Match semantics); `**` as a whole
// segment matches zero or more segments. Backslashes are normalized so a
// Windows-produced artifact path still matches its forward-slash globs.
func matchSpecGlob(pattern, name string) bool {
	return matchGlobSegments(strings.Split(normalizeSpecPath(pattern), "/"), strings.Split(normalizeSpecPath(name), "/"))
}

// Clean both sides so `./internal/x.go` and `a/../b.go` match the
// same coverage as their canonical forms.
func normalizeSpecPath(value string) string {
	return path.Clean(strings.ReplaceAll(value, "\\", "/"))
}

// matchGlobSegments matches pattern segments against name segments,
// expanding `**` to zero or more whole segments.
func matchGlobSegments(pattern, name []string) bool {
	matched := make([]bool, len(name)+1)
	matched[0] = true
	for _, segment := range pattern {
		next := make([]bool, len(name)+1)
		if segment == "**" {
			next[0] = matched[0]
			for j := 1; j <= len(name); j++ {
				next[j] = matched[j] || next[j-1]
			}
		} else {
			for j := 1; j <= len(name); j++ {
				ok, err := path.Match(segment, name[j-1])
				next[j] = matched[j-1] && err == nil && ok
			}
		}
		matched = next
	}
	return matched[len(name)]
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

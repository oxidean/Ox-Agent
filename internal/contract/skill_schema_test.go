package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillSchemaPath resolves a canonical skill schema relative to the repo root.
func skillSchemaPath(t *testing.T, skill, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "skills", skill, "schemas", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("schema %s missing: %v", path, err)
	}
	return path
}

// reviewOutput builds a valid code-review output; mutators adjust it per case.
func reviewOutput(mutators ...func(map[string]any)) []byte {
	out := map[string]any{
		"schema_version": 1,
		"invocation_id":  "inv_test",
		"skill":          "ox-agent/code-review",
		"status":         "complete",
		"verdict":        "pass",
		"summary":        "reviewed",
		"artifacts":      []any{},
		"findings":       []any{},
		"next_action":    map[string]any{"type": "advance_stage", "reason": "clean"},
	}
	for _, mutate := range mutators {
		mutate(out)
	}
	data, _ := json.Marshal(out)
	return data
}

// TestSkillSchemasCompile compiles every canonical skill input/output schema —
// a schema that fails to compile must break the build, not a release.
func TestSkillSchemasCompile(t *testing.T) {
	schemas, err := filepath.Glob(filepath.Join("..", "..", "skills", "*", "schemas", "*.json"))
	if err != nil || len(schemas) == 0 {
		t.Fatalf("no skill schemas found: %v %d", err, len(schemas))
	}
	for _, path := range schemas {
		if err := ValidateFile(path, []byte(`{}`)); err == nil {
			t.Fatalf("%s accepted an empty document — required fields unchecked", path)
		} else if strings.Contains(err.Error(), "compiling schema") || strings.Contains(err.Error(), "parsing schema") {
			t.Fatalf("%s does not compile: %v", path, err)
		}
	}
}

// TestCodeReviewVerdictCouplesNextAction verifies a verdict cannot declare a
// next_action that contradicts it.
func TestCodeReviewVerdictCouplesNextAction(t *testing.T) {
	schema := skillSchemaPath(t, "code-review", "output.schema.json")
	set := func(verdict, action string) func(map[string]any) {
		return func(out map[string]any) {
			out["verdict"] = verdict
			out["next_action"] = map[string]any{"type": action, "reason": "r"}
		}
	}
	cases := []struct {
		verdict, action string
		wantOK          bool
	}{
		{"pass", "advance_stage", true},
		{"pass", "complete", true},
		{"pass", "retry", false},
		{"pass_with_notes", "advance_stage", true},
		{"needs_rework", "retry", true},
		{"needs_rework", "escalate", true},
		{"needs_rework", "advance_stage", false},
		{"needs_rework", "complete", false},
		{"blocked", "escalate", true},
		{"blocked", "advance_stage", false},
	}
	for _, tc := range cases {
		err := ValidateFile(schema, reviewOutput(set(tc.verdict, tc.action)))
		if tc.wantOK && err != nil {
			t.Errorf("%s + %s rejected: %v", tc.verdict, tc.action, err)
		}
		if !tc.wantOK && err == nil {
			t.Errorf("%s + %s accepted; want rejection", tc.verdict, tc.action)
		}
	}
}

// TestCodeReviewFindingsRequireSeverityAndCitation verifies findings items
// carry a severity rank and a file:line citation.
func TestCodeReviewFindingsRequireSeverityAndCitation(t *testing.T) {
	schema := skillSchemaPath(t, "code-review", "output.schema.json")
	withFindings := func(findings []any) func(map[string]any) {
		return func(out map[string]any) { out["findings"] = findings }
	}
	valid := []any{map[string]any{"severity": "major", "message": "unchecked error", "citation": "main.go:42"}}
	if err := ValidateFile(schema, reviewOutput(withFindings(valid))); err != nil {
		t.Fatalf("valid finding rejected: %v", err)
	}
	for name, finding := range map[string]map[string]any{
		"no severity":  {"message": "m", "citation": "f.go:1"},
		"no citation":  {"severity": "major", "message": "m"},
		"no message":   {"severity": "major", "citation": "f.go:1"},
		"bad citation": {"severity": "major", "message": "m", "citation": "see the file"},
		"bad severity": {"severity": "meh", "message": "m", "citation": "f.go:1"},
	} {
		finding := finding
		if err := ValidateFile(schema, reviewOutput(withFindings([]any{finding}))); err == nil {
			t.Errorf("finding accepted despite %s", name)
		}
	}
}

// TestCodeReviewEvalFlagsEnum verifies eval accepts only the declared quality
// flags as booleans — misspellings are rejected.
func TestCodeReviewEvalFlagsEnum(t *testing.T) {
	schema := skillSchemaPath(t, "code-review", "output.schema.json")
	withEval := func(eval map[string]any) func(map[string]any) {
		return func(out map[string]any) { out["eval"] = eval }
	}
	good := map[string]any{"reviewed_changed_paths": true, "findings_cited": true, "severity_ranked": false, "preserves_scope": true}
	if err := ValidateFile(schema, reviewOutput(withEval(good))); err != nil {
		t.Fatalf("declared eval flags rejected: %v", err)
	}
	if err := ValidateFile(schema, reviewOutput(withEval(map[string]any{"findings_cietd": true}))); err == nil {
		t.Error("misspelled eval flag accepted")
	}
	if err := ValidateFile(schema, reviewOutput(withEval(map[string]any{"findings_cited": "yes"}))); err == nil {
		t.Error("non-boolean eval flag accepted")
	}
}

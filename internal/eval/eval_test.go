package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/project"
)

// initSkill scaffolds a project and returns the root plus a known skill.
func initSkill(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := project.Init(root); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	return root, "ox-agent/discover-context"
}

// writeOutput records one case output for a skill.
func writeOutput(t *testing.T, root, skill, caseID string, output map[string]any) {
	t.Helper()
	name := skill[strings.IndexByte(skill, '/')+1:]
	dir := runsDir(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, _ := json.Marshal(output)
	if err := os.WriteFile(outputPath(root, name, caseID), data, 0o644); err != nil {
		t.Fatalf("write output: %v", err)
	}
}

// validOutput returns a contract-conforming skill output.
func validOutput(evalBlock map[string]any) map[string]any {
	return map[string]any{
		"schema_version": 1, "invocation_id": "e1",
		"skill": "ox-agent/discover-context", "status": "complete",
		"verdict": "pass", "summary": "s", "artifacts": []any{},
		"findings": []any{}, "next_action": map[string]any{"type": "advance_stage", "reason": "done"},
		"eval": evalBlock,
	}
}

// TestScoreMixedResults verifies schema + quality rates against thresholds.
func TestScoreMixedResults(t *testing.T) {
	root, skill := initSkill(t)
	evals, name, err := Load(root, skill)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(evals.Cases) != 3 {
		t.Fatalf("expected 3 cases, got %d", len(evals.Cases))
	}
	_ = name
	// two cases fully satisfy their assertions
	writeOutput(t, root, skill, "harvest-standards", validOutput(map[string]any{
		"entries_written": true, "findings_summarized": true}))
	writeOutput(t, root, skill, "bounded-read", validOutput(map[string]any{"bounded_read": true}))
	// one fails the preserves_scope quality assertion
	writeOutput(t, root, skill, "respects-scope", validOutput(map[string]any{"preserves_scope": false}))
	report, err := Score(root, skill)
	if err != nil {
		t.Fatalf("Score() error = %v", err)
	}
	if !report.Complete || report.RecordedCount != 3 {
		t.Fatalf("report incomplete: %+v", report)
	}
	if report.SchemaRate != 1.0 {
		t.Fatalf("schema rate = %v, want 1.0", report.SchemaRate)
	}
	want := 2.0 / 3.0
	if report.QualityRate < want-0.001 || report.QualityRate > want+0.001 {
		t.Fatalf("quality rate = %v, want ~0.67", report.QualityRate)
	}
	if !report.SchemaPass || report.QualityPass {
		t.Fatalf("thresholds wrong: %+v", report)
	}
}

// TestEmitPacketsSkipsRecorded verifies emit only covers unrecorded cases.
func TestEmitPacketsSkipsRecorded(t *testing.T) {
	root, skill := initSkill(t)
	evals, name, err := Load(root, skill)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	writeOutput(t, root, skill, evals.Cases[0].ID, validOutput(nil))
	emitted, err := EmitPackets(root, evals, name)
	if err != nil {
		t.Fatalf("EmitPackets() error = %v", err)
	}
	if len(emitted) != len(evals.Cases)-1 {
		t.Fatalf("emitted %d packets, want %d", len(emitted), len(evals.Cases)-1)
	}
	last := evals.Cases[len(evals.Cases)-1]
	if _, err := os.Stat(filepath.Join(runsDir(root, name), last.ID+".packet.json")); err != nil {
		t.Fatalf("missing packet for last case: %v", err)
	}
}

// TestIncompleteReport verifies missing outputs mark the report incomplete.
func TestIncompleteReport(t *testing.T) {
	root, skill := initSkill(t)
	report, err := Score(root, skill)
	if err != nil {
		t.Fatalf("Score() error = %v", err)
	}
	if report.Complete || report.RecordedCount != 0 {
		t.Fatalf("expected incomplete report: %+v", report)
	}
}

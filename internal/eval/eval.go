// Package eval runs skill evaluation cases: it emits packets for cases
// without recorded outputs and scores recorded outputs against the skill's
// output schema plus declared quality assertions.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/contract"
	"gopkg.in/yaml.v3"
)

// Case is one evaluation prompt plus its expected assertions.
type Case struct {
	ID       string         `yaml:"id"`
	Prompt   string         `yaml:"prompt"`
	Expected map[string]any `yaml:"expected"`
}

// Thresholds are the minimum pass rates the skill must meet.
type Thresholds struct {
	SchemaPassRate  float64 `yaml:"schema_pass_rate"`
	QualityPassRate float64 `yaml:"quality_pass_rate"`
}

// Evals mirrors one skill's evals/evals.yaml.
type Evals struct {
	SchemaVersion int        `yaml:"schema_version"`
	Skill         string     `yaml:"skill"`
	Cases         []Case     `yaml:"cases"`
	Thresholds    Thresholds `yaml:"thresholds"`
}

// CaseResult is the scored outcome of one case.
type CaseResult struct {
	Case      Case
	HasOutput bool
	SchemaOK  bool
	QualityOK bool
	Detail    string
}

// Report aggregates case results against thresholds.
type Report struct {
	Skill         string
	Results       []CaseResult
	SchemaRate    float64
	QualityRate   float64
	SchemaPass    bool
	QualityPass   bool
	Complete      bool // every case has a recorded output
	RecordedCount int
}

// skillDir resolves a skill reference ("ox-agent/x" or "x") to its directory.
func skillDir(root, ref string) (string, string, error) {
	name := ref
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		name = ref[i+1:]
	}
	dir := filepath.Join(root, ".ox-agent", "skills", name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("no skill %q under .ox-agent/skills", ref)
	}
	return name, dir, nil
}

// Load parses a skill's evals/evals.yaml.
func Load(root, ref string) (*Evals, string, error) {
	name, dir, err := skillDir(root, ref)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "evals", "evals.yaml"))
	if err != nil {
		return nil, "", fmt.Errorf("loading evals for %s: %w", ref, err)
	}
	var evals Evals
	if err := yaml.Unmarshal(data, &evals); err != nil {
		return nil, "", fmt.Errorf("parsing evals for %s: %w", ref, err)
	}
	return &evals, name, nil
}

// runsDir returns the eval runtime directory for a skill.
func runsDir(root, name string) string {
	return filepath.Join(root, ".ox-agent", "runtime", "evals", name)
}

// outputPath returns where a case's recorded output is read from.
func outputPath(root, name, caseID string) string {
	return filepath.Join(runsDir(root, name), caseID+".output.json")
}

// packetPath returns where a case's packet is emitted.
func packetPath(root, name, caseID string) string {
	return filepath.Join(runsDir(root, name), caseID+".packet.json")
}

// EmitPackets writes a minimal packet for each case lacking a recorded output,
// returning the emitted paths.
func EmitPackets(root string, evals *Evals, name string) ([]string, error) {
	if err := os.MkdirAll(runsDir(root, name), 0o755); err != nil {
		return nil, err
	}
	var emitted []string
	for _, c := range evals.Cases {
		if _, err := os.Stat(outputPath(root, name, c.ID)); err == nil {
			continue
		}
		packet := map[string]any{
			"schema_version": 1,
			"skill":          evals.Skill,
			"eval_case":      c.ID,
			"prompt":         c.Prompt,
			"expected":       c.Expected,
		}
		data, err := json.MarshalIndent(packet, "", "  ")
		if err != nil {
			return nil, err
		}
		path := packetPath(root, name, c.ID)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, err
		}
		emitted = append(emitted, path)
	}
	return emitted, nil
}

// Score reads every recorded case output, validates it against the skill's
// output schema, and evaluates quality assertions from the output's "eval"
// object. Missing outputs count as incomplete, not failed.
func Score(root, ref string) (*Report, error) {
	evals, name, err := Load(root, ref)
	if err != nil {
		return nil, err
	}
	_, dir, err := skillDir(root, ref)
	if err != nil {
		return nil, err
	}
	schemaPath := filepath.Join(dir, "schemas", "output.schema.json")
	if _, err := os.Stat(schemaPath); err != nil {
		return nil, fmt.Errorf("reading output schema: %w", err)
	}
	report := &Report{Skill: evals.Skill}
	for _, c := range evals.Cases {
		result := CaseResult{Case: c, SchemaOK: true, QualityOK: true}
		data, err := os.ReadFile(outputPath(root, name, c.ID))
		if err != nil {
			result.Detail = "awaiting output"
			report.Results = append(report.Results, result)
			continue
		}
		result.HasOutput = true
		report.RecordedCount++
		var output map[string]any
		if err := json.Unmarshal(data, &output); err != nil {
			result.SchemaOK, result.QualityOK = false, false
			result.Detail = "output is not valid JSON"
			report.Results = append(report.Results, result)
			continue
		}
		if err := contract.ValidateFile(schemaPath, data); err != nil {
			result.SchemaOK = false
			result.Detail = "schema: " + err.Error()
		}
		evalBlock, _ := output["eval"].(map[string]any)
		var failed []string
		for key, want := range c.Expected {
			if key == "output_schema_valid" {
				continue // covered by the schema check itself
			}
			wantTrue, _ := want.(bool)
			got, _ := evalBlock[key].(bool)
			if wantTrue && !got {
				failed = append(failed, key)
			}
		}
		if len(failed) > 0 {
			result.QualityOK = false
			sort.Strings(failed)
			result.Detail = strings.TrimSpace(result.Detail + " quality: failed " + strings.Join(failed, ","))
		}
		report.Results = append(report.Results, result)
	}
	if report.RecordedCount > 0 {
		var schemaOK, qualityOK int
		for _, r := range report.Results {
			if !r.HasOutput {
				continue
			}
			if r.SchemaOK {
				schemaOK++
			}
			if r.QualityOK {
				qualityOK++
			}
		}
		report.SchemaRate = float64(schemaOK) / float64(report.RecordedCount)
		report.QualityRate = float64(qualityOK) / float64(report.RecordedCount)
	}
	report.SchemaPass = report.SchemaRate >= evals.Thresholds.SchemaPassRate
	report.QualityPass = report.QualityRate >= evals.Thresholds.QualityPassRate
	report.Complete = report.RecordedCount == len(evals.Cases)
	return report, nil
}

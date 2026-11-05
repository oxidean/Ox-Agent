package appctx

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
)

// Packet is the work-item context contract handed to a skill, matching
// packet.schema.json. skill, work_item_id, context, and constraints align
// with every skill's input schema.
type Packet struct {
	SchemaVersion int           `json:"schema_version"`
	PacketID      string        `json:"packet_id"`
	CreatedAt     string        `json:"created_at"`
	WorkItemID    string        `json:"work_item_id"`
	Skill         string        `json:"skill"`
	SkillDir      string        `json:"skill_dir"`
	Stage         string        `json:"stage"`
	Context       PacketContext `json:"context"`
	Constraints   PacketLimits  `json:"constraints"`
	Output        PacketOutput  `json:"output"`
}

// PacketContext lists the project context the skill may load.
type PacketContext struct {
	Project      map[string]any `json:"project"`
	Standards    []string       `json:"standards,omitempty"`
	Decisions    []string       `json:"decisions,omitempty"`
	References   []string       `json:"references,omitempty"`
	PriorOutputs []PriorOutput  `json:"prior_outputs,omitempty"`
}

// PriorOutput points at a collected run's output for reuse as context.
type PriorOutput struct {
	Skill string `json:"skill"`
	RunID string `json:"run_id"`
	Path  string `json:"path"`
}

// PacketLimits bounds what the skill may spend and touch.
type PacketLimits struct {
	TokenBudget  int      `json:"token_budget"`
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	MaxFixCycles int      `json:"max_fix_cycles"`
}

// PacketOutput tells the skill where its output contract and file live.
type PacketOutput struct {
	Schema string `json:"schema"`
	Path   string `json:"path"`
}

// defaultTokenBudget applies when the caller sets no explicit budget.
const defaultTokenBudget = 8000

// Compile builds the packet for invoking skill on the work item at stage.
// outputPath is the .ox-agent-relative path where the skill's output JSON is
// expected; it is empty for standalone packet compilation.
func Compile(root string, item *state.WorkItem, skill, stage string, budget int, outputPath string) (*Packet, error) {
	base := filepath.Join(root, ".ox-agent")
	if _, err := os.Stat(filepath.Join(base, "skills", skill, "skill.yaml")); err != nil {
		return nil, fmt.Errorf("skill %q is not materialized in this project", skill)
	}
	settings, err := project.LoadSettings(root)
	if err != nil {
		return nil, fmt.Errorf("loading project settings: %w", err)
	}
	policy, err := project.LoadPolicy(root)
	if err != nil {
		return nil, fmt.Errorf("loading project policy: %w", err)
	}
	if budget <= 0 {
		budget = defaultTokenBudget
	}
	packet := &Packet{
		SchemaVersion: 1,
		PacketID:      fmt.Sprintf("pkt_%d_%06x", time.Now().UnixMilli(), rand.Uint64N(1<<24)),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		WorkItemID:    item.ID,
		Skill:         "ox-agent/" + skill,
		SkillDir:      "skills/" + skill,
		Stage:         stage,
		Context: PacketContext{
			Project: map[string]any{
				"name":         settings.Project.Name,
				"test_command": settings.Project.TestCommand,
			},
		},
		Constraints: PacketLimits{
			TokenBudget:  budget,
			MaxFixCycles: policy.Budgets.MaxFixCycles,
		},
		Output: PacketOutput{
			Schema: "skills/" + skill + "/schemas/output.schema.json",
			Path:   outputPath,
		},
	}
	for _, kind := range []struct {
		kind string
		out  *[]string
	}{
		{"standard", &packet.Context.Standards},
		{"decision", &packet.Context.Decisions},
		{"other", &packet.Context.References},
	} {
		entries, err := Entries(root, kind.kind)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			*kind.out = append(*kind.out, entry.Path)
		}
	}
	priors, err := collectedRuns(root, item.ID)
	if err != nil {
		return nil, err
	}
	packet.Context.PriorOutputs = priors
	return packet, nil
}

// collectedRuns lists collected run outputs for the work item.
func collectedRuns(root, workItemID string) ([]PriorOutput, error) {
	runsDir := filepath.Join(root, ".ox-agent", "runtime", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var priors []PriorOutput
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(runsDir, entry.Name(), "run.json"))
		if err != nil {
			continue
		}
		var run struct {
			WorkItemID string `json:"work_item_id"`
			Skill      string `json:"skill"`
			Status     string `json:"status"`
		}
		if json.Unmarshal(data, &run) != nil || run.WorkItemID != workItemID || run.Status != "collected" {
			continue
		}
		priors = append(priors, PriorOutput{
			Skill: run.Skill,
			RunID: entry.Name(),
			Path:  "runtime/runs/" + entry.Name() + "/output.json",
		})
	}
	return priors, nil
}

// Bytes reports the packet as indented JSON.
func (p *Packet) Bytes() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

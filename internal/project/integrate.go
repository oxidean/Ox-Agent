package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// markers delimit the managed block ox-agent maintains in AGENTS.md.
const (
	markerBegin = "<!-- ox-agent:begin -->"
	markerEnd   = "<!-- ox-agent:end -->"
)

// agentsBlock is the managed instruction block written into AGENTS.md.
const agentsBlock = markerBegin + `
## Ox-Agent toolkit

This repository is managed by ` + "`ox-agent`" + ` — a repository-local AI-SDLC
runtime. Project state, skills, and contracts live under ` + "`.ox-agent/`" + `.

- ` + "`ox-agent doctor`" + ` validates the setup; ` + "`ox-agent start <title>`" + ` opens work items.
- Work items carry state in ` + "`index.json`" + `; drive them with
  ` + "`ox-agent transition|archive|unblock|status`" + `.
- Human input is durable, never synchronous. When you need an answer or a
  gated approval, file ` + "`ox-agent input request`" + ` and END THE TURN —
  chat prompts expire before humans see them. Poll with
  ` + "`ox-agent input pending`" + ` / ` + "`input wait --timeout <budget>`" + `;
  answers arrive via ` + "`input respond <id> --by <name> --answer \"...\"`" + `
  (approvals: ` + "`--approve|--reject`" + `). Escalated runs and gate hits file
  these requests automatically; a skill asks a
  question with ` + "`next_action.type: await_input`" + ` — the run stays
  dispatchable and the answer lands in the next packet under ` + "`context.inputs`" + `.
- Skills are contracts, not free-form prompts: ` + "`.ox-agent/skills/<name>/SKILL.md`" + `
  plus input/output ` + "`schemas/`" + `.
- Compile the bounded packet (` + "`ox-agent context compile <wi> --skill <s>`" + `)
  or dispatch via ` + "`ox-agent run <wi>`" + `; results land in ` + "`runtime/runs/`" + `
  and are schema-validated on collection.
- Materialized skills also resolve under ` + "`.agents/skills/`" + ` for harnesses
  that scan that path.

Do not edit ` + "`.ox-agent/skills`" + `, ` + "`schemas`" + `, ` + "`workflows`" + `, ` + "`pipelines`" + `,
` + "`lifecycle`" + `, or ` + "`integrations`" + ` in place — they are materialized assets
tracked by ` + "`assets.lock`" + `. ` + "`kind: core`" + ` in a skill manifest means bundled
with the distribution and sync-owned (repaired on ` + "`init --repair --sync`" + `);
` + "`kind: extension`" + ` means added post-init and never reconciled. To customize a
core skill, fork it under a new directory name and set its manifest ` + "`kind:`" + `
to ` + "`extension`" + `; enable the new name under ` + "`skills.enabled`" + ` and replace
the original name in the ` + "`skills.stage_skills`" + ` entries where it should run —
appending leaves the original selected first. ` + "`stage_skills`" + ` only feeds
stage-driven runs: ` + "`ox-agent review`" + `, ` + "`ship`" + `, and ` + "`run <wi> --pipeline <name>`" + `
read the ` + "`skill:`" + ` fields inside ` + "`pipelines/<name>.yaml`" + ` instead. To swap a
pipeline step's skill, copy the pipeline file under a new filename and set
its internal ` + "`name:`" + ` to the filename stem — ` + "`--pipeline <name>`" + ` resolves
the file by name and progress tracks that internal name, so the two must
match — then edit its ` + "`skill:`" + ` references and invoke it via ` + "`ox-agent run <wi> --pipeline <name>`" + `.
Update hard-coded asset paths
in the fork's instructions too: a copied SKILL.md that still points at the
core skill's scripts executes the core helper. An extension dir that reuses
a bundled skill's name can still be overwritten by ` + "`skills update --yes`" + ` —
pick a distinct name. If ` + "`assets.lock`" + ` is missing or corrupt, repair keeps
drifted files and reports them as conflicts instead of rewriting — review
conflicts before assuming sync ownership was restored.
` + markerEnd + `
`

// IntegrateReport summarizes what Integrate changed.
type IntegrateReport struct {
	Created  []string
	Updated  []string
	Warnings []string
}

// Integrate wires harness discovery for the project: a managed AGENTS.md
// block describing the toolkit, and .agents/skills + .claude/skills symlinks
// resolving to the materialized skill tree.
func Integrate(root string) (*IntegrateReport, error) {
	report := &IntegrateReport{}
	if err := integrateAgentsMD(root, report); err != nil {
		return nil, err
	}
	for _, link := range []struct {
		path   string
		target string
	}{
		{filepath.Join(root, ".agents", "skills"), filepath.Join("..", ".ox-agent", "skills")},
		{filepath.Join(root, ".claude", "skills"), filepath.Join("..", ".agents", "skills")},
	} {
		if err := integrateSymlink(link.path, link.target, report); err != nil {
			return nil, err
		}
	}
	return report, nil
}

// integrateAgentsMD upserts the managed block in AGENTS.md.
func integrateAgentsMD(root string, report *IntegrateReport) error {
	path := filepath.Join(root, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	switch {
	case strings.Contains(content, markerBegin) && strings.Contains(content, markerEnd):
		start := strings.Index(content, markerBegin)
		end := strings.Index(content, markerEnd) + len(markerEnd)
		content = content[:start] + strings.TrimRight(agentsBlock, "\n") + content[end:]
		report.Updated = append(report.Updated, "AGENTS.md")
	default:
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		if content != "" {
			content += "\n"
		}
		content += agentsBlock
		if len(data) == 0 {
			report.Created = append(report.Created, "AGENTS.md")
		} else {
			report.Updated = append(report.Updated, "AGENTS.md")
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// integrateSymlink links path→target, leaving real directories and foreign
// links untouched with a warning.
func integrateSymlink(path, target string, report *IntegrateReport) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if dest, err := os.Readlink(path); err == nil && filepath.Clean(dest) == filepath.Clean(target) {
				return nil
			}
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s is a symlink to another target; left unchanged", path))
			return nil
		}
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s exists as a real path; left unchanged", path))
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(target, path); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("could not link %s: %v (on Windows enable Developer Mode or run elevated)", path, err))
		return nil
	}
	report.Created = append(report.Created, strings.TrimPrefix(path, filepath.Dir(filepath.Dir(path))+string(filepath.Separator)))
	return nil
}

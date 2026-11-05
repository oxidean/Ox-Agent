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
- Skills are contracts, not free-form prompts: ` + "`.ox-agent/skills/<name>/SKILL.md`" + `
  plus input/output ` + "`schemas/`" + `.
- Compile the bounded packet (` + "`ox-agent context compile <wi> --skill <s>`" + `)
  or dispatch via ` + "`ox-agent run <wi>`" + `; results land in ` + "`runtime/runs/`" + `
  and are schema-validated on collection.
- Materialized skills also resolve under ` + "`.agents/skills/`" + ` for harnesses
  that scan that path.

Do not edit ` + "`.ox-agent/skills`" + `, ` + "`schemas`" + `, ` + "`workflows`" + `, ` + "`pipelines`" + `,
` + "`lifecycle`" + `, or ` + "`integrations`" + ` in place — they are materialized assets
tracked by ` + "`assets.lock`" + `. Add extension skills as new directories instead.
` + markerEnd + `
`

// IntegrationGaps lists the discovery pieces Integrate would wire that are
// currently absent: the managed AGENTS.md block and the .agents/.claude
// skill symlinks. Returns nil when the project is fully integrated.
func IntegrationGaps(root string) []string {
	var missing []string
	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || !strings.Contains(string(data), markerBegin) {
		missing = append(missing, "AGENTS.md block")
	}
	// Both discovery links are expected to resolve to the materialized
	// skill tree; a link that points at any other existing directory leaves
	// agents scanning the wrong skills while looking wired.
	want, wantErr := filepath.EvalSymlinks(filepath.Join(root, ".ox-agent", "skills"))
	for _, link := range []string{filepath.Join(".agents", "skills"), filepath.Join(".claude", "skills")} {
		target, err := os.Readlink(filepath.Join(root, link))
		if err != nil {
			missing = append(missing, link+" symlink")
			continue
		}
		resolved := target
		if !filepath.IsAbs(target) {
			resolved = filepath.Join(root, filepath.Dir(link), target)
		}
		if _, err := os.Stat(resolved); err != nil {
			missing = append(missing, link+" symlink (broken)")
			continue
		}
		got, err := filepath.EvalSymlinks(resolved)
		if err == nil && wantErr == nil && got != want {
			missing = append(missing, link+" symlink (wrong target)")
		}
	}

	return missing
}

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
		report.Warnings = append(report.Warnings, fmt.Sprintf("could not link %s: %v", path, err))
		return nil
	}
	report.Created = append(report.Created, strings.TrimPrefix(path, filepath.Dir(filepath.Dir(path))+string(filepath.Separator)))
	return nil
}

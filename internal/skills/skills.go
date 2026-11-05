// Package skills inspects and maintains the skill set materialized under
// .ox-agent/skills: listing, contract checks, and re-materialization of
// embedded skills that drifted. Directories the embedded tree does not cover
// are project-local skills — reported, never modified.
package skills

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/oxidean/Ox-Agent/internal/buildinfo"
	"github.com/oxidean/Ox-Agent/internal/contract"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Status classifies a materialized skill directory against the embedded tree.
type Status string

const (
	// StatusCurrent means every embedded file matches on disk.
	StatusCurrent Status = "current"
	// StatusModified means at least one file differs from the embedded copy.
	StatusModified Status = "modified"
	// StatusIncomplete means at least one embedded file is missing.
	StatusIncomplete Status = "incomplete"
	// StatusProject means the directory is not part of the embedded
	// distribution — a project-local skill, left alone by Update.
	StatusProject Status = "project"
)

// manifest is the skill.yaml surface List and Check inspect.
type manifest struct {
	Name          string `yaml:"name"`
	Version       string `yaml:"version"`
	Category      string `yaml:"category"`
	Compatibility struct {
		OxAgent string `yaml:"ox_agent"`
	} `yaml:"compatibility"`
	Inputs struct {
		Schema string `yaml:"schema"`
	} `yaml:"inputs"`
	Outputs struct {
		Schema string `yaml:"schema"`
	} `yaml:"outputs"`
	Evaluation struct {
		Suite           string  `yaml:"suite"`
		MinimumPassRate float64 `yaml:"minimum_pass_rate"`
	} `yaml:"evaluation"`
}

// Skill is one materialized skill directory.
type Skill struct {
	Dir         string
	Name        string // manifest name, e.g. ox-agent/specify
	Version     string
	Category    string
	Compat      string // compatibility.ox_agent constraint
	CompatOK    bool   // constraint satisfied by the running binary
	Status      Status
	Enabled     bool  // present in config.yaml skills.enabled
	ManifestErr error // skill.yaml missing or unparseable
}

// skillsDir returns the materialized skills directory under root.
func skillsDir(root string) string {
	return filepath.Join(project.Base(root), "skills")
}

// embeddedDirs groups embedded asset paths under skills/ by skill directory.
// Loose top-level files (README.md) land under the empty key.
func embeddedDirs() (map[string][]string, error) {
	files, err := assets.List("skills")
	if err != nil {
		return nil, err
	}
	dirs := map[string][]string{}
	for _, rel := range files {
		parts := strings.SplitN(rel, "/", 3)
		key := ""
		if len(parts) == 3 {
			key = parts[1]
		}
		dirs[key] = append(dirs[key], rel)
	}
	return dirs, nil
}

// statusOf compares one skill directory on disk with its embedded files.
func statusOf(base, dir string, embFiles []string) Status {
	if len(embFiles) == 0 {
		return StatusProject
	}
	var missing, modified bool
	for _, rel := range embFiles {
		target := filepath.Join(base, dir, filepath.FromSlash(strings.TrimPrefix(rel, "skills/"+dir+"/")))
		got, err := os.ReadFile(target)
		if err != nil {
			if os.IsNotExist(err) {
				missing = true
			}
			continue
		}
		want, err := assets.ReadFile(rel)
		if err != nil {
			continue
		}
		if !bytes.Equal(got, want) {
			modified = true
		}
	}
	// Missing files outrank modifications: an incomplete skill is an error,
	// drift alone is only a warning.
	switch {
	case missing:
		return StatusIncomplete
	case modified:
		return StatusModified
	default:
		return StatusCurrent
	}
}

// List scans .ox-agent/skills and reports every directory containing a
// skill.yaml, cross-referenced with config.yaml's enabled set and the
// embedded distribution.
func List(root string) ([]*Skill, error) {
	base := skillsDir(root)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("reading skills dir: %w", err)
	}
	settings, err := project.LoadSettings(root)
	if err != nil {
		return nil, err
	}
	enabled := map[string]bool{}
	for _, name := range settings.Skills.Enabled {
		enabled[name] = true
	}
	emb, err := embeddedDirs()
	if err != nil {
		return nil, err
	}
	// Candidate skills are the union of on-disk directories and embedded
	// skill names, so a deleted bundled skill still surfaces as incomplete.
	names := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			names[entry.Name()] = true
		}
	}
	for name := range emb {
		if name != "" {
			names[name] = true
		}
	}
	var out []*Skill
	for name := range names {
		manifestPath := filepath.Join(base, name, "skill.yaml")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			embFiles, embedded := emb[name]
			if !embedded && os.IsNotExist(err) {
				continue // project-local directory without a manifest
			}
			out = append(out, &Skill{
				Dir:         name,
				Enabled:     enabled[name],
				Status:      statusOf(base, name, embFiles),
				ManifestErr: fmt.Errorf("reading skill.yaml: %w", err),
			})
			continue
		}
		skill := &Skill{Dir: name, Enabled: enabled[name]}
		var m manifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			skill.ManifestErr = fmt.Errorf("parsing skill.yaml: %w", err)
		} else {
			skill.Name = m.Name
			skill.Version = m.Version
			skill.Category = m.Category
			skill.Compat = m.Compatibility.OxAgent
			if skill.Compat == "" {
				skill.CompatOK = true
			} else if ok, err := contract.Satisfies(buildinfo.Version, skill.Compat); err == nil {
				skill.CompatOK = ok
			}
		}
		skill.Status = statusOf(base, name, emb[name])
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// Finding is one skills check observation.
type Finding struct {
	Skill   string // skill directory name
	Level   string // "error" or "warning"
	Message string
}

// Check validates every materialized skill: manifest against the
// skill-manifest schema, declared contract files present and compilable,
// SKILL.md present, and the ox_agent pin satisfied by this binary.
func Check(root string) ([]Finding, error) {
	base := skillsDir(root)
	skillList, err := List(root)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	fail := func(skill *Skill, level, format string, args ...any) {
		findings = append(findings, Finding{Skill: skill.Dir, Level: level, Message: fmt.Sprintf(format, args...)})
	}
	for _, skill := range skillList {
		if skill.ManifestErr != nil {
			fail(skill, "error", "%v", skill.ManifestErr)
			continue
		}
		data, err := os.ReadFile(filepath.Join(base, skill.Dir, "skill.yaml"))
		if err == nil {
			if err := contract.ValidateYAML("skill-manifest", data); err != nil {
				fail(skill, "error", "skill.yaml fails manifest schema: %v", err)
			}
		}
		var m manifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			continue // already reported by List via ManifestErr
		}
		if _, err := os.Stat(filepath.Join(base, skill.Dir, "SKILL.md")); err != nil {
			fail(skill, "error", "SKILL.md missing")
		}
		for _, declared := range []struct {
			kind string
			rel  string
		}{
			{"input schema", m.Inputs.Schema},
			{"output schema", m.Outputs.Schema},
		} {
			if declared.rel == "" {
				continue
			}
			path := filepath.Join(base, skill.Dir, filepath.FromSlash(declared.rel))
			if err := compileSchema(path); err != nil {
				fail(skill, "error", "%s %s: %v", declared.kind, declared.rel, err)
			}
		}
		if m.Evaluation.Suite != "" {
			path := filepath.Join(base, skill.Dir, filepath.FromSlash(m.Evaluation.Suite))
			if info, err := os.Stat(path); err != nil || info.Size() == 0 {
				fail(skill, "warning", "eval suite %s missing or empty", m.Evaluation.Suite)
			}
		}
		if !skill.CompatOK {
			fail(skill, "error", "compatibility %q not satisfied by binary %s", skill.Compat, buildinfo.Version)
		}
		switch skill.Status {
		case StatusModified:
			fail(skill, "warning", "locally modified files (drifted from embedded)")
		case StatusIncomplete:
			fail(skill, "error", "files missing relative to embedded; run skills update")
		}
	}
	return findings, nil
}

// compileSchema loads a JSON schema file and verifies it compiles.
func compileSchema(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parsing: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	url := "https://ox-agent.dev/skill-schemas/" + filepath.Base(path)
	if err := compiler.AddResource(url, doc); err != nil {
		return err
	}
	if _, err := compiler.Compile(url); err != nil {
		return fmt.Errorf("compiling: %w", err)
	}
	return nil
}

// UpdateReport summarizes a skills update run.
type UpdateReport struct {
	Restored []string // missing files written
	Replaced []string // drifted files overwritten
	Skipped  []string // drifted files left in place (no --yes)
}

// Update re-materializes every embedded file under skills/. Missing files
// are always restored; files whose contents differ are overwritten only when
// overwrite is set, and otherwise reported as skipped. Project-local skills
// are never touched. The asset lock is rewritten when anything changed.
func Update(root string, overwrite bool) (*UpdateReport, error) {
	base := project.Base(root)
	files, err := assets.List("skills")
	if err != nil {
		return nil, err
	}
	report := &UpdateReport{}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", base, err)
	}
	for _, rel := range files {
		target := filepath.Join(base, filepath.FromSlash(rel))
		want, err := assets.ReadFile(rel)
		if err != nil {
			return nil, err
		}
		got, err := os.ReadFile(target)
		switch {
		case err == nil && bytes.Equal(got, want):
			continue
		case os.IsNotExist(err):
			if err := project.ConfinedTarget(resolvedBase, target); err != nil {
				return nil, err
			}
			if err := write(target, want); err != nil {
				return nil, err
			}
			report.Restored = append(report.Restored, rel)
		case err != nil:
			return nil, err
		default:
			if !overwrite {
				report.Skipped = append(report.Skipped, rel)
				continue
			}
			if err := project.ConfinedTarget(resolvedBase, target); err != nil {
				return nil, err
			}
			if err := write(target, want); err != nil {
				return nil, err
			}
			report.Replaced = append(report.Replaced, rel)
		}
	}
	if len(report.Restored)+len(report.Replaced) > 0 {
		if err := assets.WriteLock(base); err != nil {
			return nil, err
		}
	}
	sort.Strings(report.Restored)
	sort.Strings(report.Replaced)
	sort.Strings(report.Skipped)
	return report, nil
}

// write creates parent directories and writes data to path.
func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

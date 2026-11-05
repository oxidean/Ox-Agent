package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/oxidean/Ox-Agent/internal/buildinfo"
	"github.com/oxidean/Ox-Agent/internal/contract"
	"gopkg.in/yaml.v3"
)

// Check is one doctor finding: a named check, its result, the issue detail
// when it failed, and whether Repair can restore it.
type Check struct {
	Name    string
	OK      bool
	Detail  string
	Fixable bool
}

// DoctorReport collects every doctor finding plus informational warnings.
type DoctorReport struct {
	Checks   []Check
	Warnings []string
}

// Healthy reports whether every check passed.
func (r *DoctorReport) Healthy() bool {
	for _, check := range r.Checks {
		if !check.OK {
			return false
		}
	}
	return true
}

// add records a check result.
func (r *DoctorReport) add(name string, err error, fixable bool) {
	if err != nil {
		r.Checks = append(r.Checks, Check{Name: name, Detail: err.Error(), Fixable: fixable})
		return
	}
	r.Checks = append(r.Checks, Check{Name: name, OK: true})
}

// Doctor runs every project health check and collects all findings instead
// of stopping at the first failure.
func Doctor(root string) *DoctorReport {
	report := &DoctorReport{}
	base := Base(root)
	if _, err := os.Stat(base); err != nil {
		report.add(".ox-agent exists", fmt.Errorf("not found; run init"), false)
		return report
	}
	report.add(".ox-agent exists", nil, false)

	for _, dir := range scaffoldDirs {
		_, err := os.Stat(filepath.Join(base, dir))
		report.add("dir "+dir, wrapMissing(err, "missing"), true)
	}
	for _, seed := range []string{"framework.yaml", "config.yaml", "policy.yaml", ".gitignore", ".gitattributes", "assets.lock", "README.md", "state/events.ndjson", "context/project.yaml"} {
		_, err := os.Stat(filepath.Join(base, filepath.FromSlash(seed)))
		report.add("file "+seed, wrapMissing(err, "missing"), true)
	}

	report.checkFramework(root)
	report.checkConfig(root)
	report.checkSchemaFile(base, "policy.yaml", "policy")
	report.checkLifecycle(base)
	report.checkGlob(base, "workflows", "workflow")
	report.checkGlob(base, "pipelines", "pipeline")
	report.checkGlob(base, "integrations", "integration")
	report.checkAssets(base)
	report.checkIntegration(root)
	return report
}

// checkIntegration warns when agent-discovery wiring is absent. Integration
// is opt-in, so gaps are warnings rather than failed checks.
func (r *DoctorReport) checkIntegration(root string) {
	if missing := IntegrationGaps(root); len(missing) > 0 {
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("agent discovery not wired (%s); run 'ox-agent integrate'", strings.Join(missing, ", ")))
	}
}

// wrapMissing converts a stat error into a short issue string.
func wrapMissing(err error, what string) error {
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return fmt.Errorf("%s", what)
	}
	return err
}

// checkFramework validates framework.yaml and binary compatibility.
func (r *DoctorReport) checkFramework(root string) {
	path := filepath.Join(Base(root), "framework.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		report_add_err(r, "schema framework.yaml", err, false)
		return
	}
	if err := contract.ValidateYAML("framework", data); err != nil {
		r.add("schema framework.yaml", err, false)
		return
	}
	r.add("schema framework.yaml", nil, false)
	framework, err := LoadFramework(root)
	if err != nil {
		r.add("framework version", err, false)
		return
	}
	ok, err := contract.Satisfies(buildinfo.Version, framework.Framework.RequiredVersion)
	if err != nil {
		r.add("framework version", err, false)
		return
	}
	if !ok {
		r.add("framework version", fmt.Errorf("binary %s does not satisfy required_version %q", buildinfo.Version, framework.Framework.RequiredVersion), false)
		return
	}
	r.add("framework version", nil, false)
}

// checkConfig validates config.yaml and cross-checks skills and stages.
func (r *DoctorReport) checkConfig(root string) {
	base := Base(root)
	path := filepath.Join(base, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		r.add("schema config.yaml", err, false)
		return
	}
	if err := contract.ValidateYAML("config", data); err != nil {
		r.add("schema config.yaml", err, false)
		return
	}
	r.add("schema config.yaml", nil, false)
	settings, err := LoadSettings(root)
	if err != nil {
		r.add("config semantics", err, false)
		return
	}
	skillDirs := map[string]bool{}
	if entries, err := os.ReadDir(filepath.Join(base, "skills")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				if _, err := os.Stat(filepath.Join(base, "skills", entry.Name(), "skill.yaml")); err == nil {
					skillDirs[entry.Name()] = true
				}
			}
		}
	}
	enabled := map[string]bool{}
	var missing []string
	for _, name := range settings.Skills.Enabled {
		enabled[name] = true
		if !skillDirs[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		r.add("skills enabled", fmt.Errorf("enabled but not materialized: %v", missing), true)
	} else {
		r.add("skills enabled", nil, false)
	}
	stages := r.knownStages(base)
	var badKeys, badSkills []string
	for stage, names := range settings.Skills.StageSkills {
		if !stages[stage] {
			badKeys = append(badKeys, stage)
		}
		for _, name := range names {
			if !enabled[name] {
				badSkills = append(badSkills, fmt.Sprintf("%s→%s", stage, name))
			}
		}
	}
	sort.Strings(badKeys)
	if len(badKeys) > 0 {
		r.add("stage_skills keys", fmt.Errorf("unknown stages: %v", badKeys), false)
	} else {
		r.add("stage_skills keys", nil, false)
	}
	if len(badSkills) > 0 {
		sort.Strings(badSkills)
		r.Warnings = append(r.Warnings, fmt.Sprintf("stage_skills reference disabled skills: %v", badSkills))
	}
	if _, ok := settings.Workflow.Profiles[settings.Workflow.DefaultProfile]; !ok {
		r.add("default profile", fmt.Errorf("default_profile %q not in profiles", settings.Workflow.DefaultProfile), false)
		return
	}
	r.add("default profile", nil, false)
	for profile, profileStages := range settings.Workflow.Profiles {
		var unknown []string
		for _, stage := range profileStages {
			if !stages[stage] {
				unknown = append(unknown, stage)
			}
		}
		if len(unknown) > 0 {
			r.add("profile "+profile, fmt.Errorf("unknown stages: %v", unknown), false)
		} else {
			r.add("profile "+profile, nil, false)
		}
	}
}

// knownStages returns the union of stage names across workflows/*.yaml.
func (r *DoctorReport) knownStages(base string) map[string]bool {
	stages := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(base, "workflows"))
	if err != nil {
		return stages
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(base, "workflows", entry.Name()))
		if err != nil {
			continue
		}
		var wf struct {
			Stages []string `yaml:"stages"`
		}
		if yaml.Unmarshal(data, &wf) == nil {
			for _, stage := range wf.Stages {
				stages[stage] = true
			}
		}
	}
	return stages
}

// checkSchemaFile validates one YAML file against the named schema.
func (r *DoctorReport) checkSchemaFile(base, rel, schema string) {
	data, err := os.ReadFile(filepath.Join(base, rel))
	if err != nil {
		r.add("schema "+rel, err, false)
		return
	}
	r.add("schema "+rel, contract.ValidateYAML(schema, data), false)
}

// checkLifecycle validates the lifecycle file and transition endpoints.
func (r *DoctorReport) checkLifecycle(base string) {
	rel := "lifecycle/workflow.yaml"
	data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel)))
	if err != nil {
		r.add("schema "+rel, err, true)
		return
	}
	if err := contract.ValidateYAML("lifecycle", data); err != nil {
		r.add("schema "+rel, err, false)
		return
	}
	r.add("schema "+rel, nil, false)
	var life struct {
		States      []string `yaml:"states"`
		Transitions []struct {
			From string `yaml:"from"`
			To   string `yaml:"to"`
		} `yaml:"transitions"`
	}
	if yaml.Unmarshal(data, &life) != nil {
		return
	}
	states := map[string]bool{}
	for _, s := range life.States {
		states[s] = true
	}
	var bad []string
	for _, tr := range life.Transitions {
		if tr.From != "*" && !states[tr.From] {
			bad = append(bad, "from:"+tr.From)
		}
		if tr.To != "*" && !states[tr.To] {
			bad = append(bad, "to:"+tr.To)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		r.add("lifecycle transitions", fmt.Errorf("transitions reference unknown states: %v", bad), false)
		return
	}
	r.add("lifecycle transitions", nil, false)
}

// checkGlob validates every *.yaml file in an asset directory against a schema.
func (r *DoctorReport) checkGlob(base, dir, schema string) {
	entries, err := os.ReadDir(filepath.Join(base, dir))
	if err != nil {
		r.add("schemas "+dir, err, false)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		rel := dir + "/" + entry.Name()
		data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel)))
		if err != nil {
			r.add("schema "+rel, err, false)
			continue
		}
		r.add("schema "+rel, contract.ValidateYAML(schema, data), false)
	}
	if dir == "pipelines" {
		r.checkPipelineRefs(base, entries)
	}
}

// checkPipelineRefs cross-checks pipeline step skill and depends_on references.
func (r *DoctorReport) checkPipelineRefs(base string, entries []os.DirEntry) {
	skillDirs := map[string]bool{}
	if skillEntries, err := os.ReadDir(filepath.Join(base, "skills")); err == nil {
		for _, entry := range skillEntries {
			if entry.IsDir() {
				skillDirs[entry.Name()] = true
			}
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(base, "pipelines", entry.Name()))
		if err != nil {
			continue
		}
		var pipe struct {
			Steps []struct {
				ID        string   `yaml:"id"`
				Skill     string   `yaml:"skill"`
				DependsOn []string `yaml:"depends_on"`
			} `yaml:"steps"`
		}
		if yaml.Unmarshal(data, &pipe) != nil {
			continue
		}
		ids := map[string]bool{}
		for _, step := range pipe.Steps {
			ids[step.ID] = true
		}
		var badDeps, badSkills []string
		for _, step := range pipe.Steps {
			for _, dep := range step.DependsOn {
				if !ids[dep] {
					badDeps = append(badDeps, fmt.Sprintf("%s→%s", step.ID, dep))
				}
			}
			if !skillDirs[step.Skill] {
				badSkills = append(badSkills, fmt.Sprintf("%s→%s", step.ID, step.Skill))
			}
		}
		name := "pipeline refs " + entry.Name()
		switch {
		case len(badDeps) > 0:
			r.add(name, fmt.Errorf("unknown depends_on: %v", badDeps), false)
		case len(badSkills) > 0:
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s references unmaterialized skills: %v", entry.Name(), badSkills))
			r.add(name, nil, false)
		default:
			r.add(name, nil, false)
		}
	}
}

// checkAssets verifies the materialized asset tree against assets.lock.
func (r *DoctorReport) checkAssets(base string) {
	if _, err := os.Stat(filepath.Join(base, assets.LockPath)); err != nil {
		r.add("asset lock", wrapMissing(err, "missing"), true)
		return
	}
	missing, changed, unlisted, err := assets.CheckLock(base)
	if err != nil {
		r.add("asset lock", err, false)
		return
	}
	if len(missing) > 0 {
		r.add("asset lock", fmt.Errorf("missing asset files: %v", missing), true)
		return
	}
	r.add("asset lock", nil, false)
	if len(changed) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("locally modified assets (drift): %v — re-materialize with 'ox-agent init --repair --sync'", changed))
	}
	if len(unlisted) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("local extension files not in lock: %v", unlisted))
	}
}

// report_add_err records a check failure without a fixable flag.
func report_add_err(r *DoctorReport, name string, err error, fixable bool) {
	r.add(name, err, fixable)
}

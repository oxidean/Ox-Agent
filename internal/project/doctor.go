package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		if dir == "state/input" && os.IsNotExist(err) {
			// Requests are created on demand; a fresh clone legitimately
			// lacks the directory — report ok rather than missing.
			report.add("dir "+dir, nil, true)
			continue
		}
		report.add("dir "+dir, wrapMissing(err, "missing"), true)
	}
	for _, seed := range []string{"framework.yaml", "config.yaml", "policy.yaml", ".gitignore", ".gitattributes", "assets.lock", "state/events.ndjson", "context/project.yaml", "context/specs/README.md", "README.md"} {
		_, err := os.Stat(filepath.Join(base, filepath.FromSlash(seed)))
		report.add("file "+seed, wrapMissing(err, "missing"), true)
	}

	report.checkFramework(root)
	report.checkConfig(root)
	report.checkSchemaFile(base, "policy.yaml", "policy")
	if _, err := os.Stat(filepath.Join(base, "remote-skills.lock")); err == nil {
		report.checkSchemaFile(base, "remote-skills.lock", "remote-skills-lock")
	}
	report.checkLifecycle(base)
	report.checkGlob(base, "workflows", "workflow")
	report.checkGlob(base, "pipelines", "pipeline")
	report.checkGlob(base, "integrations", "integration")
	report.checkGlobMissingOK(base, "state/input", "input-request")
	report.checkAssets(base)
	report.checkAssetModes(base)
	report.checkIntegration(root)
	report.checkSourceControl(root)
	return report
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
		if tr.To != "*" && tr.To != "blocked_from" && !states[tr.To] {
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
	r.checkGlobDir(base, dir, schema, false, false)
}

// checkGlobMissingOK tolerates a missing directory (e.g. state/input on a
// fresh clone — git drops empty dirs) while validating every *.yaml that
// exists when it is present.
func (r *DoctorReport) checkGlobMissingOK(base, dir, schema string) {
	// state/input holds runtime request files plus lock files — only
	// *.yaml documents are schema-bound here; other asset directories
	// keep validating every file regardless of extension.
	r.checkGlobDir(base, dir, schema, true, true)
}

func (r *DoctorReport) checkGlobDir(base, dir, schema string, missingOK, yamlOnly bool) {
	entries, err := os.ReadDir(filepath.Join(base, dir))
	if err != nil {
		if missingOK && os.IsNotExist(err) {
			r.add("schemas "+dir, nil, false)
			return
		}
		r.add("schemas "+dir, err, false)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// In request directories only *.yaml files are schema-bound —
		// lock residue from older versions (.create.lock, IN-NNNN.lock)
		// and other runtime files must not be validated as requests.
		if yamlOnly && !strings.HasSuffix(entry.Name(), ".yaml") {
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
	missing, changed, unlisted, stale, err := assets.CheckLock(base)
	if err != nil {
		r.add("asset lock", err, false)
		return
	}
	if remote := assets.RemoteFiles(base); len(remote) > 0 {
		kept := unlisted[:0]
		for _, rel := range unlisted {
			if !remote[rel] {
				kept = append(kept, rel)
			}
		}
		unlisted = kept
	}
	if len(missing) > 0 {
		r.add("asset lock", fmt.Errorf("missing asset files: %v", missing), true)
		return
	}
	r.add("asset lock", nil, false)
	if len(changed) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("locally modified assets (drift): %v", changed))
	}
	if len(unlisted) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("local extension files not in lock: %v", unlisted))
	}
	if len(stale) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("assets no longer shipped by this binary (init --repair --sync removes unmodified copies; locally modified files are kept): %v", stale))
	}
}

// checkAssetModes verifies materialized assets carry the modes the canonical
// tree recorded: embed cannot store the executable bit, so projects synced
// by older binaries lose script executability. Skipped on Windows, where
// file mode bits are synthetic and no exec bit exists to lose.
func (r *DoctorReport) checkAssetModes(base string) {
	if runtime.GOOS == "windows" {
		return
	}
	files, err := assets.List("")
	if err != nil {
		r.add("asset modes", err, false)
		return
	}
	var lost, extra []string
	for _, rel := range files {
		target := filepath.Join(base, filepath.FromSlash(rel))
		info, err := os.Lstat(target)
		if err != nil {
			continue // missing files are the asset lock check's finding
		}
		// Never report on or through a symlinked asset path — confinement
		// owns that problem.
		if !info.Mode().IsRegular() {
			continue
		}
		exec := info.Mode().Perm()&0o111 != 0
		switch {
		case assets.IsExecutable(rel) && !exec:
			lost = append(lost, rel)
		case !assets.IsExecutable(rel) && exec:
			extra = append(extra, rel)
		}
	}
	if len(lost) > 0 {
		sort.Strings(lost)
		r.add("asset modes", fmt.Errorf("lost executable bit (init --repair --sync restores): %v", lost), true)
	} else {
		r.add("asset modes", nil, false)
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		r.Warnings = append(r.Warnings, fmt.Sprintf("unexpected executable bit on assets (init --repair --sync resets): %v", extra))
	}
}

// checkIntegration warns when no harness discovery wiring is present: a bare
// init leaves .ox-agent invisible to agents that only scan AGENTS.md or
// .agents/skills, so doctor nudges toward `ox-agent integrate`.
func (r *DoctorReport) checkIntegration(root string) {
	wired := false
	if data, err := os.ReadFile(filepath.Join(root, "AGENTS.md")); err == nil && strings.Contains(string(data), markerBegin) {
		wired = true
	}
	// Only the managed link counts: a symlink at .agents/skills must resolve
	// to this project's .ox-agent/skills. Foreign dirs or dangling links
	// leave agents without a path to the materialized skills.
	link := filepath.Join(root, ".agents", "skills")
	if target, err := os.Readlink(link); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(link), target)
		}
		if filepath.Clean(target) == filepath.Join(root, ".ox-agent", "skills") {
			if _, err := os.Stat(link); err == nil {
				wired = true
			}
		}
	}
	if !wired {
		r.Warnings = append(r.Warnings, "no harness integration detected; run `ox-agent integrate` so agents discover the materialized skills")
	}
}

// checkSourceControl nudges projects toward committing the .ox-agent trail:
// work artifacts and promoted specs are the repo's spec record, so a git
// rule covering the state dirs is a finding and a wholly untracked tree a
// warning. Skipped entirely when git is unavailable.
func (r *DoctorReport) checkSourceControl(root string) {
	git, err := exec.LookPath("git")
	if err != nil {
		return
	}
	inside, err := exec.Command(git, "-C", root, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(inside)) != "true" {
		r.Warnings = append(r.Warnings, "not inside a git work tree — .ox-agent has no source-control trail; commit it so work artifacts and specs travel with the project")
		return
	}
	probes := []string{".ox-agent", ".ox-agent/work", ".ox-agent/state", ".ox-agent/state/events.ndjson", ".ox-agent/discussions", ".ox-agent/context"}
	ignored, err := exec.Command(git, append([]string{"-C", root, "check-ignore", "--"}, probes...)...).Output()
	if err == nil && len(strings.TrimSpace(string(ignored))) > 0 {
		paths := strings.Split(strings.TrimSpace(string(ignored)), "\n")
		r.add("source control", fmt.Errorf("git rules ignore .ox-agent paths: %v — the spec trail must be committed", paths), false)
		return
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() > 1 {
		return // check-ignore itself failed; nothing reliable to report
	}
	tracked, err := exec.Command(git, "-C", root, "ls-files", "--", ".ox-agent").Output()
	if err != nil {
		return
	}
	r.add("source control", nil, false)
	if len(strings.TrimSpace(string(tracked))) == 0 {
		r.Warnings = append(r.Warnings, "no .ox-agent files are tracked by git — commit them so work artifacts, specs, and the event journal travel with the project")
	}
}

// report_add_err records a check failure without a fixable flag.
func report_add_err(r *DoctorReport, name string, err error, fixable bool) {
	r.add(name, err, fixable)
}

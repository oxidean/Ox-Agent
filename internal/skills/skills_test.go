package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/oxidean/Ox-Agent/internal/project"
)

// initProject materializes a real project scaffold in a temp dir.
func initProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := project.Init(root); err != nil {
		t.Fatalf("init: %v", err)
	}
	return root
}

// TestListClassifiesSkills verifies embedded skills read as current and a
// directory outside the distribution reads as a project-local skill.
func TestListClassifiesSkills(t *testing.T) {
	root := initProject(t)
	projectSkill := filepath.Join(project.Base(root), "skills", "my-skill")
	if err := os.MkdirAll(projectSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectSkill, "skill.yaml"), []byte(
		"schema_version: 1\nname: local/my-skill\nversion: 0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byDir := map[string]*Skill{}
	for _, skill := range list {
		byDir[skill.Dir] = skill
	}
	if byDir["specify"].Status != StatusCurrent {
		t.Fatalf("specify status = %q, want current", byDir["specify"].Status)
	}
	got := byDir["my-skill"]
	if got == nil || got.Status != StatusProject {
		t.Fatalf("my-skill = %+v, want status project", got)
	}
	if got.Enabled {
		t.Fatal("project skill reported enabled without config entry")
	}
}

// TestUpdateRestoresDrift verifies update replaces modified and missing
// embedded files only with --yes, refreshes the lock, and never reconciles
// project-local files.
func TestUpdateRestoresDrift(t *testing.T) {
	root := initProject(t)
	base := project.Base(root)
	skillDoc := filepath.Join(base, "skills", "specify", "SKILL.md")
	if err := os.WriteFile(skillDoc, []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "skills", "adr", "SKILL.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(base, "skills", "specify", "custom-note.txt")
	if err := os.WriteFile(custom, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Update(root, false)
	if err != nil {
		t.Fatalf("Update(dry): %v", err)
	}
	if len(report.Restored) != 1 || len(report.Skipped) != 1 || len(report.Replaced) != 0 {
		t.Fatalf("dry report = %+v", report)
	}
	if _, err := os.Stat(missing); err != nil {
		t.Fatalf("missing file not restored: %v", err)
	}
	if data, _ := os.ReadFile(skillDoc); string(data) != "local edit\n" {
		t.Fatal("dry run overwrote drifted file")
	}

	report, err = Update(root, true)
	if err != nil {
		t.Fatalf("Update(--yes): %v", err)
	}
	if len(report.Replaced) != 1 || len(report.Skipped) != 0 {
		t.Fatalf("--yes report = %+v", report)
	}
	want, _ := assets.ReadFile("skills/specify/SKILL.md")
	got, _ := os.ReadFile(skillDoc)
	if string(got) != string(want) {
		t.Fatal("--yes did not restore embedded SKILL.md")
	}
	if data, _ := os.ReadFile(custom); string(data) != "mine\n" {
		t.Fatal("update touched a project-local file")
	}
}

// TestCheckFindings verifies check reports a modified skill as a warning and
// a skill with a missing declared schema as an error.
func TestCheckFindings(t *testing.T) {
	root := initProject(t)
	findings, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, finding := range findings {
		if finding.Level == "error" {
			t.Fatalf("unexpected error on fresh project: %+v", finding)
		}
	}
	// Remove a declared schema file: check must flag it.
	base := project.Base(root)
	if err := os.Remove(filepath.Join(base, "skills", "specify", "schemas", "output.schema.json")); err != nil {
		t.Fatal(err)
	}
	found := false
	findings, err = Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, finding := range findings {
		if finding.Skill == "specify" && finding.Level == "error" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing output schema not flagged as error")
	}
}

// TestListReportsMissingManifest verifies an embedded skill directory that
// loses its skill.yaml still surfaces in List and Check instead of being
// silently skipped.
func TestListReportsMissingManifest(t *testing.T) {
	root := initProject(t)
	manifest := filepath.Join(project.Base(root), "skills", "verify", "skill.yaml")
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	list, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var verify *Skill
	for _, skill := range list {
		if skill.Dir == "verify" {
			verify = skill
		}
	}
	if verify == nil {
		t.Fatal("embedded skill missing from List after losing skill.yaml")
	}
	if verify.ManifestErr == nil {
		t.Fatal("ManifestErr not set for missing skill.yaml")
	}
	if verify.Status != StatusIncomplete {
		t.Fatalf("status = %q, want incomplete", verify.Status)
	}
	findings, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Skill == "verify" && f.Level == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no error finding for missing manifest: %+v", findings)
	}
}

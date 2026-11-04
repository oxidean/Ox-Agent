package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitDoctorStartStatus(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if err := doctor(root); err != nil {
		t.Fatalf("doctor() error = %v", err)
	}
	if err := start(root, "Add health check"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	workDir := filepath.Join(root, ".ox-agent", "work", "active", "WI-0001-add-health-check")
	if _, err := os.Stat(filepath.Join(workDir, "index.json")); err != nil {
		t.Fatalf("work item index missing: %v", err)
	}
}

func TestInitIsNonDestructive(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("first initProject() error = %v", err)
	}
	if err := initProject(root); err == nil {
		t.Fatal("second initProject() succeeded; want refusal")
	}
}

func TestInitUsesCurrentDirectoryName(t *testing.T) {
	project := filepath.Join(t.TempDir(), "alpha-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("creating project dir: %v", err)
	}
	t.Chdir(project)
	if err := initProject("."); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, ".ox-agent", "framework.yaml"))
	if err != nil {
		t.Fatalf("reading framework.yaml: %v", err)
	}
	if !strings.Contains(string(data), "name: alpha-project") {
		t.Fatalf("framework.yaml project name missing directory name: %s", data)
	}
}

func TestStartAllocatesUniqueIDsAcrossBuckets(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if err := start(root, "First item"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	work := filepath.Join(root, ".ox-agent", "work")
	if err := os.Rename(filepath.Join(work, "active", "WI-0001-first-item"), filepath.Join(work, "archived", "WI-0001-first-item")); err != nil {
		t.Fatalf("archiving work item: %v", err)
	}
	if err := start(root, "Second item"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "active", "WI-0002-second-item", "index.json")); err != nil {
		t.Fatalf("second work item index missing: %v", err)
	}
}

func TestStartSanitizesSlug(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if err := start(root, "Fix: login/redirect?"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	dir := filepath.Join(root, ".ox-agent", "work", "active", "WI-0001-fix-login-redirect")
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Fatalf("sanitized work item missing: %v", err)
	}
}

func TestStartAppendsCreatedEvent(t *testing.T) {
	root := t.TempDir()
	if err := initProject(root); err != nil {
		t.Fatalf("initProject() error = %v", err)
	}
	if err := start(root, "Track events"); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".ox-agent", "state", "events.ndjson"))
	if err != nil {
		t.Fatalf("reading event journal: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("parsing event journal: %v", err)
	}
	if event["type"] != "work_item.created" || event["work_item_id"] != "WI-0001" || event["actor"] != "cli" {
		t.Fatalf("unexpected event: %v", event)
	}
}

package main

import (
	"os"
	"path/filepath"
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

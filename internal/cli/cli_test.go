package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI executes the command tree in-process with args and captures output.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	buf := new(bytes.Buffer)
	root := NewRootCmd()
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// TestVersionCommand verifies version prints the bare build version.
func TestVersionCommand(t *testing.T) {
	out, err := runCLI(t, "version")
	if err != nil {
		t.Fatalf("version error = %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("version printed empty output")
	}
}

// TestInitDoctorStartStatusFlow exercises the init → doctor → start → status happy path in-process.
func TestInitDoctorStartStatusFlow(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init error = %v", err)
	}
	out, err := runCLI(t, "doctor")
	if err != nil || !strings.Contains(out, "healthy") {
		t.Fatalf("doctor = %q, %v", out, err)
	}
	out, err = runCLI(t, "start", "Add", "health", "check")
	if err != nil || !strings.Contains(out, "WI-0001: Add health check") {
		t.Fatalf("start = %q, %v", out, err)
	}
	out, err = runCLI(t, "status")
	if err != nil || !strings.Contains(out, "WI-0001-add-health-check") {
		t.Fatalf("status = %q, %v", out, err)
	}
}

// TestCommandsWorkFromSubdirectory verifies project commands resolve the root upward.
func TestCommandsWorkFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init error = %v", err)
	}
	nested := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}
	t.Chdir(nested)
	if out, err := runCLI(t, "doctor"); err != nil || !strings.Contains(out, "healthy") {
		t.Fatalf("doctor from subdir = %q, %v", out, err)
	}
	if _, err := runCLI(t, "start", "nested item"); err != nil {
		t.Fatalf("start from subdir error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ox-agent", "work", "active", "WI-0001-nested-item")); err != nil {
		t.Fatalf("work item not created at root: %v", err)
	}
}

// TestUnknownCommand verifies an unknown subcommand errors.
func TestUnknownCommand(t *testing.T) {
	if _, err := runCLI(t, "frobnicate"); err == nil {
		t.Fatal("unknown command succeeded; want error")
	}
}

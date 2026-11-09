package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/input"
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

// TestInitIntegrateOnExisting verifies --integrate runs on an initialized
// project instead of failing the non-destructive check.
func TestInitIntegrateOnExisting(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, "init", "--path", dir); err != nil {
		t.Fatalf("init error = %v", err)
	}
	if _, err := runCLI(t, "init", "--path", dir); err == nil {
		t.Fatal("bare init on an existing project succeeded")
	}
	if _, err := runCLI(t, "init", "--path", dir, "--integrate"); err != nil {
		t.Fatalf("init --integrate on existing project error = %v", err)
	}
}

// TestUnblockGateFilesApprovalRequest verifies a human-gated blocked edge
// routes through handleGateError like the other lifecycle commands: the
// durable approval request is filed, not just the bare gate error (#53).
func TestUnblockGateFilesApprovalRequest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := runCLI(t, "start", "gated unblock")
	if err != nil || !strings.Contains(out, "WI-0001") {
		t.Fatalf("start: %q, %v", out, err)
	}
	lifePath := filepath.Join(dir, ".ox-agent", "lifecycle", "workflow.yaml")
	life, err := os.ReadFile(lifePath)
	if err != nil {
		t.Fatalf("reading lifecycle: %v", err)
	}
	normalized := strings.ReplaceAll(string(life), "\r\n", "\n")
	gated := strings.Replace(normalized,
		"  - from: blocked\n    to: blocked_from\n",
		"  - from: blocked\n    to: blocked_from\n    gate: human\n", 1)
	if gated == normalized {
		t.Fatal("lifecycle gate not applied — edge text did not match")
	}
	if err := os.WriteFile(lifePath, []byte(gated), 0o644); err != nil {
		t.Fatalf("writing lifecycle: %v", err)
	}
	if _, err := runCLI(t, "transition", "WI-0001", "blocked"); err != nil {
		t.Fatalf("transition to blocked: %v", err)
	}
	_, err = runCLI(t, "unblock", "WI-0001")
	if err == nil {
		t.Fatal("unblock succeeded despite human-gated edge; want approval error")
	}
	if !strings.Contains(err.Error(), "approval") && !strings.Contains(err.Error(), "filed") {
		t.Fatalf("unblock error = %v, want filed approval request", err)
	}
	req, err := input.FindPendingAction(dir, "WI-0001", "transition", "intake")
	if err != nil {
		t.Fatalf("FindPendingAction() error = %v", err)
	}
	if req == nil {
		t.Fatal("no approval request filed for the gated unblock")
	}
}

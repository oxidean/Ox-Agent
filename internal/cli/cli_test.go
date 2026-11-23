package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/input"
	"github.com/oxidean/Ox-Agent/internal/state"
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

func TestSpecStatusReplaysFindingsAndPendingDecisions(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := runCLI(t, "start", "spec status"); err != nil {
		t.Fatalf("start: %v", err)
	}
	appendEvent := func(eventType string, data map[string]any) {
		t.Helper()
		if err := state.Append(root, state.Event{
			Type:       eventType,
			WorkItemID: "WI-0001",
			Actor:      "test",
			Data:       data,
		}); err != nil {
			t.Fatalf("append %s: %v", eventType, err)
		}
	}
	appendEvent("spec.drift", map[string]any{
		"run_id":      "run-1",
		"skill":       "ox-agent/implementation",
		"enforcement": "strict",
		"findings": []any{
			map[string]any{
				"kind":    "unacknowledged",
				"spec":    "context/specs/alpha.md",
				"summary": "alpha drift",
				"paths":   []any{"internal/alpha.go"},
			},
			map[string]any{
				"kind":    "declared",
				"spec":    "context/specs/beta.md",
				"summary": "beta drift",
			},
			map[string]any{
				"kind":    "declared",
				"summary": "unscoped drift",
			},
		},
	})
	appendEvent("input.requested", map[string]any{
		"request_id": "IN-0001",
		"kind":       "decision",
		"question":   "spec drift in WI-0001: resolve alpha drift",
	})
	appendEvent("input.requested", map[string]any{
		"request_id": "IN-0002",
		"kind":       "decision",
		"question":   "spec drift in WI-0001: answered drift",
	})
	appendEvent("input.answered", map[string]any{"request_id": "IN-0002", "outcome": "answered"})
	appendEvent("input.requested", map[string]any{
		"request_id": "IN-0003",
		"kind":       "decision",
		"question":   "spec drift in WI-0001: expired drift",
	})
	appendEvent("input.expired", map[string]any{"request_id": "IN-0003"})
	appendEvent("input.requested", map[string]any{
		"request_id": "IN-0004",
		"kind":       "decision",
		"question":   "spec drift in WI-0001: cancelled drift",
	})
	appendEvent("input.cancelled", map[string]any{"request_id": "IN-0004"})

	before, err := os.ReadFile(filepath.Join(root, ".ox-agent", "state", "events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "spec", "status", "WI-0001")
	if err != nil {
		t.Fatalf("spec status: %v", err)
	}
	for _, want := range []string{
		"latest: spec.drift",
		"run-1",
		"context/specs/alpha.md",
		"internal/alpha.go",
		"context/specs/beta.md",
		"unscoped drift",
		"IN-0001",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("spec status missing %q:\n%s", want, out)
		}
	}
	for _, absent := range []string{"IN-0002", "IN-0003", "IN-0004", "answered drift", "expired drift", "cancelled drift"} {
		if strings.Contains(out, absent) {
			t.Errorf("spec status unexpectedly contains %q:\n%s", absent, out)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, ".ox-agent", "state", "events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("spec status modified the event journal")
	}

	appendEvent("spec.checked", map[string]any{
		"run_id":      "run-2",
		"skill":       "ox-agent/verify",
		"enforcement": "strict",
		"consulted":   []any{"context/specs/alpha.md"},
	})
	out, err = runCLI(t, "spec", "status", "WI-0001")
	if err != nil {
		t.Fatalf("spec status after clean check: %v", err)
	}
	if !strings.Contains(out, "latest: spec.checked") || !strings.Contains(out, "context/specs/alpha.md") {
		t.Fatalf("spec status missing latest check:\n%s", out)
	}
	for _, want := range []string{"context/specs/beta.md", "IN-0001"} {
		if !strings.Contains(out, want) {
			t.Errorf("spec status after clean check missing still-open %q:\n%s", want, out)
		}
	}
	for _, absent := range []string{"unacknowledged context/specs/alpha.md: alpha drift", "unscoped drift"} {
		if strings.Contains(out, absent) {
			t.Fatalf("clean check did not clear %q:\n%s", absent, out)
		}
	}
}

func TestSpecStatusListsOnlyActiveItemsWithSpecActivity(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, title := range []string{"with spec activity", "without spec activity"} {
		if _, err := runCLI(t, "start", title); err != nil {
			t.Fatalf("start %q: %v", title, err)
		}
	}
	if err := state.Append(root, state.Event{
		Type:       "spec.checked",
		WorkItemID: "WI-0001",
		Actor:      "test",
		Data:       map[string]any{"run_id": "run-1", "skill": "ox-agent/verify", "consulted": []any{"context/specs/README.md"}},
	}); err != nil {
		t.Fatalf("append spec check: %v", err)
	}
	out, err := runCLI(t, "spec", "status")
	if err != nil {
		t.Fatalf("spec status: %v", err)
	}
	if !strings.Contains(out, "WI-0001") || strings.Contains(out, "WI-0002") {
		t.Fatalf("spec status item filtering = %q", out)
	}
	out, err = runCLI(t, "spec", "status", "WI-0002")
	if err != nil || !strings.Contains(out, "no spec activity recorded") {
		t.Fatalf("empty spec status = %q, %v", out, err)
	}
	if _, err := runCLI(t, "spec", "status", "WI-9999"); err == nil {
		t.Fatal("unknown work item succeeded")
	}
}

func TestSpecStatusReturnsMalformedJournalError(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	if err := os.WriteFile(journal, []byte("{broken\n"), 0o644); err != nil {
		t.Fatalf("write malformed journal: %v", err)
	}
	if _, err := runCLI(t, "spec", "status"); err == nil || !strings.Contains(err.Error(), "event journal line 1") {
		t.Fatalf("spec status error = %v, want malformed journal error", err)
	}
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubUpdateSeams replaces the network-bound self-update and the re-exec'd
// repair with test doubles, returning a recorder for repairSync calls.
func stubUpdateSeams(t *testing.T, updated bool, version string, updateErr, syncErr error) *[]string {
	t.Helper()
	calls := &[]string{}
	oldUpdate, oldSync := updateBinary, repairSync
	updateBinary = func(_ context.Context, _ string) (bool, string, error) {
		return updated, version, updateErr
	}
	repairSync = func(_ context.Context, root string, _, _ io.Writer) error {
		*calls = append(*calls, root)
		return syncErr
	}
	t.Cleanup(func() { updateBinary, repairSync = oldUpdate, oldSync })
	return calls
}

// TestUpdateSyncsProjectAssets verifies update re-execs the new binary's
// repair-sync when run inside a project.
func TestUpdateSyncsProjectAssets(t *testing.T) {
	calls := stubUpdateSeams(t, true, "v0.2.0", nil, nil)
	dir := t.TempDir()
	t.Chdir(dir)
	// Find resolves the root from the working directory, which reports the
	// filesystem's real path (long names on Windows, not 8.3 aliases).
	if resolved, err := os.Getwd(); err == nil {
		dir = resolved
	}
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := runCLI(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != dir {
		t.Fatalf("repairSync calls = %v, want [%s]", *calls, dir)
	}
	if !strings.Contains(out, "updated to v0.2.0") || !strings.Contains(out, "syncing") {
		t.Fatalf("update output = %q", out)
	}
}

// TestUpdateBinaryOnlySkipsSync verifies --binary-only leaves project assets
// alone even inside a project.
func TestUpdateBinaryOnlySkipsSync(t *testing.T) {
	calls := stubUpdateSeams(t, true, "v0.2.0", nil, nil)
	t.Chdir(t.TempDir())
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := runCLI(t, "update", "--binary-only")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("repairSync called with --binary-only: %v", *calls)
	}
	if strings.Contains(out, "syncing") {
		t.Fatalf("sync ran under --binary-only: %q", out)
	}
}

// TestUpdateOutsideProjectSkipsSync verifies update works as a global
// binary update when no project is found.
func TestUpdateOutsideProjectSkipsSync(t *testing.T) {
	calls := stubUpdateSeams(t, true, "v0.2.0", nil, nil)
	t.Chdir(t.TempDir())
	out, err := runCLI(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("repairSync called outside a project: %v", *calls)
	}
	if !strings.Contains(out, "asset sync skipped") {
		t.Fatalf("output = %q", out)
	}
}

// TestUpdateAlreadyCurrentStillSyncs verifies an at-version binary still
// reconciles drifted project assets.
func TestUpdateAlreadyCurrentStillSyncs(t *testing.T) {
	calls := stubUpdateSeams(t, false, "v0.1.0", nil, nil)
	t.Chdir(t.TempDir())
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := runCLI(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(out, "already at v0.1.0") {
		t.Fatalf("output = %q", out)
	}
	if len(*calls) != 1 {
		t.Fatalf("repairSync calls = %v, want 1", *calls)
	}
}

// TestUpdateSyncFailureReportsBinarySuccess verifies a failed asset sync
// reports the error while making clear the binary update itself succeeded.
func TestUpdateSyncFailureReportsBinarySuccess(t *testing.T) {
	stubUpdateSeams(t, true, "v0.2.0", nil, errors.New("boom"))
	t.Chdir(t.TempDir())
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := runCLI(t, "update")
	if err == nil {
		t.Fatal("update succeeded despite sync failure")
	}
	if !strings.Contains(fmt.Sprintf("%v", err), "binary update succeeded") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(out, "updated to v0.2.0") {
		t.Fatalf("output = %q", out)
	}
}

// TestUpdateFailureSkipsSync verifies a failed binary update never touches
// project assets.
func TestUpdateFailureSkipsSync(t *testing.T) {
	calls := stubUpdateSeams(t, false, "", errors.New("network down"), nil)
	t.Chdir(t.TempDir())
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := runCLI(t, "update"); err == nil {
		t.Fatal("update succeeded despite binary update failure")
	}
	if len(*calls) != 0 {
		t.Fatalf("repairSync called after failed update: %v", *calls)
	}
}

// TestUpdateSyncTargetsProjectRoot verifies repairSync receives the project
// root, not the invocation directory, when run from a subdirectory.
func TestUpdateSyncTargetsProjectRoot(t *testing.T) {
	calls := stubUpdateSeams(t, true, "v0.2.0", nil, nil)
	dir := t.TempDir()
	t.Chdir(dir)
	// Find resolves the root from the working directory, which reports the
	// filesystem's real path (long names on Windows, not 8.3 aliases).
	if resolved, err := os.Getwd(); err == nil {
		dir = resolved
	}
	if _, err := runCLI(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(sub)
	if _, err := runCLI(t, "update"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != dir {
		t.Fatalf("repairSync calls = %v, want [%s]", *calls, dir)
	}
}

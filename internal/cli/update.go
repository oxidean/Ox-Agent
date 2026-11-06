package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/oxidean/Ox-Agent/internal/buildinfo"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/selfupdate"
	"github.com/spf13/cobra"
)

// updateBinary and repairSync are seams so tests can stub the network-bound
// self-update and the re-exec'd repair.
var updateBinary = selfupdate.Update

// repairSync re-executes the freshly installed binary so project assets are
// reconciled against the NEW embedded tree — this process still carries the
// old one in memory.
var repairSync = func(ctx context.Context, root string, stdout, stderr io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	proc := exec.CommandContext(ctx, exe, "init", "--repair", "--sync", "--path", root)
	proc.Stdout = stdout
	proc.Stderr = stderr
	return proc.Run()
}

// newUpdateCmd builds the self-update command.
func newUpdateCmd() *cobra.Command {
	var checkOnly, binaryOnly bool
	var version string
	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Update the ox-agent binary (and project assets, when run inside one)",
		Args:    cobra.NoArgs,
		GroupID: "admin",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if checkOnly {
				latest, err := selfupdate.Check(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "installed: %s\nlatest:    %s\n", buildinfo.Version, latest)
				return nil
			}
			updated, resolved, err := updateBinary(cmd.Context(), version)
			if err != nil {
				return err
			}
			if !updated {
				fmt.Fprintf(out, "already at %s\n", resolved)
			} else {
				fmt.Fprintf(out, "updated to %s\n", resolved)
			}
			if binaryOnly {
				return nil
			}
			root, err := project.Find(".")
			if err != nil {
				if errors.Is(err, project.ErrNotFound) {
					fmt.Fprintln(out, "no .ox-agent project found; asset sync skipped")
					return nil
				}
				return fmt.Errorf("finding project: %w (binary update succeeded; asset sync skipped)", err)
			}
			fmt.Fprintln(out, "syncing ox-agent-owned project assets:")
			if err := repairSync(cmd.Context(), root, out, cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("syncing project assets: %w (binary update succeeded; retry with 'ox-agent init --repair --sync')", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "report the latest release without updating")
	cmd.Flags().StringVar(&version, "version", "", "install a specific release tag")
	cmd.Flags().BoolVar(&binaryOnly, "binary-only", false, "update the binary without syncing project assets")
	return cmd
}

// newUninstallCmd builds the uninstall command.
func newUninstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "uninstall",
		Short:   "Remove the ox-agent binary",
		Args:    cobra.NoArgs,
		GroupID: "admin",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("pass --yes to remove the installed binary")
			}
			removed, err := selfupdate.Uninstall()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", removed)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm removal of the binary")
	return cmd
}

package cli

import (
	"fmt"

	"github.com/oxidean/Ox-Agent/internal/buildinfo"
	"github.com/oxidean/Ox-Agent/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newUpdateCmd builds the self-update command.
func newUpdateCmd() *cobra.Command {
	var checkOnly bool
	var version string
	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Update the ox-agent binary to a published release",
		Args:    cobra.NoArgs,
		GroupID: "admin",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if checkOnly {
				latest, err := selfupdate.Check()
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "installed: %s\nlatest:    %s\n", buildinfo.Version, latest)
				return nil
			}
			updated, resolved, err := selfupdate.Update(version)
			if err != nil {
				return err
			}
			if !updated {
				fmt.Fprintf(out, "already at %s\n", resolved)
				return nil
			}
			fmt.Fprintf(out, "updated to %s\n", resolved)
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "report the latest release without updating")
	cmd.Flags().StringVar(&version, "version", "", "install a specific release tag")
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

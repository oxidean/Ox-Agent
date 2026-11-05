package cli

import (
	"fmt"
	"path/filepath"

	"github.com/oxidean/Ox-Agent/internal/buildinfo"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/spf13/cobra"
)

// newInitCmd builds the init command, which materializes .ox-agent/.
func newInitCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Initialize .ox-agent/ in a repository",
		Args:    cobra.NoArgs,
		GroupID: "project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := project.Init(path); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s\n", filepath.Join(path, ".ox-agent"))
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", ".", "directory to initialize")
	return cmd
}

// newDoctorCmd builds the doctor command, which validates the project layout.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Validate the local Ox-Agent setup",
		Args:    cobra.NoArgs,
		GroupID: "project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			if err := project.Doctor(root); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Ox-Agent doctor: healthy (binary %s)\n", buildinfo.Version)
			return nil
		},
	}
}

// newVersionCmd builds the version command, printing the bare version.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the installed binary version",
		Args:    cobra.NoArgs,
		GroupID: "project",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Version)
		},
	}
}

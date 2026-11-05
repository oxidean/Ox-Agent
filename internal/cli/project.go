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
	var repair bool
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Initialize .ox-agent/ in a repository",
		Args:    cobra.NoArgs,
		GroupID: "project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if repair {
				report, err := project.Repair(path)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				for _, dir := range report.DirsCreated {
					fmt.Fprintf(out, "created dir: %s\n", dir)
				}
				for _, file := range report.FilesRestored {
					fmt.Fprintf(out, "restored asset: %s\n", file)
				}
				for _, file := range report.SeedsWritten {
					fmt.Fprintf(out, "wrote seed: %s\n", file)
				}
				if len(report.DirsCreated)+len(report.FilesRestored)+len(report.SeedsWritten) == 0 {
					fmt.Fprintln(out, "Nothing to repair")
				}
				return nil
			}
			if err := project.Init(path); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s\n", filepath.Join(path, ".ox-agent"))
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", ".", "directory to initialize")
	cmd.Flags().BoolVar(&repair, "repair", false, "restore missing scaffold pieces in an existing project")
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

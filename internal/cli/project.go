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
	var repair, integrate bool
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
			if integrate {
				report, err := project.Integrate(path)
				if err != nil {
					return err
				}
				printIntegrateReport(cmd, report)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", ".", "directory to initialize")
	cmd.Flags().BoolVar(&repair, "repair", false, "restore missing scaffold pieces in an existing project")
	cmd.Flags().BoolVar(&integrate, "integrate", false, "wire AGENTS.md and .agents/skills discovery into the project")
	return cmd
}

// newDoctorCmd builds the doctor command, reporting every project finding.
func newDoctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:     "doctor",
		Short:   "Validate the local Ox-Agent setup",
		Args:    cobra.NoArgs,
		GroupID: "project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			report := project.Doctor(root)
			if fix && !report.Healthy() {
				if _, err := project.Repair(root); err != nil {
					return err
				}
				fmt.Fprintln(out, "repair applied; re-checking")
				report = project.Doctor(root)
			}
			for _, check := range report.Checks {
				if check.OK {
					fmt.Fprintf(out, "ok   %s\n", check.Name)
				} else {
					suffix := ""
					if check.Fixable {
						suffix = " (fixable with --fix)"
					}
					fmt.Fprintf(out, "FAIL %s: %s%s\n", check.Name, check.Detail, suffix)
				}
			}
			for _, warning := range report.Warnings {
				fmt.Fprintf(out, "warn %s\n", warning)
			}
			if !report.Healthy() {
				return fmt.Errorf("doctor found %d issue(s)", countFailures(report))
			}
			fmt.Fprintf(out, "Ox-Agent doctor: healthy (binary %s)\n", buildinfo.Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "repair fixable findings, then re-check")
	return cmd
}

// countFailures tallies failed checks in a doctor report.
func countFailures(report *project.DoctorReport) int {
	failures := 0
	for _, check := range report.Checks {
		if !check.OK {
			failures++
		}
	}
	return failures
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

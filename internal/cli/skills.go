package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/skills"
	"github.com/spf13/cobra"
)

// newSkillsCmd builds the skills command group: list, check, update.
func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "skills",
		Short:   "Inspect and maintain the materialized skill set",
		GroupID: "project",
	}
	cmd.AddCommand(newSkillsListCmd(), newSkillsCheckCmd(), newSkillsUpdateCmd())
	return cmd
}

// newSkillsListCmd lists every materialized skill with its enabled flag and
// drift status against the embedded distribution.
func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List materialized skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			skillList, err := skills.List(root)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "DIR\tVERSION\tENABLED\tSTATUS\tCOMPAT")
			for _, skill := range skillList {
				on := "no"
				if skill.Enabled {
					on = "yes"
				}
				version := skill.Version
				if version == "" {
					version = "-"
				}
				compat := "ok"
				if !skill.CompatOK && skill.Compat != "" {
					compat = "needs " + skill.Compat
				}
				status := string(skill.Status)
				if skill.ManifestErr != nil {
					status = "invalid"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", skill.Dir, version, on, status, compat)
			}
			return w.Flush()
		},
	}
}

// newSkillsCheckCmd validates every materialized skill against its contract:
// manifest schema, declared files, eval suite, and binary compatibility.
func newSkillsCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Validate materialized skills against their contracts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			findings, err := skills.Check(root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			errors := 0
			for _, finding := range findings {
				fmt.Fprintf(out, "%-8s %-20s %s\n", finding.Level, finding.Skill, finding.Message)
				if finding.Level == "error" {
					errors++
				}
			}
			if errors > 0 {
				return fmt.Errorf("skills check found %d error(s)", errors)
			}
			if len(findings) == 0 {
				fmt.Fprintln(out, "all skills pass")
			}
			return nil
		},
	}
}

// newSkillsUpdateCmd re-materializes embedded skill files that are missing
// or drifted. Drifted files require --yes; project-local skills never change.
func newSkillsUpdateCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Restore embedded skill files that are missing or drifted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			report, err := skills.Update(root, yes)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, rel := range report.Restored {
				fmt.Fprintf(out, "restored: %s\n", rel)
			}
			for _, rel := range report.Replaced {
				fmt.Fprintf(out, "replaced: %s\n", rel)
			}
			for _, rel := range report.Skipped {
				fmt.Fprintf(out, "skipped (drifted): %s\n", rel)
			}
			if len(report.Skipped) > 0 {
				fmt.Fprintln(out, "re-run with --yes to overwrite drifted files")
			}
			if len(report.Restored)+len(report.Replaced)+len(report.Skipped) == 0 {
				fmt.Fprintln(out, "embedded skills are current")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "overwrite files that drifted from the embedded copy")
	return cmd
}

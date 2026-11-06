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
	cmd.AddCommand(newSkillsListCmd(), newSkillsCheckCmd(), newSkillsUpdateCmd(),
		newSkillsInstallCmd(), newSkillsUninstallCmd())
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
			fmt.Fprintln(w, "DIR\tVERSION\tENABLED\tSTATUS\tCOMPAT\tSOURCE")
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
				if skill.Source == "remote" {
					status = "remote"
				}
				if skill.ManifestErr != nil {
					status = "invalid"
				}
				source := skill.Source
				if skill.SourceDetail != "" {
					source = skill.SourceDetail
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", skill.Dir, version, on, status, compat, source)
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

// newSkillsInstallCmd fetches a skill package from a git source and pins it
// to a resolved commit in remote-skills.lock.
func newSkillsInstallCmd() *cobra.Command {
	var ref, path, name string
	var force bool
	cmd := &cobra.Command{
		Use:   "install <git-url>",
		Short: "Install a skill package from a remote git source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			entry, err := skills.Install(root, args[0], skills.InstallOptions{
				Ref: ref, Path: path, Name: name, Force: force,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "installed %s -> .ox-agent/skills/%s\n", entry.Name, entry.Dir)
			fmt.Fprintf(out, "  source: %s\n  pinned: %s\n", entry.Source, entry.SHA)
			fmt.Fprintln(out, "provenance recorded in .ox-agent/remote-skills.lock — review the pinned commit before trusting the skill")
			fmt.Fprintln(out, "wire it into config.yaml skills.enabled / stage_skills to use it in stages")
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag, or commit to pin (default: remote HEAD)")
	cmd.Flags().StringVar(&path, "path", "", "subdirectory of the repository containing the skill package")
	cmd.Flags().StringVar(&name, "name", "", "skill directory name override")
	cmd.Flags().BoolVar(&force, "force", false, "re-pin over an existing remote install of the same directory")
	return cmd
}

// newSkillsUninstallCmd removes a remote-installed skill and its lock entry.
func newSkillsUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <skill>",
		Short: "Remove a remote-installed skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			if err := skills.Uninstall(root, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "uninstalled %s\n", skills.NormalizeName(args[0]))
			return nil
		},
	}
}

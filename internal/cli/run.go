package cli

import (
	"errors"
	"fmt"

	"github.com/oxidean/Ox-Agent/internal/harness"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// newRunCmd builds the run command group: skill dispatch, output
// collection, and pending-run listing.
func newRunCmd() *cobra.Command {
	var stage, skill, integration string
	var budget int
	cmd := &cobra.Command{
		Use:   "run <work-item>",
		Short: "Dispatch a skill invocation for a work item",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			item, err := state.Resolve(root, args[0])
			if err != nil {
				return err
			}
			if item.State == "blocked" {
				return fmt.Errorf("%s is blocked (from %s); unblock it first", item.ID, item.BlockedFrom)
			}
			if item.CurrentStage == "intake" {
				if _, err := item.Advance(root, "ox-agent"); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: → %s\n", item.ID, item.State)
			}
			if stage == "" {
				stage = item.CurrentStage
			}
			if skill == "" {
				settings, err := project.LoadSettings(root)
				if err != nil {
					return err
				}
				skill, err = harness.NextSkill(root, item, stage, settings.Skills.StageSkills)
				if err != nil {
					return err
				}
			}
			run, err := harness.Dispatch(root, item, skill, stage, integration, budget)
			if err != nil {
				var gate *state.ApprovalRequiredError
				if errors.As(err, &gate) {
					return err
				}
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s dispatched (adapter: %s)\n", run.RunID, run.Adapter)
			fmt.Fprintf(out, "  work item: %s\n", run.WorkItemID)
			fmt.Fprintf(out, "  skill:     %s\n", run.Skill)
			if run.Adapter == "manual" {
				fmt.Fprintf(out, "  packet:    .ox-agent/%s\n", run.PacketPath)
				fmt.Fprintf(out, "  skill doc: .ox-agent/skills/%s/SKILL.md\n", skill)
				fmt.Fprintf(out, "  write result to: .ox-agent/runtime/runs/%s/output.json\n", run.RunID)
				fmt.Fprintf(out, "then collect with: ox-agent run collect %s\n", run.RunID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&stage, "stage", "", "stage override (default: work item's current stage)")
	cmd.Flags().StringVar(&skill, "skill", "", "skill override (default: first pending stage skill)")
	cmd.Flags().StringVar(&integration, "harness", "", "integration name (default: project default)")
	cmd.Flags().IntVar(&budget, "budget", 0, "token budget for the compiled packet")
	cmd.AddCommand(newRunCollectCmd(), newRunPendingCmd())
	return cmd
}

// newRunCollectCmd builds the collect command, validating a run's output.
func newRunCollectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "collect <run-id>",
		Short: "Validate and apply a completed run's output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			run, output, err := harness.Collect(root, args[0])
			out := cmd.OutOrStdout()
			if err != nil {
				if run != nil && run.Status == "failed" {
					fmt.Fprintf(out, "%s failed: %s\n", run.RunID, run.Error)
				}
				return err
			}
			fmt.Fprintf(out, "%s %s (verdict: %v)\n", run.RunID, run.Status, output["verdict"])
			return nil
		},
	}
}

// newRunPendingCmd lists runs awaiting collection.
func newRunPendingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pending",
		Short: "List runs awaiting collection",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			pending, err := harness.PendingRuns(root)
			if err != nil {
				return err
			}
			for _, id := range pending {
				fmt.Fprintf(cmd.OutOrStdout(), "pending: %s\n", id)
			}
			return nil
		},
	}
}

// newIntegrateCmd builds the integrate command for existing projects.
func newIntegrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "integrate",
		Short:   "Wire harness discovery (AGENTS.md, .agents/skills) into the project",
		Args:    cobra.NoArgs,
		GroupID: "project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			report, err := project.Integrate(root)
			if err != nil {
				return err
			}
			printIntegrateReport(cmd, report)
			return nil
		},
	}
}

// printIntegrateReport renders integration changes and warnings.
func printIntegrateReport(cmd *cobra.Command, report *project.IntegrateReport) {
	out := cmd.OutOrStdout()
	for _, path := range report.Created {
		fmt.Fprintf(out, "created: %s\n", path)
	}
	for _, path := range report.Updated {
		fmt.Fprintf(out, "updated: %s\n", path)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(out, "warn: %s\n", warning)
	}
	if len(report.Created)+len(report.Updated) == 0 && len(report.Warnings) == 0 {
		fmt.Fprintln(out, "Already integrated")
	}
}

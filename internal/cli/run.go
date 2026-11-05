package cli

import (
	"errors"
	"fmt"

	"github.com/oxidean/Ox-Agent/internal/harness"
	"github.com/oxidean/Ox-Agent/internal/pipeline"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// newRunCmd builds the run command group: skill dispatch, output
// collection, and pending-run listing.
func newRunCmd() *cobra.Command {
	var stage, skill, integration, pipelineName string
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
			if pipelineName != "" {
				return runPipeline(cmd, root, item, pipelineName, integration, budget)
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
			req := harness.DispatchRequest{Skill: skill, Stage: stage, Integration: integration, Budget: budget}
			run, err := harness.Dispatch(cmd.Context(), root, item, req)
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
	cmd.Flags().StringVar(&pipelineName, "pipeline", "", "execute the named pipeline instead of a single stage skill")
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

// runPipeline dispatches every ready step of a pipeline up to the policy's
// parallel-agent budget, or reports progress when nothing is dispatchable.
func runPipeline(cmd *cobra.Command, root string, item *state.WorkItem, name, integration string, budget int) error {
	pipe, err := pipeline.Load(root, name)
	if err != nil {
		return err
	}
	progress, err := pipeline.Progress(root, pipe, item)
	if err != nil {
		return err
	}
	policy, err := project.LoadPolicy(root)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	limit := policy.Budgets.MaxParallelAgents
	if limit <= 0 {
		limit = 1
	}
	inFlight := 0
	for _, status := range progress {
		if status.Status == "pending" {
			inFlight++
		}
	}
	dispatched, pending, waiting, done := 0, 0, 0, 0
	for _, status := range progress {
		switch status.Status {
		case "done":
			done++
		case "pending":
			pending++
			fmt.Fprintf(out, "pending:  %s (%s)\n", status.Step.ID, status.RunID)
		case "waiting":
			waiting++
			fmt.Fprintf(out, "waiting:  %s (needs %v)\n", status.Step.ID, status.Step.DependsOn)
		case "ready":
			if inFlight+dispatched < limit {
				stage := status.Step.Stage
				if stage == "" {
					stage = item.CurrentStage
				}
				run, err := harness.Dispatch(cmd.Context(), root, item, harness.DispatchRequest{
					Skill: status.Step.Skill, Stage: stage, Integration: integration,
					Pipeline: pipe.Name, StepID: status.Step.ID, Budget: budget,
				})
				if err != nil {
					return err
				}
				dispatched++
				fmt.Fprintf(out, "dispatched: %s → %s (%s)\n", status.Step.ID, run.RunID, status.Step.Skill)
			} else {
				fmt.Fprintf(out, "queued:   %s (parallel budget %d)\n", status.Step.ID, limit)
			}
		}
	}
	if dispatched == 0 && pending == 0 && done == len(progress) {
		if err := recordPipelineComplete(root, pipe, item); err != nil {
			return err
		}
		fmt.Fprintf(out, "pipeline %s complete (%d steps)\n", pipe.Name, done)
	}
	return nil
}

// recordPipelineComplete journals pipeline.completed once per work item.
func recordPipelineComplete(root string, pipe *pipeline.Pipeline, item *state.WorkItem) error {
	events, err := state.EventsFor(root, item.ID, 0)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type == "pipeline.completed" && event.Data["pipeline"] == pipe.Name {
			return nil
		}
	}
	return state.Append(root, state.Event{
		Type:       "pipeline.completed",
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data:       map[string]any{"pipeline": pipe.Name},
	})
}

// newReviewCmd builds the review shortcut — the review pipeline.
func newReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "review <work-item>",
		Short:   "Run the review pipeline for a work item",
		Args:    cobra.ExactArgs(1),
		GroupID: "work",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNamedPipeline(cmd, args[0], "review")
		},
	}
}

// newShipCmd builds the ship shortcut — the ship pipeline.
func newShipCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ship <work-item>",
		Short:   "Run the ship pipeline for a work item",
		Args:    cobra.ExactArgs(1),
		GroupID: "work",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNamedPipeline(cmd, args[0], "ship")
		},
	}
}

// runNamedPipeline resolves the work item and runs a named pipeline.
func runNamedPipeline(cmd *cobra.Command, ref, name string) error {
	root, err := project.Find(".")
	if err != nil {
		return err
	}
	item, err := state.Resolve(root, ref)
	if err != nil {
		return err
	}
	return runPipeline(cmd, root, item, name, "", 0)
}

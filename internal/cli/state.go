package cli

import (
	"encoding/json"
	"fmt"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// newStateCmd builds the state command group: journal inspection, replay,
// and explanation for work items.
func newStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Inspect and fold work-item state",
	}
	cmd.AddCommand(newStateReplayCmd(), newStateExplainCmd(), newEventsCmd())
	return cmd
}

// newEventsCmd builds the events listing command.
func newEventsCmd() *cobra.Command {
	var eventType string
	cmd := &cobra.Command{
		Use:   "events [work-item]",
		Short: "List journal events, optionally for one work item",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			events, err := state.ReadEvents(root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, event := range events {
				if len(args) == 1 && event.WorkItemID != args[0] {
					continue
				}
				if eventType != "" && event.Type != eventType {
					continue
				}
				fmt.Fprintf(out, "%s %s %s\n", event.Timestamp, event.Type, event.WorkItemID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&eventType, "type", "", "only show events of this type")
	return cmd
}

// newStateReplayCmd builds the replay command, folding the journal into a
// snapshot of the work item's current state.
func newStateReplayCmd() *cobra.Command {
	var write bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "replay <work-item>",
		Short: "Fold a work item's events into a state snapshot",
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
			snap, err := state.Replay(root, item, write)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				data, err := json.MarshalIndent(snap, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}
			fmt.Fprintf(out, "%s: state=%s stage=%s events=%d\n", snap.WorkItemID, snap.State, snap.CurrentStage, snap.Events)
			if write {
				fmt.Fprintf(out, "snapshot: .ox-agent/state/snapshots/%s.json\n", snap.WorkItemID)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "snapshot", false, "persist the snapshot under state/snapshots/")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the full snapshot as JSON")
	return cmd
}

// newStateExplainCmd builds the explain command, narrating a work item's
// journal in plain order.
func newStateExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain <work-item>",
		Short: "Narrate a work item's event history",
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
			lines, err := state.Explain(root, item)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s: %s\n", item.ID, item.Title)
			for _, line := range lines {
				fmt.Fprintf(out, "  %s\n", line)
			}
			return nil
		},
	}
}

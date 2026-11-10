package cli

import (
	"fmt"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// newStartCmd builds the start command, which creates a work item.
func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "start <title>",
		Short:   "Create a work item",
		Args:    cobra.MinimumNArgs(1),
		GroupID: "work",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			item, err := state.Start(root, strings.Join(args, " "))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", item.ID, item.Title)
			return nil
		},
	}
}

// newStatusCmd builds the status command: list active items, or show one
// work item's detail when given a WI-NNNN reference.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status [work-item]",
		Short:   "List active work items, or show one item's detail",
		Args:    cobra.MaximumNArgs(1),
		GroupID: "work",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				names, err := state.ListActive(root)
				if err != nil {
					return err
				}
				for _, name := range names {
					fmt.Fprintf(out, "active: %s\n", name)
				}
				return nil
			}
			item, err := state.Resolve(root, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "id: %s\ntitle: %s\nstate: %s\nprofile: %s\ncurrent_stage: %s\ncreated_at: %s\n",
				item.ID, item.Title, item.State, item.Profile, item.CurrentStage, item.CreatedAt)
			if item.UpdatedAt != "" {
				fmt.Fprintf(out, "updated_at: %s\n", item.UpdatedAt)
			}
			if item.BlockedFrom != "" {
				fmt.Fprintf(out, "blocked_from: %s\n", item.BlockedFrom)
			}
			events, err := state.EventsFor(root, item.ID, 5)
			if err != nil {
				return err
			}
			for _, event := range events {
				fmt.Fprintf(out, "event: %s %s\n", event.Timestamp, event.Type)
			}
			return nil
		},
	}
}

// newTransitionCmd builds the transition command, which applies a legal
// lifecycle state change to a work item.
func newTransitionCmd() *cobra.Command {
	var reason, by string
	cmd := &cobra.Command{
		Use:     "transition <work-item> <state>",
		Short:   "Move a work item to a new lifecycle state",
		Args:    cobra.ExactArgs(2),
		GroupID: "work",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			item, err := state.Resolve(root, args[0])
			if err != nil {
				return err
			}
			if err := item.Transition(root, args[1], "cli", by, reason); err != nil {
				return handleGateError(root, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: → %s\n", item.ID, args[1])
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "reason recorded on the transition event")
	cmd.Flags().StringVar(&by, "by", "", "approver name for human-gated transitions")
	return cmd
}

// newUnblockCmd builds the unblock command, which returns a blocked work
// item to the state it was blocked from.
func newUnblockCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:     "unblock <work-item>",
		Short:   "Return a blocked work item to its previous state",
		Args:    cobra.ExactArgs(1),
		GroupID: "work",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			item, err := state.Resolve(root, args[0])
			if err != nil {
				return err
			}
			if err := item.Unblock(root, "cli", reason); err != nil {
				return handleGateError(root, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: → %s\n", item.ID, item.State)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "reason recorded on the transition event")
	return cmd
}

// newArchiveCmd builds the archive command, which moves a work item to the
// archived bucket.
func newArchiveCmd() *cobra.Command {
	var reason, by string
	cmd := &cobra.Command{
		Use:     "archive <work-item>",
		Short:   "Archive a work item",
		Args:    cobra.ExactArgs(1),
		GroupID: "work",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			item, err := state.Resolve(root, args[0])
			if err != nil {
				return err
			}
			if err := item.Archive(root, "cli", by, reason); err != nil {
				return handleGateError(root, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: archived\n", item.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "reason recorded on the archive event")
	cmd.Flags().StringVar(&by, "by", "", "approver name for human-gated archives")
	return cmd
}

// newAdvanceCmd builds the advance command, moving a work item to its
// profile's next stage and applying the mapped lifecycle transition.
func newAdvanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "advance <work-item>",
		Short:   "Advance a work item to the next profile stage",
		Args:    cobra.ExactArgs(1),
		GroupID: "work",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			item, err := state.Resolve(root, args[0])
			if err != nil {
				return err
			}
			next, err := item.Advance(root, "cli")
			if err != nil {
				return handleGateError(root, err)
			}
			if next == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: profile %s complete\n", item.ID, item.Profile)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: stage → %s (state %s)\n", item.ID, next, item.State)
			return nil
		},
	}
}

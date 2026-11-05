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

// newStatusCmd builds the status command, which lists active work items.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "List active work items",
		Args:    cobra.NoArgs,
		GroupID: "work",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			names, err := state.ListActive(root)
			if err != nil {
				return err
			}
			for _, name := range names {
				fmt.Fprintf(cmd.OutOrStdout(), "active: %s\n", name)
			}
			return nil
		},
	}
}

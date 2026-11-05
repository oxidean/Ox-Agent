package cli

import (
	"fmt"

	"github.com/oxidean/Ox-Agent/internal/discussion"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// newDiscussCmd builds the discussion command group.
func newDiscussCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "discuss",
		Short:   "Open and manage durable discussion threads",
		GroupID: "work",
	}
	cmd.AddCommand(
		newDiscussOpenCmd(),
		newDiscussListCmd(),
		newDiscussShowCmd(),
		newDiscussAddCmd(),
		newDiscussSummarizeCmd(),
		newDiscussCloseCmd(),
	)
	return cmd
}

// newDiscussOpenCmd starts a thread and journals discussion.opened.
func newDiscussOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <title>",
		Short: "Open a new discussion thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			thread, err := discussion.Open(root, args[0], "agent")
			if err != nil {
				return err
			}
			if err := state.Append(root, state.Event{
				Type:  "discussion.opened",
				Actor: "ox-agent",
				Data:  map[string]any{"thread_id": thread.ThreadID, "title": args[0]},
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "opened %s: %s\n", thread.ThreadID, args[0])
			return nil
		},
	}
}

// newDiscussListCmd prints all threads.
func newDiscussListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List discussion threads",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			threads, err := discussion.List(root)
			if err != nil {
				return err
			}
			for _, t := range threads {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %-9s  %s\n", t.ThreadID, t.Status, t.Title)
			}
			return nil
		},
	}
}

// newDiscussShowCmd prints one thread's messages and summary.
func newDiscussShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <thread>",
		Short: "Show a discussion thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			thread, err := discussion.Load(root, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "thread:  %s\nstatus:  %s\nsummary: v%d\n\n", thread.ThreadID, thread.Status, thread.SummaryVersion)
			for _, m := range thread.Messages {
				fmt.Fprintf(out, "[%s] %s:\n  %s\n", m.At, m.Author, m.Body)
			}
			if thread.Conclusion != "" {
				fmt.Fprintf(out, "\nconclusion: %s\n", thread.Conclusion)
			}
			return nil
		},
	}
}

// newDiscussAddCmd appends a message and journals discussion.appended.
func newDiscussAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <thread> <message>",
		Short: "Append a message to a thread",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			thread, err := discussion.Load(root, args[0])
			if err != nil {
				return err
			}
			if err := thread.Add(root, "agent", args[1]); err != nil {
				return err
			}
			if err := state.Append(root, state.Event{
				Type:  "discussion.appended",
				Actor: "ox-agent",
				Data:  map[string]any{"thread_id": thread.ThreadID, "messages": len(thread.Messages)},
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "appended to %s (%d messages)\n", thread.ThreadID, len(thread.Messages))
			return nil
		},
	}
}

// newDiscussSummarizeCmd records a compacted summary.
func newDiscussSummarizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "summarize <thread> <conclusion>",
		Short: "Record a compacted discussion summary",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			thread, err := discussion.Load(root, args[0])
			if err != nil {
				return err
			}
			if err := thread.Summarize(root, args[1], nil, nil); err != nil {
				return err
			}
			if err := state.Append(root, state.Event{
				Type:  "discussion.summarized",
				Actor: "ox-agent",
				Data: map[string]any{
					"thread_id":       thread.ThreadID,
					"summary_version": thread.SummaryVersion,
				},
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "summarized %s (v%d)\n", thread.ThreadID, thread.SummaryVersion)
			return nil
		},
	}
}

// newDiscussCloseCmd resolves a thread and journals discussion.closed.
func newDiscussCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close <thread> [conclusion]",
		Short: "Resolve a discussion thread",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			thread, err := discussion.Load(root, args[0])
			if err != nil {
				return err
			}
			conclusion := ""
			if len(args) > 1 {
				conclusion = args[1]
			}
			if err := thread.Close(root, conclusion); err != nil {
				return err
			}
			if err := state.Append(root, state.Event{
				Type:  "discussion.closed",
				Actor: "ox-agent",
				Data:  map[string]any{"thread_id": thread.ThreadID},
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "closed %s\n", thread.ThreadID)
			return nil
		},
	}
}

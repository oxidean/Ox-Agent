// input.go builds the input command group — durable human-input requests.
// Async-chat hosts cannot block on synchronous prompts (they expire in
// seconds); requests live on disk so the human answers whenever they
// respond, and agents poll or back off instead of expiring.
package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oxidean/Ox-Agent/internal/input"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// fileApprovalRequest turns a gated-operation error into a durable
// approval request and returns an error naming the request handle so the
// caller (human or agent) knows exactly how to answer later.
func fileApprovalRequest(root string, gate *state.ApprovalRequiredError) error {
	op := gate.Op
	if op == "" {
		op = "transition"
	}
	req, err := input.Create(root, input.Request{
		Kind:       input.KindApproval,
		WorkItemID: gate.ID,
		Question:   fmt.Sprintf("Approve %s of %s (%s → %s)?", op, gate.ID, gate.From, gate.To),
		Action:     &input.Action{Type: op, WorkItemID: gate.ID, Target: gate.To},
	})
	if err != nil {
		return fmt.Errorf("%w (also failed to file an input request: %v)", gate, err)
	}
	return fmt.Errorf("%s — filed %s; respond with `ox-agent input respond %s --by <name> --approve` (or --reject)",
		gate.Error(), req.RequestID, req.RequestID)
}

// handleGateError files an approval request when err is a gate error,
// returning the enriched error; it returns err unchanged otherwise.
func handleGateError(root string, err error) error {
	var gate *state.ApprovalRequiredError
	if errors.As(err, &gate) {
		return fileApprovalRequest(root, gate)
	}
	return err
}

// newInputCmd builds the input command group.
func newInputCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "input",
		GroupID: "work",
		Short:   "Durable human-input requests (file, poll, answer)",
		Long: "Durable requests for human input. Agents in async-chat hosts must never " +
			"block on synchronous prompts — they expire before a human sees them. File a " +
			"request, end the turn, and let the human respond later; poll with `input pending` " +
			"or back off inside `input wait`.",
	}
	cmd.AddCommand(
		newInputRequestCmd(),
		newInputListCmd(),
		newInputRespondCmd(),
		newInputWaitCmd(),
		newInputCancelCmd(),
	)
	return cmd
}

// newInputRequestCmd builds input request, filing a question for a human.
func newInputRequestCmd() *cobra.Command {
	var kind, question, wiID, runID, ttl string
	var options []string
	var actionType, actionWI, actionTarget string
	cmd := &cobra.Command{
		Use:   "request",
		Short: "File a durable input request",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			req := input.Request{
				Kind:       kind,
				WorkItemID: wiID,
				RunID:      runID,
				Question:   question,
				Options:    options,
				CreatedBy:  "cli",
			}
			if ttl != "" {
				d, err := time.ParseDuration(ttl)
				if err != nil {
					return fmt.Errorf("invalid --ttl: %w", err)
				}
				req.ExpiresAt = time.Now().UTC().Add(d).Format(time.RFC3339)
			}
			if actionType != "" || actionWI != "" || actionTarget != "" {
				if actionType == "" || actionWI == "" || actionTarget == "" {
					return fmt.Errorf("--action-type, --action-work-item and --action-target must be given together")
				}
				req.Action = &input.Action{Type: actionType, WorkItemID: actionWI, Target: actionTarget}
			}
			created, err := input.Create(root, req)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s filed (%s)\n", created.RequestID, created.Kind)
			fmt.Fprintf(out, "  question: %s\n", created.Question)
			fmt.Fprintf(out, "  respond:  ox-agent input respond %s --by <name> --answer \"...\"\n", created.RequestID)
			if created.Kind == input.KindApproval {
				fmt.Fprintf(out, "  or:       ox-agent input respond %s --by <name> --approve|--reject\n", created.RequestID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", input.KindClarification, "approval | clarification | decision")
	cmd.Flags().StringVar(&question, "question", "", "the question for the human (required)")
	cmd.Flags().StringVar(&wiID, "work-item", "", "work item the request belongs to")
	cmd.Flags().StringVar(&runID, "run", "", "run the request originated from")
	cmd.Flags().StringVar(&ttl, "ttl", "", "optional lifetime (e.g. 24h) — request then shows as expired")
	cmd.Flags().StringSliceVar(&options, "option", nil, "allowed answers; required for --kind decision")
	cmd.Flags().StringVar(&actionType, "action-type", "", "pending op executed on --approve (transition|archive)")
	cmd.Flags().StringVar(&actionWI, "action-work-item", "", "work item the action applies to")
	cmd.Flags().StringVar(&actionTarget, "action-target", "", "action target (e.g. shipped)")
	_ = cmd.MarkFlagRequired("question")
	return cmd
}

// newInputListCmd builds input list, the pollable pending-request surface.
func newInputListCmd() *cobra.Command {
	var wiID, status string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"pending"},
		Short:   "List input requests (default: pending)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			all, err := input.List(root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			shown := 0
			for _, r := range all {
				if status == "" && r.Status != input.StatusPending {
					continue
				}
				if status != "" && status != "all" && r.Status != status {
					continue
				}
				if wiID != "" && r.WorkItemID != wiID {
					continue
				}
				fmt.Fprintf(out, "%s [%s] %s", r.RequestID, r.Status, r.Question)
				if r.WorkItemID != "" {
					fmt.Fprintf(out, "  (%s)", r.WorkItemID)
				}
				fmt.Fprintln(out)
				shown++
			}
			if shown == 0 {
				fmt.Fprintln(out, "no matching input requests")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&wiID, "work-item", "", "filter by work item")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (default: pending; \"all\" for every request)")
	return cmd
}

// newInputRespondCmd builds input respond, recording the human's answer.
func newInputRespondCmd() *cobra.Command {
	var by, answer string
	var approve, reject bool
	cmd := &cobra.Command{
		Use:   "respond <request-id>",
		Short: "Answer a pending input request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			if strings.TrimSpace(by) == "" {
				return fmt.Errorf("--by <name> is required")
			}
			modes := 0
			for _, set := range []bool{approve, reject, answer != ""} {
				if set {
					modes++
				}
			}
			if modes != 1 {
				return fmt.Errorf("give exactly one of --answer, --approve, --reject")
			}
			var req *input.Request
			switch {
			case approve:
				req, err = input.Approve(root, args[0], by)
			case reject:
				req, err = input.Reject(root, args[0], by, "rejected")
			default:
				req, err = input.Respond(root, args[0], by, answer)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (by %s)\n", req.RequestID, req.Status, by)
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "who is answering (required)")
	cmd.Flags().StringVar(&answer, "answer", "", "free-text or option answer")
	cmd.Flags().BoolVar(&approve, "approve", false, "approve and execute the pending action")
	cmd.Flags().BoolVar(&reject, "reject", false, "reject the pending action")
	return cmd
}

// newInputWaitCmd builds input wait, the backoff-aware poller.
func newInputWaitCmd() *cobra.Command {
	var interval, maxInterval, timeout time.Duration
	var factor float64
	cmd := &cobra.Command{
		Use:   "wait <request-id>",
		Short: "Wait for an answer with exponential backoff",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			req, err := input.Wait(cmd.Context(), root, args[0], input.WaitOptions{
				Interval:    interval,
				Factor:      factor,
				MaxInterval: maxInterval,
				Timeout:     timeout,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s", req.RequestID, req.Status)
			if req.AnsweredBy != "" {
				fmt.Fprintf(out, " (by %s)", req.AnsweredBy)
			}
			fmt.Fprintln(out)
			if req.Answer != "" {
				fmt.Fprintf(out, "  answer: %s\n", req.Answer)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Second, "first poll gap")
	cmd.Flags().Float64Var(&factor, "factor", 2, "backoff multiplier")
	cmd.Flags().DurationVar(&maxInterval, "max-interval", time.Minute, "poll-gap ceiling")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "overall cap; 0 waits until a terminal status")
	return cmd
}

// newInputCancelCmd builds input cancel, withdrawing a pending request.
func newInputCancelCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "cancel <request-id>",
		Short: "Withdraw a pending input request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			if strings.TrimSpace(by) == "" {
				return fmt.Errorf("--by <name> is required")
			}
			req, err := input.Cancel(root, args[0], by)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s cancelled\n", req.RequestID)
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "who is cancelling (required)")
	return cmd
}

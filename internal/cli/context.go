package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/oxidean/Ox-Agent/internal/appctx"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// newContextCmd builds the context command group: index maintenance and
// packet compilation.
func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Build the context index and compile work-item packets",
	}
	cmd.AddCommand(newContextIndexCmd(), newContextCompileCmd())
	return cmd
}

// newContextIndexCmd rebuilds context/index.yaml.
func newContextIndexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index",
		Short: "Rebuild the project context index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			index, err := appctx.Build(root)
			if err != nil {
				return err
			}
			added, changed, removed, err := appctx.Write(root, index)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "context index: %d files (+%d ~%d -%d)\n",
				len(index.Files), added, changed, removed)
			return nil
		},
	}
}

// newContextCompileCmd compiles a work-item packet for a skill.
func newContextCompileCmd() *cobra.Command {
	var skill, stage, output string
	var budget int
	cmd := &cobra.Command{
		Use:   "compile <work-item>",
		Short: "Compile a bounded context packet for a work item",
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
			if stage == "" {
				stage = item.CurrentStage
			}
			packet, err := appctx.Compile(root, item, skill, stage, budget, output)
			if err != nil {
				return err
			}
			data, err := packet.Bytes()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if output == "" {
				fmt.Fprintln(out, string(data))
				return nil
			}
			target := filepath.Join(root, ".ox-agent", filepath.FromSlash(output))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
				return fmt.Errorf("writing packet: %w", err)
			}
			fmt.Fprintf(out, "packet: %s\n", output)
			return nil
		},
	}
	cmd.Flags().StringVar(&skill, "skill", "", "skill the packet targets (required)")
	cmd.Flags().StringVar(&stage, "stage", "", "stage override (default: work item's current stage)")
	cmd.Flags().IntVar(&budget, "budget", 0, "token budget for the packet context")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the packet to this .ox-agent-relative path instead of stdout")
	_ = cmd.MarkFlagRequired("skill")
	return cmd
}

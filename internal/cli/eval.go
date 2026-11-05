package cli

import (
	"fmt"

	"github.com/oxidean/Ox-Agent/internal/eval"
	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// newEvalCmd builds the skill evaluation command.
func newEvalCmd() *cobra.Command {
	var emit bool
	cmd := &cobra.Command{
		Use:     "eval <skill>",
		Short:   "Score a skill's recorded eval outputs against its thresholds",
		Args:    cobra.ExactArgs(1),
		GroupID: "admin",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if emit {
				evals, name, err := eval.Load(root, args[0])
				if err != nil {
					return err
				}
				emitted, err := eval.EmitPackets(root, evals, name)
				if err != nil {
					return err
				}
				for _, path := range emitted {
					fmt.Fprintf(out, "emitted %s\n", path)
				}
				if len(emitted) == 0 {
					fmt.Fprintln(out, "all cases already have recorded outputs")
				}
				fmt.Fprintf(out, "record outputs as %s/<case-id>.output.json\n",
					".ox-agent/runtime/evals/"+name)
				return nil
			}
			report, err := eval.Score(root, args[0])
			if err != nil {
				return err
			}
			for _, r := range report.Results {
				status := "awaiting"
				switch {
				case r.HasOutput && r.SchemaOK && r.QualityOK:
					status = "pass"
				case r.HasOutput:
					status = "fail"
				}
				fmt.Fprintf(out, "%-8s %s", status, r.Case.ID)
				if r.Detail != "" {
					fmt.Fprintf(out, "  (%s)", r.Detail)
				}
				fmt.Fprintln(out)
			}
			fmt.Fprintf(out, "schema_pass_rate:  %.2f\nquality_pass_rate: %.2f\n",
				report.SchemaRate, report.QualityRate)
			if !report.Complete {
				fmt.Fprintf(out, "incomplete: %d/%d cases recorded — emit packets with --emit\n",
					report.RecordedCount, len(report.Results))
				return fmt.Errorf("eval incomplete")
			}
			if !report.SchemaPass || !report.QualityPass {
				return fmt.Errorf("eval %s below thresholds", report.Skill)
			}
			if err := state.Append(root, state.Event{
				Type:  "eval.completed",
				Actor: "ox-agent",
				Data: map[string]any{
					"skill":        report.Skill,
					"schema_rate":  report.SchemaRate,
					"quality_rate": report.QualityRate,
					"cases":        len(report.Results),
				},
			}); err != nil {
				return err
			}
			fmt.Fprintf(out, "eval %s passed\n", report.Skill)
			return nil
		},
	}
	cmd.Flags().BoolVar(&emit, "emit", false, "emit packets for cases without recorded outputs")
	return cmd
}

// Package cli wires the ox-agent command tree. Commands parse flags and
// delegate to the internal domain packages; they hold no business logic.
package cli

import (
	"context"
	"fmt"

	"github.com/oxidean/Ox-Agent/internal/buildinfo"
	"github.com/spf13/cobra"
)

// Execute runs the ox-agent command tree with ctx, printing errors to stderr.
func Execute(ctx context.Context) error {
	root := NewRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(root.ErrOrStderr(), err)
		return err
	}
	return nil
}

// NewRootCmd builds a fresh ox-agent command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ox-agent",
		Short:         "Repository-local AI-SDLC runtime",
		Long:          "ox-agent executes the skills, workflows, and pipelines defined by a repository's .ox-agent directory.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       buildinfo.Version,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddGroup(
		&cobra.Group{ID: "project", Title: "Project commands:"},
		&cobra.Group{ID: "work", Title: "Work items:"},
		&cobra.Group{ID: "admin", Title: "Administration:"},
	)
	root.AddCommand(
		newInitCmd(),
		newDoctorCmd(),
		newVersionCmd(),
		newStartCmd(),
		newStatusCmd(),
		newTransitionCmd(),
		newUnblockCmd(),
		newArchiveCmd(),
		newAdvanceCmd(),
		newStateCmd(),
		newContextCmd(),
		newRunCmd(),
		newSkillsCmd(),
		newEventsCmd(),
		newIntegrateCmd(),
		newReviewCmd(),
		newShipCmd(),
		newDiscussCmd(),
		newInputCmd(),
		newUpdateCmd(),
		newUninstallCmd(),
		newEvalCmd(),
	)
	return root
}

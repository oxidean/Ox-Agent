package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

// newEventsCmd builds the events command: a filtered view of the project
// event journal, the audit spine every other command appends to.
func newEventsCmd() *cobra.Command {
	var typePrefix string
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "events [work-item]",
		Short:   "Inspect the project event journal",
		Args:    cobra.MaximumNArgs(1),
		GroupID: "project",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := project.Find(".")
			if err != nil {
				return err
			}
			events, err := state.ReadEvents(root)
			if err != nil {
				return err
			}
			wiFilter := ""
			if len(args) == 1 {
				item, err := state.Resolve(root, args[0])
				if err != nil {
					return err
				}
				wiFilter = item.ID
			}
			out := cmd.OutOrStdout()
			for _, event := range events {
				if wiFilter != "" && event.WorkItemID != wiFilter {
					continue
				}
				if typePrefix != "" && !strings.HasPrefix(event.Type, typePrefix) {
					continue
				}
				if asJSON {
					data, err := json.Marshal(event)
					if err != nil {
						return err
					}
					fmt.Fprintln(out, string(data))
					continue
				}
				fmt.Fprintf(out, "%s  %-28s  %-8s  %-10s  %s\n",
					event.Timestamp, event.Type, event.WorkItemID, event.Actor, eventData(event.Data))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typePrefix, "type", "", "only events whose type starts with this prefix")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit raw journal entries as JSON lines")
	return cmd
}

// eventData renders an event's data map as sorted key=value pairs.
func eventData(data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := data[key]
		if text, ok := value.(string); ok {
			parts = append(parts, key+"="+text)
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			parts = append(parts, key+"=?")
			continue
		}
		parts = append(parts, key+"="+string(raw))
	}
	return strings.Join(parts, " ")
}

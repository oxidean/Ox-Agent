package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/project"
	"github.com/oxidean/Ox-Agent/internal/state"
	"github.com/spf13/cobra"
)

type specStatusFinding struct {
	Kind    string
	Spec    string
	Summary string
	Paths   []string
}

type specDecisionRequest struct {
	ID       string
	Question string
}

type specStatusReport struct {
	item     *state.WorkItem
	latest   *state.Event
	findings []specStatusFinding
	requests []specDecisionRequest
}

func newSpecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "spec",
		Short:   "Inspect spec enforcement",
		GroupID: "project",
	}
	cmd.AddCommand(newSpecStatusCmd())
	return cmd
}

func newSpecStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [work-item]",
		Short: "Show spec checks, open findings, and pending decisions",
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
			if len(args) == 1 {
				item, err := state.Resolve(root, args[0])
				if err != nil {
					return err
				}
				return renderSpecStatus(cmd.OutOrStdout(), buildSpecStatus(events, item))
			}
			names, err := state.ListActive(root)
			if err != nil {
				return err
			}
			shown := 0
			for _, name := range names {
				item, err := state.Resolve(root, name)
				if err != nil {
					return err
				}
				report := buildSpecStatus(events, item)
				if !report.hasActivity() {
					continue
				}
				if shown > 0 {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				if err := renderSpecStatus(cmd.OutOrStdout(), report); err != nil {
					return err
				}
				shown++
			}
			if shown == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no active work items with spec activity")
			}
			return nil
		},
	}
}

func buildSpecStatus(events []state.Event, item *state.WorkItem) specStatusReport {
	report := specStatusReport{item: item}
	open := make(map[string][]specStatusFinding)
	var unscoped []specStatusFinding
	pending := make(map[string]specDecisionRequest)
	decisionPrefix := "spec drift in " + item.ID + ":"
	for i := range events {
		event := &events[i]
		if event.WorkItemID != item.ID {
			continue
		}
		switch event.Type {
		case "spec.checked":
			report.latest = event
			for _, spec := range eventStrings(event.Data["consulted"]) {
				delete(open, spec)
			}
			unscoped = nil
		case "spec.drift":
			report.latest = event
			groups := make(map[string][]specStatusFinding)
			for _, finding := range eventFindings(event.Data["findings"]) {
				groups[finding.Spec] = append(groups[finding.Spec], finding)
			}
			for spec, findings := range groups {
				if spec == "" {
					unscoped = findings
				} else {
					open[spec] = findings
				}
			}
		case "input.requested":
			id, _ := event.Data["request_id"].(string)
			kind, _ := event.Data["kind"].(string)
			question, _ := event.Data["question"].(string)
			if id != "" && kind == "decision" && strings.HasPrefix(question, decisionPrefix) {
				pending[id] = specDecisionRequest{ID: id, Question: question}
			}
		case "input.answered", "input.expired", "input.cancelled":
			id, _ := event.Data["request_id"].(string)
			delete(pending, id)
		}
	}
	for _, spec := range sortedKeys(open) {
		report.findings = append(report.findings, open[spec]...)
	}
	report.findings = append(report.findings, unscoped...)
	sort.Slice(report.findings, func(i, j int) bool {
		left, right := report.findings[i], report.findings[j]
		if left.Spec != right.Spec {
			return left.Spec < right.Spec
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Summary < right.Summary
	})
	for i := range report.findings {
		sort.Strings(report.findings[i].Paths)
	}
	for _, id := range sortedKeys(pending) {
		report.requests = append(report.requests, pending[id])
	}
	return report
}

func (report specStatusReport) hasActivity() bool {
	return report.latest != nil || len(report.findings) > 0 || len(report.requests) > 0
}

func renderSpecStatus(out io.Writer, report specStatusReport) error {
	if !report.hasActivity() {
		_, err := fmt.Fprintf(out, "%s: no spec activity recorded\n", report.item.ID)
		return err
	}
	if _, err := fmt.Fprintf(out, "%s: %s\n", report.item.ID, report.item.Title); err != nil {
		return err
	}
	if report.latest == nil {
		if _, err := fmt.Fprintln(out, "latest: none"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "latest: %s at %s\n", report.latest.Type, report.latest.Timestamp); err != nil {
			return err
		}
		for _, field := range []struct{ label, key string }{{"run", "run_id"}, {"skill", "skill"}, {"enforcement", "enforcement"}} {
			if value, ok := report.latest.Data[field.key].(string); ok && value != "" {
				if _, err := fmt.Fprintf(out, "%s: %s\n", field.label, value); err != nil {
					return err
				}
			}
		}
		if consulted := eventStrings(report.latest.Data["consulted"]); len(consulted) > 0 {
			sort.Strings(consulted)
			if _, err := fmt.Fprintf(out, "consulted: %s\n", strings.Join(consulted, ", ")); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(out, "open findings:"); err != nil {
		return err
	}
	if len(report.findings) == 0 {
		if _, err := fmt.Fprintln(out, "  none"); err != nil {
			return err
		}
	} else {
		for _, finding := range report.findings {
			label := finding.Kind
			if label == "" {
				label = "finding"
			}
			if finding.Spec != "" {
				label += " " + finding.Spec
			}
			if finding.Summary != "" {
				label += ": " + finding.Summary
			}
			if _, err := fmt.Fprintf(out, "  - %s\n", label); err != nil {
				return err
			}
			if len(finding.Paths) > 0 {
				if _, err := fmt.Fprintf(out, "    paths: %s\n", strings.Join(finding.Paths, ", ")); err != nil {
					return err
				}
			}
		}
	}
	if _, err := fmt.Fprintln(out, "pending spec decisions:"); err != nil {
		return err
	}
	if len(report.requests) == 0 {
		_, err := fmt.Fprintln(out, "  none")
		return err
	}
	for _, request := range report.requests {
		if _, err := fmt.Fprintf(out, "  - %s: %s\n", request.ID, request.Question); err != nil {
			return err
		}
	}
	return nil
}

func eventFindings(value any) []specStatusFinding {
	entries, ok := value.([]any)
	if !ok {
		return nil
	}
	var findings []specStatusFinding
	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		finding := specStatusFinding{}
		finding.Kind, _ = fields["kind"].(string)
		finding.Spec, _ = fields["spec"].(string)
		finding.Summary, _ = fields["summary"].(string)
		finding.Paths = eventStrings(fields["paths"])
		if finding.Kind != "" || finding.Spec != "" || finding.Summary != "" || len(finding.Paths) > 0 {
			findings = append(findings, finding)
		}
	}
	return findings
}

func eventStrings(value any) []string {
	var values []string
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			if text, ok := item.(string); ok && text != "" {
				values = append(values, text)
			}
		}
	case []string:
		for _, item := range items {
			if item != "" {
				values = append(values, item)
			}
		}
	}
	return values
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

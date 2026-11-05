package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// indexPath returns the work item's index.json path.
func indexPath(dir string) string {
	return filepath.Join(dir, "index.json")
}

// Load reads the work item index.json in dir into item.
func (item *WorkItem) Load(dir string) error {
	data, err := os.ReadFile(indexPath(dir))
	if err != nil {
		return err
	}
	item.Dir = dir
	return json.Unmarshal(data, item)
}

// Save writes the work item index.json in item.Dir.
func (item *WorkItem) Save() error {
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding work item: %w", err)
	}
	if err := os.WriteFile(indexPath(item.Dir), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing work item: %w", err)
	}
	return nil
}

// Resolve locates a work item by WI-NNNN id or directory name, searching the
// active bucket first, then archived. Ambiguous or absent ids error.
func Resolve(root, ref string) (*WorkItem, error) {
	work := workRoot(root)
	var matches []string
	for _, bucket := range []string{"active", "archived"} {
		entries, err := os.ReadDir(filepath.Join(work, bucket))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if name == ref || strings.HasPrefix(name, ref+"-") || name == ref {
				matches = append(matches, filepath.Join(work, bucket, name))
			}
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no work item matching %q", ref)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("work item %q is ambiguous: %v", ref, matches)
	}
	item := &WorkItem{}
	if err := item.Load(matches[0]); err != nil {
		return nil, fmt.Errorf("loading work item %s: %w", matches[0], err)
	}
	return item, nil
}

// bucket returns "active" or "archived" for the work item's directory.
func (item *WorkItem) bucket() string {
	return filepath.Base(filepath.Dir(item.Dir))
}

// Transition moves a work item to a new lifecycle state when the lifecycle
// file permits the edge. Gated edges require approver (--by); the approval is
// journaled with the event. The index update rolls back if journaling fails.
func (item *WorkItem) Transition(root, to, actor, approver, reason string) error {
	life, err := LoadLifecycle(root)
	if err != nil {
		return err
	}
	from := item.State
	gate, ok := life.Find(from, to)
	if !ok {
		targets := life.LegalTargets(from)
		return fmt.Errorf("transition %s → %s is not legal (from %s: %s)", from, to, from, strings.Join(targets, ", "))
	}
	if gate == "human" && approver == "" {
		return fmt.Errorf("transition %s → %s requires human approval; pass --by <name>", from, to)
	}
	if to == "blocked" {
		item.BlockedFrom = from
	} else {
		item.BlockedFrom = ""
	}
	item.State = to
	if stage, ok := stateToStage[to]; ok {
		item.CurrentStage = stage
	}
	item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	previous, err := os.ReadFile(indexPath(item.Dir))
	if err != nil {
		return err
	}
	if err := item.Save(); err != nil {
		return err
	}
	data := map[string]any{"from": from, "to": to}
	if reason != "" {
		data["reason"] = reason
	}
	if gate != "" {
		data["gate"] = gate
		data["approved_by"] = approver
	}
	if err := Append(root, Event{
		Type:       "work_item.transitioned",
		WorkItemID: item.ID,
		Actor:      actor,
		Data:       data,
	}); err != nil {
		_ = os.WriteFile(indexPath(item.Dir), previous, 0o644)
		return err
	}
	return nil
}

// Unblock returns a blocked work item to the state it was blocked from.
func (item *WorkItem) Unblock(root, actor, reason string) error {
	if item.State != "blocked" {
		return fmt.Errorf("%s is %s, not blocked", item.ID, item.State)
	}
	if item.BlockedFrom == "" {
		return errors.New("work item has no recorded blocked_from state")
	}
	return item.Transition(root, item.BlockedFrom, actor, "", reason)
}

// Archive moves a work item to the archived bucket and records the event.
// Both the move and the index update roll back if journaling fails.
func (item *WorkItem) Archive(root, actor, approver, reason string) error {
	if item.bucket() != "active" {
		return fmt.Errorf("%s is already archived", item.ID)
	}
	life, err := LoadLifecycle(root)
	if err != nil {
		return err
	}
	gate, ok := life.Find(item.State, "archived")
	if !ok {
		return fmt.Errorf("%s cannot be archived from state %s", item.ID, item.State)
	}
	if gate == "human" && approver == "" {
		return fmt.Errorf("archiving %s requires human approval; pass --by <name>", item.ID)
	}
	from := item.State
	item.State = "archived"
	item.BlockedFrom = ""
	item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	previous, err := os.ReadFile(indexPath(item.Dir))
	if err != nil {
		return err
	}
	if err := item.Save(); err != nil {
		return err
	}
	target := filepath.Join(workRoot(root), "archived", filepath.Base(item.Dir))
	if err := os.Rename(item.Dir, target); err != nil {
		_ = os.WriteFile(indexPath(item.Dir), previous, 0o644)
		return fmt.Errorf("archiving work item: %w", err)
	}
	item.Dir = target
	data := map[string]any{"from": from, "to": "archived"}
	if reason != "" {
		data["reason"] = reason
	}
	if err := Append(root, Event{
		Type:       "work_item.archived",
		WorkItemID: item.ID,
		Actor:      actor,
		Data:       data,
	}); err != nil {
		_ = os.Rename(target, filepath.Join(workRoot(root), "active", filepath.Base(item.Dir)))
		item.Dir = filepath.Join(workRoot(root), "active", filepath.Base(item.Dir))
		_ = os.WriteFile(indexPath(item.Dir), previous, 0o644)
		return err
	}
	return nil
}

// EventsFor returns the journal events for one work item, oldest first.
func EventsFor(root, workItemID string, limit int) ([]Event, error) {
	events, err := ReadEvents(root)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, event := range events {
		if event.WorkItemID == workItemID {
			out = append(out, event)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

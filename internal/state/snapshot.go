package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Snapshot is a folded, point-in-time view of a work item derived by
// replaying its journal events. It mirrors snapshot.schema.json.
type Snapshot struct {
	SchemaVersion int              `json:"schema_version"`
	WorkItemID    string           `json:"work_item_id"`
	GeneratedAt   string           `json:"generated_at"`
	State         string           `json:"state"`
	CurrentStage  string           `json:"current_stage"`
	Events        int              `json:"events"`
	LastEventID   string           `json:"last_event_id,omitempty"`
	History       []map[string]any `json:"history"`
}

// snapshotPath returns the snapshot file location for a work item.
func snapshotPath(root, id string) string {
	return filepath.Join(root, ".ox-agent", "state", "snapshots", id+".json")
}

// Replay folds every journal event for the work item into a Snapshot and,
// when write is set, persists it to state/snapshots/<id>.json.
func Replay(root string, item *WorkItem, write bool) (*Snapshot, error) {
	events, err := EventsFor(root, item.ID, 0)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{
		SchemaVersion: 1,
		WorkItemID:    item.ID,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		State:         item.State,
		CurrentStage:  item.CurrentStage,
		Events:        len(events),
	}
	for _, event := range events {
		entry := map[string]any{
			"timestamp": event.Timestamp,
			"type":      event.Type,
			"actor":     event.Actor,
		}
		if len(event.Data) > 0 {
			entry["data"] = event.Data
		}
		snap.History = append(snap.History, entry)
		snap.LastEventID = event.EventID
	}
	if !write {
		return snap, nil
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(snapshotPath(root, item.ID)), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(snapshotPath(root, item.ID), append(data, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("writing snapshot: %w", err)
	}
	return snap, nil
}

// Explain narrates a work item's journal as ordered lines: timestamp, event
// type, and the meaningful fields of each event's data.
func Explain(root string, item *WorkItem) ([]string, error) {
	events, err := EventsFor(root, item.ID, 0)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, event := range events {
		line := fmt.Sprintf("%s %s", event.Timestamp, event.Type)
		if len(event.Data) > 0 {
			keys := make([]string, 0, len(event.Data))
			for k := range event.Data {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%v", k, event.Data[k]))
			}
			line += " (" + joinParts(parts) + ")"
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// joinParts renders data key=value pairs comma-separated.
func joinParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

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

// Replay folds the work item's journal events into a Snapshot and, when
// write is set, persists it to state/snapshots/<id>.json. With accel set, a
// persisted snapshot whose last_event_id still marks a verified prefix of
// the journal (the events before it are exactly the ones it folded) resumes
// the fold: stored history is reused and only newer events are appended. A
// missing snapshot, an absent marker, or a prefix count that shifted (e.g. a
// union merge inserting events ahead of the marker) falls back to a full
// fold so accelerated and full replays can never silently diverge.
func Replay(root string, item *WorkItem, write, accel bool) (*Snapshot, error) {
	events, err := EventsFor(root, item.ID, 0)
	if err != nil {
		return nil, err
	}
	var base *Snapshot
	fold := events
	if accel {
		if prior := loadSnapshot(root, item.ID); prior != nil {
			if idx := indexOfEvent(events, prior.LastEventID); idx >= 0 && idx+1 == prior.Events {
				base = prior
				fold = events[idx+1:]
			}
		}
	}
	snap := &Snapshot{
		SchemaVersion: 1,
		WorkItemID:    item.ID,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		State:         item.State,
		CurrentStage:  item.CurrentStage,
		Events:        len(events),
	}
	if base != nil {
		snap.History = append(snap.History, base.History...)
		snap.LastEventID = base.LastEventID
	}
	for _, event := range fold {
		snap.History = append(snap.History, foldEntry(event))
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

// loadSnapshot reads a persisted snapshot for the work item, returning nil
// when none exists or it cannot be parsed.
func loadSnapshot(root, id string) *Snapshot {
	data, err := os.ReadFile(snapshotPath(root, id))
	if err != nil {
		return nil
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil
	}
	return &snap
}

// indexOfEvent returns the position of the event with id in events, or -1.
func indexOfEvent(events []Event, id string) int {
	if id == "" {
		return -1
	}
	for i, event := range events {
		if event.EventID == id {
			return i
		}
	}
	return -1
}

// foldEntry renders one event as a snapshot history entry.
func foldEntry(event Event) map[string]any {
	entry := map[string]any{
		"timestamp": event.Timestamp,
		"type":      event.Type,
		"actor":     event.Actor,
	}
	if len(event.Data) > 0 {
		entry["data"] = event.Data
	}
	return entry
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

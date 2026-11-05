// Package state manages the project event journal and work-item records
// under .ox-agent/.
package state

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Event is one entry in the .ox-agent/state/events.ndjson journal.
type Event struct {
	SchemaVersion int            `json:"schema_version"`
	EventID       string         `json:"event_id"`
	Timestamp     string         `json:"timestamp"`
	Type          string         `json:"type"`
	WorkItemID    string         `json:"work_item_id,omitempty"`
	Actor         string         `json:"actor"`
	Data          map[string]any `json:"data,omitempty"`
}

// journalPath returns the project event journal path.
func journalPath(root string) string {
	return filepath.Join(root, ".ox-agent", "state", "events.ndjson")
}

// Append journals event to the project's append-only event journal,
// stamping schema_version, event_id, and timestamp.
func Append(root string, event Event) error {
	event.SchemaVersion = 1
	event.EventID = fmt.Sprintf("evt_%d_%06x", time.Now().UnixMilli(), rand.Uint64N(1<<24))
	event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encoding lifecycle event: %w", err)
	}
	file, err := os.OpenFile(journalPath(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening event journal: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("appending lifecycle event: %w", err)
	}
	return nil
}

// ReadEvents parses every entry in the project event journal in timestamp
// order. The journal merges with the union driver across branches, which can
// interleave cross-branch appends out of chronological order; readers sort by
// the recorded timestamp so replay, snapshots, and LastEventID stay correct
// regardless of file position. A missing journal reads as empty; malformed
// lines report their line number.
func ReadEvents(root string) ([]Event, error) {
	data, err := os.ReadFile(journalPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading event journal: %w", err)
	}
	var events []Event
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("event journal line %d: %w", i+1, err)
		}
		events = append(events, event)
	}
	sort.SliceStable(events, func(i, j int) bool {
		ti, errI := time.Parse(time.RFC3339, events[i].Timestamp)
		tj, errJ := time.Parse(time.RFC3339, events[j].Timestamp)
		if errI != nil || errJ != nil {
			return events[i].Timestamp < events[j].Timestamp
		}
		return ti.Before(tj)
	})
	return events, nil
}

package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadEventsOrdersByEventIDMillis verifies same-second journal entries
// order by the millisecond value in their event id, not by file position —
// union merges can interleave lines with equal timestamps.
func TestReadEventsOrdersByEventIDMillis(t *testing.T) {
	root := initProject(t)
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	var lines []byte
	write := func(ms int64, kind string) {
		lines = append(lines, []byte(fmt.Sprintf(
			`{"schema_version":1,"event_id":"evt_%d_0000ab","timestamp":"2026-01-01T00:00:00Z","type":%q,"actor":"test"}`,
			ms, kind))...)
		lines = append(lines, '\n')
	}
	write(3000, "third")
	write(1000, "first")
	write(2000, "second")
	if err := os.WriteFile(journal, lines, 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := ReadEvents(root)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	want := []string{"first", "second", "third"}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d", len(events), len(want))
	}
	for i, event := range events {
		if event.Type != want[i] {
			t.Fatalf("event %d type = %q, want %q", i, event.Type, want[i])
		}
	}
}

func TestReadEventsDeduplicatesUnionMergeEntries(t *testing.T) {
	root := initProject(t)
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	line := `{"schema_version":1,"event_id":"evt_1000_0000ab","timestamp":"2026-01-01T00:00:00Z","type":"created","actor":"test"}`
	if err := os.WriteFile(journal, []byte(line+"\n"+line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := ReadEvents(root)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 1 || events[0].EventID != "evt_1000_0000ab" {
		t.Fatalf("ReadEvents returned %#v, want one unique event", events)
	}
}

func TestReadEventsRejectsConflictingDuplicateIDs(t *testing.T) {
	root := initProject(t)
	journal := filepath.Join(root, ".ox-agent", "state", "events.ndjson")
	lines := strings.Join([]string{
		`{"schema_version":1,"event_id":"evt_1000_0000ab","timestamp":"2026-01-01T00:00:00Z","type":"created","actor":"test"}`,
		`{"schema_version":1,"event_id":"evt_1000_0000ab","timestamp":"2026-01-01T00:00:00Z","type":"archived","actor":"test"}`,
	}, "\n")
	if err := os.WriteFile(journal, []byte(lines+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvents(root); err == nil || !strings.Contains(err.Error(), "duplicate event id") {
		t.Fatalf("ReadEvents error = %v, want conflicting duplicate ID error", err)
	}
}

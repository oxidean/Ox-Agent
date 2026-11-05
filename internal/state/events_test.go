package state

import (
	"fmt"
	"os"
	"path/filepath"
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

package state

import (
	"os"
	"reflect"
	"testing"
)

// TestReplayAcceleratedMatchesFull verifies that a replay resumed from a
// persisted snapshot produces exactly the same history, event count, and
// last marker as a from-scratch fold of the same journal.
func TestReplayAcceleratedMatchesFull(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "accelerated replay")
	for _, data := range []map[string]any{
		{"k": "v1"},
		{"k": "v2", "n": 2},
		nil,
	} {
		if err := Append(root, Event{Type: "work_item.transitioned", WorkItemID: item.ID, Actor: "cli", Data: data}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	// Persist a snapshot, append more events, then replay accelerated.
	if _, err := Replay(root, item, true, false); err != nil {
		t.Fatalf("Replay(write) error = %v", err)
	}
	for _, data := range []map[string]any{
		{"k": "v4"},
		{"k": "v5"},
	} {
		if err := Append(root, Event{Type: "skill.completed", WorkItemID: item.ID, Actor: "ox-agent", Data: data}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	full, err := Replay(root, item, false, false)
	if err != nil {
		t.Fatalf("Replay(full) error = %v", err)
	}
	accel, err := Replay(root, item, false, true)
	if err != nil {
		t.Fatalf("Replay(accelerated) error = %v", err)
	}
	if !reflect.DeepEqual(full.History, accel.History) {
		t.Fatalf("accelerated history diverged\nfull:  %v\naccel: %v", full.History, accel.History)
	}
	if full.Events != accel.Events || full.LastEventID != accel.LastEventID {
		t.Fatalf("diverged: full=%d/%s accel=%d/%s", full.Events, full.LastEventID, accel.Events, accel.LastEventID)
	}
	if accel.Events != 6 || len(accel.History) != 6 {
		t.Fatalf("expected 6 folded events, got %d history %d", accel.Events, len(accel.History))
	}
}

// TestReplayAcceleratesOnlyOnce verifies replay without accel re-folds from
// event zero even when a snapshot exists.
func TestReplayAcceleratesOnlyWhenAsked(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "no accel")
	if _, err := Replay(root, item, true, false); err != nil {
		t.Fatalf("Replay(write) error = %v", err)
	}
	if err := Append(root, Event{Type: "work_item.transitioned", WorkItemID: item.ID, Actor: "cli"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	snap, err := Replay(root, item, false, false)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if snap.Events != 2 || len(snap.History) != 2 {
		t.Fatalf("non-accelerated replay folded %d events", snap.Events)
	}
}

// TestReplayFallsBackOnShiftedPrefix verifies a journal prefix that no longer
// matches the snapshot's folded count (events inserted ahead of the marker,
// as a union merge can interleave) triggers a full fold instead of silently
// resuming from a stale base.
func TestReplayFallsBackOnShiftedPrefix(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "shifted prefix")
	if err := Append(root, Event{Type: "work_item.transitioned", WorkItemID: item.ID, Actor: "cli"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if _, err := Replay(root, item, true, false); err != nil {
		t.Fatalf("Replay(write) error = %v", err)
	}
	// Rewrite the journal with a forged earlier-timestamped event inserted
	// before the snapshot marker, simulating a cross-branch union merge.
	data, err := os.ReadFile(journalPath(root))
	if err != nil {
		t.Fatalf("reading journal: %v", err)
	}
	straggler := []byte(`{"schema_version":1,"event_id":"evt_1_000001","timestamp":"2020-01-01T00:00:00Z","type":"work_item.transitioned","work_item_id":"` + item.ID + `","actor":"cli"}` + "\n")
	if err := os.WriteFile(journalPath(root), append(straggler, data...), 0o644); err != nil {
		t.Fatalf("rewriting journal: %v", err)
	}
	if err := Append(root, Event{Type: "work_item.transitioned", WorkItemID: item.ID, Actor: "cli"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	full, err := Replay(root, item, false, false)
	if err != nil {
		t.Fatalf("Replay(full) error = %v", err)
	}
	accel, err := Replay(root, item, false, true)
	if err != nil {
		t.Fatalf("Replay(accel) error = %v", err)
	}
	if !reflect.DeepEqual(full.History, accel.History) || full.Events != accel.Events {
		t.Fatalf("shifted prefix must force full fold: full=%d events accel=%d", full.Events, accel.Events)
	}
	if accel.Events != 4 {
		t.Fatalf("expected 4 events after straggler insertion, got %d", accel.Events)
	}
}

// TestReplayIgnoresMissingMarker verifies a snapshot whose last_event_id is
// absent from the journal falls back to a full fold.
func TestReplayIgnoresMissingMarker(t *testing.T) {
	root := initProject(t)
	item := startItem(t, root, "missing marker")
	if _, err := Replay(root, item, true, false); err != nil {
		t.Fatalf("Replay(write) error = %v", err)
	}
	// Corrupt the snapshot's marker so it no longer appears in the journal.
	data := []byte(`{"schema_version":1,"work_item_id":"` + item.ID + `","events":1,"last_event_id":"evt_0_deadbeef","history":[{"timestamp":"x","type":"forged","actor":"x"}]}`)
	if err := os.WriteFile(snapshotPath(root, item.ID), data, 0o644); err != nil {
		t.Fatalf("planting snapshot: %v", err)
	}
	got, err := Replay(root, item, false, true)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if got.Events != 1 || len(got.History) != 1 || got.History[0]["type"] == "forged" {
		t.Fatalf("missing marker must not resume: %+v", got)
	}
}

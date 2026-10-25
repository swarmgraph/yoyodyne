package runstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestEventsOfTypeKeepsOnlyTheNamedTypes(t *testing.T) {
	store := newTestStore(t)
	const runID = "run-0123456789abcdef0123456789abcdef"
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	path, err := store.eventPath(runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var log []byte
	for sequence, event := range []struct {
		eventType execution.EventType
		payload   any
	}{
		{execution.EventRunStarted, nil},
		// A payload carrying a wanted type under its own "type" key is not an
		// event of that type.
		{execution.EventAgentMessage, map[string]string{"type": "review.completed"}},
		{execution.EventReviewCompleted, map[string]any{"decision": "repair", "findings": 2}},
		{execution.EventRunCompleted, nil},
		{execution.EventReviewCompleted, map[string]any{"decision": "approve", "findings": 0}},
	} {
		recorded, err := execution.NewEvent(runID, uint64(sequence+1), at, event.eventType, "test", event.payload)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeEvent(recorded)
		if err != nil {
			t.Fatal(err)
		}
		log = append(log, encoded...)
	}
	if err := os.WriteFile(path, log, 0o600); err != nil {
		t.Fatal(err)
	}
	// A line naming a wanted type that will not decode is reported, and a line
	// of another type that will not decode is never looked at.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{\"type\":\"review.completed\", broken\n{\"type\":\"agent.message\", broken\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()

	events, skipped, err := store.EventsOfType(runID, execution.EventReviewCompleted)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 3 || events[1].Sequence != 5 {
		t.Fatalf("events are %+v, want the two review verdicts", events)
	}
	if len(skipped) != 1 || skipped[0].Line != 6 {
		t.Fatalf("skipped lines are %+v, want the one broken verdict", skipped)
	}

	if events, skipped, err := store.EventsOfType("run-ffffffffffffffffffffffffffffffff", execution.EventReviewCompleted); err != nil || events != nil || skipped != nil {
		t.Fatalf("a run with no log has %v, %v, %v", events, skipped, err)
	}
}

func TestConversationEventsOfType(t *testing.T) {
	store, err := NewConversationStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	const conversationID = "chat-0123456789abcdef0123456789abcdef"
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for sequence, eventType := range []execution.EventType{execution.EventOperatorMessage, execution.EventTrackerActionApplied, execution.EventAgentMessage, execution.EventMemoryRecorded} {
		recorded, err := execution.NewEvent(conversationID, uint64(sequence+1), at, eventType, "test", map[string]string{"n": "x"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(recorded); err != nil {
			t.Fatal(err)
		}
	}
	events, skipped, err := store.EventsOfType(conversationID, execution.EventTrackerActionApplied, execution.EventMemoryRecorded)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("read failed: %v %v", err, skipped)
	}
	if len(events) != 2 || events[0].Type != execution.EventTrackerActionApplied || events[1].Type != execution.EventMemoryRecorded {
		t.Fatalf("events are %+v", events)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), conversationID+".events.jsonl")); err != nil {
		t.Fatalf("the log is not where the store keeps it: %v", err)
	}
}

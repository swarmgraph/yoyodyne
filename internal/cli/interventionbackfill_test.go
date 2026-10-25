package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
)

func loggedEvent(t *testing.T, at time.Time, kind execution.EventType, payload map[string]any) execution.Event {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return execution.Event{Type: kind, Timestamp: at, Payload: encoded}
}

// A conversation's log does not say who sent a message, so the backfill tells
// a person's from the harness's by what the log does say: a pass's first
// message follows its start, every other message the harness sends opens with
// words of its own, and a message typed at a console says so. What is left is a
// person's, and only from the moment asked for.
func TestTheBackfillCountsOnlyAPersonsMessages(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 17, 7, 0, 0, 0, time.UTC)
	at := func(hours int) time.Time { return since.Add(time.Duration(hours) * time.Hour) }
	text := func(said string) map[string]any { return map[string]any{"text": said} }
	events := []execution.Event{
		loggedEvent(t, at(-2), execution.EventOperatorMessage, text("From Mason's assistant: before the moment asked for")),
		loggedEvent(t, at(1), execution.EventSessionPassStarted, map[string]any{}),
		loggedEvent(t, at(1), execution.EventOperatorMessage, text("Sweep the backlog for work that serves no goal.")),
		loggedEvent(t, at(2), execution.EventAgentMessage, text("done")),
		loggedEvent(t, at(3), execution.EventOperatorMessage, text("Your previous reply omitted its closing report block.")),
		loggedEvent(t, at(4), execution.EventOperatorMessage, text("Run run-1234 of yoyodyne-ifd.1 stopped: independent review still required repair.")),
		loggedEvent(t, at(5), execution.EventOperatorMessage, text("From Mason's operator assistant: start counting interventions now.")),
		loggedEvent(t, at(6), execution.EventOperatorMessage, map[string]any{"text": "The harness woke you for something I typed", "typed_at_console": true}),
	}
	found := personMessagesIn("chat-1", domain.RoleProductManager, events, since)
	if len(found) != 2 || found[0].text != "From Mason's operator assistant: start counting interventions now." || !found[1].console {
		t.Fatalf("found %+v, want the assistant's message and the one typed at the console", found)
	}
	event, err := backfilledMessage("yoyodyne", found[0], at(10))
	if err != nil {
		t.Fatal(err)
	}
	if err := event.Validate(); err != nil || event.Kind != intervention.KindMessage || !event.At.Equal(at(5)) {
		t.Fatalf("recorded %+v, %v; want a valid message at the moment it was sent", event, err)
	}
	if !alreadyRecorded([]intervention.Event{event}, found[0]) || alreadyRecorded([]intervention.Event{event}, found[1]) {
		t.Error("a message already recorded is found again, and only that one")
	}
}

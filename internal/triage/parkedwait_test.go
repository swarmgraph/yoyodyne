package triage

import (
	"strings"
	"testing"
	"time"
)

// A re-run waiting for its item to be unparked is not owed by the harness and is
// not the development manager's to decide: the docket names whoever parked the
// item as the next mover, and says what the decision waits for.
func TestARerunWaitingOnAParkedItemNamesWhoeverParkedItAsTheNextMover(t *testing.T) {
	t.Parallel()

	standing := Standing{Decided: true, Spends: true, WaitsOnParking: true}
	if AwaitingCarryOut(standing) {
		t.Fatalf("standing %#v read as owed by the harness", standing)
	}
	if !DecisionStands(standing) || !WaitingOnParking(standing) {
		t.Fatalf("standing %#v, want the decision still standing and waiting on the unparking", standing)
	}
	finding := CarryOut{
		Decision: "rerun", Gate: ParkedGate, Unattempted: true,
		Refusal:   "yoyodyne-task is parked by the Lead Product Manager in conversation chat-1 (held for the Slack route)",
		Clears:    "the Lead Product Manager in conversation chat-1 releasing the item from parking",
		RefusedAt: time.Date(2026, 11, 15, 21, 16, 0, 0, time.UTC),
	}
	entry := Entry{RunID: "run-1", WorkItemID: "yoyodyne-task", Counters: Counters{Standing: standing}, CarryOut: &finding}
	if mover := entry.renderNextMover(); !strings.Contains(mover, "Next mover: whoever parked the item") {
		t.Fatalf("next mover %q, want whoever parked the item", mover)
	}
	rendered := entry.renderCarryOut()
	for _, want := range []string{"waits for the item to be unparked", "parked by the Lead Product Manager", "first pass after the item is unparked"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered %q, want it to say %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "No pass has attempted") {
		t.Fatalf("rendered %q, want the parked wait not read as a decision no pass reached", rendered)
	}
}

// A wait on a parking does not go ahead of the list as a decision that needs
// the development manager.
func TestAWaitOnAParkingIsNotCritical(t *testing.T) {
	t.Parallel()

	closed := time.Date(2026, 11, 15, 21, 15, 0, 0, time.UTC)
	entry := Entry{Class: ClassStoppedRun, Closed: &Closure{ClosedAt: closed},
		CarryOut: &CarryOut{Decision: "rerun", Gate: ParkedGate, Unattempted: true, RefusedAt: closed.Add(time.Minute)}}
	if entry.Critical() {
		t.Fatal("a wait on a parking was critical")
	}
	entry.CarryOut.Gate = "developer capacity"
	if !entry.Critical() {
		t.Fatal("an unattempted decision was not critical")
	}
}

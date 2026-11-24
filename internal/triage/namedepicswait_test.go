package triage

import (
	"strings"
	"testing"
	"time"
)

// A decision waiting for the named epics is not critical, names the harness as
// the one that moves next, and says what it waits for rather than that no pass
// attempted it.
func TestAWaitOnTheNamedEpicsIsNotCriticalAndSaysWhatItWaitsFor(t *testing.T) {
	t.Parallel()

	closed := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	for _, finding := range []CarryOut{{Unattempted: true}, {Waiting: true, Attempts: 1}} {
		finding.Decision, finding.Gate, finding.RefusedAt = "rerun", NamedEpicsGate, closed.Add(time.Minute)
		finding.Refusal, finding.Clears = "yoyodyne-task is not in an open epic the operator named", "the operator adds it"
		entry := Entry{Class: ClassStoppedRun, RunID: "run-1", WorkItemID: "yoyodyne-task", Closed: &Closure{ClosedAt: closed}, CarryOut: &finding,
			Counters: Counters{Standing: Standing{Decided: true, Spends: true, WaitsOnNamedEpics: true}}}
		if entry.Critical() {
			t.Fatalf("%#v was critical", finding)
		}
		if mover := entry.renderNextMover(); !strings.Contains(mover, "Next mover: the harness") || !strings.Contains(mover, "the epics the operator named do not let its item start") {
			t.Fatalf("next mover %q, want the harness, waiting on the named epics", mover)
		}
		rendered := entry.renderCarryOut()
		if !strings.Contains(rendered, "waits because the epics the operator named do not let its item start") || strings.Contains(rendered, "No pass has attempted") {
			t.Fatalf("rendered %q, want the wait on the named epics", rendered)
		}
		finding.Gate, finding.Waiting = "developer capacity", false
		if !entry.Critical() {
			t.Fatalf("%#v was not critical", finding)
		}
		finding.Gate, finding.Cause = NamedEpicsGate, CarryOutDecisionMissing
		if finding.WaitsOnNamedEpics() || !entry.Critical() {
			t.Fatalf("%#v, a refusal nothing clears, read as a wait", finding)
		}
	}
}

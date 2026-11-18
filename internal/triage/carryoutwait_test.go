package triage

import (
	"strings"
	"testing"
	"time"
)

// A decision no run has taken up is said with when it was decided and how long
// it had waited by the finding, so one standing for hours does not read as a
// fresh wait. One whose decision time is not known says only what it did.
func TestAWaitingDecisionIsSaidWithItsAge(t *testing.T) {
	t.Parallel()
	decided := time.Date(2026, 10, 17, 18, 56, 0, 0, time.UTC)
	for _, finding := range []CarryOut{
		{Decision: "rerun", Gate: "capacity", Refusal: "every developer slot is occupied", Unattempted: true},
		{Decision: "rerun", Gate: "capacity", Refusal: "every developer slot is occupied", Waiting: true, Attempts: 3},
	} {
		finding.DecidedAt, finding.RefusedAt = decided, decided.Add(6*time.Hour+20*time.Minute)
		entry := Entry{RunID: "run-1", WorkItemID: "yoyodyne-i92", CarryOut: &finding}
		if rendered := entry.renderCarryOut(); !strings.Contains(rendered, "You decided it at 2026-10-17T18:56:00Z; it had waited 6 hours 20 minutes by then with no run taking it up.") {
			t.Fatalf("rendered %q; want the decision's age said", rendered)
		}
		finding.DecidedAt = time.Time{}
		if rendered := entry.renderCarryOut(); strings.Contains(rendered, "You decided it at") {
			t.Fatalf("rendered %q; want no age where the decision's time is not known", rendered)
		}
	}
}

// A span is said in words a person reads, never as Go prints a duration.
func TestASpanIsSaidInWords(t *testing.T) {
	t.Parallel()
	for span, want := range map[time.Duration]string{
		30 * time.Second:              "less than a minute",
		time.Minute:                   "1 minute",
		45 * time.Minute:              "45 minutes",
		time.Hour:                     "1 hour",
		6*time.Hour + 20*time.Minute:  "6 hours 20 minutes",
		24 * time.Hour:                "1 day",
		51*time.Hour + 10*time.Minute: "2 days 3 hours",
	} {
		if got := SpanInWords(span); got != want {
			t.Errorf("SpanInWords(%v) = %q, want %q", span, got, want)
		}
	}
}

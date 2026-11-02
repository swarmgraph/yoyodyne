package runstate

import (
	"strings"
	"testing"
	"time"
)

// A wait for a person's merge records at most one ending, and each ending
// carries what it is recorded with.
func TestPersonMergeRecordsOneEndingWithWhatItNeeds(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC)
	later := since.Add(time.Hour)
	commit := strings.Repeat("c", 40)

	waiting := PersonMerge{Number: 7, Since: since}
	if err := waiting.Validate(); err != nil || !waiting.Waiting() {
		t.Fatalf("waiting = %v (waiting %t), want a valid wait", err, waiting.Waiting())
	}
	if got := waiting.Says(); got != "waiting for a person to merge pull request #7" {
		t.Errorf("Says() = %q", got)
	}
	merged := PersonMerge{Number: 7, Since: since, MergedAt: &later, MergeCommit: commit}
	if err := merged.Validate(); err != nil || merged.Waiting() {
		t.Fatalf("merged = %v (waiting %t), want a valid ended wait", err, merged.Waiting())
	}
	returned := PersonMerge{Number: 7, Since: since, ClosedAt: &later, ClosedBy: "octo", ReturnedAt: &later}
	if err := returned.Validate(); err != nil || returned.Waiting() {
		t.Fatalf("returned = %v (waiting %t), want a valid ended wait", err, returned.Waiting())
	}
	for name, invalid := range map[string]PersonMerge{
		"no number":             {Since: since},
		"no start":              {Number: 7},
		"both endings":          {Number: 7, Since: since, MergedAt: &later, ClosedAt: &later, ReturnedAt: &later},
		"a commit before merge": {Number: 7, Since: since, MergeCommit: commit},
		"returned with no time": {Number: 7, Since: since, ReturnedAt: &later},
		"closer while waiting":  {Number: 7, Since: since, ClosedBy: "octo"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want it refused", name)
		}
	}

	state := State{Status: StatusSucceeded, PullRequest: &PullRequest{Number: 7}, PersonMerge: &waiting}
	if !state.WaitingForPersonMerge() {
		t.Fatal("a succeeded run with an open wait does not read as waiting")
	}
	state.PersonMerge = &merged
	if state.WaitingForPersonMerge() {
		t.Fatal("a run whose wait ended still reads as waiting")
	}
}

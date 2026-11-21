package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
)

func untouchedRerunFor(stage, commit string) UntouchedRerun {
	return UntouchedRerun{
		Stage:     stage,
		Commit:    commit,
		Command:   "make test",
		Tests:     []checks.FailingTest{{Name: "TestTotals", File: "internal/alpha/alpha_test.go"}},
		StartedAt: time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC),
	}
}

// The reruns are counted by check stage and candidate commit, a rerun never
// known to have ended included, and the record keeps the latest within its
// bound.
func TestUntouchedRerunsAreCountedByStageAndCommit(t *testing.T) {
	t.Parallel()

	const first, second = "0123456789abcdef0123456789abcdef01234567", "1123456789abcdef0123456789abcdef01234567"
	var state State
	state.BeginUntouchedRerun(untouchedRerunFor(UntouchedRerunBeforeReview, first))
	if got := state.UntouchedRerunsSpent(UntouchedRerunBeforeReview, first); got != 1 {
		t.Fatalf("spent before review on the first commit = %d, want the unfinished rerun counted", got)
	}
	if got := state.UntouchedRerunsSpent(UntouchedRerunBeforePromotion, first); got != 0 {
		t.Fatalf("spent before promotion = %d, want the other stage counted apart", got)
	}
	if got := state.UntouchedRerunsSpent(UntouchedRerunBeforeReview, second); got != 0 {
		t.Fatalf("spent on a new commit = %d, want a new candidate commit to start again", got)
	}
	for range MaxUntouchedRerunRecords + 3 {
		state.BeginUntouchedRerun(untouchedRerunFor(UntouchedRerunBeforeReview, second))
	}
	if len(state.UntouchedReruns) != MaxUntouchedRerunRecords || state.UntouchedReruns[0].Commit != second {
		t.Fatalf("records = %d, oldest on %s; want the latest %d kept", len(state.UntouchedReruns), state.UntouchedReruns[0].Commit, MaxUntouchedRerunRecords)
	}
}

// The narrow update adds a rerun as it starts and replaces that same rerun
// when it ends, moving the revision on each time.
func TestTheUntouchedRerunUpdateAddsThenReplacesTheSameRerun(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"
	started := untouchedRerunFor(UntouchedRerunBeforeReview, commit)
	at := started.StartedAt.Add(time.Minute)
	state, changed, err := ApplyUpdate(State{RunID: "run-x"}, UntouchedRerunRecorded{Rerun: started, At: started.StartedAt})
	if err != nil || !changed || state.Revision != 1 || len(state.UntouchedReruns) != 1 || state.UntouchedReruns[0].Outcome != "" {
		t.Fatalf("ApplyUpdate(start) = %#v, %t, %v", state.UntouchedReruns, changed, err)
	}
	ended := started
	ended.Outcome, ended.EndedAt = UntouchedRerunPassed, &at
	state, _, err = ApplyUpdate(state, UntouchedRerunRecorded{Rerun: ended, At: at})
	if err != nil || state.Revision != 2 || len(state.UntouchedReruns) != 1 || state.UntouchedReruns[0].Outcome != UntouchedRerunPassed {
		t.Fatalf("ApplyUpdate(end) = %#v, %v; want the same rerun replaced", state.UntouchedReruns, err)
	}
}

// A rerun record holds a stage, a commit, its tests, and an ending that agrees
// with whether it has ended.
func TestAnUntouchedRerunRecordIsValidated(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"
	valid := untouchedRerunFor(UntouchedRerunBeforeReview, commit)
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() = %v for a rerun recorded before it started", err)
	}
	ended := valid
	at := valid.StartedAt.Add(time.Minute)
	ended.Outcome, ended.EndedAt = UntouchedRerunPassed, &at
	if err := ended.Validate(); err != nil {
		t.Fatalf("Validate() = %v for a rerun that passed", err)
	}
	for name, change := range map[string]func(*UntouchedRerun){
		"stage":         func(r *UntouchedRerun) { r.Stage = "after landing" },
		"commit":        func(r *UntouchedRerun) { r.Commit = "main" },
		"no tests":      func(r *UntouchedRerun) { r.Tests = nil },
		"outcome":       func(r *UntouchedRerun) { r.Outcome, r.EndedAt = "flaky", &at },
		"ended no time": func(r *UntouchedRerun) { r.Outcome = UntouchedRerunFailed },
		"time no end":   func(r *UntouchedRerun) { r.EndedAt = &at },
	} {
		broken := valid
		change(&broken)
		if err := broken.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted %#v", name, broken)
		}
	}
	passed := State{UntouchedReruns: []UntouchedRerun{valid, ended}}
	if got := passed.PassedUntouchedReruns(commit); len(got) != 1 || got[0].Outcome != UntouchedRerunPassed {
		t.Fatalf("PassedUntouchedReruns() = %#v, want only the rerun that passed", got)
	}
	if got := ended.TestNames(); !strings.Contains(got, "TestTotals (internal/alpha/alpha_test.go)") {
		t.Fatalf("TestNames() = %q", got)
	}
}

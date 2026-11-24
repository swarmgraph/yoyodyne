package runstate

import (
	"testing"
	"time"
)

func TestAConflictsHandbackIsWhatTheRunSpendsForUntilSomethingElseIsAsked(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusRunning)
	if said, named := state.SpendsForConflict(); said != SpendConflictNone || named != "" {
		t.Fatalf("a run with no conflict says %q, %q", said, named)
	}
	state.ConflictSpendingUnclear()
	if state.ConflictSpending != nil {
		t.Fatal("a run no conflict was handed back to was made unclear")
	}
	at := time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)
	lines := 4
	first := ConflictHandback{ID: "mqe-1", From: ConflictFromMergeQueue, At: at, TargetBranch: "main", Paths: []string{"a.go"}, Lines: &lines}
	state.RecordConflictHandback(first)
	state.RecordConflictHandback(first)
	if len(state.ConflictHandbacks) != 1 {
		t.Fatalf("the same hand-back was recorded %d times", len(state.ConflictHandbacks))
	}
	if said, named := state.SpendsForConflict(); said != SpendConflictHandback || named != "mqe-1" {
		t.Fatalf("after a hand-back the run says %q, %q", said, named)
	}
	state.ConflictSpendingUnclear()
	if said, named := state.SpendsForConflict(); said != SpendConflictUnclear || named != "mqe-1" {
		t.Fatalf("after something else was asked the run says %q, %q", said, named)
	}
	// A hand-back with no id of its own takes one, and the run spends for it.
	id := state.RecordConflictHandback(ConflictHandback{From: ConflictFromLanding, At: at.Add(time.Hour), TargetBranch: "main"})
	if said, named := state.SpendsForConflict(); id == "" || id == "mqe-1" || said != SpendConflictHandback || named != id {
		t.Fatalf("after a second hand-back %q the run says %q, %q", id, said, named)
	}
	if state.ConflictHandbacks[1].LinesUnknown == "" {
		t.Fatal("a hand-back with no count does not say it is not known")
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	for _, broken := range []func(*ConflictHandback){
		func(h *ConflictHandback) { h.ID = "" },
		func(h *ConflictHandback) { h.From = "elsewhere" },
		func(h *ConflictHandback) { h.Lines, h.LinesUnknown = nil, "" },
		func(h *ConflictHandback) { h.LinesUnknown = "and a count" },
	} {
		record := state.ConflictHandbacks[0]
		broken(&record)
		if record.Validate() == nil {
			t.Fatalf("%#v was accepted", record)
		}
	}
}

func TestRunsHandedBackForAConflictAreFoundThroughTheIndex(t *testing.T) {
	t.Parallel()

	store := newStoreAt(t, t.TempDir())
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)
	if found, err := store.ConflictHandbacksSince(at); err != nil || len(found) != 0 {
		t.Fatalf("found %d runs, %v; want none before a hand-back", len(found), err)
	}
	state.RecordConflictHandback(ConflictHandback{From: ConflictFromLanding, At: at, TargetBranch: "main"})
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if found, err := store.ConflictHandbacksSince(at); err != nil || len(found) != 1 || found[0].RunID != state.RunID {
		t.Fatalf("found %d runs, %v; want the one handed back", len(found), err)
	}
	if found, err := store.ConflictHandbacksSince(at.Add(time.Second)); err != nil || len(found) != 0 {
		t.Fatalf("found %d runs after the hand-back, %v", len(found), err)
	}
}

func TestASpendLinesAccountOfAConflictHasToAgreeWithItself(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		line Spend
		ok   bool
	}{
		{Spend{RunID: "run-1"}, true},
		{Spend{ConversationID: "chat-1"}, true},
		{Spend{RunID: "run-1", Conflict: SpendConflictNone}, true},
		{Spend{RunID: "run-1", Conflict: SpendConflictHandback, ConflictHandback: "mqe-1"}, true},
		{Spend{RunID: "run-1", Conflict: SpendConflictUnclear}, true},
		{Spend{RunID: "run-1", Conflict: SpendConflictHandback}, false},
		{Spend{RunID: "run-1", Conflict: SpendConflictNone, ConflictHandback: "mqe-1"}, false},
		{Spend{RunID: "run-1", ConflictHandback: "mqe-1"}, false},
		{Spend{RunID: "run-1", Conflict: "maybe"}, false},
		{Spend{ConversationID: "chat-1", Conflict: SpendConflictNone}, false},
	} {
		if err := test.line.conflictProblem(); (err == nil) != test.ok {
			t.Errorf("%+v: %v, want accepted %t", test.line, err, test.ok)
		}
	}
}

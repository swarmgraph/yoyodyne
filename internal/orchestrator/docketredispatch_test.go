package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// refusedAttempt is a dispatch the primary checkout refused before any run was
// reserved, which is what about twenty entries on the docket were on 2026-10-02.
func refusedAttempt(workItemID string) UnstartedAttempt {
	return UnstartedAttempt{
		WorkItemID:      workItemID,
		WorkItemTitle:   "the item the checkout refused",
		SelectedBecause: "first in the product manager's order",
		Failure:         "repository is not ready for an isolated run: the primary checkout has uncommitted changes",
	}
}

// redispatchedRun is a run of an item that started at a moment, which is the
// item being dispatched again.
func redispatchedRun(runID, workItemID string, started time.Time) runstate.State {
	state := stoppedState()
	state.RunID = runID
	state.WorkItemID = workItemID
	state.StartedAt = started
	state.UpdatedAt = started.Add(time.Hour)
	completed := started.Add(time.Hour)
	state.CompletedAt = &completed
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseComplete
	state.Blocker = ""
	state.CheckFailure = nil
	state.ReviewFindingDetails = nil
	state.ReviewFindings = 0
	state.ReviewDecision = runstate.ReviewApprove
	return state
}

// An attempt that never became a run, whose item was dispatched again and became
// a run, is settled by the next build with the run named, and is not offered on
// the pass after. An attempt whose item has not been dispatched since stays.
func TestAnAttemptIsSettledOnceItsItemIsDispatchedAgain(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	const (
		redispatched = "yoyodyne-ifd.501"
		waiting      = "yoyodyne-ifd.502"
		laterRun     = "run-11111111111111111111111111111111"
		laterStill   = "run-22222222222222222222222222222222"
		earlierRun   = "run-33333333333333333333333333333333"
	)
	refused := docketedNow.Add(-6 * time.Hour)
	recorder := Docketer{Docket: store, ProductID: "yoyodyne", Decisions: &recordedDecisions{}, Caps: docketedCaps, Triage: docketedTriage, Clock: docketClockAt{at: refused}}
	for _, item := range []string{redispatched, waiting} {
		if created, err := recorder.RecordUnstartedAttempt(refusedAttempt(item)); err != nil || !created {
			t.Fatalf("RecordUnstartedAttempt(%s) = %v, %v, want it docketed", item, created, err)
		}
	}

	// The waiting item had a run before it was refused, which says nothing about
	// what happened after; the redispatched one had two runs after, and the first
	// of them is what overtook the attempt.
	recorded := &recordedDecisions{}
	docketer := Docketer{
		Docket: store,
		Runs: recordedRuns{states: []runstate.State{
			redispatchedRun(earlierRun, waiting, refused.Add(-24*time.Hour)),
			redispatchedRun(laterStill, redispatched, refused.Add(3*time.Hour)),
			redispatchedRun(laterRun, redispatched, refused.Add(time.Hour)),
		}},
		Decisions: recorded,
		Reruns:    recorded,
		Caps:      docketedCaps,
		Triage:    docketedTriage,
		Clock:     docketClock{},
	}
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 || built.Entries[0].WorkItemID != waiting || built.Entries[0].Class != triage.ClassUnstartedAttempt {
		t.Fatalf("entries = %#v, want only the attempt whose item has not been dispatched since", built.Entries)
	}
	if built.Closed != 1 {
		t.Fatalf("closed = %d, want the overtaken attempt counted as settled", built.Closed)
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, entry := range entries {
		if entry.Class != triage.ClassUnstartedAttempt {
			continue
		}
		switch entry.WorkItemID {
		case redispatched:
			closed := entry.Closed
			if closed == nil || closed.Decision != redispatchedAttemptDecision {
				t.Fatalf("closure = %#v, want the attempt settled as dispatched again", closed)
			}
			if !strings.Contains(closed.Reason, "dispatched again") || !strings.Contains(closed.Reason, laterRun) || strings.Contains(closed.Reason, laterStill) {
				t.Fatalf("reason = %q, want it to name the first run that followed", closed.Reason)
			}
			if !strings.Contains(closed.DecidedBy, "the harness") {
				t.Fatalf("decided by %q, want the harness named", closed.DecidedBy)
			}
		case waiting:
			if entry.Closed != nil {
				t.Fatalf("closure = %#v, want the attempt whose item was not dispatched again left standing", entry.Closed)
			}
		}
	}

	// The next pass offers the same: the settled attempt is not docketed again.
	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if rebuilt.Added != 0 || len(rebuilt.Entries) != 1 || rebuilt.Entries[0].WorkItemID != waiting {
		t.Fatalf("second build = %#v, want only the waiting attempt and nothing added", rebuilt)
	}

	// The same refusal met again after it was settled is news, and is docketed.
	again := recorder
	again.Clock = docketClockAt{at: docketedNow.Add(time.Hour)}
	if created, err := again.RecordUnstartedAttempt(refusedAttempt(redispatched)); err != nil || !created {
		t.Fatalf("RecordUnstartedAttempt() after the settlement = %v, %v, want the new refusal docketed", created, err)
	}
}

// A decision the development manager made about an attempt stands: the
// harness settles only an attempt nobody's decision is standing over.
func TestAnAttemptADecisionStandsOverIsLeftToThatDecision(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	const item = "yoyodyne-ifd.503"
	refused := docketedNow.Add(-6 * time.Hour)
	recorder := Docketer{Docket: store, ProductID: "yoyodyne", Decisions: &recordedDecisions{}, Caps: docketedCaps, Triage: docketedTriage, Clock: docketClockAt{at: refused}}
	if _, err := recorder.RecordUnstartedAttempt(refusedAttempt(item)); err != nil {
		t.Fatalf("RecordUnstartedAttempt() error = %v", err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, err := store.Close(triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           entries[0].Key,
		ProductID:     "yoyodyne",
		WorkItemID:    item,
		Decision:      "escalate",
		Reason:        "the primary checkout is dirty, and only a person can clean it",
		DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
		ClosedAt:      refused.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recorded := &recordedDecisions{}
	docketer := Docketer{
		Docket:    store,
		Runs:      recordedRuns{states: []runstate.State{redispatchedRun("run-44444444444444444444444444444444", item, refused.Add(time.Hour))}},
		Decisions: recorded,
		Reruns:    recorded,
		Caps:      docketedCaps,
		Triage:    docketedTriage,
		Clock:     docketClock{},
	}
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	entries, err = store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if closed := entries[0].Closed; closed == nil || closed.Decision != "escalate" {
		t.Fatalf("closure = %#v, want her decision to stand", closed)
	}
}

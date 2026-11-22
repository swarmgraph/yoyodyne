package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A document run's carry-out notes are kept under its own run identifier,
// which no work item the tracker holds can be. Delivering them goes through the
// run's record to the item it is for, and a note with no item to go on is set
// aside once rather than asked about at every pull.

const documentNoteRunID = "run-c7220000111122223333444455556666"

// runNoteTracker answers for the items it holds and records every id it was
// asked about, so a test can say the tracker never saw a run identifier.
type runNoteTracker struct {
	holds map[string]*beads.WorkItem
	asked []string
}

func (t *runNoteTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	t.asked = append(t.asked, id)
	item, ok := t.holds[id]
	if !ok {
		return beads.WorkItem{}, errors.New("no issue found matching " + id)
	}
	return *item, nil
}

func (t *runNoteTracker) RecordOutcome(_ context.Context, id, note string) (beads.WorkItem, error) {
	t.asked = append(t.asked, id)
	item, ok := t.holds[id]
	if !ok {
		return beads.WorkItem{}, errors.New("no issue found matching " + id)
	}
	item.Notes += note
	return *item, nil
}

type noteRuns struct{ recorded []runstate.State }

func (r noteRuns) Incomplete() ([]runstate.State, error) { return nil, nil }
func (r noteRuns) Recorded() ([]runstate.State, error)   { return r.recorded, nil }
func (r noteRuns) Held(string) (bool, error)             { return false, nil }

type noDocket struct{}

func (noDocket) List() ([]triage.Entry, error) { return nil, nil }

// documentNoteRun is a document run recorded under its own run identifier,
// whose latest revision names answers, or no item where answers is empty.
func documentNoteRun(answers string) runstate.State {
	reason := "a revision naming no work item"
	if answers != "" {
		reason = answers + ": the revision this run publishes"
	}
	return runstate.State{
		RunID:      documentNoteRunID,
		WorkItemID: documentNoteRunID,
		Document: &runstate.DocumentPublication{
			WriteID: "document-7.1",
			Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{
				Revisions: []artifact.Revision{{Reason: reason}},
			}},
		},
	}
}

func pendingNoteOn(t *testing.T, triageStore *runstate.TriageStore, workItemID string) string {
	t.Helper()
	counters, err := triageStore.RecordCarryOutRefusal(context.Background(), workItemID, runstate.TriageCarryOut{
		Cause:    triage.CarryOutWorktreeGone,
		RunID:    documentNoteRunID,
		Decision: runstate.TriageDecisionRepair,
		Gate:     runstate.TriageGatePreservedWork,
		Refusal:  "the stopped run's worktree is gone",
		Clears:   "a re-run or an escalation",
	}, time.Now())
	if err != nil || len(counters.PendingCarryOutNotes) != 1 {
		t.Fatalf("RecordCarryOutRefusal() = %+v, %v; want one pending note", counters, err)
	}
	return counters.PendingCarryOutNotes[0]
}

func newNoteCarryOut(t *testing.T, tracker *runNoteTracker, state runstate.State) (CarryOut, *runstate.TriageStore) {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	triageStore := store.Triage()
	return CarryOut{Docket: noDocket{}, Decisions: triageStore, Notes: tracker, Runs: noteRuns{recorded: []runstate.State{state}}}, triageStore
}

func TestADocumentRunsCarryOutNoteIsDeliveredToTheItemItsRecordNames(t *testing.T) {
	t.Parallel()
	const answers = "yoyodyne-ifd.900"
	tracker := &runNoteTracker{holds: map[string]*beads.WorkItem{answers: {ID: answers}}}
	carrying, triageStore := newNoteCarryOut(t, tracker, documentNoteRun(answers))
	note := pendingNoteOn(t, triageStore, documentNoteRunID)

	for pull := 0; pull < 3; pull++ {
		if err := carrying.DeliverNotes(context.Background()); err != nil {
			t.Fatalf("pull %d: DeliverNotes() error = %v", pull, err)
		}
	}
	if got := tracker.holds[answers].Notes; strings.Count(got, note) != 1 {
		t.Fatalf("item notes = %q, want the pending note once", got)
	}
	for _, id := range tracker.asked {
		if id != answers {
			t.Fatalf("tracker asked about %q; want only the item the run's record names (asked %v)", id, tracker.asked)
		}
	}
	counters, err := triageStore.Counters(documentNoteRunID)
	if err != nil || len(counters.PendingCarryOutNotes) != 0 || len(counters.UndeliverableCarryOutNotes) != 0 {
		t.Fatalf("after delivery = %+v, %v; want nothing pending and nothing set aside", counters, err)
	}
}

func TestACarryOutNoteWithNoWorkItemIsSetAsideOnceAndNeverAskedAbout(t *testing.T) {
	t.Parallel()
	for name, recorded := range map[string]bool{"document naming no item": true, "run no longer on record": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker := &runNoteTracker{holds: map[string]*beads.WorkItem{}}
			carrying, triageStore := newNoteCarryOut(t, tracker, documentNoteRun(""))
			if !recorded {
				carrying.Runs = noteRuns{}
			}
			note := pendingNoteOn(t, triageStore, documentNoteRunID)
			if !recorded {
				// With no run on record, nothing makes the pull walk the run's
				// item; a stop decision delivering it is what reaches it.
				if err := carrying.deliverItemNotes(context.Background(), documentNoteRunID, onceRecorded(carrying.Runs)); err != nil {
					t.Fatalf("deliverItemNotes() error = %v", err)
				}
			}
			for pull := 0; pull < 3; pull++ {
				if err := carrying.DeliverNotes(context.Background()); err != nil {
					t.Fatalf("pull %d: DeliverNotes() error = %v", pull, err)
				}
			}
			if len(tracker.asked) != 0 {
				t.Fatalf("tracker asked about %v; want it never asked", tracker.asked)
			}
			counters, err := triageStore.Counters(documentNoteRunID)
			if err != nil || len(counters.PendingCarryOutNotes) != 0 || len(counters.UndeliverableCarryOutNotes) != 1 {
				t.Fatalf("after pulls = %+v, %v; want the note set aside once", counters, err)
			}
			setAside := counters.UndeliverableCarryOutNotes[0]
			want := "no work item"
			if !recorded {
				want = "no longer on record"
			}
			if setAside.Note != note || !strings.Contains(setAside.Reason, want) || !strings.Contains(setAside.Reason, documentNoteRunID) {
				t.Fatalf("set aside = %+v; want the note with a reason naming the run and %q", setAside, want)
			}
		})
	}
}

// noteWorkTracker is runNoteTracker as the reconciler's tracker, refusing every
// write it is not meant to make.
type noteWorkTracker struct {
	WorkTracker
	*runNoteTracker
}

func (t noteWorkTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	return t.runNoteTracker.Show(ctx, id)
}

func (t noteWorkTracker) RecordOutcome(ctx context.Context, id, note string) (beads.WorkItem, error) {
	return t.runNoteTracker.RecordOutcome(ctx, id, note)
}

type savedRuns struct {
	ReconcileStore
	saved []runstate.State
}

func (s *savedRuns) Save(state runstate.State) error {
	s.saved = append(s.saved, state)
	return nil
}

// Reconcile's note saying where a retired checkout's work went is written onto
// the item a document run's record names, and a run with no item records that
// once, so later passes stop retrying it and the tracker is never asked about
// the run identifier.
func TestReconcileWritesAPreservedWorkNoteThroughTheRunsRecord(t *testing.T) {
	t.Parallel()
	const preserved = "refs/yoyodyne/preserved/" + documentNoteRunID
	for name, answers := range map[string]string{"names an item": "yoyodyne-ifd.901", "names none": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker := &runNoteTracker{holds: map[string]*beads.WorkItem{}}
			if answers != "" {
				tracker.holds[answers] = &beads.WorkItem{ID: answers}
			}
			store := &savedRuns{}
			reconciler := Reconciler{Tracker: noteWorkTracker{runNoteTracker: tracker}, Store: store}
			state := documentNoteRun(answers)
			state.WorktreeRemoved, state.PreservedWorkRef, state.WorktreePath = true, preserved, "/retired/checkout"
			if !state.PreservedWorkNotePending() {
				t.Fatal("a retired checkout with an undelivered note must be pending")
			}
			if problem := reconciler.recordPreservedWork(context.Background(), &state, preserved); problem != "" {
				t.Fatalf("recordPreservedWork() = %q, want no problem", problem)
			}
			for _, id := range tracker.asked {
				if id != answers {
					t.Fatalf("tracker asked about %q; want only the item the run's record names (asked %v)", id, tracker.asked)
				}
			}
			if len(store.saved) != 1 || store.saved[0].PreservedWorkNotePending() {
				t.Fatalf("saved = %+v, want the note settled in one save", store.saved)
			}
			if answers != "" {
				if !strings.Contains(tracker.holds[answers].Notes, preserved) || state.PreservedWorkNotedAt == nil {
					t.Fatalf("item notes = %q, noted = %v; want the note on the named item", tracker.holds[answers].Notes, state.PreservedWorkNotedAt)
				}
				return
			}
			if len(tracker.asked) != 0 || state.PreservedWorkNotedAt != nil || !strings.Contains(state.PreservedWorkNoteUndeliverable, "no work item") {
				t.Fatalf("asked %v, noted %v, undeliverable %q; want the note recorded once as having no item", tracker.asked, state.PreservedWorkNotedAt, state.PreservedWorkNoteUndeliverable)
			}
			if !reconcileFindingResolved(state, runstate.ReconcileWorktree) || len(sweepableWorktrees([]runstate.State{state})) != 0 {
				t.Fatal("a note recorded as undeliverable must leave nothing for reconcile to retry")
			}
		})
	}
}

type loadedRuns struct{ state runstate.State }

func (l loadedRuns) Load(string) (runstate.State, error) { return l.state, nil }

// A document run's pending settlement finding is delivered through the tracker
// the reconciler is given in the product, which answers for the run's
// identifier from its record, so the plain tracker is never asked about it.
func TestAPendingReconcileFindingOfADocumentRunIsDeliveredWithoutAskingForTheRunIdentifier(t *testing.T) {
	t.Parallel()
	const answers = "yoyodyne-ifd.902"
	base := &runNoteTracker{holds: map[string]*beads.WorkItem{answers: {ID: answers}}}
	state := documentNoteRun(answers)
	state.ReconcileFindings = []runstate.ReconcileFinding{{Step: runstate.ReconcileWorktree, Problem: "observe leftover worktree: unreadable", Pending: true}}
	store := &savedRuns{}
	reconciler := Reconciler{Tracker: HarnessRunItems{WorkTracker: noteWorkTracker{runNoteTracker: base}, Runs: loadedRuns{state}}, Store: store}
	for pass := 0; pass < 2; pass++ {
		if _, problem := reconciler.deliverReconcileFinding(context.Background(), &state, 0); problem != "" {
			t.Fatalf("pass %d: deliverReconcileFinding() = %q, want none", pass, problem)
		}
	}
	for _, id := range base.asked {
		if id != answers {
			t.Fatalf("tracker asked about %q; want only the item the run's record names (asked %v)", id, base.asked)
		}
	}
	if !strings.Contains(base.holds[answers].Notes, "Settlement finding") || state.ReconcileFindings[0].Pending {
		t.Fatalf("notes = %q, finding = %+v; want it delivered to the named item and settled", base.holds[answers].Notes, state.ReconcileFindings[0])
	}
}

type runItemsListingTracker struct {
	noteWorkTracker
	lists int
}

func (l *runItemsListingTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	l.lists++
	return []beads.WorkItem{{ID: "yoyodyne-ifd.903", Status: "closed"}}, nil
}

// The tracker the reconciler is given still lists closed items in one read.
func TestTheRunItemsTrackerStillListsClosedItemsInOneRead(t *testing.T) {
	t.Parallel()
	base := &runItemsListingTracker{noteWorkTracker: noteWorkTracker{runNoteTracker: &runNoteTracker{}}}
	reconciler := Reconciler{Tracker: HarnessRunItems{WorkTracker: base, Runs: loadedRuns{}}}
	closed, unread, err := reconciler.closedItems(context.Background(), []string{"yoyodyne-ifd.903", "yoyodyne-ifd.904"})
	if err != nil || len(unread) != 0 || !closed["yoyodyne-ifd.903"] || closed["yoyodyne-ifd.904"] {
		t.Fatalf("closedItems() = %v, %v, %v", closed, unread, err)
	}
	if base.lists != 1 || len(base.runNoteTracker.asked) != 0 {
		t.Fatalf("listed %d times, asked %v; want one listing and no single reads", base.lists, base.runNoteTracker.asked)
	}
}

type capableTracker struct {
	noteWorkTracker
	prefetched, statused []string
	waits                []string
}

func (c *capableTracker) PrefetchItems(_ context.Context, ids []string) func() {
	c.prefetched = append(c.prefetched, ids...)
	return func() {}
}

func (c *capableTracker) Statuses(_ context.Context, ids []string) (map[string]string, error) {
	c.statused = append(c.statused, ids...)
	out := map[string]string{}
	for _, id := range ids {
		out[id] = "open"
	}
	return out, nil
}

func (c *capableTracker) RecordSchedulingWait(_ context.Context, id, _, _ string, _ time.Time) (beads.WorkItem, error) {
	c.waits = append(c.waits, id)
	return beads.WorkItem{ID: id}, nil
}

// The tracker the reconciler is given keeps every optional ability of the
// tracker it wraps, so the sweep's batched reads and scheduling-reason clears
// still reach it, and a run identifier never does.
func TestTheRunItemsTrackerKeepsTheWrappedTrackersAbilities(t *testing.T) {
	t.Parallel()
	base := &capableTracker{noteWorkTracker: noteWorkTracker{runNoteTracker: &runNoteTracker{}}}
	state := documentNoteRun("yoyodyne-ifd.905")
	reconciler := Reconciler{Tracker: HarnessRunItems{WorkTracker: base, Runs: loadedRuns{state}}}
	other := runstate.State{RunID: "run-aaaa0000111122223333444455556666", WorkItemID: "yoyodyne-ifd.906"}
	reconciler.prefetchItems(context.Background(), []runstate.State{other})()
	if len(base.prefetched) != 1 || base.prefetched[0] != "yoyodyne-ifd.906" {
		t.Fatalf("prefetched %v, want the one work item", base.prefetched)
	}
	got, err := itemStatuses(context.Background(), reconciler.Tracker, []string{"yoyodyne-ifd.906", "yoyodyne-ifd.907", documentNoteRunID})
	if err != nil || got["yoyodyne-ifd.906"] != "open" || got[documentNoteRunID] == "" {
		t.Fatalf("itemStatuses() = %v, %v", got, err)
	}
	if len(base.statused) != 2 {
		t.Fatalf("batched read asked for %v, want the two work items in one call", base.statused)
	}
	if err := recordSchedulingWait(context.Background(), reconciler.Tracker, "yoyodyne-ifd.906", "", "waiting", time.Now()); err != nil || len(base.waits) != 1 {
		t.Fatalf("scheduling wait recorded %v, %v; want one record on the work item", base.waits, err)
	}
	if err := recordSchedulingWait(context.Background(), reconciler.Tracker, documentNoteRunID, "", "waiting", time.Now()); err != nil || len(base.waits) != 1 {
		t.Fatalf("a run identifier reached the tracker: %v, %v", base.waits, err)
	}
}

// waitingTracker is runNoteTracker able to keep a scheduling reason, as the
// scheduler's tracker is.
type waitingTracker struct{ *runNoteTracker }

func (w waitingTracker) RecordSchedulingWait(_ context.Context, id, _, _ string, _ time.Time) (beads.WorkItem, error) {
	w.asked = append(w.asked, id)
	return beads.WorkItem{ID: id}, nil
}

// A refused decision about a document run keeps its scheduling reason on the
// item the run is for or on none, and never puts the run identifier in front of
// the tracker.
func TestARefusedDecisionAboutADocumentRunNeverAsksTheTrackerForTheRunIdentifier(t *testing.T) {
	t.Parallel()
	tracker := &runNoteTracker{holds: map[string]*beads.WorkItem{}}
	carrying, _ := newNoteCarryOut(t, tracker, documentNoteRun(""))
	carrying.Notes = waitingTracker{tracker}
	task := CarryOutTask{WorkItemID: documentNoteRunID, RunID: documentNoteRunID, Decision: "repair", DecidedAt: time.Now()}
	carrying.stopped(context.Background(), task, CarriedOut{WorkItemID: documentNoteRunID, RunID: documentNoteRunID, Decision: "repair"}, runstate.TriageGatePreservedWork, false, "the worktree is gone", "a new run")
	if err := recordSchedulingWait(context.Background(), carrying.Notes, documentNoteRunID, runstate.PassedOverAwaitingCarryOut, "held", time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(tracker.asked) != 0 {
		t.Fatalf("tracker asked about %v; want nothing for a run identifier", tracker.asked)
	}
}

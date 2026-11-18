package orchestrator

// The note a run writes once its approved change is in the merge queue
// records what the queue already holds, so a tracker that will not take it
// never stops the run (mergequeuenote.go).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// cancelledWrite is how the tracker client reports a bd write it stopped.
var cancelledWrite = errors.New("bd update failed with status cancelled and exit code -1: ")

// admissionNoteTracker is the stand-in tracker refusing the merge queue
// admission note a set number of times, or every time while failing is set,
// and taking every other write. onFailure runs at each refusal.
type admissionNoteTracker struct {
	*orchestratortest.Tracker
	mu        sync.Mutex
	failures  int
	failing   bool
	refused   int
	onFailure func()
}

func isAdmissionNote(notes string) bool {
	return strings.Contains(notes, "admitted to the merge queue for")
}

func (f *admissionNoteTracker) RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error) {
	if isAdmissionNote(notes) {
		f.mu.Lock()
		refuse := f.failing || f.failures > 0
		if refuse {
			f.refused++
			if f.failures > 0 {
				f.failures--
			}
		}
		hook := f.onFailure
		f.mu.Unlock()
		if refuse {
			if hook != nil {
				hook()
			}
			return beads.WorkItem{}, cancelledWrite
		}
	}
	return f.Tracker.RecordOutcome(ctx, id, notes)
}

func (f *admissionNoteTracker) stopFailing() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = false
}

func admissionNotes(tracker *orchestratortest.Tracker) int {
	count := 0
	for _, note := range tracker.NoteRecords {
		if isAdmissionNote(note) {
			count++
		}
	}
	return count
}

// sleeps records each wait the pipeline takes and takes none of them.
type sleeps struct {
	mu    sync.Mutex
	taken []time.Duration
}

func (s *sleeps) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taken = append(s.taken, d)
	return ctx.Err()
}

func noteRetries(state runstate.State) (int, time.Duration) {
	return state.RetryAttempts(runstate.RetryMergeQueueNote), state.RetryWaited(runstate.RetryMergeQueueNote)
}

// A write refused several times and then taken: the run stays in the queue,
// the note lands once, and the record says what was retried and for how long.
// The run's own context is cancelled at the first refusal, as a session
// draining for a restart cancels it, which is what stopped the run before.
func TestAnAdmissionNoteRefusedSeveralTimesLandsAndTheRunStaysInTheQueue(t *testing.T) {
	t.Parallel()

	r := newQueueRun(t, false)
	r.pipeline.Config.Execution.MergeQueue = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker := &admissionNoteTracker{Tracker: r.tracker, failures: 4, onFailure: cancel}
	r.pipeline.Tracker = tracker
	waits := &sleeps{}
	r.pipeline.Sleep = func(sleepCtx context.Context, d time.Duration) error {
		// The pipeline's waits for the note are made under a context the run's
		// cancellation does not reach.
		return waits.sleep(sleepCtx, d)
	}

	outcome, err := r.pipeline.Run(ctx, "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.MergeQueue == nil || outcome.MergeQueueNoteOwed != "" {
		t.Fatalf("Run() = status %s, queue %#v, owed %q; want the run ended waiting on the queue with its note written", outcome.Status, outcome.MergeQueue, outcome.MergeQueueNoteOwed)
	}
	if tracker.refused != 4 || admissionNotes(r.tracker) != 1 {
		t.Fatalf("refused %d, notes %d; want four refusals and the note written once", tracker.refused, admissionNotes(r.tracker))
	}
	state, err := r.runs.Load(outcome.RunID)
	if err != nil || !state.AwaitingMergeQueue() || state.MergeQueueNoteOwed != nil || state.Outstanding() {
		t.Fatalf("run = %#v, %v; want it waiting on the queue owing nothing", state, err)
	}
	attempts, waited := noteRetries(state)
	if attempts != 4 || waited != 7*time.Second {
		t.Fatalf("retries = %d over %s; want four waits of 1s, 1s, 2s and 3s recorded", attempts, waited)
	}
	for _, retry := range state.Retries {
		if retry.Boundary == runstate.RetryMergeQueueNote && !strings.Contains(retry.Failure, "status cancelled") {
			t.Fatalf("retry = %#v, want it to say what failed", retry)
		}
	}
	if standing, ok, err := r.queue.Standing(outcome.MergeQueue.Key(), outcome.RunID); err != nil || !ok || standing.EntryID != outcome.MergeQueue.EntryID {
		t.Fatalf("queue entry = %#v, %v, %v; want the change still standing in the queue", standing, ok, err)
	}
	if r.tracker.Blocked || r.tracker.Closed {
		t.Fatal("the item was blocked or closed although the change waits in the queue")
	}
}

// A write the tracker keeps refusing is tried for at least one tracker call's
// bound, then left owed on the run, which ends waiting on the queue as it
// would have; the next sweep writes the note once the tracker answers.
func TestAnAdmissionNoteNeverTakenIsLeftOwedAndReconciliationWritesIt(t *testing.T) {
	t.Parallel()

	r := newQueueRun(t, false)
	r.pipeline.Config.Execution.MergeQueue = true
	tracker := &admissionNoteTracker{Tracker: r.tracker, failing: true}
	r.pipeline.Tracker = tracker
	waits := &sleeps{}
	r.pipeline.Sleep = waits.sleep

	outcome := r.run(t)
	if outcome.Status != runstate.StatusSucceeded || outcome.MergeQueue == nil || !strings.Contains(outcome.MergeQueueNoteOwed, "reconciliation writes it") {
		t.Fatalf("Run() = status %s, owed %q; want the run ended waiting on the queue with the note left owed", outcome.Status, outcome.MergeQueueNoteOwed)
	}
	state, err := r.runs.Load(outcome.RunID)
	if err != nil || !state.AwaitingMergeQueue() || state.MergeQueueNoteOwed == nil || !state.Outstanding() {
		t.Fatalf("run = %#v, %v; want it waiting on the queue, owing the note", state, err)
	}
	if _, waited := noteRetries(state); waited < beads.DefaultTimeout {
		t.Fatalf("waited %s between tries; want at least one tracker call's bound, %s", waited, beads.DefaultTimeout)
	}
	if owed := state.MergeQueueNoteOwed; owed.Attempts != tracker.refused || !isAdmissionNote(owed.Note) || !strings.Contains(owed.Failure, "status cancelled") {
		t.Fatalf("owed = %#v, refused %d; want every try counted and the note kept", owed, tracker.refused)
	}
	if r.tracker.Blocked || admissionNotes(r.tracker) != 0 {
		t.Fatal("the item was blocked, or carries a note the tracker refused")
	}

	// A sweep while the tracker still refuses keeps the note owed and counts
	// the try.
	refused := tracker.refused
	reconciler := r.reconciler(r.runs, r.queue)
	reconciler.Tracker = tracker
	if result := reconcileOne(t, reconciler); !strings.Contains(result.Failure, "still owed") {
		t.Fatalf("Reconcile() = %#v, want the owed note said as still owed", result)
	}
	if kept, err := r.runs.Load(outcome.RunID); err != nil || kept.MergeQueueNoteOwed == nil || kept.MergeQueueNoteOwed.Attempts != refused+1 {
		t.Fatalf("run = %#v, %v; want the note still owed with the sweep's try counted", kept.MergeQueueNoteOwed, err)
	}

	tracker.stopFailing()
	result := reconcileOne(t, reconciler)
	if result.Failure != "" || result.Action != ActionQueued {
		t.Fatalf("Reconcile() = %#v, want the note written and the run left waiting on the queue", result)
	}
	if admissionNotes(r.tracker) != 1 {
		t.Fatalf("notes = %q, want the admission note written once", r.tracker.NoteRecords)
	}
	settled, err := r.runs.Load(outcome.RunID)
	if err != nil || settled.MergeQueueNoteOwed != nil || settled.Outstanding() || !settled.AwaitingMergeQueue() {
		t.Fatalf("run = %#v, %v; want it waiting on the queue owing nothing", settled, err)
	}
	// Nothing is left for the next sweep, and nothing is written twice.
	if results, err := reconciler.Reconcile(context.Background()); err != nil || len(results) != 0 || admissionNotes(r.tracker) != 1 {
		t.Fatalf("Reconcile() = %#v, %v, notes %d; want nothing left to settle", results, err, admissionNotes(r.tracker))
	}
}

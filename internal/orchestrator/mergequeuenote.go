package orchestrator

// The note a run writes on its item once its approved change is in the merge
// queue (completeQueued) records something already true elsewhere: the queue's
// own record holds the entry, the run's record holds the admission, and the
// queue lands the change or hands it back whatever the item's notes say. So a
// tracker that will not take the note is no reason to stop the run, and no
// reason to hand anything to a person.
//
// The write is tried with backoff, read back before each try after the first
// so the note is never written twice, for at least as long as one tracker call
// is allowed (beads.DefaultTimeout). It is made under the run's context with
// its cancellation dropped, because a run ended while its process shuts down or
// drains for a restart has already put its change in the queue and owes only
// this. A note still not written after that is left on the run's record
// (runstate.MergeQueueNoteOwed), the run ends waiting on the queue exactly as
// it would have, and reconciliation, which the maintenance pass runs, writes it
// (Reconciler.retryMergeQueueNote).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// mergeQueueNoteRetryFor is how long the run waits between tries of the note
// before it leaves the note to reconciliation: at least one tracker call's
// whole bound, so a store slow enough to kill one call is given at least that
// long again to answer.
const mergeQueueNoteRetryFor = beads.DefaultTimeout

// noteQueueAdmission writes the admission note on the run's item, or leaves it
// owed on the run's record. It never fails the run: a note left owed, or one
// that could not even be recorded as owed, is said on the run's outcome
// (Outcome.MergeQueueNoteOwed).
func (a *activeRun) noteQueueAdmission(ctx context.Context, notes string) {
	p := a.pipeline
	ctx, cancel := trackerRecordContext(ctx)
	defer cancel()
	since := p.clock().Now()
	var waited time.Duration
	attempted := false
	for attempt := 1; ; attempt++ {
		err := a.writeAdmissionNote(ctx, notes, attempted)
		attempted = true
		if err == nil {
			return
		}
		delay := recovery.Interval(attempt)
		if waited >= mergeQueueNoteRetryFor || ctx.Err() != nil {
			a.oweAdmissionNote(ctx, notes, since, attempt, waited, err)
			return
		}
		if recordErr := a.recordRetry(runstate.RetryMergeQueueNote, attempt, delay, err); recordErr != nil {
			a.oweAdmissionNote(ctx, notes, since, attempt, waited, errors.Join(err, recordErr))
			return
		}
		if sleepErr := p.sleep(ctx, delay); sleepErr != nil {
			a.oweAdmissionNote(ctx, notes, since, attempt, waited, err)
			return
		}
		waited += delay
	}
}

// writeAdmissionNote is one try: after the first, the item is read back and a
// note that landed is not written again.
func (a *activeRun) writeAdmissionNote(ctx context.Context, notes string, readFirst bool) error {
	tracker := a.pipeline.Tracker
	if readFirst {
		item, err := tracker.Show(ctx, a.state.WorkItemID)
		if err != nil {
			return fmt.Errorf("could not read the item back to tell whether the note landed, so it was not written again: %w", err)
		}
		if notesCarry(item.Notes, notes) {
			return nil
		}
	}
	_, err := tracker.RecordOutcome(ctx, a.state.WorkItemID, notes)
	return err
}

// oweAdmissionNote leaves the note on the run's record for reconciliation.
// The queue may be recording on the run meanwhile, so it is a narrow update.
func (a *activeRun) oweAdmissionNote(ctx context.Context, notes string, since time.Time, attempts int, waited time.Duration, cause error) {
	p := a.pipeline
	owed := runstate.OwedMergeQueueNote(notes, since, attempts, waited, boundedFailureDetail(cause.Error()))
	a.outcome.MergeQueueNoteOwed = owed.Describe()
	written, err := p.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.MergeQueueNoteRecorded{Owed: &owed, At: p.clock().Now()})
	if err != nil {
		a.outcome.MergeQueueNoteOwed = fmt.Sprintf("the note saying the change was admitted to the merge queue could not be written on the item after %d tries over %s (%v), and recording it as owed failed too, so nothing will write it: %v", attempts, waited, cause, err)
		return
	}
	a.state = written
}

// retryMergeQueueNote is reconciliation writing the note a run left owed. The
// item is read first, so a note an earlier try landed without saying so is
// found rather than written twice. A run whose change has landed since has its
// landing note on the item, which says the same and more, so the owed note is
// let go unwritten. A failure keeps the note owed, with the try counted, for
// the next sweep.
func (r Reconciler) retryMergeQueueNote(ctx context.Context, state *runstate.State) error {
	owed := state.MergeQueueNoteOwed
	if owed == nil {
		return nil
	}
	clear := func() error {
		written, err := r.Store.Update(ctx, state.RunID, runstate.MergeQueueNoteRecorded{At: r.clock().Now()})
		if err != nil {
			return fmt.Errorf("record that the merge queue admission note is written: %w", err)
		}
		*state = written
		return nil
	}
	if state.Integration != nil {
		return clear()
	}
	err := func() error {
		item, err := r.Tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return err
		}
		if notesCarry(item.Notes, owed.Note) {
			return nil
		}
		_, err = r.Tracker.RecordOutcome(ctx, state.WorkItemID, owed.Note)
		return err
	}()
	if err == nil {
		return clear()
	}
	retried := runstate.OwedMergeQueueNote(owed.Note, owed.Since, owed.Attempts+1, time.Duration(owed.WaitedSeconds)*time.Second, boundedFailureDetail(err.Error()))
	if written, saveErr := r.Store.Update(ctx, state.RunID, runstate.MergeQueueNoteRecorded{Owed: &retried, At: r.clock().Now()}); saveErr == nil {
		*state = written
	}
	return fmt.Errorf("write the note saying run %s's approved change was admitted to the merge queue on %s, which is still owed and tried again by the next sweep: %w", state.RunID, state.WorkItemID, err)
}

package orchestrator

// A paused run whose note on its work item could not be written.
//
// Every pause makes itself durable on the run's record before it writes a note
// on the item, so the note is the operator's view of the pause and not the
// pause itself: a run whose note timed out against a slow tracker is exactly as
// paused, claimed, and resumable as one whose note was written. The pause
// reports the failed write rather than swallowing it, because an operator
// watching the tracker would otherwise see the item simply stop moving.
//
// It used to be reported as a plain error, and a plain error from a run is how
// the scheduler recognizes a run that failed. So a run paused for a move to a
// new build, whose note timed out, was counted towards the hold on new work as
// a blocked run. The error is typed so the scheduler can tell the two apart:
// the run is paused, and the note is a problem to say, not a stoppage.

import "fmt"

// PauseNoteError is a run that paused and made its pause durable, and whose
// note saying so on its work item could not be written.
type PauseNoteError struct {
	// What names the pause the note was for, as the sentence about it reads:
	// "the redeploy stop", "the pause", "the directive pause".
	What string
	Err  error
}

func (e PauseNoteError) Error() string {
	return fmt.Sprintf("record %s on the work item: %v", e.What, e.Err)
}

func (e PauseNoteError) Unwrap() error { return e.Err }

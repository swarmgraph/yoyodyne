package runstate

import (
	"strings"
	"time"
)

// ContinuationWait is why the scheduler last passed over continuing a paused
// run — one waiting on work its item depends on, or on the forge's checks — and
// since when it has said so: a developer slot that went to written work ahead of
// it, a hold or a pause, a dependency still open, a stop somebody asked for. A
// paused run's item is claimed, so the reason the queue keeps on an item it did
// not start never reaches it; this is the run's own record of the same thing,
// read by status. It is cleared as the run takes its slot back
// (Store.ReclaimSlot).
type ContinuationWait struct {
	Why string `json:"why"`
	// Since is when the scheduler first gave this reason; a later pull giving
	// the same reason leaves it alone.
	Since time.Time `json:"since"`
}

// ContinuationPassedOver records on a paused run why a pull did not continue
// it (State.ContinuationWait). It changes nothing on a run that is not paused
// waiting to be continued, or whose record already gives the same reason, so a
// pull every minute through a long wait writes once.
//
// The record's updated time is left alone: a paused run's place among the
// written work waiting for a slot is measured from that time
// (Scheduler.nextContinuations), and saying why it waits must not move it.
type ContinuationPassedOver struct {
	Why string
	At  time.Time
}

func (u ContinuationPassedOver) apply(state *State) (bool, error) {
	why := strings.TrimSpace(u.Why)
	if why == "" || !state.Status.InFlight() || !state.AwaitsContinuation() {
		return false, nil
	}
	if state.ContinuationWait != nil && state.ContinuationWait.Why == why {
		return false, nil
	}
	state.ContinuationWait = &ContinuationWait{Why: why, Since: u.At.UTC()}
	return true, nil
}

// AwaitsContinuation reports a run paused until the scheduler continues it:
// waiting on work its item depends on, waiting for its epic, or waiting for the
// forge's checks without having taken its developer slot back yet.
func (s State) AwaitsContinuation() bool {
	return s.DependencyPause != nil || s.EpicWait != nil || s.CIWait.Waiting()
}

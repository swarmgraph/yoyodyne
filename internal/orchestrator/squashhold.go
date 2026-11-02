package orchestrator

// A squash of the live tracker store that is due holds new starts by itself.
//
// The squash holds the store's exclusive lock for its whole length, so a run
// started into it can have its tracker reads time out over nothing it did. So
// the maintenance pass, once it has decided a squash is due, records a hold on
// the product, waits for the runs already going to finish, and squashes only
// once none is in flight (internal/maintain, trackersquash.go). A watching
// session reads that record at every pull and starts nothing while it stands:
// no chosen item, no carried-out decision, no continuation, no re-adopted run,
// and no document landing, because each of them reads the tracker. What is
// already running carries on and is collected as it ends.
//
// Nothing here lifts it. Maintenance lifts it however its step ends, and the
// record carries its own end, so one a dead pass left behind ends on its own.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ScheduleSquashHolds is the hold as a watching session reads it. It is
// satisfied by *runstate.TrackerSquashHoldStore.
type ScheduleSquashHolds interface {
	Standing(now time.Time) (runstate.TrackerSquashHold, bool, error)
}

// squashHeld reads whether a squash's hold on new runs stands. A pull wired
// without the record reads none. A record that cannot be read is said on the
// schedule and the pull is made as though none stood: a hold nobody can show
// was placed must not stop the line, and the squash itself still waits for
// no run to be in flight before it starts.
func (s Scheduler) squashHeld(schedule *Schedule, pull Pull) (runstate.TrackerSquashHold, bool) {
	schedule.SquashHold = nil
	if pull.SquashHolds == nil {
		schedule.SquashHoldProblem = ""
		return runstate.TrackerSquashHold{}, false
	}
	hold, standing, err := pull.SquashHolds.Standing(s.now())
	if err != nil {
		schedule.SquashHoldProblem = fmt.Sprintf("whether maintenance holds new runs for a tracker squash could not be read, so new runs were started as though it did not stand: %v", err)
		return runstate.TrackerSquashHold{}, false
	}
	schedule.SquashHoldProblem = ""
	if !standing {
		return runstate.TrackerSquashHold{}, false
	}
	schedule.SquashHold = &hold
	return hold, true
}

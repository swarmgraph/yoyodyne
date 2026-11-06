package checks

import (
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// LimitOwner is whose time limit a check was given as it launched.
type LimitOwner string

const (
	// LimitOfCheck is the check's own limit: execution.check_timeout, or the
	// limit the request gave its checks in its place.
	LimitOfCheck LimitOwner = "check"
	// LimitOfStage is what the stage's bound had left when the check launched,
	// where that was less than the check's own limit.
	LimitOfStage LimitOwner = "stage"
	// LimitOfRun is what the deadline of the work that started the run had left
	// when the check launched, where that was less than either of the others.
	LimitOfRun LimitOwner = "run"
)

// limitSlack is how far short of a limit a check stopped on time may read and
// still be said to have reached it: the process runner's timer starts a moment
// after the check's start is read, and the figures are said in whole seconds.
const limitSlack = time.Second

// TimeLimit says which limit stopped a check that ran out of time, and how long
// that limit was. A check's own limit and what the deadline left it are
// measured from when the check launched, and the stage's bound from when the
// stage began, so each is named only where the time measured the same way
// reached it. ok is false for a check that did not run out of time, for one the
// runner never launched for want of the caller, and for one stopped on time
// before any limit it was given had run out: what stopped that one is not a
// limit this record holds, and naming one the check had not reached is a
// message that contradicts itself.
func (r Result) TimeLimit() (LimitOwner, time.Duration, bool) {
	if r.Process.Status != execution.ProcessTimedOut || r.NotLaunched {
		return "", 0, false
	}
	switch {
	case r.StoppedByCaller:
		if r.LimitOf == LimitOfRun && r.Elapsed() >= r.Limit-limitSlack {
			return LimitOfRun, r.Limit, true
		}
	case r.StoppedByStage:
		if r.StageTimeout > 0 && r.StageElapsed >= r.StageTimeout-limitSlack {
			return LimitOfStage, r.StageTimeout, true
		}
	default:
		// Neither the caller nor the stage ended it, so the only limit that can
		// have is its own; a result recorded before Limit was reads the same way.
		if r.Timeout > 0 && r.Elapsed() >= r.Timeout-limitSlack {
			return LimitOfCheck, r.Timeout, true
		}
	}
	return "", 0, false
}

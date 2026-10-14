package checks

import (
	"context"
	"os"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A heavy check is one the project has said only so many of may run at once on
// one machine (execution.heavy_checks). Before the runner launches one it asks
// Permits for a place, and waits for it; a check that is not heavy is launched
// as it always was. The wait is not the check's: its budget, and the stage's
// bound, start when it launches, so a run kept waiting behind another run's
// race suite is never stopped for time it spent not running. A runner with no
// Permits, or a project that declares no heavy checks, launches every check
// the way it did before heavy checks existed.

// Permits gives out places for heavy checks.
type Permits interface {
	// Acquire waits for a place for ask's command and returns it, or returns
	// nil with no error for a command that is not heavy. waiting, where not
	// nil, is told once, as a wait begins, which check is being waited for and
	// since when. A wait cut short by ctx returns ctx's error.
	Acquire(ctx context.Context, ask PermitAsk, waiting func(check string, since time.Time)) (Permit, error)
}

// PermitAsk is what a run asks for a place with: whose check it is, and which
// of the orders a waiting check is let start in it is asked in.
type PermitAsk struct {
	RunID      string
	WorkItemID string
	Commit     string
	Command    string
	Priority   domain.HeavyCheckPriority
}

// Permit is one place, given to one launch of one check.
type Permit interface {
	// Check is the heavy check's name the place is counted under.
	Check() string
	// Waited is how long the run waited for the place.
	Waited() time.Duration
	// Hold is a file the check's process and everything it starts inherit, so
	// whether any of them is still running can be read after the harness that
	// started them is gone.
	Hold() *os.File
	// Register writes the check's process down before it does any work.
	Register(execution.StartedProcess) error
	// Finish records how the check ended, and the time limit it launched with
	// where it launched, and gives the place back once every process it
	// started is gone.
	Finish(result execution.ProcessResult, budget time.Duration) error
}

// PermitRequest is what one stage's checks ask their places with. A request
// that leaves it out asks in the ordinary order.
type PermitRequest struct {
	WorkItemID string
	Commit     string
	Priority   domain.HeavyCheckPriority
	// Waiting is told as a check begins waiting for a place, and Granted as it
	// is given one, with how long it waited, so a run's record can say it is
	// waiting rather than running while it waits. Both are optional.
	Waiting func(check string, since time.Time)
	Granted func(check string, waited time.Duration)
}

// gateWithPermit is the launch gate a heavy check is started behind: the
// caller's own, where it has one, holding the permit's file too and writing
// the process down to the permit first; or one of the permit's alone.
func gateWithPermit(gate *execution.LaunchGate, permit Permit) *execution.LaunchGate {
	if permit == nil {
		return gate
	}
	if gate == nil {
		return &execution.LaunchGate{Hold: permit.Hold(), Register: permit.Register}
	}
	register := gate.Register
	return &execution.LaunchGate{
		Hold:  gate.Hold,
		Holds: append(append([]*os.File(nil), gate.Holds...), permit.Hold()),
		Register: func(process execution.StartedProcess) error {
			if err := permit.Register(process); err != nil {
				return err
			}
			return register(process)
		},
	}
}

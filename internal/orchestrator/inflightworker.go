package orchestrator

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// runWorkers reads whether a run's worker is still working on it, by the one
// rule every reader of a worker applies (runstate.Store.Worker). It is
// satisfied by *runstate.Store; a run store that cannot say leaves the account
// of an in-flight run without it.
type runWorkers interface {
	Worker(state runstate.State, now time.Time) runstate.WorkerReading
}

// startedByThisProgram reports a reserved run whose worker is the program
// running in this process. The scheduler replaces its program in place to take
// up a new build, so a run whose worker names this process's number from an
// earlier program is one an earlier program started, and never this session's.
func startedByThisProgram(state runstate.State) bool {
	return state.Worker != nil && state.Worker.ThisProgram()
}

// inFlightWorker says, for a run in flight over an item that keeps a decision
// back, whose run it is and whether its worker is still working on it, in a
// clause a person reads after the reason the decision waits. A run whose worker
// is gone names what ends it, because the decision waits on that and nothing
// else; it is the same remedy every surface names for a run with no process
// behind it.
func inFlightWorker(runs any, state runstate.State, now time.Time) string {
	whose := "it was not started by this session"
	if startedByThisProgram(state) {
		whose = "it is this session's own"
	}
	workers, ok := runs.(runWorkers)
	if !ok || state.RunID == "" {
		return whose
	}
	reading := workers.Worker(state, now)
	switch reading.Liveness {
	case runstate.WorkerAbsent:
		return fmt.Sprintf("%s, and its worker is gone: %s; %s", whose, reading.Says, runstate.DeadRunRemedy(state, DefaultVanishedGrace))
	case runstate.WorkerLive:
		return fmt.Sprintf("%s, and its worker is working on it: %s", whose, reading.Says)
	}
	return fmt.Sprintf("%s, and whether its worker is still working on it is not known: %s", whose, reading.Says)
}

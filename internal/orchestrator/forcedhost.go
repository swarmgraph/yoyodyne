package orchestrator

// The runs of a watch session the supervisor forced to stop.
//
// A session asked to stop for a move onto a new build records, on every run it
// stops, the stop the session that comes back continues from (redeploydrain.go).
// A session that would not stop when asked, and was killed by the supervisor,
// recorded nothing: its runs are left in flight with no process behind them,
// which the maintenance sweep settles as interrupted and abandons, and the
// change each had written is never continued. PreserveRunsOfForcedHost writes
// that stop on the session's behalf, once the session is gone and before the
// supervisor starts the next one, so each run is continued where it was — in
// the worktree it has, with what it wrote kept, and the developer session it
// established — as a run the drain stopped is.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ForcedHost is the watch session the supervisor forced to stop: its process,
// as the operating system reported it before the stop, its session, and why it
// was forced, in words a person reads on each run's record.
type ForcedHost struct {
	PID       int
	StartedAt time.Time
	SessionID string
	At        time.Time
	// Bound is the wind-down limit the session was given
	// (execution.redeploy_drain_limit), recorded as the drain records it.
	Bound time.Duration
	Why   string
}

// ForcedHostRuns is what PreserveRunsOfForcedHost did, by run.
type ForcedHostRuns struct {
	// Preserved is each run now carrying a stop the next session continues.
	Preserved []string
	// Left is each run the forced session was working that is at a step the
	// stop does not cover — its promotion, or its cleanup — and is left to the
	// maintenance sweep, which settles those from the repository.
	Left []string
}

// PreserveRunsOfForcedHost records a stop on every run still in flight whose
// worker was the forced process, so the session started in its place continues
// each one rather than the maintenance sweep abandoning it. A run already
// carrying something that says how it is continued is left as it is. It must
// be called only once the forced process is gone: a run it still held could be
// written over by it.
func PreserveRunsOfForcedHost(ctx context.Context, store *runstate.Store, host ForcedHost) (ForcedHostRuns, error) {
	var done ForcedHostRuns
	if store == nil || host.PID <= 0 {
		return done, nil
	}
	states, _, err := store.IncompleteReadable()
	if err != nil {
		return done, fmt.Errorf("read the runs in flight: %w", err)
	}
	worker := runstate.WorkerIdentity{PID: host.PID, StartedAt: host.StartedAt}
	stop := runstate.RedeployStop{
		At:           host.At.UTC(),
		BoundSeconds: int64(host.Bound / time.Second),
		SessionID:    host.SessionID,
		CutOff:       oneline.Fold(host.Why, runstate.MaxRedeployCutOffBytes/2),
	}
	var problems []error
	for _, state := range states {
		if state.Status != runstate.StatusRunning || state.Worker == nil || state.Worker.PID != host.PID || state.RedeployStop != nil {
			continue
		}
		if resumable(state) || owedUpdateContinuation(state) {
			continue
		}
		switch state.Phase {
		case runstate.PhaseDeveloping, runstate.PhaseChecking, runstate.PhaseReviewing:
		default:
			done.Left = append(done.Left, state.RunID)
			continue
		}
		written, err := store.Update(ctx, state.RunID, runstate.HostForcedStopRecorded{Host: worker, Stop: stop})
		if err != nil {
			problems = append(problems, fmt.Errorf("record the forced stop on run %s: %w", state.RunID, err))
			continue
		}
		// The update checks the worker's start time as well as its number, and
		// changes nothing on a run a later process with the same number holds.
		if written.RedeployStop != nil && written.RedeployStop.At.Equal(stop.At) {
			done.Preserved = append(done.Preserved, state.RunID)
		}
	}
	slices.Sort(done.Preserved)
	slices.Sort(done.Left)
	return done, errors.Join(problems...)
}

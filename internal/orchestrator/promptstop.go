package orchestrator

// How a recorded stop reaches a run that is working, and what it does to a
// change that has already passed its checks.
//
// A stop is read at the run's step boundaries (stopRequested). Two steps can
// each run for an hour — a developer attempt and the check stage — and a stop
// recorded at the start of one used to wait for both, then reach the run at its
// review and cancel a change that had just passed its checks. So each of those
// steps is watched while it runs, and a stop that has waited
// runstate.StopTakesEffectWithin is carried out by cutting the step short; a
// developer attempt cut short has what it wrote committed first, so the change
// is kept on the run's branch as any stopped run's is.
//
// A stop that reaches the run only after the checks passed on the change it
// holds is not carried out at all (honorStop): the change is kept and goes on to
// its review, and the run records that the stop arrived late.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stopWatchInterval is how often a watched step reads whether a stop was
// recorded, the same interval a wait reads whether it was released.
const stopWatchInterval = releaseCheckInterval

// watchForStop returns a context for one long step that is cancelled once a
// stop recorded on this run has waited runstate.StopTakesEffectWithin, and a
// function the caller calls as soon as the step returns. That function ends the
// watch, waiting for it to have ended, and reports the stop if it was what cut
// the step short. step names the step in the words the run's ending says it in.
//
// The watch ends with the step whatever happens to it, because the function
// is the only way the caller gets the step's result and it always stops the
// watching goroutine before returning.
func (a *activeRun) watchForStop(ctx context.Context, step string) (context.Context, func() (operatorStop, bool)) {
	p := a.pipeline
	interval := p.StopWatchInterval
	if interval <= 0 {
		interval = stopWatchInterval
	}
	runID := a.state.RunID
	watched, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	finished := make(chan struct{})
	var (
		cut    operatorStop
		wasCut bool
	)
	go func() {
		defer close(finished)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-watched.Done():
				return
			case <-ticker.C:
			}
			// A record nobody can read cuts nothing short, for the reason
			// stopRequested gives; the boundary after the step reports it.
			request, requested, err := p.Store.StopRequested(runID)
			if err != nil || !requested {
				continue
			}
			now := p.clock().Now()
			if now.Before(request.TakesEffectBy()) {
				continue
			}
			cut = operatorStop{request: request, at: now, cut: step}
			wasCut = true
			cancel(cut)
			return
		}
	}()
	return watched, func() (operatorStop, bool) {
		close(done)
		<-finished
		cancel(nil)
		return cut, wasCut
	}
}

// honorStop is stopRequested at a boundary the run can reach after its checks
// have passed on the change it holds: the top of each round of checks and
// review, the review itself, and a resumed run. A stop found there before the
// checks passed is carried out as at any boundary. One found after they passed
// arrived late: the change is kept and goes on to its review, the stop is
// recorded on the run as having arrived late, and nothing is cancelled. A
// repair the review hands back is a new attempt, so a stop still recorded then
// is carried out at that attempt's boundary in develop.
func (a *activeRun) honorStop() error {
	err := a.stopRequested()
	var stopped operatorStop
	if !errors.As(err, &stopped) {
		return err
	}
	passed, kept := checksPassedOnHeldChange(a.state)
	if !kept {
		return err
	}
	if late := a.state.LateStop; late != nil && late.RequestedAt.Equal(stopped.request.RequestedAt.UTC()) {
		return nil
	}
	now := a.pipeline.clock().Now()
	late := runstate.NewLateStop(stopped.request, passed.At, now)
	held := a.state.Revision
	written, err := a.pipeline.Store.Update(context.Background(), a.state.RunID, runstate.LateStopRecorded{Stop: late, At: now})
	if err != nil {
		return fmt.Errorf("record the stop that arrived after the checks passed: %w", err)
	}
	// Only the fields the update wrote are taken onto the run's copy where
	// nothing else wrote the record in between, as adoptInstallUpdate does.
	if written.Revision == held+1 {
		a.state.LateStop, a.state.Revision, a.state.UpdatedAt = written.LateStop, written.Revision, written.UpdatedAt
	} else {
		a.state = written
	}
	a.outcome.LateStop = a.state.LateStop
	return nil
}

// checksPassedOnHeldChange reports the checks having passed on the attempt the
// run holds now, which is what makes a stop found after them a late one. A
// repair handed back since is a new attempt and does not count.
func checksPassedOnHeldChange(state runstate.State) (*runstate.ChecksPassed, bool) {
	passed := state.ChecksPassed
	if passed == nil || passed.Attempt != state.RepairAttempts {
		return nil, false
	}
	return passed, true
}

// recordLateStopOnUnheldRun is the sweep's half of honorStop, for a run no
// process holds whose checks have passed on the change it holds: the stop is
// recorded as having arrived late, and the run is left for the rest of the
// sweep to settle as it would any run in flight, so its own pipeline resumes it
// to its review rather than the sweep cancelling a change that has just earned
// its checks. It reports whether the stop was such a stop.
func (r Reconciler) recordLateStopOnUnheldRun(ctx context.Context, state runstate.State, request runstate.StopRequest) (runstate.State, bool, error) {
	passed, kept := checksPassedOnHeldChange(state)
	if !kept {
		return state, false, nil
	}
	now := r.clock().Now()
	late := runstate.NewLateStop(request, passed.At, now)
	if existing := state.LateStop; existing != nil && existing.RequestedAt.Equal(late.RequestedAt) {
		return state, true, nil
	}
	written, err := r.Store.Update(ctx, state.RunID, runstate.LateStopRecorded{Stop: late, At: now})
	if err != nil {
		return state, true, fmt.Errorf("record the stop that reached run %s after its checks passed: %w", state.RunID, err)
	}
	return written, true, nil
}

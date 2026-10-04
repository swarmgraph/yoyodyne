package orchestratortest

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"sync"
)

// ClaimState records the tracker releases, run changes, and release log an audit makes.
type ClaimState struct {
	Mu sync.Mutex
	// released is every item this harness was asked to give back, with the note
	// the audit wrote onto it.
	Released map[string]string
	// order is the identifiers in the order they were released, so a test can say
	// what a sweep of several did.
	Order []string
	Runs  []runstate.State
	Log   []runstate.ReleasedClaim
	// saved is every run record the audit ended, by run, which is the half of a
	// release that actually frees the developer slot.
	Saved map[string]runstate.State
	// held names the runs whose lease a live process owns, which is the only
	// answer about liveness that is not a guess.
	Held map[string]bool
	// adopted names the runs whose lease this audit took, so a test can say that a
	// claim was settled under one rather than written to from outside.
	Adopted []string
	// parkOnAdopt replaces what a run's record says once its lease is taken, which
	// is how a run that parked between the reading and the lease is driven.
	ParkOnAdopt map[string]runstate.State
	// failRelease, failAppend, failRuns, failAdopt, and failSave stand in for a
	// tracker that refuses, a log that cannot be written, and a run store that
	// will not answer or will not be written.
	FailRelease error
	FailAppend  error
	FailRuns    error
	FailAdopt   error
	FailSave    error
}

// AdoptRun stands in for taking a run's lease. The nil lease is what a released
// one is: runstate.Lease.Release tolerates it, so a fake needs nothing more.
//
// A terminal run is refused outright, which the real store does not do — the
// point is the opposite of imitation. How runstate.Store answers for a record
// nobody holds is a question the audit must not depend on, so this fake makes
// asking it a failure rather than something that quietly works here and is
// decided by the store in production.
func (h *ClaimState) AdoptRun(_ context.Context, runID string) (runstate.State, *runstate.Lease, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if h.FailAdopt != nil {
		return runstate.State{}, nil, h.FailAdopt
	}
	if h.Held[runID] {
		return runstate.State{}, nil, runstate.ErrRunHeld
	}
	for _, run := range h.Runs {
		if run.RunID == runID && run.Status.Terminal() {
			return runstate.State{}, nil, fmt.Errorf("run %s already ended and must not be taken up to be settled", runID)
		}
	}
	h.Adopted = append(h.Adopted, runID)
	if parked, changed := h.ParkOnAdopt[runID]; changed {
		return parked, nil, nil
	}
	for _, run := range h.Runs {
		if run.RunID == runID {
			return run, nil, nil
		}
	}
	return runstate.State{}, nil, errors.New("no such run")
}

func (h *ClaimState) Save(state runstate.State) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if h.FailSave != nil {
		return h.FailSave
	}
	h.Saved[state.RunID] = state
	for index, run := range h.Runs {
		if run.RunID == state.RunID {
			h.Runs[index] = state
		}
	}
	return nil
}

func (h *ClaimState) Release(_ context.Context, id, reason string) (beads.WorkItem, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if h.FailRelease != nil {
		return beads.WorkItem{}, h.FailRelease
	}
	h.Released[id] = reason
	h.Order = append(h.Order, id)
	return beads.WorkItem{ID: id, Status: "open"}, nil
}

func (h *ClaimState) Recorded() ([]runstate.State, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if h.FailRuns != nil {
		return nil, h.FailRuns
	}
	return h.Runs, nil
}

func (h *ClaimState) Append(Released runstate.ReleasedClaim) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if h.FailAppend != nil {
		return h.FailAppend
	}
	h.Log = append(h.Log, Released)
	return nil
}

func (h *ClaimState) List() ([]runstate.ReleasedClaim, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	return h.Log, nil
}

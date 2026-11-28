package runstate

import (
	"errors"
	"fmt"
	"time"
)

// SlotGivenBack puts back on a run the wait a take-up lifted when it took the
// run's developer slot back (ReclaimSlot), once the take-up was refused before
// the run got under way: paused on its item's dependencies, waiting for its
// epic, or waiting on GitHub's checks. With the wait back the run holds no
// developer slot, and the next pull offers it again exactly as before
// (orchestrator's takeup.go).
//
// It changes nothing on a run that already holds no slot by its record — one
// back to waiting, or one the refusal ended — so giving the slot back twice is
// safe. A GitHub-checks wait is put back as not holding the slot, whatever the
// copy it was given says.
type SlotGivenBack struct {
	DependencyPause  *DependencyPause
	EpicWait         *EpicWait
	ContinuationWait *ContinuationWait
	CIWait           *CIWait
	At               time.Time
}

func (u SlotGivenBack) apply(state *State) (bool, error) {
	if u.DependencyPause == nil && u.EpicWait == nil && u.CIWait == nil {
		return false, errors.New("a slot is given back by putting back the wait the run was taken up from, and none was named")
	}
	if !state.Status.InFlight() || !state.HoldsDeveloperSlot() {
		return false, nil
	}
	if u.DependencyPause != nil {
		pause := *u.DependencyPause
		state.DependencyPause = &pause
	}
	if u.EpicWait != nil {
		wait := *u.EpicWait
		state.EpicWait = &wait
	}
	if u.ContinuationWait != nil {
		wait := *u.ContinuationWait
		state.ContinuationWait = &wait
	}
	if u.CIWait != nil {
		wait := *u.CIWait
		wait.SlotTaken = false
		state.CIWait = &wait
	}
	if state.HoldsDeveloperSlot() {
		return false, fmt.Errorf("putting back the wait run %s was taken up from left it holding a developer slot", state.RunID)
	}
	state.UpdatedAt = u.At
	return true, nil
}

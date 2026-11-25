package runstate

import (
	"errors"
	"time"
)

// EpicWait is a run that stopped before its next call to a provider because its
// work item is in no open epic the operator named. The run keeps its claim, its
// branch and its checkout, and is picked up again when the item is added to an
// epic or epic mode is turned off.
type EpicWait struct {
	// Since is when the run stopped.
	Since time.Time `json:"since"`
}

// Validate rejects a wait that does not say when it began.
func (w EpicWait) Validate() error {
	if w.Since.IsZero() {
		return errors.New("since is required")
	}
	return nil
}

// Summary is what the run waits for, as the object of "waiting on".
func (w EpicWait) Summary() string {
	return "its item being in an epic the operator named, or epic mode being turned off"
}

// EpicWaitStarted records that a run stops to wait for its epic, which gives its
// developer slot back. It changes nothing on a run that already waits, or that
// has no checkout to wait with. The session, commit and sequence are what the
// step the run just finished left, where its holder had not yet saved them.
type EpicWaitStarted struct {
	SessionID     string
	HarnessCommit string
	LastSequence  uint64
	At            time.Time
}

func (u EpicWaitStarted) apply(state *State) (bool, error) {
	if state.EpicWait != nil || state.Status != StatusRunning || state.WorktreePath == "" || state.Branch == "" || state.BaseCommit == "" {
		return false, nil
	}
	state.EpicWait = &EpicWait{Since: u.At.UTC()}
	if u.SessionID != "" {
		state.ProviderSessionID = u.SessionID
	}
	if u.HarnessCommit != "" {
		state.HarnessCommit = u.HarnessCommit
	}
	state.LastSequence = max(state.LastSequence, u.LastSequence)
	state.UpdatedAt = u.At
	return true, nil
}

// TeamConflictDropped ends a run that resolves a team branch conflict and waits
// for its epic, because a branch moved under it and nobody would land its merge.
type TeamConflictDropped struct {
	Why string
	At  time.Time
}

func (u TeamConflictDropped) apply(state *State) (bool, error) {
	if state.TeamConflict == nil || state.EpicWait == nil {
		return false, nil
	}
	completedAt := u.At
	state.EpicWait, state.RedeployStop, state.OperatorHeldSince = nil, nil, nil
	state.UsageLimitResetsAt, state.UsageLimitPausedSince, state.UsageLimitResetUnknown = nil, nil, false
	state.PauseCause, state.ProviderStop = "", ""
	state.Status, state.Failure = StatusCancelled, RecordFailure(u.Why)
	state.UpdatedAt, state.CompletedAt = u.At, &completedAt
	return true, nil
}

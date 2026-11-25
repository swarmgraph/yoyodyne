package orchestrator

// A run already started whose item is in none of the epics the operator named.
// It finishes the step it is in and waits before its next call to a provider,
// with its claim, branch and checkout kept and its developer slot given back
// (runstate.EpicWait). Taking up a run that waited or was interrupted asks first,
// so a run put down before epic mode was turned on waits the same way. A watching
// session's pull, or whatever hosts the run otherwise, takes it up once it may go
// on.
//
// A run that publishes a document or brings an outside file in is not asked: it
// holds no developer slot and carries on to its end. Nor is a change whose
// promotion is recorded, or one in the merge queue, which no run carries.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// refuseContinuing is why a run already started may not go on, and nil where it
// may.
func (d StartDecision) refuseContinuing(state runstate.State) *OutsideNamedEpicsError {
	if state.HarnessAuthored() || state.Integration != nil {
		return nil
	}
	path := maystart.PickUpRun
	if state.TeamConflict != nil {
		path = maystart.ResolveConflict
	}
	refused := d.refuseRun(path, state.WorkItemID)
	if refused != nil {
		refused.RunID = state.RunID
	}
	return refused
}

// epicWaitRuns is the run store's part in recording a wait.
type epicWaitRuns interface {
	Update(ctx context.Context, runID string, update runstate.RunUpdate) (runstate.State, error)
}

// recordEpicWait records the wait on a run whose lease the caller holds, and
// writes nothing where the run already waits.
func recordEpicWait(ctx context.Context, runs epicWaitRuns, state runstate.State, now time.Time) (runstate.State, error) {
	waiting, err := runs.Update(ctx, state.RunID, runstate.EpicWaitStarted{
		SessionID: state.ProviderSessionID, HarnessCommit: state.HarnessCommit, LastSequence: state.LastSequence, At: now,
	})
	if err != nil {
		return state, fmt.Errorf("record that run %s waits for its epic: %w", state.RunID, err)
	}
	return waiting, nil
}

// pausedForEpic reports a run waiting for its epic that can be taken up again.
func pausedForEpic(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.EpicWait == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// waitForEpic is asked of every run the pipeline adopts, before it takes a
// developer slot or resumes anything. A refusal leaves the run waiting.
func (p Pipeline) waitForEpic(ctx context.Context, state runstate.State) error {
	refused := p.MayStart.refuseContinuing(state)
	if refused == nil {
		return nil
	}
	waiting, err := recordEpicWait(ctx, p.Store, state, p.clock().Now())
	if err != nil {
		return errors.Join(refused, err)
	}
	if state.EpicWait == nil && waiting.EpicWait != nil {
		// The wait is durable, so a note that could not be written costs nothing.
		noteCtx, cancel := trackerCallContext(ctx)
		defer cancel()
		_, _ = p.Tracker.RecordOutcome(noteCtx, state.WorkItemID, renderEpicWaitNotes(state, refused))
	}
	return refused
}

// epicPause is a run that stopped before its next call to a provider to wait for
// its epic. Like the dependency pause it is an error only so that it travels the
// path a stopped step travels.
type epicPause struct{ refused *OutsideNamedEpicsError }

func (e epicPause) Error() string { return e.refused.Error() }

// holdForEpic stops a run before a call to a provider where its item may not go
// on, and does nothing otherwise.
func (a *activeRun) holdForEpic(ctx context.Context) error {
	refused := a.pipeline.MayStart.refuseContinuing(a.state)
	if refused == nil {
		return nil
	}
	waiting, err := recordEpicWait(ctx, a.pipeline.Store, a.state, a.pipeline.clock().Now())
	if err != nil {
		return err
	}
	a.state = waiting
	return epicPause{refused: refused}
}

// pauseForEpic reports the run as waiting, on the terms every pause keeps.
func (a *activeRun) pauseForEpic(paused epicPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	a.outcome.PausedByEpic = paused.refused
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := runTrackerContext(a.state.RunID, a.state.WorkItemID)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderEpicWaitNotes(a.state, paused.refused)); err != nil {
		return a.outcome, PauseNoteError{What: "the wait for its epic", Err: err}
	}
	return a.outcome, nil
}

func renderEpicWaitNotes(state runstate.State, refused *OutsideNamedEpicsError) string {
	return strings.Join([]string{
		"Yoyodyne stopped this run before its next call to a provider: " + refused.Error() + ". Nothing about the change was judged.",
		"Run: " + state.RunID,
		"Branch: " + state.Branch,
		"Worktree: " + state.WorktreePath,
		"This item stays claimed and its branch, worktree and developer session are kept. Its developer slot is given back, and a watching `yoyo work` session continues the same run once it may go on.",
	}, "\n")
}

// leftWaiting is a continuation that found its run may not go on yet, or that
// found no developer slot for a run that gave its own back: neither is a failure,
// and the run waits as it stood.
func leftWaiting(err error) bool {
	var outside *OutsideNamedEpicsError
	var capacity runstate.CapacityError
	return errors.As(err, &outside) || errors.As(err, &capacity)
}

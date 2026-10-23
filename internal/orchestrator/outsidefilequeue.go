package orchestrator

// What the merge queue reads and writes about a run that brings a file in.
//
// With execution.merge_queue on, such a run is admitted to the queue like any
// approved change (admitsToMergeQueue): nothing else replays a change onto a
// target that moved, so one kept out of the queue could land on a target its
// checks never saw. The queue reads the work item a change is for and writes
// its notes, its block, and its closure there; a run that brings a file in is
// recorded under a bookkeeping item of its own that the tracker does not hold
// (runstate.OutsideFileLanding.WorkItemID). OutsideFileItems answers for that
// item from the run's record and writes what the queue says onto the item the
// record names in `for`, so the queue's own steps — the candidate's review,
// its landing, a hand-back — work unchanged.
//
// A hand-back hands nothing to a developer: there is none, and the queue's
// hand-back only ends the run with its reason recorded. The next pull reads
// that ending (settleOutsideFile), counts it against the record, and starts a
// fresh run from the record as the target branch then holds it; three in a
// row stop the record and file one report for the development manager.

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// OutsideFileItems is the tracker the merge queue is given: every work item is
// the tracker's own, and the bookkeeping item of a run that brings a file in
// is answered from that run's record.
type OutsideFileItems struct {
	WorkTracker
	Runs interface {
		Load(runID string) (runstate.State, error)
	}
}

// landing is the run behind a bookkeeping item, and false for a work item the
// tracker holds.
func (t OutsideFileItems) landing(id string) (runstate.State, bool, error) {
	runID, ours := runstate.OutsideFileRunOfItem(id)
	if !ours {
		return runstate.State{}, false, nil
	}
	state, err := t.Runs.Load(runID)
	if err != nil {
		return runstate.State{}, true, fmt.Errorf("read run %s for %s: %w", runID, id, err)
	}
	if state.OutsideFile == nil {
		return runstate.State{}, true, fmt.Errorf("run %s does not bring a file in", runID)
	}
	return state, true, nil
}

// item is the bookkeeping item as the queue reads it.
func (OutsideFileItems) item(state runstate.State, status string) beads.WorkItem {
	item := outsideFileItem(*state.OutsideFile)
	item.Status = status
	return item
}

// status is the bookkeeping item's status as its run's record stands.
func outsideFileItemStatus(state runstate.State) string {
	if state.Status.Terminal() && state.Status != runstate.StatusSucceeded {
		return "blocked"
	}
	return "in_progress"
}

// note writes what the queue said onto the item the record is for.
func (t OutsideFileItems) note(ctx context.Context, state runstate.State, what string) error {
	landing := state.OutsideFile
	_, err := t.WorkTracker.RecordOutcome(ctx, landing.For, fmt.Sprintf("Bringing in %s (run %s): %s", outsidefile.PinnedPath(landing.Record), state.RunID, what))
	return err
}

func (t OutsideFileItems) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	state, ours, err := t.landing(id)
	if !ours {
		return t.WorkTracker.Show(ctx, id)
	}
	if err != nil {
		return beads.WorkItem{}, err
	}
	return t.item(state, outsideFileItemStatus(state)), nil
}

func (t OutsideFileItems) Claim(ctx context.Context, id string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	state, ours, err := t.landing(id)
	if !ours {
		return t.WorkTracker.Claim(ctx, id)
	}
	if err != nil {
		return beads.WorkItem{}, nil, err
	}
	return t.item(state, "in_progress"), nil, nil
}

func (t OutsideFileItems) settle(ctx context.Context, id, what, status string) (beads.WorkItem, error) {
	state, _, err := t.landing(id)
	if err != nil {
		return beads.WorkItem{}, err
	}
	if err := t.note(ctx, state, what); err != nil {
		return beads.WorkItem{}, err
	}
	return t.item(state, status), nil
}

func (t OutsideFileItems) RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error) {
	if _, ours := runstate.OutsideFileRunOfItem(id); !ours {
		return t.WorkTracker.RecordOutcome(ctx, id, notes)
	}
	state, _, err := t.landing(id)
	if err != nil {
		return beads.WorkItem{}, err
	}
	return t.settle(ctx, id, notes, outsideFileItemStatus(state))
}

func (t OutsideFileItems) Block(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	if _, ours := runstate.OutsideFileRunOfItem(id); !ours {
		return t.WorkTracker.Block(ctx, id, reason)
	}
	return t.settle(ctx, id, reason+" No developer is asked to repair it; a later pull starts a fresh run from the record.", "blocked")
}

func (t OutsideFileItems) Release(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	if _, ours := runstate.OutsideFileRunOfItem(id); !ours {
		return t.WorkTracker.Release(ctx, id, reason)
	}
	return t.settle(ctx, id, reason, "open")
}

func (t OutsideFileItems) Complete(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	if _, ours := runstate.OutsideFileRunOfItem(id); !ours {
		return t.WorkTracker.Complete(ctx, id, reason)
	}
	return t.settle(ctx, id, "the file is brought in. "+reason, "closed")
}

func (t OutsideFileItems) Reopen(ctx context.Context, id, reason string, parking domain.WorkItemParking) (beads.WorkItem, error) {
	if _, ours := runstate.OutsideFileRunOfItem(id); !ours {
		return t.WorkTracker.Reopen(ctx, id, reason, parking)
	}
	return t.settle(ctx, id, reason, "open")
}

func (t OutsideFileItems) AddBlocker(ctx context.Context, id, blockerID string) error {
	if _, ours := runstate.OutsideFileRunOfItem(id); !ours {
		return t.WorkTracker.AddBlocker(ctx, id, blockerID)
	}
	return errors.New("a run that brings a file in waits on no work item")
}

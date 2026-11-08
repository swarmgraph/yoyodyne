package orchestrator

// What the merge queue reads and writes about a run of the harness's own: a
// run that brings a file in, and a run that lands a confirmed document.
//
// With execution.merge_queue on, either is admitted to the queue like any
// approved change (admitsToMergeQueue): nothing else replays a change onto a
// target that moved, so one kept out of the queue could land on a target its
// checks never saw. The queue reads the work item a change is for and writes
// its notes, its block, and its closure there; a run of the harness's own is
// recorded under a bookkeeping item the tracker does not hold — a run that
// brings a file in under runstate.OutsideFileLanding.WorkItemID, a document run
// under its own run identifier (runstate.DocumentPublication.RunIDFor).
// HarnessRunItems answers for that item from the run's record and writes what
// the queue says onto the work item the run is for — the item a pinned file's
// record names in `for`, or the item a document's latest revision names — so
// the queue's own steps — the candidate's review, its landing, a hand-back —
// work unchanged.
//
// A hand-back hands nothing to a developer: there is none, and the queue's
// hand-back only ends the run with its reason recorded. For a run that brings
// a file in, the next pull reads that ending (settleOutsideFile), counts it
// against the record, and starts a fresh run from the record as the target
// branch then holds it; three in a row stop the record and file one report for
// the development manager. A document run's ending is read by its conversation
// the way any document run's is (documentDelivery): the document goes back to
// its owner.

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// HarnessRunItems is the tracker the merge queue is given: every work item is
// the tracker's own, and the bookkeeping item of a run of the harness's own is
// answered from that run's record.
type HarnessRunItems struct {
	WorkTracker
	Runs interface {
		Load(runID string) (runstate.State, error)
	}
}

// runOfItem is the run a bookkeeping item names, and false for a work item the
// tracker holds. A document run's item is its run identifier, which no work
// item the tracker holds can be.
func runOfItem(id string) (string, bool) {
	if runID, ours := runstate.OutsideFileRunOfItem(id); ours {
		return runID, true
	}
	if runstate.ValidRunID(id) {
		return id, true
	}
	return "", false
}

// landing is the run behind a bookkeeping item, and false for a work item the
// tracker holds.
func (t HarnessRunItems) landing(id string) (runstate.State, bool, error) {
	runID, ours := runOfItem(id)
	if !ours {
		return runstate.State{}, false, nil
	}
	state, err := t.Runs.Load(runID)
	if err != nil {
		return runstate.State{}, true, fmt.Errorf("read run %s for %s: %w", runID, id, err)
	}
	if state.OutsideFile == nil && (state.Document == nil || state.WorkItemID != id) {
		return runstate.State{}, true, fmt.Errorf("run %s is not a run of the harness's own recorded under %s", runID, id)
	}
	return state, true, nil
}

// item is the bookkeeping item as the queue reads it: the one the run was
// recorded under, whose description carries what its reviewer is told and the
// paths it may change.
func (HarnessRunItems) item(state runstate.State, status string) beads.WorkItem {
	var item beads.WorkItem
	if state.OutsideFile != nil {
		item = outsideFileItem(*state.OutsideFile)
	} else {
		item = documentItem(*state.Document, 0)
		item.ID, item.Title = state.WorkItemID, nonEmpty(state.WorkItemTitle, item.Title)
	}
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

// note writes what the queue said onto the item the run is for. A document
// whose latest revision names no item the tracker holds has nowhere to be
// told, as with any document run (documentItemRecorder).
func (t HarnessRunItems) note(ctx context.Context, state runstate.State, what string) error {
	if document := state.Document; document != nil {
		documentItemRecorder{tracker: t.WorkTracker, document: *document}.note(ctx, fmt.Sprintf("Publishing %s (run %s): %s", document.Candidate.Artifact.Path, state.RunID, what))
		return nil
	}
	landing := state.OutsideFile
	_, err := t.WorkTracker.RecordOutcome(ctx, landing.For, fmt.Sprintf("Bringing in %s (run %s): %s", outsidefile.PinnedPath(landing.Record), state.RunID, what))
	return err
}

func (t HarnessRunItems) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	state, ours, err := t.landing(id)
	if !ours {
		return t.WorkTracker.Show(ctx, id)
	}
	if err != nil {
		return beads.WorkItem{}, err
	}
	return t.item(state, outsideFileItemStatus(state)), nil
}

func (t HarnessRunItems) Claim(ctx context.Context, id string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	state, ours, err := t.landing(id)
	if !ours {
		return t.WorkTracker.Claim(ctx, id)
	}
	if err != nil {
		return beads.WorkItem{}, nil, err
	}
	return t.item(state, "in_progress"), nil, nil
}

func (t HarnessRunItems) settle(ctx context.Context, id, what, status string) (beads.WorkItem, error) {
	state, _, err := t.landing(id)
	if err != nil {
		return beads.WorkItem{}, err
	}
	if err := t.note(ctx, state, what); err != nil {
		return beads.WorkItem{}, err
	}
	return t.item(state, status), nil
}

func (t HarnessRunItems) RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error) {
	if _, ours := runOfItem(id); !ours {
		return t.WorkTracker.RecordOutcome(ctx, id, notes)
	}
	state, _, err := t.landing(id)
	if err != nil {
		return beads.WorkItem{}, err
	}
	return t.settle(ctx, id, notes, outsideFileItemStatus(state))
}

func (t HarnessRunItems) Block(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	if _, ours := runOfItem(id); !ours {
		return t.WorkTracker.Block(ctx, id, reason)
	}
	state, _, err := t.landing(id)
	if err != nil {
		return beads.WorkItem{}, err
	}
	next := " No developer is asked to repair it; a later pull starts a fresh run from the record."
	if state.Document != nil {
		next = " No developer is asked to repair it; the document goes back to its owner, as a document run that did not land does."
	}
	return t.settle(ctx, id, reason+next, "blocked")
}

func (t HarnessRunItems) Release(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	if _, ours := runOfItem(id); !ours {
		return t.WorkTracker.Release(ctx, id, reason)
	}
	return t.settle(ctx, id, reason, "open")
}

func (t HarnessRunItems) Complete(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	if _, ours := runOfItem(id); !ours {
		return t.WorkTracker.Complete(ctx, id, reason)
	}
	state, _, err := t.landing(id)
	if err != nil {
		return beads.WorkItem{}, err
	}
	landed := "the file is brought in. "
	if state.Document != nil {
		landed = "the document landed. "
	}
	return t.settle(ctx, id, landed+reason, "closed")
}

func (t HarnessRunItems) Reopen(ctx context.Context, id, reason string, parking domain.WorkItemParking) (beads.WorkItem, error) {
	if _, ours := runOfItem(id); !ours {
		return t.WorkTracker.Reopen(ctx, id, reason, parking)
	}
	return t.settle(ctx, id, reason, "open")
}

func (t HarnessRunItems) AddBlocker(ctx context.Context, id, blockerID string) error {
	if _, ours := runOfItem(id); !ours {
		return t.WorkTracker.AddBlocker(ctx, id, blockerID)
	}
	return errors.New("a run of the harness's own waits on no work item")
}

package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// PermanentCarryOutError keeps the action's refusal intact while naming why
// another attempt of the same decision cannot succeed.
type PermanentCarryOutError struct {
	Cause triage.CarryOutCause
	Err   error
}

func (e PermanentCarryOutError) Error() string { return e.Err.Error() }
func (e PermanentCarryOutError) Unwrap() error { return e.Err }

func permanentCarryOut(cause triage.CarryOutCause, err error) error {
	return PermanentCarryOutError{Cause: cause, Err: err}
}

func carryOutCause(err error) triage.CarryOutCause {
	var permanent PermanentCarryOutError
	var unmakeable UnrearmablePublicationError
	var missing NoDocketedStoppageError
	switch {
	case errors.As(err, &permanent):
		return permanent.Cause
	case errors.Is(err, gitworktree.ErrOwnedHeadMoved):
		return triage.CarryOutHeadMoved
	case errors.As(err, &unmakeable):
		return triage.CarryOutPublicationUnmakeable
	case errors.Is(err, runstate.ErrRerunTaken):
		return triage.CarryOutDecisionSuperseded
	case errors.As(err, &missing):
		return triage.CarryOutStoppageMissing
	default:
		return ""
	}
}

// itemClosed is the refusal of a re-run or a repair whose item is closed,
// finished or retired. Reopening it is a decision of its own, and only a
// recovery recorded after that is tried.
func itemClosed(workItemID string) error {
	return permanentCarryOut(triage.CarryOutItemClosed, fmt.Errorf(
		"%w: work item %s is closed, so there is no open work for a run of it to do; nothing was claimed and nothing was spent",
		ErrItemNotStartable, workItemID))
}

// runGone is the refusal of a decision naming a run whose record no longer
// exists.
func runGone(runID string, err error) error {
	return permanentCarryOut(triage.CarryOutRunGone, fmt.Errorf(
		"run %s, which the decision is about, no longer has a record, so there is nothing to start from: %w", runID, err))
}

// refusedOnClosedItem is a refusal nothing classed as permanent, read again as
// one where the item is closed. A repair asks much about its run before it reads
// the item, so a decision on a closed item can be refused for one of those first
// and would be tried again for as long as it stood. An item that cannot be read
// leaves the refusal as it was.
func (c CarryOut) refusedOnClosedItem(ctx context.Context, task CarryOutTask, err error) error {
	if err == nil || carryOutCause(err) != "" || beads.Unanswered(err) {
		return err
	}
	item, read := c.itemReader()(ctx, task.WorkItemID)
	if !read || item.ID != task.WorkItemID || item.Status != closedStatus {
		return err
	}
	return permanentCarryOut(triage.CarryOutItemClosed, fmt.Errorf("work item %s is closed, and the attempt was refused: %w", task.WorkItemID, err))
}

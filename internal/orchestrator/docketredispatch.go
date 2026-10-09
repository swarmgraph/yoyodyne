package orchestrator

// Settling an attempt that never became a run once its item has been
// dispatched again.
//
// The entry names no run, so no triage decision about a run answers it, and
// until the item closed nothing took it off: an item refused once by a dirty
// primary checkout and then dispatched and run the next morning left the refusal
// on the development manager's docket on every pass after, naming her as the
// next mover for a question the later run had already answered. What became of
// the item is that run's to say — it is docketed on its own account if it
// stops — so the attempt before it is settled by the harness, naming the run.
//
// An attempt whose item has not been dispatched since is left exactly as it is:
// nothing has happened to it, and it is still the only record that the dispatch
// was tried. An attempt meeting the same failure after it was settled is
// docketed again, because the docket takes a stoppage recorded after the
// decision that settled its key (runstate.DocketStore.RecordOnce).

import (
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// redispatchedAttemptDecision is the word the harness's own closure of an
// attempt overtaken by a later run carries, so a reader can tell it from one the
// development manager decided about.
const redispatchedAttemptDecision = "dispatched-again"

// settleRedispatchedAttempts closes every open attempt that never became a run
// whose item has a run recorded as starting at or after the attempt was
// docketed, and reports how many it closed. The run named is the first such run,
// because that is the dispatch that overtook the attempt.
func (d Docketer) settleRedispatchedAttempts(recorded []runstate.State, now time.Time) (int, error) {
	entries, err := d.Docket.List()
	if err != nil {
		return 0, fmt.Errorf("read the triage docket to settle attempts whose item was dispatched again: %w", err)
	}
	closed := 0
	var problems []error
	for _, entry := range entries {
		if entry.Class != triage.ClassUnstartedAttempt {
			continue
		}
		// A decision standing over the entry has already taken it off the docket; one
		// that has lapsed has not, and the later run settles it here.
		if entry.Closed != nil && entry.Closed.Holds(now) {
			continue
		}
		run, found := firstRunSince(recorded, entry.WorkItemID, entry.RecordedAt)
		if !found {
			continue
		}
		closedAt := now.UTC()
		if closedAt.Before(entry.RecordedAt) {
			closedAt = entry.RecordedAt
		}
		took, err := d.Docket.Close(triage.Closure{
			SchemaVersion: triage.ClosureSchemaVersion,
			Key:           entry.Key,
			ProductID:     entry.ProductID,
			WorkItemID:    entry.WorkItemID,
			Decision:      redispatchedAttemptDecision,
			Reason: fmt.Sprintf("%s was dispatched again after this attempt failed, and became run %s; what became of the item is that run's to say, and it is docketed on its own if it stops",
				entry.WorkItemID, run.RunID),
			DecidedBy: "the harness, reading the run records",
			ClosedAt:  closedAt,
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("settle the attempt at %s that was dispatched again as run %s: %w", entry.WorkItemID, run.RunID, err))
			continue
		}
		if took {
			closed++
		}
	}
	return closed, errors.Join(problems...)
}

// firstRunSince is the earliest run recorded for an item that started at or
// after a moment.
func firstRunSince(recorded []runstate.State, workItemID string, since time.Time) (runstate.State, bool) {
	var first runstate.State
	found := false
	for _, state := range recorded {
		if state.WorkItemID != workItemID || state.RunID == "" || state.StartedAt.Before(since) {
			continue
		}
		if !found || state.StartedAt.Before(first.StartedAt) {
			first, found = state, true
		}
	}
	return first, found
}

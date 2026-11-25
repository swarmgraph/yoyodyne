package orchestrator

// Settling an older run's docket entry once a later run of the same item holds
// or has landed the item's approved change.
//
// An item worked more than once leaves every earlier stopped run on the docket.
// Once a later run's change has been approved by the independent reviewer, or
// promoted (onto the target branch, or into a pull request the forge has been
// asked to merge), nothing the development manager could decide
// about an earlier run is still a question: a repair or re-run of it would redo
// work a later run carries, and the item's progress is that later run's to say.
// Before this, only a timed wait took such an entry off her docket, so it came
// back every time the wait ran out.
//
// The later run must have started after the earlier one, so a run that came
// after a landing is never settled by that landing. An earlier run that itself
// integrated is left alone, because its entry is about its own landing, unless
// its publication was handed back for a fresh run. An earlier run with no later
// approved or landed run stays exactly as it is, whatever its kept change holds:
// a later run still being worked on has not yet replaced anything. The one
// exception is an earlier run that ended cancelled, which judged nothing: any
// later run of its item settles it.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// supersededRunClasses are the entries about one run that a later run's
// approved or landed change answers. An attempt that never became a run is
// settled by docketredispatch.go, and a product decision is about a run still
// going rather than one that stopped.
var supersededRunClasses = map[triage.Class]bool{
	triage.ClassStoppedRun:   true,
	triage.ClassUnstartedRun: true,
	triage.ClassEscalation:   true,
	triage.ClassPublication:  true,
}

// settleSupersededRuns closes every docket entry about an earlier run whose
// item has a later run holding an approved change or having landed one, and
// reports how many it closed. A decision still standing over an entry is left
// to stand, except a wait, which gives way to it (triage.Closure.Supersedes).
func (d Docketer) settleSupersededRuns(recorded []runstate.State, now time.Time) (int, error) {
	entries, err := d.Docket.List()
	if err != nil {
		return 0, fmt.Errorf("read the triage docket to settle runs a later run of the same item replaced: %w", err)
	}
	byID := make(map[string]runstate.State, len(recorded))
	for _, state := range recorded {
		byID[state.RunID] = state
	}
	closed := 0
	var problems []error
	for _, entry := range entries {
		if !supersededRunClasses[entry.Class] {
			continue
		}
		if entry.Closed != nil && entry.Closed.Holds(now) && !waitingClosure(*entry.Closed) {
			continue
		}
		earlier, known := byID[strings.TrimSpace(entry.RunID)]
		if !known || earlier.WorkItemID != entry.WorkItemID || (earlier.Integration != nil && !handedBack(earlier)) {
			continue
		}
		later, found := laterApprovedRun(earlier, recorded)
		reason := ""
		if found {
			reason = supersededRunReason(earlier, later)
		} else if later, found = laterRunOf(earlier, recorded); found && earlier.CancelledHoldingItem(true) {
			// The hold names a cancelled run only while it is its item's latest, so
			// the docket does too.
			reason = fmt.Sprintf("a later run of %s, %s, started after run %s was cancelled, so what happens to the item is decided from that later run", earlier.WorkItemID, later.RunID, earlier.RunID)
		} else {
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
			RunID:         entry.RunID,
			WorkItemID:    entry.WorkItemID,
			Decision:      triage.SupersededByLaterRunDecision,
			Reason:        reason,
			DecidedBy:     "the harness, reading the run records",
			ClosedAt:      closedAt,
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("settle the %s entry of run %s, which later run %s replaced: %w", entry.Class, entry.RunID, later.RunID, err))
			continue
		}
		if took {
			closed++
		}
	}
	return closed, errors.Join(problems...)
}

// laterRunOf is the latest run of the earlier run's item where one started after
// it.
func laterRunOf(earlier runstate.State, recorded []runstate.State) (runstate.State, bool) {
	var later runstate.State
	for _, candidate := range recorded {
		if candidate.WorkItemID == earlier.WorkItemID && candidate.StartedAt.After(earlier.StartedAt) && candidate.StartedAt.After(later.StartedAt) {
			later = candidate
		}
	}
	return later, later.RunID != ""
}

// waitingClosure reports a decision to wait, timed or on named work: the one
// kind of standing decision a later run's approved change takes the place of.
func waitingClosure(closure triage.Closure) bool {
	return closure.WaitsOnWork() || !closure.RevisitAfter.IsZero()
}

// laterApprovedRun is the run of the earlier run's item, started after it, that
// has landed its change or holds one the reviewer approved. A landing is named
// ahead of an approval, and the latest of each ahead of an earlier one.
func laterApprovedRun(earlier runstate.State, recorded []runstate.State) (runstate.State, bool) {
	var chosen runstate.State
	chosenLanded := false
	for _, candidate := range recorded {
		if candidate.RunID == earlier.RunID || candidate.WorkItemID != earlier.WorkItemID ||
			!candidate.StartedAt.After(earlier.StartedAt) || candidate.Retirement != nil || handedBack(candidate) {
			continue
		}
		if earlier.TargetBranch != "" && candidate.TargetBranch != "" && candidate.TargetBranch != earlier.TargetBranch {
			continue
		}
		landed := candidate.Integration != nil
		if !landed && !holdsApprovedChange(candidate) {
			continue
		}
		switch {
		case chosen.RunID == "",
			landed && !chosenLanded,
			landed == chosenLanded && candidate.StartedAt.After(chosen.StartedAt):
			chosen, chosenLanded = candidate, landed
		}
	}
	return chosen, chosen.RunID != ""
}

// holdsApprovedChange reports a run whose change the independent reviewer
// approved, which has not been integrated yet, and whose branch is still there
// to integrate it from.
func holdsApprovedChange(state runstate.State) bool {
	return state.ApprovedAwaitingIntegration() && !state.BranchRemoved
}

// supersededRunReason says in plain words which later run took the earlier
// run's place, and what that later run has done.
func supersededRunReason(earlier, later runstate.State) string {
	return fmt.Sprintf("a later run of %s, %s, %s, so nothing about this earlier run %s is left to decide: repairing or re-running it would redo work the later run carries",
		earlier.WorkItemID, later.RunID, laterRunDid(later), earlier.RunID)
}

// laterRunDid says what the later run has done with its approved change, and
// says it landed only where that is confirmed. A promotion onto a protected
// target records its integration when the pull request is asked to merge,
// before the forge merges it, so such a run is described by its pull request
// until the record says the forge merged it, and then by the merge commit.
func laterRunDid(later runstate.State) string {
	integrated := later.Integration
	published := later.PullRequest
	switch {
	case integrated == nil:
		return fmt.Sprintf("holds a change the reviewer approved, on branch %s, waiting to be merged", later.Branch)
	case published != nil && published.Merged:
		commit := strings.TrimSpace(published.MergeCommit)
		if commit == "" {
			commit = integrated.SourceCommit
		}
		return fmt.Sprintf("landed its change on %s through pull request #%d, merged at commit %s", integrated.TargetBranch, published.Number, commit)
	case integrated.ThroughPullRequest && published != nil && published.Number > 0:
		return fmt.Sprintf("has its approved change in pull request #%d into %s, waiting for the forge to merge it", published.Number, integrated.TargetBranch)
	case integrated.ThroughPullRequest:
		return fmt.Sprintf("has its approved change, commit %s, waiting for the forge to merge it into %s", integrated.SourceCommit, integrated.TargetBranch)
	default:
		return fmt.Sprintf("landed its change on %s at commit %s", integrated.TargetBranch, integrated.SourceCommit)
	}
}

package orchestrator

// Settling the changes that wait for the team's merge.
//
// Under execution.upstream_branch a run whose change landed on the target
// branch ends there, holding its item's claim and no developer slot, with the
// wait recorded on it (runstate.TeamMerge). Nothing the run does afterwards can
// move the change: the team merges the target branch into theirs, outside the
// harness. So each forge check fetches the team's branch once for every run
// waiting and settles the ones whose landing commit is now in its history: the
// item is closed with the commit and the branch named, and the run records
// when and at which tip of the team's branch it was found.
//
// A fetch that cannot be made, or a branch that is not there, changes nothing
// and is said on the settlement; the next check reads again. Every write reads
// the item first and is not made twice, so a check that failed after the item
// closed (or after its note was written) finishes the rest at the next one.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TeamMergeWaits lists the runs waiting for the team to merge their change,
// from the run index. It is satisfied by runstate.Store.
type TeamMergeWaits interface {
	WaitingForTeamMerge() ([]runstate.State, error)
}

// TeamBranchReader fetches the team's branch and reports whether a commit is in
// its history. It is satisfied by gitworktree.Manager.
type TeamBranchReader interface {
	RemoteTargetCarries(ctx context.Context, branch string, commits ...string) (gitworktree.TargetReading, error)
}

// TeamMergeSettlement is what one check did about one change waiting for the
// team's merge.
type TeamMergeSettlement struct {
	RunID          string `json:"run_id"`
	WorkItemID     string `json:"work_item_id"`
	TargetBranch   string `json:"target_branch"`
	UpstreamBranch string `json:"upstream_branch"`
	LandingCommit  string `json:"landing_commit"`
	// Found reports the landing commit on the team's branch, and FoundOn the
	// tip of the branch it was read at.
	Found   bool   `json:"found"`
	FoundOn string `json:"found_on,omitempty"`
	// Settled reports the item closed and the run's record written to say so.
	Settled bool `json:"settled"`
	// Kept is why a wait was deliberately left as it stands: a run a live
	// process holds, or one settled while the branch was being read.
	Kept    string `json:"kept,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// SettleTeamMerges fetches the team's branch for every run waiting for the
// team's merge and closes the items whose landing commit it now carries. One
// that cannot be settled never stops the others, and is read again by the next
// check.
func (r Reconciler) SettleTeamMerges(ctx context.Context) ([]TeamMergeSettlement, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	waits, ok := r.Store.(TeamMergeWaits)
	if !ok {
		return nil, errors.New("this check's run store cannot list the runs waiting for the team to merge their change")
	}
	waiting, err := waits.WaitingForTeamMerge()
	if err != nil {
		return nil, fmt.Errorf("list the runs waiting for the team to merge their change: %w", err)
	}
	settled := make([]TeamMergeSettlement, 0, len(waiting))
	if len(waiting) == 0 {
		return settled, nil
	}
	reader, ok := r.Worktrees.(TeamBranchReader)
	if !ok {
		return settled, fmt.Errorf("%d run(s) are waiting for the team to merge their change, and this check cannot read the team's branch", len(waiting))
	}
	for _, state := range waiting {
		if err := ctx.Err(); err != nil {
			return settled, err
		}
		settled = append(settled, r.settleTeamMerge(ctx, reader, state))
	}
	return settled, nil
}

// settleTeamMerge reads the team's branch for one wait and settles it under the
// run's own lease where the landing commit is there.
func (r Reconciler) settleTeamMerge(ctx context.Context, reader TeamBranchReader, recorded runstate.State) TeamMergeSettlement {
	wait := *recorded.TeamMerge
	result := TeamMergeSettlement{
		RunID:          recorded.RunID,
		WorkItemID:     recorded.WorkItemID,
		TargetBranch:   wait.TargetBranch,
		UpstreamBranch: wait.UpstreamBranch,
		LandingCommit:  wait.LandingCommit,
	}
	reading, err := reader.RemoteTargetCarries(ctx, wait.UpstreamBranch, wait.LandingCommit)
	if err != nil {
		result.Failure = fmt.Errorf("read %s for %s, which run %s is waiting on: %w", wait.UpstreamBranch, wait.LandingCommit, recorded.RunID, err).Error()
		return result
	}
	if reading.Found == "" {
		return result
	}
	result.Found, result.FoundOn = true, reading.Tip

	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		result.Kept = fmt.Sprintf("a live process holds run %s, so the next check settles it", recorded.RunID)
		return result
	case err != nil:
		result.Failure = fmt.Errorf("adopt run %s to settle its wait for the team: %w", recorded.RunID, err).Error()
		return result
	}
	defer lease.Release()
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != wait.LandingCommit {
		result.Kept = fmt.Sprintf("run %s was settled while %s was being read", recorded.RunID, wait.UpstreamBranch)
		return result
	}
	// A wait is recorded only for a change that discharges its item
	// (awaitsTeamMerge), and the team's merge settles where the work is, not
	// whether it is the work the item asked for. So the record is asked again
	// rather than trusted, and one that no longer says so is left for a person.
	if !state.Discharges() {
		result.Kept = fmt.Sprintf("run %s's record no longer says its change discharges %s, so the team's merge does not close it", recorded.RunID, recorded.WorkItemID)
		return result
	}
	headline := teamMergeFoundHeadline(wait)
	notes := headline + "\n" + fmt.Sprintf("%s was at %s when it was read.", wait.UpstreamBranch, reading.Tip)
	err = r.recovering(ctx, &state, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		item, err := r.Tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return fmt.Errorf("load work item %s: %w", state.WorkItemID, err)
		}
		if item.Status == closedStatus {
			return nil
		}
		if !notesCarry(item.Notes, headline) {
			if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, notes); err != nil {
				return fmt.Errorf("record the team's merge on %s: %w", state.WorkItemID, err)
			}
		}
		if _, err := r.Tracker.Complete(ctx, state.WorkItemID, teamMergeCompletionReason(state, wait)); err != nil {
			return fmt.Errorf("close work item %s on the team's merge into %s: %w", state.WorkItemID, wait.UpstreamBranch, err)
		}
		return nil
	})
	if err != nil {
		result.Failure = err.Error()
		return result
	}
	now := r.clock().Now()
	if _, err := r.Store.Update(ctx, state.RunID, runstate.TeamMergeFound{LandingCommit: wait.LandingCommit, FoundOn: reading.Tip, At: now}); err != nil {
		result.Failure = fmt.Errorf("record on run %s that %s carries %s: %w", state.RunID, wait.UpstreamBranch, wait.LandingCommit, err).Error()
		return result
	}
	if r.Docket != nil {
		// A closure that cannot be written does not undo the item's: the sweep
		// closes every docket entry whose item the tracker holds as closed.
		_, _ = r.Docket.SettleClosedItem(state.WorkItemID, teamMergeCompletionReason(state, wait))
	}
	result.Settled = true
	return result
}

// teamMergeFoundHeadline is the note's first line, and the line a repeated
// write is recognised by: the tip it was read at is not in it, because a retry
// may read a later one.
func teamMergeFoundHeadline(wait runstate.TeamMerge) string {
	return fmt.Sprintf("The team merged this item into %s: %s, the commit it landed on %s as, is now on %s, so the item is closed.",
		wait.UpstreamBranch, wait.LandingCommit, wait.TargetBranch, wait.UpstreamBranch)
}

// teamMergeCompletionReason is the reason the item closes with.
func teamMergeCompletionReason(state runstate.State, wait runstate.TeamMerge) string {
	return fmt.Sprintf("Reviewed by Yoyodyne run %s and landed on %s as %s, which the team merged into %s",
		state.RunID, wait.TargetBranch, wait.LandingCommit, strings.TrimSpace(wait.UpstreamBranch))
}

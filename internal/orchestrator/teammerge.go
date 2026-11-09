package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A project that sets execution.upstream_branch keeps its target branch (say
// staging) as the place work lands and the team's branch (say main) as where
// the team merges it. A change that lands on the target therefore does not
// close its item: the item stays claimed, and the run records the wait
// (runstate.TeamMerge) until a fetch of the team's branch finds the landing
// commit there. This file holds what every place that would have closed the
// item shares: whether a run waits, the wait it records, and the words for it.

// upstreamBranchFor is the team's branch a run records when it starts: the
// configured one, and only when the run has a target branch and the two
// differ. A team branch that is the target branch is the ordinary case, where
// landing is the end.
func upstreamBranchFor(configured, targetBranch string) string {
	upstream := strings.TrimSpace(configured)
	if upstream == "" || targetBranch == "" || upstream == targetBranch {
		return ""
	}
	return upstream
}

// awaitsTeamMerge reports a run whose landing closes nothing yet: the project
// names a team branch and the change discharges its item.
func awaitsTeamMerge(state runstate.State) bool {
	return state.UpstreamBranch != "" && state.Discharges()
}

// integrationLandingCommit is the commit on the target branch the team's
// branch has to come to hold: the integration's target commit, else its source
// commit.
func integrationLandingCommit(integration *gitworktree.Integration) string {
	if integration == nil {
		return ""
	}
	return nonEmpty(integration.TargetCommit, integration.SourceCommit)
}

// recordedLandingCommit is integrationLandingCommit for the integration a run
// has recorded.
func recordedLandingCommit(state runstate.State) string {
	if state.Integration == nil {
		return ""
	}
	return nonEmpty(state.Integration.TargetCommit, state.Integration.SourceCommit)
}

// teamMergeWait is the wait a run records when its change landed as
// landingCommit at the given time.
func teamMergeWait(state runstate.State, landingCommit string, at time.Time) runstate.TeamMerge {
	target := state.TargetBranch
	if target == "" && state.Integration != nil {
		target = state.Integration.TargetBranch
	}
	return runstate.TeamMerge{
		TargetBranch:   target,
		UpstreamBranch: state.UpstreamBranch,
		LandingCommit:  landingCommit,
		Since:          at,
	}
}

// teamMergeHeadline is the sentence that says the wait, and the line a repeat
// write looks for on the item.
func teamMergeHeadline(wait runstate.TeamMerge) string {
	commit := wait.LandingCommit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("Landed on %s as %s, waiting for the team to merge it into %s.", wait.TargetBranch, commit, wait.UpstreamBranch)
}

// teamMergeNotes is the whole account of the wait the item carries.
func teamMergeNotes(wait runstate.TeamMerge) string {
	return teamMergeHeadline(wait) + fmt.Sprintf(" The item stays open and is not started again; it closes once a fetch of %s finds that commit there.", wait.UpstreamBranch)
}

// renderTeamMergeNotes is the note a sweep-side settlement leaves on the item.
func renderTeamMergeNotes(state runstate.State, wait runstate.TeamMerge) string {
	return strings.Join([]string{teamMergeNotes(wait), "Run: " + state.RunID}, "\n")
}

// recordTeamMergeWait is what a settlement that would have closed an item does
// when the run awaits the team's merge: it says the wait on the item, leaves
// the item claimed, and sets the wait on state for the caller to save. An item
// already closed is left alone. The note is written once however many times
// this is asked.
func recordTeamMergeWait(ctx context.Context, tracker WorkTracker, state *runstate.State, landingCommit string, at time.Time) error {
	item, err := tracker.Show(ctx, state.WorkItemID)
	if err != nil {
		return fmt.Errorf("load work item %s: %w", state.WorkItemID, err)
	}
	if item.Status == "closed" {
		return nil
	}
	wait := teamMergeWait(*state, landingCommit, at)
	if state.TeamMerge != nil {
		wait = *state.TeamMerge
	}
	if !notesCarry(item.Notes, teamMergeHeadline(wait)) {
		if _, err := tracker.RecordOutcome(ctx, state.WorkItemID, renderTeamMergeNotes(*state, wait)); err != nil {
			return fmt.Errorf("record that %s waits for the team's merge: %w", state.WorkItemID, err)
		}
	}
	if state.TeamMerge == nil {
		state.TeamMerge = &wait
	}
	return nil
}

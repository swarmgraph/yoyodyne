package orchestrator

import (
	"context"
	"errors"
)

// The forge check: what the harness reads of the forge on a schedule of its
// own, apart from the maintenance pass (docs/designs/
// working-alongside-other-contributors.md, section 6). `yoyo forge-check` runs
// it, and the supervisor runs that every execution.forge_check_interval.
//
// One check reads the pull requests the factory is waiting on a person to merge
// and settles the ones merged or closed (SettlePersonMerges), brings one the
// target branch has moved past up to date (updatePersonMerge, which compares
// the request's head with the target), and reads the reviews and comments on
// the ones still open (TakeUpFeedback). It also fetches the team's branch for
// each change waiting for the team's merge (execution.upstream_branch) and
// closes the item whose landing commit it now carries (SettleTeamMerges). The
// command also hosts bringing the target branch up to date with the team's
// branch (TeamBranchUpdater, teambranchupdate.go), which runs checks and a
// review and so is not part of CheckForge. The claim, team pause and records
// references the design adds with team mode are not read: team mode does not
// exist yet, and the check covers them once it does.
//
// Two rules make a check safe to repeat and safe to fail:
//
//   - A check that cannot reach the forge changes nothing. Every step reads
//     before it writes, and a request whose read failed is left exactly as it
//     was, said as a failure on its settlement, for the next check to read
//     again.
//   - A change the check read and could not finish acting on is read and acted
//     on again by the next check. Nothing is marked handled until the tracker
//     write that follows from it has gone through: a merged request's run stops
//     waiting only once its item is closed, and a feedback cursor moves only
//     once what it read was acted on. Each write is made so that making it
//     twice changes nothing — an item already closed is not closed again, and a
//     note the item already ends with is not written again — so a write that
//     timed out after it had in fact gone through leaves the item as it was,
//     with one note. The check sets RetryAtNextCheck, so such a write is not
//     waited out inside the check either.
//
// Neither failure stops a run or holds back other work: a check takes no
// developer slot of its own, and what it cannot do is said on its own output and
// left for the next one.

// ForgeCheck is what one check of the forge found and did.
type ForgeCheck struct {
	// PersonMerges is each pull request waiting for a person to merge it: still
	// open, brought up to date, merged and its item closed, or closed without
	// merging and its item given back to the Lead Product Manager.
	PersonMerges []PersonMergeSettlement `json:"person_merges"`
	// TeamMerges is each landed change waiting for the team to merge it into
	// their branch: still waiting, or found on that branch and its item closed.
	TeamMerges []TeamMergeSettlement `json:"team_merges"`
	// Feedback is what was said on each of them since the head was pushed, and
	// what the check did about it.
	Feedback []FeedbackSettlement `json:"feedback"`
}

// Failures counts the requests the check could not read or finish acting on,
// each of which the next check reads again.
func (c ForgeCheck) Failures() int {
	failures := 0
	for _, settled := range c.PersonMerges {
		if settled.Failure != "" {
			failures++
		}
	}
	for _, settled := range c.TeamMerges {
		if settled.Failure != "" {
			failures++
		}
	}
	for _, settled := range c.Feedback {
		if settled.Failure != "" {
			failures++
		}
	}
	return failures
}

// CheckForge is one check of the forge. Merges are settled before feedback is
// read, so a request merged or closed is not read for feedback, and a run this
// check put back at its promotion is no longer waiting when its feedback would
// be read.
func (r Reconciler) CheckForge(ctx context.Context) (ForgeCheck, error) {
	r.RetryAtNextCheck = true
	var check ForgeCheck
	var mergeErr, teamErr, feedbackErr error
	check.PersonMerges, mergeErr = r.SettlePersonMerges(ctx)
	check.TeamMerges, teamErr = r.SettleTeamMerges(ctx)
	check.Feedback, feedbackErr = r.TakeUpFeedback(ctx)
	return check, errors.Join(mergeErr, teamErr, feedbackErr)
}

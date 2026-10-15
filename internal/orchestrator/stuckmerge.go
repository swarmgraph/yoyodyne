package orchestrator

// A queued merge has a limit on how long it may wait.
//
// The sweep reads a queued merge's checks and decides on a red one
// (queuedchecks.go), and it decides a merge the forge dropped. What neither did
// was notice a merge that simply never lands: one the forge holds with green
// checks and never merges, or one it dropped behind its target whose replay
// waits for a free developer slot that never comes. The record of either kept
// saying "queued", and a queued run counts as integrated and succeeded, so the
// development manager could neither repair it nor run it again, and kept
// renewing waits on it while the forge showed it conflicting with main.
//
// So a merge that has waited with the forge longer than
// triage.stuck_merge_age — measured from when it was queued
// (runstate.PullRequest.QueuedAt), or from the run's end on a record older than
// that field — is asked about and handed on, whichever of those it is:
//
//   - The forge's own account is read: its merge state for the request, and
//     the queue events on its timeline with any reason the forge gave
//     (readQueueRemoval). The record says in words what that state means and
//     what would let the merge land.
//   - A merge the forge still holds is withdrawn first, so nothing lands while
//     the development manager decides.
//   - The run is then settled as a dropped merge (settleDroppedMerge): not
//     merged, its branch and worktree kept, the reason on the item as its
//     blocker, and the run on the development manager's docket of stopped runs.
//
// What she can decide there is said on the same line. A repair brings the kept
// change up to date with the target: the run is put back at its promotion from
// its kept branch, checked and reviewed again, and its merge queued again, as
// the sweep does for a merge that fell behind (replaysDroppedMerge in this file,
// carried out by RepairContinuer.Continue). Where that replay is impossible — a
// local promotion, a closed pull request, a branch or session gone — the line
// says why and names a re-run instead, and a repair is refused once, on the
// item, with cause merge-dropped. A re-arm repeats the same merge, which only
// helps where the forge's reason has passed.
//
// Where what the forge reports can only be cleared by somebody with access to
// the repository on GitHub — an approving review a protection rule requires, or
// a draft to mark ready — the record says exactly what that person does, so she
// can pass it on as it stands.
//
// A read of the forge that fails decides nothing: the merge is left queued, and
// the failed read is what the sweep reports for the run. A merge that lands
// before the limit, or whose checks are decided on before it, is untouched.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// queuedSince is when the run's merge was last taken by the forge, as far as
// the record says.
func queuedSince(state runstate.State) time.Time {
	if state.PullRequest != nil && state.PullRequest.QueuedAt != nil {
		return *state.PullRequest.QueuedAt
	}
	return stoppedAt(state)
}

// queuedPastLimit reports how long the run's merge has waited, and whether that
// is past the limit this sweep holds merges to.
func (r Reconciler) queuedPastLimit(state runstate.State) (time.Duration, bool) {
	if r.StuckMergeAge <= 0 || r.Checks == nil || state.PullRequest == nil {
		return 0, false
	}
	waited := r.clock().Now().Sub(queuedSince(state))
	return waited, waited >= r.StuckMergeAge
}

// settleStuckMerge hands on a merge that waited past the limit. held says the
// forge still holds it, which is withdrawn before anything is written: a
// withdrawal that fails leaves the record queued, and the next sweep asks
// again. waitedOn, where it is not empty, is what the harness itself was
// waiting for, for a dropped merge whose head it meant to bring up to date.
func (r Reconciler) settleStuckMerge(ctx context.Context, state runstate.State, waited time.Duration, held bool, waitedOn string) (Reconciliation, error) {
	published := *state.PullRequest
	if held {
		if err := r.Checks.DisableAutoMerge(ctx, published.Number); err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf(
				"withdraw the merge of pull request %d for run %s, queued for %s: %w", published.Number, state.RunID, waitedFor(waited), err)
		}
	}
	removal, unread := r.readQueueRemoval(ctx, published.Number)
	target := state.Integration.TargetBranch
	reason := stuckMerge(published, target, queuedSince(state), waited, r.StuckMergeAge, held, waitedOn, dropReplayRefusal(state), removal, unread)
	published.MergeQueued = false
	published.TargetRed = nil
	state.PullRequest = &published
	state.PublishFailure = reason
	state.MergeDrop = &runstate.MergeDrop{At: r.clock().Now(), Reason: reason}
	return r.settleDroppedMerge(ctx, state)
}

// stuckMerge is what the record and the item say about a merge that waited
// past the limit: how long, what the forge says is holding it in words, what
// would let it land, the forge's own account, and who moves next.
//
// cannotReplay is why a repair cannot bring this run's change up to date, and
// empty where it can.
func stuckMerge(published runstate.PullRequest, target string, since time.Time, waited, limit time.Duration, held bool, waitedOn, cannotReplay string, removal publish.QueueRemoval, unread string) string {
	sentences := []string{fmt.Sprintf("Pull request %d was queued to merge into %s at %s and has not merged after %s, which is past the %s a queued merge may wait (triage.stuck_merge_age).",
		published.Number, target, since.Local().Format("2006-01-02 15:04 MST"), waitedFor(waited), waitedFor(limit))}
	if held {
		sentences = append(sentences, "The forge was still holding the merge, so the harness withdrew it, and nothing lands until it is armed again.")
	} else {
		sentences = append(sentences, "The forge had already dropped the merge.")
	}
	if waitedOn != "" {
		sentences = append(sentences, fmt.Sprintf("Its head had fallen behind %s, and bringing it up to date waited because %s, so the harness stopped waiting.", target, waitedOn))
	}
	if cannotReplay == "" {
		sentences = append(sentences, fmt.Sprintf("The change is not merged. The run's branch and worktree are kept, so a repair decided on this run brings it up to date without rebuilding it: the harness puts the run back at its merge, brings the kept change up to date with %s, checks and reviews it again, and queues the merge again, handing any conflict to the developer.", target))
	} else {
		sentences = append(sentences, fmt.Sprintf("The change is not merged, and a repair cannot bring it up to date because %s, so a re-run of the item is what builds it again on %s.", cannotReplay, target))
	}
	sentences = append(sentences, stuckMergeCause(removal, unread, published.Number, target))
	sentences = append(sentences, forgeDropAccount(removal, unread, published, target))
	return strings.Join(sentences, " ") + " The harness does not merge past a requirement of " + target + ", so " + developmentManagerMovesNext
}

// stuckMergeCause says in words what the forge's merge state means for this
// merge and what would let it land. A step only a person with access to the
// repository can take is named as exactly that step.
func stuckMergeCause(removal publish.QueueRemoval, unread string, number int, target string) string {
	if unread != "" || removal.MergeStatusError != "" {
		return "The forge's merge state for it could not be read, so what is holding it is not known; the forge's account below is what could be read."
	}
	switch strings.ToUpper(strings.TrimSpace(removal.MergeStatus)) {
	case "DIRTY":
		return fmt.Sprintf("What is holding it: the forge says the pull request conflicts with %s. The change has to be brought up to date with %s and the conflict resolved.", target, target)
	case "BEHIND":
		return fmt.Sprintf("What is holding it: %s has moved ahead of the pull request and requires it to be up to date. The change has to be brought up to date with %s.", target, target)
	case "UNSTABLE":
		return "What is holding it: a check the forge runs on the pull request has not succeeded. The checks below say which."
	case "BLOCKED":
		return fmt.Sprintf("What is holding it: a protection rule of %s is not satisfied. If the checks below are all passing, the unmet rule is one only somebody with access to the repository on GitHub can satisfy: they approve pull request %d on GitHub if the rule requires an approving review, or change the branch protection of %s so it no longer requires what is missing.", target, number, target)
	case "DRAFT":
		return fmt.Sprintf("What is holding it: the pull request is still a draft. Somebody with access to the repository on GitHub has to mark pull request %d ready for review before it can merge.", number)
	case "CLEAN", "HAS_HOOKS":
		return "What is holding it: nothing the forge reports. The pull request can merge and the forge's queue has not merged it, so arming the merge again is the likely remedy."
	case "UNKNOWN", "":
		return "What is holding it: the forge has not decided whether the pull request can merge, so it says nothing about what is in the way."
	}
	return fmt.Sprintf("What is holding it: the forge reports the merge state %q, which the harness does not recognize.", removal.MergeStatus)
}

// dropReplayRefusal says why a repair cannot bring a run's change up to date
// onto its target, and nothing where it can: its approved change landed through
// an open pull request whose branch, worktree, approval and developer session
// the run still has, with a resumption left to spend (unreplayable).
func dropReplayRefusal(state runstate.State) string {
	switch {
	case state.Integration == nil:
		return "its record carries no approved change awaiting the merge"
	case state.PullRequest == nil:
		return "its record names no pull request"
	case state.PullRequest.Closed():
		return fmt.Sprintf("pull request %d is closed", state.PullRequest.Number)
	}
	return unreplayable(state)
}

// replaysDroppedMerge reports a repair decided on a run whose approved change
// the forge did not merge, and that nothing returned to a developer: what it
// carries out is the replay, not a developer attempt. The branch and the
// checkout have to be there as the repository shows them, because the replay is
// made from them; a run without them is refused as any repair is.
func replaysDroppedMerge(prior runstate.State, found triage.Found) bool {
	if prior.MergeDrop == nil || handedBackRepair(prior) || continuableStall(prior) || prior.StoppedAtStageBound() {
		return false
	}
	// A merge withdrawn over a check failing on the change itself is the
	// change's failure, not a race: replaying it would queue the same red change
	// again. Its own refusal names the supported alternative (oldForgeRepairAlternative).
	if oldForgeRepairAlternative(prior) != nil || (prior.PullRequest != nil && prior.PullRequest.Checks != nil && prior.PullRequest.Checks.ChangeFails()) {
		return false
	}
	if found.Unknown || !found.BranchThere || !found.WorktreeThere {
		return false
	}
	return dropReplayRefusal(prior) == ""
}

// putBackAtPromotion is the record of a run made live again at its promotion to
// bring its approved change up to date onto its target: the resumption that
// says why, the promotion and the dropped merge taken off the record because
// what is promoted and queued next is the replayed change, and the reading of
// the old head's checks with them. It is the record updateQueuedHead writes for
// a queued head that fell behind, which is what the pipeline resumes from.
func putBackAtPromotion(continued *runstate.State, prior runstate.State, reason string, now time.Time) {
	continued.IntegrationResumptions = append(append([]runstate.IntegrationResumption{}, prior.IntegrationResumptions...),
		runstate.IntegrationResumption{
			Cause:             runstate.CauseQueuedHeadBehind,
			Reason:            boundedReason(reason),
			ResumedAt:         now,
			SupersededFailure: prior.Failure,
			SupersededBlocker: prior.Blocker,
		})
	continued.Integration = nil
	if prior.PullRequest != nil {
		published := *prior.PullRequest
		published.MergeQueued = false
		published.Checks = nil
		published.TargetRed = nil
		continued.PullRequest = &published
	}
	continued.PublishFailure = ""
	continued.MergeDrop = nil
	continued.Phase = runstate.PhaseIntegrating
}

// replayReason is what the run and the item record as why a repair put the run
// back at its promotion rather than invoking its developer.
func replayReason(entry triage.Entry, granted repairGrant, prior runstate.State) string {
	reason := fmt.Sprintf(
		"Triaged: the development manager's triage decided a repair of run %s, %s, whose approved change the forge did not merge (pull request %d). Nothing was returned to its developer, so the harness put the run back at its merge to bring the kept change up to date with %s, check it and have it reviewed again, and queue its merge again; a conflict goes to the developer under a grant of %d further repair attempt(s) recorded against %s's durable triage budget. The reasoning that decision was recorded with: ",
		entry.RunID, granted.decision.Cite(), prior.PullRequest.Number, prior.TargetBranch, granted.attempts, entry.WorkItemID)
	room := max(runstate.MaxSelectionReasonBytes-len(reason), 0)
	return singleLine(reason+singleLine(strings.TrimSpace(granted.decision.Reason), room), runstate.MaxSelectionReasonBytes)
}

// waitedFor says a wait the way somebody reads it: "13 hours 31 minutes", not
// "13h31m0s".
func waitedFor(span time.Duration) string {
	if span < time.Minute {
		return "less than a minute"
	}
	days := int(span / (24 * time.Hour))
	hours := int(span/time.Hour) % 24
	minutes := int(span/time.Minute) % 60
	if days > 0 {
		// A wait of days is read to the hour.
		minutes = 0
	}
	var parts []string
	for _, part := range []struct {
		count int
		unit  string
	}{{days, "day"}, {hours, "hour"}, {minutes, "minute"}} {
		switch {
		case part.count == 1:
			parts = append(parts, "1 "+part.unit)
		case part.count > 1:
			parts = append(parts, fmt.Sprintf("%d %ss", part.count, part.unit))
		}
	}
	return strings.Join(parts, " ")
}

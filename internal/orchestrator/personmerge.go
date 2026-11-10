package orchestrator

// Settling the changes that wait for a person to merge them.
//
// Under a merge setting that leaves the merge to a person, a run that has
// published its approved change ends there, holding its item's claim and no
// developer slot, with the wait recorded on it (runstate.PersonMerge). Nothing
// the run does afterwards can move the change: a person merges the pull
// request, or closes it, on the forge. So each check of the forge reads every
// request waited on and settles the ones that have moved:
//
//   - Merged: the local target is brought onto the merge by fast-forward, the
//     item is closed with the merge commit named (or put back where the run's
//     landing did not discharge it), and the run is recorded as having landed.
//     Items waiting on it are then free to go on, because the tracker releases
//     them once it closes.
//   - Closed without merging: the item goes back to the Lead Product Manager,
//     parked with a note naming who closed the request and when, so nothing
//     starts it again before she decides whether to drop the work, revise it,
//     or admit it again. The branch and the run's checkout are kept, and
//     nothing is deleted.
//   - Still open: nothing changes, unless the target has moved past the
//     request. Then the request is brought up to date (updatePersonMerge): its
//     merge authority is withdrawn and confirmed withdrawn, and the run is put
//     back at its promotion, where the change is replayed onto the new target,
//     checked and reviewed again, and pushed to the same request, which waits
//     for the person again. A request the forge is already merging is left
//     alone, and the run reads the request again immediately before it pushes
//     (activeRun.publishUpdated). A conflict in the replay is handed to the
//     developer, up to runstate.MaxUpToDateConflicts times.
//
// Every write here reads the item first and is not made again where an earlier
// attempt landed, so a check that stopped part-way is finished by the next one
// with one note rather than two.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReconcileClosers reads who closed a pull request and when. It only reads. It
// is satisfied by publish.GitHub.
type ReconcileClosers interface {
	Closer(ctx context.Context, number int) (publish.Closer, error)
}

// PersonMergeWaits lists the runs waiting for a person to merge their pull
// request, from the run index rather than every record. It is satisfied by
// runstate.Store.
type PersonMergeWaits interface {
	WaitingForPersonMerge() ([]runstate.State, error)
}

// What became of one pull request a person was to merge, as a check found it.
const (
	PersonMergeOpen   = "open"
	PersonMergeMerged = "merged"
	PersonMergeClosed = "closed"
)

// PersonMergeSettlement is what one check did about one change waiting for a
// person to merge it.
type PersonMergeSettlement struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Number     int    `json:"number"`
	URL        string `json:"url,omitempty"`
	// Found is what the forge said: PersonMergeOpen, PersonMergeMerged or
	// PersonMergeClosed. It is empty where the forge was not answered.
	Found string `json:"found,omitempty"`
	// Settled reports the item closed on the merge, or given back on the
	// closing, and the run's record written to say so.
	Settled     bool                 `json:"settled"`
	MergeCommit string               `json:"merge_commit,omitempty"`
	Catchup     *gitworktree.Catchup `json:"catchup,omitempty"`
	ClosedBy    string               `json:"closed_by,omitempty"`
	// CleanupProblem is why the run's branch and checkout were not removed
	// after a merge the check settled. The merge and the item's settlement
	// stand; the run owes the cleanup, and the next sweep tries it again.
	CleanupProblem string     `json:"cleanup_problem,omitempty"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	// Kept is why a wait was deliberately left as it stands: a run a live
	// process holds, a branch the forge answers about with another request, a
	// merge in progress on the forge, or a request behind its target that this
	// check could not bring up to date now.
	Kept string `json:"kept,omitempty"`
	// BehindBy is how many commits the target carries that the open request's
	// head does not, where the check read it. Updating reports the run put back
	// at its promotion to bring the request up to date, and Update says why.
	BehindBy int    `json:"behind_by,omitempty"`
	Updating bool   `json:"updating,omitempty"`
	Update   string `json:"update,omitempty"`
	Failure  string `json:"failure,omitempty"`
}

// SettlePersonMerges reads every pull request a run is waiting on a person to
// merge and settles the ones the forge reports merged or closed. One that
// cannot be settled never stops the others, and is read again by the next
// check.
func (r Reconciler) SettlePersonMerges(ctx context.Context) ([]PersonMergeSettlement, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	waits, ok := r.Store.(PersonMergeWaits)
	if !ok {
		return nil, errors.New("this check's run store cannot list the runs waiting for a person to merge their pull request")
	}
	waiting, err := waits.WaitingForPersonMerge()
	if err != nil {
		return nil, fmt.Errorf("list the runs waiting for a person to merge their pull request: %w", err)
	}
	settled := make([]PersonMergeSettlement, 0, len(waiting))
	if len(waiting) == 0 {
		return settled, nil
	}
	if r.Publisher == nil {
		return settled, fmt.Errorf(
			"%d run(s) are waiting for a person to merge their pull request, and this check has no forge access to ask about them", len(waiting))
	}
	branches := make([]string, 0, len(waiting))
	for _, state := range waiting {
		branches = append(branches, state.PullRequest.Branch)
	}
	answers := r.askForge(ctx, branches)
	for _, state := range waiting {
		if err := ctx.Err(); err != nil {
			return settled, err
		}
		settled = append(settled, r.settlePersonMerge(ctx, answers, state))
	}
	return settled, nil
}

// settlePersonMerge settles one wait from the forge's answer, under the run's
// own lease.
func (r Reconciler) settlePersonMerge(ctx context.Context, answers forgeAnswers, recorded runstate.State) PersonMergeSettlement {
	published := *recorded.PullRequest
	result := PersonMergeSettlement{
		RunID:      recorded.RunID,
		WorkItemID: recorded.WorkItemID,
		Number:     published.Number,
		URL:        published.URL,
	}
	observed, err := r.state(ctx, answers, published.Branch)
	if err != nil {
		result.Failure = fmt.Errorf("ask the forge about pull request %d of run %s: %w", published.Number, recorded.RunID, err).Error()
		return result
	}
	// The branch is the handle the run keeps on its request, so another request
	// answering for it is not the one waited on.
	if observed.Number != published.Number {
		result.Kept = fmt.Sprintf("the forge reports pull request %d for branch %s, and run %s is waiting on request %d there",
			observed.Number, published.Branch, recorded.RunID, published.Number)
		return result
	}
	switch {
	case observed.Merged:
		result.Found = PersonMergeMerged
	case strings.EqualFold(strings.TrimSpace(observed.State), "closed"):
		result.Found = PersonMergeClosed
	default:
		result.Found = PersonMergeOpen
		return r.updatePersonMerge(ctx, recorded, observed, result)
	}

	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		result.Kept = fmt.Sprintf("a live process holds run %s, so the next check settles its pull request", recorded.RunID)
		return result
	case err != nil:
		result.Failure = fmt.Errorf("adopt run %s to settle pull request %d: %w", recorded.RunID, published.Number, err).Error()
		return result
	}
	defer lease.Release()
	if !state.WaitingForPersonMerge() || state.PersonMerge.Number != published.Number {
		result.Kept = fmt.Sprintf("run %s was settled while the forge was being asked", recorded.RunID)
		return result
	}
	if result.Found == PersonMergeMerged {
		err = r.settlePersonMerged(ctx, &state, observed, &result)
	} else {
		err = r.returnPersonMergeClosed(ctx, &state, observed, &result)
	}
	if err != nil {
		result.Failure = err.Error()
		return result
	}
	result.Settled = true
	return result
}

// settlePersonMerged follows a merge a person made: the local target is brought
// onto it by fast-forward, the item is settled, and the run is recorded as
// having landed through its pull request.
func (r Reconciler) settlePersonMerged(ctx context.Context, state *runstate.State, observed publish.PullRequest, result *PersonMergeSettlement) error {
	mergeCommit := strings.TrimSpace(observed.MergeCommit)
	result.MergeCommit = mergeCommit
	// A target that cannot be fast-forwarded here does not hold the item open:
	// the merge is the forge's fact, and the convergence sweep catches the
	// target up later. The note says which it was.
	if target := strings.TrimSpace(state.TargetBranch); target != "" {
		catchup := r.catchUp(ctx, target)
		result.Catchup = &catchup
	}
	notes := renderPersonMergedNotes(*state, mergeCommit, result.Catchup)
	var arranged *runstate.State
	// Where the merge landed on the target branch of a project with a team
	// branch, the commit the team's branch has to come to hold is the merge
	// commit, else the published head.
	landing := nonEmpty(mergeCommit, state.PullRequest.HeadCommit)
	var teamWait *runstate.TeamMerge
	err := r.recovering(ctx, state, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		item, err := r.Tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return fmt.Errorf("load work item %s: %w", state.WorkItemID, err)
		}
		if itemSettled(*state, item.Status) {
			return nil
		}
		if !notesCarry(item.Notes, personMergedHeadline(*state, mergeCommit)) {
			if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, notes); err != nil {
				return fmt.Errorf("record the merge of pull request %d on %s: %w", state.PersonMerge.Number, state.WorkItemID, err)
			}
		}
		if !state.Discharges() {
			settled, err := settleUndischarged(ctx, r.Tracker, *state)
			if err != nil {
				return fmt.Errorf("reopen the work item run %s did not discharge: %w", state.RunID, err)
			}
			arranged = &settled
			return nil
		}
		if awaitsTeamMerge(*state) {
			// Merged into the target branch by a person, not yet into the team's:
			// the item stays claimed (teammerge.go).
			scratch := *state
			if err := recordTeamMergeWait(ctx, r.Tracker, &scratch, landing, r.clock().Now()); err != nil {
				return err
			}
			teamWait = scratch.TeamMerge
			return nil
		}
		if _, err := r.Tracker.Complete(ctx, state.WorkItemID, personMergedCompletionReason(*state, mergeCommit)); err != nil {
			return fmt.Errorf("close work item %s on the merge of pull request %d: %w", state.WorkItemID, state.PersonMerge.Number, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	update := runstate.PersonMergeMerged{
		Number:                   state.PersonMerge.Number,
		MergeCommit:              mergeCommit,
		ForgeState:               nonEmpty(observed.State, "MERGED"),
		LandingBlockedBy:         state.LandingBlockedBy,
		LandingImpedimentProblem: state.LandingImpedimentProblem,
		At:                       r.clock().Now(),
	}
	if arranged != nil {
		update.LandingBlockedBy, update.LandingImpedimentProblem = arranged.LandingBlockedBy, arranged.LandingImpedimentProblem
	}
	recorded, err := r.Store.Update(ctx, state.RunID, update)
	if err != nil {
		return fmt.Errorf("record the merge of pull request %d on run %s: %w", update.Number, state.RunID, err)
	}
	*state = recorded
	if teamWait != nil && state.Integration != nil {
		recorded, err := r.Store.Update(ctx, state.RunID, runstate.TeamMergeAwaited{Wait: *teamWait, At: r.clock().Now()})
		if err != nil {
			return fmt.Errorf("record that run %s waits for the team's merge: %w", state.RunID, err)
		}
		*state = recorded
	}
	// The run is now recorded as landed through its pull request and put at its
	// cleanup, which is finished here the way a merge the forge made for the
	// harness is finished: the branch and checkout are removed once the local
	// target is proven to carry the change. A cleanup that cannot finish — a
	// merge that rewrote the change, such as a squash, leaves the target without
	// the run's own commit — keeps the run owing that step, retried by every
	// sweep and shown as such, and does not undo the merge or the item's
	// settlement.
	if state.Integration != nil {
		if _, err := r.completeIntegrated(ctx, *state, false); err != nil {
			result.CleanupProblem = err.Error()
		}
	}
	return nil
}

// returnPersonMergeClosed gives the item of a pull request closed without
// merging back to the Lead Product Manager, parked with who closed it and when,
// and keeps the branch and the run's checkout.
func (r Reconciler) returnPersonMergeClosed(ctx context.Context, state *runstate.State, observed publish.PullRequest, result *PersonMergeSettlement) error {
	number := state.PersonMerge.Number
	if r.Closers == nil {
		return fmt.Errorf("pull request %d is closed without merging, and this check has no forge access to read who closed it; it is read again at the next check", number)
	}
	closer, err := r.Closers.Closer(ctx, number)
	if err != nil {
		return fmt.Errorf("read who closed pull request %d: %w; it is read again at the next check", number, err)
	}
	result.ClosedBy = closer.By
	closedAt := closer.At
	result.ClosedAt = &closedAt
	note, parking := personMergeClosedNotes(*state, closer)
	err = r.recovering(ctx, state, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		item, err := r.Tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return fmt.Errorf("load work item %s: %w", state.WorkItemID, err)
		}
		// An item somebody already closed, or one already given back by an
		// earlier attempt, keeps what it has.
		if item.Status == "closed" || (item.Status == "open" && item.Parking.Reason() == parking.Reason() && beads.NotesEndWith(item.Notes, note)) {
			return nil
		}
		if _, err := r.Tracker.Reopen(ctx, state.WorkItemID, note, parking); err != nil {
			return fmt.Errorf("give work item %s back to the Lead Product Manager: %w", state.WorkItemID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	recorded, err := r.Store.Update(ctx, state.RunID, runstate.PersonMergeReturned{
		Number:     number,
		ClosedBy:   closer.By,
		ClosedAt:   closedAt,
		ForgeState: nonEmpty(observed.State, "CLOSED"),
		At:         r.clock().Now(),
	})
	if err != nil {
		return fmt.Errorf("record the closing of pull request %d on run %s: %w", number, state.RunID, err)
	}
	*state = recorded
	return nil
}

// updatePersonMerge brings an open request a person is to merge up to date
// with a target that moved past it, at most once per check: the merge
// authority is withdrawn and confirmed withdrawn, the item is told, and the run
// is put back at its promotion, live and holding a developer slot, for
// ContinueUpdates to host. A request the forge is already merging is left
// alone. Whatever stops this short leaves the wait as it stands, says why, and
// the next check asks again.
func (r Reconciler) updatePersonMerge(ctx context.Context, recorded runstate.State, observed publish.PullRequest, result PersonMergeSettlement) PersonMergeSettlement {
	target := strings.TrimSpace(recorded.TargetBranch)
	if r.Checks == nil || target == "" {
		return result
	}
	number := recorded.PersonMerge.Number
	if observed.AutoMerge {
		result.Kept = fmt.Sprintf("a merge of pull request %d is in progress on the forge, so it is left alone", number)
		return result
	}
	behindBy, err := r.behindBy(ctx, number, observed.HeadCommit, target)
	if err != nil {
		result.Failure = fmt.Errorf("read whether pull request %d is behind %s: %w; it is read again at the next check", number, target, err).Error()
		return result
	}
	if behindBy <= 0 {
		return result
	}
	result.BehindBy = behindBy
	keep := func(why string) PersonMergeSettlement {
		result.Kept = fmt.Sprintf("pull request %d is %d commit(s) behind %s, and %s, so the next check tries again to bring it up to date", number, behindBy, target, why)
		return result
	}
	if refusal := cannotUpdatePersonMerge(recorded); refusal != "" {
		return r.refusePersonMergeUpdate(ctx, recorded, behindBy, refusal, result)
	}
	if !r.HostsRuns {
		return keep("this pass hosts no runs (`yoyo reconcile` does)")
	}
	if r.Intake != nil {
		hold, held, err := r.Intake.Held()
		if err != nil {
			return keep(fmt.Sprintf("whether intake is held could not be read (%v)", err))
		}
		if held {
			return keep(fmt.Sprintf("intake is held (%s)", nonEmpty(hold.Reason, "no reason recorded")))
		}
	}
	if free, err := r.slotFree(); err != nil || !free {
		if err != nil {
			return keep(fmt.Sprintf("what is in flight could not be read (%v)", err))
		}
		return keep("every developer slot is taken")
	}

	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		return keep(fmt.Sprintf("a live process holds run %s", recorded.RunID))
	case err != nil:
		result.Failure = fmt.Errorf("adopt run %s to bring pull request %d up to date: %w", recorded.RunID, number, err).Error()
		return result
	}
	defer lease.Release()
	if !state.WaitingForPersonMerge() || state.PersonMerge.Number != number {
		result.Kept = fmt.Sprintf("run %s was settled while the forge was being asked", recorded.RunID)
		return result
	}
	// The replay is made onto the local target, so the local target is brought
	// onto the remote's first. One that cannot be is left as it stands for the
	// convergence sweep, and nothing has been withdrawn yet.
	if catchup := r.catchUp(ctx, target); catchup.Held != "" {
		return keep(fmt.Sprintf("the local %s could not be brought onto the remote's (%s)", target, catchup.Held))
	}
	// The merge authority is withdrawn before anything rewrites the head, and
	// only the request's own state says it is withdrawn: a merge left armed would
	// land the replayed head the moment its checks passed, before any reviewer
	// had seen it.
	if err := r.Checks.DisableAutoMerge(ctx, number); err != nil {
		result.Failure = fmt.Errorf("withdraw the merge authority of pull request %d before bringing it up to date: %w; it is tried again at the next check", number, err).Error()
		return result
	}
	withdrawn, err := r.Publisher.State(ctx, state.PullRequest.Branch)
	switch {
	case err != nil:
		result.Failure = fmt.Errorf("confirm the merge authority of pull request %d withdrawn: %w; it is tried again at the next check", number, err).Error()
		return result
	case withdrawn.Number != number || withdrawn.Merged || !strings.EqualFold(strings.TrimSpace(withdrawn.State), "open"):
		result.Kept = fmt.Sprintf("pull request %d was merged or closed while its merge authority was being withdrawn, so the next check settles it", number)
		return result
	case withdrawn.AutoMerge:
		result.Failure = fmt.Sprintf("the forge still holds a merge of pull request %d after it was asked to withdraw it, so its head is not rewritten; it is tried again at the next check", number)
		return result
	}

	reason := fmt.Sprintf("%s moved %d commit(s) past pull request %d while it waited for a person to merge it, so the forge check withdrew its merge authority and put the run back at its promotion to replay the change onto %s, run the checks and the independent review again, and push the result to the same pull request",
		target, behindBy, number, target)
	notes := renderPersonMergeUpdateNotes(state, reason)
	err = r.recovering(ctx, &state, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		item, err := r.Tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return fmt.Errorf("load work item %s: %w", state.WorkItemID, err)
		}
		if beads.NotesEndWith(item.Notes, notes) {
			return nil
		}
		if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, notes); err != nil {
			return fmt.Errorf("record on %s that pull request %d is being brought up to date: %w", state.WorkItemID, number, err)
		}
		return nil
	})
	if err != nil {
		result.Failure = err.Error()
		return result
	}
	// The wait ends here and starts again when the run publishes the updated
	// head (activeRun.complete); while the run is live it holds a developer slot
	// and is not waiting on anybody.
	resumed := state.PutBackToBringUpToDate(boundedReason(reason), r.clock().Now())
	if err := r.Store.ReserveLive(ctx, resumed, r.Capacity); err != nil {
		if _, taken := slotTakenMeanwhile(err); taken {
			return keep("the last developer slot went to another run first")
		}
		result.Failure = fmt.Errorf("put run %s back at its promotion to bring pull request %d up to date: %w", state.RunID, number, err).Error()
		return result
	}
	result.Updating = true
	result.Update = reason
	return result
}

// BehindReads is the forge answering how far a target branch has moved past a
// pull request's head, and nothing else. A Checks that has it is asked this
// rather than for the whole check reading, which also reads the head, its files
// and every check run on it (publish.GitHub.Behind, asked conditionally).
type BehindReads interface {
	Behind(ctx context.Context, head, base string) (int, error)
}

// behindBy is how many commits target carries past the request's head.
func (r Reconciler) behindBy(ctx context.Context, number int, head, target string) (int, error) {
	if light, ok := r.Checks.(BehindReads); ok && strings.TrimSpace(head) != "" {
		return light.Behind(ctx, strings.TrimSpace(head), target)
	}
	reading, err := r.Checks.Checks(ctx, number, target)
	if err != nil {
		return 0, err
	}
	return reading.BehindBy, nil
}

// personMergedHeadline is the part of the merge's note that says the same
// whichever check writes it. What follows it says how the local target was
// caught up, which a check that writes the note again after an earlier one's
// write timed out finds already done, so it is the headline that says whether
// the note is already on the item.
func personMergedHeadline(state runstate.State, mergeCommit string) string {
	return strings.Join([]string{
		fmt.Sprintf("Pull request #%d was merged by a person, so the change this run published has landed and the item is settled on it.", state.PersonMerge.Number),
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"Merge commit: " + nonEmpty(mergeCommit, "not named by the forge"),
	}, "\n")
}

// cannotUpdatePersonMerge says why a run waiting for a person to merge its pull
// request cannot be put back at its promotion, and nothing for one that can.
func cannotUpdatePersonMerge(state runstate.State) string {
	switch {
	case state.WorktreePath == "" || state.WorktreeRemoved:
		return "its worktree is gone"
	case state.Branch == "" || state.BranchRemoved:
		return "its branch is gone"
	case state.BaseCommit == "":
		return "its record names no base commit"
	case state.ReviewDecision != runstate.ReviewApprove || strings.TrimSpace(state.ReviewSessionID) == "":
		return "its record carries no standing approval"
	case strings.TrimSpace(state.ProviderSessionID) == "":
		return "its record names no developer session"
	}
	return ""
}

// refusePersonMergeUpdate settles a request behind its target that the harness
// cannot bring up to date, for a reason no later check will clear: the item is
// told once and the reason is recorded on the wait, which every surface then
// shows as the development manager's to decide (a re-run onto the moved target,
// or leaving the request to the person). A request whose reason is already
// recorded is left as it stands. The wait goes on either way, so a merge or a
// closing is still settled by the next check.
func (r Reconciler) refusePersonMergeUpdate(ctx context.Context, recorded runstate.State, behind int, refusal string, result PersonMergeSettlement) PersonMergeSettlement {
	number := recorded.PersonMerge.Number
	target := recorded.TargetBranch
	reason := boundedReason(fmt.Sprintf("pull request %d is %d commit(s) behind %s, and the harness cannot bring it up to date because %s", number, behind, target, refusal))
	result.Kept = reason + "; the development manager decides what becomes of it"
	if recorded.PersonMerge.UpdateRefused != "" {
		return result
	}
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		return result
	case err != nil:
		result.Failure = fmt.Errorf("adopt run %s to record why pull request %d cannot be brought up to date: %w", recorded.RunID, number, err).Error()
		return result
	}
	defer lease.Release()
	if !state.WaitingForPersonMerge() || state.PersonMerge.Number != number || state.PersonMerge.UpdateRefused != "" {
		return result
	}
	notes := renderPersonMergeRefusedNotes(state, reason)
	err = r.recovering(ctx, &state, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		item, err := r.Tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return fmt.Errorf("load work item %s: %w", state.WorkItemID, err)
		}
		if beads.NotesEndWith(item.Notes, notes) {
			return nil
		}
		if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, notes); err != nil {
			return fmt.Errorf("record on %s that pull request %d cannot be brought up to date: %w", state.WorkItemID, number, err)
		}
		return nil
	})
	if err != nil {
		result.Failure = err.Error()
		return result
	}
	if _, err := r.Store.Update(ctx, state.RunID, runstate.PersonMergeUpdateRefused{Number: number, Reason: reason, At: r.clock().Now()}); err != nil {
		result.Failure = fmt.Errorf("record on run %s why pull request %d cannot be brought up to date: %w", state.RunID, number, err).Error()
	}
	return result
}

// renderPersonMergeRefusedNotes tells the item its waiting pull request cannot
// be brought up to date, and who decides what becomes of it.
func renderPersonMergeRefusedNotes(state runstate.State, reason string) string {
	return strings.Join([]string{
		"Yoyodyne cannot bring this item's waiting pull request up to date with its target: " + reason + ".",
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"The pull request is left as it is and still waits for a person to merge it. Where the project requires branches to be up to date before merging, it cannot be merged until something changes, so the development manager decides what becomes of it: a re-run of the item onto the moved target, or leaving it to the person.",
	}, "\n")
}

// renderPersonMergeUpdateNotes tells the work item its waiting pull request is
// being brought up to date.
func renderPersonMergeUpdateNotes(state runstate.State, reason string) string {
	return strings.Join([]string{
		fmt.Sprintf("Yoyodyne is bringing pull request #%d up to date, because the target moved while it waited for a person to merge it.", state.PersonMerge.Number),
		"Outcome: " + reason,
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		fmt.Sprintf("If the replay conflicts, the developer is given the conflict to settle, up to %d times, and those rounds are not counted against the item's repair attempts; after that the run goes to the development manager with the change kept. Nothing is pushed over a merge a person already has in progress.", runstate.MaxUpToDateConflicts),
	}, "\n")
}

// personMergeLocalTime is how a time on these notes is written: the operator's
// local time with its zone named.
func personMergeLocalTime(at time.Time) string {
	return at.Local().Format("2006-01-02 15:04 MST")
}

// renderPersonMergedNotes is the note a merge a person made leaves on the item.
func renderPersonMergedNotes(state runstate.State, mergeCommit string, catchup *gitworktree.Catchup) string {
	lines := []string{personMergedHeadline(state, mergeCommit)}
	if catchup == nil {
		lines = append(lines, "The run recorded no target branch, so no local branch was brought onto the merge.")
	}
	return strings.Join(append(lines, renderCatchupNotes(catchup)...), "\n")
}

// personMergedCompletionReason is the reason the item closes with.
func personMergedCompletionReason(state runstate.State, mergeCommit string) string {
	return fmt.Sprintf("Reviewed by Yoyodyne run %s, then merged by a person into %s through pull request %d at %s",
		state.RunID, nonEmpty(state.TargetBranch, "its target branch"), state.PersonMerge.Number, nonEmpty(mergeCommit, "a merge commit the forge did not name"))
}

// personMergeClosedNotes is the note and the parking a pull request closed
// without merging leaves on its item. The parking is what keeps the item from
// being started again before the Lead Product Manager decides, and it is kept
// to the bound a parking reason is held to.
func personMergeClosedNotes(state runstate.State, closer publish.Closer) (string, domain.WorkItemParking) {
	who := "an account the forge did not name"
	if closer.By != "" {
		who = closer.By
	}
	when := personMergeLocalTime(closer.At)
	kept := "The branch " + state.Branch
	if state.WorktreePath != "" {
		kept += " and the run's checkout at " + state.WorktreePath
	}
	note := strings.Join([]string{
		fmt.Sprintf("Pull request #%d was closed without being merged, by %s at %s.", state.PersonMerge.Number, who, when),
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		kept + " are kept, and nothing was deleted.",
		"The item goes back to the Lead Product Manager, who decides whether to drop the work, revise it, or admit it again.",
	}, "\n")
	parking := fmt.Sprintf("Pull request #%d was closed without merging by %s at %s; the Lead Product Manager decides whether to drop, revise, or admit this work again.",
		state.PersonMerge.Number, who, when)
	return note, domain.WorkItemParking(oneline.Bound(parking, domain.MaxWorkItemParkingBytes))
}

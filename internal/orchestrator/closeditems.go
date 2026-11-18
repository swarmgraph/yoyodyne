package orchestrator

// Closing the pull requests earlier runs left open once their work is over.
//
// The sweep in supersession.go closes a run's pull request when another run of
// the same item landed, on the harness's own record of that landing. Three
// kinds of request it does not reach stayed open for weeks, each one an
// approved change somebody could merge over its replacement by accident:
//
//   - the request of a run retired after its item merged (runretirement.go),
//     which keeps its branch and checkout and was never revisited;
//   - the request of a run whose item was closed or retired in the tracker
//     with no landing the harness recorded for it;
//   - a request whose change reached the target branch through another pull
//     request, a different commit with the same content.
//
// This step reads every ended run holding an open request and decides each one
// from three facts: whether the change is already on the target branch, and
// through which pull request (gitworktree's ChangeLanded); whether the item is
// open, closed, or retired; and whether a replacement is on record (the
// retirement's confirmed merge, or a later run of the item that landed).
//
//   - A change already on the target through another pull request is closed
//     naming that pull request, whatever the item's state.
//   - An open item's request is otherwise never touched: it may be waiting in a
//     merge queue, held for the development manager, or about to be repaired.
//   - A closed item's request is closed where its change is on the target or a
//     replacement is on record, and kept otherwise — the request, its branch
//     and its checkout — with the reason on the item and a finding the
//     development manager is shown, because an item can be closed while its
//     work never reached the target, and that request is then the only copy.
//   - A retired item's request with neither is closed only once that finding
//     has been shown, saying the item was retired; its branches are kept.
//
// A closed request's remote branch, local branch and checkout are let go where
// the work landed or was replaced: the checkout's uncommitted work is kept on a
// run-scoped ref first, and each branch is deleted only at the exact commit the
// request carried, which the forge keeps on the closed request.
//
// Document runs are left to documentsupersession.go, and a request the earlier
// sweep in the same pass already acted on is left to it, so no request is
// closed twice.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// LandedChanges is the repository access this step needs beyond the sweep's:
// reading whether a change is on its target, and letting a replaced branch go.
// It is satisfied by gitworktree.Manager.
type LandedChanges interface {
	ChangeLanded(ctx context.Context, targetBranch, head string) (gitworktree.ChangeLanding, error)
	ReleaseReplacedBranch(ctx context.Context, branch, commit string) (bool, error)
}

// retiredWithoutBeingDone is how the tracker's close reason begins on an item
// the Lead Product Manager retired, as chat's tracker writes it.
const retiredWithoutBeingDone = "Retired from the backlog without being done"

// ClosedItemPublication is one earlier run's open pull request and what this
// step did with it.
type ClosedItemPublication struct {
	Finding        *readmodel.Attention `json:"finding,omitempty"`
	FindingProblem string               `json:"finding_problem,omitempty"`
	RunID          string               `json:"run_id"`
	WorkItemID     string               `json:"work_item_id"`
	WorkItemTitle  string               `json:"work_item_title,omitempty"`
	Number         int                  `json:"number"`
	URL            string               `json:"url,omitempty"`
	// Why is the reason it was closed or kept, in the words the comment and the
	// item's note use.
	Why string `json:"why"`
	// ReplacedBy is what replaced the request, where something did.
	ReplacedBy    string `json:"replaced_by,omitempty"`
	Closed        bool   `json:"closed"`
	Kept          bool   `json:"kept,omitempty"`
	BranchDeleted bool   `json:"branch_deleted,omitempty"`
	// Released lists what of the run's own copy was let go: its checkout, its
	// local branch. ReleaseKept says why something was not.
	Released      []string `json:"released,omitempty"`
	ReleaseKept   string   `json:"release_kept,omitempty"`
	PreservedWork string   `json:"preserved_work,omitempty"`
	Failure       string   `json:"failure,omitempty"`
	ItemProblem   string   `json:"item_problem,omitempty"`
}

// closedItemAction is what to do with one request.
type closedItemAction int

const (
	leaveRequest closedItemAction = iota
	closeRequest
	keepRequest
)

// requestDecision is the decision about one request, made from what was
// read and nothing else, so it can be tested apart from the forge and Git.
type requestDecision struct {
	action closedItemAction
	// why says in a sentence why the request is closed or kept.
	why string
	// replacedBy names the vehicle, where one replaced the request.
	replacedBy string
	// release says the run's branches and checkout may be let go.
	release bool
}

// closedItemCandidate reports a run holding a pull request this step may
// decide about: harness-opened by being on a run record, open as far as the
// record says, not merged, queued, handed back or already retired by anything,
// and from a run that has ended. A run that succeeded without integrating is
// one waiting for a person or a merge queue, and is theirs unless the harness
// retired it. A document's request is the document clean-up's.
func closedItemCandidate(state runstate.State) bool {
	published := state.PullRequest
	if published == nil || state.Document != nil {
		return false
	}
	if published.Merged || published.MergeQueued || published.HandedBack != nil || strings.TrimSpace(published.Superseded) != "" || !stillOpen(*published) {
		return false
	}
	if !state.Status.Terminal() {
		return false
	}
	if state.Retirement == nil && (state.Integration != nil || state.Status == runstate.StatusSucceeded) {
		return false
	}
	return true
}

// decideClosedItemPublication decides one request. item is nil where the
// tracker's closed work could not be read or does not include the item, which
// is read as open; landing is nil where it could not be read.
func decideClosedItemPublication(state runstate.State, item *beads.WorkItem, landing *gitworktree.ChangeLanding, replacement string) requestDecision {
	target := state.TargetBranch
	if landing != nil && landing.Landed && landing.Number > 0 && landing.Number != state.PullRequest.Number {
		return requestDecision{
			action:     closeRequest,
			why:        fmt.Sprintf("its change is already on %s: it landed through pull request #%d (commit %s), so merging this one would add nothing", target, landing.Number, shortCommit(landing.Commit)),
			replacedBy: fmt.Sprintf("pull request #%d, commit %s on %s", landing.Number, landing.Commit, target),
			release:    true,
		}
	}
	if item == nil || item.Status != "closed" {
		return requestDecision{action: leaveRequest}
	}
	if landing != nil && landing.Landed {
		return requestDecision{
			action:     closeRequest,
			why:        fmt.Sprintf("the work item is closed and its change is already on %s, brought in by commit %s", target, shortCommit(landing.Commit)),
			replacedBy: fmt.Sprintf("commit %s on %s", landing.Commit, target),
			release:    true,
		}
	}
	if replacement != "" {
		return requestDecision{
			action:     closeRequest,
			why:        "the work item is closed and its work landed through " + replacement + ", so this earlier pull request will not be merged",
			replacedBy: replacement,
			release:    true,
		}
	}
	if strings.HasPrefix(strings.TrimSpace(item.CloseReason), retiredWithoutBeingDone) {
		if retiredRequestShown(state) {
			return requestDecision{
				action: closeRequest,
				why:    "the work item was retired without being done, and the development manager has been shown this pull request, so it will not be merged",
			}
		}
		return requestDecision{
			action: keepRequest,
			why: fmt.Sprintf("the work item was retired without being done, and nothing shows its change on %s or names what replaced it; pull request #%d %s, and is closed at a later sweep once it has been shown",
				target, state.PullRequest.Number, ownership.PublicationKeptForDecision),
		}
	}
	return requestDecision{
		action: keepRequest,
		why: fmt.Sprintf("the work item is closed, but nothing shows its change on %s or names what replaced it, so this may be the only copy of the work; pull request #%d %s to decide",
			target, state.PullRequest.Number, ownership.PublicationKeptForDecision),
	}
}

// retiredRequestShown reports a retired item's kept request having been put in
// front of the development manager: the finding saying so was delivered on an
// earlier sweep.
func retiredRequestShown(state runstate.State) bool {
	for _, finding := range state.ReconcileFindings {
		if finding.Step == runstate.ReconcileSuperseded && !finding.Pending &&
			strings.Contains(finding.Problem, "retired without being done") && strings.Contains(finding.Problem, ownership.PublicationKeptForDecision) {
			return true
		}
	}
	return false
}

// recordedReplacement names a replacement the harness's own records hold for a
// run's request: the confirmed merge its retirement names, or the latest later
// run of the same item that landed.
func recordedReplacement(state runstate.State, recorded []runstate.State) string {
	if retirement := state.Retirement; retirement != nil {
		return fmt.Sprintf("pull request #%d, which the forge merged for run %s at commit %s on %s", retirement.Number, retirement.RunID, retirement.Commit, retirement.TargetBranch)
	}
	var latest runstate.State
	for _, other := range recorded {
		if other.RunID == state.RunID || other.WorkItemID != state.WorkItemID || other.Integration == nil || handedBack(other) || !landedAfter(other, state.StartedAt) {
			continue
		}
		if latest.RunID == "" || laterLanding(other, latest) {
			latest = other
		}
	}
	if latest.RunID == "" {
		return ""
	}
	return supersessionOf(latest).Vehicle()
}

// sweepClosedItemPublications decides every candidate request and acts on it.
// handled names the runs the earlier sweep in this pass already acted on.
func (r Reconciler) sweepClosedItemPublications(ctx context.Context, recorded []runstate.State, handled map[string]bool) ([]ClosedItemPublication, error) {
	results := make([]ClosedItemPublication, 0)
	candidates := make([]runstate.State, 0)
	for _, state := range recorded {
		if !handled[state.RunID] && closedItemCandidate(state) {
			candidates = append(candidates, state)
		}
	}
	if len(candidates) == 0 {
		return results, nil
	}
	closed, listErr := r.closedItemsByID(ctx)
	landings, _ := r.Worktrees.(LandedChanges)
	for _, state := range candidates {
		if ctx.Err() != nil {
			return results, errors.Join(listErr, ctx.Err())
		}
		var item *beads.WorkItem
		if found, ok := closed[state.WorkItemID]; ok {
			item = &found
		}
		var landing *gitworktree.ChangeLanding
		var landingProblem string
		if landings != nil && state.PullRequest.HeadCommit != "" && state.TargetBranch != "" {
			read, err := landings.ChangeLanded(ctx, state.TargetBranch, state.PullRequest.HeadCommit)
			switch {
			case err != nil:
				landingProblem = err.Error()
			case read.Unread != "":
				landingProblem = read.Unread
			default:
				landing = &read
			}
		}
		decision := decideClosedItemPublication(state, item, landing, recordedReplacement(state, recorded))
		if decision.action == leaveRequest {
			continue
		}
		if decision.action == keepRequest && landingProblem != "" {
			decision.why += "; whether its change is on " + state.TargetBranch + " could not be read: " + landingProblem
		}
		result, acted := r.actOnClosedItemPublication(ctx, state, item, decision, landings)
		if !acted {
			continue
		}
		problem := result.Failure
		if result.Kept {
			problem = result.Why
		}
		result.Finding, result.FindingProblem = r.recordReconcileFinding(ctx, result.RunID, runstate.ReconcileSuperseded, problem)
		results = append(results, result)
	}
	if listErr != nil {
		listErr = &ClosedItemsUnread{Need: "close the pull requests earlier runs left open for closed and retired work items", Err: listErr}
	}
	return results, listErr
}

// closedItemsByID reads the closed work once for this step, from the sweep's
// shared listing where it has one. A listing the tracker cut short is used for
// what it holds.
func (r Reconciler) closedItemsByID(ctx context.Context) (map[string]beads.WorkItem, error) {
	var lister closedItemLister
	switch {
	case r.ClosedListing != nil:
		lister = r.ClosedListing
	default:
		listing, ok := r.Tracker.(closedItemLister)
		if !ok {
			// A sweep wired with a tracker that cannot list reads every item as
			// open, which closes nothing on an item's state: only a change already
			// on its target through another pull request is acted on.
			return map[string]beads.WorkItem{}, nil
		}
		lister = listing
	}
	items, err := lister.List(ctx, "closed")
	var cut *beads.ListingCut
	if errors.As(err, &cut) {
		items, err = cut.Items, nil
	}
	closed := make(map[string]beads.WorkItem, len(items))
	for _, item := range items {
		closed[item.ID] = item
	}
	return closed, err
}

// actOnClosedItemPublication closes or keeps one request under its run's lease,
// and reports whether there is anything to say about it. The record is read
// again under the lease, so a request something else settled since the listing
// is left alone, and a run a live process holds is that process's.
func (r Reconciler) actOnClosedItemPublication(ctx context.Context, recorded runstate.State, item *beads.WorkItem, decision requestDecision, landings LandedChanges) (ClosedItemPublication, bool) {
	result := ClosedItemPublication{
		RunID:         recorded.RunID,
		WorkItemID:    recorded.WorkItemID,
		WorkItemTitle: itemTitle(recorded, item),
		Number:        recorded.PullRequest.Number,
		URL:           recorded.PullRequest.URL,
		Why:           decision.why,
		ReplacedBy:    decision.replacedBy,
	}
	if decision.action == keepRequest {
		// Nothing is touched: the finding recorded after this says why, on the
		// item and in front of the development manager.
		if hasKeptFinding(recorded, decision.why) {
			return result, false
		}
		result.Kept = true
		return result, true
	}
	if r.Publisher == nil {
		result.Failure = fmt.Sprintf("pull request #%d %s, because this sweep has no forge access", result.Number, ownership.PublicationNotClosed)
		return result, true
	}
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		return ClosedItemPublication{}, false
	case err != nil:
		result.Failure = fmt.Sprintf("pull request #%d %s: the record of run %s could not be taken: %v", result.Number, ownership.PublicationNotClosed, recorded.RunID, err)
		return result, true
	}
	defer lease.Release()
	if !closedItemCandidate(state) {
		return ClosedItemPublication{}, false
	}
	published := *state.PullRequest
	closure, err := r.Publisher.Close(ctx, publish.CloseRequest{
		Head:    published.Branch,
		Number:  published.Number,
		Comment: renderClosedItemComment(state, result.WorkItemTitle, decision),
		Mark:    forgeMark(ctx, r.Factory, state.ProductID),
	})
	if err != nil {
		result.Failure = closeFailure(published.Number, err)
		return result, true
	}
	if closure.Merged {
		result.Failure = fmt.Sprintf("pull request #%d %s: the forge says it is merged, so its own work reached the remote; the sweep that refreshes publications records the merge on run %s", published.Number, ownership.PublicationNotClosed, state.RunID)
		return result, true
	}
	result.Closed = true
	updated, err := r.Store.Update(ctx, state.RunID, runstate.PullRequestSuperseded{
		Number: published.Number, Vehicle: closedVehicle(decision), State: "CLOSED", At: r.clock().Now(), Wrote: closeCommentID(closure),
	})
	if err != nil {
		// The forge is settled and the record is not, so the next sweep asks the
		// forge again; Close reports a closed request without a second comment.
		result.Failure = fmt.Sprintf("pull request #%d was closed and run %s's record could not say so: %v", published.Number, state.RunID, err)
		return result, true
	}
	state = updated
	if decision.release {
		r.releaseReplacedRun(ctx, &state, landings, &result)
	}
	result.ItemProblem = r.noteClosedItemPublication(ctx, state, result)
	return result, true
}

// hasKeptFinding reports the same keep already recorded on the run, which needs
// no second line on every sweep.
func hasKeptFinding(state runstate.State, why string) bool {
	for _, finding := range state.ReconcileFindings {
		if finding.Step == runstate.ReconcileSuperseded && !finding.Pending && finding.Problem == runstate.RecordReconcileProblem(why) {
			return true
		}
	}
	return false
}

// closedVehicle is what the run's record keeps as what ended its request.
func closedVehicle(decision requestDecision) string {
	if decision.replacedBy != "" {
		return decision.replacedBy
	}
	return "its work item was retired without being done"
}

// releaseReplacedRun lets go of the run's remote branch, checkout and local
// branch, in the order that never strands one: the checkout holds the local
// branch, so it goes first, with its uncommitted work kept on a ref. A recovery
// the development manager decided on the run keeps all of it, unless the run
// was retired, which ended any such recovery.
func (r Reconciler) releaseReplacedRun(ctx context.Context, state *runstate.State, landings LandedChanges, result *ClosedItemPublication) {
	published := *state.PullRequest
	worktree := worktreeOf(*state)
	worktree.Branch = published.Branch
	if err := r.Worktrees.DeleteRemoteBranch(ctx, worktree, published.HeadCommit); err != nil {
		result.ReleaseKept = fmt.Sprintf("the branch %s on the forge was not deleted: %v", published.Branch, err)
	} else {
		result.BranchDeleted = true
	}
	if state.Retirement == nil {
		kept, release := r.recoveryNeedsArtifacts(ctx, *state)
		release()
		if kept != "" {
			result.ReleaseKept = joinKept(result.ReleaseKept, kept)
			return
		}
	}
	if state.WorktreePath != "" && !state.WorktreeRemoved {
		removal, err := r.Worktrees.RemovePreservedWorktree(ctx, worktreeOf(*state), gitworktree.CaptureUncommittedWork)
		switch {
		case err != nil:
			result.ReleaseKept = joinKept(result.ReleaseKept, fmt.Sprintf("the checkout %s was not removed: %v", state.WorktreePath, err))
			return
		case removal.Kept != "":
			result.ReleaseKept = joinKept(result.ReleaseKept, removal.Kept)
			return
		case removal.Removed:
			result.Released = append(result.Released, "checkout "+state.WorktreePath)
			result.PreservedWork = removal.PreservedWork
			if problem := r.recordSweptWorktree(state, removal.PreservedWork); problem != "" {
				result.ReleaseKept = joinKept(result.ReleaseKept, problem)
			}
		}
	}
	if landings == nil || state.Branch == "" || state.BranchRemoved {
		return
	}
	// The local branch goes only at the commit the request carried; one that
	// moved past it holds something the request never had.
	if _, err := landings.ReleaseReplacedBranch(ctx, state.Branch, published.HeadCommit); err != nil {
		result.ReleaseKept = joinKept(result.ReleaseKept, fmt.Sprintf("the branch %s was kept: %v", state.Branch, err))
		return
	}
	result.Released = append(result.Released, "branch "+state.Branch)
	if problem := r.recordSweptBranch(*state); problem != "" {
		result.ReleaseKept = joinKept(result.ReleaseKept, problem)
	}
}

func joinKept(kept, more string) string {
	if kept == "" {
		return more
	}
	return kept + "; " + more
}

// noteClosedItemPublication tells the work item its earlier pull request was
// closed, once: a note already carried is not written again.
func (r Reconciler) noteClosedItemPublication(ctx context.Context, state runstate.State, result ClosedItemPublication) string {
	note := renderClosedItemNote(state, result)
	recordCtx, cancel := trackerCallContext(ctx)
	defer cancel()
	item, err := r.Tracker.Show(recordCtx, state.WorkItemID)
	if err != nil {
		return fmt.Sprintf("pull request #%d was closed and %s could not be read to be told: %v", result.Number, state.WorkItemID, err)
	}
	if strings.Contains(item.Notes, closedItemHeadline(result)) {
		return ""
	}
	if _, err := r.Tracker.RecordOutcome(recordCtx, state.WorkItemID, note); err != nil {
		return fmt.Sprintf("pull request #%d was closed and %s could not be told: %v", result.Number, state.WorkItemID, err)
	}
	return ""
}

func closedItemHeadline(result ClosedItemPublication) string {
	return fmt.Sprintf("Yoyodyne closed pull request #%d, from run %s, without merging it.", result.Number, result.RunID)
}

// renderClosedItemNote is what the item is told: which request, why, and what
// of the run's own copy was let go.
func renderClosedItemNote(state runstate.State, result ClosedItemPublication) string {
	lines := []string{
		closedItemHeadline(result),
		"Why: " + result.Why + ".",
	}
	if result.ReplacedBy != "" {
		lines = append(lines, "Replaced by: "+result.ReplacedBy)
	}
	if len(result.Released) > 0 {
		lines = append(lines, "Let go: "+strings.Join(result.Released, ", ")+".")
	}
	if result.PreservedWork != "" {
		lines = append(lines, "Uncommitted work from the checkout is kept at "+result.PreservedWork+".")
	}
	if result.ReleaseKept != "" {
		lines = append(lines, "Kept: "+result.ReleaseKept+".")
	}
	return strings.Join(lines, "\n")
}

// renderClosedItemComment is what the closed pull request is left carrying. It
// names the work item by its title and identifier and says why in a sentence,
// because a request closed by a machine with no account of why is as unclear as
// one left open.
func renderClosedItemComment(state runstate.State, title string, decision requestDecision) string {
	lines := []string{
		"Yoyodyne closed this pull request without merging it: " + decision.why + ".",
		"",
		fmt.Sprintf("- Work item: %s (`%s`)", title, state.WorkItemID),
		"- This pull request: run `" + state.RunID + "` on branch `" + state.PullRequest.Branch + "`",
	}
	if decision.replacedBy != "" {
		lines = append(lines, "- Replaced by: "+decision.replacedBy)
	}
	lines = append(lines, "")
	if decision.release {
		lines = append(lines, "The branch this pull request carries is being deleted with it; its commits stay readable here.")
	} else {
		lines = append(lines, "The branch this pull request carries is kept, so the change can still be read or picked up again.")
	}
	return strings.Join(lines, "\n")
}

// closeFailure says why a close failed, and marks a refusal only somebody with
// rights on the repository can lift so the finding is theirs.
func closeFailure(number int, err error) string {
	failure := fmt.Sprintf("pull request #%d %s: %v", number, ownership.PublicationNotClosed, err)
	said := strings.ToLower(err.Error())
	for _, refusal := range []string{"resource not accessible", "http 403", "must have admin rights", "must have push access", "permission"} {
		if strings.Contains(said, refusal) {
			return failure + "; the forge refused the harness's account, so this " + ownership.PublicationNeedsForgeHand + ": give that account the right to close pull requests, or close #" + fmt.Sprint(number) + " by hand"
		}
	}
	return failure
}

// itemTitle is the work item's title from the tracker where it was read, the
// run's copy where not, and the identifier where neither has one.
func itemTitle(state runstate.State, item *beads.WorkItem) string {
	if item != nil && strings.TrimSpace(item.Title) != "" {
		return item.Title
	}
	if strings.TrimSpace(state.WorkItemTitle) != "" {
		return state.WorkItemTitle
	}
	return state.WorkItemID
}

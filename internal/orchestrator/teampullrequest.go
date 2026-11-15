package orchestrator

// Pull requests into the team's branch that the harness did not open.
//
// A change waiting for the team's merge (teammergesettle.go) is closed when its
// landing commit is found on the team's branch. When an outside tool carries a
// batch of such changes to the team's branch in a pull request of its own, it
// tells the harness which pull request carries which items
// (TrackTeamPullRequest). Each forge check then reads every tracked pull request
// (SettleTeamPullRequests): one merged closes the items it carries, one closed
// without merging gives them back to the Lead Product Manager, and one still
// open picks up the waiting items whose landing commit it carries but nobody
// named.
//
// Recording a pull request is information, not a decision: it grants nothing
// beyond what those three outcomes say. Every write reads the item first and is
// not made twice, so a check that failed part-way finishes at the next one.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TeamPullRequestReader reads a pull request by its number, with the commits it
// carries. It only reads, and is satisfied by publish.GitHub.
type TeamPullRequestReader interface {
	TeamPullRequest(ctx context.Context, number int) (publish.TeamPullRequest, error)
}

// teamPullRequestCommit is what the forge's merge commit has to look like for it
// to be recorded as where the items were found.
var teamPullRequestCommit = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)

// The states of a pull request, as a settlement reports them.
const (
	TeamPullRequestOpen   = "open"
	TeamPullRequestMerged = "merged"
	TeamPullRequestClosed = "closed"
)

// TeamPullRequestTracking is an outside tool's word that a pull request into the
// team's branch carries some waiting items. TargetBranch and UpstreamBranch are
// the project's configured branches.
type TeamPullRequestTracking struct {
	Number         int      `json:"number"`
	Items          []string `json:"items"`
	Unverified     bool     `json:"unverified,omitempty"`
	TargetBranch   string   `json:"target_branch"`
	UpstreamBranch string   `json:"upstream_branch"`
}

// TrackedItem is what recording or removing one item's pull request did.
type TrackedItem struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	// MovedFrom is the pull request the item was recorded under before.
	MovedFrom int `json:"moved_from,omitempty"`
	// Unverified reports the landing commit was not among the pull request's
	// commits and the item was recorded anyway.
	Unverified bool `json:"unverified,omitempty"`
	// Unchanged reports the item was already recorded as asked.
	Unchanged bool   `json:"unchanged,omitempty"`
	Failure   string `json:"failure,omitempty"`
}

// TeamPullRequestTrack is what recording or removing a pull request did.
type TeamPullRequestTrack struct {
	Number int           `json:"number"`
	URL    string        `json:"url,omitempty"`
	Items  []TrackedItem `json:"items"`
}

// waitingForTeam lists the runs waiting for the team's merge.
func (r Reconciler) waitingForTeam() ([]runstate.State, error) {
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
	return waiting, nil
}

// TrackTeamPullRequest records that a pull request into the team's branch
// carries the named items. The pull request is read first, and nothing is
// recorded unless every reason to refuse is absent; the refusal names them all.
// An item that cannot be recorded after that, such as one a live process holds,
// is said on its own entry and never stops the others.
func (r Reconciler) TrackTeamPullRequest(ctx context.Context, request TeamPullRequestTracking) (TeamPullRequestTrack, error) {
	waiting, err := r.waitingForTeam()
	if err != nil {
		return TeamPullRequestTrack{}, err
	}
	number := request.Number
	target := strings.TrimSpace(request.TargetBranch)
	upstream := strings.TrimSpace(request.UpstreamBranch)

	var refusals []string
	teamBranch := upstream != "" && upstream != target
	if !teamBranch {
		refusals = append(refusals, "this project names no team branch (execution.upstream_branch) apart from its target branch, so there is no pull request into the team's branch to track")
	}
	if number <= 0 {
		refusals = append(refusals, "a pull request number is needed")
	}
	var ids []string
	for _, id := range request.Items {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		refusals = append(refusals, "no item is named; name each item the pull request carries")
	}

	var pull publish.TeamPullRequest
	read := false
	if teamBranch && number > 0 {
		if r.TeamPullRequests == nil {
			refusals = append(refusals, fmt.Sprintf("this command has no forge access to read pull request #%d", number))
		} else if pull, err = r.TeamPullRequests.TeamPullRequest(ctx, number); err != nil {
			refusals = append(refusals, fmt.Sprintf("pull request #%d could not be read: %v", number, err))
		} else {
			read = true
			if pull.BaseBranch != upstream {
				refusals = append(refusals, fmt.Sprintf("pull request #%d goes into %s, not into the team's branch %s", number, pull.BaseBranch, upstream))
			}
			if pull.State == "CLOSED" && !pull.Merged {
				refusals = append(refusals, fmt.Sprintf("pull request #%d was closed without being merged", number))
			}
		}
	}

	waits := map[string]runstate.WaitingForTeamMergeItem{}
	for _, wait := range runstate.WaitingForTeamMergeOf(waiting) {
		waits[wait.WorkItemID] = wait
	}
	unverified := map[string]bool{}
	for _, id := range ids {
		wait, ok := waits[id]
		switch {
		case !ok:
			refusals = append(refusals, fmt.Sprintf("%s is not waiting for the team's merge", id))
			continue
		case wait.TargetBranch != target:
			refusals = append(refusals, fmt.Sprintf("%s landed on %s, not on this project's target branch %s", id, wait.TargetBranch, target))
			continue
		case wait.UpstreamBranch != upstream:
			refusals = append(refusals, fmt.Sprintf("%s waits for the team's merge into %s, not into the team's branch %s", id, wait.UpstreamBranch, upstream))
			continue
		}
		if !read || slices.Contains(pull.Commits, wait.LandingCommit) {
			continue
		}
		if !request.Unverified {
			reason := fmt.Sprintf("%s's landing commit %s is not among pull request #%d's commits; name it with --unverified only if the batch was built from new commits, such as a squash or picked commits", id, wait.LandingCommit, number)
			if pull.CommitsCapped {
				reason += " (the forge lists only the first 250 commits of a request, and this one has more)"
			}
			refusals = append(refusals, reason)
			continue
		}
		unverified[id] = true
	}
	if len(refusals) == 1 {
		return TeamPullRequestTrack{}, errors.New(refusals[0])
	}
	if len(refusals) > 1 {
		return TeamPullRequestTrack{}, fmt.Errorf("nothing was recorded, for these reasons:\n- %s", strings.Join(refusals, "\n- "))
	}

	track := TeamPullRequestTrack{Number: number, URL: pull.URL, Items: make([]TrackedItem, 0, len(ids))}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return track, err
		}
		track.Items = append(track.Items, r.trackTeamPullRequestItem(ctx, waits[id], number, pull.URL, upstream, unverified[id]))
	}
	return track, nil
}

// trackTeamPullRequestItem records one item under the pull request, under its
// run's lease.
func (r Reconciler) trackTeamPullRequestItem(ctx context.Context, wait runstate.WaitingForTeamMergeItem, number int, url, upstream string, unverified bool) TrackedItem {
	result := TrackedItem{WorkItemID: wait.WorkItemID, RunID: wait.RunID, Unverified: unverified}
	state, lease, kept, err := r.adoptTeamWait(ctx, wait.RunID, wait.LandingCommit,
		fmt.Sprintf("a live process holds run %s, so %s is not recorded now; run this again once it has stopped", wait.RunID, wait.WorkItemID))
	if err != nil {
		result.Failure = err.Error()
		return result
	}
	if kept != "" {
		result.Failure = kept
		return result
	}
	defer lease.Release()

	have := state.TeamMerge.PullRequest
	if have != nil && have.Number == number && have.Unverified == unverified {
		result.Unchanged = true
		return result
	}
	if have != nil && have.Number != number {
		result.MovedFrom = have.Number
	}
	lines := []string{fmt.Sprintf("Pull request #%d into %s carries this item, so the item closes when the team merges it.", number, upstream)}
	headline := lines[0]
	if result.MovedFrom != 0 {
		lines = append(lines, fmt.Sprintf("It was carried by pull request #%d before.", result.MovedFrom))
	}
	if unverified {
		lines = append(lines, fmt.Sprintf("Its landing commit %s was not found among the pull request's commits; it was recorded as unverified at the outside tool's word.", wait.LandingCommit))
	}
	lines = append(lines, "Run: "+state.RunID)
	if err := r.recordTeamPullRequestNote(ctx, &state, headline, strings.Join(lines, "\n")); err != nil {
		result.Failure = err.Error()
		return result
	}
	now := r.clock().Now()
	if _, err := r.Store.Update(ctx, state.RunID, runstate.TeamPullRequestTracked{
		LandingCommit: wait.LandingCommit,
		PullRequest:   runstate.TeamPullRequest{Number: number, URL: url, Unverified: unverified, TrackedAt: now},
		At:            now,
	}); err != nil {
		result.Failure = fmt.Errorf("record pull request #%d on run %s: %w", number, state.RunID, err).Error()
	}
	return result
}

// UntrackTeamPullRequest removes the record of a pull request from every item it
// is recorded on. The items go on waiting for their landing commit to be found
// on the team's branch.
func (r Reconciler) UntrackTeamPullRequest(ctx context.Context, number int) (TeamPullRequestTrack, error) {
	waiting, err := r.waitingForTeam()
	if err != nil {
		return TeamPullRequestTrack{}, err
	}
	track := TeamPullRequestTrack{Number: number}
	for _, state := range waiting {
		if pull := state.TeamMerge.PullRequest; pull != nil && pull.Number == number {
			if track.URL == "" {
				track.URL = pull.URL
			}
			track.Items = append(track.Items, r.untrackTeamPullRequestItem(ctx, state, number))
		}
	}
	if len(track.Items) == 0 {
		return TeamPullRequestTrack{}, fmt.Errorf("no item waiting for the team's merge is tracked under pull request #%d", number)
	}
	return track, nil
}

func (r Reconciler) untrackTeamPullRequestItem(ctx context.Context, recorded runstate.State, number int) TrackedItem {
	result := TrackedItem{WorkItemID: recorded.WorkItemID, RunID: recorded.RunID}
	state, lease, kept, err := r.adoptTeamWait(ctx, recorded.RunID, recorded.TeamMerge.LandingCommit,
		fmt.Sprintf("a live process holds run %s, so %s is not changed now; run this again once it has stopped", recorded.RunID, recorded.WorkItemID))
	if err != nil {
		result.Failure = err.Error()
		return result
	}
	if kept != "" {
		result.Failure = kept
		return result
	}
	defer lease.Release()

	if pull := state.TeamMerge.PullRequest; pull == nil || pull.Number != number {
		result.Unchanged = true
		return result
	}
	headline := fmt.Sprintf("Pull request #%d is no longer tracked as carrying this item; it still waits for its landing commit to be found on %s.", number, state.TeamMerge.UpstreamBranch)
	if err := r.recordTeamPullRequestNote(ctx, &state, headline, headline+"\nRun: "+state.RunID); err != nil {
		result.Failure = err.Error()
		return result
	}
	if _, err := r.Store.Update(ctx, state.RunID, runstate.TeamPullRequestUntracked{Number: number, At: r.clock().Now()}); err != nil {
		result.Failure = fmt.Errorf("take pull request #%d off run %s: %w", number, state.RunID, err).Error()
	}
	return result
}

// adoptTeamWait takes a run waiting for the team's merge under its lease and
// reads it again, because the listing it was chosen from may be out of date. A
// non-empty kept says why the run is left as it stands, in which case there is
// no lease; otherwise the lease is the caller's to release.
func (r Reconciler) adoptTeamWait(ctx context.Context, runID, landingCommit, ifHeld string) (runstate.State, *runstate.Lease, string, error) {
	state, lease, err := r.Store.AdoptRun(ctx, runID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		return runstate.State{}, nil, ifHeld, nil
	case err != nil:
		return runstate.State{}, nil, "", fmt.Errorf("adopt run %s for the team's pull request: %w", runID, err)
	}
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != landingCommit {
		lease.Release()
		return runstate.State{}, nil, fmt.Sprintf("run %s was settled while this was being read", runID), nil
	}
	return state, lease, "", nil
}

// recordTeamPullRequestNote writes a note on the run's item unless the item's
// notes already carry its first line.
func (r Reconciler) recordTeamPullRequestNote(ctx context.Context, state *runstate.State, headline, note string) error {
	return r.recovering(ctx, state, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		item, err := r.Tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return fmt.Errorf("load work item %s: %w", state.WorkItemID, err)
		}
		if notesCarry(item.Notes, headline) {
			return nil
		}
		if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, note); err != nil {
			return fmt.Errorf("record the team's pull request on %s: %w", state.WorkItemID, err)
		}
		return nil
	})
}

// TeamPullRequestSettlement is what one check did about one tracked pull request.
type TeamPullRequestSettlement struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
	// Found is the state the forge reported (TeamPullRequestOpen, ...Merged or
	// ...Closed), empty when the request could not be read.
	Found       string `json:"found,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
	// ClosedBy and ClosedAt are who closed a request without merging it, and when.
	ClosedBy string                `json:"closed_by,omitempty"`
	ClosedAt *time.Time            `json:"closed_at,omitempty"`
	Items    []TeamPullRequestItem `json:"items,omitempty"`
	Failure  string                `json:"failure,omitempty"`
}

// TeamPullRequestItem is what one check did about one item of a tracked pull
// request.
type TeamPullRequestItem struct {
	WorkItemID    string `json:"work_item_id"`
	RunID         string `json:"run_id"`
	LandingCommit string `json:"landing_commit"`
	// Settled reports the item closed because the request merged, or given back
	// because it was closed without merging.
	Settled bool `json:"settled"`
	// Added reports the item found among the request's commits and recorded as
	// carried by it.
	Added bool `json:"added,omitempty"`
	// Kept is why the item was deliberately left as it stands.
	Kept    string `json:"kept,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// SettleTeamPullRequests reads every pull request that carries a waiting item,
// once each, and acts on what it finds: a merged request closes its items, one
// closed without merging gives them back, and an open one adds the waiting items
// it carries that nobody named. One that cannot be read or finished never stops
// the others, and is read again by the next check.
func (r Reconciler) SettleTeamPullRequests(ctx context.Context) ([]TeamPullRequestSettlement, error) {
	waiting, err := r.waitingForTeam()
	if err != nil {
		return nil, err
	}
	carried := map[int][]runstate.State{}
	for _, state := range waiting {
		if pull := state.TeamMerge.PullRequest; pull != nil {
			carried[pull.Number] = append(carried[pull.Number], state)
		}
	}
	settled := make([]TeamPullRequestSettlement, 0, len(carried))
	if len(carried) == 0 {
		return settled, nil
	}
	if r.TeamPullRequests == nil {
		return settled, fmt.Errorf("%d pull request(s) carry changes waiting for the team's merge, and this check cannot read them", len(carried))
	}
	numbers := make([]int, 0, len(carried))
	for number := range carried {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	for _, number := range numbers {
		if err := ctx.Err(); err != nil {
			return settled, err
		}
		settled = append(settled, r.settleTeamPullRequest(ctx, number, carried[number], waiting))
	}
	return settled, nil
}

func (r Reconciler) settleTeamPullRequest(ctx context.Context, number int, carried, waiting []runstate.State) TeamPullRequestSettlement {
	result := TeamPullRequestSettlement{Number: number}
	if pull := carried[0].TeamMerge.PullRequest; pull != nil {
		result.URL = pull.URL
	}
	pull, err := r.TeamPullRequests.TeamPullRequest(ctx, number)
	if err != nil {
		result.Failure = fmt.Errorf("read pull request #%d: %w; it is read again at the next check", number, err).Error()
		return result
	}
	if pull.URL != "" {
		result.URL = pull.URL
	}
	switch {
	case pull.Merged || pull.State == "MERGED":
		result.Found = TeamPullRequestMerged
		result.MergeCommit = pull.MergeCommit
		if !teamPullRequestCommit.MatchString(pull.MergeCommit) {
			result.Failure = fmt.Sprintf("the forge names no merge commit for pull request #%d; it is read again at the next check", number)
			return result
		}
		for _, state := range carried {
			result.Items = append(result.Items, r.settleTeamPullRequestMerged(ctx, state, number, pull.MergeCommit))
		}
	case pull.State == "CLOSED":
		result.Found = TeamPullRequestClosed
		r.settleTeamPullRequestClosed(ctx, number, carried, &result)
	case pull.State == "OPEN":
		result.Found = TeamPullRequestOpen
		for _, state := range waiting {
			if state.TeamMerge.PullRequest != nil || state.TeamMerge.UpstreamBranch != pull.BaseBranch ||
				!slices.Contains(pull.Commits, state.TeamMerge.LandingCommit) {
				continue
			}
			result.Items = append(result.Items, r.addToTeamPullRequest(ctx, state, pull))
		}
	default:
		result.Failure = fmt.Sprintf("the forge reports pull request #%d as %q, which this check does not know; it is read again at the next check", number, pull.State)
	}
	return result
}

// adoptTracked is adoptTeamWait for a run recorded under a pull request, which
// must still be recorded under it once the run is held.
func (r Reconciler) adoptTracked(ctx context.Context, recorded runstate.State, number int) (runstate.State, *runstate.Lease, TeamPullRequestItem) {
	item := TeamPullRequestItem{WorkItemID: recorded.WorkItemID, RunID: recorded.RunID, LandingCommit: recorded.TeamMerge.LandingCommit}
	state, lease, kept, err := r.adoptTeamWait(ctx, recorded.RunID, recorded.TeamMerge.LandingCommit,
		fmt.Sprintf("a live process holds run %s, so the next check settles it", recorded.RunID))
	if err != nil {
		item.Failure = err.Error()
		return state, nil, item
	}
	if kept != "" {
		item.Kept = kept
		return state, nil, item
	}
	if pull := state.TeamMerge.PullRequest; pull == nil || pull.Number != number {
		lease.Release()
		item.Kept = fmt.Sprintf("run %s is no longer tracked under pull request #%d", recorded.RunID, number)
		return state, nil, item
	}
	return state, lease, item
}

// settleTeamPullRequestMerged closes the item of one run carried by a request
// the team merged.
func (r Reconciler) settleTeamPullRequestMerged(ctx context.Context, recorded runstate.State, number int, mergeCommit string) TeamPullRequestItem {
	state, lease, item := r.adoptTracked(ctx, recorded, number)
	if lease == nil {
		return item
	}
	defer lease.Release()
	wait := *state.TeamMerge
	// The team's merge settles where the work is, not whether it is the work the
	// item asked for, so the record is asked again, as settleTeamMerge asks it.
	if !state.Discharges() {
		item.Kept = fmt.Sprintf("run %s's record no longer says its change discharges %s, so the team's merge does not close it", state.RunID, state.WorkItemID)
		return item
	}
	headline := fmt.Sprintf("The team merged pull request #%d, which carries this item, into %s as %s, so the item is closed.", number, wait.UpstreamBranch, mergeCommit)
	err := r.closeOnTeamMerge(ctx, &state, teamMergeClosing{
		Branch:   wait.UpstreamBranch,
		Headline: headline,
		Notes:    headline + "\n" + fmt.Sprintf("It landed on %s as %s. Run: %s", wait.TargetBranch, wait.LandingCommit, state.RunID),
		Reason: fmt.Sprintf("Reviewed by Yoyodyne run %s and landed on %s as %s, which the team merged into %s through pull request #%d as %s",
			state.RunID, wait.TargetBranch, wait.LandingCommit, strings.TrimSpace(wait.UpstreamBranch), number, mergeCommit),
		Found: runstate.TeamMergeFound{LandingCommit: wait.LandingCommit, FoundOn: mergeCommit, PullRequest: number, At: r.clock().Now()},
	})
	if err != nil {
		item.Failure = err.Error()
		return item
	}
	item.Settled = true
	return item
}

// settleTeamPullRequestClosed gives the items of a request closed without
// merging back to the Lead Product Manager. Who closed it is read once.
func (r Reconciler) settleTeamPullRequestClosed(ctx context.Context, number int, carried []runstate.State, result *TeamPullRequestSettlement) {
	if r.Closers == nil {
		result.Failure = fmt.Sprintf("pull request #%d is closed without merging, and this check has no forge access to read who closed it; it is read again at the next check", number)
		return
	}
	closer, err := r.Closers.Closer(ctx, number)
	if err != nil {
		result.Failure = fmt.Sprintf("read who closed pull request #%d: %v; it is read again at the next check", number, err)
		return
	}
	if closer.At.IsZero() {
		closer.At = r.clock().Now()
	}
	result.ClosedBy = closer.By
	closedAt := closer.At
	result.ClosedAt = &closedAt
	for _, recorded := range carried {
		result.Items = append(result.Items, r.returnTeamPullRequestItem(ctx, recorded, number, closer))
	}
}

// returnTeamPullRequestItem gives one item back, and keeps the change on the
// target branch where it landed.
func (r Reconciler) returnTeamPullRequestItem(ctx context.Context, recorded runstate.State, number int, closer publish.Closer) TeamPullRequestItem {
	state, lease, item := r.adoptTracked(ctx, recorded, number)
	if lease == nil {
		return item
	}
	defer lease.Release()
	note, parking := teamPullRequestClosedNotes(state, number, closer)
	err := r.recovering(ctx, &state, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		current, err := r.Tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return fmt.Errorf("load work item %s: %w", state.WorkItemID, err)
		}
		// An item somebody already closed, or one an earlier attempt already gave
		// back, keeps what it has.
		if current.Status == closedStatus || (current.Status == "open" && current.Parking.Reason() == parking.Reason() && beads.NotesEndWith(current.Notes, note)) {
			return nil
		}
		if _, err := r.Tracker.Reopen(ctx, state.WorkItemID, note, parking); err != nil {
			return fmt.Errorf("give work item %s back to the Lead Product Manager: %w", state.WorkItemID, err)
		}
		return nil
	})
	if err != nil {
		item.Failure = err.Error()
		return item
	}
	if _, err := r.Store.Update(ctx, state.RunID, runstate.TeamPullRequestClosed{
		LandingCommit: state.TeamMerge.LandingCommit,
		Number:        number,
		ClosedBy:      closer.By,
		ClosedAt:      closer.At,
		At:            r.clock().Now(),
	}); err != nil {
		item.Failure = fmt.Errorf("record the closing of pull request #%d on run %s: %w", number, state.RunID, err).Error()
		return item
	}
	item.Settled = true
	return item
}

// teamPullRequestClosedNotes is the note and the parking a request closed
// without merging leaves on each item it carried. The parking keeps the item
// from being started again before the Lead Product Manager decides, and is kept
// to the bound a parking reason is held to.
func teamPullRequestClosedNotes(state runstate.State, number int, closer publish.Closer) (string, domain.WorkItemParking) {
	wait := state.TeamMerge
	who := "an account the forge did not name"
	if closer.By != "" {
		who = closer.By
	}
	when := personMergeLocalTime(closer.At)
	note := strings.Join([]string{
		fmt.Sprintf("Pull request #%d into %s, which carried this item, was closed without being merged, by %s at %s.", number, wait.UpstreamBranch, who, when),
		"Run: " + state.RunID,
		fmt.Sprintf("The change stays on %s as %s, and nothing was undone.", wait.TargetBranch, wait.LandingCommit),
		fmt.Sprintf("The item goes back to the Lead Product Manager, who decides whether to leave it on %s for a later batch, admit an item that reverts it, or close it as wanted only on %s.", wait.TargetBranch, wait.TargetBranch),
	}, "\n")
	parking := fmt.Sprintf("Pull request #%d into %s was closed without merging by %s at %s; the Lead Product Manager decides whether to leave this change on %s for a later batch, revert it, or close the item.",
		number, wait.UpstreamBranch, who, when, wait.TargetBranch)
	return note, domain.WorkItemParking(oneline.Bound(parking, domain.MaxWorkItemParkingBytes))
}

// addToTeamPullRequest records an open request as carrying a waiting item that
// nobody named, because the item's landing commit is among its commits.
func (r Reconciler) addToTeamPullRequest(ctx context.Context, recorded runstate.State, pull publish.TeamPullRequest) TeamPullRequestItem {
	item := TeamPullRequestItem{WorkItemID: recorded.WorkItemID, RunID: recorded.RunID, LandingCommit: recorded.TeamMerge.LandingCommit}
	state, lease, kept, err := r.adoptTeamWait(ctx, recorded.RunID, recorded.TeamMerge.LandingCommit,
		fmt.Sprintf("a live process holds run %s, so the next check records it", recorded.RunID))
	if err != nil {
		item.Failure = err.Error()
		return item
	}
	if kept != "" {
		item.Kept = kept
		return item
	}
	defer lease.Release()
	if state.TeamMerge.PullRequest != nil {
		item.Kept = fmt.Sprintf("run %s was recorded under pull request #%d while this was being read", state.RunID, state.TeamMerge.PullRequest.Number)
		return item
	}
	wait := *state.TeamMerge
	headline := fmt.Sprintf("A forge check found this item's landing commit %s among the commits of pull request #%d into %s, so the pull request is now tracked as carrying it.", wait.LandingCommit, pull.Number, wait.UpstreamBranch)
	if err := r.recordTeamPullRequestNote(ctx, &state, headline, headline+"\nRun: "+state.RunID); err != nil {
		item.Failure = err.Error()
		return item
	}
	now := r.clock().Now()
	if _, err := r.Store.Update(ctx, state.RunID, runstate.TeamPullRequestTracked{
		LandingCommit: wait.LandingCommit,
		PullRequest:   runstate.TeamPullRequest{Number: pull.Number, URL: pull.URL, Found: true, TrackedAt: now},
		At:            now,
	}); err != nil {
		item.Failure = fmt.Errorf("record pull request #%d on run %s: %w", pull.Number, state.RunID, err).Error()
		return item
	}
	item.Added = true
	return item
}

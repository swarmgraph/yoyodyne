package orchestrator

// Feedback left on a pull request into the team's branch that the harness
// tracks but did not open (teampullrequest.go).
//
// It is read as feedback on the harness's own pull requests is read
// (forgefeedback.go): each check of the forge reads what was said on every
// tracked pull request still open, leaves out what a factory of this product
// wrote, and acts on the rest once. What differs is what acting means. A
// tracked pull request carries many items, and the runs that landed them have
// ended, so nothing said there starts a repair round on its own:
//
//   - A review that requests changes, or a comment on a line of the change, is
//     put on the development manager's list, naming the pull request, the
//     feedback, and the items whose landing commits touched the file it is
//     about. A review names no file, so every item the request carries is named.
//   - An ordinary comment is put on her list in the same way, naming no item.
//   - A person's approval is recorded on each item as information, and never
//     stands in for the independent review its change already passed.
//
// Whether the feedback calls for a change is her decision. A repair of a landed
// item cannot yet be started from here, because her decisions today attach
// only to a stopped run and an item waiting for the team's merge has none, so
// the report tells her to ask for a work item to be admitted instead.
//
// The cursor that keeps a piece of feedback from being acted on twice is kept
// on every run the request carries (runstate.TeamPullRequest.Feedback), and a
// piece any of them has passed is not acted on again, so a run that joins the
// request later does not bring old feedback back. It moves only after the
// development manager's list and the items have been told.
//
// The design is docs/designs/integrating-to-a-local-branch.md, section 2,
// "Feedback on a tracked pull request".

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/factory"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// LandingPathReader lists the paths a landing commit changed on its target
// branch. It only reads, and is satisfied by gitworktree.Manager.
type LandingPathReader interface {
	LandingPaths(ctx context.Context, commit string) ([]string, error)
}

// TeamFeedbackSettlement is what one check did about the feedback on one
// tracked pull request into the team's branch.
type TeamFeedbackSettlement struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
	// Items are the items the request carries, as the check found them.
	Items []string `json:"items"`
	// New is how many pieces of feedback the check found that nothing had acted
	// on, leaving out what a factory of this product wrote.
	New int `json:"new"`
	// Requested is how many requested changes and line comments went on the
	// development manager's list, Listed how many ordinary comments, and
	// Approvals how many approvals were recorded as information.
	Requested int    `json:"requested,omitempty"`
	Listed    int    `json:"listed,omitempty"`
	Approvals int    `json:"approvals,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Failure   string `json:"failure,omitempty"`
}

// TakeUpTeamFeedback reads the feedback on every tracked pull request into the
// team's branch and acts on what is new. It never starts a run. One request
// that cannot be read or acted on never stops the others, and its feedback is
// read again by the next check.
func (r Reconciler) TakeUpTeamFeedback(ctx context.Context) ([]TeamFeedbackSettlement, error) {
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
	settled := make([]TeamFeedbackSettlement, 0, len(carried))
	if len(carried) == 0 {
		return settled, nil
	}
	if r.Feedback == nil {
		return settled, fmt.Errorf("%d pull request(s) into the team's branch are tracked, and this check has no forge access to read what was said on them", len(carried))
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
		runs := carried[number]
		sort.Slice(runs, func(i, j int) bool { return runs[i].RunID < runs[j].RunID })
		settled = append(settled, r.takeUpTeamFeedback(ctx, number, runs))
	}
	return settled, nil
}

func (r Reconciler) takeUpTeamFeedback(ctx context.Context, number int, recorded []runstate.State) TeamFeedbackSettlement {
	result := TeamFeedbackSettlement{Number: number, URL: recorded[0].TeamMerge.PullRequest.URL, Items: teamFeedbackItems(recorded)}
	reading, err := r.Feedback.Feedback(ctx, number)
	if err != nil {
		result.Failure = fmt.Sprintf("read what was said on pull request #%d: %v; nothing was acted on, and the next check reads it again", number, err)
		return result
	}
	if reading.State != "" && reading.State != "OPEN" {
		result.Detail = fmt.Sprintf("pull request #%d is no longer open (%s), so what was said on it is not acted on", number, strings.ToLower(reading.State))
		return result
	}
	if fresh := newTeamFeedback(recorded, reading); len(fresh) == 0 {
		result.Detail = "nothing new was said on it"
		return result
	}

	// Every run the request carries is held while the feedback is acted on, so
	// no other check acts on the same feedback meanwhile. A run a live process
	// holds keeps its cursor where it is, and is told nothing it was not told by
	// the runs that were held.
	var held []runstate.State
	for _, state := range recorded {
		adopted, lease, kept, err := r.adoptTeamWait(ctx, state.RunID, state.TeamMerge.LandingCommit, "held")
		if err != nil {
			// The runs already held are released by their deferred releases.
			result.Failure = err.Error()
			return result
		}
		if kept != "" {
			continue
		}
		if pull := adopted.TeamMerge.PullRequest; pull == nil || pull.Number != number {
			lease.Release()
			continue
		}
		defer lease.Release()
		held = append(held, adopted)
	}
	if len(held) == 0 {
		result.Detail = "a live process holds every run this pull request carries, so its feedback is left for the next check"
		return result
	}
	result.Items = teamFeedbackItems(held)
	// The records read under the lease are the ones acted on: a cursor another
	// check moved meanwhile leaves less, or nothing, to act on.
	fresh := newTeamFeedback(held, reading)
	result.New = len(fresh)
	if len(fresh) == 0 {
		result.Detail = "nothing new was said on it"
		return result
	}

	now := r.clock().Now()
	var approvals []runstate.ForgeFeedbackItem
	for _, item := range fresh {
		switch {
		case item.Kind == runstate.FeedbackApproval:
			approvals = append(approvals, item)
		case item.Kind.StartsRepair():
			named, unread := r.itemsTouching(ctx, held, item)
			if err := r.listTeamFeedback(held, number, result.URL, item, named, unread, now); err != nil {
				result.Failure = fmt.Sprintf("put feedback on pull request #%d on the development manager's list: %v; nothing more was acted on, and the next check reads it again", number, err)
				return result
			}
			result.Requested++
		default:
			if err := r.listTeamFeedback(held, number, result.URL, item, nil, false, now); err != nil {
				result.Failure = fmt.Sprintf("put a comment on pull request #%d on the development manager's list: %v; nothing more was acted on, and the next check reads it again", number, err)
				return result
			}
			result.Listed++
		}
	}
	// Approvals are told on each item before the cursor moves past them; a note
	// an earlier check wrote is not written twice (noteOnce).
	if len(approvals) > 0 {
		for _, state := range held {
			if err := r.noteOnce(ctx, state.WorkItemID, renderTeamApprovalNotes(state, number, approvals)); err != nil {
				result.Failure = fmt.Sprintf("record the approvals on pull request #%d on %s: %v; the next check reads them again", number, state.WorkItemID, err)
				return result
			}
		}
		result.Approvals = len(approvals)
	}
	var failures []string
	for _, state := range held {
		if _, err := r.Store.Update(ctx, state.RunID, runstate.TeamPullRequestFeedbackRead{
			LandingCommit: state.TeamMerge.LandingCommit, Number: number, Read: fresh, At: now,
		}); err != nil {
			failures = append(failures, fmt.Sprintf("record what was read of pull request #%d on run %s: %v", number, state.RunID, err))
		}
	}
	if len(failures) == len(held) {
		result.Failure = strings.Join(failures, "; ")
	}
	result.Detail = describeTeamFeedbackSettlement(result)
	return result
}

// teamFeedbackItems lists the item identifiers the runs carry, in order.
func teamFeedbackItems(runs []runstate.State) []string {
	items := make([]string, 0, len(runs))
	for _, state := range runs {
		if !slices.Contains(items, state.WorkItemID) {
			items = append(items, state.WorkItemID)
		}
	}
	sort.Strings(items)
	return items
}

// newTeamFeedback is the feedback on a tracked request that no check has acted
// on: what was said after the request was first tracked, leaving out what a
// factory of this product wrote and what any carried run's cursor has passed.
// Feedback older than the tracking was said before the harness was told the
// request carries anything of its own.
func newTeamFeedback(runs []runstate.State, reading publish.FeedbackReading) []runstate.ForgeFeedbackItem {
	var since time.Time
	for _, state := range runs {
		at := state.TeamMerge.PullRequest.TrackedAt
		if since.IsZero() || at.Before(since) {
			since = at
		}
	}
	handled := func(item runstate.ForgeFeedbackItem) bool {
		for _, state := range runs {
			if state.TeamMerge.PullRequest.Feedback.Handled(item) {
				return true
			}
		}
		return false
	}
	var items []runstate.ForgeFeedbackItem
	keep := func(item runstate.ForgeFeedbackItem) {
		if item.Author == "" || teamOwnWords(runs, item) {
			return
		}
		if item.At.IsZero() || (!since.IsZero() && item.At.Before(since)) || handled(item) {
			return
		}
		items = append(items, boundedFeedback(item))
	}
	for _, item := range feedbackItemsOf(reading) {
		keep(item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.Before(items[j].At)
		}
		return items[i].ID < items[j].ID
	})
	return items
}

// feedbackItemsOf sorts a reading into feedback by kind, as newFeedback sorts
// it: a dismissed or pending review, a review that only commented with no text
// of its own, and an empty comment say nothing anybody stands behind.
func feedbackItemsOf(reading publish.FeedbackReading) []runstate.ForgeFeedbackItem {
	var items []runstate.ForgeFeedbackItem
	for _, review := range reading.Reviews {
		item := runstate.ForgeFeedbackItem{ID: runstate.FeedbackID("review", review.ID), Author: review.Author, At: review.At, Body: review.Body, URL: review.URL}
		switch review.State {
		case publish.ReviewChangesRequested:
			item.Kind = runstate.FeedbackChangesRequested
		case publish.ReviewApproved:
			item.Kind = runstate.FeedbackApproval
		case publish.ReviewCommented:
			if strings.TrimSpace(review.Body) == "" {
				continue
			}
			item.Kind = runstate.FeedbackComment
		default:
			continue
		}
		items = append(items, item)
	}
	for _, comment := range reading.LineComments {
		items = append(items, runstate.ForgeFeedbackItem{ID: runstate.FeedbackID("line-comment", comment.ID), Kind: runstate.FeedbackLineComment,
			Author: comment.Author, At: comment.At, Body: comment.Body, Path: comment.Path, Line: comment.Line, URL: comment.URL})
	}
	for _, comment := range reading.Comments {
		if strings.TrimSpace(comment.Body) == "" {
			continue
		}
		items = append(items, runstate.ForgeFeedbackItem{ID: runstate.FeedbackID("comment", comment.ID), Kind: runstate.FeedbackComment,
			Author: comment.Author, At: comment.At, Body: comment.Body, URL: comment.URL})
	}
	return items
}

// teamOwnWords reports a review or comment a factory of this product wrote,
// by the mark it carries (factory.Identity.Mark). The harness writes nothing on
// a pull request it did not open, so the mark is the whole test, and the
// account that wrote a piece is never looked at, for the reason ownWords gives.
// A record written before runs named their product takes any mark as this
// product's.
func teamOwnWords(runs []runstate.State, item runstate.ForgeFeedbackItem) bool {
	for _, state := range runs {
		if state.ProductID == "" {
			if len(factory.MarkedProducts(item.Body)) > 0 {
				return true
			}
			continue
		}
		if factory.MarkedBy(item.Body, state.ProductID) {
			return true
		}
	}
	return false
}

// itemsTouching names the items whose landing commits changed the file a piece
// of feedback is about. Feedback about no file names every item the request
// carries. Where a landing commit's paths cannot be read, every item is named
// and unread says so, so the list never leaves out an item it could not rule
// out.
func (r Reconciler) itemsTouching(ctx context.Context, runs []runstate.State, item runstate.ForgeFeedbackItem) (named []string, unread bool) {
	all := teamFeedbackItems(runs)
	if item.Path == "" {
		return all, false
	}
	if r.LandingPaths == nil {
		return all, true
	}
	for _, state := range runs {
		paths, err := r.LandingPaths.LandingPaths(ctx, state.TeamMerge.LandingCommit)
		if err != nil {
			return all, true
		}
		if slices.Contains(paths, item.Path) && !slices.Contains(named, state.WorkItemID) {
			named = append(named, state.WorkItemID)
		}
	}
	sort.Strings(named)
	return named, false
}

// listTeamFeedback puts one piece of feedback on the development manager's
// list, as a report addressed to her. A request for changes names the items it
// may be about; an ordinary comment names none.
func (r Reconciler) listTeamFeedback(runs []runstate.State, number int, url string, item runstate.ForgeFeedbackItem, named []string, unread bool, now time.Time) error {
	if r.Reports == nil {
		return errors.New("this check has nowhere to put reports")
	}
	first := runs[0]
	message, severity, workItem := teamFeedbackMessage(runs, number, url, item, named, unread)
	entry := report.Entry{Severity: severity, Message: oneline.Fold(message, report.MaxMessageBytes), For: domain.RoleDevelopmentManager}
	collected, err := report.Collect([]report.Entry{entry}, report.Attribution{
		Role: report.HarnessReporter, RunID: first.RunID, WorkItemID: workItem, Build: first.Build,
		ProductID: first.ProductID, RepositoryID: first.RepositoryID,
	}, now)
	if err != nil {
		return err
	}
	for _, reported := range collected {
		if err := r.Reports.Append(reported); err != nil {
			return err
		}
	}
	return nil
}

// teamFeedbackMessage is what the development manager reads about one piece of
// feedback: the request, who said what and where, which items it may be about,
// and that what happens next is hers. A report about exactly one item is
// attributed to it.
func teamFeedbackMessage(runs []runstate.State, number int, url string, item runstate.ForgeFeedbackItem, named []string, unread bool) (string, report.Severity, string) {
	wait := runs[0].TeamMerge
	where := fmt.Sprintf("pull request #%d into %s", number, wait.UpstreamBranch)
	if url != "" {
		where += " (" + url + ")"
	}
	said := fmt.Sprintf("%s on %s, which carries changes that landed on %s and wait for the team's merge: %q.",
		describeFeedbackItem(item), where, wait.TargetBranch, oneline.Bound(item.Body, 1500))
	if !item.Kind.StartsRepair() {
		return said + " It is a comment rather than a requested change, so no item is named and nothing was started; whether it calls for anything is yours to decide.", report.SeverityNote, ""
	}
	var about string
	switch {
	case unread:
		about = fmt.Sprintf(" Which landed changes touched %s could not be read, so every item the pull request carries is named: %s.", item.Path, describeTeamItems(runs, named))
	case item.Path == "":
		about = fmt.Sprintf(" It names no file, so every item the pull request carries is named: %s.", describeTeamItems(runs, named))
	case len(named) == 0:
		about = fmt.Sprintf(" No landed change the pull request carries touched %s; it carries %s.", item.Path, describeTeamItems(runs, teamFeedbackItems(runs)))
	default:
		about = fmt.Sprintf(" The landed changes that touched %s are those of %s.", item.Path, describeTeamItems(runs, named))
	}
	workItem := ""
	if len(named) == 1 {
		workItem = named[0]
	}
	return said + about + " Nothing was started, and a repair of a landed item cannot yet be started from feedback on a tracked pull request. " +
		"If the feedback calls for a change, ask the Lead Product Manager to admit a work item for it, naming this pull request and quoting the feedback; " +
		"that work runs on " + wait.TargetBranch + " through the configured checks and an independent review like any other.", report.SeverityWarning, workItem
}

// describeTeamItems names each item by its title with its identifier after
// it, as the run that landed it recorded them, or by its identifier alone
// where no title was recorded.
func describeTeamItems(runs []runstate.State, ids []string) string {
	titles := map[string]string{}
	for _, state := range runs {
		if state.WorkItemTitle != "" {
			titles[state.WorkItemID] = oneline.Bound(state.WorkItemTitle, 200)
		}
	}
	named := make([]string, 0, len(ids))
	for _, id := range ids {
		if title := titles[id]; title != "" {
			named = append(named, fmt.Sprintf("%s (%s)", title, id))
			continue
		}
		named = append(named, id)
	}
	return strings.Join(named, "; ")
}

func renderTeamApprovalNotes(state runstate.State, number int, approvals []runstate.ForgeFeedbackItem) string {
	lines := make([]string, 0, len(approvals)+1)
	for _, approval := range approvals {
		lines = append(lines, describeFeedbackItem(approval)+fmt.Sprintf(" on pull request #%d into %s.", number, state.TeamMerge.UpstreamBranch))
	}
	lines = append(lines, fmt.Sprintf("Recorded on run %s as information. A person's approval on the forge does not stand in for the independent review this change already passed, and does not change what this item is for.", state.RunID))
	return strings.Join(lines, "\n")
}

func describeTeamFeedbackSettlement(result TeamFeedbackSettlement) string {
	var said []string
	if result.Requested > 0 {
		said = append(said, fmt.Sprintf("%d requested change(s) or line comment(s) went on the development manager's list with the items they may be about", result.Requested))
	}
	if result.Listed > 0 {
		said = append(said, fmt.Sprintf("%d comment(s) went on the development manager's list", result.Listed))
	}
	if result.Approvals > 0 {
		said = append(said, fmt.Sprintf("%d approval(s) were recorded as information", result.Approvals))
	}
	if len(said) == 0 {
		return "nothing new was said on it"
	}
	return strings.Join(said, "; ") + "; nothing was started"
}

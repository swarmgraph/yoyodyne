package orchestrator

// What people say on a member's merged pull request while the operator's naming
// of its epic is open: docs/designs/epic-mode-second-part.md, piece 11, its
// second bullet and "Which pull requests are read, and for how long", points 2 to
// 5. Every other reader of pull request feedback skips a pull request that is no
// longer open, so this is the only one that reads a merged one, and it reads one
// only for a member of an epic the operator named and has not ended.
//
// The pull request read is the one whose merge the harness recorded for the
// member, with its merge commit: a run that is the item's work, whose pull
// request is recorded merged with a merge commit on it or on its wait for a
// person's merge. A pull request closed unmerged is not read, and a member done
// with no pull request has nothing to read. Whose comments count is the rule the
// reader of open pull requests uses (ownWords, an author, a time), approvals are
// not feedback, and only what was said after the merge is read. The merge's time
// is the one the forge reports on the pull request, read with it: the harness may
// record the merge later than it happened, and a comment made in between is still
// feedback on merged work.
//
// Which case a comment is, is decided when it is read, from where its epic stands
// then. While the epic is not delivered, the comment's identifier is written into
// the epic's progress with the one correcting piece it owes, in one write, before
// anything else is done with it, and the development manager writes that piece.
// Once the epic is delivered, integrated or handed over, the comment becomes one
// parked work item for the Lead Product Manager, which starts nothing: the
// comment is written into the progress first and the item's identifier after it,
// and the item carries a marker it is found by again (cli's epicmerged.go). Either
// way a comment read twice, or again after a restart, makes one piece or one item.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// EpicMember is one item of an epic the operator named and has not ended, while
// epic mode is on, and whether its epic is delivered now.
type EpicMember struct {
	Epic       string
	NamedAt    time.Time
	WorkItemID string
	Delivered  bool
}

// MergedComment is one piece of feedback on a member's merged pull request.
type MergedComment struct {
	Member      EpicMember
	PullRequest int
	Item        runstate.ForgeFeedbackItem
}

// EpicMergedFeedback is where the forge check finds the members of the open
// namings and records feedback on their merged pull requests. It is satisfied by
// the epics' progress the harness wires; nil reads no merged pull request.
type EpicMergedFeedback interface {
	// Members is each item of each naming that has not ended. It is empty with
	// epic mode off.
	Members(ctx context.Context) ([]EpicMember, error)
	// Owe writes the comment and the one correction it owes into the epic's
	// progress, and reports whether this write made it.
	Owe(ctx context.Context, comment MergedComment) (bool, error)
	// AfterDelivery writes a comment read once its epic is delivered into the
	// epic's progress and files the one parked item it becomes, and reports
	// whether this call filed that item.
	AfterDelivery(ctx context.Context, comment MergedComment) (bool, error)
}

// itemRunListing is the run store's listing of one item's runs, newest first.
type itemRunListing interface {
	Runs(workItemID string) ([]runstate.State, error)
}

// MergedFeedbackSettlement is what one check did about the feedback on one
// member's merged pull request.
type MergedFeedbackSettlement struct {
	Epic       string `json:"epic"`
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id,omitempty"`
	Number     int    `json:"number,omitempty"`
	// Owed is how many comments this check recorded as owing a correcting piece.
	Owed int `json:"owed,omitempty"`
	// Filed is how many comments read after delivery this check filed an item for.
	Filed   int    `json:"filed,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// TakeUpMergedFeedback reads the feedback on each merged pull request of a
// member of an open naming. One that cannot be read never stops the others, and
// its feedback is read again by the next check.
func (r Reconciler) TakeUpMergedFeedback(ctx context.Context) ([]MergedFeedbackSettlement, error) {
	if r.EpicMerged == nil {
		return nil, nil
	}
	members, err := r.EpicMerged.Members(ctx)
	if err != nil || len(members) == 0 {
		return nil, err
	}
	runs, can := r.Store.(itemRunListing)
	if !can {
		return nil, fmt.Errorf("%d member(s) of named epics are open, and this check cannot list an item's runs to find their merged pull requests", len(members))
	}
	var settled []MergedFeedbackSettlement
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return settled, err
		}
		listed, err := runs.Runs(member.WorkItemID)
		if err != nil {
			settled = append(settled, MergedFeedbackSettlement{Epic: member.Epic, WorkItemID: member.WorkItemID,
				Failure: fmt.Sprintf("list the runs of %s: %v; the next check reads them again", member.WorkItemID, err)})
			continue
		}
		for _, run := range mergedPullRequests(listed) {
			if r.Feedback == nil {
				return settled, fmt.Errorf("pull request %d of %s merged, and this check has no forge access to read what was said on it", run.PullRequest.Number, member.WorkItemID)
			}
			settled = append(settled, r.takeUpMergedFeedback(ctx, member, run))
		}
	}
	return settled, nil
}

// mergedPullRequests is each run that is the item's work and whose pull request
// the harness recorded merged with its merge commit, one for each pull request.
func mergedPullRequests(runs []runstate.State) []runstate.State {
	var merged []runstate.State
	seen := map[int]bool{}
	for _, run := range runs {
		if !run.Discharges() || run.PullRequest == nil || !run.PullRequest.Merged || mergeCommitOf(run) == "" || seen[run.PullRequest.Number] {
			continue
		}
		seen[run.PullRequest.Number] = true
		merged = append(merged, run)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].PullRequest.Number < merged[j].PullRequest.Number })
	return merged
}

// mergeCommitOf is the merge commit the harness recorded for a run's pull request.
func mergeCommitOf(run runstate.State) string {
	if run.PersonMerge != nil && run.PersonMerge.Merged() && strings.TrimSpace(run.PersonMerge.MergeCommit) != "" {
		return run.PersonMerge.MergeCommit
	}
	return strings.TrimSpace(run.PullRequest.MergeCommit)
}

// takeUpMergedFeedback reads one merged pull request and records what is new.
func (r Reconciler) takeUpMergedFeedback(ctx context.Context, member EpicMember, run runstate.State) MergedFeedbackSettlement {
	number := run.PullRequest.Number
	result := MergedFeedbackSettlement{Epic: member.Epic, WorkItemID: member.WorkItemID, RunID: run.RunID, Number: number}
	reading, err := r.Feedback.Feedback(ctx, number)
	if err != nil {
		result.Failure = fmt.Sprintf("read what was said on merged pull request %d: %v; the next check reads it again", number, err)
		return result
	}
	if reading.MergedAt.IsZero() {
		result.Detail = "the forge gave no time for its merge, so nothing said on it is read until it does"
		return result
	}
	fresh := newMergedFeedback(run, reading.MergedAt, feedbackItemsOf(reading))
	if len(fresh) == 0 {
		result.Detail = "nothing was said on it after it merged"
		return result
	}
	if member.Delivered {
		for _, item := range fresh {
			filed, err := r.EpicMerged.AfterDelivery(ctx, MergedComment{Member: member, PullRequest: number, Item: item})
			if err != nil {
				result.Failure = fmt.Sprintf("file comment %s on merged pull request %d: %s; the next check reads it again", item.ID, number, oneline.Bound(err.Error(), 600))
				return result
			}
			if filed {
				result.Filed++
			}
		}
		result.Detail = fmt.Sprintf("%d comment(s) were left after it merged; epic %s is delivered, so %d newly became an item that starts nothing, for the Lead Product Manager", len(fresh), member.Epic, result.Filed)
		return result
	}
	for _, item := range fresh {
		made, err := r.EpicMerged.Owe(ctx, MergedComment{Member: member, PullRequest: number, Item: item})
		if err != nil {
			result.Failure = fmt.Sprintf("record comment %s on merged pull request %d: %s; the next check reads it again", item.ID, number, oneline.Bound(err.Error(), 600))
			return result
		}
		if made {
			result.Owed++
		}
	}
	result.Detail = fmt.Sprintf("%d comment(s) were left after it merged; %d newly owe a correcting piece for the development manager to write", len(fresh), result.Owed)
	return result
}

// newMergedFeedback is what people said on a merged pull request after the forge's
// merge, by the rule the reader of open pull requests uses for whose words count,
// oldest first. Approvals are not feedback.
func newMergedFeedback(run runstate.State, since time.Time, items []runstate.ForgeFeedbackItem) []runstate.ForgeFeedbackItem {
	var fresh []runstate.ForgeFeedbackItem
	for _, item := range items {
		if item.Author == "" || item.Kind == runstate.FeedbackApproval || ownWords(run, item) || item.At.IsZero() || !item.At.After(since) {
			continue
		}
		fresh = append(fresh, boundedFeedback(item))
	}
	sort.SliceStable(fresh, func(i, j int) bool {
		if !fresh[i].At.Equal(fresh[j].At) {
			return fresh[i].At.Before(fresh[j].At)
		}
		return fresh[i].ID < fresh[j].ID
	})
	return fresh
}

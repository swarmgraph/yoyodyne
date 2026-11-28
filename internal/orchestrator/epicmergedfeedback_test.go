package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/factory"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// listedRuns answers each item's runs from a map, and nothing else.
type listedRuns struct {
	ReconcileStore
	runs map[string][]runstate.State
}

func (l listedRuns) Runs(item string) ([]runstate.State, error) { return l.runs[item], nil }

// owedComments holds the open namings' members and records each comment it is
// told owes a correction, once for each identifier, as the progress does.
type owedComments struct {
	members []EpicMember
	owed    []MergedComment
	seen    map[string]bool
}

func (o *owedComments) Members(context.Context) ([]EpicMember, error) { return o.members, nil }

func (o *owedComments) Owe(_ context.Context, comment MergedComment) (bool, error) {
	if o.seen == nil {
		o.seen = map[string]bool{}
	}
	if o.seen[comment.Item.ID] {
		return false, nil
	}
	o.seen[comment.Item.ID] = true
	o.owed = append(o.owed, comment)
	return true, nil
}

var mergedAt = time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)

// mergedRun is a run of the item whose pull request number the harness recorded
// merged, by a person, with its merge commit.
func mergedRun(item string, number int) runstate.State {
	at := mergedAt
	return runstate.State{RunID: "run-" + item, WorkItemID: item, ProductID: "yoyodyne",
		PullRequest: &runstate.PullRequest{Number: number, Merged: true, MergeCommit: "1111111111111111111111111111111111111111"},
		PersonMerge: &runstate.PersonMerge{Number: number, Since: at.Add(-time.Hour), MergedAt: &at, MergeCommit: "1111111111111111111111111111111111111111"}}
}

// mergedFixture is one member of an open epic, not delivered, whose pull request
// #7 merged, with one comment before the merge, one after, an approval after
// and one of the factory's own words after.
func mergedFixture() (Reconciler, *owedComments, *fakeTeamFeedback) {
	feedback := &fakeTeamFeedback{readings: map[int]publish.FeedbackReading{7: {
		State: "MERGED",
		Comments: []publish.FeedbackComment{
			{ID: 1, Author: "teammate", Body: "Before the merge.", At: mergedAt.Add(-time.Minute)},
			{ID: 2, Author: "teammate", Body: "The flag still has the old name.", At: mergedAt.Add(time.Hour)},
			{ID: 3, Author: "owner", Body: "Done. " + factory.Identity{Product: "yoyodyne"}.Mark(), At: mergedAt.Add(time.Hour)},
		},
		Reviews: []publish.FeedbackReview{{ID: 4, Author: "teammate", State: publish.ReviewApproved, At: mergedAt.Add(time.Hour)}},
	}}}
	epics := &owedComments{members: []EpicMember{{Epic: "yoyodyne-ep", NamedAt: mergedAt.Add(-24 * time.Hour), WorkItemID: "yoyodyne-ep.1"}}}
	reconciler := Reconciler{
		Store:      listedRuns{runs: map[string][]runstate.State{"yoyodyne-ep.1": {mergedRun("yoyodyne-ep.1", 7)}}},
		Feedback:   feedback,
		EpicMerged: epics,
	}
	return reconciler, epics, feedback
}

// Only a person's words made after the recorded merge are read, and each owes one
// correction however often it is read.
func TestACommentAfterAMembersMergeOwesOneCorrection(t *testing.T) {
	t.Parallel()
	reconciler, epics, _ := mergedFixture()
	for round := 0; round < 2; round++ {
		settled, err := reconciler.TakeUpMergedFeedback(context.Background())
		if err != nil || len(settled) != 1 || settled[0].Number != 7 || settled[0].Failure != "" {
			t.Fatalf("round %d: settled = %#v, %v", round, settled, err)
		}
		if want := 1 - round; settled[0].Owed != want {
			t.Fatalf("round %d: %d newly owed, want %d", round, settled[0].Owed, want)
		}
	}
	if len(epics.owed) != 1 || epics.owed[0].Item.Body != "The flag still has the old name." || epics.owed[0].PullRequest != 7 || epics.owed[0].Member.WorkItemID != "yoyodyne-ep.1" {
		t.Fatalf("owed = %#v, want only the comment made after the merge", epics.owed)
	}
}

// A pull request closed unmerged, one whose merge commit the harness did not
// record, and a member done with no pull request, have nothing read.
func TestOnlyAPullRequestWhoseMergeWasRecordedIsRead(t *testing.T) {
	t.Parallel()
	closed := mergedRun("yoyodyne-ep.1", 7)
	closed.PullRequest.Merged, closed.PullRequest.State, closed.PersonMerge = false, "CLOSED", nil
	unrecorded := mergedRun("yoyodyne-ep.1", 7)
	unrecorded.PullRequest.MergeCommit, unrecorded.PersonMerge = "", nil
	noRequest := mergedRun("yoyodyne-ep.1", 7)
	noRequest.PullRequest, noRequest.PersonMerge = nil, nil
	for name, run := range map[string]runstate.State{"closed unmerged": closed, "no merge commit": unrecorded, "no pull request": noRequest} {
		reconciler, epics, feedback := mergedFixture()
		reconciler.Store = listedRuns{runs: map[string][]runstate.State{"yoyodyne-ep.1": {run}}}
		settled, err := reconciler.TakeUpMergedFeedback(context.Background())
		if err != nil || len(settled) != 0 || feedback.reads != 0 || len(epics.owed) != 0 {
			t.Errorf("%s: settled %#v, %v, %d reads; want nothing read", name, settled, err, feedback.reads)
		}
	}
}

// With no open naming nothing is read, and once the epic is delivered a comment
// owes nothing here.
func TestNoMergedPullRequestIsReadWithoutAnOpenNamingOrOnceDelivered(t *testing.T) {
	t.Parallel()
	reconciler, epics, feedback := mergedFixture()
	epics.members = nil
	if settled, err := reconciler.TakeUpMergedFeedback(context.Background()); err != nil || len(settled) != 0 || feedback.reads != 0 {
		t.Fatalf("no naming: settled %#v, %v, %d reads", settled, err, feedback.reads)
	}
	reconciler.EpicMerged = nil
	if settled, err := reconciler.TakeUpMergedFeedback(context.Background()); err != nil || len(settled) != 0 || feedback.reads != 0 {
		t.Fatalf("nothing wired: settled %#v, %v, %d reads", settled, err, feedback.reads)
	}
	reconciler, epics, _ = mergedFixture()
	epics.members[0].Delivered = true
	settled, err := reconciler.TakeUpMergedFeedback(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Owed != 0 || len(epics.owed) != 0 {
		t.Fatalf("delivered: settled %#v, %v, owed %#v; want it read and left", settled, err, epics.owed)
	}
}

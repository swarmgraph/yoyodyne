package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/epicjoin"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// judgedComments answers each change request as it is told to, and keeps what it
// was asked.
type judgedComments struct {
	verdict epicjoin.Verdict
	err     error
	asked   []epicjoin.Comment
}

func (j *judgedComments) JudgeComment(_ context.Context, comment epicjoin.Comment) (epicjoin.Verdict, error) {
	j.asked = append(j.asked, comment)
	return j.verdict, j.err
}

// changesAsked is a person-merge fixture whose pull request carries one change
// request beside the comment and approval the fixture has.
func changesAsked(t *testing.T) (personMergeRepairFixture, Reconciler, *judgedComments) {
	t.Helper()
	f := newPersonMergeRepairFixture(t)
	number := f.run.PullRequest.Number
	reading := f.feedback.readings[number]
	reading.Reviews = append(reading.Reviews, publish.FeedbackReview{ID: 61, Author: "teammate", State: publish.ReviewChangesRequested, Body: "Please rename the flag.", At: reading.Comments[0].At})
	f.feedback.readings[number] = reading
	judge := &judgedComments{verdict: epicjoin.Repair}
	reconciler := f.reconciler(t)
	reconciler.Feedback, reconciler.Reports, reconciler.EpicComments = f.feedback, f.reports, judge
	reconciler.Capacity, reconciler.RepairLimit = 1, 3
	return f, reconciler, judge
}

func TestAChangeRequestJudgedInsideIsRepairedOnTheSamePullRequest(t *testing.T) {
	t.Parallel()
	f, reconciler, judge := changesAsked(t)
	settled, err := reconciler.TakeUpFeedback(context.Background())
	if err != nil || len(settled) != 1 || !settled[0].RepairStarted || settled[0].Listed != 1 {
		t.Fatalf("settled = %#v, %v; want the round started and the comment listed", settled, err)
	}
	if len(judge.asked) != 1 || judge.asked[0].Member != f.run.WorkItemID || judge.asked[0].PullRequest != f.run.PullRequest.Number || judge.asked[0].Body != "Please rename the flag." {
		t.Fatalf("asked = %#v, want the one change request judged, and not the comment", judge.asked)
	}
	live := f.load(t)
	if live.ForgeFeedback == nil || live.ForgeFeedback.PullRequest != f.run.PullRequest.Number || len(live.ForgeFeedback.Items) != 1 {
		t.Fatalf("the round answers %#v, want the change request on the same pull request", live.ForgeFeedback)
	}
	// The same comment read again starts no second repair.
	if settled, err := reconciler.TakeUpFeedback(context.Background()); err != nil || len(settled) != 0 {
		t.Fatalf("settled again = %#v, %v; want nothing waiting", settled, err)
	}
}

func TestAChangeRequestJudgedOutsideStartsNoRepairAndIsReadPast(t *testing.T) {
	t.Parallel()
	f, reconciler, judge := changesAsked(t)
	judge.verdict = epicjoin.AskedAboutScope
	before := f.load(t)
	settled, err := reconciler.TakeUpFeedback(context.Background())
	if err != nil || len(settled) != 1 || settled[0].RepairStarted || settled[0].RepairStopped || settled[0].Asked != 1 || !strings.Contains(settled[0].Detail, "question about scope") {
		t.Fatalf("settled = %#v, %v; want no repair and one question", settled, err)
	}
	after := f.load(t)
	if after.Status != before.Status || after.RepairAttempts != before.RepairAttempts || after.ForgeFeedback != nil || !after.WaitingOnAPersonsMerge() {
		t.Fatalf("run = %s with %d attempts, round %#v; want it waiting as it was", after.Status, after.RepairAttempts, after.ForgeFeedback)
	}
	if settled, err := reconciler.TakeUpFeedback(context.Background()); err != nil || len(settled) != 1 || settled[0].New != 0 || len(judge.asked) != 1 {
		t.Fatalf("settled again = %#v, %v after %d judgments; want nothing new", settled, err, len(judge.asked))
	}
}

func TestAChangeRequestWhoseJudgmentIsNotSettledLeavesEverythingForTheNextCheck(t *testing.T) {
	t.Parallel()
	f, reconciler, judge := changesAsked(t)
	judge.verdict, judge.err = epicjoin.Later, errors.New("the judgment of the comment may not start: that naming has ended")
	settled, err := reconciler.TakeUpFeedback(context.Background())
	if err != nil || len(settled) != 1 || settled[0].RepairStarted || settled[0].Listed != 0 || !strings.Contains(settled[0].Detail, "that naming has ended") {
		t.Fatalf("settled = %#v, %v; want everything left", settled, err)
	}
	if after := f.load(t); after.PullRequest.Feedback != nil || len(f.reports.reported) != 0 {
		t.Fatalf("feedback read = %#v, reports %#v; want nothing read past or listed", after.PullRequest.Feedback, f.reports.reported)
	}
	judge.verdict, judge.err = epicjoin.Repair, nil
	if settled, err := reconciler.TakeUpFeedback(context.Background()); err != nil || len(settled) != 1 || !settled[0].RepairStarted {
		t.Fatalf("settled = %#v, %v; want the round once it is judged", settled, err)
	}
}

// What the existing repair does not act on is left as it is: an ordinary comment
// goes on the development manager's list unjudged, and a pull request that is not
// waiting for a person's merge, such as a merge queue candidate's, is not read.
func TestWhatTheRepairDoesNotActOnIsNotJudged(t *testing.T) {
	t.Parallel()
	f := newPersonMergeRepairFixture(t)
	judge := &judgedComments{verdict: epicjoin.AskedAboutScope}
	reconciler := f.reconciler(t)
	reconciler.Feedback, reconciler.Reports, reconciler.EpicComments = f.feedback, f.reports, judge
	settled, err := reconciler.TakeUpFeedback(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Listed != 1 || settled[0].Asked != 0 || len(judge.asked) != 0 {
		t.Fatalf("settled = %#v, %v, judged %d; want the comment listed and nothing judged", settled, err, len(judge.asked))
	}

	queued, reconciler, judge := changesAsked(t)
	reconciler.Store = queuedCandidates{queued.store}
	if settled, err := reconciler.TakeUpFeedback(context.Background()); err != nil || len(settled) != 0 || len(judge.asked) != 0 {
		t.Fatalf("settled = %#v, %v, judged %d; want a merge queue candidate's pull request left unread", settled, err, len(judge.asked))
	}
}

// queuedCandidates lists each run waiting for a person's merge as a merge queue
// candidate's run instead, whose pull request the harness merges itself.
type queuedCandidates struct{ *runstate.Store }

func (q queuedCandidates) WaitingForPersonMerge() ([]runstate.State, error) {
	listed, err := q.Store.WaitingForPersonMerge()
	for at := range listed {
		published := *listed[at].PullRequest
		published.MergeQueued = true
		listed[at].PullRequest, listed[at].PersonMerge, listed[at].StopClass = &published, nil, ""
	}
	return listed, err
}

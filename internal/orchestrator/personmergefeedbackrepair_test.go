package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive the development manager's repair, from feedback, of an item whose
// change waits on a pull request the harness opened for a person to merge
// (yoyodyne-ifd.429.251). Each case is a whole run under a merge setting that
// leaves the merge to a person; a person's comment reaches her list, and her
// decision starts a new run that updates the same pull request.

type personMergeRepairFixture struct {
	personMergeUpdateFixture
	repairing *atomic.Bool
	feedback  *fakeTeamFeedback
	reports   *collectedReports
	budget    *fakeFeedbackBudget
	starts    *[]feedbackRepairStart
	repairer  TeamFeedbackRepairer
}

const (
	firstWrite    = "implemented\n"
	repairedWrite = "implemented, and repaired\n"
)

func newPersonMergeRepairFixture(t *testing.T) personMergeRepairFixture {
	t.Helper()
	var repairing atomic.Bool
	f := newPersonMergeUpdateFixtureWriting(t, func(request backend.RunRequest) error {
		content := firstWrite
		if repairing.Load() {
			content = repairedWrite
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(content), 0o600)
	})
	number := f.run.PullRequest.Number
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	feedback := &fakeTeamFeedback{readings: map[int]publish.FeedbackReading{number: {
		State: "OPEN",
		Comments: []publish.FeedbackComment{
			{ID: 41, Author: "teammate", Body: "Should this say repaired?", At: later},
		},
		Reviews: []publish.FeedbackReview{{ID: 51, Author: "teammate", State: publish.ReviewApproved, At: later}},
	}}}
	budget := &fakeFeedbackBudget{}
	var starts []feedbackRepairStart
	fixture := personMergeRepairFixture{personMergeUpdateFixture: f, repairing: &repairing, feedback: feedback, reports: &collectedReports{}, budget: budget, starts: &starts}
	fixture.repairer = TeamFeedbackRepairer{
		Runs: f.store, Items: f.tracker, Feedback: feedback, Budget: budget, Intake: noIntakeHold{}, Gates: noGates{}, Capacity: 1,
		Start: func(ctx context.Context, item string, selection runstate.Selection) (Outcome, error) {
			starts = append(starts, feedbackRepairStart{item, selection})
			pipeline := f.pipeline
			pipeline.NewRunID = runstate.NewRunID
			pipeline.Selection = selection
			return pipeline.Run(ctx, item)
		},
	}
	return fixture
}

func (f personMergeRepairFixture) request(feedback string) TeamFeedbackRepairRequest {
	return TeamFeedbackRepairRequest{PullRequest: f.run.PullRequest.Number, FeedbackID: feedback, WorkItemID: f.run.WorkItemID,
		Reason: "the comment is a fair point about this change", DecidedBy: "Development Manager", Conversation: "chat-dm", Turn: 9}
}

func TestACommentOnAPullRequestAPersonIsToMergeReachesTheListNamingTheDecision(t *testing.T) {
	t.Parallel()
	f := newPersonMergeRepairFixture(t)
	reconciler := f.reconciler(t)
	reconciler.Feedback, reconciler.Reports = f.feedback, f.reports
	settled, err := reconciler.TakeUpFeedback(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Listed != 1 || settled[0].Failure != "" {
		t.Fatalf("TakeUpFeedback() = %#v, %v; want the comment listed", settled, err)
	}
	if len(f.reports.reported) != 1 {
		t.Fatalf("reports = %#v, want one", f.reports.reported)
	}
	message := f.reports.reported[0].Message
	for _, want := range []string{"repair-from-feedback", "comment-41", "pull_request 1", "updates this same pull request"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message = %q, want it to say %q so the decision can be recorded from it", message, want)
		}
	}
}

func TestRepairFromFeedbackOnAPullRequestAPersonIsToMergeUpdatesThatPullRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPersonMergeRepairFixture(t)
	number := f.run.PullRequest.Number
	oldHead := f.run.PullRequest.HeadCommit

	decided, err := f.repairer.Decide(ctx, f.request("comment-41"))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decided.RunID != f.run.RunID || len(f.budget.spent) != 1 || f.budget.spent[0] != f.run.WorkItemID+"@"+f.run.RunID {
		t.Fatalf("decided = %#v, spent = %v; want the decision on the run that published and one attempt spent", decided, f.budget.spent)
	}
	if len(*f.starts) != 0 {
		t.Fatalf("starts = %v after deciding, want none until the harness carries it out", *f.starts)
	}
	if owed := f.load(t).PersonMerge.RepairsOwed(); len(owed) != 1 || owed[0].Feedback.Body != "Should this say repaired?" {
		t.Fatalf("owed = %#v, want the decision kept with the feedback as evidence", owed)
	}

	f.repairing.Store(true)
	carried, err := f.repairer.CarryOut(ctx)
	if err != nil || len(carried) != 1 || !carried[0].Started || carried[0].Failure != "" {
		t.Fatalf("CarryOut() = %#v, %v; want one new run started", carried, err)
	}
	selection := (*f.starts)[0].selection
	if selection.By != runstate.SelectedByDevelopmentManager || !strings.Contains(selection.Reason, "comment-41") || !strings.Contains(selection.Reason, "conversation chat-dm") {
		t.Fatalf("selection = %#v, want the decision and the entry it answers named", selection)
	}
	if selection.Lift == nil || selection.Lift.RunID != f.run.RunID || selection.Lift.Branch != f.run.Branch || selection.Lift.FallBackToTarget {
		t.Fatalf("lift = %#v, want a start from the change the earlier run kept on its branch", selection.Lift)
	}
	if update := selection.UpdatePullRequest; update == nil || update.Number != number || update.Branch != f.run.PullRequest.Branch || update.HeadCommit != oldHead {
		t.Fatalf("update = %#v, want the same pull request, branch and head", update)
	}

	fresh, err := f.store.Load(carried[0].NewRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !fresh.WaitingForPersonMerge() || fresh.PullRequest == nil || fresh.PullRequest.Number != number || fresh.PullRequest.Branch != f.run.PullRequest.Branch {
		t.Fatalf("new run = %#v / %#v, want it waiting for a person on the same pull request and branch", fresh.PersonMerge, fresh.PullRequest)
	}
	if fresh.PullRequest.HeadCommit == oldHead {
		t.Fatalf("head = %s, want the pull request's head replaced by the repaired change", oldHead)
	}
	if opened := f.forge.OpenedRequests(); len(opened) != 1 {
		t.Fatalf("opened = %d pull requests, want the one the first run opened and no other", len(opened))
	}
	head, err := f.forge.Git("rev-parse", "refs/heads/"+f.run.PullRequest.Branch)
	if err != nil || head != fresh.PullRequest.HeadCommit {
		t.Fatalf("remote branch = %q, %v; want it at the new run's head %s", head, err, fresh.PullRequest.HeadCommit)
	}
	if content, err := f.forge.Git("show", head+":feature.txt"); err != nil || content != strings.TrimSpace(repairedWrite) {
		t.Fatalf("feature.txt on the pull request = %q, %v; want the repaired change", content, err)
	}
	if reviewed := f.reviews(); reviewed < 2 {
		t.Fatalf("reviews = %d, want the repaired change reviewed again", reviewed)
	}

	earlier := f.load(t)
	if earlier.WaitingForPersonMerge() || earlier.PersonMerge.SupersededBy != fresh.RunID {
		t.Fatalf("earlier run = %#v, want its wait ended in favour of run %s", earlier.PersonMerge, fresh.RunID)
	}
	if repair, _ := earlier.PersonMerge.Repair(number, "comment-41"); repair.StartedRunID != fresh.RunID {
		t.Fatalf("repair = %#v, want the new run recorded on the decision", repair)
	}
	if again, err := f.repairer.CarryOut(ctx); err != nil || len(again) != 0 || len(*f.starts) != 1 {
		t.Fatalf("second CarryOut() = %#v, %v with %d starts; want the decision carried out once", again, err, len(*f.starts))
	}
}

func TestRepairFromFeedbackOnAHarnessPullRequestIsRefusedWithItsReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("an entry is answered once", func(t *testing.T) {
		t.Parallel()
		f := newPersonMergeRepairFixture(t)
		if _, err := f.repairer.Decide(ctx, f.request("comment-41")); err != nil {
			t.Fatalf("first Decide() error = %v", err)
		}
		_, err := f.repairer.Decide(ctx, f.request("comment-41"))
		if err == nil || !strings.Contains(err.Error(), "already answered") || len(f.budget.spent) != 1 {
			t.Fatalf("second Decide() = %v with %v spent, want it refused as already answered and nothing more spent", err, f.budget.spent)
		}
	})
	t.Run("an item that has closed", func(t *testing.T) {
		t.Parallel()
		f := newPersonMergeRepairFixture(t)
		if _, err := f.store.Update(ctx, f.run.RunID, runstate.PersonMergeMerged{Number: f.run.PullRequest.Number, ForgeState: "MERGED", At: time.Now().UTC()}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		f.tracker.Item.Status = "closed"
		_, err := f.repairer.Decide(ctx, f.request("comment-41"))
		if err == nil || !strings.Contains(err.Error(), "has closed") || len(f.budget.spent) != 0 {
			t.Fatalf("Decide() = %v with %v spent, want it refused because the item has closed, spending nothing", err, f.budget.spent)
		}
	})
	t.Run("an item that is not waiting on a merge", func(t *testing.T) {
		t.Parallel()
		f := newPersonMergeRepairFixture(t)
		request := f.request("comment-41")
		request.WorkItemID = "yoyodyne-elsewhere"
		_, err := f.repairer.Decide(ctx, request)
		if err == nil || !strings.Contains(err.Error(), "not waiting for a merge of pull request #1") || len(f.budget.spent) != 0 {
			t.Fatalf("Decide() = %v, want it refused for an item not waiting on this pull request", err)
		}
	})
	t.Run("an item with no attempts left", func(t *testing.T) {
		t.Parallel()
		f := newPersonMergeRepairFixture(t)
		f.budget.refuses = errors.New("re-run budget is spent")
		_, err := f.repairer.Decide(ctx, f.request("comment-41"))
		if err == nil || !strings.Contains(err.Error(), "no attempt left") {
			t.Fatalf("Decide() = %v, want it refused for want of an attempt", err)
		}
		if owed := f.load(t).PersonMerge.RepairsOwed(); len(owed) != 0 {
			t.Fatalf("owed = %#v, want no decision recorded", owed)
		}
	})
	t.Run("an approval", func(t *testing.T) {
		t.Parallel()
		f := newPersonMergeRepairFixture(t)
		if _, err := f.repairer.Decide(ctx, f.request("review-51")); err == nil || !strings.Contains(err.Error(), "approval") {
			t.Fatalf("Decide() on an approval = %v, want it refused", err)
		}
	})
}

func TestWhileARepairIsDecidedTheWaitingPullRequestIsNotBroughtUpToDateUnderIt(t *testing.T) {
	t.Parallel()
	f := newPersonMergeRepairFixture(t)
	ctx := context.Background()
	run := f.load(t)
	if _, err := f.store.Update(ctx, run.RunID, runstate.TeamFeedbackRepairDecided{Repair: runstate.TeamFeedbackRepair{
		PullRequest: run.PullRequest.Number, FeedbackID: "comment-41", WorkItemID: run.WorkItemID, Reason: "fair", DecidedBy: "Development Manager",
		Conversation: "chat-dm", Turn: 1, DecidedAt: time.Now().UTC(),
		Feedback: runstate.ForgeFeedbackItem{ID: "comment-41", Kind: runstate.FeedbackComment, Author: "teammate", At: time.Now().UTC(), Body: "x"},
	}}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := f.store.Update(ctx, run.RunID, runstate.TeamFeedbackRepairTaken{PullRequest: run.PullRequest.Number, FeedbackID: "comment-41", At: time.Now().UTC()}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	settled, updates := f.check(t)
	if settled.Kept == "" || !strings.Contains(settled.Kept, "decided from feedback") || len(updates) != 0 {
		t.Fatalf("check = %#v, %v; want the pull request left alone while the repair runs", settled, updates)
	}
	if got := f.load(t); !got.WaitingForPersonMerge() || len(got.IntegrationResumptions) != 0 {
		t.Fatalf("run = %#v, want it left waiting and not put back", got.PersonMerge)
	}
	reconciler := f.reconciler(t)
	reconciler.Feedback, reconciler.Reports = f.feedback, f.reports
	if fed, err := reconciler.TakeUpFeedback(ctx); err != nil || len(fed) != 0 {
		t.Fatalf("TakeUpFeedback() = %#v, %v; want the run's feedback left to the repair answering it", fed, err)
	}
}

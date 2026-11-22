package publicationtests

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive yoyodyne-ifd.429.257: the development manager's decision to repair
// an item whose change waits on a pull request the harness opened, from a
// comment on that request. The new run starts from the change the request's
// branch carries and updates the same request.

type noHold struct{}

func (noHold) Held() (runstate.IntakeHold, bool, error) { return runstate.IntakeHold{}, false, nil }

type noGates struct{}

func (noGates) DischargedGates() (map[string][]string, error) { return nil, nil }

type attemptSpend struct {
	spent   []string
	refuses error
}

func (a *attemptSpend) RecordFeedbackRepair(_ context.Context, item, runID string, _ time.Time) (runstate.TriageCounters, error) {
	if a.refuses != nil {
		return runstate.TriageCounters{}, a.refuses
	}
	a.spent = append(a.spent, item+"@"+runID)
	return runstate.TriageCounters{}, nil
}

func (f *feedbackFixture) repairer(reader orchestrator.ReconcileFeedback, spend *attemptSpend, started *[]runstate.Selection) orchestrator.TeamFeedbackRepairer {
	return orchestrator.TeamFeedbackRepairer{
		Runs: f.store, Items: f.tracker, Feedback: reader, Budget: spend, Intake: noHold{}, Gates: noGates{}, Capacity: 2,
		Start: func(ctx context.Context, item string, selection runstate.Selection) (orchestrator.Outcome, error) {
			*started = append(*started, selection)
			pipeline := f.pipeline
			pipeline.NewRunID = func() (string, error) { return "run-" + strings.Repeat("b", 32), nil }
			pipeline.Selection = selection
			return pipeline.Run(ctx, item)
		},
	}
}

func repairRequest(item, feedback string) orchestrator.TeamFeedbackRepairRequest {
	return orchestrator.TeamFeedbackRepairRequest{PullRequest: 1, FeedbackID: feedback, WorkItemID: item, Reason: "the question points at a real gap",
		DecidedBy: "Development Manager", Conversation: "chat-dm", Turn: 3}
}

func TestACommentOnAHarnessPullRequestIsRepairedOntoTheSameRequest(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	before := fixture.run
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Comments: []publish.FeedbackComment{{ID: 31, Author: "linus", Body: "Why not rename the flag?", At: fixture.after(2)}},
	}}
	reports := &collectedReports{}
	if settled := fixture.check(t, reader, reports, 1, 3); settled.Listed != 1 {
		t.Fatalf("settlement = %#v, want the comment listed", settled)
	}
	if len(reports.reports) != 1 || !strings.Contains(reports.reports[0].Message, "repair-from-feedback") || !strings.Contains(reports.reports[0].Message, "comment-31") {
		t.Fatalf("reports = %#v, want the entry to name the decision and the feedback identifier", reports.reports)
	}

	spend := &attemptSpend{}
	var started []runstate.Selection
	repairer := fixture.repairer(reader, spend, &started)
	decided, err := repairer.Decide(context.Background(), repairRequest("yoyodyne-task", "comment-31"))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decided.RunID != before.RunID || !decided.OnPersonsPullRequest || len(spend.spent) != 1 || len(started) != 0 {
		t.Fatalf("decided = %#v, spent = %v, started = %d; want the decision on the publishing run, one attempt spent, nothing started yet", decided, spend.spent, len(started))
	}

	// An entry is answered once, and a refused decision spends nothing.
	if _, err := repairer.Decide(context.Background(), repairRequest("yoyodyne-task", "comment-31")); err == nil || !strings.Contains(err.Error(), "already answered") {
		t.Fatalf("second Decide() error = %v, want it refused as already answered", err)
	}
	if len(spend.spent) != 1 {
		t.Fatalf("spent = %v, want the refusal to spend nothing", spend.spent)
	}

	carried, err := repairer.CarryOut(context.Background())
	if err != nil || len(carried) != 1 || !carried[0].Started || carried[0].Failure != "" {
		t.Fatalf("CarryOut() = %#v, %v, want one new run started", carried, err)
	}
	selection := started[0]
	if selection.Lift == nil || selection.Lift.Branch != before.Branch || selection.Continues == nil ||
		selection.Continues.Number != before.PullRequest.Number || selection.Continues.Branch != before.PullRequest.Branch || selection.Continues.HeadCommit != before.PullRequest.HeadCommit {
		t.Fatalf("selection = %#v, want a start from the kept change and the request it updates", selection)
	}
	if !strings.Contains(selection.Reason, "comment-31") {
		t.Fatalf("selection reason = %q, want it to name the entry", selection.Reason)
	}

	repaired := testharness.LoadRun(t, fixture.store, carried[0].NewRunID)
	if !repaired.WaitingOnAPersonsMerge() || repaired.PullRequest.Number != before.PullRequest.Number || repaired.PullRequest.Branch != before.PullRequest.Branch ||
		repaired.PullRequest.HeadCommit == before.PullRequest.HeadCommit {
		t.Fatalf("new run = %s %s pull request %#v, want the same request and branch at a new head, waiting for a person", repaired.Status, repaired.Phase, repaired.PullRequest)
	}
	if opened := fixture.forge.OpenedRequests(); opened[len(opened)-1].Head != before.PullRequest.Branch {
		t.Fatalf("last request = %#v, want the kept branch updated rather than another opened", opened[len(opened)-1])
	}
	old := testharness.LoadRun(t, fixture.store, before.RunID)
	if old.PersonMerge.ContinuedBy != repaired.RunID || old.WaitingForPersonMerge() || !repaired.WaitingForPersonMerge() {
		t.Fatalf("old wait = %#v, want it handed to the new run so one request has one waiting run", old.PersonMerge)
	}
	if again, err := repairer.CarryOut(context.Background()); err != nil || len(started) != 1 {
		t.Fatalf("second CarryOut() = %#v, %v with %d starts, want nothing started twice", again, err, len(started))
	}
}

func TestARepairOfAHarnessPullRequestIsRefusedInPlainWords(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Comments: []publish.FeedbackComment{{ID: 31, Author: "linus", Body: "Why?", At: fixture.after(2)}},
		Reviews:  []publish.FeedbackReview{{ID: 32, Author: "ada", State: publish.ReviewApproved, At: fixture.after(3)}},
	}}
	spend := &attemptSpend{}
	var started []runstate.Selection
	repairer := fixture.repairer(reader, spend, &started)
	ctx := context.Background()

	for name, test := range map[string]struct {
		request orchestrator.TeamFeedbackRepairRequest
		want    string
	}{
		"another item":     {repairRequest("yoyodyne-other", "comment-31"), "not waiting for a merge"},
		"another request":  {orchestrator.TeamFeedbackRepairRequest{PullRequest: 9, FeedbackID: "comment-31", WorkItemID: "yoyodyne-task", Reason: "r", DecidedBy: "Development Manager", Conversation: "c"}, "not waiting for a merge"},
		"unknown feedback": {repairRequest("yoyodyne-task", "comment-99"), "was not found"},
		"an approval":      {repairRequest("yoyodyne-task", "review-32"), "approval"},
	} {
		if _, err := repairer.Decide(ctx, test.request); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: Decide() error = %v, want it to say %q", name, err, test.want)
		}
	}

	// An item with no attempt left is refused, and nothing is recorded.
	spend.refuses = errors.New("the item has used every attempt")
	if _, err := repairer.Decide(ctx, repairRequest("yoyodyne-task", "comment-31")); err == nil || !strings.Contains(err.Error(), "no attempt left") {
		t.Errorf("Decide() error = %v, want it to say no attempt is left", err)
	}
	if run := testharness.LoadRun(t, fixture.store, fixture.run.RunID); len(run.PersonMerge.Repairs) != 0 {
		t.Errorf("repairs = %#v, want none recorded", run.PersonMerge.Repairs)
	}

	// Once the wait has ended, the item is not waiting on a merge.
	if _, err := fixture.store.Update(ctx, fixture.run.RunID, runstate.PersonMergeContinued{PullRequest: 1, RunID: "run-" + strings.Repeat("a", 32), At: time.Now().UTC()}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	spend.refuses = nil
	if _, err := repairer.Decide(ctx, repairRequest("yoyodyne-task", "comment-31")); err == nil || !strings.Contains(err.Error(), "not waiting for a merge") {
		t.Errorf("Decide() after the wait ended error = %v, want it refused as not waiting", err)
	}
	if len(spend.spent) != 0 || len(started) != 0 {
		t.Errorf("spent = %v, started = %d, want a refusal to spend and start nothing", spend.spent, len(started))
	}
}

// A kept branch holding a commit the harness did not write is not overwritten:
// the repaired change is kept, the branch is left as it is, and the development
// manager's list gets one entry naming the branch and the commit found there.
func TestARepairIsNotPushedOverACommitAPersonAddedToTheKeptBranch(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	before := fixture.run
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Comments: []publish.FeedbackComment{{ID: 31, Author: "linus", Body: "Why not rename the flag?", At: fixture.after(2)}},
	}}
	reports := &collectedReports{}
	fixture.pipeline.Reports = reports
	spend := &attemptSpend{}
	var started []runstate.Selection
	repairer := fixture.repairer(reader, spend, &started)
	if _, err := repairer.Decide(context.Background(), repairRequest("yoyodyne-task", "comment-31")); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	// A person pushes a commit onto the branch the request carries.
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", before.WorktreePath, "-c", "user.name=Person", "-c", "user.email=person@example.invalid"}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	if err := os.WriteFile(filepath.Join(before.WorktreePath, "person.txt"), []byte("by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "person.txt")
	git("commit", "-m", "a person's commit")
	theirs := git("rev-parse", "HEAD")
	git("push", "origin", "HEAD:refs/heads/"+before.PullRequest.Branch)

	carried, err := repairer.CarryOut(context.Background())
	if err != nil || len(carried) != 1 {
		t.Fatalf("CarryOut() = %#v, %v, want the decision carried out", carried, err)
	}
	if found := git("ls-remote", "origin", "refs/heads/"+before.PullRequest.Branch); !strings.HasPrefix(found, theirs) {
		t.Fatalf("remote branch = %q, want it left at the person's commit %s", found, theirs)
	}
	if len(reports.reports) != 1 || reports.reports[0].For != domain.RoleDevelopmentManager ||
		!strings.Contains(reports.reports[0].Message, before.PullRequest.Branch) || !strings.Contains(reports.reports[0].Message, theirs) {
		t.Fatalf("reports = %#v, want one entry for the development manager naming the branch and the commit found", reports.reports)
	}
	old := testharness.LoadRun(t, fixture.store, before.RunID)
	if old.PersonMerge.ContinuedBy != "" {
		t.Fatalf("old wait = %#v, want it kept because nothing was published", old.PersonMerge)
	}
}

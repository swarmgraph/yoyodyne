package publicationtests

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// personMergeFixture is a run under a merge setting that leaves the merge to a
// person, driven to the end: its approved change is published and the run is
// over, waiting on its pull request.
type personMergeFixture struct {
	repository   string
	remote       string
	worktreeRoot string
	store        *runstate.Store
	tracker      *orchestratortest.Tracker
	run          runstate.State
}

func newPersonMergeFixture(t *testing.T) personMergeFixture {
	t.Helper()
	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	remote := testharness.AddBareRemote(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline := testharness.Publishing(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider), &orchestratortest.Forge{Remote: remote})
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration != nil || outcome.WorkItemClosed || outcome.PullRequest == nil {
		t.Fatalf("outcome = %#v, want the change published and nothing promoted", outcome)
	}
	run := testharness.LoadRun(t, store, outcome.RunID)
	return personMergeFixture{repository: repository, remote: remote, worktreeRoot: worktreeRoot, store: store, tracker: tracker, run: run}
}

func (f personMergeFixture) check(t *testing.T, forge orchestrator.ReconcilePullRequests, closers orchestrator.ReconcileClosers) []orchestrator.PersonMergeSettlement {
	t.Helper()
	settled, err := orchestrator.Reconciler{
		Tracker:   f.tracker,
		Worktrees: testharness.NewObserver(t, f.repository, f.worktreeRoot),
		Store:     f.store,
		Publisher: forge,
		Closers:   closers,
	}.SettlePersonMerges(context.Background())
	if err != nil {
		t.Fatalf("SettlePersonMerges() error = %v", err)
	}
	return settled
}

type closers struct {
	closer publish.Closer
	asked  int
}

func (c *closers) Closer(_ context.Context, number int) (publish.Closer, error) {
	c.asked++
	closer := c.closer
	closer.Number = number
	return closer, nil
}

// A run whose change a person merges ends once it has published: it holds no
// developer slot, its item keeps its claim, and the item's notes and the status
// line both say what it is waiting for.
func TestAPublishedChangeAPersonMergesWaitsWithoutADeveloperSlot(t *testing.T) {
	t.Parallel()
	fixture := newPersonMergeFixture(t)
	run := fixture.run

	if run.PersonMerge == nil || run.PersonMerge.Number != run.PullRequest.Number || !run.WaitingForPersonMerge() {
		t.Fatalf("run = %#v, want it waiting for a person to merge its pull request", run.PersonMerge)
	}
	if !run.Status.Terminal() || run.HoldsDeveloperSlot() {
		t.Fatalf("run status = %s, holds a slot = %t; want it over and holding no developer slot", run.Status, run.HoldsDeveloperSlot())
	}
	if fixture.tracker.Closed || fixture.tracker.Reopened || !fixture.tracker.Claimed {
		t.Fatalf("tracker = %v, want the item still claimed", fixture.tracker.Calls)
	}
	want := "waiting for a person to merge pull request #" + strconv.Itoa(run.PullRequest.Number)
	if !strings.Contains(fixture.tracker.Notes, want) {
		t.Fatalf("item notes = %q, want them to say %q", fixture.tracker.Notes, want)
	}
}

// A pull request still open changes nothing: the item stays claimed and the run
// goes on waiting, however many checks read it.
func TestAPullRequestStillOpenLeavesTheWaitAsItStands(t *testing.T) {
	t.Parallel()
	fixture := newPersonMergeFixture(t)
	calls := len(fixture.tracker.Calls)
	forge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{Number: fixture.run.PullRequest.Number, URL: fixture.run.PullRequest.URL, State: "OPEN"}}
	for range 2 {
		settled := fixture.check(t, forge, &closers{})
		if len(settled) != 1 || settled[0].Found != orchestrator.PersonMergeOpen || settled[0].Settled || settled[0].Failure != "" {
			t.Fatalf("check = %#v, want the request found open and nothing settled", settled)
		}
	}
	if len(fixture.tracker.Calls) != calls {
		t.Fatalf("the check made %v on the work item", fixture.tracker.Calls[calls:])
	}
	after := testharness.LoadRun(t, fixture.store, fixture.run.RunID)
	if !after.WaitingForPersonMerge() || !after.UpdatedAt.Equal(fixture.run.UpdatedAt) {
		t.Fatalf("run = %#v, want it still waiting and its record untouched", after.PersonMerge)
	}
}

// A merge a person made is followed locally by fast-forward, the item closes
// with the merge commit named, and the run is recorded as landed. A second check
// finds nothing left to do.
func TestAPullRequestAPersonMergedClosesTheItemAndFollowsTheMerge(t *testing.T) {
	t.Parallel()
	fixture := newPersonMergeFixture(t)
	run := fixture.run
	// The person merges by fast-forward on the forge, so the remote target is
	// the change's own head.
	testharness.RunPipelineGit(t, fixture.repository, "push", "origin", run.PullRequest.HeadCommit+":refs/heads/main")
	forge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{
		Number: run.PullRequest.Number, URL: run.PullRequest.URL, State: "MERGED", Merged: true, MergeCommit: run.PullRequest.HeadCommit,
	}}

	settled := fixture.check(t, forge, &closers{})
	if len(settled) != 1 || !settled[0].Settled || settled[0].Found != orchestrator.PersonMergeMerged || settled[0].Failure != "" {
		t.Fatalf("check = %#v, want the merge settled", settled)
	}
	if settled[0].Catchup == nil || !settled[0].Catchup.Advanced {
		t.Fatalf("catch-up = %#v, want local main brought onto the merge", settled[0].Catchup)
	}
	if local := testharness.GitLine(t, fixture.repository, "rev-parse", "main"); local != run.PullRequest.HeadCommit {
		t.Fatalf("local main = %s, want the merge %s", local, run.PullRequest.HeadCommit)
	}
	if !fixture.tracker.Closed || !strings.Contains(fixture.tracker.CloseReason, run.PullRequest.HeadCommit) {
		t.Fatalf("closed = %t, reason = %q; want the item closed naming the merge commit", fixture.tracker.Closed, fixture.tracker.CloseReason)
	}
	after := testharness.LoadRun(t, fixture.store, run.RunID)
	if after.WaitingForPersonMerge() || after.PersonMerge == nil || !after.PersonMerge.Merged() || after.PersonMerge.MergeCommit != run.PullRequest.HeadCommit {
		t.Fatalf("wait = %#v, want it ended on the merge, with the merge commit", after.PersonMerge)
	}
	if !after.PullRequest.Merged || after.StopClass == runstate.StopIntegrationPolicy {
		t.Fatalf("run = %#v / %s, want the request recorded merged and the run no longer ended by the merge setting", after.PullRequest, after.StopClass)
	}

	calls := len(fixture.tracker.Calls)
	if again := fixture.check(t, forge, &closers{}); len(again) != 0 {
		t.Fatalf("second check = %#v, want nothing left waiting", again)
	}
	if len(fixture.tracker.Calls) != calls {
		t.Fatalf("the second check made %v on the work item", fixture.tracker.Calls[calls:])
	}
}

// A pull request closed without merging sends the item back to the Lead
// Product Manager, parked with who closed it and when, and keeps the branch and
// the run's checkout.
func TestAPullRequestClosedUnmergedGoesBackToTheLeadProductManagerWithTheWorkKept(t *testing.T) {
	t.Parallel()
	fixture := newPersonMergeFixture(t)
	run := fixture.run
	forge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{Number: run.PullRequest.Number, URL: run.PullRequest.URL, State: "CLOSED"}}
	closedAt := time.Date(2026, 10, 30, 2, 15, 0, 0, time.UTC)
	who := &closers{closer: publish.Closer{By: "octo-reviewer", At: closedAt}}

	settled := fixture.check(t, forge, who)
	if len(settled) != 1 || !settled[0].Settled || settled[0].Found != orchestrator.PersonMergeClosed || settled[0].Failure != "" {
		t.Fatalf("check = %#v, want the closing settled", settled)
	}
	if !fixture.tracker.Reopened || fixture.tracker.Closed {
		t.Fatalf("tracker = %v, want the item given back, not closed", fixture.tracker.Calls)
	}
	when := closedAt.Local().Format("2006-01-02 15:04 MST")
	for _, want := range []string{"closed without being merged, by octo-reviewer at " + when, "Lead Product Manager", run.Branch + " and the run's checkout", "nothing was deleted"} {
		if !strings.Contains(fixture.tracker.ReopenReason, want) {
			t.Errorf("note = %q, want it to say %q", fixture.tracker.ReopenReason, want)
		}
	}
	if parking := fixture.tracker.Item.Parking; !parking.Parked() || !strings.Contains(parking.Reason(), "octo-reviewer") {
		t.Fatalf("parking = %q, want the item parked naming who closed the request", parking)
	}
	after := testharness.LoadRun(t, fixture.store, run.RunID)
	if after.WaitingForPersonMerge() || after.PersonMerge == nil || !after.PersonMerge.Returned() || after.PersonMerge.ClosedBy != "octo-reviewer" {
		t.Fatalf("wait = %#v, want it ended on the closing, naming who closed it", after.PersonMerge)
	}
	if testharness.PublishedCommit(t, fixture.remote, run.Branch) != run.PullRequest.HeadCommit {
		t.Fatalf("the published branch %s is gone, want it kept", run.Branch)
	}
	if _, err := os.Stat(run.WorktreePath); err != nil {
		t.Fatalf("the run's checkout %s: %v, want it kept", run.WorktreePath, err)
	}

	if again := fixture.check(t, forge, who); len(again) != 0 || who.asked != 1 {
		t.Fatalf("second check = %#v (asked %d), want nothing left waiting", again, who.asked)
	}
}

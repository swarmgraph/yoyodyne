package landingtests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive a run end to end against a scratch remote that refuses direct
// pushes to main, which is what a repository requiring a pull request is. The
// rule they hold is yoyodyne-ifd.429.5's: on a protected target a run never
// moves the primary checkout's main ahead of the forge. The change lands through
// its pull request, and main moves only by a fast-forward onto what the remote
// has — so no ending of the run can leave local main ahead of origin, which is
// what stranded commits there on 2026-09-20 and 2026-09-24.

func TestAProtectedTargetLandsThroughItsPullRequestAndOnlyFastForwardsMain(t *testing.T) {
	t.Parallel()

	run := testharness.NewProtectedRun(t, &orchestratortest.Forge{TargetProtection: publish.BranchProtection{Protected: true, By: "ruleset"}})
	outcome, err := run.Pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(run.Forge.ProtectionAsked) == 0 || run.Forge.ProtectionAsked[0] != "main" {
		t.Fatalf("protection asked about %v, want the target branch", run.Forge.ProtectionAsked)
	}
	// The forge was asked to merge while local main still stood at the base: the
	// promotion wrote nothing to it first.
	if run.AtMerge != outcome.BaseCommit {
		t.Fatalf("local main was %q when the forge was asked to merge, want the untouched base %q", run.AtMerge, outcome.BaseCommit)
	}
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	if outcome.Integration.TargetCommit != outcome.Integration.SourceCommit {
		t.Errorf("landed commit = %q, want the reviewed commit %q", outcome.Integration.TargetCommit, outcome.Integration.SourceCommit)
	}
	if outcome.Status != runstate.StatusSucceeded || !outcome.WorkItemClosed || outcome.PublishFailure != "" {
		t.Fatalf("outcome = %#v, want a clean landing that closed the item", outcome)
	}
	// The reviewed commit reached origin by the forge's merge, and main followed
	// it there by a fast-forward onto the forge's merge commit.
	remoteTarget := testharness.PublishedCommit(t, run.Remote, "main")
	testharness.AssertRemoteCarriesPromotion(t, run.Repository, run.Remote, "main", outcome.Integration.SourceCommit)
	if local := testharness.PublishedCommit(t, run.Repository, "main"); local != remoteTarget {
		t.Errorf("local main = %q, want it fast-forwarded onto the forge's merge %q", local, remoteTarget)
	}
	if outcome.Catchup == nil || !outcome.Catchup.Advanced {
		t.Errorf("catch-up = %#v, want main advanced onto the forge", outcome.Catchup)
	}
	if !outcome.WorktreeRemoved || !outcome.BranchRemoved {
		t.Errorf("worktree removed = %t, branch removed = %t; want the landed run cleaned up", outcome.WorktreeRemoved, outcome.BranchRemoved)
	}
	state, err := run.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Integration == nil || !state.Integration.ThroughPullRequest {
		t.Errorf("durable integration = %#v, want the landing recorded as through the pull request", state.Integration)
	}
	if !strings.Contains(run.Tracker.Notes, "main is protected on the forge by ruleset") {
		t.Errorf("tracker notes do not say which path the protected target took:\n%s", run.Tracker.Notes)
	}
	run.AssertMainNotAhead(t)
}

// A forge that could not be asked is taken as protecting the branch, and the
// run says so rather than guessing the other way.
func TestAnUnaskableForgeTakesTheProtectedPath(t *testing.T) {
	t.Parallel()

	run := testharness.NewProtectedRun(t, &orchestratortest.Forge{ProtectionErr: errors.New("gh: Resource not accessible by integration (HTTP 403)")})
	outcome, err := run.Pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if run.AtMerge != outcome.BaseCommit {
		t.Fatalf("local main was %q when the forge was asked to merge, want the untouched base %q", run.AtMerge, outcome.BaseCommit)
	}
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	for _, want := range []string{"could not be asked of the forge", "HTTP 403", "taken as protected"} {
		if !strings.Contains(outcome.TargetProtection, want) {
			t.Errorf("target protection %q does not say %q", outcome.TargetProtection, want)
		}
	}
	run.AssertMainNotAhead(t)
}

// The case that stranded commits on main: the forge refuses the merge. Nothing
// was promoted locally, so nothing is stranded; the change is on its pull
// request, and the item is handed to a person rather than closed as integrated.
func TestARefusedMergeOnAProtectedTargetLeavesMainWhereTheForgeHasIt(t *testing.T) {
	t.Parallel()

	run := testharness.NewProtectedRun(t, &orchestratortest.Forge{
		TargetProtection: publish.BranchProtection{Protected: true, By: "branch protection"},
		MergeErr: publish.MergeRefused{
			Number: 1,
			Method: publish.MergeCommit,
			Status: "BLOCKED",
			Reason: "At least 1 approving review is required by reviewers with write access",
		},
	})
	outcome, err := run.Pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped on the unlanded change", outcome)
	}
	if outcome.WorkItemClosed || run.Tracker.Closed {
		t.Fatalf("an unmerged change closed the item as integrated: %q", run.Tracker.CloseReason)
	}
	if !run.Tracker.Blocked {
		t.Fatal("the unlanded change was not handed to a person")
	}
	for _, want := range []string{"did not merge it", "was not moved", "approving review is required", "triage rearm"} {
		if !strings.Contains(run.Tracker.BlockReason, want) {
			t.Errorf("blocker does not say %q:\n%s", want, run.Tracker.BlockReason)
		}
	}
	if local := testharness.PublishedCommit(t, run.Repository, "main"); local != outcome.BaseCommit {
		t.Errorf("local main = %q, want the base %q it stood at before the run", local, outcome.BaseCommit)
	}
	if remote := testharness.PublishedCommit(t, run.Remote, "main"); remote != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", remote, outcome.BaseCommit)
	}
	// What a re-arm needs is on the record: the promotion it pins, the request,
	// and the moment the merge was dropped.
	state, err := run.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !state.Status.Terminal() || state.Blocker == "" {
		t.Errorf("run status = %q with blocker %q, want a stopped, blocked run", state.Status, state.Blocker)
	}
	if state.Integration == nil || !state.Integration.ThroughPullRequest || state.MergeDrop == nil || state.PullRequest == nil {
		t.Errorf("record = integration %#v, merge drop %#v, pull request %#v; want what a re-arm repeats", state.Integration, state.MergeDrop, state.PullRequest)
	}
	if published := testharness.PublishedCommit(t, run.Remote, outcome.Branch); published == "" {
		t.Error("the branch carrying the unlanded change was removed from the remote")
	}
	run.AssertMainNotAhead(t)
}

// A merge the forge queues is on no branch yet, so the run finishes without
// touching main, leaves its artifacts for the proof that has not arrived, and
// reconciliation does the rest once the forge merges: main fast-forwarded onto
// the forge, the item closed, and the run cleaned up.
func TestAQueuedMergeOnAProtectedTargetIsLandedByReconciliation(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	testharness.ProtectBranch(t, fixture.Remote, "main")
	fixture.Forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "branch protection"})
	outcome := fixture.Run(t)

	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != outcome.BaseCommit {
		t.Fatalf("local main = %q after a queued merge, want the base %q", local, outcome.BaseCommit)
	}
	if outcome.WorkItemClosed || fixture.Tracker.Record().Closed {
		t.Fatal("a queued merge closed the item before the forge merged it")
	}
	if outcome.CleanupFailure != "" || outcome.WorktreeRemoved {
		t.Errorf("cleanup failure = %q, worktree removed = %t; want the artifacts left for reconciliation without a failure", outcome.CleanupFailure, outcome.WorktreeRemoved)
	}
	held, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !held.Outstanding() {
		t.Fatal("the queued landing is not outstanding, so nothing would ever settle it")
	}

	fixture.Forge.PerformQueuedMerge(t)
	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the queued landing settled", results)
	}
	remoteTarget := testharness.PublishedCommit(t, fixture.Remote, "main")
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != remoteTarget {
		t.Errorf("local main = %q, want it fast-forwarded onto the forge's merge %q", local, remoteTarget)
	}
	testharness.AssertRemoteCarriesPromotion(t, fixture.Repository, fixture.Remote, "main", outcome.Integration.SourceCommit)
	if !fixture.Tracker.Record().Closed {
		t.Error("the confirmed landing did not close the item")
	}
	settled, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.WorktreeRemoved || !settled.BranchRemoved || settled.Outstanding() {
		t.Errorf("settled run = worktree removed %t, branch removed %t, outstanding %t; want it cleaned up and done",
			settled.WorktreeRemoved, settled.BranchRemoved, settled.Outstanding())
	}
}

// An unprotected target keeps the arrangement it had: the change is promoted
// onto local main first and the pull request merged after it.
func TestAnUnprotectedTargetIsStillPromotedLocallyFirst(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	var atMerge string
	forge := &orchestratortest.Forge{Remote: remote}
	forge.OnMerge = func() { atMerge = testharness.PublishedCommit(t, repository, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a local promotion", outcome.Integration)
	}
	if atMerge != outcome.Integration.SourceCommit {
		t.Errorf("local main was %q when the forge was asked to merge, want the promoted commit %q", atMerge, outcome.Integration.SourceCommit)
	}
	if !strings.Contains(outcome.TargetProtection, "is not protected") {
		t.Errorf("target protection = %q, want it to say the target is not protected", outcome.TargetProtection)
	}
}

// A remote that moves after the landing was prepared and before the forge is
// asked to merge is the race a promotion can lose, not a divergence: nothing was
// promoted, so main is fast-forwarded onto the remote, the change is replayed
// onto it with the gate re-earned, and the replayed change lands.
func TestAProtectedTargetThatMovesBeforeTheMergeIsReplayedOnto(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first merge is dropped and the remote target moves during the wait
	// that follows, so the re-read in front of the retried merge finds it moved.
	forge := &orchestratortest.Forge{Remote: remote, MergeResets: 1, TargetProtection: publish.BranchProtection{Protected: true, By: "ruleset"}}
	forge.AfterMergeReset = func() { testharness.DriftRemoteTarget(t, remote, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline = testharness.Waiting(pipeline, &testharness.PausingClock{Instant: testharness.BaseTime}, 6*time.Hour, 6*time.Hour)

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.IntegrationRetries != 1 || len(forge.Merges) != 1 {
		t.Fatalf("integration retries = %d, merges = %d; want one replay and one merge of the replayed change", outcome.IntegrationRetries, len(forge.Merges))
	}
	if outcome.PublishFailure != "" || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want the replayed change landed cleanly", outcome)
	}
	remoteTarget := testharness.PublishedCommit(t, remote, "main")
	if local := testharness.PublishedCommit(t, repository, "main"); local != remoteTarget {
		t.Errorf("local main = %q, want it fast-forwarded onto the forge's merge %q", local, remoteTarget)
	}
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.SourceCommit)
	for _, file := range []string{"feature.txt", "elsewhere.txt"} {
		if _, err := os.Stat(filepath.Join(repository, file)); err != nil {
			t.Errorf("the primary checkout does not carry %s after the landing: %v", file, err)
		}
	}
}

// A process killed between preparing a landing and hearing what the forge did
// with it leaves a record whose local target was never moved, so the sweep has
// to settle it on the forge's answer. A local promotion killed at the same
// point is settled as succeeded, because its local target already carries the
// change; doing that here would close the item over a change that may be on an
// open pull request and no target branch at all.
func TestAnInterruptedLandingIsSettledOnTheForgesAnswer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// answer is what the forge does with the request after the process died.
		answer func(t *testing.T, forge testharness.QueuedForge, landed string)
	}{
		{name: "left open", answer: func(*testing.T, testharness.QueuedForge, string) {}},
		{name: "merged", answer: func(t *testing.T, forge testharness.QueuedForge, landed string) {
			if err := forge.MergeByHand("main", landed); err != nil {
				t.Fatalf("merge the request: %v", err)
			}
		}},
		{name: "queued", answer: func(_ *testing.T, forge testharness.QueuedForge, _ string) { forge.HoldQueuedMerge() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := testharness.NewQueuedFixture(t)
			testharness.ProtectBranch(t, fixture.Remote, "main")
			forge := fixture.Forge
			forge.SetQueueMerge(false)
			forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "branch protection"})
			// The forge declines, so the run goes on past the merge request and stops;
			// what a killed process leaves is the record as it stood when the forge
			// was asked, which is taken here and put back afterwards.
			forge.SetMergeErr(publish.MergeRefused{Number: 1, Method: publish.MergeCommit, Status: "BLOCKED", Reason: "held"})
			var killed runstate.State
			forge.SetOnMerge(func() {
				recorded, err := fixture.Store.Load(testharness.PipelineRunID)
				if err != nil {
					t.Fatalf("Load() at the merge request error = %v", err)
				}
				killed = recorded
			})
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, testharness.ApproveVerdict)
			pipeline := testharness.Publishing(testharness.Automatic(testharness.NewSharedPipeline(t, fixture.Repository, fixture.WorktreeRoot, fixture.Store, fixture.Tracker, provider, []string{"exit 0"}), provider), forge)
			outcome, _ := pipeline.Run(context.Background(), fixture.Tracker.Record().Item.ID)
			if killed.Integration == nil || !killed.Integration.ThroughPullRequest || killed.Status.Terminal() {
				t.Fatalf("record at the merge request = status %q, integration %#v; want a live landing on the record", killed.Status, killed.Integration)
			}
			// The process dies there: the record is what it had written, and the item
			// is still claimed by it.
			if err := fixture.Store.Save(killed); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			fixture.Tracker.ForgetSettlement()
			fixture.Tracker.SetItemStatus("in_progress")
			forge.SetMergeErr(nil)
			landed := killed.Integration.SourceCommit
			tc.answer(t, forge, landed)

			results := fixture.Reconcile(t)
			if len(results) != 1 || results[0].Failure != "" {
				t.Fatalf("reconciliation = %#v, want the interrupted landing settled", results)
			}
			settled, err := fixture.Store.Load(testharness.PipelineRunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			local := testharness.PublishedCommit(t, fixture.Repository, "main")
			switch tc.name {
			case "left open":
				if results[0].Action != orchestrator.ActionBlocked || fixture.Tracker.Record().Closed || !fixture.Tracker.Record().Blocked {
					t.Fatalf("action = %q, closed = %t, blocked = %t; want the unlanded change handed to a person", results[0].Action, fixture.Tracker.Record().Closed, fixture.Tracker.Record().Blocked)
				}
				if local != outcome.BaseCommit {
					t.Errorf("local main = %q, want the base %q it never left", local, outcome.BaseCommit)
				}
				if settled.Integration == nil || !settled.Status.Terminal() || settled.Outstanding() {
					t.Errorf("settled = integration %#v, status %q, outstanding %t; want a stopped run keeping what a re-arm repeats", settled.Integration, settled.Status, settled.Outstanding())
				}
				if again := fixture.Reconcile(t); len(again) != 0 {
					t.Errorf("second reconciliation = %#v, want nothing owed by a run handed to a person", again)
				}
			case "merged":
				if results[0].Action != orchestrator.ActionCompleted || !fixture.Tracker.Record().Closed {
					t.Fatalf("action = %q, closed = %t; want the merged landing completed", results[0].Action, fixture.Tracker.Record().Closed)
				}
				if remote := testharness.PublishedCommit(t, fixture.Remote, "main"); local != remote {
					t.Errorf("local main = %q, want it caught up onto the forge's merge %q", local, remote)
				}
				if !settled.WorktreeRemoved || !settled.BranchRemoved {
					t.Errorf("worktree removed = %t, branch removed = %t; want the landed run cleaned up", settled.WorktreeRemoved, settled.BranchRemoved)
				}
			case "queued":
				if results[0].Action != orchestrator.ActionQueued || fixture.Tracker.Record().Closed {
					t.Fatalf("action = %q, closed = %t; want the queued landing left waiting", results[0].Action, fixture.Tracker.Record().Closed)
				}
				if local != outcome.BaseCommit {
					t.Errorf("local main = %q, want the base %q until the forge merges", local, outcome.BaseCommit)
				}
				if settled.PullRequest == nil || !settled.PullRequest.MergeQueued || !settled.Status.Terminal() {
					t.Fatalf("settled = %#v, want a finished run waiting on its queued merge", settled)
				}
				// The forge merges, and the ordinary queued-merge settlement lands it.
				if err := forge.MergeByHand("main", landed); err != nil {
					t.Fatalf("merge the request: %v", err)
				}
				if again := fixture.Reconcile(t); len(again) != 1 || again[0].Action != orchestrator.ActionCompleted || !fixture.Tracker.Record().Closed {
					t.Fatalf("second reconciliation = %#v, closed = %t; want the queued landing completed", again, fixture.Tracker.Record().Closed)
				}
				if remote, local := testharness.PublishedCommit(t, fixture.Remote, "main"), testharness.PublishedCommit(t, fixture.Repository, "main"); local != remote {
					t.Errorf("local main = %q, want it caught up onto the forge's merge %q", local, remote)
				}
			}
			(&testharness.ProtectedRun{Repository: fixture.Repository, Remote: fixture.Remote}).AssertMainNotAhead(t)
		})
	}
}

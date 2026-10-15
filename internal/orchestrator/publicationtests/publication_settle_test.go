package publicationtests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The same shape met as it happens rather than as residue: the forge performs
// the queued merge, something else lands on the remote before the sweep asks,
// and the settlement confirms the merge by containment and records this
// request's own merge commit. Nothing is left outstanding, so nothing is left
// for the finishing sweep to do afterwards.
func TestASettledMergeIsConfirmedWhenOthersLandedAfterIt(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)
	merge := testharness.PublishedCommit(t, fixture.Remote, "main")
	tip := testharness.LandAnotherChange(t, fixture.Remote)

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the merge settled as completed", results)
	}
	settled := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if settled.PublishFailure != "" {
		t.Errorf("publish failure = %q, want a merge that landed among others confirmed", settled.PublishFailure)
	}
	if settled.PullRequest.MergeCommit != merge {
		t.Errorf("recorded merge commit = %q, want this request's merge %q rather than the tip %q", settled.PullRequest.MergeCommit, merge, tip)
	}
	if !fixture.Tracker.Record().Closed {
		t.Error("the confirmed merge did not close the item")
	}
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != tip {
		t.Errorf("local main = %q, want it caught up to the remote's %q", local, tip)
	}
	if published := testharness.PublishedCommit(t, fixture.Remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	if left, err := fixture.Reconciler(t).FinishPublications(context.Background()); err != nil || len(left) != 0 {
		t.Fatalf("FinishPublications() = %#v, %v; want nothing left unfinished", left, err)
	}
}

// A merge the forge dropped puts the item in a person's hands with a blocker. When
// that person merges the request on the forge by hand, the refresh records the
// merge and this finishes it: the blocker goes, the item closes on its own
// landing, the docket entries for both the stoppage and the publication close,
// and the count and the hold clear with them. Before this, the hand-merge was
// recorded and nothing followed from it.
func TestReconcileFinishesADroppedMergeSomebodyMadeByHand(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	fixture.Forge.DropQueuedMerge()
	docket := &testharness.MemoryDocket{}
	docketer := testharness.DocketerOverStore(docket, fixture.Store, config.Config{
		Execution: config.Execution{IntegrationRetriesBeforeReconciliation: 1},
		Triage:    testharness.DocketedTriage,
	})
	reconciler := fixture.Reconciler(t)
	reconciler.Docket = docketer
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("reconciliation = %#v, %v; want the dropped merge handed to a person", results, err)
	}
	dropped := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if dropped.Blocker == "" || dropped.MergeDrop == nil || !fixture.Tracker.Record().Blocked || fixture.Tracker.Record().Closed {
		t.Fatalf("dropped = blocker %q, drop %#v, item blocked %t closed %t; want the item handed back", dropped.Blocker, dropped.MergeDrop, fixture.Tracker.Record().Blocked, fixture.Tracker.Record().Closed)
	}
	if built, err := docketer.Build(); err != nil || len(built.Entries) != 1 || len(built.Entries[0].Earlier) != 1 {
		t.Fatalf("docket = %#v, %v; want the stoppage and the publication both docketed, as one live entry for the run", built, err)
	}
	if !dropped.AwaitingForge() || !testharness.HeldItemsOf(t, fixture.Store, fixture.Tracker.Record().Item.ID)[fixture.Tracker.Record().Item.ID] {
		t.Fatal("a dropped merge is neither counted as awaiting the forge nor holding its item")
	}

	// A person merges the request on the forge.
	if err := fixture.Forge.MergeByHand(fixture.Forge.OpenedRequests()[0].Base, outcome.PullRequest.HeadCommit); err != nil {
		t.Fatalf("merge by hand: %v", err)
	}

	refreshed, err := reconciler.RefreshPublications(context.Background())
	if err != nil || len(refreshed) != 1 || !refreshed[0].Updated || !refreshed[0].Merged {
		t.Fatalf("refresh = %#v, %v; want the hand-made merge recorded", refreshed, err)
	}
	settlements, err := reconciler.FinishPublications(context.Background())
	if err != nil {
		t.Fatalf("FinishPublications() error = %v", err)
	}
	if len(settlements) != 1 || !settlements[0].Settled || settlements[0].Failure != "" || settlements[0].Remaining != "" {
		t.Fatalf("settlements = %#v, want the hand-made merge settled", settlements)
	}

	after := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if after.PublishFailure != "" || after.Blocker != "" || after.PullRequest.MergeCommit == "" {
		t.Fatalf("record = publish failure %q, blocker %q, merge commit %q; want the publication finished and the blocker cleared", after.PublishFailure, after.Blocker, after.PullRequest.MergeCommit)
	}
	if after.MergeDrop == nil {
		t.Error("the moment the drop was found out was erased; it is history and stays")
	}
	if after.AwaitingForge() || after.Outcome() != runstate.OutcomeSucceeded {
		t.Errorf("record awaiting the forge = %t, outcome = %s; want neither awaiting nor stopped", after.AwaitingForge(), after.Outcome())
	}
	// The item closes on its own landing, in the words the settle path uses.
	if !fixture.Tracker.Record().Closed || !strings.Contains(fixture.Tracker.Record().CloseReason, "merged by the forge") {
		t.Errorf("item closed = %t with reason %q, want the hand-made merge to settle it", fixture.Tracker.Record().Closed, fixture.Tracker.Record().CloseReason)
	}
	if held := testharness.HeldItemsOf(t, fixture.Store, fixture.Tracker.Record().Item.ID); held[fixture.Tracker.Record().Item.ID] {
		t.Errorf("holds = %v, want the hold lifted", held)
	}
	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() after the settlement error = %v", err)
	}
	if len(rebuilt.Entries) != 0 || rebuilt.Closed != 2 {
		t.Errorf("docket after the settlement = %#v, want both entries closed", rebuilt)
	}
	if local, remote := testharness.PublishedCommit(t, fixture.Repository, "main"), testharness.PublishedCommit(t, fixture.Remote, "main"); local != remote {
		t.Errorf("local main = %q, want it caught up to the remote's %q", local, remote)
	}
	if published := testharness.PublishedCommit(t, fixture.Remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
}

// A publication the remote still refuses stays outstanding, and the sweep
// writes nothing at all: the record keeps the account the run wrote, which is
// the line on the item, the item gets no note, and the hold stands. What the
// remote says now is reported by the sweep rather than written anywhere.
func TestReconcileLeavesAPublicationTheRemoteStillRefuses(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	fixture.Forge.SetReplayMerge(true)
	fixture.Run(t)
	// The forge replays the promotion rather than merging it, so the promoted
	// commit is on no branch of the remote and containment honestly fails.
	fixture.Forge.PerformQueuedMerge(t)
	if settled := fixture.Reconcile(t); len(settled) != 1 || settled[0].Action != orchestrator.ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the merge settled with its publication outstanding", settled)
	}
	before := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if before.PublishFailure == "" || !before.PullRequest.Merged {
		t.Fatalf("record = %#v, want a merged publication nothing could confirm", before)
	}
	notes := len(fixture.Tracker.Record().NoteRecords)

	reconciler := fixture.Reconciler(t)
	settlements, err := reconciler.FinishPublications(context.Background())
	if err != nil {
		t.Fatalf("FinishPublications() error = %v", err)
	}
	if len(settlements) != 1 || settlements[0].Settled || settlements[0].Failure != "" {
		t.Fatalf("settlements = %#v, want the publication left outstanding without failing the sweep", settlements)
	}
	if !strings.Contains(settlements[0].Remaining, "does not contain the promoted commit") {
		t.Errorf("remaining = %q, want the remote's refusal", settlements[0].Remaining)
	}
	// The record keeps the run's own account, which is the `Publication
	// outstanding` line on the item; what the remote says now is the sweep's.
	after := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if after.PublishFailure != before.PublishFailure {
		t.Errorf("publish failure = %q, want the run's own account %q kept", after.PublishFailure, before.PublishFailure)
	}
	if after.PullRequest.MergeCommit != "" {
		t.Errorf("record = %#v, want nothing confirmed", after.PullRequest)
	}
	if len(fixture.Tracker.Record().NoteRecords) != notes+1 || settlements[0].Finding == nil {
		t.Errorf("notes = %q, want one settlement finding", fixture.Tracker.Record().NoteRecords[notes:])
	}
	if held := testharness.HeldItemsOf(t, fixture.Store, fixture.Tracker.Record().Item.ID); !held[fixture.Tracker.Record().Item.ID] {
		t.Errorf("holds = %v, want the item still held by its outstanding publication", held)
	}

	// The same answer again writes nothing.
	again, err := reconciler.FinishPublications(context.Background())
	if err != nil || len(again) != 1 || again[0].Settled {
		t.Fatalf("second FinishPublications() = %#v, %v; want the same publication still outstanding", again, err)
	}
	if len(fixture.Tracker.Record().NoteRecords) != notes+1 {
		t.Error("the same refusal was announced again")
	}
	if repeated := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID); !repeated.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("a record the remote still refuses was rewritten at %s, was %s", repeated.UpdatedAt, before.UpdatedAt)
	}
}

// The third outstanding shape: a confirmed merge whose consumed branch could not
// be deleted. The next sweep tries the branch again and, once it goes, settles
// the publication — and while it does not go, writes nothing on the item, because
// a note per sweep about the same dead branch is the nagging that gets an item's
// notes ignored.
func TestReconcileFinishesAPublicationOnceItsLeftoverBranchIsGone(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	dropped := &droppingRemote{deletions: 1_000}
	fixture.Worktrees = func(observer orchestrator.ReconcileWorktrees) orchestrator.ReconcileWorktrees {
		dropped.ReconcileWorktrees = observer
		return dropped
	}
	fixture.Sleep = func(context.Context, time.Duration) error { return nil }
	fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)
	if settled := fixture.Reconcile(t); len(settled) != 1 || settled[0].Action != orchestrator.ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the merge settled with its branch left behind", settled)
	}
	leftover := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if !strings.Contains(leftover.PublishFailure, "delete the merged remote branch") || leftover.PullRequest.MergeCommit == "" {
		t.Fatalf("record = %#v, want a confirmed merge with a leftover branch", leftover)
	}
	notes := len(fixture.Tracker.Record().NoteRecords)

	// The connection still drops: nothing is written, and the publication stands.
	still := fixture.Reconciler(t)
	settlements, err := still.FinishPublications(context.Background())
	if err != nil || len(settlements) != 1 || settlements[0].Settled || settlements[0].Failure != "" {
		t.Fatalf("FinishPublications() over a dropping connection = %#v, %v; want the leftover left standing", settlements, err)
	}
	if len(fixture.Tracker.Record().NoteRecords) != notes {
		t.Errorf("the sweep wrote %d note(s) about a branch it still could not delete", len(fixture.Tracker.Record().NoteRecords)-notes)
	}

	// The connection comes back: the branch goes, and the publication settles.
	fixture.Worktrees = nil
	working := fixture.Reconciler(t)
	settlements, err = working.FinishPublications(context.Background())
	if err != nil || len(settlements) != 1 || !settlements[0].Settled {
		t.Fatalf("FinishPublications() = %#v, %v; want the leftover branch deleted and the publication settled", settlements, err)
	}
	after := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if after.PublishFailure != "" {
		t.Errorf("publish failure = %q, want none", after.PublishFailure)
	}
	if published := testharness.PublishedCommit(t, fixture.Remote, leftover.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	if len(fixture.Tracker.Record().NoteRecords) != notes+1 || !strings.Contains(fixture.Tracker.Record().NoteRecords[notes], "Previously outstanding, as the line above it reads: \"Publication outstanding: delete the merged remote branch") {
		t.Errorf("tracker notes after the settlement = %q, want one note naming the leftover it replaces", fixture.Tracker.Record().NoteRecords[notes:])
	}
}

// A branch moved after the forge merged its request. Containment, rather than
// the recorded publication hash alone, decides whether it leaves anything owed.
func TestReconcileSettlesAMovedMergedBranchOnlyWhenItsTipIsInTheTarget(t *testing.T) {
	t.Parallel()
	for _, contained := range []bool{true, false} {
		t.Run(map[bool]string{true: "tip in target", false: "tip outside target"}[contained], func(t *testing.T) {
			t.Parallel()
			fixture := testharness.NewQueuedFixture(t)
			outcome := fixture.Run(t)
			fixture.Forge.PerformQueuedMerge(t)
			tip := testharness.LandAnotherChange(t, fixture.Remote)
			if !contained {
				// Make a commit not reachable from main using the published tip
				// as its parent, without moving any target.
				tree := testharness.GitLine(t, fixture.Repository, "rev-parse", outcome.Integration.SourceCommit+"^{tree}")
				tip = testharness.GitLine(t, fixture.Repository, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", tree, "-p", outcome.Integration.SourceCommit, "-m", "unmerged branch work")
				testharness.RunPipelineGit(t, fixture.Repository, "push", "origin", tip+":refs/heads/"+outcome.Branch)
			} else {
				testharness.RunPipelineGit(t, fixture.Remote, "update-ref", "refs/heads/"+outcome.Branch, tip)
			}
			results := fixture.Reconcile(t)
			if len(results) != 1 || results[0].Failure != "" || results[0].Action != orchestrator.ActionCompleted {
				t.Fatalf("Reconcile() = %#v, want the merge settled without a pass failure", results)
			}
			state := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
			if contained {
				if state.PublishFailure != "" || state.ReconcileFindings != nil || results[0].Finding != nil {
					t.Fatalf("state = %#v, want no outstanding publication or finding", state)
				}
				if settlements, err := fixture.Reconciler(t).FinishPublications(context.Background()); err != nil || len(settlements) != 0 {
					t.Fatalf("FinishPublications() = %#v, %v, want nothing outstanding", settlements, err)
				}
			} else {
				if !strings.Contains(state.PublishFailure, "want the published commit") || state.ReconcileFindings == nil || results[0].Finding == nil {
					t.Fatalf("state = %#v, result = %#v, want the moved tip refusal recorded", state, results[0])
				}
				finding := results[0].Finding
				if finding.Mover != readmodel.MoverDevelopmentManager || !strings.Contains(finding.Whose(), "preserve any branch work outside the target") {
					t.Fatalf("finding = %#v, next move = %q", finding, finding.Whose())
				}
				notes := len(fixture.Tracker.Record().NoteRecords)
				for pass := 0; pass < 2; pass++ {
					settlements, err := fixture.Reconciler(t).FinishPublications(context.Background())
					if err != nil || len(settlements) != 1 || settlements[0].Finding == nil || settlements[0].Settled {
						t.Fatalf("FinishPublications() = %#v, %v", settlements, err)
					}
				}
				if len(fixture.Tracker.Record().NoteRecords) != notes {
					t.Error("the repeated refusal wrote another finding note")
				}
			}
			if testharness.PublishedCommit(t, fixture.Remote, outcome.Branch) != tip {
				t.Error("the moved remote branch was deleted or changed")
			}
		})
	}
}

// Both requests need their merged branch settled. One refuses deletion, and
// the other must still lose its branch and outstanding publication on this pass.
func TestReconcileFinishesOtherPublicationsBesideARefusedDeletion(t *testing.T) {
	t.Parallel()
	fixture := testharness.NewQueuedFixture(t)
	fixture.Worktrees = func(observer orchestrator.ReconcileWorktrees) orchestrator.ReconcileWorktrees {
		return &testharness.RefusingBranch{ReconcileWorktrees: observer}
	}
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)
	fixture.Reconcile(t)
	first := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	other := first
	other.RunID = "run-ffffffffffffffffffffffffffffffff"
	other.WorkItemID = "yoyodyne-other"
	other.Branch = first.Branch + "-other"
	other.WorktreePath = first.WorktreePath + "-other"
	other.ReconcileFindings = nil
	published := *first.PullRequest
	published.Number = 2
	published.Branch = other.Branch
	other.PullRequest = &published
	if err := fixture.Store.Create(other); err != nil {
		t.Fatal(err)
	}
	testharness.RunPipelineGit(t, fixture.Remote, "update-ref", "refs/heads/"+other.Branch, outcome.Integration.SourceCommit)
	reconciler := fixture.Reconciler(t)
	reconciler.Publisher = orchestratortest.PublicationAnswers{first.Branch: {Number: 1, Merged: true}, other.Branch: {Number: 2, Merged: true}}
	settled, err := reconciler.FinishPublications(context.Background())
	if err != nil || len(settled) != 2 {
		t.Fatalf("FinishPublications() = %#v, %v", settled, err)
	}
	for _, result := range settled {
		if result.WorkItemID == first.WorkItemID {
			if result.Settled || result.Finding == nil || result.FindingProblem != "" {
				t.Fatalf("refusal = %#v", result)
			}
		} else if !result.Settled || result.Failure != "" || result.Finding != nil {
			t.Fatalf("other settlement = %#v", result)
		}
	}
	if state := testharness.LoadRun(t, fixture.Store, other.RunID); state.PublishFailure != "" {
		t.Fatalf("other publication still outstanding: %s", state.PublishFailure)
	}
	if testharness.PublishedCommit(t, fixture.Remote, other.Branch) != "" {
		t.Error("other branch was not removed")
	}
	if state := testharness.LoadRun(t, fixture.Store, first.RunID); state.ReconcileFindings == nil || state.ReconcileFindings[0].Pending {
		t.Fatal("refusal was not durably recorded and delivered")
	}
	notes := fixture.Tracker.Record().NoteRecords
	found := false
	for _, note := range notes {
		if strings.Contains(note, "Settlement finding:") && strings.Contains(note, first.WorkItemID) && strings.Contains(note, "Next move: the development manager's") && strings.Contains(note, "preserve any branch work") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes = %q, want the one item's refusal, remedy, and owner", notes)
	}
}

func TestReconcileSettlesAnOutstandingPublicationWhoseMovedTipReachedTheTarget(t *testing.T) {
	t.Parallel()
	fixture := testharness.NewQueuedFixture(t)
	fixture.Worktrees = func(observer orchestrator.ReconcileWorktrees) orchestrator.ReconcileWorktrees {
		return &testharness.RefusingBranch{ReconcileWorktrees: observer}
	}
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)
	fixture.Reconcile(t)
	before := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if before.PublishFailure == "" || len(before.ReconcileFindings) != 1 {
		t.Fatalf("before = %#v, want an outstanding branch and finding", before)
	}
	tip := testharness.LandAnotherChange(t, fixture.Remote)
	testharness.RunPipelineGit(t, fixture.Remote, "update-ref", "refs/heads/"+outcome.Branch, tip)
	fixture.Worktrees = nil
	results, err := fixture.Reconciler(t).FinishPublications(context.Background())
	if err != nil || len(results) != 1 || !results[0].Settled || results[0].Finding != nil || results[0].FindingProblem != "" {
		t.Fatalf("FinishPublications() = %#v, %v", results, err)
	}
	if after := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID); after.PublishFailure != "" || len(after.ReconcileFindings) != 0 {
		t.Fatalf("after = %#v, want nothing outstanding", after)
	}
	if testharness.PublishedCommit(t, fixture.Remote, outcome.Branch) != tip {
		t.Fatal("the moved branch was disturbed")
	}
}

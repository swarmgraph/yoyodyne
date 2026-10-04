package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The residue PR 497 left on 2026-09-13, replayed. Ten held merges landed in one
// sitting, so every promotion but the last sat under a merge commit that later
// merges had built on, and confirmation — which demanded the remote tip carry
// exactly the promotion's content — reported eight of them as carrying content
// the promotion did not, on every sweep, for good. Each was a closed item that
// read as an unconfirmed publication on its record, on the triage docket, and in
// the hold that kept it out of the pull.
//
// The sweep re-asks the remote with containment, finds the promotion there under
// its own merge commit, and finishes the publication: record, item, docket, and
// local target all agree that nothing is outstanding.
func TestReconcileFinishesAMergeThatLandedAmongOthers(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)
	if settled := fixture.reconcile(t); len(settled) != 1 || settled[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", settled)
	}
	confirmed := loadRun(t, fixture.store, pipelineRunID)
	merge := confirmed.PullRequest.MergeCommit
	if merge == "" || merge == outcome.Integration.TargetCommit {
		t.Fatalf("recorded merge commit = %q, want the forge's merge commit", merge)
	}
	// The record as the old confirmation left it: merged, unconfirmed, outstanding.
	residue := confirmed
	residue.PullRequest.MergeCommit = ""
	residue.PublishFailure = "confirm the queued merge reached main: remote integration target does not carry the promoted commit: main on origin is at " +
		merge + ", which carries content the promoted commit " + outcome.Integration.TargetCommit + " does not"
	if err := fixture.store.Save(residue); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// Another change lands on the remote after it, so the tip carries content this
	// promotion never had — the state the old check could never get past.
	tip := landAnotherChange(t, fixture.remote)
	completes := len(fixture.tracker.Record().Calls)

	docket := &memoryDocket{}
	docketer := docketerOverStore(docket, fixture.store, config.Config{
		Execution: config.Execution{IntegrationRetriesBeforeReconciliation: 1},
		Triage:    docketedTriage,
	})
	// Before the sweep: the docket has the publication, and the hold keeps the
	// item out of the pull. The heartbeat's count does not have it — the forge did
	// merge — which is the disagreement between surfaces this settles.
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 || built.Entries[0].Class != triage.ClassPublication {
		t.Fatalf("docket = %#v, want the unconfirmed publication docketed", built.Entries)
	}
	if held := heldItemsOf(t, fixture.store, fixture.tracker.Record().Item.ID); !held[fixture.tracker.Record().Item.ID] {
		t.Fatalf("holds = %v, want the item held out of the pull by its outstanding publication", held)
	}

	reconciler := fixture.reconciler(t)
	reconciler.Docket = docketer
	settlements, err := reconciler.FinishPublications(context.Background())
	if err != nil {
		t.Fatalf("FinishPublications() error = %v", err)
	}
	if len(settlements) != 1 || !settlements[0].Settled || settlements[0].Failure != "" || settlements[0].Remaining != "" {
		t.Fatalf("settlements = %#v, want the one residue settled", settlements)
	}
	settlement := settlements[0]
	if settlement.MergeCommit != merge {
		t.Errorf("settled merge commit = %q, want this request's merge commit %q rather than the tip %q", settlement.MergeCommit, merge, tip)
	}
	if !strings.Contains(settlement.Outstanding, "carries content the promoted commit") {
		t.Errorf("outstanding = %q, want what the record said before", settlement.Outstanding)
	}

	// The record: confirmed, nothing outstanding, and no longer awaiting the forge.
	after := loadRun(t, fixture.store, pipelineRunID)
	if after.PublishFailure != "" || after.PullRequest.MergeCommit != merge {
		t.Fatalf("record = publish failure %q, merge commit %q; want the publication finished", after.PublishFailure, after.PullRequest.MergeCommit)
	}
	if after.AwaitingForge() {
		t.Error("the settled publication still counts as awaiting the forge")
	}
	if after.Outcome() != runstate.OutcomeSucceeded {
		t.Errorf("outcome = %s, want succeeded", after.Outcome())
	}
	// The hold lifts, so the item is pullable again for whatever it is worth to a
	// closed item; the point is that no surface reads it as held.
	if held := heldItemsOf(t, fixture.store, fixture.tracker.Record().Item.ID); held[fixture.tracker.Record().Item.ID] {
		t.Errorf("holds = %v, want the item released once its publication settled", held)
	}
	// The item: told, and not closed a second time — its run closed it.
	if !strings.Contains(fixture.tracker.Record().Notes, "settled this item's publication") || !strings.Contains(fixture.tracker.Record().Notes, "Previously outstanding, as the line above it reads: \"Publication outstanding: confirm the queued merge") {
		t.Errorf("tracker notes do not report the settlement against the line it replaces:\n%s", fixture.tracker.Record().Notes)
	}
	for _, call := range fixture.tracker.Record().Calls[completes:] {
		if call != "record" {
			t.Errorf("the settlement made %q on an item its run already closed", call)
		}
	}
	// The docket: the entry closed as settled, and not re-derived.
	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() after the settlement error = %v", err)
	}
	if len(rebuilt.Entries) != 0 || rebuilt.Closed != 1 || rebuilt.Added != 0 {
		t.Errorf("docket after the settlement = %#v, want the publication entry closed and nothing re-docketed", rebuilt)
	}
	if closed := docket.closed[triage.PublicationKey(pipelineRunID, after.PullRequest.Number)]; closed.Decision != settledPublicationDecision {
		t.Errorf("docket closure = %#v, want the harness's settlement recorded on it", closed)
	}
	// The local target: caught up onto the remote, which now carries both merges.
	if local, remote := publishedCommit(t, fixture.repository, "main"), publishedCommit(t, fixture.remote, "main"); local != remote || local != tip {
		t.Errorf("local main = %q, want it caught up to the remote's %q", local, remote)
	}
	// Settled is settled: the next sweep has nothing to ask.
	if again, err := reconciler.FinishPublications(context.Background()); err != nil || len(again) != 0 {
		t.Fatalf("second FinishPublications() = %#v, %v; want nothing left to finish", again, err)
	}
}

// The same shape met as it happens rather than as residue: the forge performs
// the queued merge, something else lands on the remote before the sweep asks,
// and the settlement confirms the merge by containment and records this
// request's own merge commit. Nothing is left outstanding, so nothing is left
// for the finishing sweep to do afterwards.
func TestASettledMergeIsConfirmedWhenOthersLandedAfterIt(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)
	merge := publishedCommit(t, fixture.remote, "main")
	tip := landAnotherChange(t, fixture.remote)

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the merge settled as completed", results)
	}
	settled := loadRun(t, fixture.store, pipelineRunID)
	if settled.PublishFailure != "" {
		t.Errorf("publish failure = %q, want a merge that landed among others confirmed", settled.PublishFailure)
	}
	if settled.PullRequest.MergeCommit != merge {
		t.Errorf("recorded merge commit = %q, want this request's merge %q rather than the tip %q", settled.PullRequest.MergeCommit, merge, tip)
	}
	if !fixture.tracker.Record().Closed {
		t.Error("the confirmed merge did not close the item")
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != tip {
		t.Errorf("local main = %q, want it caught up to the remote's %q", local, tip)
	}
	if published := publishedCommit(t, fixture.remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	if left, err := fixture.reconciler(t).FinishPublications(context.Background()); err != nil || len(left) != 0 {
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

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.DropQueuedMerge()
	docket := &memoryDocket{}
	docketer := docketerOverStore(docket, fixture.store, config.Config{
		Execution: config.Execution{IntegrationRetriesBeforeReconciliation: 1},
		Triage:    docketedTriage,
	})
	reconciler := fixture.reconciler(t)
	reconciler.Docket = docketer
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, %v; want the dropped merge handed to a person", results, err)
	}
	dropped := loadRun(t, fixture.store, pipelineRunID)
	if dropped.Blocker == "" || dropped.MergeDrop == nil || !fixture.tracker.Record().Blocked || fixture.tracker.Record().Closed {
		t.Fatalf("dropped = blocker %q, drop %#v, item blocked %t closed %t; want the item handed back", dropped.Blocker, dropped.MergeDrop, fixture.tracker.Record().Blocked, fixture.tracker.Record().Closed)
	}
	if built, err := docketer.Build(); err != nil || len(built.Entries) != 1 || len(built.Entries[0].Earlier) != 1 {
		t.Fatalf("docket = %#v, %v; want the stoppage and the publication both docketed, as one live entry for the run", built, err)
	}
	if !dropped.AwaitingForge() || !heldItemsOf(t, fixture.store, fixture.tracker.Record().Item.ID)[fixture.tracker.Record().Item.ID] {
		t.Fatal("a dropped merge is neither counted as awaiting the forge nor holding its item")
	}

	// A person merges the request on the forge.
	if err := fixture.forge.MergeByHand(fixture.forge.OpenedRequests()[0].Base, outcome.PullRequest.HeadCommit); err != nil {
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

	after := loadRun(t, fixture.store, pipelineRunID)
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
	if !fixture.tracker.Record().Closed || !strings.Contains(fixture.tracker.Record().CloseReason, "merged by the forge") {
		t.Errorf("item closed = %t with reason %q, want the hand-made merge to settle it", fixture.tracker.Record().Closed, fixture.tracker.Record().CloseReason)
	}
	if held := heldItemsOf(t, fixture.store, fixture.tracker.Record().Item.ID); held[fixture.tracker.Record().Item.ID] {
		t.Errorf("holds = %v, want the hold lifted", held)
	}
	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() after the settlement error = %v", err)
	}
	if len(rebuilt.Entries) != 0 || rebuilt.Closed != 2 {
		t.Errorf("docket after the settlement = %#v, want both entries closed", rebuilt)
	}
	if local, remote := publishedCommit(t, fixture.repository, "main"), publishedCommit(t, fixture.remote, "main"); local != remote {
		t.Errorf("local main = %q, want it caught up to the remote's %q", local, remote)
	}
	if published := publishedCommit(t, fixture.remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
}

// A publication the remote still refuses stays outstanding, and the sweep
// writes nothing at all: the record keeps the account the run wrote, which is
// the line on the item, the item gets no note, and the hold stands. What the
// remote says now is reported by the sweep rather than written anywhere.
func TestReconcileLeavesAPublicationTheRemoteStillRefuses(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.forge.SetReplayMerge(true)
	fixture.run(t)
	// The forge replays the promotion rather than merging it, so the promoted
	// commit is on no branch of the remote and containment honestly fails.
	fixture.forge.PerformQueuedMerge(t)
	if settled := fixture.reconcile(t); len(settled) != 1 || settled[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the merge settled with its publication outstanding", settled)
	}
	before := loadRun(t, fixture.store, pipelineRunID)
	if before.PublishFailure == "" || !before.PullRequest.Merged {
		t.Fatalf("record = %#v, want a merged publication nothing could confirm", before)
	}
	notes := len(fixture.tracker.Record().NoteRecords)

	reconciler := fixture.reconciler(t)
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
	after := loadRun(t, fixture.store, pipelineRunID)
	if after.PublishFailure != before.PublishFailure {
		t.Errorf("publish failure = %q, want the run's own account %q kept", after.PublishFailure, before.PublishFailure)
	}
	if after.PullRequest.MergeCommit != "" {
		t.Errorf("record = %#v, want nothing confirmed", after.PullRequest)
	}
	if len(fixture.tracker.Record().NoteRecords) != notes+1 || settlements[0].Finding == nil {
		t.Errorf("notes = %q, want one settlement finding", fixture.tracker.Record().NoteRecords[notes:])
	}
	if held := heldItemsOf(t, fixture.store, fixture.tracker.Record().Item.ID); !held[fixture.tracker.Record().Item.ID] {
		t.Errorf("holds = %v, want the item still held by its outstanding publication", held)
	}

	// The same answer again writes nothing.
	again, err := reconciler.FinishPublications(context.Background())
	if err != nil || len(again) != 1 || again[0].Settled {
		t.Fatalf("second FinishPublications() = %#v, %v; want the same publication still outstanding", again, err)
	}
	if len(fixture.tracker.Record().NoteRecords) != notes+1 {
		t.Error("the same refusal was announced again")
	}
	if repeated := loadRun(t, fixture.store, pipelineRunID); !repeated.UpdatedAt.Equal(before.UpdatedAt) {
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

	fixture := newQueuedFixture(t)
	dropped := &droppingRemote{deletions: 1_000}
	fixture.worktrees = func(observer ReconcileWorktrees) ReconcileWorktrees {
		dropped.ReconcileWorktrees = observer
		return dropped
	}
	fixture.sleep = func(context.Context, time.Duration) error { return nil }
	fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)
	if settled := fixture.reconcile(t); len(settled) != 1 || settled[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the merge settled with its branch left behind", settled)
	}
	leftover := loadRun(t, fixture.store, pipelineRunID)
	if !strings.Contains(leftover.PublishFailure, "delete the merged remote branch") || leftover.PullRequest.MergeCommit == "" {
		t.Fatalf("record = %#v, want a confirmed merge with a leftover branch", leftover)
	}
	notes := len(fixture.tracker.Record().NoteRecords)

	// The connection still drops: nothing is written, and the publication stands.
	still := fixture.reconciler(t)
	settlements, err := still.FinishPublications(context.Background())
	if err != nil || len(settlements) != 1 || settlements[0].Settled || settlements[0].Failure != "" {
		t.Fatalf("FinishPublications() over a dropping connection = %#v, %v; want the leftover left standing", settlements, err)
	}
	if len(fixture.tracker.Record().NoteRecords) != notes {
		t.Errorf("the sweep wrote %d note(s) about a branch it still could not delete", len(fixture.tracker.Record().NoteRecords)-notes)
	}

	// The connection comes back: the branch goes, and the publication settles.
	fixture.worktrees = nil
	working := fixture.reconciler(t)
	settlements, err = working.FinishPublications(context.Background())
	if err != nil || len(settlements) != 1 || !settlements[0].Settled {
		t.Fatalf("FinishPublications() = %#v, %v; want the leftover branch deleted and the publication settled", settlements, err)
	}
	after := loadRun(t, fixture.store, pipelineRunID)
	if after.PublishFailure != "" {
		t.Errorf("publish failure = %q, want none", after.PublishFailure)
	}
	if published := publishedCommit(t, fixture.remote, leftover.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	if len(fixture.tracker.Record().NoteRecords) != notes+1 || !strings.Contains(fixture.tracker.Record().NoteRecords[notes], "Previously outstanding, as the line above it reads: \"Publication outstanding: delete the merged remote branch") {
		t.Errorf("tracker notes after the settlement = %q, want one note naming the leftover it replaces", fixture.tracker.Record().NoteRecords[notes:])
	}
}

// heldItems is what the read model holds out of the pull, by item, read from the
// same store the sweep settled: the hold has to lift because the facts changed,
// and this is the derivation the scheduler and every operator surface read.
type heldItems map[string]bool

func heldItemsOf(t *testing.T, store *runstate.Store, items ...string) heldItems {
	t.Helper()
	holds, err := readmodel.HeldForAPerson(context.Background(), store, store.Triage(), nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	held := make(heldItems)
	for _, id := range items {
		_, holding := holds.Reason(id)
		held[id] = holding
	}
	return held
}

// landAnotherChange puts somebody else's commit on the remote's target branch,
// which is what every merge after this one's does to the tip: it carries content
// this promotion never had.
func landAnotherChange(t *testing.T, remote string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "elsewhere")
	for _, arguments := range [][]string{
		{"clone", "--quiet", remote, clone},
		{"-C", clone, "config", "user.name", "Someone Else"},
		{"-C", clone, "config", "user.email", "someone@example.invalid"},
	} {
		if output, err := exec.Command("git", arguments...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
	disablePipelineMaintenance(t, clone)
	if err := os.WriteFile(filepath.Join(clone, "elsewhere.txt"), []byte("someone else's work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, clone, "add", ".")
	runPipelineGit(t, clone, "commit", "--quiet", "-m", "someone else's work")
	runPipelineGit(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return publishedCommit(t, remote, "main")
}

// A branch moved after the forge merged its request. Containment, rather than
// the recorded publication hash alone, decides whether it leaves anything owed.
func TestReconcileSettlesAMovedMergedBranchOnlyWhenItsTipIsInTheTarget(t *testing.T) {
	t.Parallel()
	for _, contained := range []bool{true, false} {
		t.Run(map[bool]string{true: "tip in target", false: "tip outside target"}[contained], func(t *testing.T) {
			t.Parallel()
			fixture := newQueuedFixture(t)
			outcome := fixture.run(t)
			fixture.forge.PerformQueuedMerge(t)
			tip := landAnotherChange(t, fixture.remote)
			if !contained {
				// Make a commit not reachable from main using the published tip
				// as its parent, without moving any target.
				tree := gitLine(t, fixture.repository, "rev-parse", outcome.Integration.SourceCommit+"^{tree}")
				tip = gitLine(t, fixture.repository, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit-tree", tree, "-p", outcome.Integration.SourceCommit, "-m", "unmerged branch work")
				runPipelineGit(t, fixture.repository, "push", "origin", tip+":refs/heads/"+outcome.Branch)
			} else {
				runPipelineGit(t, fixture.remote, "update-ref", "refs/heads/"+outcome.Branch, tip)
			}
			results := fixture.reconcile(t)
			if len(results) != 1 || results[0].Failure != "" || results[0].Action != ActionCompleted {
				t.Fatalf("Reconcile() = %#v, want the merge settled without a pass failure", results)
			}
			state := loadRun(t, fixture.store, pipelineRunID)
			if contained {
				if state.PublishFailure != "" || state.ReconcileFindings != nil || results[0].Finding != nil {
					t.Fatalf("state = %#v, want no outstanding publication or finding", state)
				}
				if settlements, err := fixture.reconciler(t).FinishPublications(context.Background()); err != nil || len(settlements) != 0 {
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
				notes := len(fixture.tracker.Record().NoteRecords)
				for pass := 0; pass < 2; pass++ {
					settlements, err := fixture.reconciler(t).FinishPublications(context.Background())
					if err != nil || len(settlements) != 1 || settlements[0].Finding == nil || settlements[0].Settled {
						t.Fatalf("FinishPublications() = %#v, %v", settlements, err)
					}
				}
				if len(fixture.tracker.Record().NoteRecords) != notes {
					t.Error("the repeated refusal wrote another finding note")
				}
			}
			if publishedCommit(t, fixture.remote, outcome.Branch) != tip {
				t.Error("the moved remote branch was deleted or changed")
			}
		})
	}
}

// Both requests need their merged branch settled. One refuses deletion, and
// the other must still lose its branch and outstanding publication on this pass.
func TestReconcileFinishesOtherPublicationsBesideARefusedDeletion(t *testing.T) {
	t.Parallel()
	fixture := newQueuedFixture(t)
	fixture.worktrees = func(observer ReconcileWorktrees) ReconcileWorktrees {
		return &refusingBranch{ReconcileWorktrees: observer}
	}
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)
	fixture.reconcile(t)
	first := loadRun(t, fixture.store, pipelineRunID)
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
	if err := fixture.store.Create(other); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, fixture.remote, "update-ref", "refs/heads/"+other.Branch, outcome.Integration.SourceCommit)
	reconciler := fixture.reconciler(t)
	reconciler.Publisher = publicationAnswers{first.Branch: {Number: 1, Merged: true}, other.Branch: {Number: 2, Merged: true}}
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
	if state := loadRun(t, fixture.store, other.RunID); state.PublishFailure != "" {
		t.Fatalf("other publication still outstanding: %s", state.PublishFailure)
	}
	if publishedCommit(t, fixture.remote, other.Branch) != "" {
		t.Error("other branch was not removed")
	}
	if state := loadRun(t, fixture.store, first.RunID); state.ReconcileFindings == nil || state.ReconcileFindings[0].Pending {
		t.Fatal("refusal was not durably recorded and delivered")
	}
	notes := fixture.tracker.Record().NoteRecords
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

type refusingBranch struct{ ReconcileWorktrees }

func (r *refusingBranch) DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error {
	if worktree.WorkItemID == "yoyodyne-task" {
		return fmt.Errorf("remote branch %s is at an unexpected tip, want the published commit %s", worktree.Branch, commit)
	}
	return r.ReconcileWorktrees.DeleteRemoteBranch(ctx, worktree, commit)
}

type publicationAnswers map[string]publish.PullRequest

func (p publicationAnswers) State(_ context.Context, branch string) (publish.PullRequest, error) {
	return p[branch], nil
}
func (p publicationAnswers) Merge(context.Context, publish.MergeRequest) (publish.MergeResult, error) {
	panic("settlement must not merge")
}
func (p publicationAnswers) Close(context.Context, publish.CloseRequest) (publish.Closure, error) {
	panic("settlement must not close")
}

func TestReconcileSettlesAnOutstandingPublicationWhoseMovedTipReachedTheTarget(t *testing.T) {
	t.Parallel()
	fixture := newQueuedFixture(t)
	fixture.worktrees = func(observer ReconcileWorktrees) ReconcileWorktrees {
		return &refusingBranch{ReconcileWorktrees: observer}
	}
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)
	fixture.reconcile(t)
	before := loadRun(t, fixture.store, pipelineRunID)
	if before.PublishFailure == "" || len(before.ReconcileFindings) != 1 {
		t.Fatalf("before = %#v, want an outstanding branch and finding", before)
	}
	tip := landAnotherChange(t, fixture.remote)
	runPipelineGit(t, fixture.remote, "update-ref", "refs/heads/"+outcome.Branch, tip)
	fixture.worktrees = nil
	results, err := fixture.reconciler(t).FinishPublications(context.Background())
	if err != nil || len(results) != 1 || !results[0].Settled || results[0].Finding != nil || results[0].FindingProblem != "" {
		t.Fatalf("FinishPublications() = %#v, %v", results, err)
	}
	if after := loadRun(t, fixture.store, pipelineRunID); after.PublishFailure != "" || len(after.ReconcileFindings) != 0 {
		t.Fatalf("after = %#v, want nothing outstanding", after)
	}
	if publishedCommit(t, fixture.remote, outcome.Branch) != tip {
		t.Fatal("the moved branch was disturbed")
	}
}

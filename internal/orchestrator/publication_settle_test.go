package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
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
	residue.PublishFailure = "confirm the queued merge reached main: remote target branch does not carry the promoted commit: main on origin is at " +
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

type refusingBranch struct{ ReconcileWorktrees }

func (r *refusingBranch) DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error {
	if worktree.WorkItemID == "yoyodyne-task" {
		return fmt.Errorf("remote branch %s is at an unexpected tip, want the published commit %s", worktree.Branch, commit)
	}
	return r.ReconcileWorktrees.DeleteRemoteBranch(ctx, worktree, commit)
}

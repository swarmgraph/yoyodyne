package publicationtests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The case the harness exists to handle: a base branch whose required checks
// have not finished. The merge is queued with the forge rather than demanded, so
// the run finishes and reports the request as queued instead of waiting out a
// confirmation that arrives minutes later — and leaves the published branch on
// the remote, because that branch is what the forge still has to merge.
func TestPipelineQueuesTheMergeAndFinishesWithoutWaitingForIt(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, QueueMerge: true}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	waits := 0
	pipeline.Sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want a succeeded, integrated run", outcome)
	}
	// The promotion is made, but nothing has merged it anywhere but here. Closing
	// the item now would record it integrated against a publication the forge may
	// yet drop, so the closure waits for the forge's answer.
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("a queued merge closed the item as integrated before the forge merged it: reason = %q", tracker.CloseReason)
	}
	// A queued merge is not an outstanding publication: the forge accepted it.
	if outcome.PublishFailure != "" {
		t.Fatalf("publish failure = %q, want a queued merge to be a clean outcome", outcome.PublishFailure)
	}
	if !outcome.PullRequest.MergeQueued || outcome.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want the merge queued and not yet performed", outcome.PullRequest)
	}
	if outcome.PullRequest.MergeMethod != string(publish.MergeCommit) {
		t.Errorf("recorded merge method = %q, want the method the forge was asked for", outcome.PullRequest.MergeMethod)
	}
	// Nothing is waited for, because nothing can arrive while the run watches.
	if waits != 0 || forge.StateCalls != 0 {
		t.Errorf("waits = %d and forge queries = %d, want a run that finished without confirming", waits, forge.StateCalls)
	}
	// The forge has not merged yet, so the remote target has not moved and the
	// branch the queued merge will consume must still be there.
	if head := testharness.PublishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
	if published := testharness.PublishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q left for the queued merge", published, outcome.PullRequest.HeadCommit)
	}
	// The run is over, but it still owes the answer to that merge, so it stays
	// outstanding for reconciliation rather than being settled and forgotten.
	state, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PullRequest == nil || !state.PullRequest.MergeQueued {
		t.Fatalf("durable pull request = %#v, want the queued merge recorded", state.PullRequest)
	}
	if !state.Outstanding() {
		t.Error("a run with a queued merge is not outstanding, so nothing would ever settle it")
	}
	for _, want := range []string{"Merge queued", "stays open until then"} {
		if !strings.Contains(tracker.Notes, want) {
			t.Errorf("tracker notes do not report %q:\n%s", want, tracker.Notes)
		}
	}
}

// A repository whose settings forbid queued merges and whose base branch
// requires checks cannot be published to at all. The operator has to be told
// which setting to change, rather than reading a refusal on every run.
func TestPipelineReportsARepositoryThatCannotQueueAMerge(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, MergeErr: publish.AutoMergeUnavailable{
		Number: 1,
		Status: "BLOCKED",
		Reason: "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the promotion to stand", outcome)
	}
	for _, want := range []string{"cannot queue a merge", "Allow auto-merge", "protection rules"} {
		if !strings.Contains(outcome.PublishFailure, want) {
			t.Errorf("publish failure %q does not name %q", outcome.PublishFailure, want)
		}
	}
	if outcome.PullRequest.MergeQueued {
		t.Errorf("pull request = %#v, want no queued merge recorded for a forge that queued nothing", outcome.PullRequest)
	}
}

// The merge the forge queued lands minutes later, with no run watching.
// Reconciliation is what finds out, and it finishes the publication the run
// could not: the remote target is confirmed to carry the promotion, the forge's
// merge commit is recorded, and the branch the merge consumed is deleted.
func TestReconcileFinishesAQueuedMergeTheForgePerformed(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the queued merge settled as completed", results)
	}
	if !strings.Contains(results[0].Detail, "merged pull request 1") {
		t.Errorf("detail = %q, want the merge named", results[0].Detail)
	}
	settled, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.MergeDrop != nil {
		t.Errorf("merge drop = %#v recorded over a merge the forge performed", settled.MergeDrop)
	}
	if settled.PullRequest.MergeQueued || !settled.PullRequest.Merged {
		t.Fatalf("settled pull request = %#v, want a merge that is no longer queued", settled.PullRequest)
	}
	if settled.PublishFailure != "" {
		t.Fatalf("publish failure = %q, want a publication that finished", settled.PublishFailure)
	}
	remoteTarget := testharness.PublishedCommit(t, fixture.Remote, "main")
	if settled.PullRequest.MergeCommit != remoteTarget {
		t.Errorf("recorded merge commit = %q, want the remote target %q", settled.PullRequest.MergeCommit, remoteTarget)
	}
	testharness.AssertRemoteCarriesPromotion(t, fixture.Repository, fixture.Remote, "main", outcome.Integration.TargetCommit)
	if published := testharness.PublishedCommit(t, fixture.Remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	if !strings.Contains(fixture.Tracker.Record().Notes, "settled the merge this run left queued") {
		t.Errorf("tracker notes do not report the settled merge:\n%s", fixture.Tracker.Record().Notes)
	}
	// The run left the closure to this answer, so this is where the item closes —
	// and the reason says what actually happened rather than describing a run
	// somebody interrupted.
	if !fixture.Tracker.Record().Closed {
		t.Fatal("the confirmed merge did not close the item, so nothing ever will")
	}
	for _, want := range []string{"merged by the forge", "Reviewed and integrated"} {
		if !strings.Contains(fixture.Tracker.Record().CloseReason, want) {
			t.Errorf("close reason %q does not name %q", fixture.Tracker.Record().CloseReason, want)
		}
	}
	// Nothing is left owed, so a second sweep finds nothing at all.
	if again := fixture.Reconcile(t); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// A merge the forge is still holding decides nothing. The run stays outstanding
// and the next sweep asks again, rather than being settled on an answer that has
// not arrived.
func TestReconcileLeavesAQueuedMergeThatIsStillWaiting(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionQueued || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run reported as queued", results)
	}
	held, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !held.PullRequest.MergeQueued || held.PublishFailure != "" {
		t.Fatalf("held pull request = %#v, publish failure = %q; want the queued merge untouched", held.PullRequest, held.PublishFailure)
	}
	// The branch the forge still has to merge must not be swept away underneath
	// it, and the remote target must not have moved.
	if published := testharness.PublishedCommit(t, fixture.Remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q kept for the queued merge", published, outcome.PullRequest.HeadCommit)
	}
	if head := testharness.PublishedCommit(t, fixture.Remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}

	// The forge merges it, and the very next sweep settles the run.
	fixture.Forge.PerformQueuedMerge(t)
	if settled := fixture.Reconcile(t); len(settled) != 1 || settled[0].Action != orchestrator.ActionCompleted {
		t.Fatalf("reconciliation after the merge = %#v, want it completed", settled)
	}
}

// A publication triage has already re-armed, dropped a second time, is not a
// transient cause to repeat past: the request has been through the forge's
// requirements twice and been dropped twice. So the sweep records it as an
// escalation, and it records it on the blocker the item is handed back with —
// which is the surface the configuration and conversation guides both promise
// says so in as many words, and the one a development manager reads before
// deciding anything about it.
func TestReconcileRecordsASecondDropAsAnEscalationRatherThanAReArm(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	fixture.Run(t)

	// The state a carried-out re-arm leaves behind: the identical request
	// repeated, the forge holding a merge for it again, and the publication's one
	// re-arm spent on its own durable counter.
	rearmed, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if rearmed.PullRequest == nil || !rearmed.PullRequest.MergeQueued {
		t.Fatalf("run = %#v, want a queued merge to have been re-armed", rearmed.PullRequest)
	}
	rearmed.PullRequest.MergeRearms = 1
	if err := fixture.Store.Save(rearmed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	fixture.Forge.DropQueuedMerge()
	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run settled on a blocker", results)
	}
	for _, want := range []string{
		// Still everything a first drop says, because it is still a dropped merge
		// the development manager decides about.
		"dropped the queued merge", "the next move is the development manager's",
		// And what this one is beyond that.
		"second drop of this publication", "escalation rather than something to re-arm again",
	} {
		if !strings.Contains(results[0].Detail, want) {
			t.Errorf("detail %q does not name %q", results[0].Detail, want)
		}
	}
	// The blocker on the item is the surface the guides describe, and it is where
	// the development manager reads this rather than in a sweep's output.
	if !fixture.Tracker.Record().Blocked {
		t.Fatal("a second drop left the item unblocked")
	}
	for _, want := range []string{"second drop of this publication", "escalation rather than something to re-arm again"} {
		if !strings.Contains(fixture.Tracker.Record().BlockReason, want) {
			t.Fatalf("the blocker %q does not say %q", fixture.Tracker.Record().BlockReason, want)
		}
	}
	// Nothing about the second drop closes the item or repeats the merge: the
	// sweep can only ask the forge, and a second re-arm is not its to make.
	if fixture.Tracker.Record().Closed {
		t.Fatalf("a second drop closed the item as integrated: reason = %q", fixture.Tracker.Record().CloseReason)
	}
	settled, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.PullRequest.MergeRearms != 1 {
		t.Fatalf("the settled publication records %d re-arm(s), want the one it had spent", settled.PullRequest.MergeRearms)
	}
}

// A queued merge the forge dropped is a requirement that went unmet, and the
// harness does not merge past one — not by asking again and not with
// administrator privileges. The publication is reported as outstanding, on the
// run and on the work item, and the item is handed to the development manager
// with a blocker rather than closed as integrated: nothing merged the change anywhere but here.
// The change itself is safe: the local target branch it was integrated into is
// the authoritative one.
func TestReconcileReportsAQueuedMergeTheForgeDropped(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	fixture.Forge.DropQueuedMerge()
	merges := len(fixture.Forge.MergeRequests())

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run settled on a blocker", results)
	}
	for _, want := range []string{"dropped the queued merge", "the next move is the development manager's", "The forge gave no reason for the drop"} {
		if !strings.Contains(results[0].Detail, want) {
			t.Errorf("detail %q does not name %q", results[0].Detail, want)
		}
	}
	// The forge never merged it, so nothing may record the item as integrated.
	if fixture.Tracker.Record().Closed {
		t.Fatalf("a dropped merge closed the item as integrated: reason = %q", fixture.Tracker.Record().CloseReason)
	}
	if !fixture.Tracker.Record().Blocked || !strings.Contains(fixture.Tracker.Record().BlockReason, "dropped the queued merge") {
		t.Fatalf("blocked = %t, reason = %q; want the dropped merge handed to the development manager",
			fixture.Tracker.Record().Blocked, fixture.Tracker.Record().BlockReason)
	}
	// A first drop is the development manager's and is also the one drop triage may
	// re-arm, so it must not read as the escalation a second one is.
	if strings.Contains(fixture.Tracker.Record().BlockReason, "escalation rather than something to re-arm again") {
		t.Fatalf("a first drop was handed over as an escalation: %q", fixture.Tracker.Record().BlockReason)
	}
	// Reconciliation can only ask the forge, so nothing was merged a second time.
	if len(fixture.Forge.MergeRequests()) != merges {
		t.Fatalf("reconciliation asked for %d further merge(s)", len(fixture.Forge.MergeRequests())-merges)
	}
	settled, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.PullRequest.MergeQueued || settled.PullRequest.Merged {
		t.Fatalf("settled pull request = %#v, want a dropped merge recorded as neither queued nor merged", settled.PullRequest)
	}
	if !strings.Contains(settled.PublishFailure, "dropped the queued merge") {
		t.Errorf("durable publish failure = %q, want the dropped merge recorded", settled.PublishFailure)
	}
	// The drop is a moment as well as a state, and the moment is what a channel
	// can say. Without it the only account of a dropped merge was a status
	// surface somebody thought to run.
	if settled.MergeDrop == nil {
		t.Fatalf("settled state = %#v, want the moment the forge dropped the merge recorded", settled)
	}
	if settled.MergeDrop.At.IsZero() || !strings.Contains(settled.MergeDrop.Reason, "dropped the queued merge") {
		t.Errorf("merge drop = %#v, want the moment and the reason it was dropped", settled.MergeDrop)
	}
	if !strings.Contains(fixture.Tracker.Record().Notes, "Publication outstanding") {
		t.Errorf("tracker notes do not report the outstanding publication:\n%s", fixture.Tracker.Record().Notes)
	}
	// The work is where it belongs, and the evidence a person needs survives.
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want the integrated commit %q", local, outcome.Integration.TargetCommit)
	}
	if published := testharness.PublishedCommit(t, fixture.Remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q left for whoever finishes it", published, outcome.PullRequest.HeadCommit)
	}
	// It is settled, so the sweep does not keep reporting it forever.
	if again := fixture.Reconcile(t); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// Settling a queued merge is a question for the forge, and reconciliation asks
// it before it observes the repository at all. A local target that moved on
// since the run finished says nothing about a merge nobody has answered for
// yet, and must never settle one as a disagreement.
func TestReconcileAsksTheForgeAboutAQueuedMergeWhateverTheLocalTargetShows(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	// Unrelated work lands on the local target, so it no longer stands where
	// this run's promotion left it.
	if err := os.WriteFile(filepath.Join(fixture.Repository, "later.txt"), []byte("later work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	testharness.RunPipelineGit(t, fixture.Repository, "add", "later.txt")
	testharness.RunPipelineGit(t, fixture.Repository, "commit", "-m", "later work")

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionQueued || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the forge asked and the run reported as queued", results)
	}
	if fixture.Tracker.Record().Blocked {
		t.Fatalf("reconciliation blocked a run whose merge the forge still holds: %q", fixture.Tracker.Record().BlockReason)
	}
	held, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if held.Integration == nil || !held.PullRequest.MergeQueued {
		t.Fatalf("held state = %#v, want the queued merge and its promotion untouched", held)
	}

	// The forge merges it, and the next sweep settles the run on that answer
	// rather than on what the local target happens to show.
	fixture.Forge.PerformQueuedMerge(t)
	settled := fixture.Reconcile(t)
	if len(settled) != 1 || settled[0].Action != orchestrator.ActionCompleted || settled[0].Failure != "" {
		t.Fatalf("reconciliation after the merge = %#v, want it completed", settled)
	}
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local == outcome.Integration.TargetCommit {
		t.Fatalf("local main = %q, want the target this test moved on", local)
	}
}

// The forge is asked before the repository is observed at all, so a local
// target that does not carry the promotion cannot settle a merge nobody has
// answered for yet. This builds the one repository the two orderings disagree
// about — the run's branch back at its promoted commit, the local target back
// where it started, and the artifacts recorded as still present — because the
// pipeline's own queued run cleans up locally and never produces it.
func TestReconcileAsksTheForgeAboutAQueuedMergeBeforeObservingTheRepository(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	testharness.RunPipelineGit(t, fixture.Repository, "branch", outcome.Branch, outcome.Integration.SourceCommit)
	testharness.RunPipelineGit(t, fixture.Repository, "update-ref", "refs/heads/main", outcome.BaseCommit)
	queued, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	queued.Phase = runstate.PhaseCleaningUp
	queued.WorktreeRemoved = false
	queued.BranchRemoved = false
	if err := fixture.Store.Save(queued); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionQueued || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the forge asked rather than the repository believed", results)
	}
	if fixture.Tracker.Record().Blocked {
		t.Fatalf("reconciliation blocked a run whose merge the forge still holds: %q", fixture.Tracker.Record().BlockReason)
	}
	held, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if held.Integration == nil || !held.PullRequest.MergeQueued {
		t.Fatalf("held state = %#v, want the queued merge and its promotion untouched", held)
	}
}

// The yoyodyne-ifd.295 shape, replayed. The forge performed the merge it had
// queued, and the connection dropped at the one step the settlement had left:
// deleting the branch that merge consumed. That step is hygiene, so the item
// closes on the confirmed merge anyway, the local target is still caught up, and
// what the failure leaves is a dead branch on the remote recorded for triage.
//
// The old ordering closed the item only if the deletion succeeded. Twice on
// yoyodyne-ifd.295 it did not, and the item stayed open with nothing holding it
// back: the scheduler pulled it as ordinary ready work, and three developer runs
// and three reviews were spent re-deriving that the change had already landed.
func TestASettledMergeClosesTheItemWhenTheMergedBranchCannotBeDeleted(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	dropped := &droppingRemote{deletions: 1_000}
	fixture.Worktrees = func(observer orchestrator.ReconcileWorktrees) orchestrator.ReconcileWorktrees {
		dropped.ReconcileWorktrees = observer
		return dropped
	}
	waits := 0
	fixture.Sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the merge settled as completed", results)
	}
	// The whole of the change: the merge is confirmed, so the item closes.
	if !fixture.Tracker.Record().Closed {
		t.Fatal("the confirmed merge did not close the item because a branch deletion failed, which is the loop this exists to end")
	}
	if !strings.Contains(fixture.Tracker.Record().CloseReason, "merged by the forge") {
		t.Errorf("close reason = %q, want the forge's merge named", fixture.Tracker.Record().CloseReason)
	}
	settled, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The failure is recorded rather than swallowed: nothing sweeps a remote
	// branch afterwards, so the leftover is a person's and the outstanding
	// publication is what puts it on the docket and out of the pull.
	if !strings.Contains(settled.PublishFailure, "delete the merged remote branch") {
		t.Errorf("durable publish failure = %q, want the leftover branch recorded", settled.PublishFailure)
	}
	if !strings.Contains(settled.PublishFailure, "handed to a person") {
		t.Errorf("durable publish failure = %q, want the spent recovery window named", settled.PublishFailure)
	}
	// Retried under yoyodyne-ifd.264's rule rather than given up on at the first
	// reset, and the record says so.
	retries := 0
	for _, retry := range settled.Retries {
		if retry.Boundary == runstate.RetryDeleteRemoteBranch {
			retries++
		}
	}
	if retries < 2 || waits != retries {
		t.Errorf("recorded %d retr(ies) of the deletion over %d wait(s), want the connection reset waited out repeatedly", retries, waits)
	}
	if dropped.attempts != retries+1 {
		t.Errorf("the deletion was attempted %d time(s) for %d recorded retr(ies)", dropped.attempts, retries)
	}
	// The catch-up is not the deletion's to hold up either: the merge is
	// confirmed, so the local target moves onto what the forge made of it.
	if local, remote := testharness.PublishedCommit(t, fixture.Repository, "main"), testharness.PublishedCommit(t, fixture.Remote, "main"); local != remote {
		t.Errorf("local main = %q, want it caught up to the remote's %q", local, remote)
	}
	if published := testharness.PublishedCommit(t, fixture.Remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the branch the deletion could not remove left at %q", published, outcome.PullRequest.HeadCommit)
	}
	// A closed item that says nothing about what it left on the forge tells the
	// person who has to remove it nothing.
	for _, want := range []string{"could not delete the branch that merge consumed", outcome.Branch} {
		if !strings.Contains(fixture.Tracker.Record().Notes, want) {
			t.Errorf("tracker notes do not report %q:\n%s", want, fixture.Tracker.Record().Notes)
		}
	}
	// The run is settled, so nothing pulls this item's thread again.
	if again := fixture.Reconcile(t); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// The same drop, survived. A reset that the next attempt gets past leaves a
// publication with nothing outstanding at all — which is the half of the
// recoverable-failure rule that keeps a dead branch off the triage docket for a
// connection that came back.
func TestASettledMergeWaitsOutADroppedConnectionAtTheBranchDeletion(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	dropped := &droppingRemote{deletions: 1}
	fixture.Worktrees = func(observer orchestrator.ReconcileWorktrees) orchestrator.ReconcileWorktrees {
		dropped.ReconcileWorktrees = observer
		return dropped
	}
	fixture.Sleep = func(context.Context, time.Duration) error { return nil }
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the merge settled as completed", results)
	}
	settled, err := fixture.Store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.PublishFailure != "" {
		t.Errorf("publish failure = %q, want a deletion that succeeded on the second attempt to leave none", settled.PublishFailure)
	}
	if published := testharness.PublishedCommit(t, fixture.Remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	if len(settled.Retries) != 1 || settled.Retries[0].Boundary != runstate.RetryDeleteRemoteBranch {
		t.Errorf("retries = %#v, want the one waited-out reset recorded", settled.Retries)
	}
}

// droppingRemote is the connection to the forge going away at the step a
// settlement has left, which is where it actually went twice on
// yoyodyne-ifd.295. The wording is the one `git` produced there, so the class
// the recoverable-failure rule reads is the class it was given.
type droppingRemote struct {
	orchestrator.ReconcileWorktrees
	deletions int
	attempts  int
}

func (d *droppingRemote) DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error {
	d.attempts++
	if d.attempts <= d.deletions {
		return fmt.Errorf("resolve %s on origin failed with exit code 128: Read from remote host ssh.github.com: Connection reset by peer", worktree.Branch)
	}
	return d.ReconcileWorktrees.DeleteRemoteBranch(ctx, worktree, commit)
}

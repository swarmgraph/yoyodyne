package orchestrator

// The shapes yoyodyne-ifd.402 was admitted on, replayed, and the rule it
// established: a promotion whose record holds no pull request is an outstanding
// publication every surface reports, never a run that finished quietly.
//
// docs/diagnoses/yoyodyne-ifd-402-publication-record-not-lost.md is the account
// of the two runs. Neither record lost its request; what each shows is here so
// the reading stays checkable — a queued merge is on the record the run
// completes with and settles on the next sweep, and an escalated run's request
// is on its record and, now, on the docket entry the development manager reads.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// run-7ae62396 on yoyodyne-ifd.391: the run asked the forge to merge, the forge
// queued it, and the run completed with the merge queued on its record. The
// number the summary prints is the number the durable record holds, in the same
// arming state, and the forge's merge is settled by the sweep after the run.
func TestAQueuedMergeIsOnTheRecordTheRunCompletesWith(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)

	recorded, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Status != runstate.StatusSucceeded || recorded.Integration == nil {
		t.Fatalf("recorded = status %q, integration %#v; want a succeeded, integrated run", recorded.Status, recorded.Integration)
	}
	if recorded.PullRequest == nil {
		t.Fatalf("the record holds no pull request while the outcome reports #%d", outcome.PullRequest.Number)
	}
	if recorded.PullRequest.Number != outcome.PullRequest.Number || !recorded.PullRequest.MergeQueued {
		t.Fatalf("recorded pull request = %#v, want #%d with its merge queued, as the outcome reports", recorded.PullRequest, outcome.PullRequest.Number)
	}
	if len(fixture.forge.MergeRequests()) != 1 {
		t.Fatalf("forge merges = %d, want the one request the run made", len(fixture.forge.MergeRequests()))
	}
	if recorded.PublishFailure != "" {
		t.Fatalf("publish failure = %q on a merge the forge accepted", recorded.PublishFailure)
	}

	// The forge merges seconds later, with no run watching, and the next sweep
	// finishes the publication rather than leaving the record at the run's death.
	fixture.forge.PerformQueuedMerge(t)
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", results)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.PullRequest.Merged || settled.PullRequest.MergeQueued || settled.PullRequest.MergeCommit == "" {
		t.Fatalf("settled pull request = %#v, want the forge's merge recorded", settled.PullRequest)
	}
}

// run-c07d6849 on yoyodyne-ifd.141.3: the reviewer escalated on the granted
// repair round. An escalation integrates nothing, so no merge is asked for —
// that is the verb, not a lost record — and the request the run opened stays on
// its record, open and unmerged, and is named on the escalation entry the
// development manager decides from.
func TestAnEscalatedRunRecordsThePullRequestItLeftOnTheForge(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := newOutcomeTracker()
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(writeFeature, escalateVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Record().Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration != nil {
		t.Fatalf("status = %q, integration = %#v, want an escalation that promoted nothing", outcome.Status, outcome.Integration)
	}
	if outcome.PullRequest == nil || outcome.PullRequest.Merged || outcome.PullRequest.MergeQueued {
		t.Fatalf("outcome pull request = %#v, want the request the developer phase opened, open and unmerged", outcome.PullRequest)
	}
	// Nothing authorized a merge, so nothing asked for one: an escalated change
	// on the forge is one only a person merges.
	if len(forge.Merges) != 0 {
		t.Fatalf("forge merges = %#v, want none for an escalated change", forge.Merges)
	}
	recorded, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.PullRequest == nil || recorded.PullRequest.Number != outcome.PullRequest.Number {
		t.Fatalf("recorded pull request = %#v, want #%d as the outcome reports", recorded.PullRequest, outcome.PullRequest.Number)
	}
	if !strings.Contains(tracker.Record().Notes, "Pull request: #1") {
		t.Errorf("the work item does not name the request:\n%s", tracker.Record().Notes)
	}
	// The entry is what the development manager reads before deciding, and the
	// request is an artifact of the run exactly as its branch is.
	entry := onlyDocketed(t, docket)
	if entry.Class != triage.ClassEscalation {
		t.Fatalf("entry class = %q, want the reviewer's escalation", entry.Class)
	}
	if entry.Artifacts.PullRequest != outcome.PullRequest.Number || entry.Artifacts.PullRequestURL != outcome.PullRequest.URL {
		t.Fatalf("entry artifacts = %#v, want pull request #%d %s named", entry.Artifacts, outcome.PullRequest.Number, outcome.PullRequest.URL)
	}
	if entry.Artifacts.PullRequestMerged || entry.Artifacts.PullRequestMergeQueued {
		t.Errorf("the entry artifacts = %#v report a merge, and nothing merged or armed one", entry.Artifacts)
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "Pull request (open on the forge, unmerged, no merge armed): #1") {
		t.Errorf("the rendered entry does not name the open request:\n%s", rendered)
	}
}

// The refusal the item asks for: a run whose outcome names a request its
// durable record does not hold is refused completion rather than recorded
// succeeded over it. The shape cannot be produced by the pipeline — the record
// and the outcome are written by one statement — so it is produced here by
// hand, through the registered completing step the pipeline itself goes
// through.
func TestCompletionRefusesARunWhoseRecordLostItsPullRequest(t *testing.T) {
	t.Parallel()

	const itemID = "yoyodyne-ifd.402"
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	complete, found := registry.Lookup("run.complete")
	if !found {
		t.Fatal(`Lookup("run.complete") found nothing`)
	}
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := completingRun(itemID, runstate.PhaseCompleting)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: itemID, Status: "in_progress"}}
	run := &activeRun{
		pipeline: Pipeline{Tracker: tracker, Store: store},
		claimed:  true,
		state:    state,
		outcome: Outcome{
			Integration: &gitworktree.Integration{TargetBranch: "main", SourceCommit: "b0bb1e5"},
			// What the summary would print: the request the run obtained and, on
			// this record, never wrote down.
			PullRequest: &runstate.PullRequest{Number: 544, URL: "https://forge.invalid/pull/544"},
		},
	}
	err = complete.Perform(context.Background(), run)
	if err == nil {
		t.Fatal("Perform() completed a run whose record holds no pull request")
	}
	for _, want := range []string{"pull request 544", "durable record holds none", "refused completion"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Perform() error = %v, want it to say %q", err, want)
		}
	}
	// Nothing about the run was recorded as finished: the item was not closed,
	// and what the tracker was told is the refusal rather than the outcome.
	if tracker.Closed || countCalls(tracker.Calls, "complete") != 0 {
		t.Errorf("the tracker was asked for %v on a refused completion, want no closure", tracker.Calls)
	}
	refused, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if refused.Status != runstate.StatusFailed {
		t.Errorf("recorded status = %q, want the refusal recorded as a failed run", refused.Status)
	}
}

// The arming state is confirmed with the number: a record holding the right
// request with its merge not queued, while the summary says the forge holds the
// merge, is a merge no sweep would ever settle, and the run is refused the same
// way.
func TestCompletionRefusesARecordWhoseArmingStateDisagreesWithTheSummary(t *testing.T) {
	t.Parallel()

	const itemID = "yoyodyne-ifd.402"
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	complete, _ := registry.Lookup("run.complete")
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := completingRun(itemID, runstate.PhaseCompleting)
	state.Branch = "yoyodyne/task/abc"
	state.WorktreePath = "/state/worktrees/task"
	state.BaseCommit = rearmedBase
	state.PullRequest = &runstate.PullRequest{
		Remote:     "origin",
		Branch:     state.Branch,
		Number:     544,
		URL:        "https://forge.invalid/pull/544",
		HeadCommit: rearmedCommit,
		State:      "OPEN",
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reported := *state.PullRequest
	reported.MergeQueued = true
	reported.MergeMethod = string(mergeMethod)
	run := &activeRun{
		pipeline: Pipeline{Tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: itemID, Status: "in_progress"}}, Store: store},
		claimed:  true,
		state:    state,
		outcome:  Outcome{PullRequest: &reported},
	}
	err = complete.Perform(context.Background(), run)
	if err == nil {
		t.Fatal("Perform() completed a run whose record disagrees with its summary about the merge")
	}
	for _, want := range []string{"pull request 544 with its merge queued by the merge method", "holds it with no merge asked for", "refused completion"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Perform() error = %v, want it to say %q", err, want)
		}
	}
}

// The silence the item names, ended at the run: a publishing run that promoted
// a change and holds no request records the publication as outstanding rather
// than returning over it, and asks the forge for nothing. The account names the
// branch, because the branch is what the sweep then asks the forge by.
func TestAPromotionWithoutARequestIsRecordedAsAnOutstandingPublication(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := lostPublicationState()
	state.Status = runstate.StatusRunning
	state.Phase = runstate.PhaseIntegrating
	state.CompletedAt = nil
	state.WorktreeRemoved, state.BranchRemoved = false, false
	state.PublishFailure = ""
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	forge := &orchestratortest.Forge{}
	run := &activeRun{
		pipeline:   Pipeline{Store: store, Publisher: forge},
		publishing: true,
		state:      state,
		worktree:   gitworktree.Worktree{Branch: state.Branch, TargetBranch: "main", BaseCommit: state.BaseCommit},
		outcome: Outcome{Integration: &gitworktree.Integration{
			TargetBranch:         "main",
			SourceCommit:         rearmedCommit,
			TargetCommit:         rearmedCommit,
			PreviousTargetCommit: rearmedBase,
		}},
	}
	if err := run.publishIntegration(context.Background()); err != nil {
		t.Fatalf("publishIntegration() error = %v", err)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("forge merges = %#v, want nothing asked of the forge without a request to merge", forge.Merges)
	}
	for _, want := range []string{"holds no pull request for branch " + state.Branch, "nothing was asked of the forge", "yoyo reconcile"} {
		if !strings.Contains(run.outcome.PublishFailure, want) {
			t.Errorf("publish failure %q does not say %q", run.outcome.PublishFailure, want)
		}
	}
	if run.state.MergeDrop != nil {
		t.Errorf("merge drop = %#v recorded over a merge that was never asked for", run.state.MergeDrop)
	}
	recorded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.PublishFailure != run.outcome.PublishFailure {
		t.Fatalf("durable publish failure = %q, want the outcome's %q", recorded.PublishFailure, run.outcome.PublishFailure)
	}
	// The account reaches the work item in the same words, which is where a
	// reader without the record finds it.
	if notes := strings.Join(renderPublishNotes(run.outcome), "\n"); !strings.Contains(notes, "Publication outstanding: "+run.outcome.PublishFailure) {
		t.Errorf("the item notes do not carry the outstanding publication:\n%s", notes)
	}
}

// The silence ended at the sweep, and the merge the run never asked for made:
// the next reconcile asks the forge for the request by the run's branch, records
// it, and arms the merge through the run's own gate — the promoted commit pinned,
// the remote target checked under the promotion lease — so the forge holds the
// merge the approving verdict authorized, and the sweep after that settles it
// exactly as it settles the merge a run queued itself.
func TestReconcileRecoversAndArmsThePullRequestOfAPromotionThatRecordedNone(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	lost := loseThePublication(t, fixture)
	// Counted as awaiting the forge from the record's own account, before the
	// sweep has asked the forge anything.
	if len(readmodel.AwaitingForge([]runstate.State{lost})) != 1 {
		t.Fatal("a promotion with no recorded request was not counted as awaiting the forge before the sweep")
	}
	// The forge holds the request open with no merge queued for it, which is what
	// a run that never asked leaves behind.
	fixture.forge.ForgetMerges()

	recoveries := fixture.recover(t)
	if len(recoveries) != 1 || !recoveries[0].Recovered || !recoveries[0].Armed || recoveries[0].Failure != "" || recoveries[0].Refused != "" {
		t.Fatalf("recoveries = %#v, want the one request recovered and armed", recoveries)
	}
	if recoveries[0].Number != outcome.PullRequest.Number || recoveries[0].Branch != outcome.Branch || !recoveries[0].Queued {
		t.Errorf("recovery = %#v, want pull request %d found by branch %s and queued by the forge", recoveries[0], outcome.PullRequest.Number, outcome.Branch)
	}
	// The request the forge took is the run's own: the promoted commit, by the
	// method the run's merge is made by.
	if len(fixture.forge.MergeRequests()) != 1 {
		t.Fatalf("forge merges = %#v, want exactly the armed request", fixture.forge.MergeRequests())
	}
	if merge := fixture.forge.MergeRequests()[0]; merge.Number != outcome.PullRequest.Number || merge.HeadCommit != outcome.Integration.SourceCommit || merge.Method != mergeMethod {
		t.Errorf("merge request = %#v, want pull request %d pinned to %s by the %s method", merge, outcome.PullRequest.Number, outcome.Integration.SourceCommit, mergeMethod)
	}
	recovered, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recovered.PullRequest == nil {
		t.Fatal("the sweep reported the request recovered and the record still holds none")
	}
	published := *recovered.PullRequest
	if published.Number != outcome.PullRequest.Number || published.Branch != outcome.Branch || published.HeadCommit != outcome.Integration.SourceCommit || published.Remote != "origin" {
		t.Fatalf("recovered pull request = %#v, want #%d on the run's branch at the promoted commit, on origin", published, outcome.PullRequest.Number)
	}
	if !published.MergeQueued || published.MergeMethod != string(mergeMethod) || published.Merged {
		t.Fatalf("recovered pull request = %#v, want the armed merge recorded queued by the %s method", published, mergeMethod)
	}
	// The account of the loss is settled by the request having been made, and the
	// run is back where the queued-merge settlement finds it.
	if recovered.PublishFailure != "" || recovered.MergeDrop != nil {
		t.Errorf("publish failure = %q, merge drop = %#v; want the loss settled by the armed merge", recovered.PublishFailure, recovered.MergeDrop)
	}
	if !recovered.Outstanding() {
		t.Error("the armed merge left the run settled, so nothing would ever finish the publication")
	}
	if len(readmodel.AwaitingForge([]runstate.State{recovered})) != 1 {
		t.Error("the recovered publication is not counted as awaiting the forge")
	}
	// A second sweep finds nothing to recover: the record holds the request.
	if again := fixture.recover(t); len(again) != 0 {
		t.Fatalf("second recovery = %#v, want nothing left to recover", again)
	}
	// The forge merges, and the next sweep finishes the publication as it finishes
	// any queued merge.
	fixture.forge.PerformQueuedMerge(t)
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the armed merge settled", results)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.PullRequest.Merged || settled.PullRequest.MergeQueued || settled.PullRequest.MergeCommit == "" || settled.PublishFailure != "" {
		t.Fatalf("settled pull request = %#v, publish failure = %q; want the forge's merge confirmed and recorded", settled.PullRequest, settled.PublishFailure)
	}
	assertRemoteCarriesPromotion(t, fixture.repository, fixture.remote, "main", outcome.Integration.TargetCommit)
}

// The silence ended before the sweep, too: a promotion whose record holds no
// request is on the docket and counted as awaiting the forge from the record's
// own account of the loss, before anything has asked the forge — so a forge
// that holds no request for the branch, on every sweep, leaves a promotion
// every surface still names rather than one only reconcile's stderr does. The
// entry is keyed to the run, since there is no number, and it is the same entry
// the recovered request joins and the settlement of the armed merge closes.
func TestAPromotionWithoutARequestIsDocketedAndCountedBeforeReconcileRuns(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.docket = &memoryDocket{}
	outcome := fixture.run(t)
	lost := loseThePublication(t, fixture)

	// Counted by the read model, which is what the status line and the heartbeat
	// read; TestAPromotionAwaitingTheForgeNeedsAHuman holds the line it prints.
	if awaiting := readmodel.AwaitingForge([]runstate.State{lost}); len(awaiting) != 1 {
		t.Fatalf("awaiting the forge = %d run(s), want the requestless promotion counted before any sweep", len(awaiting))
	}
	// Docketed, keyed to the run alone, carrying the account and the branch.
	build, err := docketerOverStore(fixture.docket, fixture.store, docketConfig()).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if build.Added != 1 {
		t.Fatalf("docket build = %#v, want the requestless publication docketed", build)
	}
	entries, _ := fixture.docket.List()
	if len(entries) != 1 || entries[0].Class != triage.ClassPublication || entries[0].Key != triage.Key(triage.ClassPublication, pipelineRunID) {
		t.Fatalf("docket = %#v, want one publication entry keyed to run %s alone", entries, pipelineRunID)
	}
	entry := entries[0]
	if entry.Publication == nil || entry.Publication.Number != 0 || entry.Publication.Branch != outcome.Branch {
		t.Fatalf("entry publication = %#v, want no number and the run's branch", entry.Publication)
	}
	if !strings.Contains(entry.Publication.Message, "nothing was asked of the forge") {
		t.Errorf("entry publication = %#v, want the run's account of the loss", entry.Publication)
	}
	rendered := entry.Render()
	for _, want := range []string{"Pull request: none recorded for branch " + outcome.Branch, "Publication outstanding"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered entry does not say %q:\n%s", want, rendered)
		}
	}

	// The recovery arms the merge and dockets nothing further: the request joins
	// the entry the run already has rather than opening a second.
	fixture.forge.ForgetMerges()
	if recoveries := fixture.recover(t); len(recoveries) != 1 || !recoveries[0].Armed {
		t.Fatalf("recoveries = %#v, want the merge armed", recoveries)
	}
	if again, err := docketerOverStore(fixture.docket, fixture.store, docketConfig()).Build(); err != nil || again.Added != 0 {
		t.Fatalf("docket build after recovery = %#v, %v; want nothing added beside the entry the run already has", again, err)
	}
	// The forge merges, the settlement finishes the publication, and the entry
	// closes with it: nothing about the publication is outstanding any more.
	fixture.forge.PerformQueuedMerge(t)
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].DocketProblem != "" {
		t.Fatalf("reconciliation = %#v, want the armed merge settled and its entry closed", results)
	}
	entries, _ = fixture.docket.List()
	if len(entries) != 1 || entries[0].Closed == nil {
		t.Fatalf("docket after settlement = %#v, want the run's publication entry closed", entries)
	}
	if !strings.Contains(entries[0].Closed.Reason, "confirmed on main") {
		t.Errorf("closure = %#v, want the confirmed merge named", entries[0].Closed)
	}
}

// A merge somebody armed by hand on a recovered request needs no arming and
// leaves the record saying so: the account of the loss goes, the merge is
// recorded queued, and the next sweep settles the run on what the forge does.
func TestReconcileSettlesARecoveredRequestSomebodyQueuedByHand(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	loseThePublication(t, fixture)
	// The forge is still holding the merge the run queued, which from the record's
	// side is a merge somebody else armed.
	asked := len(fixture.forge.MergeRequests())

	recoveries := fixture.recover(t)
	if len(recoveries) != 1 || !recoveries[0].Recovered || recoveries[0].Armed || recoveries[0].Failure != "" {
		t.Fatalf("recoveries = %#v, want the queued request recorded and nothing armed", recoveries)
	}
	if !strings.Contains(recoveries[0].Kept, "already holds a merge") || len(fixture.forge.MergeRequests()) != asked {
		t.Fatalf("kept = %q, merges = %#v; want the held merge named and no further request made", recoveries[0].Kept, fixture.forge.MergeRequests())
	}
	recovered, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recovered.PullRequest == nil || !recovered.PullRequest.MergeQueued || recovered.PullRequest.Number != outcome.PullRequest.Number {
		t.Fatalf("recovered pull request = %#v, want #%d recorded with its merge queued", recovered.PullRequest, outcome.PullRequest.Number)
	}
	if recovered.PublishFailure != "" {
		t.Fatalf("publish failure = %q on a merge the forge holds; want the account of the loss cleared", recovered.PublishFailure)
	}
	if !recovered.Outstanding() {
		t.Fatal("the queued merge left the run settled, so nothing would finish the publication")
	}
	fixture.forge.PerformQueuedMerge(t)
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the hand-queued merge settled", results)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.PullRequest.Merged || settled.PullRequest.MergeQueued || settled.PublishFailure != "" {
		t.Fatalf("settled = pull request %#v, publish failure %q; want the merge confirmed and nothing outstanding", settled.PullRequest, settled.PublishFailure)
	}
	assertRemoteCarriesPromotion(t, fixture.repository, fixture.remote, "main", outcome.Integration.TargetCommit)
}

// The merge is armed on the recorded verdict and not inferred from the
// promotion: a record that does not say the reviewer approved is not selected,
// and one that stops saying so under the lease is refused at the action.
func TestReconcileArmsNothingWithoutTheRecordedApproval(t *testing.T) {
	t.Parallel()

	unapproved := lostPublicationState()
	unapproved.ReviewDecision = runstate.ReviewRepair
	if lostPublicationRecord(unapproved) {
		t.Error("a promotion whose record carries no approving verdict was selected for recovery")
	}

	repository, worktreeRoot, store := restartableFixture(t)
	forge := &orchestratortest.Forge{Number: 7}
	reconciler := Reconciler{
		Tracker:   newOutcomeTracker(),
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Publisher: forge,
	}
	recovery := reconciler.armRecoveredMerge(context.Background(), unapproved, runstate.PullRequest{Number: 7, HeadCommit: rearmedCommit}, PublicationRecovery{RunID: unapproved.RunID})
	if recovery.Armed || len(forge.Merges) != 0 {
		t.Fatalf("recovery = %#v, merges = %#v; want nothing armed without an approving verdict", recovery, forge.Merges)
	}
	if !strings.Contains(recovery.Failure, `review decision "repair"`) || !strings.Contains(recovery.Failure, "nothing authorizes the merge") {
		t.Errorf("failure = %q, want the missing approval named", recovery.Failure)
	}
}

// A recovery interrupted between finding the request and arming it — the
// target's promotion queue timing out, here — writes nothing: the record stays
// exactly as the run wrote it, with no request beside the lost account, and the
// next sweep asks the forge again and arms. A request written ahead of the
// arming would be a record nothing selects again, presented everywhere as a
// merge nobody asked for — the hand merge by another route.
func TestAnInterruptedRecoveryLeavesTheRecordForTheNextSweep(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	lost := loseThePublication(t, fixture)
	fixture.forge.ForgetMerges()

	interrupted := fixture.reconciler(t)
	interrupted.Store = &refusingPromotionLease{ReconcileStore: fixture.store}
	recoveries, err := interrupted.RecoverPublications(context.Background())
	if err != nil {
		t.Fatalf("RecoverPublications() error = %v", err)
	}
	if len(recoveries) != 1 || recoveries[0].Recovered || recoveries[0].Armed || !strings.Contains(recoveries[0].Failure, "held the lease") {
		t.Fatalf("recoveries = %#v, want nothing recovered or armed, and the lease named", recoveries)
	}
	if len(fixture.forge.MergeRequests()) != 0 {
		t.Fatalf("forge merges = %#v, want nothing asked while the promotion lease was held", fixture.forge.MergeRequests())
	}
	untouched, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if untouched.PullRequest != nil {
		t.Fatalf("record = pull request %#v beside the account %q; want no request written ahead of the arming", untouched.PullRequest, untouched.PublishFailure)
	}
	if untouched.PublishFailure != lost.PublishFailure || !untouched.PublicationUnrecorded() {
		t.Fatalf("publish failure = %q, want the run's own account left for the next sweep", untouched.PublishFailure)
	}

	// The next sweep, with the lease free, arms it.
	recoveries = fixture.recover(t)
	if len(recoveries) != 1 || !recoveries[0].Recovered || !recoveries[0].Armed {
		t.Fatalf("recoveries = %#v, want the next sweep to arm the merge", recoveries)
	}
	armed, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if armed.PullRequest == nil || !armed.PullRequest.MergeQueued || armed.PullRequest.Number != outcome.PullRequest.Number || armed.PublishFailure != "" {
		t.Fatalf("armed record = pull request %#v, publish failure %q; want the merge queued and the loss settled", armed.PullRequest, armed.PublishFailure)
	}
}

// refusingPromotionLease is the run store with the target branch's promotion
// queue never draining: the lease is refused the way LeasePromotion refuses one
// after the whole wait, without spending the wait.
type refusingPromotionLease struct {
	ReconcileStore
}

func (r *refusingPromotionLease) LeasePromotion(_ context.Context, targetBranch string) (*runstate.Lease, error) {
	return nil, fmt.Errorf("wait to promote into %s: another promotion held the lease for the whole 15m0s wait", targetBranch)
}

// A merge the forge refuses is not forced: the recovered request is recorded
// with the refusal as the dropped merge it is, which is what puts the
// publication on the docket for triage and keeps the item out of the pull.
func TestAReconciledMergeTheForgeRefusesIsDocketedForTriage(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	loseThePublication(t, fixture)
	fixture.forge.ForgetMerges()
	fixture.forge.SetMergeErr(errors.New("GraphQL: Pull request is not mergeable: the base branch requires a review"))

	recoveries := fixture.recover(t)
	if len(recoveries) != 1 || !recoveries[0].Recovered || recoveries[0].Armed || recoveries[0].Failure != "" {
		t.Fatalf("recoveries = %#v, want the request recovered and the merge refused", recoveries)
	}
	if !strings.Contains(recoveries[0].Refused, "requires a review") {
		t.Errorf("refused = %q, want the forge's own words", recoveries[0].Refused)
	}
	recovered, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recovered.PullRequest == nil || recovered.PullRequest.Number != outcome.PullRequest.Number || recovered.PullRequest.MergeQueued {
		t.Fatalf("recovered pull request = %#v, want #%d recorded and not queued", recovered.PullRequest, outcome.PullRequest.Number)
	}
	// How the refused merge was to be made stays on the record, which is what a
	// decided retry repeats.
	if recovered.PullRequest.MergeMethod != string(mergeMethod) {
		t.Fatalf("recovered merge method = %q, want %q kept beside the refusal", recovered.PullRequest.MergeMethod, mergeMethod)
	}
	if !strings.Contains(recovered.PublishFailure, "requires a review") || recovered.MergeDrop == nil {
		t.Fatalf("publish failure = %q, merge drop = %#v; want the refusal recorded as a dropped merge", recovered.PublishFailure, recovered.MergeDrop)
	}
	// On the docket, keyed to the run and the request, carrying the refusal.
	docket := &memoryDocket{}
	build, err := docketerOverStore(docket, fixture.store, docketConfig()).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if build.Added != 1 {
		t.Fatalf("docket build = %#v, want the one publication docketed", build)
	}
	entries, _ := docket.List()
	if len(entries) != 1 || entries[0].Class != triage.ClassPublication || entries[0].Key != triage.PublicationKey(pipelineRunID, outcome.PullRequest.Number) {
		t.Fatalf("docket = %#v, want the publication of run %s and pull request %d", entries, pipelineRunID, outcome.PullRequest.Number)
	}
	if entries[0].Publication == nil || !strings.Contains(entries[0].Publication.Message, "requires a review") {
		t.Errorf("docket entry publication = %#v, want the forge's refusal carried", entries[0].Publication)
	}
	// And counted as awaiting the forge, with the merge as somebody's to decide.
	if len(readmodel.AwaitingForge([]runstate.State{recovered})) != 1 {
		t.Error("the refused publication is not counted as awaiting the forge")
	}
	// The refusal is not asked again by the next sweep: the record holds the
	// request, so there is nothing left to recover, and a dropped merge is
	// triage's to decide about.
	if again := fixture.recover(t); len(again) != 0 {
		t.Fatalf("second recovery = %#v, want nothing left to recover", again)
	}
}

// A request that no longer carries the promoted commit is not what the verdict
// authorized, and is not armed: the same refusal the run's own merge makes.
func TestReconcileDoesNotArmARecoveredRequestThatMoved(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	loseThePublication(t, fixture)
	fixture.forge.ForgetMerges()
	fixture.forge.SetHeadCommit(rearmedCommit)

	recoveries := fixture.recover(t)
	if len(recoveries) != 1 || !recoveries[0].Recovered || recoveries[0].Armed {
		t.Fatalf("recoveries = %#v, want the request recorded and not armed", recoveries)
	}
	for _, want := range []string{"carries " + rearmedCommit, "the promotion integrated " + outcome.Integration.SourceCommit} {
		if !strings.Contains(recoveries[0].Refused, want) {
			t.Errorf("refused = %q, want it to say %q", recoveries[0].Refused, want)
		}
	}
	if len(fixture.forge.MergeRequests()) != 0 {
		t.Fatalf("forge merges = %#v, want nothing asked for a request that moved", fixture.forge.MergeRequests())
	}
	recovered, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recovered.PullRequest == nil || recovered.PullRequest.HeadCommit != rearmedCommit || recovered.MergeDrop == nil {
		t.Fatalf("record = pull request %#v, merge drop %#v; want the moved head recorded as the forge holds it and the drop beside it", recovered.PullRequest, recovered.MergeDrop)
	}
}

// A request the forge has already merged — the hand merge that ended 141.3's
// day — needs no arming: it is recorded merged, and the finishing sweep confirms
// it on the remote and settles the publication.
func TestReconcileFinishesARecoveredRequestTheForgeAlreadyMerged(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)
	loseThePublication(t, fixture)
	fixture.forge.ForgetMerges()

	recoveries := fixture.recover(t)
	if len(recoveries) != 1 || !recoveries[0].Recovered || recoveries[0].Armed || recoveries[0].Failure != "" {
		t.Fatalf("recoveries = %#v, want the merged request recorded and nothing armed", recoveries)
	}
	if !strings.Contains(recoveries[0].Kept, "merged") {
		t.Errorf("kept = %q, want the merge named as the reason nothing was armed", recoveries[0].Kept)
	}
	if len(fixture.forge.MergeRequests()) != 0 {
		t.Fatalf("forge merges = %#v, want nothing asked for a merged request", fixture.forge.MergeRequests())
	}
	// Before anything finishes it, the record already says the truth: the request
	// is merged and the merge is unconfirmed — never that nothing was asked.
	recovered, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recovered.PullRequest == nil || !recovered.PullRequest.Merged {
		t.Fatalf("recovered pull request = %#v, want it recorded merged", recovered.PullRequest)
	}
	if strings.Contains(recovered.PublishFailure, "nothing was asked of the forge") {
		t.Fatalf("publish failure = %q still says nothing was asked, on a request the forge reports merged", recovered.PublishFailure)
	}
	if !strings.Contains(recovered.PublishFailure, "has not confirmed the merge on main") {
		t.Fatalf("publish failure = %q, want the unconfirmed merge named for the finishing sweep", recovered.PublishFailure)
	}
	settlements, err := fixture.reconciler(t).FinishPublications(context.Background())
	if err != nil {
		t.Fatalf("FinishPublications() error = %v", err)
	}
	if len(settlements) != 1 || !settlements[0].Settled || settlements[0].Failure != "" {
		t.Fatalf("settlements = %#v, want the recovered merge finished", settlements)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.PublishFailure != "" || settled.PullRequest.MergeCommit == "" || !settled.PullRequest.Merged {
		t.Fatalf("settled = pull request %#v, publish failure %q; want the merge confirmed and nothing outstanding", settled.PullRequest, settled.PublishFailure)
	}
	assertRemoteCarriesPromotion(t, fixture.repository, fixture.remote, "main", outcome.Integration.TargetCommit)
}

// A forge that holds no request for the branch leaves the record as the run
// wrote it, and says so on every sweep: the promotion is still one nothing can
// see waiting, and going quiet about it would read as having recovered it.
func TestReconcileReportsAPromotionWhoseRequestTheForgeDoesNotHold(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	state := lostPublicationState()
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reconciler := Reconciler{
		Tracker:   newOutcomeTracker(),
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Publisher: &orchestratortest.Forge{StateErr: errors.New("no pull request exists for branch " + state.Branch)},
	}
	recoveries, err := reconciler.RecoverPublications(context.Background())
	if err != nil {
		t.Fatalf("RecoverPublications() error = %v", err)
	}
	if len(recoveries) != 1 || recoveries[0].Recovered || !strings.Contains(recoveries[0].Failure, "no pull request exists") {
		t.Fatalf("recoveries = %#v, want the forge's refusal reported and nothing recovered", recoveries)
	}
	unchanged, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if unchanged.PullRequest != nil || unchanged.PublishFailure != state.PublishFailure {
		t.Fatalf("record = pull request %#v, publish failure %q; want the record left as the run wrote it", unchanged.PullRequest, unchanged.PublishFailure)
	}
}

// A publishing run that never promoted, and a local run that promoted and
// published nothing, are not lost publications: neither said it published a
// request it does not hold.
func TestOnlyAPromotionThatSaidItPublishedIsRecovered(t *testing.T) {
	t.Parallel()

	local := lostPublicationState()
	local.PublishFailure = ""
	if lostPublicationRecord(local) {
		t.Error("a local promotion with no publication failure was selected for recovery")
	}
	unpromoted := lostPublicationState()
	unpromoted.Integration = nil
	if lostPublicationRecord(unpromoted) {
		t.Error("a run that promoted nothing was selected for recovery")
	}
	live := lostPublicationState()
	live.Status = runstate.StatusRunning
	live.Phase = runstate.PhaseIntegrating
	live.CompletedAt = nil
	live.WorktreeRemoved, live.BranchRemoved = false, false
	if lostPublicationRecord(live) {
		t.Error("a run still in flight was selected for recovery; its publication is its own to record")
	}
	if !lostPublicationRecord(lostPublicationState()) {
		t.Error("the promoted, published, requestless run was not selected for recovery")
	}
}

// loseThePublication rewrites a finished, published run's record into the shape
// the item describes and publishIntegration now writes: the promotion recorded,
// the request gone, and the run's own account of the loss in its place. The
// pipeline cannot produce it — the record and the outcome are written by one
// statement — so it is produced by hand on a record the pipeline did write.
func loseThePublication(t *testing.T, fixture queuedFixture) runstate.State {
	t.Helper()
	state, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.PullRequest = nil
	state.PublishFailure = lostPublication(state.RunID, state.WorkItemID, state.Integration.TargetBranch, state.Branch).Error()
	if err := fixture.store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if state.Outstanding() {
		t.Fatal("the requestless record reads as still owing a step, so the run settlement rather than the recovery would find it")
	}
	return state
}

// recover is the sweep that looks a promotion's request up by branch, records
// it, and arms the merge.
func (f queuedFixture) recover(t *testing.T) []PublicationRecovery {
	t.Helper()
	recoveries, err := f.reconciler(t).RecoverPublications(context.Background())
	if err != nil {
		t.Fatalf("RecoverPublications() error = %v", err)
	}
	return recoveries
}

// lostPublicationState is a succeeded, integrated run whose record says it
// published and holds no request: the state publishIntegration now writes
// instead of returning quietly, as it stands once the run is over.
func lostPublicationState() runstate.State {
	state := droppedPublication()
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseComplete
	state.WorktreeRemoved = true
	state.BranchRemoved = true
	state.Blocker = ""
	state.PullRequest = nil
	state.PublishFailure = lostPublication(state.RunID, state.WorkItemID, "main", state.Branch).Error()
	return state
}

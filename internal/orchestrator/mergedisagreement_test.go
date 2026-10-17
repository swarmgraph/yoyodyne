package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A forge that accepted the merge and reports the request merged, where the
// target branch does not carry the reviewed commit, has not merged the change:
// the run is not recorded as merged, the item is not closed, and the forge's
// state is what the record says of the request.
func TestAMergeTheForgeAcceptedThatIsNotOnTheTargetIsNotRecordedAsMerged(t *testing.T) {
	t.Parallel()

	// A replaying forge puts the change's content on main under a fresh commit,
	// so the reviewed commit itself never reaches the target branch.
	run := newProtectedRun(t, &orchestratortest.Forge{
		TargetProtection: publish.BranchProtection{Protected: true, By: "ruleset"},
		ReplayMerge:      true,
	})
	outcome, err := run.pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped at its merge", outcome)
	}
	if outcome.WorkItemClosed || run.tracker.Closed {
		t.Fatal("the item was closed on a merge the target branch does not carry")
	}
	state, err := run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status == runstate.StatusSucceeded || state.RecordedAsMerged() {
		t.Fatalf("status = %s, want the run not recorded as merged", state.Status)
	}
	if state.PullRequest == nil || state.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want it not recorded as merged", state.PullRequest)
	}
	if state.PullRequest.State != "MERGED" {
		t.Errorf("recorded pull request state = %q, want the forge's own MERGED", state.PullRequest.State)
	}
	if state.Integration == nil || !state.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want the landing kept on the record for finishing the merge", state.Integration)
	}
	for _, want := range []string{"not recorded as merged", "does not contain the promoted commit"} {
		if !strings.Contains(state.PublishFailure, want) {
			t.Errorf("publish failure %q does not say %q", state.PublishFailure, want)
		}
	}
	run.assertMainNotAhead(t)
}

// disagreementFixture is one landed run whose record a test can then make
// disagree with the target branch, and the reconciler and docket that sweep it.
type disagreementFixture struct {
	run        *protectedRun
	reconciler Reconciler
	docket     *runstate.DocketStore
	docketer   *Docketer
}

func newDisagreementFixture(t *testing.T) *disagreementFixture {
	t.Helper()
	run := newProtectedRun(t, &orchestratortest.Forge{TargetProtection: publish.BranchProtection{Protected: true, By: "ruleset"}})
	outcome, err := run.pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !run.tracker.Closed {
		t.Fatalf("outcome = %#v, want a landed run", outcome)
	}
	// The item is open again, which is the shape every lost change had: its run
	// recorded as merged and its item still waiting on the work.
	run.tracker.Closed = false
	run.tracker.Item.Status = "in_progress"
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	docketer := &Docketer{
		Docket:    docket,
		Runs:      run.store,
		Decisions: run.store.Triage(),
		Reruns:    run.store.Reruns(),
		Caps:      docketedCaps,
		Triage:    docketedTriage,
		ProductID: "yoyodyne",
	}
	return &disagreementFixture{
		run: run,
		reconciler: Reconciler{
			Tracker:   run.tracker,
			Worktrees: newObserver(t, run.repository, filepath.Dir(outcome.WorktreePath)),
			Store:     run.store,
			Publisher: run.forge,
			Docket:    docketer,
		},
		docket:   docket,
		docketer: docketer,
	}
}

// forgetMerge rewrites the landed run's record onto a commit main does not
// carry, which is what a run recorded as merged over a pull request that never
// landed looks like.
func (f *disagreementFixture) forgetMerge(t *testing.T) string {
	t.Helper()
	tree := strings.TrimSpace(gitOutput(t, f.run.repository, "rev-parse", "main^{tree}"))
	unlanded := strings.TrimSpace(gitOutput(t, f.run.repository,
		"-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit-tree", tree, "-p", "main", "-m", "a change that never landed"))
	state, err := f.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.Integration.SourceCommit = unlanded
	state.Integration.TargetCommit = unlanded
	published := *state.PullRequest
	published.HeadCommit = unlanded
	published.MergeCommit = ""
	published.State = "OPEN"
	state.PullRequest = &published
	if err := f.run.store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return unlanded
}

func (f *disagreementFixture) sweep(t *testing.T) []MergeDisagreementFinding {
	t.Helper()
	findings, err := f.reconciler.FindMergeDisagreements(context.Background())
	if err != nil {
		t.Fatalf("FindMergeDisagreements() error = %v", err)
	}
	return findings
}

func (f *disagreementFixture) entries(t *testing.T) []triage.Entry {
	t.Helper()
	entries, err := f.docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	return entries
}

// A run recorded as merged whose change is on the target branch is the record
// being right, and nothing is written or listed about it.
func TestARunRecordedAsMergedWhoseCommitIsOnTheTargetIsNotListed(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	before, err := fixture.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	findings := fixture.sweep(t)
	if len(findings) != 1 || !findings[0].OnTarget || findings[0].Failure != "" {
		t.Fatalf("findings = %#v, want the one run found on its target", findings)
	}
	if entries := fixture.entries(t); len(entries) != 0 {
		t.Fatalf("docket = %#v, want nothing listed", entries)
	}
	after, err := fixture.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.MergeDisagreement != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("record = %#v, want it untouched", after.MergeDisagreement)
	}
}

// A run recorded as merged whose change is not on the target branch has the
// disagreement written on it and is listed once, with the cause given as
// uncertain; a second sweep, and a docket built from the records, add nothing,
// and nothing is merged, closed or re-run.
func TestARunRecordedAsMergedWhoseCommitIsAbsentIsListedOnce(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	unlanded := fixture.forgetMerge(t)
	merges := len(fixture.run.forge.Merges)

	findings := fixture.sweep(t)
	if len(findings) != 1 {
		t.Fatalf("findings = %#v, want the one run", findings)
	}
	finding := findings[0]
	if finding.OnTarget || !finding.Recorded || !finding.Docketed || finding.Failure != "" {
		t.Fatalf("finding = %#v, want the disagreement recorded and docketed", finding)
	}
	state, err := fixture.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	disagreement := state.MergeDisagreement
	if disagreement == nil || disagreement.Cause != runstate.MergeCauseUncertain || disagreement.Commit != unlanded || disagreement.TargetBranch != "main" {
		t.Fatalf("disagreement = %#v, want the absent commit recorded with an uncertain cause", disagreement)
	}
	// The forge is asked what it says of the request now, and that is what the
	// disagreement and the entry name.
	if disagreement.ForgeState != "MERGED" || disagreement.PullRequest != state.PullRequest.Number {
		t.Errorf("disagreement = %#v, want the request and the forge's state named", disagreement)
	}
	// Recording and listing is all the sweep did.
	if state.Status != runstate.StatusSucceeded || len(fixture.run.forge.Merges) != merges || len(fixture.run.forge.Closed) != 0 || fixture.run.tracker.Closed {
		t.Fatalf("status %s, merges %d (was %d), closed %v; want nothing acted on", state.Status, len(fixture.run.forge.Merges), merges, fixture.run.forge.Closed)
	}

	entries := fixture.entries(t)
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want one entry", entries)
	}
	entry := entries[0]
	if entry.Class != triage.ClassPublication || entry.Key != triage.DisagreementKey(pipelineRunID, state.PullRequest.Number) ||
		entry.Publication == nil || entry.Publication.Disagreement == nil || entry.Publication.Disagreement.Cause != "uncertain" {
		t.Fatalf("entry = %#v, want a publication entry carrying the uncertain disagreement", entry)
	}
	rendered := entry.Render()
	for _, want := range []string{"yoyodyne-task", pipelineRunID, "Pull request #1", "Forge state: MERGED", "Not on the target branch", unlanded, "Cause: uncertain"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("entry does not say %q:\n%s", want, rendered)
		}
	}

	again := fixture.sweep(t)
	if len(again) != 1 || again[0].Recorded || again[0].Docketed || again[0].OnTarget {
		t.Fatalf("second sweep = %#v, want the run found again with nothing new written", again)
	}
	built, err := fixture.docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 {
		t.Errorf("docket build added %d entries, want none beside the one already standing", built.Added)
	}
	if entries := fixture.entries(t); len(entries) != 1 {
		t.Fatalf("docket after a second sweep = %#v, want still one entry", entries)
	}
}

// sweepEvery puts the fixture's sweeps and docket on a clock the test moves, so
// each sweep happens at a later moment than the last and than any decision
// recorded between them.
func (f *disagreementFixture) sweepEvery(t *testing.T) *steppingClock {
	t.Helper()
	clock := &steppingClock{now: time.Now().UTC().Truncate(time.Second)}
	f.reconciler.Clock = clock
	f.docketer.Clock = clock
	return clock
}

// disagreementEntry is the one entry the docket stands at for the fixture's
// run, failing the test where there is not exactly one.
func (f *disagreementFixture) disagreementEntry(t *testing.T) triage.Entry {
	t.Helper()
	entries := f.entries(t)
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want exactly one entry", entries)
	}
	return entries[0]
}

// Sweeping an unchanged run record again and again lists it once, and the entry
// keeps the moment the disagreement was found rather than taking each sweep's.
func TestSweepsOverAnUnchangedRunKeepItsOneEntryAndItsTime(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	fixture.forgetMerge(t)
	clock := fixture.sweepEvery(t)

	fixture.sweep(t)
	first := fixture.disagreementEntry(t)
	for sweep := 2; sweep <= 3; sweep++ {
		clock.now = clock.now.Add(time.Hour)
		findings := fixture.sweep(t)
		if len(findings) != 1 || findings[0].Recorded || findings[0].Docketed {
			t.Fatalf("sweep %d = %#v, want the run found again with nothing new written", sweep, findings)
		}
		if built, err := fixture.docketer.Build(); err != nil || built.Added != 0 {
			t.Fatalf("docket build after sweep %d added %d, error %v; want nothing added", sweep, built.Added, err)
		}
		entry := fixture.disagreementEntry(t)
		if !entry.RecordedAt.Equal(first.RecordedAt) {
			t.Fatalf("after sweep %d the entry is dated %s, want its original %s", sweep, entry.RecordedAt, first.RecordedAt)
		}
	}
	state, err := fixture.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !first.RecordedAt.Equal(state.MergeDisagreement.FoundAt.UTC()) {
		t.Errorf("entry dated %s, want the moment the disagreement was found, %s", first.RecordedAt, state.MergeDisagreement.FoundAt)
	}
}

// A wait the development manager records on the entry still stands after later
// sweeps and a rebuilt docket: none of them lists the run again, and none takes
// her decision off it before the moment it names.
func TestAWaitRecordedOnADisagreementOutlastsLaterSweeps(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	fixture.forgetMerge(t)
	clock := fixture.sweepEvery(t)

	fixture.sweep(t)
	listed := fixture.disagreementEntry(t)
	clock.now = clock.now.Add(time.Minute)
	waited := triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           listed.Key,
		ProductID:     listed.ProductID,
		RunID:         listed.RunID,
		WorkItemID:    listed.WorkItemID,
		Decision:      "wait",
		DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
		ClosedAt:      clock.now,
		RevisitAfter:  clock.now.Add(24 * time.Hour),
	}
	if took, err := fixture.docket.Close(waited); err != nil || !took {
		t.Fatalf("Close() = %v, %v; want the wait recorded", took, err)
	}

	for sweep := 1; sweep <= 2; sweep++ {
		clock.now = clock.now.Add(time.Hour)
		if findings := fixture.sweep(t); len(findings) != 1 || findings[0].Docketed {
			t.Fatalf("sweep %d after the wait = %#v, want the run found again and not listed again", sweep, findings)
		}
		if built, err := fixture.docketer.Build(); err != nil || built.Added != 0 {
			t.Fatalf("docket build after sweep %d added %d, error %v; want nothing added", sweep, built.Added, err)
		}
		entry := fixture.disagreementEntry(t)
		if entry.Closed == nil || entry.Closed.Decision != "wait" || !entry.Closed.Holds(clock.now) {
			t.Fatalf("after sweep %d the entry's decision = %#v, want the wait still holding", sweep, entry.Closed)
		}
		if !entry.RecordedAt.Equal(listed.RecordedAt) {
			t.Fatalf("after sweep %d the entry is dated %s, want its original %s", sweep, entry.RecordedAt, listed.RecordedAt)
		}
	}
}

// A listed run whose change then reaches the target branch is taken off the
// list by the next sweep, and later sweeps leave it off.
func TestADisagreementWhoseCommitArrivesIsTakenOffTheList(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	unlanded := fixture.forgetMerge(t)
	clock := fixture.sweepEvery(t)

	fixture.sweep(t)
	if entry := fixture.disagreementEntry(t); entry.Closed != nil {
		t.Fatalf("entry = %#v, want it listed and open", entry.Closed)
	}

	// The change lands: the remote's main comes to carry the run's commit.
	gitOutput(t, fixture.run.repository, "update-ref", "refs/heads/landing", unlanded)
	gitOutput(t, fixture.run.remote, "fetch", fixture.run.repository, "+refs/heads/landing:refs/heads/main")

	for sweep := 1; sweep <= 2; sweep++ {
		clock.now = clock.now.Add(time.Hour)
		findings := fixture.sweep(t)
		if len(findings) != 1 || !findings[0].OnTarget || findings[0].Failure != "" || findings[0].Settled != (sweep == 1) {
			t.Fatalf("sweep %d after the change landed = %#v, want the run found on its target and its entry closed by the first", sweep, findings)
		}
		entry := fixture.disagreementEntry(t)
		if entry.Closed == nil || entry.Closed.Decision != settledPublicationDecision || !entry.Closed.Holds(clock.now) {
			t.Fatalf("after sweep %d the entry's decision = %#v, want it closed as settled", sweep, entry.Closed)
		}
	}
}

// A run whose item has closed is not the sweep's: the item no longer waits on
// the change, whatever the branch shows.
func TestARunRecordedAsMergedWhoseItemIsClosedIsNotRead(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	fixture.forgetMerge(t)
	fixture.run.tracker.Item.Status = "closed"
	if findings := fixture.sweep(t); len(findings) != 0 {
		t.Fatalf("findings = %#v, want none for a closed item", findings)
	}
	if entries := fixture.entries(t); len(entries) != 0 {
		t.Fatalf("docket = %#v, want nothing listed", entries)
	}
}

// listingTracker is the fake tracker with a listing, which is how the real one
// is read by the sweep: one listing of closed work per sweep.
type listingTracker struct {
	*orchestratortest.Tracker
	lists int
}

func (l *listingTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	l.lists++
	if l.Item.Status == status {
		return []beads.WorkItem{l.Item}, nil
	}
	return nil, nil
}

// Which items are closed is read with one listing for the whole sweep, and no
// item is read on its own: a long history of landed runs costs the tracker one
// call per sweep rather than one per run.
func TestTheSweepReadsWhichItemsAreClosedWithOneListing(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	fixture.forgetMerge(t)
	tracker := &listingTracker{Tracker: fixture.run.tracker}
	fixture.reconciler.Tracker = tracker
	shows := tracker.ShowCalls

	if findings := fixture.sweep(t); len(findings) != 1 || findings[0].OnTarget || !findings[0].Docketed {
		t.Fatalf("findings = %#v, want the open item's run listed", findings)
	}
	if tracker.lists != 1 || tracker.ShowCalls != shows {
		t.Fatalf("listings = %d, items read one at a time = %d; want one listing and none read on its own", tracker.lists, tracker.ShowCalls-shows)
	}

	// An item the listing names as closed is not read again, and its run is not.
	tracker.Item.Status = "closed"
	if findings := fixture.sweep(t); len(findings) != 0 {
		t.Fatalf("findings = %#v, want none for an item the listing names as closed", findings)
	}
	if tracker.ShowCalls != shows {
		t.Errorf("items read one at a time = %d, want none", tracker.ShowCalls-shows)
	}
}

// disagreementEntries is the docket's entries about a merge disagreement.
func disagreementEntries(t *testing.T, docket *memoryDocket) []triage.Entry {
	t.Helper()
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	var found []triage.Entry
	for _, entry := range entries {
		if entry.Publication != nil && entry.Publication.Disagreement != nil {
			found = append(found, entry)
		}
	}
	return found
}

// assertLeftAtItsMerge holds what a sweep that settles a landing through the
// pull request leaves when the forge says merged and the target branch does
// not carry the change: the item open, the run not recorded as merged, the
// forge's state on the request, and the disagreement written and listed once.
func assertLeftAtItsMerge(t *testing.T, fixture queuedFixture, results []Reconciliation, landed string) runstate.State {
	t.Helper()
	if len(results) != 1 || results[0].Action != ActionUnsettled || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run left unsettled at its merge", results)
	}
	if fixture.tracker.Record().Closed {
		t.Fatal("the item was closed on a merge the target branch does not carry")
	}
	state, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.RecordedAsMerged() || state.PullRequest == nil || state.PullRequest.Merged {
		t.Fatalf("status %s, pull request %#v; want the run not recorded as merged", state.Status, state.PullRequest)
	}
	if state.PullRequest.State != "MERGED" {
		t.Errorf("recorded pull request state = %q, want the forge's own MERGED", state.PullRequest.State)
	}
	disagreement := state.MergeDisagreement
	if disagreement == nil || disagreement.Cause != runstate.MergeCauseUncertain || disagreement.Commit != landed || disagreement.ForgeState != "MERGED" {
		t.Fatalf("disagreement = %#v, want the absent commit recorded with the forge's state and an uncertain cause", disagreement)
	}
	if entries := disagreementEntries(t, fixture.docket); len(entries) != 1 || entries[0].Key != triage.DisagreementKey(pipelineRunID, state.PullRequest.Number) {
		t.Fatalf("docket = %#v, want the disagreement listed once", entries)
	}
	return state
}

// A queued merge the forge then performed by replaying the change, so the
// reviewed commit never reaches main, is left at its queued merge rather than
// closed on the forge's word; a second sweep asks again and lists nothing more.
func TestAQueuedMergeTheTargetDoesNotCarryIsLeftAtItsMerge(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	protectBranch(t, fixture.remote, "main")
	fixture.forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "branch protection"})
	fixture.forge.SetReplayMerge(true)
	fixture.docket = &memoryDocket{}
	outcome := fixture.run(t)
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	fixture.forge.PerformQueuedMerge(t)

	state := assertLeftAtItsMerge(t, fixture, fixture.reconcile(t), outcome.Integration.SourceCommit)
	if !state.PullRequest.MergeQueued {
		t.Errorf("pull request = %#v, want it still at its queued merge for the next sweep", state.PullRequest)
	}
	assertLeftAtItsMerge(t, fixture, fixture.reconcile(t), outcome.Integration.SourceCommit)
}

// A process killed while landing, whose request the forge then reports merged
// by a replay, is left at its merge rather than completed on the forge's word;
// a second sweep asks again and lists nothing more.
func TestAnInterruptedLandingTheTargetDoesNotCarryIsLeftAtItsMerge(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	protectBranch(t, fixture.remote, "main")
	fixture.docket = &memoryDocket{}
	forge := fixture.forge
	forge.SetQueueMerge(false)
	forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "branch protection"})
	// The forge declines, so the run goes on past the merge request and stops;
	// what a killed process leaves is the record as it stood when the forge was
	// asked, which is taken here and put back afterwards.
	forge.SetMergeErr(publish.MergeRefused{Number: 1, Method: publish.MergeCommit, Status: "BLOCKED", Reason: "held"})
	var killed runstate.State
	forge.SetOnMerge(func() {
		recorded, err := fixture.store.Load(pipelineRunID)
		if err != nil {
			t.Fatalf("Load() at the merge request error = %v", err)
		}
		killed = recorded
	})
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, fixture.repository, fixture.worktreeRoot, fixture.store, fixture.tracker, provider, []string{"exit 0"}), provider), forge)
	_, _ = pipeline.Run(context.Background(), fixture.tracker.Record().Item.ID)
	if killed.Integration == nil || !killed.Integration.ThroughPullRequest || killed.Status.Terminal() {
		t.Fatalf("record at the merge request = status %q, integration %#v; want a live landing on the record", killed.Status, killed.Integration)
	}
	if err := fixture.store.Save(killed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	fixture.tracker.ForgetSettlement()
	fixture.tracker.SetItemStatus("in_progress")
	forge.SetMergeErr(nil)
	landed := killed.Integration.SourceCommit
	forge.SetReplayMerge(true)
	if err := forge.MergeByHand("main", landed); err != nil {
		t.Fatalf("merge the request: %v", err)
	}

	state := assertLeftAtItsMerge(t, fixture, fixture.reconcile(t), landed)
	if state.Status.Terminal() || state.Integration == nil {
		t.Errorf("status %s, integration %#v; want the run still at its landing for the next sweep", state.Status, state.Integration)
	}
	assertLeftAtItsMerge(t, fixture, fixture.reconcile(t), landed)
}

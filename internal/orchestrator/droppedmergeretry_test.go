package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A merge the forge's merge queue dropped leaves a run that is not landed and
// whose unfinished merge is on file, and the three things that act on it read
// that one record: the development manager's docket lists the publication,
// finishing the merge does not call it landed, and a re-arm she decides is
// carried out as the identical request. This is pull request #1543's case
// (yoyodyne-ifd.428.99.21).
func TestADroppedQueuedMergeIsLeftUnmergedOnFileAndItsDecidedRetryIsCarriedOut(t *testing.T) {
	t.Parallel()

	fixture, reconciler := droppedFromTheQueue(t, removedForFailedChecks(), nil)
	settleDrop(t, fixture, reconciler)
	dropped, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertUnfinishedMergeOnFile(t, dropped)
	if dropped.MergeDrop.Unsettled {
		t.Fatalf("merge drop = %#v, want it settled once the item and the docket were told", dropped.MergeDrop)
	}

	// Her list: the publication, under the key the re-arm is counted by.
	if _, err := docketerOverStore(fixture.docket, fixture.store, rearmConfig()).Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	key := triage.PublicationKey(dropped.RunID, dropped.PullRequest.Number)
	if !docketHolds(t, fixture.docket, key) {
		t.Fatalf("docket = %v, want the dropped merge listed as publication %s", fixture.docket.keys(), key)
	}

	rearmer, forge := rearmerOver(fixture.docket, fixture.store, dropped, execution.RealClock{})
	if _, err := fixture.store.Triage().RecordMergeRearm(context.Background(), dropped.WorkItemID, key,
		triageDecided(runstate.TriageDecisionRearm, dropped.RunID), docketedNow, rearmCaps); err != nil {
		t.Fatalf("RecordMergeRearm() error = %v", err)
	}
	result, err := rearmer.Rearm(context.Background(), RearmRequest{Run: dropped.RunID, Reason: rearmReasoning})
	if err != nil {
		t.Fatalf("Rearm() error = %v, want the decided retry carried out", err)
	}
	assertRepeated(t, forge, result, dropped)
	rearmed, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !rearmed.PullRequest.MergeQueued || rearmed.PullRequest.MergeRearms != 1 || rearmed.MergeDropped() {
		t.Fatalf("publication = %+v, want the merge queued again and the retry counted", rearmed.PullRequest)
	}
}

// A sweep that finds the drop and then cannot read the tracker leaves the drop
// on the run rather than a record still saying the forge holds the merge: not
// landed, on the development manager's docket, and owed the rest of its
// settlement, which the next sweep makes from the record without asking the
// forge again.
func TestADroppedMergeWhoseTrackerReadTimesOutStaysOnFileForTheNextSweep(t *testing.T) {
	t.Parallel()

	fixture, reconciler := droppedFromTheQueue(t, removedForFailedChecks(), nil)
	tracker := &unansweringTracker{WorkTracker: fixture.tracker}
	reconciler.Tracker = tracker
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Failure, "did not answer") {
		t.Fatalf("reconciliation = %#v, want the sweep stopped by the tracker that did not answer", results)
	}
	stranded, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertUnfinishedMergeOnFile(t, stranded)
	if !stranded.MergeDrop.Unsettled || !stranded.MergeDropUnsettled() || !stranded.Outstanding() {
		t.Fatalf("merge drop = %#v, want it on file and owed the rest of its settlement", stranded.MergeDrop)
	}
	if blocked := fixture.tracker.Record().BlockReason; blocked != "" {
		t.Fatalf("item blocked with %q, though the tracker never answered", blocked)
	}
	if _, err := docketerOverStore(fixture.docket, fixture.store, rearmConfig()).Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if key := triage.PublicationKey(stranded.RunID, stranded.PullRequest.Number); !docketHolds(t, fixture.docket, key) {
		t.Fatalf("docket = %v, want the dropped merge listed from the run's record alone", fixture.docket.keys())
	}

	// The forge is not asked again: what the drop needs from it is on the record.
	fixture.forge.StateErr = fmt.Errorf("the forge is not to be asked again")
	tracker.answering = true
	results, err = reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the drop's settlement finished on a blocker", results)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertUnfinishedMergeOnFile(t, settled)
	if settled.MergeDrop.Unsettled || settled.Outstanding() || settled.Blocker == "" {
		t.Fatalf("run = drop %#v, blocker %q, outstanding %v; want the settlement finished", settled.MergeDrop, settled.Blocker, settled.Outstanding())
	}
	if blocked := fixture.tracker.Record().BlockReason; !strings.Contains(blocked, "dropped the queued merge") {
		t.Fatalf("item blocker = %q, want the drop", blocked)
	}
}

// A record written before a dropped merge was read as unfinished — recorded as
// merged, listed by the disagreement sweep under its own key, its re-arm decided
// and then set aside by a wait, as run-bb543a24cf52d94ba411560a73a01237's was —
// is read as unfinished without being rewritten, and a re-arm decided on it again
// is carried out.
func TestARunRecordedAsMergedOverItsDroppedMergeIsRetriedFromTheRecordItHas(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	state := harness.state
	ended := *state.CompletedAt
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseCleaningUp
	integration := *state.Integration
	integration.ThroughPullRequest = true
	state.Integration = &integration
	state.MergeDrop = &runstate.MergeDrop{At: ended, Reason: state.PublishFailure}
	state.MergeDisagreement = &runstate.MergeDisagreement{
		FoundAt: ended.Add(time.Minute), TargetBranch: "main", Commit: rearmedCommit, TargetTip: rearmedBase,
		PullRequest: state.PullRequest.Number, ForgeState: "OPEN", Cause: runstate.MergeCauseUncertain,
		Detail: "recorded as merged and its work item is still open, but main does not carry its commit",
	}
	saved, err := harness.runs.Read(state.RunID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	state.Revision = saved.Revision
	if err := harness.runs.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	docket := &memoryDocket{}
	entry, err := docketerOverStore(docket, harness.runs, rearmConfig()).disagreementEntry(state, docketedNow)
	if err != nil {
		t.Fatalf("disagreementEntry() error = %v", err)
	}
	if _, err := docket.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	harness.docket = docket
	assertUnfinishedMergeOnFile(t, state)

	// Decided, then set aside by a wait before it was carried out, then decided
	// again: the second decision takes up the re-arm the first spent and nobody
	// made, rather than being refused at the cap of one.
	ctx := context.Background()
	key := triage.PublicationKey(state.RunID, state.PullRequest.Number)
	triages := harness.runs.Triage()
	if _, err := triages.RecordMergeRearm(ctx, state.WorkItemID, key, triageDecided(runstate.TriageDecisionRearm, state.RunID), docketedNow, rearmCaps); err != nil {
		t.Fatalf("RecordMergeRearm() error = %v", err)
	}
	wait := triageDecided(runstate.TriageDecisionWait, state.RunID)
	wait.WaitsOn = "yoyodyne-ifd.428.99.21"
	if _, err := triages.RecordDecision(ctx, state.WorkItemID, wait, docketedNow.Add(time.Hour)); err != nil {
		t.Fatalf("RecordDecision(wait) error = %v", err)
	}
	if _, err := triages.RecordMergeRearm(ctx, state.WorkItemID, key, triageDecided(runstate.TriageDecisionRearm, state.RunID), docketedNow.Add(2*time.Hour), rearmCaps); err == nil {
		t.Fatal("RecordMergeRearm() spent a second re-arm of one publication; the cap of one should refuse it")
	}
	counters, err := triages.RecordMergeRearmOf(ctx, state.WorkItemID, key, state.PullRequest.MergeRearms,
		triageDecided(runstate.TriageDecisionRearm, state.RunID), docketedNow.Add(2*time.Hour), rearmCaps)
	if err != nil {
		t.Fatalf("RecordMergeRearmOf() error = %v, want the unmade re-arm decided again", err)
	}
	if counters.RearmsOf(key) != 1 {
		t.Fatalf("re-arms of %s = %d, want the one spent and not made", key, counters.RearmsOf(key))
	}

	rearmer, forge := rearmerOver(docket, harness.runs, state, docketClock{})
	result, err := rearmer.Rearm(ctx, RearmRequest{Run: state.RunID, Reason: rearmReasoning})
	if err != nil {
		t.Fatalf("Rearm() error = %v, want the retry decided from the disagreement entry carried out", err)
	}
	assertRepeated(t, forge, result, state)
}

// assertUnfinishedMergeOnFile is the reading every route takes of a run whose
// merge the forge dropped: not landed, its unfinished merge and how it is to be
// made on the record, nothing for the disagreement sweep, and finishing the
// merge naming the retry rather than calling it landed.
func assertUnfinishedMergeOnFile(t *testing.T, state runstate.State) {
	t.Helper()
	if state.RecordedAsMerged() {
		t.Fatalf("run %s is recorded as merged over its dropped merge: status %s, pull request %+v", state.RunID, state.Status, state.PullRequest)
	}
	if !state.MergeDropped() {
		t.Fatalf("run %s does not read as a dropped merge: drop %#v, pull request %+v", state.RunID, state.MergeDrop, state.PullRequest)
	}
	if state.PullRequest.MergeQueued || strings.TrimSpace(state.PullRequest.MergeMethod) == "" || state.Integration.TargetBranch != "main" {
		t.Fatalf("unfinished merge = %+v into %q, want it unqueued with its method and target on file", state.PullRequest, state.Integration.TargetBranch)
	}
	if disagreementCandidate(state) {
		t.Fatalf("run %s is a candidate for the merge disagreement sweep, which reads only runs recorded as merged", state.RunID)
	}
	err := RecoveryFinishable(state)
	if err == nil || strings.Contains(err.Error(), "promoted already") || !strings.Contains(err.Error(), fmt.Sprintf("pull request %d", state.PullRequest.Number)) {
		t.Fatalf("RecoveryFinishable() = %v, want the dropped merge named as unfinished rather than landed", err)
	}
}

// docketHolds reports a publication-class entry under key.
func docketHolds(t *testing.T, docket *memoryDocket, key string) bool {
	t.Helper()
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, entry := range entries {
		if entry.Class == triage.ClassPublication && entry.Key == key {
			return true
		}
	}
	return false
}

// rearmerOver is the re-arm action over a run store, with a forge whose request
// is open, unchanged and holding nothing back. The clock is the one the run was
// recorded under.
func rearmerOver(docket *memoryDocket, runs *runstate.Store, state runstate.State, clock execution.Clock) (Rearmer, *orchestratortest.RearmForge) {
	forge := &orchestratortest.RearmForge{
		Observed: publish.PullRequest{Number: state.PullRequest.Number, URL: state.PullRequest.URL, State: "OPEN", HeadCommit: state.PullRequest.HeadCommit},
		Status:   "CLEAN",
		Result:   publish.MergeResult{Queued: true},
	}
	return Rearmer{
		Docket:    docket,
		Runs:      &leasedRuns{Store: runs},
		Forge:     forge,
		Worktrees: &orchestratortest.RemoteTarget{},
		Decisions: runs.Triage(),
		Clock:     clock,
	}, forge
}

// assertRepeated is the one request a re-arm makes: the recorded pull request,
// by its recorded method, pinned to the commit that was promoted.
func assertRepeated(t *testing.T, forge *orchestratortest.RearmForge, result RearmResult, state runstate.State) {
	t.Helper()
	want := publish.MergeRequest{Number: state.PullRequest.Number, HeadCommit: state.Integration.SourceCommit, Method: publish.MergeMethod(state.PullRequest.MergeMethod)}
	if len(forge.Requested) != 1 || forge.Requested[0] != want {
		t.Fatalf("merge requests = %#v, want the dropped request repeated as %#v", forge.Requested, want)
	}
	if !result.Rearmed || !result.Queued {
		t.Fatalf("result = %+v, want the retry made and queued", result)
	}
}

// unansweringTracker is a tracker whose reads time out until it answers again,
// which is the slow tracker a scheduled sweep met.
type unansweringTracker struct {
	WorkTracker
	answering bool
}

func (t *unansweringTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	if !t.answering {
		return beads.WorkItem{}, fmt.Errorf("the tracker did not answer within its time limit: %w", context.DeadlineExceeded)
	}
	return t.WorkTracker.Show(ctx, id)
}

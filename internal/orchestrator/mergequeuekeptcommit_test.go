package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const keptEntryID = "mqe-a0191548c939cdb93cbe658ab8d54877"

// waitingOnQueueState is an approved developer's change admitted to the
// harness's merge queue and waiting there: its run ended succeeded, keeping its
// branch, worktree, session, passed checks and approval, with the commit the
// queue holds as its harness commit.
func waitingOnQueueState() runstate.State {
	state := approvedStoppedState()
	completed := state.UpdatedAt
	state.Status = runstate.StatusSucceeded
	state.Failure = ""
	state.Environmental = nil
	state.IntegrationStop = nil
	state.ChecksPassed = &runstate.ChecksPassed{
		Content: strings.Repeat("d", 40), Attempt: state.RepairAttempts, Commands: []string{"make test"}, At: completed,
	}
	state.MergeQueue = &runstate.MergeQueueAdmissionRecord{
		EntryID: keptEntryID, Order: 39, Repository: "yoyodyne", TargetBranch: "main",
		Mode: runstate.MergeQueueHarness, ApprovedHead: state.HarnessCommit, AdmittedAt: completed,
	}
	return state
}

func keptEntry(state runstate.State) runstate.MergeQueueEntry {
	return runstate.MergeQueueEntry{
		ProductID: "yoyodyne", Repository: "yoyodyne", TargetBranch: "main", EntryID: keptEntryID, Order: 39,
		WorkItemID: state.WorkItemID, WorkItemTitle: state.WorkItemTitle, RunID: state.RunID,
		ApprovedHead: state.HarnessCommit, Mode: runstate.MergeQueueHarness,
	}
}

// stuckRelease is the hand-back the queue owes a run whose merge it withdrew
// for having been held queued past the limit: a release naming the harness as
// who moves next.
func stuckRelease(state runstate.State, mover ownership.Mover) runstate.MergeQueueHandback {
	return runstate.MergeQueueHandback{
		At: docketedNow, Continuation: runstate.MergeQueueReleased, Mover: mover,
		Reason:       "its queued merge was withdrawn: the forge held its merge queued for longer than triage.stuck_merge_age (2h0m0s); the entry leaves the queue, and the change is admitted again once what the withdrawal was for is done",
		ApprovedHead: state.HarnessCommit,
	}
}

// handBackKept hands the release back to the waiting run, through the real run
// store and docket, and returns the harness the resumption then acts on.
func handBackKept(t *testing.T, mover ownership.Mover) (*resumeHarness, runstate.MergeQueueHandback) {
	t.Helper()
	waiting := waitingOnQueueState()
	if err := waiting.Validate(); err != nil {
		t.Fatalf("the waiting run is not a valid record: %v", err)
	}
	harness := &resumeHarness{capacity: 2, ownership: &orchestratortest.ResumeOwnership{}}
	root := t.TempDir()
	var err error
	if harness.runs, err = runstate.NewStore(root, "yoyodyne"); err != nil {
		t.Fatal(err)
	}
	if err := harness.runs.Create(waiting); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if harness.intake, err = runstate.NewIntakeHoldStore(root, "yoyodyne"); err != nil {
		t.Fatal(err)
	}
	if harness.docket, err = runstate.NewDocketStore(root, "yoyodyne"); err != nil {
		t.Fatal(err)
	}
	harness.tracker = &orchestratortest.Tracker{Item: beads.WorkItem{ID: waiting.WorkItemID, Title: waiting.WorkItemTitle, Status: "in_progress"}}
	harness.outcome = Outcome{RunID: waiting.RunID, WorkItemID: waiting.WorkItemID, Status: runstate.StatusSucceeded}
	harness.runsRoot = root

	docketer := docketerOverStore(harness.docket, harness.runs, docketConfig())
	docketer.Clock = docketClock{}
	handback := stuckRelease(waiting, mover)
	given := MergeQueueRunHandback{Store: harness.runs, Tracker: harness.tracker, Docket: docketer, Now: func() time.Time { return docketedNow }}
	if err := given.HandBack(context.Background(), keptEntry(waiting), handback); err != nil {
		t.Fatalf("HandBack() error = %v", err)
	}
	harness.recorded = harness.reload(t)
	return harness, handback
}

// The merge queue withdrew an approved change for having been held queued past
// its limit, which says nothing about the change. Its run keeps the item, a
// fresh run of the item is refused, and the harness takes the change up again
// from its kept commit in a developer slot, ahead of fresh work, at no cost.
func TestAWithdrawnApprovedChangeIsResumedFromItsKeptCommitAndNeverPulledFresh(t *testing.T) {
	t.Parallel()

	harness, handback := handBackKept(t, ownership.MoverHarness)
	stopped := harness.recorded
	waiting := waitingOnQueueState()

	// The run holds the change: stopped at its promotion with its approval and
	// passed checks standing, nothing handed to a developer, and nothing charged.
	if stopped.Status != runstate.StatusFailed || stopped.IntegrationStop == nil || stopped.IntegrationStop.Cause != runstate.CauseMergeQueueReleased {
		t.Fatalf("handed-back run = %s, stop %#v; want it stopped at its promotion as released by the merge queue", stopped.Status, stopped.IntegrationStop)
	}
	if stopped.HandedBack() || stopped.CheckFailure != nil || stopped.ChecksPassed == nil || stopped.HarnessCommit != waiting.HarnessCommit {
		t.Fatalf("handed-back run: failure %#v, checks passed %#v, commit %s; want no failure, its checks and its approved commit kept",
			stopped.CheckFailure, stopped.ChecksPassed, stopped.HarnessCommit)
	}
	if stopped.RepairAttempts != waiting.RepairAttempts || stopped.ReviewDecision != runstate.ReviewApprove {
		t.Fatalf("handed-back run: attempts %d, decision %q; want nothing charged and the approval standing", stopped.RepairAttempts, stopped.ReviewDecision)
	}
	if !stopped.HarnessResumesIntegration() {
		t.Fatal("the handed-back run is not one the harness resumes by itself")
	}
	if !strings.Contains(harness.tracker.Item.Notes, "Nothing about the change itself failed") || !strings.Contains(harness.tracker.Item.Notes, mergeQueueHandbackMarker(keptEntry(waiting), handback)) {
		t.Fatalf("item notes = %q, want the item told its kept change is taken up again", harness.tracker.Item.Notes)
	}
	// Handing the same release back again writes nothing more.
	calls := len(harness.tracker.Calls)
	again := MergeQueueRunHandback{Store: harness.runs, Tracker: harness.tracker, Now: func() time.Time { return docketedNow }}
	if err := again.HandBack(context.Background(), keptEntry(waiting), handback); err != nil {
		t.Fatalf("HandBack() again error = %v", err)
	}
	if len(harness.tracker.Calls) != calls || harness.reload(t).UpdatedAt != stopped.UpdatedAt {
		t.Fatalf("tracker calls %v; want nothing written to the item again, and the run unchanged", harness.tracker.Calls[calls:])
	}

	// No pull starts a fresh run of the item in between.
	repository, worktreeRoot, store := restartableFixture(t)
	if err := store.Create(stopped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	pulled := &orchestratortest.Tracker{Item: beads.WorkItem{ID: stopped.WorkItemID, Title: stopped.WorkItemTitle, Status: "open"}}
	fresh := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a fresh run reached a developer in %s, in place of resuming run %s", request.WorkingDirectory, stopped.RunID)
		return nil
	}, approveVerdict)
	starting := automatic(newSharedPipeline(t, repository, worktreeRoot, store, pulled, fresh, []string{"exit 0"}), fresh)
	starting.NewRunID = runstate.NewRunID
	_, err := starting.Run(context.Background(), stopped.WorkItemID)
	var kept KeptApprovalError
	if !errors.Is(err, ErrKeptApprovalSubstituted) || !errors.As(err, &kept) || !kept.Harness {
		t.Fatalf("Run() error = %v, want the fresh run refused for the approved change the harness resumes", err)
	}
	if inFlight, err := store.Incomplete(); err != nil || len(inFlight) != 0 || len(fresh.Requests) != 0 {
		t.Fatalf("runs in flight %#v (%v), provider requests %d; want nothing reserved or spent", inFlight, err, len(fresh.Requests))
	}
	var declined Started
	declined.record(completed{err: err})
	if declined.Declined == "" || declined.Failure != "" {
		t.Fatalf("schedule entry declined %q, failure %q; want the refused start recorded as declined, not failed", declined.Declined, declined.Failure)
	}

	// It is taken up ahead of fresh work, as work on a kept change.
	if !keptChange(context.Background(), Pull{}, time.Now)(CarryOutTask{Decision: DecisionResumeIntegration, RunID: stopped.RunID}) {
		t.Fatal("the harness's resumption is not ranked as work on a kept change")
	}
	resumes := HarnessResumedIntegrations{Runs: harness.runs, Resumer: harness.resumer()}
	if due, err := resumes.Due(stopped.RunID); err != nil || !due {
		t.Fatalf("Due() = %t, %v; want the resumption due at the next pull", due, err)
	}
	result, err := resumes.Resume(context.Background(), stopped.RunID)
	if err != nil || !result.Resumed {
		t.Fatalf("Resume() = %#v, %v; want the run resumed", result, err)
	}
	if len(harness.started) != 1 || harness.started[0] != (continuedRun{workItemID: stopped.WorkItemID, runID: stopped.RunID}) {
		t.Fatalf("started = %#v, want the same run continued from its kept commit, once", harness.started)
	}
	resumed := harness.reload(t)
	if resumed.Status != runstate.StatusRunning || resumed.Phase != runstate.PhaseIntegrating || resumed.HarnessCommit != waiting.HarnessCommit || resumed.Branch != waiting.Branch {
		t.Fatalf("resumed run = %s/%s at %s on %s; want it live at its promotion on its kept commit and branch", resumed.Status, resumed.Phase, resumed.HarnessCommit, resumed.Branch)
	}
	if resumed.ChecksPassed == nil || resumed.RepairAttempts != waiting.RepairAttempts || resumed.IntegrationStop != nil {
		t.Fatalf("resumed run: checks %#v, attempts %d, stop %#v; want its checks kept and nothing charged", resumed.ChecksPassed, resumed.RepairAttempts, resumed.IntegrationStop)
	}
	if last, ok := resumed.LastIntegrationResumption(); !ok || last.Cause != runstate.CauseMergeQueueReleased {
		t.Fatalf("resumption = %#v, want it recorded as the merge queue's release resumed", last)
	}
	if !harness.tracker.Claimed {
		t.Fatal("the item was not put back to work for the resumed run")
	}
}

// A candidate whose checks failed may be the change's own failure, so its
// release is the development manager's to decide on, as before: the run is
// stopped with the failure handed back, and nothing resumes it by itself.
func TestAReleaseForFailedChecksIsLeftForTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	harness, _ := handBackKept(t, ownership.MoverDevelopmentManager)
	stopped := harness.recorded
	if stopped.IntegrationStop != nil || stopped.CheckFailure == nil || !stopped.HandedBack() || stopped.HarnessResumesIntegration() {
		t.Fatalf("handed-back run: stop %#v, failure %#v; want the failure handed back and no resumption by the harness", stopped.IntegrationStop, stopped.CheckFailure)
	}
	if strings.Contains(harness.tracker.Item.Notes, "Nothing about the change itself failed") {
		t.Fatalf("item notes = %q, want no promise of a resumption", harness.tracker.Item.Notes)
	}
}

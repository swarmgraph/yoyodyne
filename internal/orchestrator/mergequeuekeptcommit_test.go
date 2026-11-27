package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
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
	// A run admitted to the queue ends past its promotion, at its clean-up.
	state.Phase = runstate.PhaseCleaningUp
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
		Reason:       "its queued merge was withdrawn: the forge held its merge queued for longer than triage.stuck_merge_age (2h0m0s); the entry leaves the queue, and its change goes back to its run, kept",
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
	if says := excludedBecause(declined); !strings.HasPrefix(says, "this session chose it and did not start it: ") || !strings.Contains(says, stopped.RunID) || strings.Contains(says, "another process") {
		t.Fatalf("excludedBecause() = %q, want it said not started and held by run %s, with no other process named", says, stopped.RunID)
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

// failedCheckFixture is a merge GitHub holds whose required check has failed,
// so a pass withdraws it for the development manager.
func failedCheckFixture(t *testing.T) (*promotionFixture, *withdrawingForge, *recordingRepair) {
	t.Helper()
	return heldMergeFixture(t, &recordedGitHub{reply: recordedReply(t, "failed-required-check.json")})
}

func assertReleasedToDevelopmentManager(t *testing.T, f *promotionFixture, repair *recordingRepair) {
	t.Helper()
	landing := f.landing()
	attempt, _ := landing.Current()
	if attempt.Withdrawal == nil || attempt.Withdrawal.NextMover != ownership.MoverDevelopmentManager {
		t.Fatalf("withdrawal = %#v, want it to keep the development manager as who decides next", attempt.Withdrawal)
	}
	if landing.Handback == nil || landing.Handback.Continuation != runstate.MergeQueueReleased || landing.Handback.Mover != ownership.MoverDevelopmentManager {
		t.Fatalf("handback = %#v, want the failed change released for the development manager", landing.Handback)
	}
	if repair.count() != 1 || repair.given[0].Mover != ownership.MoverDevelopmentManager {
		t.Fatalf("handed back %#v, want the run given it once for the development manager", repair.given)
	}
	if strings.Contains(landing.Handback.Reason, "admitted again") || !strings.Contains(landing.Handback.Reason, "for the development manager to decide on") {
		t.Fatalf("handback reason = %q, want it to say who decides next", landing.Handback.Reason)
	}
	// Given to the run itself, it is no stop the harness resumes by itself.
	state := waitingOnQueueState()
	handback := *landing.Handback
	handback.ApprovedHead = state.HarnessCommit
	if resumesFromKeptCommit(state, handback) {
		t.Fatal("a change whose candidate's checks failed would be resumed by the harness itself")
	}
}

// GitHub does not answer the withdrawal of a merge whose required check failed.
// A later pass finishes it without reading the check again, and the change is
// still the development manager's to decide on.
func TestAFailedChecksReleaseAfterGitHubDidNotAnswerGoesToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	f, withdrawer, repair := failedCheckFixture(t)
	withdrawer.answer = func(call int) error {
		if call == 1 {
			return errors.New("the forge did not say whether it withdrew the merge")
		}
		return nil
	}
	f.passAt(0)
	if landing := f.landing(); landing.Handback != nil || withdrawer.count() != 1 {
		t.Fatalf("handback %#v after %d withdrawals, want the withdrawal left standing", landing.Handback, withdrawer.count())
	}
	// One pass finishes the withdrawal, and the next reads it from the record.
	f.passAt(0)
	f.passAt(0)
	assertReleasedToDevelopmentManager(t, f, repair)
}

// The process stops after GitHub confirmed the withdrawal and before the
// release is recorded. The next pass records it from the withdrawal alone, and
// the change is still the development manager's to decide on.
func TestAFailedChecksReleaseAProcessStopCutOffGoesToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	f, withdrawer, repair := failedCheckFixture(t)
	stopped := false
	f.landings.fail = func(landing runstate.MergeQueueLanding) (bool, error) {
		if stopped || landing.Handback == nil {
			return false, nil
		}
		stopped = true
		return false, errStopped
	}
	f.passAt(0)
	landing := f.landing()
	if attempt, _ := landing.Current(); !stopped || landing.Handback != nil || !attempt.Withdrawn() || withdrawer.count() != 1 {
		t.Fatalf("landing = %#v, want the merge confirmed withdrawn and the release not recorded", landing)
	}
	f.passAt(0)
	assertReleasedToDevelopmentManager(t, f, repair)
}

// A withdrawal made for something that says nothing about the change, and cut
// off the same way, still goes to the harness.
func TestAWithdrawalForNothingTheChangeDidStaysTheHarnesssAfterAStop(t *testing.T) {
	t.Parallel()

	f, withdrawer, repair := heldMergeFixture(t, &recordedGitHub{reply: recordedReply(t, "pending-required-check.json")})
	withdrawer.answer = func(call int) error {
		if call == 1 {
			return errors.New("the forge did not say whether it withdrew the merge")
		}
		return nil
	}
	for range 3 {
		f.passAt(2 * time.Hour)
	}
	landing := f.landing()
	if landing.Handback == nil || landing.Handback.Mover != ownership.MoverHarness || repair.count() != 1 {
		t.Fatalf("handback = %#v, want the change released to the harness", landing.Handback)
	}
}

// The whole of it through the real parts: an approved change is admitted, the
// queue releases it, the carry-out offers its resumption, the run is continued
// at its promotion, and the same commit is admitted to the queue again and
// lands, with no developer asked again.
func TestAReleasedChangeIsContinuedAdmittedAgainAndLandsFromItsKeptCommit(t *testing.T) {
	t.Parallel()

	r := newQueueRun(t, false)
	r.pipeline.Config.Execution.MergeQueue = true
	provider := r.pipeline.Backend.(*orchestratortest.Backend)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	outcome := r.run(t)
	first, err := r.runs.Load(outcome.RunID)
	if err != nil || !first.AwaitingMergeQueue() {
		t.Fatalf("run = %#v, %v; want it waiting on the queue", first.MergeQueue, err)
	}
	key := first.MergeQueue.Key()

	docket := &memoryDocket{}
	promoter := r.driver().Promoter
	docketer := docketerOverStore(docket, r.runs, r.pipeline.Config)
	promoter.Repair = MergeQueueRunHandback{Store: r.runs, Tracker: r.tracker, Docket: docketer}
	if released, err := promoter.Withdraw(ctx, key, first.MergeQueue.EntryID, "the forge held its merge too long"); err != nil || !released.Withdrawn {
		t.Fatalf("Withdraw() = %#v, %v; want the entry released", released, err)
	}
	if _, err := promoter.Recover(ctx, key); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	stopped, err := r.runs.Load(outcome.RunID)
	if err != nil || !stopped.HarnessResumesIntegration() {
		t.Fatalf("stop = %#v, %v; want one the harness resumes by itself", stopped.IntegrationStop, err)
	}
	// What every reader of the stop is told is about the queue, and never that
	// a service did not answer or that the environment stopped the change.
	entry := docket.entries[0].Render()
	for _, said := range []string{entry, stopped.IntegrationStop.ResumeSays(stopped.RunID), stopped.IntegrationStop.Describe()} {
		if !strings.Contains(said, "merge queue") || strings.Contains(said, "answer") || strings.Contains(said, "environment") {
			t.Errorf("said %q, want the merge queue named as what let the change go", said)
		}
	}

	carry := CarryOut{
		Docket: docket, Decisions: r.runs.Triage(), Reruns: r.runs.Reruns(), Runs: r.runs,
		Resumes: HarnessResumedIntegrations{Runs: r.runs, Items: r.tracker, Resumer: IntegrationResumer{
			Docket: docket, Runs: r.runs, Intake: newIntakeHoldStore(t), Items: r.tracker,
			Worktrees: r.pipeline.Worktrees.(*gitworktree.Manager), Capacity: 1,
			Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
				return r.pipeline.Continue(ctx, workItemID, runID)
			},
		}},
	}
	tasks, err := carry.Outstanding()
	if err != nil || len(tasks) != 1 || tasks[0].Decision != DecisionResumeIntegration || tasks[0].RunID != outcome.RunID {
		t.Fatalf("Outstanding() = %#v, %v; want the resumption of the kept change", tasks, err)
	}
	for _, said := range []string{tasks[0].Reason, carryingOutWhat(tasks[0])} {
		if !strings.Contains(said, "the merge queue let") || strings.Contains(said, "answer") {
			t.Errorf("the resumption gives %q as its reason, want the merge queue's release", said)
		}
	}
	carried, continued, err := carry.Carry(ctx, tasks[0])
	if err != nil || !carried.Carried {
		t.Fatalf("Carry() = %#v, %v; want the run continued", carried, err)
	}
	if strings.Contains(carried.Reason, "environment") || !strings.Contains(carried.Reason, "merge queue") {
		t.Errorf("resumption reason = %q, want the merge queue's release", carried.Reason)
	}
	if continued.RunID != outcome.RunID || continued.MergeQueue == nil || continued.MergeQueue.EntryID == first.MergeQueue.EntryID ||
		continued.MergeQueue.ApprovedHead != first.MergeQueue.ApprovedHead {
		t.Fatalf("continued run admitted as %#v, want the same run's same commit under a new entry", continued.MergeQueue)
	}
	if pass := r.driver().Pass(ctx, key); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the kept commit landed", pass)
	}
	final, err := r.runs.Load(outcome.RunID)
	if err != nil || final.Integration == nil || final.HarnessCommit != first.HarnessCommit || final.RepairAttempts != 0 || !r.tracker.Closed {
		t.Fatalf("final run: integration %#v, commit %s, attempts %d, closed %t, %v; want the kept commit landed at no charge",
			final.Integration, final.HarnessCommit, final.RepairAttempts, r.tracker.Closed, err)
	}
	if developed := provider.RequestsForRole(domain.RoleDeveloper); len(developed) != 1 {
		t.Fatalf("developer invocations = %d, want only the first run's", len(developed))
	}
}

// GitHub's own merge queue drops a candidate whose checks failed on the merge
// commit: in a test the change touches, or a second time in one it does not. A
// check failed, so the development manager decides. A drop with no failing
// check says nothing about the change and stays the harness's.
func TestADropForFailedChecksGoesToTheDevelopmentManagerAndAPlainDropDoesNot(t *testing.T) {
	t.Parallel()

	for _, tc := range []string{"touched", "untouched twice", "plain"} {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			f, _, repair, checks := heldChecksFixture(t)
			checks.reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 4}
			f.promoter.Drops = f.forge.Forge
			switch tc {
			case "touched":
				// GitHub puts the failure on the file the candidate changes.
				githubQueueDrop(f, 8001)
				f.forge.Forge.QueuedMerge.Failing[0].Paths = []string{"feature.txt"}
			case "untouched twice":
				githubQueueDrop(f, 8001)
				f.pass(queueKey)
				githubQueueDrop(f, 8002)
			default:
				f.forge.DropQueuedMerge()
			}
			f.pass(queueKey)
			if tc != "plain" {
				assertReleasedToDevelopmentManager(t, f, repair)
				reason := f.landing().Handback.Reason
				if !strings.Contains(reason, "TestTheTestRunsDecidedRepairTakesTheHoldsStartAndItsLandingLiftsTheHold") {
					t.Errorf("handback reason = %q, want the failing test named", reason)
				}
				if tc == "touched" && len(f.forge.MergeRequests()) != 1 {
					t.Errorf("merge requests = %d, want no further run for a test the change touches", len(f.forge.MergeRequests()))
				}
				return
			}
			handback := f.landing().Handback
			if handback == nil || handback.Mover != ownership.MoverHarness || repair.count() != 1 {
				t.Fatalf("handback = %#v, want the plain drop released to the harness", handback)
			}
			state := waitingOnQueueState()
			given := *handback
			given.ApprovedHead = state.HarnessCommit
			if !resumesFromKeptCommit(state, given) {
				t.Fatal("a plain drop is not resumed from its kept commit")
			}
		})
	}
}

// fixedWait is what GitHub says a held merge waits on, the same every time.
type fixedWait struct{ wait publish.MergeWait }

func (w fixedWait) MergeWait(context.Context, int) (publish.MergeWait, error) { return w.wait, nil }

// A merge withdrawn for something else — its wait past the limit, or a check
// that never judged the change — while another of its checks had failed is the
// development manager's too.
func TestAWithdrawalBesideAFailedCheckGoesToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	t.Run("held too long", func(t *testing.T) {
		t.Parallel()
		f, _, repair := heldMergeFixture(t, &recordedGitHub{})
		f.promoter.Waits = fixedWait{wait: publish.MergeWait{Required: []publish.HeldMergeCheck{
			{Name: "build", State: publish.RequiredCheckFailed, Conclusion: "failure"},
			{Name: "race", State: publish.RequiredCheckPending},
		}}}
		f.passAt(2 * time.Hour)
		assertReleasedToDevelopmentManager(t, f, repair)
	})
	t.Run("a check that never judged it", func(t *testing.T) {
		t.Parallel()
		f, _, repair, checks := heldChecksFixture(t)
		f.promoter.Reruns = f.queue
		reading := failingBuild(9001)
		reading.Failing = append(reading.Failing, publish.FailedCheck{Name: "race", Conclusion: "cancelled"})
		checks.reading = reading
		f.pass(queueKey)
		assertReleasedToDevelopmentManager(t, f, repair)
	})
}

// A candidate GitHub would not merge directly has no merge to take back, so its
// withdrawal is not written on the attempt: who decides next still follows
// whether a check failed on it.
func TestACandidateGitHubWouldNotMergeIsReleasedByWhetherACheckFailed(t *testing.T) {
	t.Parallel()

	for mover, wait := range map[ownership.Mover]publish.MergeWait{
		ownership.MoverDevelopmentManager: {Required: []publish.HeldMergeCheck{
			{Name: "build", State: publish.RequiredCheckFailed, Conclusion: "failure"},
			{Name: "race", State: publish.RequiredCheckPending},
		}},
		ownership.MoverHarness: {Required: []publish.HeldMergeCheck{{Name: "race", State: publish.RequiredCheckPending}}},
	} {
		t.Run(string(mover), func(t *testing.T) {
			t.Parallel()
			f := newDirectMergeFixture(t)
			f.forge.MergeStatus = "BLOCKED"
			repair := &recordingRepair{}
			f.promoter.Repair, f.promoter.Withdrawer = repair, &withdrawingForge{forge: f.forge.Forge}
			f.promoter.Waits, f.promoter.Readings = fixedWait{wait: wait}, f.queue
			f.verify()
			f.passAt(2 * time.Hour)
			handback := f.landing().Handback
			if handback == nil || handback.Mover != mover || repair.count() != 1 {
				t.Fatalf("handback = %#v, want it released to %s", handback, mover)
			}
			state := waitingOnQueueState()
			given := *handback
			given.ApprovedHead = state.HarnessCommit
			if resumes := resumesFromKeptCommit(state, given); resumes != (mover == ownership.MoverHarness) {
				t.Fatalf("resumesFromKeptCommit() = %t for a release to %s", resumes, mover)
			}
		})
	}
}

// unlookableWorktrees is a worktree manager whose repository cannot be asked
// what is left of a run.
type unlookableWorktrees struct{ *gitworktree.Manager }

func (unlookableWorktrees) Survives(context.Context, gitworktree.Worktree) (gitworktree.Survival, error) {
	return gitworktree.Survival{}, errors.New("the repository did not open")
}

// A fresh run is refused while the branch holding the approved change is there,
// or may be. Once the branch and checkout are both gone, with nothing decided
// and the run's record not yet saying so, nothing is kept and the item is
// started over.
func TestAnItemIsStartedOverOnceItsKeptApprovedChangeIsGone(t *testing.T) {
	t.Parallel()

	r := newQueueRun(t, false)
	r.pipeline.Config.Execution.MergeQueue = true
	provider := r.pipeline.Backend.(*orchestratortest.Backend)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	outcome := r.run(t)
	promoter := r.driver().Promoter
	if released, err := promoter.Withdraw(ctx, outcome.MergeQueue.Key(), outcome.MergeQueue.EntryID, "the forge held its merge too long"); err != nil || !released.Withdrawn {
		t.Fatalf("Withdraw() = %#v, %v; want the entry released", released, err)
	}
	if _, err := promoter.Recover(ctx, outcome.MergeQueue.Key()); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	stopped, err := r.runs.Load(outcome.RunID)
	if err != nil || !stopped.HarnessResumesIntegration() {
		t.Fatalf("stop = %#v, %v; want one the harness resumes by itself", stopped.IntegrationStop, err)
	}
	// As a pull finds it once the item is ready again.
	r.tracker.Item.Status = "open"
	r.pipeline.NewRunID = runstate.NewRunID
	refused := func(pipeline Pipeline) KeptApprovalError {
		t.Helper()
		var kept KeptApprovalError
		if _, err := pipeline.Run(ctx, "yoyodyne-task"); !errors.As(err, &kept) {
			t.Fatalf("Run() error = %v, want the fresh run refused", err)
		}
		if developed := provider.RequestsForRole(domain.RoleDeveloper); len(developed) != 1 {
			t.Fatalf("developer invocations = %d, want none for the refused run", len(developed))
		}
		return kept
	}
	if kept := refused(r.pipeline); kept.Unchecked != "" || strings.Contains(kept.Error(), "could not be checked") {
		t.Fatalf("refusal = %v, want the branch found", kept)
	}

	gitLine(t, r.repository, "worktree", "remove", "--force", stopped.WorktreePath)
	gitLine(t, r.repository, "branch", "-D", stopped.Branch)
	unlookable := r.pipeline
	unlookable.Worktrees = unlookableWorktrees{r.pipeline.Worktrees.(*gitworktree.Manager)}
	if kept := refused(unlookable); !strings.Contains(kept.Error(), "could not be checked") {
		t.Fatalf("refusal = %v, want it to say the branch could not be checked", kept)
	}

	if stopped, err = r.runs.Load(outcome.RunID); err != nil || stopped.BranchRemoved {
		t.Fatalf("branch removed = %t, %v; want the record not saying so", stopped.BranchRemoved, err)
	}
	fresh, err := r.pipeline.Run(ctx, "yoyodyne-task")
	if err != nil || fresh.RunID == outcome.RunID {
		t.Fatalf("Run() = run %s, %v; want the item started over in a fresh run", fresh.RunID, err)
	}
	if developed := provider.RequestsForRole(domain.RoleDeveloper); len(developed) != 2 {
		t.Fatalf("developer invocations = %d, want the fresh run's beside the first", len(developed))
	}
}

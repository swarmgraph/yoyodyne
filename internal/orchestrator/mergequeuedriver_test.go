package orchestrator

// The merge queue's driver: one pass of a queue takes an admitted change from
// candidate to landing by itself, only the candidate's own checks and review
// land it, one process works a queue at a time while another target's queue
// moves beside it, what was admitted drains after the switch is turned off and
// across a restart, and the failures a candidate meets are charged to the
// change only where they are the change's. The project's checks here are shell
// commands over its own files, nothing to do with Go.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func (f *promotionFixture) driver() MergeQueueDriver {
	return MergeQueueDriver{Worker: f.worker, Promoter: f.promoter, Queues: f.queue}
}

func (f *promotionFixture) pass(key runstate.MergeQueueKey) runstate.MergeQueuePass {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return f.driver().Pass(ctx, key)
}

// admitAnother commits a change of its own on a branch off target and admits
// it behind whatever the queue holds, for a run of its own.
func (f *promotionFixture) admitAnother(key runstate.MergeQueueKey, branch, file, content string) runstate.MergeQueueEntry {
	f.t.Helper()
	runPipelineGit(f.t, f.repository, "switch", "-c", branch, key.TargetBranch)
	writeQueueFile(f.t, f.repository, file, content)
	runPipelineGit(f.t, f.repository, "add", file)
	runPipelineGit(f.t, f.repository, "commit", "-m", "the change on "+branch)
	head := gitLine(f.t, f.repository, "rev-parse", "HEAD")
	runPipelineGit(f.t, f.repository, "switch", "main")
	runID, err := runstate.NewRunID()
	if err != nil {
		f.t.Fatal(err)
	}
	if f.run.admittedRun.others == nil {
		f.run.admittedRun.others = map[string]runstate.State{}
	}
	f.run.admittedRun.others[runID] = runstate.State{
		RunID: runID, WorkItemID: "yoyodyne-task", ProviderSessionID: "developer-session-" + branch, Branch: branch,
		ReviewDecision: string(review.DecisionApprove), ReviewSessionID: "earlier-reviewer-session", ReviewHeadCommit: head,
	}
	entry, _, err := f.queue.Admit(context.Background(), runstate.MergeQueueAdmission{
		Key: key, WorkItemID: "yoyodyne-task", WorkItemTitle: "The change on " + branch, RunID: runID,
		ApprovedHead: head, IntegrationPolicy: "automatic", Mode: runstate.MergeQueueHarness, ModeEvidence: harnessModeEvidence(),
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return entry
}

func (f *promotionFixture) pending() []runstate.MergeQueueKey {
	f.t.Helper()
	pending, err := f.driver().Pending()
	if err != nil {
		f.t.Fatalf("Pending() error = %v", err)
	}
	return pending
}

func TestOnePassBuildsChecksReviewsAndLandsTheAdmittedChange(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	if pending := f.pending(); len(pending) != 1 || pending[0] != queueKey {
		t.Fatalf("Pending() = %v, want the one queue with an admitted change", pending)
	}
	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the change landed", pass)
	}
	generations := f.generations()
	landed := generations[len(generations)-1]
	f.assertLandedOnce(landed)
	if f.local("main") != landed.Candidate {
		t.Fatalf("main = %s, want the candidate its own checks and review approved, %s", f.local("main"), landed.Candidate)
	}
	// What authorized the landing is the candidate's own review, not the
	// approval the run was admitted with.
	if landed.Review == nil || landed.Review.SessionID == "earlier-reviewer-session" || landed.Paths == nil || len(landed.Paths.Refused) != 0 {
		t.Fatalf("landed generation = %#v, want its own path check and review", landed)
	}
	if pending := f.pending(); len(pending) != 0 {
		t.Fatalf("Pending() after the landing = %v, want nothing", pending)
	}
	recorded, found, err := f.queue.LastPass(queueKey)
	if err != nil || !found || recorded.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("LastPass() = %#v, %t, %v; want the landing recorded for status to read", recorded, found, err)
	}
}

func TestTheChangesOwnApprovalLandsNothingItsCandidatesReviewerRefused(t *testing.T) {
	t.Parallel()

	// The run that made the change approved it; the candidate's reviewer asks
	// for a repair. Nothing lands.
	f := newPromotionFixture(t, landLocally, repairVerdict)
	f.run.admittedRun.state.Branch = "change"
	f.worker.Pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	base := f.local("main")
	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the change handed back", pass)
	}
	if f.local("main") != base || len(f.landing().Attempts) != 0 {
		t.Fatal("a candidate its reviewer refused moved the target or began to land")
	}
	if repair.count() != 1 || repair.given[0].Continuation != runstate.MergeQueueContinueRepair {
		t.Fatalf("repair given %#v, want the change handed back to its run once", repair.given)
	}
}

func TestOneProcessWorksAQueueWhileAnotherTargetsQueueMovesBesideIt(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	runPipelineGit(t, f.repository, "branch", "release", "main")
	release := runstate.MergeQueueKey{Repository: queueKey.Repository, TargetBranch: "release"}
	f.admitAnother(release, "for-release", "feature.txt", "implemented for the release\n")
	// Another process is working main's queue.
	held, ok, err := f.queue.LeaseWorker(context.Background(), queueKey)
	if err != nil || !ok {
		t.Fatalf("LeaseWorker() = %t, %v", ok, err)
	}
	if pass := f.pass(queueKey); pass.Outcome != runstate.MergeQueuePassWaiting || !strings.Contains(pass.Says, "another process") {
		t.Fatalf("Pass(main) beside another worker = %#v, want it to leave the queue to that worker", pass)
	}
	if f.checks.calls() != 0 {
		t.Fatal("a pass without the queue's lease ran a check")
	}
	if pass := f.pass(release); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass(release) = %#v, want the other target's change landed while main's queue was held", pass)
	}
	if contains, err := f.lander.TargetHolds(context.Background(), "release", f.run.admittedRun.others[firstOther(f)].ReviewHeadCommit); err != nil || !contains {
		t.Fatalf("release holds its change: %t, %v", contains, err)
	}
	_ = held.Release()

	// Two passes of main's queue at once: one works it, the other leaves it.
	var wg sync.WaitGroup
	passes := make([]runstate.MergeQueuePass, 2)
	for index := range passes {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			passes[index] = f.pass(queueKey)
		}(index)
	}
	wg.Wait()
	landedBy := 0
	for _, pass := range passes {
		if pass.Outcome == runstate.MergeQueuePassLanded {
			landedBy++
		}
	}
	if landedBy != 1 {
		t.Fatalf("passes = %#v, want exactly one to have landed main's change", passes)
	}
	if completion := f.landing().Completion; completion == nil || !completion.Whole() || f.local("main") != completion.Landed {
		t.Fatalf("main's completion = %#v, want one landing", completion)
	}
	if f.checks.calls() != 2 || f.reviewer.calls() != 2 {
		t.Fatalf("checks ran %d times and reviews %d, want one of each per change", f.checks.calls(), f.reviewer.calls())
	}
}

func firstOther(f *promotionFixture) string {
	for runID := range f.run.admittedRun.others {
		return runID
	}
	return ""
}

func TestWhatWasAdmittedDrainsInItsModeAfterTheSwitchIsOffAndAcrossARestart(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	// The switch is turned off: nothing new is admitted, and what was admitted
	// is still worked.
	f.worker.Pipeline.Config.Execution.MergeQueue = false
	holds := f.worker.Pipeline.Holds.(*runstate.OperatorHoldStore)
	if _, err := holds.Hold(time.Now()); err != nil {
		t.Fatal(err)
	}
	if pass := f.pass(queueKey); pass.Outcome != runstate.MergeQueuePassWaiting || !strings.Contains(pass.Says, "paused") {
		t.Fatalf("Pass() under the operator's pause = %#v, want a wait", pass)
	}
	if _, _, err := holds.Release(); err != nil {
		t.Fatal(err)
	}
	// A restarted harness builds its driver afresh over the same records.
	restarted := MergeQueueDriver{Worker: f.worker, Promoter: f.promoter, Queues: f.queue}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if pass := restarted.Pass(ctx, queueKey); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() after the restart = %#v, want the change landed", pass)
	}
	entries, err := f.queue.Entries(queueKey)
	if err != nil || len(entries) != 1 || entries[0].Mode != runstate.MergeQueueHarness {
		t.Fatalf("entries = %#v, %v; want the one entry, in the mode it was admitted in", entries, err)
	}
	generations := f.generations()
	f.assertLandedOnce(generations[len(generations)-1])
}

func TestACheckTheTargetFailsAtItsBaseChargesTheChangeNothingAndStepsAside(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	f.worker.Pipeline.Config.Checks = []string{"test -f fixed.txt"}
	f.run.admittedRun.state.Branch = "change"
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	// The change behind the first is the one that makes the target pass.
	fix := f.admitAnother(queueKey, "fix", "fixed.txt", "fixed\n")
	for range 3 {
		if pass := f.pass(queueKey); pass.Outcome == runstate.MergeQueuePassStopped {
			t.Fatalf("Pass() = %#v", pass)
		}
	}
	first := f.generations()[0]
	if first.BaseCheck == nil || first.BaseCheck.Passed || first.BaseCheck.Command != "test -f fixed.txt" {
		t.Fatalf("first candidate's base check = %#v, want the target found failing the same check", first.BaseCheck)
	}
	if repair.count() != 0 || f.run.latest().RepairAttempts != 0 {
		t.Fatal("a check the target fails too was charged to the change")
	}
	fixed, found, err := f.queue.Landing(queueKey, fix.EntryID)
	if err != nil || !found || fixed.Completion == nil {
		t.Fatalf("the fix's landing = %#v, %v; want the change behind landed past the one waiting on the target", fixed, err)
	}
	// Once the target moved, the first change was built again on it and landed.
	completion := f.landing().Completion
	if completion == nil || !completion.Whole() {
		t.Fatalf("first completion = %#v, want it landed once the target passes", completion)
	}
	if pending := f.pending(); len(pending) != 0 {
		t.Fatalf("Pending() = %v, want nothing left", pending)
	}
}

func TestACandidateTouchingAnUngrantedProtectedPathIsRefusedBeforeItsChecks(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	f.worker.Waiting = func(entry runstate.MergeQueueEntry) bool { return entry.EntryID != f.entry.EntryID }
	protected := f.admitAnother(queueKey, "protected", ".yoyodyne/extra.yaml", "granted: everything\n")
	verification, err := f.work()
	if err != nil || verification.Entry.EntryID != protected.EntryID || verification.Verified || !strings.Contains(verification.Refusal, "protected paths") {
		t.Fatalf("Work() = %#v, %v; want the candidate refused for its protected path", verification, err)
	}
	if f.checks.calls() != 0 || f.reviewer.calls() != 0 {
		t.Fatal("a candidate the protected-path gate refused had its checks run or was reviewed")
	}
	generation := verification.Generation
	if generation.Paths == nil || len(generation.Paths.Refused) != 1 || generation.Paths.Refused[0] != ".yoyodyne/extra.yaml" {
		t.Fatalf("path evidence = %#v, want the ungranted path named", generation.Paths)
	}
	failure, failed, err := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{Generations: []runstate.MergeQueueGeneration{generation}, Configured: f.configured()})
	if err != nil || !failed || failure.Class != runstate.MergeQueueCandidateDefect {
		t.Fatalf("ClassifyMergeQueueFailure() = %#v, %v; want the change's defect", failure, err)
	}
}

func TestAHeadThatWillNotMergeOntoTheTargetIsHandedBackAsAConflict(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	f.run.admittedRun.state.Branch = "change"
	f.worker.Pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2
	f.promoter.Repair = MergeQueueRunHandback{Store: f.run, Tracker: f.tracker}
	// The target gains its own feature.txt, which the change also adds.
	writeQueueFile(t, f.repository, "feature.txt", "something else\n")
	runPipelineGit(t, f.repository, "add", "feature.txt")
	runPipelineGit(t, f.repository, "commit", "-m", "a different feature.txt")
	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflicting change handed back", pass)
	}
	handback := f.landing().Handback
	if handback == nil || handback.Conflict == nil || handback.HandedBackAt == nil || len(handback.Conflict.Paths) == 0 || handback.Conflict.Paths[0] != "feature.txt" {
		t.Fatalf("handback = %#v, want the conflict named and given to the run", handback)
	}
	run := f.run.latest()
	if run.ReplayConflict == nil || run.Status != runstate.StatusFailed || strings.TrimSpace(run.Blocker) == "" || run.ReplayConflict.TargetCommit != handback.TargetBase {
		t.Fatalf("run = %#v, want the conflict recorded where the repair continuation reads one", run)
	}
	if len(f.generations()) != 0 {
		t.Fatal("a head that would not merge left a candidate generation")
	}
}

func TestAHandbackNoRunCanTakeUpReachesTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	f, repair := failingFixture(t, 2, 0)
	// The run keeps no branch, so no repair can continue it.
	f.run.admittedRun.state.Branch = ""
	recovered, err := f.recover()
	if err != nil || recovered.Handback == nil || recovered.Handback.Continuation != runstate.MergeQueueMissingPrerequisite {
		t.Fatalf("Recover() = %#v, %v; want a handback no run can take up", recovered, err)
	}
	if handback := f.landing().Handback; handback.ReportedAt == nil || len(repair.reported) != 1 || repair.count() != 0 {
		t.Fatalf("handback = %#v with %d reported and %d given, want it put on the docket once", handback, len(repair.reported), repair.count())
	}
	if pending := f.pending(); len(pending) != 0 {
		t.Fatalf("Pending() = %v, want nothing owed once it is on the docket", pending)
	}
}

// slotQueues is a merge queue whose one pass waits until a run has started
// beside it, which can happen only if the pass holds no developer slot.
type slotQueues struct {
	begun, runStarted chan struct{}
	once              sync.Once
	passes            int
	mu                sync.Mutex
}

func (q *slotQueues) Pending() ([]runstate.MergeQueueKey, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.passes > 0 {
		return nil, nil
	}
	return []runstate.MergeQueueKey{queueKey}, nil
}

func (q *slotQueues) Pass(ctx context.Context, key runstate.MergeQueueKey) runstate.MergeQueuePass {
	q.mu.Lock()
	q.passes++
	q.mu.Unlock()
	q.once.Do(func() { close(q.begun) })
	select {
	case <-q.runStarted:
		return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassLanded, Says: "landed beside the run"}
	case <-time.After(30 * time.Second):
		return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassStopped, Says: "no run started beside the pass"}
	}
}

func TestASessionWorksTheMergeQueueBesideARunWithoutTakingItsSlot(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 1
	queues := &slotQueues{begun: make(chan struct{}), runStarted: make(chan struct{})}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		select {
		case <-queues.begun:
		case <-time.After(30 * time.Second):
			t.Error("the queue pass never began")
		}
		close(queues.runStarted)
		h.close(id)
		return Outcome{WorkItemID: id, Status: runstate.StatusSucceeded}, nil
	}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.MergeQueues = queues
		return pull, err
	}
	schedule, err := Scheduler{Open: open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || len(schedule.QueuePasses) != 1 || schedule.QueuePasses[0].Pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("schedule = %s with queue passes %#v; want the run started in the one slot while the queue pass was in flight", schedule.Render(), schedule.QueuePasses)
	}
}

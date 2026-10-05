package orchestrator

// The dispatch half of the founding case.
//
// Every recorded instance of a repair round being lost was a dispatch that
// started something fresh in place of the handback: a new run, a new worktree
// cut off the target branch, and a developer handed the reviewer's findings
// about a change that was not in front of it. The refusal in the fresh path
// catches that after the fact and reads a record to do it; these tests are the
// other end, where a repair says which run it means and gets an entry point that
// cannot start one.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What the item is for: a repair carried out reserves nothing and creates
// nothing. The run named goes on in the worktree it stopped in, and the
// repository has exactly the worktrees it had before the dispatch.
func TestARepairDispatchCreatesNoWorktree(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	docket := &memoryDocket{}

	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, docket)
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, stopped.RunID), 2, docketedNow, handbackCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	worktreesBefore := worktreeDirectories(t, worktreeRoot)
	runsBefore := runsRecordedFor(t, store, tracker.Item.ID)

	second := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)

	result, err := repairContinuerOver(t, continuing, store, docket, tracker).
		Continue(context.Background(), RepairContinueRequest{Run: stopped.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.Outcome.RunID != stopped.RunID {
		t.Fatalf("continued run = %s, want the stopped run %s continued", result.Outcome.RunID, stopped.RunID)
	}
	// The structural half: nothing cut a worktree, so there was no fresh one for
	// the handback to be delivered into by mistake. What the continued run then
	// removed on its way out is not this question, so what is measured is what
	// appeared rather than how many there are.
	if cut := worktreesCutSince(t, worktreeRoot, worktreesBefore); len(cut) != 0 {
		t.Fatalf("worktrees cut by the dispatch = %v, want none", cut)
	}
	if after := runsRecordedFor(t, store, tracker.Item.ID); after != runsBefore {
		t.Fatalf("recorded runs = %d, want the dispatch to have reserved none beyond the %d already recorded", after, runsBefore)
	}
}

// A repair dispatched to a run that is not going refuses rather than starting
// one in its place. This is the substitution itself, refused at the dispatch:
// the run was never made live, so there is nothing to continue, and the answer
// to that is never a fresh worktree off the target branch.
func TestARepairDispatchRefusesWhereTheRunItNamesIsNotInFlight(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	// Somebody puts the item back, which is what lets a fresh run past every
	// other gate. The run itself is still stopped: nothing re-entered it.
	tracker.Item.Status = "open"
	worktreesBefore := worktreeDirectories(t, worktreeRoot)

	fresh := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s, in place of the repair run %s is owed", request.WorkingDirectory, stopped.RunID)
		return nil
	}, approveVerdict)
	dispatching := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, fresh, []string{"exit 0"}), fresh)
	dispatching.NewRunID = runstate.NewRunID

	_, err := dispatching.Continue(context.Background(), tracker.Item.ID, stopped.RunID)
	if !errors.Is(err, ErrNoRunToContinue) {
		t.Fatalf("Continue() error = %v, want the dispatch refused for finding no run to continue", err)
	}
	// The mismatch is named, and so is the decision that does start the item over.
	for _, want := range []string{stopped.RunID, "no run is in flight", "yoyo triage rerun"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v, is missing %q", err, want)
		}
	}
	if len(fresh.Requests) != 0 {
		t.Fatalf("provider invocations = %d, want nothing spent", len(fresh.Requests))
	}
	inFlight, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(inFlight) != 0 {
		t.Fatalf("runs in flight = %#v, want the refusal to have reserved nothing", inFlight)
	}
	if cut := worktreesCutSince(t, worktreeRoot, worktreesBefore); len(cut) != 0 {
		t.Fatalf("worktrees cut by the refused dispatch = %v, want none", cut)
	}
}

// And a repair dispatched while a different run of the same item is going does
// not pick that one up. Continuing it would spend the grant that was decided
// about one change on another, so the run in flight is named and left alone.
func TestARepairDispatchRefusesADifferentRunInFlight(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	// The run that is actually going, made live exactly as a re-entry leaves one.
	reEnterAt(t, store, tracker, stopped.RunID, runstate.PhaseDeveloping)

	other := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s for a run the dispatch never named", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	dispatching := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, other, []string{"exit 0"}), other)

	decided, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	_, err = dispatching.Continue(context.Background(), tracker.Item.ID, decided)
	if !errors.Is(err, ErrNoRunToContinue) {
		t.Fatalf("Continue() error = %v, want the dispatch refused for naming a run that is not the one in flight", err)
	}
	var mismatch ContinuationMismatchError
	if !errors.As(err, &mismatch) || mismatch.InFlight != stopped.RunID {
		t.Fatalf("refusal = %v, want it to name %s as the run in flight", err, stopped.RunID)
	}
	if len(other.Requests) != 0 {
		t.Fatalf("provider invocations = %d, want nothing spent", len(other.Requests))
	}
	// The run that is going was left exactly as it was, so the decision about it
	// is still there to be carried out.
	untouched, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if untouched.Status != runstate.StatusRunning || untouched.Phase != runstate.PhaseDeveloping {
		t.Fatalf("run in flight = %s/%s, want the refusal to have left it running and developing", untouched.Status, untouched.Phase)
	}
}

// The third refusal: the run named is the one in flight, and it is not a repair
// loop. A continuation is dispatched about a run that was made live under a
// grant, so one that is going for any other reason is somebody else's, and
// spending this grant on it would be the same substitution one run further on.
func TestARepairDispatchRefusesARunThatIsNotAResumableRepair(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	// The run is live again, but at a phase no repair loop is re-entered at: this
	// is a run in the middle of being integrated rather than one owed an attempt.
	live, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	live.Status = runstate.StatusRunning
	live.Phase = runstate.PhaseIntegrating
	live.Blocker = ""
	live.CompletedAt = nil
	if err := store.Save(live); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s for a run that is not owed a repair attempt", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	dispatching := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	_, err = dispatching.Continue(context.Background(), tracker.Item.ID, stopped.RunID)
	if !errors.Is(err, ErrNoRunToContinue) {
		t.Fatalf("Continue() error = %v, want the dispatch refused for a run that is not a repair loop", err)
	}
	// The refusal says what it found rather than only that it refused, because
	// what to do about it depends on which of the three mismatches happened.
	if !strings.Contains(err.Error(), string(runstate.PhaseIntegrating)) {
		t.Fatalf("refusal = %v, want it to name the phase the run is actually at", err)
	}
	if len(provider.Requests) != 0 {
		t.Fatalf("provider invocations = %d, want nothing spent", len(provider.Requests))
	}
}

// The approval policy is deliberately not asked. A repair carried out under a
// policy that holds integration for a person is still a repair the development
// manager granted and the harness verified, and refusing it there would spend
// the grant to report that the run may not be integrated automatically —
// something the run reaches on its own and reports for itself. This pins that,
// because the check it leaves out is one a later reader could restore as a fix.
func TestARepairDispatchContinuesUnderAPolicyThatHoldsIntegration(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	reEnterAt(t, store, tracker, stopped.RunID, runstate.PhaseDeveloping)

	var handedTo backend.RunRequest
	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		handedTo = request
		return nil
	}, approveVerdict)
	// No `automatic`: integration is whatever the shared fixture configures,
	// which is a policy that does not integrate on the harness's own say-so.
	continuing := newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"})
	if continuing.Config.Approvals.Integration == domain.ApprovalAutomatic {
		t.Fatalf("integration policy = %q, want a fixture whose policy is not automatic", continuing.Config.Approvals.Integration)
	}

	_, err := continuing.Continue(context.Background(), tracker.Item.ID, stopped.RunID)
	// The two refusals a policy-conditioned re-entry would produce: `yoyo run`
	// under this policy reports the run in flight as somebody else's rather than
	// picking its repair loop up, and that is exactly what a dispatch must not do
	// to a run it was told to continue.
	var existing ExistingRunError
	if errors.As(err, &existing) {
		t.Fatalf("Continue() reported the run it was dispatched to as somebody else's: %v", err)
	}
	if errors.Is(err, ErrNoRunToContinue) {
		t.Fatalf("Continue() error = %v, want the repair re-entered rather than refused for the approval policy", err)
	}
	// What the criterion actually is: the developer was handed the preserved
	// worktree, which is the re-entry having happened at all.
	if handedTo.WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("the repair was handed %q, want the preserved worktree %q", handedTo.WorkingDirectory, stopped.WorktreePath)
	}
}

// The route that actually caused the loss, driven end to end: the scheduler
// pulling an item off the backlog whose last run stopped owing a repair. Its
// dispatch is `Pipeline.Run` — the same call `openPull` makes — so what refuses
// it is the fresh-run refusal, and this is the test that says so about the
// scheduler's own path rather than about the pipeline in isolation.
func TestTheSchedulersDispatchIsRefusedWhereARepairIsOwed(t *testing.T) {
	t.Parallel()

	harness := newRealScheduleHarness(t, 1, "yoyodyne-alpha")
	stopped := stopScheduledItemWithARepairOwed(t, harness, "yoyodyne-alpha")
	// Somebody puts the item back, which is what makes the scheduler pull it: an
	// item the tracker calls ready is one the backlog offers, and nothing about
	// the stoppage is visible in that offer.
	if _, err := harness.SetStatus("yoyodyne-alpha", "open"); err != nil {
		t.Fatalf("setStatus() error = %v", err)
	}
	harness.develop = func(workItemID, worktree string) error {
		t.Errorf("the scheduler put a developer to work on %s in %s, in place of the repair run %s is owed",
			workItemID, worktree, stopped.RunID)
		return nil
	}
	worktreesBefore := worktreeDirectories(t, harness.worktreeRoot)

	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %d, want the scheduler to have pulled the item once: %s", len(schedule.Started), schedule.Render())
	}
	// The pull reached the refusal and was turned away by it, naming the branch
	// the change is on and both decisions that act on it.
	started := schedule.Started[0]
	for _, want := range []string{stopped.Branch, "yoyo triage repair", "yoyo triage rerun"} {
		if !strings.Contains(started.Failure, want) {
			t.Fatalf("the scheduler's dispatch reported %q, want a substituted-handback refusal naming %q", started.Failure, want)
		}
	}
	// And it was turned away before anything was reserved or cut, so the stopped
	// run is still exactly what a repair would be carried out on.
	if cut := worktreesCutSince(t, harness.worktreeRoot, worktreesBefore); len(cut) != 0 {
		t.Fatalf("worktrees cut by the scheduler's dispatch = %v, want none", cut)
	}
	recorded, err := harness.store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].RunID != stopped.RunID {
		t.Fatalf("recorded runs = %#v, want only the stopped run %s", recorded, stopped.RunID)
	}
}

// stopScheduledItemWithARepairOwed runs one of the scheduler harness's items to
// a stoppage owing a repair, over the same repository, worktree root, and run
// state the scheduler's own dispatch will use. It is the ordinary stoppage: a
// reviewer that kept asking for repair until the budget was spent, with the
// change preserved on the run's branch.
func stopScheduledItemWithARepairOwed(t *testing.T, harness *realScheduleHarness, workItemID string) Outcome {
	t.Helper()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return writeHandbackChange(request.WorkingDirectory)
	}, repairVerdict)
	stopping := automatic(newSharedPipeline(t, harness.repository, harness.worktreeRoot, harness.store, harness, provider, []string{"exit 0"}), provider)
	stopping.NewRunID = runstate.NewRunID

	stopped, err := stopping.Run(context.Background(), workItemID)
	if err == nil {
		t.Fatal("Run() ended without stopping, so there is no repair for a dispatch to stand in for")
	}
	if !stopped.Blocked || stopped.Branch == "" {
		t.Fatalf("stopped run = %#v, want a blocked run with its change preserved on a branch", stopped)
	}
	return stopped
}

// worktreesCutSince is the worktrees that appeared while a dispatch was being
// made, which is the question these tests ask: a repair that cut one cut it for
// a change that already exists somewhere else.
func worktreesCutSince(t *testing.T, worktreeRoot string, before []string) []string {
	t.Helper()
	existing := make(map[string]bool, len(before))
	for _, name := range before {
		existing[name] = true
	}
	var cut []string
	for _, name := range worktreeDirectories(t, worktreeRoot) {
		if !existing[name] {
			cut = append(cut, name)
		}
	}
	return cut
}

// worktreeDirectories is what the repository's worktree root actually holds,
// which is how these tests ask whether a dispatch cut a fresh one.
func worktreeDirectories(t *testing.T, worktreeRoot string) []string {
	t.Helper()
	entries, err := os.ReadDir(worktreeRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	var directories []string
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}
	return directories
}

// runsRecordedFor is how many runs the store holds for one work item, which is
// how these tests ask whether a dispatch reserved one.
func runsRecordedFor(t *testing.T, store *runstate.Store, workItemID string) int {
	t.Helper()
	runs, err := store.Runs(workItemID)
	if err != nil {
		t.Fatalf("Runs() error = %v", err)
	}
	return len(runs)
}

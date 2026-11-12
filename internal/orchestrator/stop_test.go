package orchestrator

// What the operator's stop does to a run they did not start, and what holding
// intake does to work they did not ask for. The two are deliberately different:
// a stop ends one run and leaves its artifacts for reconciliation, and a hold on
// intake ends nothing at all.

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
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A stop recorded while the developer is working reaches the run at its next
// step — before the checks — and ends it there, so neither the checks nor the
// review are spent on it. An attempt that finishes inside the stop's bound is
// not interrupted, because throwing away a generation that has been paid for is
// the cost that made killing processes the wrong verb (promptstop.go).
func TestARunStopsAtItsNextProviderCallWhenTheOperatorAsksIt(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		// The operator stops the run mid-attempt, from a process that holds
		// nothing: this is the file they write beside the run.
		if err := store.RecordStop(runstate.StopRequest{
			SchemaVersion: runstate.StopSchemaVersion,
			ProductID:     "yoyodyne",
			RunID:         request.RunID,
			WorkItemID:    "yoyodyne-task",
			RequestedAt:   baseTime,
			Reason:        "it is rewriting the wrong file",
		}); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.StopOperator)
	if outcome.StopClass != runstate.StopOperator {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.StopOperator)
	}
	if err == nil {
		t.Fatal("Run() error = nil, want the stop reported as what ended the run")
	}
	if !strings.Contains(err.Error(), "the operator stopped this run") ||
		!strings.Contains(err.Error(), "it is rewriting the wrong file") {
		t.Fatalf("Run() error = %v, want it to name the stop and the reason given", err)
	}
	if outcome.Status != runstate.StatusCancelled {
		t.Fatalf("status = %q, want a stopped run recorded as cancelled", outcome.Status)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a stopped run promoted its work: %#v (closed=%t)", outcome.Integration, tracker.Closed)
	}
	// Neither the checks nor the reviewer ran. Spending them on a change nobody
	// is going to take is exactly what stopping at the boundary avoids, and a
	// stop that waited for them is one that cancels finished work.
	if reviews := countRoleRequests(provider.Requests, "reviewer"); reviews != 0 {
		t.Fatalf("reviewer invocations = %d, want the stop to have landed before the review", reviews)
	}
	if len(outcome.Checks) != 0 {
		t.Fatalf("checks = %#v, want the stop to have landed before the checks", outcome.Checks)
	}
	if !strings.Contains(err.Error(), "at the run's next step") {
		t.Fatalf("Run() error = %v, want it to say the stop took effect at the next step", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.Status.Terminal() || stopped.CompletedAt == nil {
		t.Fatalf("stopped state = %#v, want a terminal run", stopped)
	}
	// The artifacts are left exactly where a run cancelled in its own process
	// leaves them, which is what lets the same reconciliation settle both.
	if stopped.WorktreeRemoved || stopped.BranchRemoved {
		t.Fatalf("stopped state = %#v, want the artifacts preserved for settling", stopped)
	}
	if _, err := os.Stat(stopped.WorktreePath); err != nil {
		t.Fatalf("the stopped run's worktree did not survive: %v", err)
	}
	if !strings.Contains(tracker.Notes, "the operator stopped this run") {
		t.Fatalf("the work item was not told why it stopped: %q", tracker.Notes)
	}
}

// A run whose process exited before it reached a boundary is picked up by a
// later invocation. Without this, a stop the operator asked for would be
// forgotten by exactly the restart that was meant to be safe. The run's check
// fails, so it parks before its repair attempt with no change that has passed
// its checks; one parked after they passed keeps its change instead
// (TestAStopFoundOnResumingARunWhoseChecksPassedKeepsTheChange).
func TestAStopAlreadyAskedForEndsAResumedRunRatherThanContinuingIt(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	holds := newOperatorHoldStore(t)
	// A hold is the simplest way to leave a run in flight with a process that has
	// gone: the run parks, records why, and the invocation returns.
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if _, err := holds.Hold(baseTime); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 1"}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, 0)
	firstPipeline.Holds = holds

	parked, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("parked Run() error = %v", err)
	}
	if !parked.Paused || parked.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a parked run still in flight", parked)
	}
	if held, err := store.Load(parked.RunID); err != nil || held.ChecksPassed != nil || held.Phase != runstate.PhaseDeveloping {
		t.Fatalf("parked run = phase %q, checks passed %#v (%v), want it parked before its repair attempt", held.Phase, held.ChecksPassed, err)
	}

	// The operator stops the parked run and lifts the pause. Only one of those
	// decisions is about this item, and the run has to honor that one.
	if err := store.RecordStop(runstate.StopRequest{
		SchemaVersion: runstate.StopSchemaVersion,
		ProductID:     "yoyodyne",
		RunID:         parked.RunID,
		WorkItemID:    tracker.Item.ID,
		RequestedAt:   baseTime.Add(time.Hour),
		Reason:        "we are doing something else first",
	}); err != nil {
		t.Fatalf("RecordStop() error = %v", err)
	}
	if _, _, err := holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	second := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	secondPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	secondPipeline.Holds = holds
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("resumed Run() error = nil, want the stop honored")
	}
	if outcome.RunID != parked.RunID || outcome.Status != runstate.StatusCancelled {
		t.Fatalf("resumed run = %#v, want the parked run %s ended as cancelled", outcome, parked.RunID)
	}
	// Nothing was asked of the provider. A run somebody stopped must not spend
	// one more invocation on its way to being stopped.
	if len(second.Requests) != 0 {
		t.Fatalf("the provider was invoked resuming a stopped run: %#v", second.Requests)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a stopped run promoted its work: %#v (closed=%t)", outcome.Integration, tracker.Closed)
	}
}

// A run parked after its checks passed, with a stop recorded while it waited,
// is resumed with its change kept for its review rather than cancelled: the stop
// reached it only after the checks passed, and the run says it arrived late.
func TestAStopFoundOnResumingARunWhoseChecksPassedKeepsTheChange(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	holds := newOperatorHoldStore(t)
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if _, err := holds.Hold(baseTime); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, 0)
	firstPipeline.Holds = holds

	parked, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !parked.Paused {
		t.Fatalf("parked Run() = %#v, %v, want a parked run", parked, err)
	}
	if held, err := store.Load(parked.RunID); err != nil || held.ChecksPassed == nil {
		t.Fatalf("parked run's checks passed = %#v (%v), want it parked at its review", held.ChecksPassed, err)
	}
	if err := store.RecordStop(runstate.StopRequest{
		SchemaVersion: runstate.StopSchemaVersion,
		ProductID:     "yoyodyne",
		RunID:         parked.RunID,
		WorkItemID:    tracker.Item.ID,
		RequestedAt:   baseTime.Add(time.Hour),
		Reason:        "we are doing something else first",
	}); err != nil {
		t.Fatalf("RecordStop() error = %v", err)
	}
	if _, _, err := holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	second := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	secondPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	secondPipeline.Holds = holds
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v, want the kept change reviewed", err)
	}
	if outcome.RunID != parked.RunID || outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("resumed run = %#v, want the parked run's change reviewed and integrated", outcome)
	}
	if outcome.LateStop == nil || !strings.Contains(tracker.Notes, "Stop arrived late: the operator asked this run to stop") {
		t.Fatalf("late stop = %#v, notes = %q, want the stop said to have arrived late", outcome.LateStop, tracker.Notes)
	}
}

// Holding intake stops the harness choosing work and nothing else. The item is
// left exactly where it was, so lifting the hold is all that stands between here
// and the work starting.
func TestHeldIntakeStartsNothingTheHarnessChose(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	intake := newIntakeHoldStore(t)
	pipeline.Intake = intake
	pipeline.Selection = runstate.Selection{
		By:     runstate.SelectedByDevelopmentManager,
		Reason: "the highest-priority admitted item nothing was holding back",
		At:     baseTime,
	}
	heldAt := baseTime
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue looks wrong", heldAt); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a pause rather than a failure", err)
	}
	if !outcome.Paused || outcome.PausedByIntake == nil {
		t.Fatalf("outcome = %#v, want work the harness declined to choose", outcome)
	}
	if !outcome.PausedByIntake.HeldAt.Equal(heldAt.UTC()) || outcome.PausedByIntake.Reason != "the queue looks wrong" {
		t.Fatalf("intake hold = %#v, want when it was placed and why", outcome.PausedByIntake)
	}
	if outcome.RunID != "" || outcome.WorktreePath != "" {
		t.Fatalf("outcome = %#v, want nothing reserved behind a held intake", outcome)
	}
	if tracker.Claimed {
		t.Fatalf("a held intake claimed the work item: calls=%v", tracker.Calls)
	}
	if len(provider.Requests) != 0 {
		t.Fatalf("the provider was invoked while intake was held: %#v", provider.Requests)
	}
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("runs in flight = %d, want a held intake to have reserved nothing", len(incomplete))
	}
}

// The hold is on what the harness chooses, not on the operator. An item they
// name runs while it stands, which is the distinction between this and the pause
// over everything — and it is also how they inspect the thing that worried them.
func TestHeldIntakeStillRunsAnItemTheOperatorNames(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	intake := newIntakeHoldStore(t)
	pipeline.Intake = intake
	pipeline.Selection = runstate.OperatorSelection("the operator ran this item by name", baseTime)
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue looks wrong", baseTime); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PausedByIntake != nil || outcome.RunID == "" {
		t.Fatalf("outcome = %#v, want the operator's own item run despite the hold", outcome)
	}
	// The reason is recorded with the run, which is what makes a survey able to
	// say why anything is in flight rather than only that it is.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Selection == nil {
		t.Fatal("the run recorded nothing about why it was started")
	}
	if state.Selection.By != runstate.SelectedByOperator ||
		state.Selection.Reason != "the operator ran this item by name" {
		t.Fatalf("selection = %#v, want who chose the work and why", state.Selection)
	}
	if state.Selection.At.IsZero() {
		t.Fatalf("selection = %#v, want the moment the choice was made", state.Selection)
	}
}

// The one harness selection a held intake lets through is the brake's own
// probe, and only where the hold's own record names the item as the probe it
// has in flight. A selection that merely says it is the probe is refused like
// any other harness selection: the record is what is read, because the brake
// wrote it under its lock and a selection is words any caller could supply.
func TestHeldIntakeLetsOnlyTheRecordedProbeThrough(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	intake := newIntakeHoldStore(t)
	pipeline.Intake = intake
	pipeline.Selection = runstate.Selection{By: runstate.SelectedByBrake, Reason: "the intake brake's probe run", At: baseTime}
	trip := runstate.IntakeBrake{
		Blocked:        []runstate.BrakeBlockedRun{{RunID: "run-1", WorkItemID: "yoyodyne-other", Reason: "review required repair"}},
		CooldownEndsAt: baseTime.Add(30 * time.Minute),
	}
	if _, err := intake.Brake(trip, "3 run(s) blocked in a row", baseTime); err != nil {
		t.Fatalf("Brake() error = %v", err)
	}

	// The hold names no probe yet, so a selection claiming to be one is refused.
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a pause rather than a failure", err)
	}
	if !outcome.Paused || outcome.PausedByIntake == nil || tracker.Claimed {
		t.Fatalf("outcome = %#v (claimed=%t), want a self-declared probe refused by the hold", outcome, tracker.Claimed)
	}
	// The hold names another item as its probe: still refused.
	if _, err := intake.ReviseBrake(func(brake *runstate.IntakeBrake) error {
		brake.Probe = &runstate.IntakeProbe{WorkItemID: "yoyodyne-other", StartedAt: baseTime}
		return nil
	}); err != nil {
		t.Fatalf("ReviseBrake() error = %v", err)
	}
	outcome, err = pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a pause rather than a failure", err)
	}
	if !outcome.Paused || tracker.Claimed {
		t.Fatalf("outcome = %#v (claimed=%t), want a probe of another item refused for this one", outcome, tracker.Claimed)
	}
	// The hold names this item as its probe: the run goes through, with the
	// brake recorded as what chose it.
	if _, err := intake.ReviseBrake(func(brake *runstate.IntakeBrake) error {
		brake.Probe = &runstate.IntakeProbe{WorkItemID: tracker.Item.ID, StartedAt: baseTime}
		return nil
	}); err != nil {
		t.Fatalf("ReviseBrake() error = %v", err)
	}
	outcome, err = pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PausedByIntake != nil || outcome.RunID == "" {
		t.Fatalf("outcome = %#v, want the recorded probe run despite the hold", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Selection == nil || state.Selection.By != runstate.SelectedByBrake {
		t.Fatalf("selection = %#v, want the intake brake recorded as what chose the probe", state.Selection)
	}
}

// A pipeline that says nothing about why it is running an item records nothing,
// rather than a selection with an empty reason. The two read very differently to
// whoever is deciding whether to trust what the harness is doing.
func TestARunWithNoStatedSelectionRecordsNoReason(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Selection != nil {
		t.Fatalf("selection = %#v, want nothing recorded rather than an empty reason", state.Selection)
	}
}

// countRoleRequests counts the invocations made of one role, so a test can say
// which provider calls a stop landed in front of.
func countRoleRequests(requests []backend.RunRequest, role string) int {
	counted := 0
	for _, request := range requests {
		if string(request.Role) == role {
			counted++
		}
	}
	return counted
}

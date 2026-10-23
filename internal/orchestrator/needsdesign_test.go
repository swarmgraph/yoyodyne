package orchestrator

// The needs-design label (docs/designs/needs-design-gate.md): an item carrying
// it is held from every start the harness chooses, exactly as a parked item is;
// a run already going on it carries on; a run the operator names is not held,
// and says so; and the item starts once the label is removed.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The pull passes a labelled item over in the queue's own words, however far
// the queue drains, and starts it at the first pull after the label comes off.
func TestSchedulerStartsANeedsDesignItemOnlyOnceTheLabelIsRemoved(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID: "yoyodyne-ifd.431.5", Title: "Atomic JSON replacement",
		Status: "open", Priority: 0, Labels: []string{"reliability", domain.NeedsDesignLabel},
	}
	harness := newScheduleHarness(item)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want an item labelled needs-design never selected: %s", schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != item.ID {
		t.Fatalf("deferred = %#v, want the labelled item named rather than counted among the unready", schedule.Deferred)
	}
	reason := schedule.Deferred[0].Reason
	for _, required := range []string{domain.NeedsDesignLabel, "architect's design", "however far the queue drains", "a run the operator names is not held"} {
		if !strings.Contains(reason, required) {
			t.Fatalf("deferred reason = %q, want it to contain %q", reason, required)
		}
	}
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("backlog = %d admitted, %d pullable, want it queued and unpullable", schedule.Admitted, schedule.Pullable)
	}

	// The architect removes the label, and the next pull takes the item.
	harness.mu.Lock()
	harness.Items[0].Labels = []string{"reliability"}
	harness.mu.Unlock()
	schedule, err = Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != item.ID {
		t.Fatalf("started = %#v, want the item pulled once its label is removed: %s", schedule.Started, schedule.Render())
	}
}

// The class a passed-over labelled item is counted in is its own, after a
// parking and ahead of a hold, so the architect is named as the one to move.
func TestANeedsDesignItemIsPassedOverAsWaitingOnTheArchitectsDesign(t *testing.T) {
	t.Parallel()

	queue := backlog.Order([]beads.WorkItem{{ID: "item", Title: "Item", Status: "open", Labels: []string{domain.NeedsDesignLabel}}},
		[]string{"item"}, backlog.ReadHolds(nil), nil)
	entry := queue.Entries[0]
	if class := unreadyClass(entry); class != runstate.PassedOverWaitingOnDesign {
		t.Fatalf("class = %q, want %q", class, runstate.PassedOverWaitingOnDesign)
	}
	entry.Parking = "deferred by the scope decision"
	if class := unreadyClass(entry); class != runstate.PassedOverParked {
		t.Fatalf("class = %q, want a parking that would still hold once the label came off named first", class)
	}
}

// A label applied between the pull choosing an item and its run being reserved
// is a start that never became a run, not a failure: nothing is docketed, and
// the exclusion says which it was.
func TestALabelAppliedAfterThePullChoseTheItemDeclinesTheStart(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.run = func(_ *scheduleHarness, id string) (Outcome, error) {
		return Outcome{}, NeedsDesignError{WorkItemID: id, SelectedBy: runstate.SelectedByScheduler}
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the one chosen item reported", schedule.Started)
	}
	started := schedule.Started[0]
	if started.Failure != "" || !strings.Contains(started.Declined, domain.NeedsDesignLabel) {
		t.Fatalf("started = %#v, want a declined start naming the label rather than a failure", started)
	}
	if because := excludedBecause(started); !strings.Contains(because, "did not start it") || strings.Contains(because, "another process") {
		t.Fatalf("excluded because %q, want it to say the session did not start it, not that another process took it", because)
	}
}

// Every selection the harness makes is refused at the pipeline's door, before
// anything is reserved, claimed, or asked of a provider.
func TestThePipelineStartsNoNeedsDesignItemTheHarnessChose(t *testing.T) {
	t.Parallel()

	for _, by := range []string{runstate.SelectedByScheduler, runstate.SelectedByDevelopmentManager, runstate.SelectedByBrake, ""} {
		repository := pipelineRepository(t)
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open", Labels: []string{domain.NeedsDesignLabel}}}
		provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
		pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
		pipeline.Selection = runstate.Selection{By: by, Reason: "the highest-priority admitted item nothing was holding back", At: baseTime}

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		var design NeedsDesignError
		if !errors.As(err, &design) {
			t.Fatalf("selected by %q: Run() error = %v, want the needs-design refusal", by, err)
		}
		if !strings.Contains(err.Error(), domain.NeedsDesignLabel) {
			t.Fatalf("selected by %q: refusal %q does not name the label", by, err)
		}
		if outcome.RunID != "" || tracker.Claimed || len(provider.Requests) != 0 {
			t.Fatalf("selected by %q: outcome = %#v, claimed = %v, requests = %d, want nothing reserved, claimed or asked", by, outcome, tracker.Claimed, len(provider.Requests))
		}
		incomplete, err := store.Incomplete()
		if err != nil {
			t.Fatalf("Incomplete() error = %v", err)
		}
		if len(incomplete) != 0 {
			t.Fatalf("selected by %q: runs in flight = %d, want none", by, len(incomplete))
		}
	}
}

// Naming the item is the operator overriding the hold, and the run records that
// it did.
func TestAnOperatorNamedRunOfANeedsDesignItemRunsAndRecordsTheOverride(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open", Labels: []string{domain.NeedsDesignLabel}}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Selection = runstate.OperatorSelection("the operator ran this item by name", baseTime)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.RunID == "" || !tracker.Claimed {
		t.Fatalf("outcome = %#v, want the operator's own item run despite the label", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Selection == nil || state.Selection.By != runstate.SelectedByOperator ||
		!strings.HasPrefix(state.Selection.Reason, "the operator ran this item by name") ||
		!strings.Contains(state.Selection.Reason, "overrides that hold") {
		t.Fatalf("selection = %#v, want the operator's reason with the override recorded beside it", state.Selection)
	}
}

// Labelling an item while its run is in flight blocks only new starts: the
// run, picked up again by a later dispatch the harness chose, carries on to
// its end.
func TestLabellingAnItemWithARunInFlightLeavesTheRunAlone(t *testing.T) {
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
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, 0)
	firstPipeline.Holds = holds
	firstPipeline.Selection = runstate.Selection{By: runstate.SelectedByScheduler, Reason: "next in the order", At: baseTime}

	parked, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("parked Run() error = %v", err)
	}
	if !parked.Paused || parked.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a parked run still in flight", parked)
	}

	// The item is labelled while its run is in flight, and the hold lifted.
	tracker.Item.Labels = []string{domain.NeedsDesignLabel}
	if _, _, err := holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	second := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	secondPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	secondPipeline.Holds = holds
	secondPipeline.Selection = runstate.Selection{By: runstate.SelectedByScheduler, Reason: "re-adopted the run in flight", At: baseTime}
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v, want the run in flight carried on", err)
	}
	if outcome.RunID != parked.RunID || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("resumed run = %#v, want the parked run %s carried to its end", outcome, parked.RunID)
	}
}

// A re-run the development manager decided is a fresh start the harness
// chooses, so the label refuses it before the claim and the stoppage keeps its
// re-run for once the label is removed.
func TestARerunOfANeedsDesignItemWaitsForTheLabelToBeRemoved(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Labels = []string{domain.NeedsDesignLabel}
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil {
		t.Fatal("Rerun() started an item labelled needs-design")
	}
	for _, want := range []string{domain.NeedsDesignLabel, "architect's design", "keeps its re-run"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatal("a re-run refused on the label spent the stoppage's claim")
	}

	harness.item.Labels = nil
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() after the label came off error = %v", err)
	}
	if !result.Started || len(harness.started) != 1 {
		t.Fatalf("result = %#v, started = %#v, want the decision carried out once the label is removed", result, harness.started)
	}
}

package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// harnessContinuations is what a pull continues a paused run through, over the
// real harness: its tracker for the item and its store for a stop.
type harnessContinuations struct {
	harness *realScheduleHarness
}

func (c harnessContinuations) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	return c.harness.Show(ctx, id)
}

func (c harnessContinuations) StopRequested(runID string) (runstate.StopRequest, bool, error) {
	return c.harness.store.StopRequested(runID)
}

// continuationFixture is one item whose run paused on a dependency, over the
// real harness, with the starts the pulls make counted and, where a test asks,
// refused the way a full harness refuses one.
type continuationFixture struct {
	harness   *realScheduleHarness
	scheduler Scheduler
	paused    runstate.State

	mu      sync.Mutex
	starts  int
	refuse  int
	refused int
}

func (f *continuationFixture) start(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
	f.mu.Lock()
	f.starts++
	refuse := f.refused < f.refuse
	if refuse {
		f.refused++
	}
	f.mu.Unlock()
	if refuse {
		return Outcome{}, runstate.CapacityError{Limit: 1, Active: 1}
	}
	return f.harness.start(ctx, workItemID, selection)
}

func (f *continuationFixture) startsMade() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// pausedOnADependency runs one item through a pull whose developer links it
// behind unfinished work, so the run pauses at the gate with its change in its
// worktree, its item claimed, and no process behind it.
func pausedOnADependency(t *testing.T) *continuationFixture {
	t.Helper()
	harness := newRealScheduleHarness(t, 1, "yoyodyne-task")
	developed := 0
	develop := harness.develop
	harness.develop = func(workItemID, worktree string) error {
		harness.mu.Lock()
		developed++
		first := developed == 1
		harness.mu.Unlock()
		if first {
			if err := harness.AddBlocker(context.Background(), workItemID, "yoyodyne-blocker"); err != nil {
				return err
			}
		}
		return develop(workItemID, worktree)
	}
	fixture := &continuationFixture{harness: harness}
	fixture.scheduler = Scheduler{Open: func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Continuations = harnessContinuations{harness: harness}
		pull.Start = fixture.start
		return pull, err
	}}

	schedule, err := fixture.scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || !schedule.Started[0].Outcome.Paused || schedule.Started[0].Outcome.PausedByDependency == nil {
		t.Fatalf("first pass = %s, want the one item started and paused on its dependency", schedule.Render())
	}
	paused, err := harness.store.Load(schedule.Started[0].Outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !pausedForDependency(paused) {
		t.Fatalf("run after the first pass = %#v, want it paused on the dependency", paused)
	}
	// The pull after the run paused found it still waiting, and said on what.
	if !deferredSaying(schedule, "yoyodyne-task", "waits on unfinished work: yoyodyne-blocker") {
		t.Fatalf("first pass = %#v, want the paused run passed over naming what it waits on", schedule.Deferred)
	}
	fixture.paused = paused
	fixture.starts = 0
	return fixture
}

func deferredSaying(schedule Schedule, workItemID, says string) bool {
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID == workItemID && strings.Contains(deferred.Reason, says) {
			return true
		}
	}
	return false
}

func closeBlocker(harness *realScheduleHarness, workItemID string) {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	for index := range harness.Items {
		if harness.Items[index].ID != workItemID {
			continue
		}
		for dependency := range harness.Items[index].Dependencies {
			harness.Items[index].Dependencies[dependency].Status = "closed"
		}
	}
}

// continuedNotes is the notes a run wrote saying its dependency pause lifted.
func continuedNotes(harness *realScheduleHarness) []string {
	var continued []string
	for _, note := range harness.RecordedNotes() {
		if strings.HasPrefix(note, "Continued ") {
			continued = append(continued, note)
		}
	}
	return continued
}

// A run paused on work its item waits on is continued by the next pull once
// that work has closed, in its own worktree and session, with nobody typing
// `yoyo run`. Its item stays claimed while it waits, so no queue ever offers
// it again, and before the watch came to continue these runs
// (yoyodyne-ifd.428.51) that is where it stayed.
func TestAPullContinuesARunPausedOnADependencyOnceItCloses(t *testing.T) {
	t.Parallel()

	fixture := pausedOnADependency(t)
	harness, paused := fixture.harness, fixture.paused

	// While the work it waits on is open, a pull passes it over and leaves it.
	waiting, err := fixture.scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() while waiting error = %v", err)
	}
	if len(waiting.Started) != 0 || !deferredSaying(waiting, "yoyodyne-task", "yoyodyne-blocker") {
		t.Fatalf("pass while waiting = %s (%#v), want nothing started and the paused run named with what it waits on", waiting.Render(), waiting.Deferred)
	}

	closeBlocker(harness, "yoyodyne-task")
	schedule, err := fixture.scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("pass after the dependency closed = %s, want the paused run continued", schedule.Render())
	}
	continued := schedule.Started[0]
	if continued.Outcome.RunID != paused.RunID {
		t.Fatalf("continued run = %q, want the paused run %q rather than a new one", continued.Outcome.RunID, paused.RunID)
	}
	if continued.Failure != "" || continued.Declined != "" || continued.Outcome.Paused || continued.Outcome.Integration == nil {
		t.Fatalf("continued run = %#v, want it finished and integrated", continued)
	}
	if !strings.Contains(continued.Reason, "continuing it in its own worktree and developer session") {
		t.Fatalf("reason = %q, want the continuation said", continued.Reason)
	}
	item, err := harness.Show(context.Background(), "yoyodyne-task")
	if err != nil || item.Status != "closed" {
		t.Fatalf("item after the continued run = %#v, %v; want it closed", item, err)
	}
	notes := continuedNotes(harness)
	if len(notes) != 1 || !strings.Contains(notes[0], "Continued by the harness") || !strings.Contains(notes[0], paused.RunID) {
		t.Fatalf("continuation notes = %q, want the continuation recorded on the item once, naming the run", notes)
	}
	finished, err := harness.store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.DependencyPause != nil || finished.Status != runstate.StatusSucceeded {
		t.Fatalf("run after the continuation = %#v, want it succeeded with its pause lifted", finished)
	}
}

// A continuation the start refuses writes nothing on the item and is not
// attempted again at the next poll: the session remembers it and leaves it for
// the retry interval. Before, every poll re-dispatched it and re-noted the item.
func TestARefusedContinuationIsNotRetriedAtEveryPull(t *testing.T) {
	t.Parallel()

	fixture := pausedOnADependency(t)
	closeBlocker(fixture.harness, "yoyodyne-task")
	fixture.refuse = 1

	now := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC)
	scheduler := fixture.scheduler
	scheduler.Now = func() time.Time { return now }
	// A drain reads again after every run it collects, so a continuation nothing
	// paced would be dispatched over and over within this one pass.
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := fixture.startsMade(); starts != 1 {
		t.Fatalf("starts = %d (%s), want the refused continuation attempted once", starts, schedule.Render())
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Declined == "" {
		t.Fatalf("pass = %s, want the one continuation recorded as declined", schedule.Render())
	}
	if !deferredSaying(schedule, "yoyodyne-task", "is attempted again from 2026-09-27T18:15:00Z") {
		t.Fatalf("deferred = %#v, want the refusal and when it is retried named", schedule.Deferred)
	}
	if notes := continuedNotes(fixture.harness); len(notes) != 0 {
		t.Fatalf("continuation notes = %q, want nothing written for a continuation that never went on", notes)
	}
	left, err := fixture.harness.store.Load(fixture.paused.RunID)
	if err != nil || !pausedForDependency(left) {
		t.Fatalf("run = %#v, %v; want it still paused", left, err)
	}

	// A later session with nothing refusing continues it.
	again, err := fixture.scheduler.Schedule(context.Background())
	if err != nil || len(again.Started) != 1 || again.Started[0].Outcome.Integration == nil {
		t.Fatalf("later pass = %s, %v; want the run continued", again.Render(), err)
	}
}

// Picking a paused run back up is the harness choosing what to spend a slot
// on, so a held intake stops it exactly as it stops a recorded repair, and the
// first pull after the hold lifts continues it.
func TestAHeldIntakeHoldsAContinuationUntilItLifts(t *testing.T) {
	t.Parallel()

	fixture := pausedOnADependency(t)
	closeBlocker(fixture.harness, "yoyodyne-task")
	if _, err := fixture.harness.intake.Hold(runstate.IntakeHolderOperator, "looking at the line", time.Now().UTC()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	held, err := fixture.scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() under the hold error = %v", err)
	}
	if len(held.Started) != 0 || fixture.startsMade() != 0 {
		t.Fatalf("pass under the hold = %s, want nothing continued", held.Render())
	}

	if _, _, err := fixture.harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	released, err := fixture.scheduler.Schedule(context.Background())
	if err != nil || len(released.Started) != 1 || released.Started[0].Outcome.RunID != fixture.paused.RunID || released.Started[0].Outcome.Integration == nil {
		t.Fatalf("pass after the hold lifted = %s, %v; want the paused run continued", released.Render(), err)
	}
}

// A stop somebody recorded on the paused run is honoured before any
// continuation: the pull does not pick the run up again even once the work it
// waited on has closed, and leaves it for the sweep to end.
func TestAPullDoesNotContinueAPausedRunSomebodyStopped(t *testing.T) {
	t.Parallel()

	fixture := pausedOnADependency(t)
	harness, paused := fixture.harness, fixture.paused
	if err := harness.store.RecordStop(runstate.StopRequest{
		SchemaVersion: runstate.StopSchemaVersion,
		ProductID:     "yoyodyne",
		RunID:         paused.RunID,
		WorkItemID:    paused.WorkItemID,
		RequestedAt:   time.Now().UTC(),
		RequestedBy:   "the development manager",
		Reason:        "another item does this work",
		Decision:      runstate.TriageDecisionStop,
	}); err != nil {
		t.Fatalf("RecordStop() error = %v", err)
	}
	closeBlocker(harness, "yoyodyne-task")

	schedule, err := fixture.scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 || fixture.startsMade() != 0 {
		t.Fatalf("pass = %s, want the stopped run not continued", schedule.Render())
	}
	if !deferredSaying(schedule, "yoyodyne-task", "the development manager asked for it to stop") {
		t.Fatalf("deferred = %#v, want the stop named", schedule.Deferred)
	}
	if notes := continuedNotes(harness); len(notes) != 0 {
		t.Fatalf("continuation notes = %q, want no continuation recorded", notes)
	}
	left, err := harness.store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !pausedForDependency(left) {
		t.Fatalf("run = %#v, want it left paused for the sweep to end", left)
	}

	// And the sweep ends it at once, as the stop asked: the dependency case left
	// the park-settlement rule, and the stop is read ahead of that rule.
	sweep := Reconciler{Tracker: harness, Worktrees: newObserver(t, harness.repository, harness.worktreeRoot), Store: harness.store}
	results, err := sweep.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].RunID != paused.RunID || results[0].Action != ActionCancelled {
		t.Fatalf("reconciliation = %#v, want the stopped paused run ended cancelled", results)
	}
	ended, err := harness.store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ended.Status != runstate.StatusCancelled || ended.DependencyPause != nil {
		t.Fatalf("run after the sweep = %#v, want it cancelled with its pause cleared", ended)
	}
	if _, err := os.Stat(filepath.Join(ended.WorktreePath, "yoyodyne-task.txt")); err != nil {
		t.Fatalf("the stopped run's change is not where it was left: %v", err)
	}
}

// A paused run the pass could not read for continuing is said on the pass,
// not only in its JSON.
func TestAContinuationProblemIsSaidOnThePass(t *testing.T) {
	t.Parallel()
	rendered := Schedule{ContinuationProblem: "read what yoyodyne-task waits on: the tracker did not answer"}.Render()
	if !strings.Contains(rendered, "was not continued: read what yoyodyne-task waits on") {
		t.Fatalf("the pass reads:\n%s\nwant the continuation problem said", rendered)
	}
}

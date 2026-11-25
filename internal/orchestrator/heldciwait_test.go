package orchestrator

// The hold on new work, the operator's pause, and a stop, each met by a run that
// waits for GitHub's checks.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func operatorsHold(at time.Time) runstate.IntakeHold {
	return runstate.IntakeHold{SchemaVersion: runstate.IntakeHoldSchemaVersion, ProductID: "yoyodyne", HeldAt: at, HeldBy: runstate.IntakeHolderOperator, Reason: "only what I name"}
}

// With the hold standing, a pull continues the run the operator started by name
// once GitHub has decided, and leaves one the scheduler chose and one recorded
// with no choice at all; the operator's pause leaves all three, with or without
// the hold.
func TestUnderTheHoldOnlyARunTheOperatorNamedGoesOnAfterGitHubDecides(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		held, paused bool
		want         []string
	}{
		{"held", true, false, []string{"named"}},
		{"paused", false, true, nil},
		{"held and paused", true, true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness()
			harness.capacity = 3
			pushed := harness.now.Add(-time.Hour)
			for id, by := range map[string]string{"named": runstate.SelectedByOperator, "chosen": runstate.SelectedByScheduler, "unaccounted": ""} {
				state := waitingOnGitHub(id, "run-"+id, pushed)
				if by != "" {
					state.Selection = &runstate.Selection{By: by, Reason: "a reason", At: pushed}
				}
				harness.inFlight[id] = state
			}
			if test.held {
				hold := operatorsHold(pushed)
				harness.held = &hold
			}
			runs := recordingRuns{scheduleHarness: harness}
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.Runs, pull.CIWaits, pull.Continuations = runs, passedGitHub{}, waitingContinuations{}
				if test.paused {
					holds, storeErr := runstate.NewOperatorHoldStore(t.TempDir())
					if storeErr != nil {
						return pull, storeErr
					}
					if _, storeErr = holds.Hold(harness.now); storeErr != nil {
						return pull, storeErr
					}
					pull.Holds = holds
				}
				return pull, err
			}
			harness.onSleep = func(*scheduleHarness, int) bool { return false }
			if _, err := (Scheduler{Open: open, Watching: true, Now: harness.clock, Sleep: harness.sleep, Sessions: &recordedSessions{}}).Schedule(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := harness.pullOrder(); fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Fatalf("started %v, want %v", got, test.want)
			}
			if len(test.want) > 0 {
				if reason := harness.selectionFor("named").Reason; !strings.Contains(reason, "the operator started this run by name") {
					t.Fatalf("reason = %q, want it to say why the hold let it through", reason)
				}
			}
			for _, id := range []string{"chosen", "unaccounted"} {
				record := runs.record(id)
				if record.CIWait.Decided == nil || record.ContinuationWait == nil {
					t.Fatalf("%s = %#v, want GitHub's decision and why it waits recorded", id, record)
				}
			}
		})
	}
}

// The continued run goes on to its review under the hold and records why it was
// let through; a run the operator did not name records nothing of the sort.
func TestARunTheOperatorNamedRecordsWhyItWentOnUnderTheHold(t *testing.T) {
	t.Parallel()

	for _, named := range []bool{true, false} {
		f := newCIFixture(t)
		starting := f.pipeline(t, true)
		starting.Selection = runstate.Selection{By: runstate.SelectedByScheduler, Reason: "first in the queue"}
		if named {
			starting.Selection = runstate.OperatorSelection("ran it by name", time.Now())
		}
		outcome, err := starting.Run(context.Background(), f.tracker.Item.ID)
		if err != nil || outcome.PausedByCIWait == nil {
			t.Fatalf("outcome = %#v, err = %v, want a run waiting", outcome, err)
		}
		f.forge.setResult(publish.CheckPassed)
		continuing := f.pipeline(t, true)
		continuing.Selection = runstate.Selection{By: runstate.SelectedByScheduler, Reason: "GitHub has decided"}
		intake := newIntakeHoldStore(t)
		hold := operatorsHold(time.Now())
		if _, err := intake.Hold(hold.HeldBy, hold.Reason, hold.HeldAt); err != nil {
			t.Fatal(err)
		}
		continuing.Intake = intake
		if _, err := continuing.Run(context.Background(), f.tracker.Item.ID); err != nil {
			t.Fatal(err)
		}
		after, err := f.store.Load(outcome.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if f.reviewed() == 0 || after.NamedByOperator() != named {
			t.Fatalf("reviews = %d, selection = %#v", f.reviewed(), after.Selection)
		}
		if note := after.ContinuedUnderHold; (note != nil) != named ||
			(named && (!strings.Contains(note.Why, "the operator started this run by name") || !strings.Contains(note.Why, hold.Reason) || note.At.IsZero())) {
			t.Fatalf("named = %v, continued under hold = %#v", named, note)
		}
	}
}

// stopAsked answers that somebody asked the run to stop.
type stopAsked struct{ waitingContinuations }

func (stopAsked) StopRequested(runID string) (runstate.StopRequest, bool, error) {
	return stopWaitedItsBound(runID), true, nil
}

// A stop asked for while a run waits on GitHub is carried out by the pull, the
// pause and the hold standing or not, and ends the run with its branch and
// checkout kept rather than being called late for the short check having passed.
func TestAStopDuringTheWaitForGitHubEndsTheRunAndKeepsItsChange(t *testing.T) {
	t.Parallel()

	f := newCIFixture(t)
	parked, waiting := f.park(t)
	if waiting.ChecksPassed == nil {
		t.Fatal("the short check left no evidence, so this test shows nothing")
	}
	if err := f.store.RecordStop(stopWaitedItsBound(parked.RunID)); err != nil {
		t.Fatal(err)
	}
	if says := waiting.StoppingSays(stopWaitedItsBound(parked.RunID), time.Now()); !strings.Contains(says, "overdue") {
		t.Fatalf("status says %q of the stop, want it still owed", says)
	}
	harness := newScheduleHarness()
	harness.inFlight[waiting.WorkItemID] = waiting
	hold := operatorsHold(harness.now)
	harness.held = &hold
	ended := 0
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs, pull.CIWaits, pull.Continuations = recordingRuns{scheduleHarness: harness}, passedGitHub{}, stopAsked{}
		pull.EndStopped = func(ctx context.Context, state runstate.State) error {
			ended++
			harness.retire(state.WorkItemID)
			results := reconcileSweep(t, f.repository, f.worktreeRoot, f.store, f.tracker)
			if len(results) != 1 || results[0].Action != ActionCancelled {
				return fmt.Errorf("settled as %#v, want it cancelled", results)
			}
			return nil
		}
		return pull, err
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	schedule, err := (Scheduler{Open: open, Watching: true, Now: harness.clock, Sleep: harness.sleep, Sessions: &recordedSessions{}}).Schedule(context.Background())
	if err != nil || ended == 0 || schedule.ContinuationProblem != "" || len(harness.pullOrder()) != 0 {
		t.Fatalf("ended %d, problem %q, started %v, err %v", ended, schedule.ContinuationProblem, harness.pullOrder(), err)
	}
	stoppedWithChangeKept(t, f, parked.RunID)
}

// A run taken up again by name with a stop pending stops before its local
// checks, including a stop an earlier process called late.
func TestARunTakenUpFromItsWaitWithAStopPendingChecksNothing(t *testing.T) {
	t.Parallel()

	for _, calledLate := range []bool{false, true} {
		f := newCIFixture(t)
		parked, waiting := f.park(t)
		f.forge.setResult(publish.CheckCancelled)
		request := stopWaitedItsBound(parked.RunID)
		if err := f.store.RecordStop(request); err != nil {
			t.Fatal(err)
		}
		if calledLate {
			late := runstate.NewLateStop(request, waiting.ChecksPassed.At, time.Now())
			if _, err := f.store.Update(context.Background(), parked.RunID, runstate.LateStopRecorded{Stop: late, At: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
		_, _ = f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
		if logLines(t, f.fullLog) != 0 || f.reviewed() != 0 {
			t.Fatalf("full checks ran %d times and %d reviews, want neither", logLines(t, f.fullLog), f.reviewed())
		}
		stoppedWithChangeKept(t, f, parked.RunID)
	}
}

func stoppedWithChangeKept(t *testing.T, f *ciFixture, runID string) {
	t.Helper()
	after, err := f.store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != runstate.StatusCancelled || after.CIWait != nil || after.WorktreeRemoved || after.BranchRemoved || after.Branch == "" || after.WorktreePath == "" {
		t.Fatalf("run = %#v, want it cancelled with its branch and checkout kept", after)
	}
}

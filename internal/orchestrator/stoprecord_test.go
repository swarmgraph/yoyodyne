package orchestrator

// From a run's stop to the entry the development manager is shown: the stop
// is written on the run as it stops, with its cause decided then, and the
// entry built from the run carries that stop. Each case below is one the first
// slice of stopped-run recovery (yoyodyne-ifd.428.99.2) names.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// listedAfterStopping records the run in flight, stops it the way stop says
// through the store as the pipeline does, and returns the entry the docket
// lists for it along with the stored run.
func listedAfterStopping(t *testing.T, stop func(*runstate.State)) (triage.Entry, runstate.State) {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	running := stoppedState()
	running.Status = runstate.StatusRunning
	running.CompletedAt = nil
	running.Blocker = ""
	running.CheckFailure = nil
	running.ReviewFindingDetails = nil
	running.ReviewFindings = 0
	running.ReviewSummary = ""
	running.RepairAttempts = 0
	running.ReviewRounds = 0
	if err := store.Create(running); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stopped, err := store.Load(running.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	completed := docketedNow.Add(-30 * time.Minute)
	stopped.Status = runstate.StatusFailed
	stopped.UpdatedAt = completed
	stopped.CompletedAt = &completed
	stop(&stopped)
	if err := store.Save(stopped); err != nil {
		t.Fatalf("Save() of the stop error = %v", err)
	}
	stored, err := store.Load(running.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(stored.Stops) != 1 {
		t.Fatalf("stops = %#v, want exactly one stop written", stored.Stops)
	}
	docket := &memoryDocket{}
	cfg := config.Config{Triage: docketedTriage}
	docketer := docketerOverStore(docket, store, cfg)
	docketer.Clock = fixedClock{at: docketedNow}
	if listed, err := docketer.RecordStoppedRun(stored); err != nil || !listed {
		t.Fatalf("RecordStoppedRun() = %t, %v, want the stop listed", listed, err)
	}
	entries, err := docket.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("List() = %#v, %v, want one entry", entries, err)
	}
	if entries[0].Stop == nil {
		t.Fatalf("entry = %#v, want the run's stop carried on it", entries[0])
	}
	return entries[0], stored
}

func TestACheckStoppedByItsTimeLimitAfterBothRerunsIsListedWithNoAttemptCharged(t *testing.T) {
	t.Parallel()

	entry, _ := listedAfterStopping(t, func(state *runstate.State) {
		state.Phase = runstate.PhaseChecking
		state.StopClass = runstate.CauseCheckTimeLimit.StopClass()
		state.Blocker = "Yoyodyne stopped this item: the check make test ran past its time limit after both of its reruns."
	})
	if entry.Stop.Cause != runstate.StopCauseOutside.Name() || entry.Stop.Charged {
		t.Fatalf("stop = %#v, want it outside the work and uncharged", entry.Stop)
	}
	if entry.Counters.RepairAttempts != 0 {
		t.Fatalf("counters = %#v, want no repair attempt charged", entry.Counters)
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "cause outside the work; no repair attempt or review round charged") {
		t.Fatalf("entry = %s, want the stop's cause and its charge said", rendered)
	}
}

func TestAReviewerSessionOutOfBudgetIsListedAsOutsideTheWork(t *testing.T) {
	t.Parallel()

	entry, _ := listedAfterStopping(t, func(state *runstate.State) {
		state.Phase = runstate.PhaseReviewing
		state.StopClass = runstate.StopReviewBudget
		state.Blocker = "Yoyodyne stopped this item: the reviewer's session ran past its time before a verdict."
	})
	if entry.Stop.Cause != runstate.StopCauseOutside.Name() || entry.Stop.Charged || entry.Stop.Step != string(runstate.PhaseReviewing) {
		t.Fatalf("stop = %#v, want it outside the work, uncharged, at the review", entry.Stop)
	}
}

func TestACheckFailingAfterAReviewKeepsTheReviewersFindingsOnTheStop(t *testing.T) {
	t.Parallel()

	finding := runstate.Finding{Severity: "blocker", Message: "handle the empty list", File: "list.go", Line: 4}
	entry, stored := listedAfterStopping(t, func(state *runstate.State) {
		state.Phase = runstate.PhaseChecking
		state.StopClass = runstate.StopChecks
		state.Blocker = "Yoyodyne stopped this item: the repair budget was spent."
		state.CheckFailure = &runstate.CheckFailure{
			Command: "make test", ExitCode: 1, Output: "FAIL list_test.go",
			ReviewBefore: &runstate.ReviewBefore{Decision: runstate.ReviewRepair, Summary: "the empty list is not handled", Findings: []runstate.Finding{finding}},
		}
	})
	stop, _ := stored.LatestStop()
	if len(stop.Evidence.Findings) != 1 || stop.Evidence.Findings[0].Message != finding.Message || stop.Evidence.CheckCommand != "make test" {
		t.Fatalf("stop evidence = %#v, want the failing check and the reviewer's finding kept", stop.Evidence)
	}
	if entry.Stop.Cause != runstate.StopCauseWork.Name() || !entry.Stop.Charged {
		t.Fatalf("stop = %#v, want a failing check about the work and charged", entry.Stop)
	}
	if len(entry.Findings) != 1 || entry.Findings[0].Message != finding.Message || entry.Summary != "the empty list is not handled" {
		t.Fatalf("entry findings = %#v, summary %q, want the reviewer's findings listed", entry.Findings, entry.Summary)
	}
}

func TestAnApprovedChangeThatLostEveryRaceToMergeIsListedWithTheChangeKept(t *testing.T) {
	t.Parallel()

	candidate := strings.Repeat("c", 40)
	entry, _ := listedAfterStopping(t, func(state *runstate.State) {
		state.Phase = runstate.PhaseIntegrating
		state.StopClass = runstate.StopLostRaces
		state.HarnessCommit = candidate
		state.Blocker = "Yoyodyne stopped this item: other work kept landing first."
	})
	if entry.Stop.Cause != runstate.StopCauseOutside.Name() || entry.Stop.Charged {
		t.Fatalf("stop = %#v, want lost races outside the work and uncharged", entry.Stop)
	}
	if !entry.Stop.Kept || entry.Stop.Candidate != candidate {
		t.Fatalf("stop = %#v, want the change kept at its candidate commit", entry.Stop)
	}
}

// The four stops condition 5 names leave the hold on new work's count where
// it was: four runs stopped one after another for them trip nothing and count
// nothing, where the brake would trip at three.
func TestTheFourStopsOutsideTheWorkLeaveTheHoldsCountUnchanged(t *testing.T) {
	t.Parallel()

	causes := map[string]runstate.StopClass{
		"yoyodyne-one":   runstate.CauseCheckTimeLimit.StopClass(),
		"yoyodyne-two":   runstate.StopLostRaces,
		"yoyodyne-three": runstate.CauseNoCheckRan.StopClass(),
		"yoyodyne-four":  runstate.StopReviewBudget,
	}
	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		return stoppedFor(causes[id])(h, id)
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked != nil || schedule.BlockedInARow != 0 {
		t.Fatalf("braked = %#v, blocked in a row = %d, want none of the four counted", schedule.Braked, schedule.BlockedInARow)
	}
}

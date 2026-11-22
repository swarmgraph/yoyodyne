package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The count a decided re-run is refused by is read from the run records each
// time the re-run is asked, never from the refusal an earlier attempt left on
// the item's record. See docs/diagnoses/yoyodyne-ifd-428-99-36-decided-rerun-refused-for-slots.md.

// recordStaleCapacityRefusal leaves on the item's record the refusal a full
// harness wrote once, under a larger limit than the one configured now.
func recordStaleCapacityRefusal(t *testing.T, harness *rerunHarness) {
	t.Helper()
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	decision, found := counters.DecisionOf(docketedRunID)
	if !found {
		t.Fatal("no re-run decision stands to record a refusal against")
	}
	if _, err := harness.runs.Triage().RecordCarryOutRefusal(context.Background(), docketedItem, runstate.TriageCarryOut{
		RunID:     docketedRunID,
		Decision:  runstate.TriageDecisionRerun,
		DecidedAt: decision.DecidedAt,
		Gate:      runstate.TriageGateCapacity,
		Refusal:   "every developer slot is occupied: 4 active, limit 3",
		Clears:    "a developer slot freeing, which needs nobody; nothing was claimed, so the stoppage keeps its re-run",
		Waiting:   true,
	}, docketedNow); err != nil {
		t.Fatalf("RecordCarryOutRefusal() error = %v", err)
	}
}

// A decided re-run once refused with "4 active, limit 3" starts when the run
// records leave a developer slot free, whatever the refusal on its record says.
func TestADecidedRerunStartsWhenTheRecordsShowAFreeSlotDespiteAStaleRefusal(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.capacity = 2
	recordStaleCapacityRefusal(t, harness)
	// One run holds one of the two slots on record.
	if err := harness.runs.Create(runningState("run-11112222333344445555666677778888", "yoyodyne-ifd.other")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if result.CapacityFull != nil {
		t.Fatalf("capacity = %#v, want no refusal with one of two slots held on record", result.CapacityFull)
	}
	if !result.Started || len(harness.started) != 1 {
		t.Fatalf("started = %t / %#v, want the decided re-run started in the free slot", result.Started, harness.started)
	}
}

// Where both slots are held on record the re-run is refused, and the refusal
// counts what the records hold now against the limit configured now, not the
// figures the earlier refusal carried.
func TestADecidedRerunRefusedForFullSlotsCountsTheRecordsNow(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.capacity = 2
	recordStaleCapacityRefusal(t, harness)
	for _, run := range []runstate.State{
		runningState("run-11112222333344445555666677778888", "yoyodyne-ifd.other"),
		runningState("run-22223333444455556666777788889999", "yoyodyne-ifd.another"),
	} {
		if err := harness.runs.Create(run); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if result.Started || len(harness.started) != 0 {
		t.Fatalf("started = %t / %#v, want nothing started with both slots held", result.Started, harness.started)
	}
	if result.CapacityFull == nil || result.CapacityFull.Active != 2 || result.CapacityFull.Limit != 2 {
		t.Fatalf("capacity = %#v, want 2 active against the limit of 2", result.CapacityFull)
	}
	if says := capacityWaitSays(*result.CapacityFull); !strings.Contains(says, "2 active, limit 2") {
		t.Fatalf("the wait says %q, want today's count", says)
	}
}

// What kept the guides re-run from starting: a wait naming a work item, recorded
// about the same run, replaces the re-run decision, so there is no re-run left
// to carry out and no slot is ever asked for. The refusal from the attempt
// before stays on the record but is not about the decision that stands.
func TestAWaitNamingWorkAboutTheSameRunLeavesNoRerunToCarryOut(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	recordStaleCapacityRefusal(t, harness)
	wait := triageDecided(runstate.TriageDecisionWait, docketedRunID)
	wait.WaitsOn = "yoyodyne-ifd.428.99.31"
	counters, err := harness.runs.RecordTriageDecision(context.Background(), docketedItem, wait, docketedNow.Add(1))
	if err != nil {
		t.Fatalf("RecordTriageDecision() error = %v", err)
	}
	if counters.Replaced == nil || counters.Replaced.Decision != runstate.TriageDecisionRerun {
		t.Fatalf("replaced = %#v, want the re-run named as what the wait ended", counters.Replaced)
	}
	if _, refused := counters.RefusedCarryOut(docketedRunID); refused {
		t.Fatal("the earlier capacity refusal reads as standing against the wait that replaced its decision")
	}

	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err == nil {
		t.Fatal("Rerun() error = nil, want the replaced re-run refused as no longer decided")
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started once the re-run was replaced", harness.started)
	}
}

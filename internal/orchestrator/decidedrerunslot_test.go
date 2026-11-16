package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The re-runs this covers were refused once because all three slots were held,
// and were never tried again: the session counted a slot for every start of its
// own that had not reserved one, carry-outs the tracker kept waiting among
// them, so its count read full long after the records showed slots empty, and
// the re-runs it had marked as waiting on capacity were passed over as still
// shut while decisions recorded after them started in the slots they were owed.

func decidedRerunSlotTasks() (waiting, newer CarryOutTask) {
	waiting = decidedTaskOf("yoyodyne-7xx", "run-273b7c4d85397b33f32706ac59590fee")
	waiting.DecidedAt = time.Date(2026, 11, 15, 16, 19, 16, 0, time.UTC)
	newer = decidedTaskOf("yoyodyne-zkyc", "run-388d5d1114aff7d2d34851dcb9d39d0b")
	newer.DecidedAt = time.Date(2026, 11, 15, 19, 57, 7, 0, time.UTC)
	return waiting, newer
}

func inFlightRun(workItemID, runID string) runstate.State {
	return runstate.State{WorkItemID: workItemID, RunID: runID, Status: runstate.StatusRunning}
}

// A decided re-run a full harness refused is tried again at the first pull the
// records leave a slot free, though the session's own count still reads full
// for starts of its own that hold no slot yet, and it is tried ahead of a
// decision recorded after it.
func TestADecidedRerunRefusedForAFullHarnessIsTriedOnceTheRecordsFreeASlot(t *testing.T) {
	t.Parallel()

	waiting, newer := decidedRerunSlotTasks()
	harness := newScheduleHarness()
	harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) {
		return []CarryOutTask{waiting, newer}, nil
	}
	// Three slots: one run on record holds one, and two starts of this session's
	// that have not reserved stand over two more items, so the session counts
	// none free while the records leave two.
	occupied := map[string]runstate.State{
		"yoyodyne-ifd.429.138": inFlightRun("yoyodyne-ifd.429.138", "run-5a0c748d8593873484807d139d2e4f33"),
		"yoyodyne-ifd.434.12":  {WorkItemID: "yoyodyne-ifd.434.12"},
		"yoyodyne-ifd.309":     {WorkItemID: "yoyodyne-ifd.309"},
	}
	free := 3 - len(occupied)
	if capacityGateShut(free, harness, occupied, 3, time.Now()) {
		t.Fatalf("the capacity gate reads shut with one of three slots held on record, want it open")
	}
	closed := closedGates{capacity: capacityGateShut(free, harness, occupied, 3, time.Now())}
	waitingOn := map[string]string{waiting.WorkItemID: runstate.TriageGateCapacity}

	schedule := Schedule{}
	chosen, passed, _ := Scheduler{}.nextCarryOuts(&schedule, Pull{CarryOut: harness, Capacity: 3}, occupied, nil, waitingOn, closed, free, 0, nil)
	if len(chosen) != 1 || chosen[0].RunID != waiting.RunID {
		t.Fatalf("chosen = %#v, want the re-run the full harness refused tried again, ahead of the decision recorded after it", chosen)
	}
	why := passed[newer.RunID]
	if !strings.Contains(why, "while the runs on record leave a slot free") || strings.Contains(why, "held by runs in flight") {
		t.Fatalf("the newer decision was passed over because %q, want it said that the records leave a slot the attempt ahead of it takes", why)
	}
	if strings.Contains(schedule.Render(), "refused its last attempt and is still shut") {
		t.Fatalf("the pass log reads:\n%s\nwant nothing said of a shut gate the records show open", schedule.Render())
	}
}

// Where the runs on record hold every slot, the re-run a full harness refused
// stays where it is: the gate is still shut, and the one attempt a full pull
// makes to record that goes to a decision not yet refused. The operator's hold
// on intake keeps it back the same way while the hold is in force, whatever
// the slots say.
func TestADecidedRerunStaysHeldWhileEverySlotIsHeldOnRecordOrIntakeIsHeld(t *testing.T) {
	t.Parallel()

	waiting, newer := decidedRerunSlotTasks()
	harness := newScheduleHarness()
	harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) {
		return []CarryOutTask{waiting, newer}, nil
	}

	t.Run("every slot is held on record", func(t *testing.T) {
		t.Parallel()
		occupied := map[string]runstate.State{
			"yoyodyne-ifd.429.166": inFlightRun("yoyodyne-ifd.429.166", "run-5fcbd419baa09bb541329154808d5a39"),
			"yoyodyne-ifd.435.48":  inFlightRun("yoyodyne-ifd.435.48", "run-be0dec87d9abcce0466b2d93d1fa3740"),
			"yoyodyne-6d1":         inFlightRun("yoyodyne-6d1", "run-a471c1d0c70afec671cb5e43d186f9ee"),
		}
		if !capacityGateShut(0, harness, occupied, 3, time.Now()) {
			t.Fatalf("the capacity gate reads open with all three slots held on record, want it shut")
		}
		schedule := Schedule{}
		waitingOn := map[string]string{waiting.WorkItemID: runstate.TriageGateCapacity}
		chosen, _, _ := Scheduler{}.nextCarryOuts(&schedule, Pull{CarryOut: harness, Capacity: 3}, occupied, nil, waitingOn, closedGates{capacity: true}, 0, 0, nil)
		for _, task := range chosen {
			if task.RunID == waiting.RunID {
				t.Fatalf("chosen = %#v, want the refused re-run left while every slot is held on record", chosen)
			}
		}
		if rendered := schedule.Render(); !strings.Contains(rendered, "yoyodyne-7xx was not started this pass") || !strings.Contains(rendered, "still shut") {
			t.Fatalf("the pass log reads:\n%s\nwant the held re-run named with the gate that holds it", rendered)
		}
	})

	t.Run("intake is held", func(t *testing.T) {
		t.Parallel()
		occupied := map[string]runstate.State{}
		if capacityGateShut(3, harness, occupied, 3, time.Now()) {
			t.Fatalf("the capacity gate reads shut with every slot free")
		}
		schedule := Schedule{}
		waitingOn := map[string]string{waiting.WorkItemID: runstate.TriageGateIntakeHold}
		chosen, _, _ := Scheduler{}.nextCarryOuts(&schedule, Pull{CarryOut: harness, Capacity: 3}, occupied, nil, waitingOn, closedGates{intake: true}, 3, 0, nil)
		for _, task := range chosen {
			if task.RunID == waiting.RunID {
				t.Fatalf("chosen = %#v, want the re-run the hold refused left while the hold is in force", chosen)
			}
		}
	})
}

// What counts against the slots on record is a run with its record: a start of
// the session's own with no run recorded yet holds nothing there, and nor does
// a run whose worker is gone; a run whose worker is working does.
func TestOnlyRunsOnRecordThatHoldASlotFillTheRecordedCount(t *testing.T) {
	t.Parallel()

	runs := goneWorkers{scheduleHarness: newScheduleHarness(), gone: map[string]bool{"run-gone": true}}
	occupied := map[string]runstate.State{
		"yoyodyne-unreserved": {WorkItemID: "yoyodyne-unreserved"},
		"yoyodyne-gone":       inFlightRun("yoyodyne-gone", "run-gone"),
		"yoyodyne-working":    inFlightRun("yoyodyne-working", "run-working"),
	}
	if recordedSlotsFull(runs, occupied, 2, time.Now()) {
		t.Fatalf("two slots read full on record with one working run, want one free")
	}
	if !recordedSlotsFull(runs, occupied, 1, time.Now()) {
		t.Fatalf("one slot reads free on record with its working run, want it full")
	}
}

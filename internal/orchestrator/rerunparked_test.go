package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// parkedByProductManager is an item the Lead Product Manager parked from her
// conversation: the parking says why, and the note she appended says who.
func parkedByProductManager(h *rerunHarness) {
	h.item.Parking = domain.WorkItemParking("held until the Slack route is decided")
	h.item.Notes = "Parked by the Lead Product Manager in conversation chat-91253e0e, after turn 5747.\n\nReason: held until the Slack route is decided"
}

// unpark is whoever parked the item releasing it.
func unpark(h *rerunHarness) {
	h.item.Parking = ""
	h.item.Notes += "\n\nReleased from parking by the Lead Product Manager in conversation chat-91253e0e, after turn 5800."
}

// A re-run decided on an item somebody else parked is not started while the
// item stays parked, spends nothing, and names who parked it. Once the item is
// unparked, the same decision starts the run with nobody deciding it again.
func TestARerunOfAnItemSomebodyElseParkedWaitsUntilItIsUnparked(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	parkedByProductManager(harness)
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v, want the parking reported as a wait rather than a failure", err)
	}
	if result.Started || len(harness.started) != 0 {
		t.Fatalf("started = %t / %#v, want nothing started on a parked item", result.Started, harness.started)
	}
	if result.ItemParked == nil || result.ItemParked.By != "the Lead Product Manager in conversation chat-91253e0e" {
		t.Fatalf("item parked = %#v, want the parking named with who placed it", result.ItemParked)
	}
	says := result.ItemParked.Says(docketedItem)
	for _, want := range []string{"parked by the Lead Product Manager", "waits until the item is unparked", "held until the Slack route is decided"} {
		if !strings.Contains(says, want) {
			t.Fatalf("says %q, want it to say %q", says, want)
		}
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
	}

	unpark(harness)
	result, err = harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() after the item was unparked error = %v", err)
	}
	if !result.Started || len(harness.started) != 1 || result.ItemParked != nil {
		t.Fatalf("result = %#v, started = %#v, want the same decision carried out once the item is unparked", result, harness.started)
	}
}

// A parking the stopped run placed on its own item — the evidence landing's — is
// the one the re-run answers, so it does not hold the re-run.
func TestARerunOfAnItemParkedOnlyByItsOwnStoppedRunStarts(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Parking = domain.WorkItemParking("yoyodyne run " + docketedRunID + " landed evidence for this item rather than the work, and did not discharge it: the parent has to land first")
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if !result.Started || len(harness.started) != 1 || result.ItemParked != nil {
		t.Fatalf("result = %#v, started = %#v, want the re-run started over its own run's parking", result, harness.started)
	}
}

// A parking another run placed is not the stopped run's own, and holds the
// re-run like any other.
func TestARerunOfAnItemAnotherRunParkedWaits(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Parking = domain.WorkItemParking("yoyodyne run run-99999999999999999999999999999999 landed evidence for this item rather than the work")
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if result.Started || result.ItemParked == nil || result.ItemParked.By != "run run-99999999999999999999999999999999" {
		t.Fatalf("result = %#v, want the re-run waiting on the other run's parking, naming that run", result)
	}
}

// The pass holds a re-run of a parked item back before anything is attempted,
// and writes on the item's record that it waits for the item to be unparked —
// which every surface then reads as a wait on the unparking rather than as a
// decision the harness owes, a refusal, or a decision no pass reached. Once the
// item is unparked the next pass offers it and carries it out.
func TestTheCarryOutPassHoldsAParkedRerunAsWaitingOnTheUnparking(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	parkedByProductManager(harness)
	carrying := harness.carryOut()
	carrying.Notes = harness

	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want a re-run of a parked item held back rather than offered", outstanding)
	}
	// Written at once, with no grace interval: until it is, every surface reads
	// the decision as the harness's to carry out.
	if _, err := carrying.RecordUnattempted(context.Background(), 0, nil); err != nil {
		t.Fatalf("RecordUnattempted() error = %v", err)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	finding, found := counters.CarryOutOf(docketedRunID)
	if !found || !finding.WaitsOnParking() || !strings.Contains(finding.Refusal, "the Lead Product Manager") {
		t.Fatalf("finding = %#v, want one saying the re-run waits on the Lead Product Manager's parking", finding)
	}
	standing := counters.Standing(docketedRunID)
	if triage.AwaitingCarryOut(standing) {
		t.Fatalf("standing = %#v, want a decision waiting on the unparking not read as owed by the harness", standing)
	}
	if !triage.DecisionStands(standing) || !triage.WaitingOnParking(standing) {
		t.Fatalf("standing = %#v, want the decision still standing and waiting on the unparking", standing)
	}
	if refused, unattempted := counters.CarryOutFindings(); refused != 0 || unattempted != 0 {
		t.Fatalf("findings = %d refused, %d unattempted, want the parked wait counted as neither", refused, unattempted)
	}
	if !strings.Contains(finding.Describe(), "waits for the item to be unparked") {
		t.Fatalf("describe = %q, want it to say the decision waits for the unparking", finding.Describe())
	}

	unpark(harness)
	task := theOneOutstanding(t, carrying)
	carried, _, err := carrying.Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want the re-run started once the item is unparked", carried, harness.started)
	}
	after, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if _, standing := after.CarryOutOf(docketedRunID); standing {
		t.Fatalf("carry-outs = %#v, want the wait cleared once the re-run started", after.CarryOuts)
	}
}

// A carry-out that reaches a parked item anyway — a pass with no tracker to read
// the item through, or a parking placed between the sweep and the attempt —
// meets the action's own wait, which records the same finding and spends
// nothing.
func TestACarryOutThatMeetsAParkedItemWaitsOnIt(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	parkedByProductManager(harness)
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want the parking reported rather than a failure", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing started on a parked item", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGateParked || !carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want a wait on the item being parked", carried.Gate, carried.Waiting)
	}
	if !strings.Contains(carried.Problem, "parked by the Lead Product Manager") {
		t.Fatalf("problem = %q, want who parked the item named", carried.Problem)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if standing := counters.Standing(docketedRunID); triage.AwaitingCarryOut(standing) || !triage.WaitingOnParking(standing) {
		t.Fatalf("standing = %#v, want the decision read as waiting on the unparking, not owed", standing)
	}
}

// A wait an earlier attempt recorded is replaced once the item is parked, since
// a parked item is never attempted and nothing else would replace it; and the
// parked wait is replaced in turn when the released item is held back for
// something else.
func TestAParkingReplacesAnEarlierWaitAndALaterHoldReplacesTheParking(t *testing.T) {
	t.Parallel()

	for _, earlier := range []string{runstate.TriageGateIntakeHold, runstate.TriageGateCapacity} {
		harness := newRerunHarness(t, stoppedState())
		carrying := harness.carryOutAt(time.Minute)
		if earlier == runstate.TriageGateIntakeHold {
			if _, err := harness.intake.Hold(runstate.IntakeHolderOperator, "held", docketedNow); err != nil {
				t.Fatal(err)
			}
		} else if err := harness.runs.Create(runningState("run-11111111111111111111111111111111", "yoyodyne-other")); err != nil {
			t.Fatal(err)
		} else {
			harness.capacity = 1
			carrying = harness.carryOutAt(time.Minute)
		}
		if carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil || carried.Gate != earlier {
			t.Fatalf("carried = %#v, error = %v, want it waiting on %s", carried, err, earlier)
		}

		parkedByProductManager(harness)
		carrying.Notes = harness
		record := func() runstate.TriageCounters {
			t.Helper()
			if _, err := carrying.RecordUnattempted(context.Background(), 0, map[string]string{docketedRunID: "no slot"}); err != nil {
				t.Fatalf("RecordUnattempted() error = %v", err)
			}
			counters, err := harness.runs.Triage().Counters(docketedItem)
			if err != nil {
				t.Fatal(err)
			}
			return counters
		}
		if standing := record().Standing(docketedRunID); !triage.WaitingOnParking(standing) {
			t.Fatalf("standing after %s = %#v, want it waiting on the parking", earlier, standing)
		}

		// The action's own parked wait is replaced as the sweep's is.
		task := CarryOutTask{WorkItemID: docketedItem, RunID: docketedRunID, Decision: runstate.TriageDecisionRerun, DocketKey: triage.Key(triage.ClassStoppedRun, docketedRunID)}
		counters, err := harness.runs.Triage().Counters(docketedItem)
		if err != nil {
			t.Fatal(err)
		}
		decision, _ := counters.DecisionOf(docketedRunID)
		task.DecidedAt = decision.DecidedAt
		if carried, _, err := carrying.Carry(context.Background(), task); err != nil || carried.Gate != runstate.TriageGateParked {
			t.Fatalf("carried = %#v, error = %v", carried, err)
		}
		unpark(harness)
		if finding, _ := record().CarryOutOf(docketedRunID); finding.WaitsOnParking() || finding.Gate != runstate.TriageGateCapacity {
			t.Fatalf("finding = %#v, want the released item's later hold in place of the parking", finding)
		}
	}
}

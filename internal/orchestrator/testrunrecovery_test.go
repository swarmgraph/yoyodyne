package orchestrator

// A decided recovery of the hold's own test run takes the hold's one start.
//
// The brake's hold lets one run start at a time to find out whether the line is
// fine. When that run stopped short of landing and the development manager
// decided to recover it, the recovery used to be refused by the very hold it
// would lift, while the hold's next start went to fresh work from the queue — so
// the hold had no end built in. Each test below starts from that hold: its test
// run blocked, a repair of it decided, another decided recovery queued ahead of
// it, and fresh work ready.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	testRunItem        = "yoyodyne-test-run"
	testRunID          = "run-test-run"
	otherRecoveryItem  = "yoyodyne-other-recovery"
	otherRecoveryRunID = "run-other-recovery"
	freshItem          = "yoyodyne-fresh"
)

// heldAfterTestRun is the brake's hold as it stands once its test run blocked:
// the probe settled blocked, one cycle spent, and the next start due at
// cooldownEnds.
func heldAfterTestRun(now, cooldownEnds time.Time) *runstate.IntakeHold {
	started, ended := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	return &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        now.Add(-24 * time.Hour),
		HeldBy:        runstate.IntakeHolderBrake,
		Reason:        "three runs in a row blocked with nothing landing",
		Brake: &runstate.IntakeBrake{
			Blocked:        []runstate.BrakeBlockedRun{{RunID: "run-a", WorkItemID: "yoyodyne-a", Reason: "review refused"}},
			CooldownEndsAt: cooldownEnds,
			Probe: &runstate.IntakeProbe{
				WorkItemID: testRunItem,
				RunID:      testRunID,
				StartedAt:  started,
				EndedAt:    &ended,
				Blocked:    true,
				Reason:     "lost the race to land on main five times",
			},
			Probes: 1,
			Cycles: 1,
		},
	}
}

// recoveryHarness is the hold above with two decided repairs outstanding — the
// other one decided first — and fresh work ready. finish is what the test run's
// repair comes to; the other recovery is refused by the hold while it stands
// and lands once it is lifted, as the real action does.
func recoveryHarness(finish func(*scheduleHarness) Outcome) (*scheduleHarness, *[]string) {
	harness := newScheduleHarness(readyItems(freshItem)...)
	harness.capacity = 2
	harness.cooldown = 30 * time.Minute
	now := harness.now
	harness.held = heldAfterTestRun(now, now.Add(-time.Minute))
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
	}
	other := CarryOutTask{WorkItemID: otherRecoveryItem, RunID: otherRecoveryRunID, Decision: runstate.TriageDecisionRepair, DecidedAt: now.Add(-3 * time.Hour)}
	test := CarryOutTask{WorkItemID: testRunItem, RunID: testRunID, Decision: runstate.TriageDecisionRepair, DecidedAt: now.Add(-time.Hour)}
	fired := map[string]bool{}
	harness.outstanding = outstandingUntilFired(fired, other, test)
	var startedOrder []string
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		h.mu.Lock()
		hold := h.held
		h.mu.Unlock()
		carried := CarriedOut{WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision}
		// The real repair action's own check: a hold lets through only the
		// recovery its record names as the probe in flight.
		if hold != nil && !hold.RecoveryProbing(task.WorkItemID, task.RunID) {
			carried.Gate, carried.Waiting = runstate.TriageGateIntakeHold, true
			carried.Problem = "the hold on new work is standing"
			return carried, Outcome{}, nil
		}
		h.mu.Lock()
		fired[task.WorkItemID] = true
		startedOrder = append(startedOrder, task.WorkItemID)
		h.mu.Unlock()
		carried.Carried = true
		if task.WorkItemID == testRunItem {
			return carried, finish(h), nil
		}
		landed := h.complete(task.WorkItemID)
		landed.RunID = task.RunID
		return carried, landed, nil
	}
	return harness, &startedOrder
}

func probes(schedule Schedule) []Started {
	var started []Started
	for _, entry := range schedule.Started {
		if entry.Probe {
			started = append(started, entry)
		}
	}
	return started
}

// The test run's repair is the hold's next start, ahead of the other decided
// recovery queued before it and of the fresh work ready in the order; it counts
// as the hold's test run, so its landing lifts the hold exactly as the original
// run's would have; and only then do the other recovery and the fresh work go.
func TestTheTestRunsDecidedRepairTakesTheHoldsStartAndItsLandingLiftsTheHold(t *testing.T) {
	t.Parallel()

	harness, startedOrder := recoveryHarness(func(h *scheduleHarness) Outcome {
		landed := h.complete(testRunItem)
		landed.RunID = testRunID
		return landed
	})
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 6 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: &recordedSessions{}, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(*startedOrder) == 0 || (*startedOrder)[0] != testRunItem {
		t.Fatalf("started %v, want the test run's repair to start first under the hold: %s", *startedOrder, schedule.Render())
	}
	started := probes(schedule)
	if len(started) != 1 || started[0].WorkItemID != testRunItem {
		t.Fatalf("probes = %#v, want the one start the hold allowed to be the test run's repair", started)
	}
	if order := harness.pullOrder(); len(order) > 0 && order[0] == freshItem && len(harness.releases) == 0 {
		t.Fatalf("pulled %v under the hold, want nothing from the queue while the test run's repair waited", order)
	}
	if len(harness.releases) != 1 {
		t.Fatalf("released %d time(s), want the hold lifted once on the repaired run landing", len(harness.releases))
	}
	lifted := harness.releases[0]
	if lifted.Brake == nil || lifted.Brake.Probe == nil || !lifted.Brake.Probe.Landed || lifted.Brake.Probe.Recovers != testRunID {
		t.Fatalf("lifted hold = %#v, want the landed recovery of %s on its record", lifted.Brake.Probe, testRunID)
	}
	if len(schedule.Released) != 1 || !strings.Contains(schedule.Released[0].Reason, testRunID+" of "+testRunItem+" landed") {
		t.Fatalf("released = %#v, want the release attributed to the repaired test run landing", schedule.Released)
	}
	// Once the hold was lifted the other recovery and the fresh work went too.
	if len(*startedOrder) != 2 || (*startedOrder)[1] != otherRecoveryItem {
		t.Fatalf("started %v, want the other recovery carried out once the hold was lifted", *startedOrder)
	}
	if order := harness.pullOrder(); len(order) != 1 || order[0] != freshItem {
		t.Fatalf("pulled %v, want the fresh item pulled once the hold was lifted", order)
	}
}

// A repaired test run that blocks again keeps the hold, as the original run's
// blocking did: one more cycle, the development manager asked again, and the
// record still naming the run so the next decided recovery of it takes the
// hold's next start in turn. Nothing else started under the hold.
func TestATestRunsRepairThatBlocksAgainKeepsTheHold(t *testing.T) {
	t.Parallel()

	harness, startedOrder := recoveryHarness(func(h *scheduleHarness) Outcome {
		h.retire(testRunItem)
		return Outcome{RunID: testRunID, WorkItemID: testRunItem, Status: runstate.StatusFailed, Blocked: true,
			Failure: "lost the race to land on main again"}
	})
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 6 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: &recordedSessions{}, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(*startedOrder) != 1 || (*startedOrder)[0] != testRunItem {
		t.Fatalf("started %v, want the test run's repair and nothing else", *startedOrder)
	}
	if len(harness.releases) != 0 {
		t.Fatalf("released %d time(s), want the hold kept when the repaired run blocked", len(harness.releases))
	}
	hold, held, _ := harness.Held()
	if !held || hold.Brake == nil || hold.Brake.Probe == nil {
		t.Fatalf("hold = %#v, want it still standing with its probe recorded", hold)
	}
	probe := hold.Brake.Probe
	if !probe.Blocked || probe.Recovers != testRunID || probe.RunID != testRunID {
		t.Fatalf("probe = %#v, want the blocked recovery of %s on the record", probe, testRunID)
	}
	if hold.Brake.Cycles != 2 {
		t.Fatalf("cycles = %d, want the blocked recovery counted as one more cycle", hold.Brake.Cycles)
	}
	if !hold.Brake.TestRun(testRunID) {
		t.Fatal("the record no longer names the test run, want a later recovery of it to take the next start")
	}
	if order := harness.pullOrder(); len(order) != 0 {
		t.Fatalf("pulled %v, want nothing from the queue under a hold that stays", order)
	}
	if len(probes(schedule)) != 1 {
		t.Fatalf("probes = %#v, want one start under the hold before the cooldown", probes(schedule))
	}
}

// While the test run's repair waits for the hold's next start, what is said of it
// — on the pass and on its item, which is what the status surfaces read — says
// it is the run that lifts the hold and what it waits for. The other decided
// recovery is attempted and refused by the hold exactly as before.
func TestATestRunsRepairWaitingForTheHoldSaysItIsTheRunThatLiftsIt(t *testing.T) {
	t.Parallel()

	harness, startedOrder := recoveryHarness(func(h *scheduleHarness) Outcome { return h.complete(testRunItem) })
	harness.held = heldAfterTestRun(harness.now, harness.now.Add(20*time.Minute))
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 2 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: &recordedSessions{}, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(*startedOrder) != 0 {
		t.Fatalf("started %v, want nothing before the hold's next start is due", *startedOrder)
	}
	harness.mu.Lock()
	passed := append([]map[string]string(nil), harness.passedOver...)
	attempted := append([]CarryOutTask(nil), harness.carried...)
	harness.mu.Unlock()
	if len(passed) == 0 {
		t.Fatal("nothing was passed over, want the test run's repair said to be waiting")
	}
	why := passed[0][testRunID]
	for _, want := range []string{"the run that lifts the hold", "waits for the development manager deciding", "if it lands the hold is lifted"} {
		if !strings.Contains(why, want) {
			t.Fatalf("waiting reason = %q, want it to say %q", why, want)
		}
	}
	waited := false
	for _, wait := range schedule.CarryOutWaiting {
		waited = waited || (wait.RunID == testRunID && strings.Contains(wait.Waits, "the run that lifts the hold"))
	}
	if !waited {
		t.Fatalf("carry-out waits = %#v, want the pass's log to say why the test run's repair waits", schedule.CarryOutWaiting)
	}
	// The other recovery is offered and refused by the hold, as it always was;
	// the test run's repair is not attempted beside the queue at all.
	for _, task := range attempted {
		if task.WorkItemID == testRunItem {
			t.Fatalf("attempted %#v, want the test run's repair kept for the hold's start rather than refused by it", task)
		}
	}
	if len(attempted) == 0 || attempted[0].WorkItemID != otherRecoveryItem {
		t.Fatalf("attempted %#v, want the other decided recovery offered and refused by the hold as before", attempted)
	}
}

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// carryOutOver wires a carry-out onto the same durable state a re-run acts on,
// so what the sweep fires and what the verb fires are one action rather than two
// accounts of one.
func (h *rerunHarness) carryOut() CarryOut { return h.carryOutAt(0) }

// carryOutAt is the same carry-out reading the world a while later, which is what
// the pacing of a refused decision is measured against.
func (h *rerunHarness) carryOutAt(after time.Duration) CarryOut {
	return CarryOut{
		Docket:    h.docket,
		Decisions: h.runs.Triage(),
		Reruns:    h.reruns,
		Runs:      h.runs,
		Rerunner:  h.rerunner(),
		Holds:     h.holds,
		Clock:     laterClock{after: after},
	}
}

// laterClock is the harness clock moved on by a fixed amount, so a test can put a
// refusal's pacing behind it without spending the interval.
type laterClock struct{ after time.Duration }

func (c laterClock) Now() time.Time { return docketedNow.Add(c.after) }

// theOneOutstanding is the single decision the sweep should have found, or a
// failure naming what it found instead: every sequence below turns on the sweep
// choosing exactly one thing to fire.
func theOneOutstanding(t *testing.T, carrying CarryOut) CarryOutTask {
	t.Helper()
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 1 {
		t.Fatalf("outstanding = %#v, want the one decision nobody has acted on", outstanding)
	}
	return outstanding[0]
}

// The condition this exists to end: a decision the development manager recorded
// causes the work, with nobody typing a verb. What it fires is the same action
// the verb fires, under the same attribution.
func TestARecordedDecisionIsCarriedOutWithNobodyAskingForIt(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	carrying := harness.carryOut()
	task := theOneOutstanding(t, carrying)
	if task.Decision != runstate.TriageDecisionRerun || task.RunID != docketedRunID {
		t.Fatalf("task = %#v, want the re-run decided about the docketed stoppage", task)
	}
	// The reasoning travels from the record the development manager wrote rather
	// than from anything this package composed.
	if task.Reason != rerunReasoning {
		t.Fatalf("reason = %q, want the reasoning the decision was recorded with", task.Reason)
	}
	carried, outcome, err := carrying.Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want the decision fired once", carried, harness.started)
	}
	if outcome.RunID == "" {
		t.Fatalf("outcome = %#v, want the fresh run the carry-out started", outcome)
	}
	if !strings.Contains(carried.Reason, rerunReasoning) || !strings.Contains(carried.Reason, decidedIn) {
		t.Fatalf("reason = %q, want the decision cited to the record it was read from", carried.Reason)
	}
	// And it is not outstanding afterwards, which is what stops the next pass
	// firing the same decision again.
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() after the carry-out error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want nothing left once the decision has been acted on", outstanding)
	}
}

// The gate the invariant turns on: a carry-out is the harness choosing work, so
// the operator's hold stops it — and stops it visibly, because a decision that
// silently fails to fire is the condition this replaces rather than a new form
// of it.
func TestAHeldIntakeStopsTheCarryOutAndSaysSoOnTheItem(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere odd", docketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want a gate reported rather than a failure", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing fired under a hold", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGateIntakeHold || !carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the intake hold reported as a gate that clears on its own", carried.Gate, carried.Waiting)
	}
	// The finding is on the item's own record, which is where the docket entry the
	// development manager reads joins it from.
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	recorded, found := counters.CarryOutOf(docketedRunID)
	if !found {
		t.Fatalf("counters = %#v, want the refusal recorded against the stoppage it was about", counters)
	}
	if recorded.Gate != runstate.TriageGateIntakeHold || !strings.Contains(recorded.Clears, "yoyo release") {
		t.Fatalf("finding = %#v, want the gate named and what clears it", recorded)
	}
	// Nothing was spent, so releasing the hold carries out the same decision.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() after the hold was released error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the decision carried out once the hold was lifted", harness.started)
	}
}

// A decision the harness fires while the operator has paused spending is one it
// must not begin at all: a repair writes to the item and the run before it starts
// anything, so a pause noticed later is a paused harness that nonetheless
// unblocked an item.
func TestTheOperatorsPauseStopsTheCarryOutBeforeAnythingIsAttempted(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.holds.Hold(docketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want the pause reported rather than a failure", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing attempted under a pause", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGateSpendingPause || !carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the pause reported as a gate that clears on its own", carried.Gate, carried.Waiting)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
	}
}

// yoyodyne-ifd.299's guarantee, exercised through the pass rather than assumed:
// a carry-out refused before anything was claimed leaves the budget where it was,
// so the same decision is carried out by asking again once the refusal no longer
// applies. The item's own state is the refusal that provoked it.
func TestACarryOutRefusedPreFlightSpendsNothingAndSaysWhatWouldClearIt(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	// The item is still blocked by the run that stopped, which is what a fresh run
	// of it would start from.
	harness.item.Status = "closed"
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want the refusal reported rather than raised", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing started on an item no run may start on", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGateWorkItem || carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the item's own state named as a gate somebody has to open", carried.Gate, carried.Waiting)
	}
	// Nothing was claimed, which is the whole of what makes asking again worth
	// anything.
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.Reruns != 1 {
		t.Fatalf("reruns = %d, want the decision's own spend and nothing more", counters.Reruns)
	}
	// And the same decision fires once the item is back, on the budget that was
	// never touched. It is asked past the pacing the refusal put on it, which is
	// what a later pass reaches on its own.
	harness.item.Status = "open"
	carrying = harness.carryOutAt(runstate.TriageCarryOutRetryDelay + time.Minute)
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() after the item was put back error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the same decision carried out once the item was put back", harness.started)
	}
}

// A refusal that needs somebody to act is paced, or one refused decision spends
// every pass's single carry-out and starves the decided stoppages behind it.
func TestARefusedDecisionIsLeftAloneUntilItsPacingHasPassed(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "closed"
	carrying := harness.carryOut()
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want a refused decision left to its pacing rather than tried again at once", outstanding)
	}
	// The finding stands on the record the whole time, which is what keeps the
	// pacing from being silence.
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if _, found := counters.CarryOutOf(docketedRunID); !found {
		t.Fatalf("counters = %#v, want the refusal readable for as long as it stands", counters)
	}
}

// A gate that clears without anybody doing anything is not paced, or a decision
// waits a quarter of an hour after the switch it was waiting on was opened.
func TestAGateThatClearsOnItsOwnIsTriedAgainAtOnce(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere odd", docketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	carrying := harness.carryOut()
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 1 {
		t.Fatalf("outstanding = %#v, want the decision offered again the moment the hold was lifted", outstanding)
	}
}

// A decision whose carry-out fired once is not offered again, and neither is a
// stoppage of an item something is already running: both would be the harness
// spending a slot to be refused.
func TestDecisionsNothingCanActOnAreNotOffered(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	live := stoppedState()
	live.RunID = "run-aaaabbbbccccddddeeeeffff00001111"
	live.Status = runstate.StatusRunning
	live.CompletedAt = nil
	live.Blocker = ""
	if err := harness.runs.Create(live); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	outstanding, err := harness.carryOut().Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want nothing offered for an item with a run in flight", outstanding)
	}
}

// The three decisions that ask for no run at all are not the harness's to fire.
// A carry-out that acted on one would be starting work nobody decided to start.
func TestOnlyTheTwoDecisionsThatAskForARunAreCarriedOut(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionWait, docketedRunID), docketedNow); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	outstanding, err := harness.carryOut().Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want nothing fired for a decision that asks for no run", outstanding)
	}
}

// yoyodyne-ifd.309's mechanism, exercised rather than assumed: a decision that
// only exists because a recorded cap crossing permitted it is carried out like
// any other, and the run's own account names the crossing it stands on.
func TestADecisionStandingOnARecordedCapCrossingIsCarriedOut(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	// The item is at the end of its one re-run, so a second decision is refused
	// until somebody crosses that cap.
	second := "run-cccc2222dddd3333eeee4444ffff5555"
	stopped := stoppedState()
	stopped.RunID = second
	if err := harness.runs.Create(stopped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := docketerOver(nil, harness.docket).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	ctx := context.Background()
	_, err := harness.runs.Triage().RecordRerun(ctx, docketedItem, triageDecided(runstate.TriageDecisionRerun, second), docketedNow, rerunCaps)
	if !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("RecordRerun() error = %v, want the cap to refuse the second decision", err)
	}
	if _, err := harness.runs.Triage().Override(ctx, docketedItem, runstate.TriageOverride{
		Budget:    runstate.TriageRerunBudget,
		Cap:       2,
		DecidedBy: "mason-bryant",
		Reason:    "the first re-run met a broken toolchain rather than the work",
	}, docketedNow, rerunCaps); err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	if _, err := harness.runs.Triage().RecordRerun(ctx, docketedItem, triageDecided(runstate.TriageDecisionRerun, second), docketedNow, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() after the crossing error = %v", err)
	}
	carrying := harness.carryOut()
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 2 {
		t.Fatalf("outstanding = %#v, want both decided stoppages offered", outstanding)
	}
	carried, _, err := carrying.Carry(ctx, outstanding[0])
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried {
		t.Fatalf("carried = %#v, want the decision the crossing permitted carried out", carried)
	}
	if !strings.Contains(carried.Reason, "operator override") || !strings.Contains(carried.Reason, "mason-bryant") {
		t.Fatalf("reason = %q, want the crossing the decision stands on named in the run's own account", carried.Reason)
	}
}

// A carry-out with nothing wired to it fires nothing rather than reporting that
// it did, which is the direction every optional part of a pull fails in.
func TestACarryOutWithoutItsPartsRefuses(t *testing.T) {
	t.Parallel()

	if _, err := (CarryOut{}).Outstanding(); err == nil {
		t.Fatalf("Outstanding() = nil, want a refusal naming what is missing")
	}
	if _, _, err := (CarryOut{}).Carry(context.Background(), CarryOutTask{}); err == nil {
		t.Fatalf("Carry() = nil, want a refusal naming what is missing")
	}
}

// A gate is named from the sentinel the action exports rather than from the words
// of its refusal, because a gate matched on wording is one that changes when
// somebody rewrites a sentence — and this one is written into a record somebody
// acts on.
func TestEveryGateIsNamedFromTheRefusalsSentinel(t *testing.T) {
	t.Parallel()

	for name, refusal := range map[string]struct {
		err  error
		want string
	}{
		"a worktree somebody has been in": {WorktreeSurgeryError{RunID: docketedRunID}, runstate.TriageGatePreservedWork},
		"a worktree holding no change":    {MissingPreservedChangeError{RunID: docketedRunID}, runstate.TriageGatePreservedWork},
		"an item no run may start on":     {ErrItemNotStartable, runstate.TriageGateWorkItem},
		"a budget that is spent":          {runstate.TriageCapError{Action: runstate.TriageRerun}, runstate.TriageGateBudget},
		"a stoppage already re-run":       {runstate.RerunTakenError{}, runstate.TriageGateBudget},
		"anything else":                   {errors.New("the store would not answer"), runstate.TriageGateHarness},
	} {
		gate, clears := carryOutGate(refusal.err)
		if gate != refusal.want {
			t.Fatalf("%s: gate = %q, want %q", name, gate, refusal.want)
		}
		if strings.TrimSpace(clears) == "" {
			t.Fatalf("%s: nothing says what would clear it, which is the whole of what a finding is worth", name)
		}
	}
	// Every gate the record permits is one this file declares, so a finding can
	// never name something the vocabulary does not have.
	for _, gate := range []string{runstate.TriageGateSpendingPause, runstate.TriageGateIntakeHold, runstate.TriageGateCapacity} {
		if !strings.Contains(strings.Join(runstate.TriageGateVocabulary(), "|"), gate) {
			t.Fatalf("gate %q is not in the vocabulary", gate)
		}
	}

}

// Outstanding and Carry stand in for the two halves of a carry-out: the reading
// that costs no slot, and the firing that takes one. They are separate here for
// the reason they are separate in the interface — the pass has to know there is
// something to fire before it spends a slot on finding out.
func (h *scheduleHarness) Outstanding() ([]CarryOutTask, error) {
	h.mu.Lock()
	outstanding := h.outstanding
	h.mu.Unlock()
	return outstanding(h)
}

func (h *scheduleHarness) Carry(_ context.Context, task CarryOutTask) (CarriedOut, Outcome, error) {
	h.mu.Lock()
	h.carried = append(h.carried, task)
	carry := h.carry
	h.mu.Unlock()
	return carry(h, task)
}

// RecordUnattempted keeps what each pull said it passed over, and writes
// nothing: what the real one writes is exercised against the real record.
func (h *scheduleHarness) RecordUnattempted(_ context.Context, _ time.Duration, passed map[string]string) ([]CarriedOut, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	copied := make(map[string]string, len(passed))
	for run, why := range passed {
		copied[run] = why
	}
	h.passedOver = append(h.passedOver, copied)
	// A pull never waits on a test that has stopped listening: the first
	// account is the one a test waits for, and a buffer of one holds it.
	select {
	case h.unattempted <- struct{}{}:
	default:
	}
	return nil, nil
}

// harnessPause is the operator's pause as the pull reads it, over the harness's
// own switch.
type harnessPause struct{ h *scheduleHarness }

func (p harnessPause) Held() (runstate.OperatorHold, bool, error) {
	p.h.mu.Lock()
	defer p.h.mu.Unlock()
	if !p.h.paused {
		return runstate.OperatorHold{}, false, nil
	}
	return runstate.OperatorHold{HeldAt: p.h.now}, true, nil
}

// outstandingUntilAttempted is the fake reading what the real one reads for a
// decision that fired or that a gate shut for one item refused: it is not
// outstanding on the next pull, because it is spent or its pacing has it. It is
// wrong for a gate shut for everything at once, which the record deliberately
// does not pace — see outstandingUntilFired for those.
func outstandingUntilAttempted(tasks ...CarryOutTask) func(*scheduleHarness) ([]CarryOutTask, error) {
	return func(h *scheduleHarness) ([]CarryOutTask, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		var waiting []CarryOutTask
		for _, task := range tasks {
			attempted := false
			for _, done := range h.carried {
				attempted = attempted || done.WorkItemID == task.WorkItemID
			}
			if !attempted {
				waiting = append(waiting, task)
			}
		}
		return waiting, nil
	}
}

// outstandingUntilFired is the fake reading what the real one reads for a
// decision a gate shut for everything at once refused: the record does not pace
// it, so it is offered again on every pull until it actually fires. It is what
// exercises the recurrence the pass has to bound, and the fired set is the test's
// own, kept by its carry.
func outstandingUntilFired(fired map[string]bool, tasks ...CarryOutTask) func(*scheduleHarness) ([]CarryOutTask, error) {
	return func(h *scheduleHarness) ([]CarryOutTask, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		var waiting []CarryOutTask
		for _, task := range tasks {
			if !fired[task.WorkItemID] {
				waiting = append(waiting, task)
			}
		}
		return waiting, nil
	}
}

// decidedTask is one recorded decision the pass may fire, named after the item
// it is about so a test reads the report the way an operator would.
func decidedTask(workItemID string) CarryOutTask {
	return CarryOutTask{
		WorkItemID: workItemID,
		RunID:      docketedRunID,
		DocketKey:  triage.Key(triage.ClassStoppedRun, docketedRunID),
		Decision:   runstate.TriageDecisionRerun,
		Reason:     rerunReasoning,
	}
}

// The pass fires a recorded decision itself, against the same capacity the
// queue's own work is chosen against: this is the whole of what "no person
// involved" means, and the run it starts is accounted for like any other.
func TestAPassCarriesOutARecordedDecisionBesideTheQueuesOwnWork(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
	harness.capacity = 2
	harness.outstanding = outstandingUntilAttempted(decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: "the development manager's triage decided a re-run, " + rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// Once, however many pulls the drain made: the item the decision is about is
	// occupied by the run this started, exactly as a pulled item is, so nothing
	// puts a second developer on it.
	if len(harness.carried) != 1 || harness.carried[0].WorkItemID != "yoyodyne-ifd.346" {
		t.Fatalf("carried = %#v, want the decision fired exactly once", harness.carried)
	}
	if len(schedule.CarriedOut) != 1 || !schedule.CarriedOut[0].Carried {
		t.Fatalf("carried out = %#v, want the pass to account for the decision it fired", schedule.CarriedOut)
	}
	// It is a run as well, so it is among the started runs with the reason the
	// action recorded rather than the placeholder the pass started it under.
	var started *Started
	for index := range schedule.Started {
		if schedule.Started[index].WorkItemID == "yoyodyne-ifd.346" {
			started = &schedule.Started[index]
		}
	}
	if started == nil {
		t.Fatalf("started = %#v, want the carried-out decision accounted for as a run", schedule.Started)
	}
	if !strings.Contains(started.Reason, rerunReasoning) {
		t.Fatalf("reason = %q, want the reasoning the decision was recorded with", started.Reason)
	}
	// And the queue's own work was pulled beside it, because a carry-out takes one
	// slot rather than the pass.
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %#v, want the queue's work pulled beside the decision", schedule.Started)
	}
}

// A session bounded by --limit fires no more decisions than the bound leaves,
// because every decision fired is a run started.
func TestAPullFiresNoMoreDecisionsThanTheSessionsLimitLeaves(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 3
	harness.outstanding = outstandingUntilAttempted(
		decidedTask("yoyodyne-ifd.346"), decidedTask("yoyodyne-ifd.347"), decidedTask("yoyodyne-ifd.348"))
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
	// One pull's worth: the bound the operator puts on a pass stops it at the
	// first run, which is the carry-out.
	schedule, err := (Scheduler{Open: harness.open, Limit: 1}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.carried) != 1 || harness.carried[0].WorkItemID != "yoyodyne-ifd.346" {
		t.Fatalf("carried = %#v, want one decision fired, the oldest first", harness.carried)
	}
	if schedule.Stopped != ScheduleLimitReached {
		t.Fatalf("stopped = %q, want the pass bounded by the limit it was given", schedule.Stopped)
	}
}

// decidedTaskOf is a recorded decision about a stoppage of its own, so a pull
// passing some of them over can say which.
func decidedTaskOf(workItemID, runID string) CarryOutTask {
	task := decidedTask(workItemID)
	task.RunID = runID
	task.DocketKey = triage.Key(triage.ClassStoppedRun, runID)
	return task
}

// A recorded decision is attempted on the first pull that has a slot for it,
// whatever its place on the docket, and one a pull could not reach is handed to
// the record with why rather than left unsaid. Until yoyodyne-ifd.428.39 a pull
// fired the oldest decision and nothing else, however many slots stood free.
func TestAPullAttemptsEveryDecisionItHasASlotForAndSaysWhyItPassedTheRest(t *testing.T) {
	t.Parallel()

	first := "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	second := "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	third := "run-cccccccccccccccccccccccccccccccc"
	harness := newScheduleHarness()
	harness.capacity = 2
	harness.outstanding = outstandingUntilAttempted(
		decidedTaskOf("yoyodyne-ifd.346", first), decidedTaskOf("yoyodyne-ifd.347", second), decidedTaskOf("yoyodyne-ifd.348", third))
	unattempted := make(chan struct{}, 1)
	harness.unattempted = unattempted
	reached := make(chan struct{}, 3)
	release := make(chan struct{})
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		// Held until the first pull has accounted for what it passed over, so the
		// slots it spent are still spent when it does.
		reached <- struct{}{}
		<-release
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
	done := make(chan struct{})
	var schedule Schedule
	var err error
	go func() {
		defer close(done)
		schedule, err = (Scheduler{Open: harness.open}).Schedule(context.Background())
	}()
	// The pull hands each decision it has a slot for to its own goroutine, so
	// it can account for what it passed over before either of those reaches
	// Carry. Wait for both: the carries are held on release, so any that has
	// arrived by now belongs to the first pull. Each is waited on rather than
	// polled for under a deadline, which a loaded machine can reach with the
	// pull working; a pull that never gets there is reported by the binary's
	// own -timeout, naming where it waited.
	<-unattempted
	<-reached
	<-reached
	harness.mu.Lock()
	var firstPull []string
	for _, task := range harness.carried {
		firstPull = append(firstPull, task.RunID)
	}
	var passed map[string]string
	if len(harness.passedOver) > 0 {
		passed = harness.passedOver[0]
	}
	harness.mu.Unlock()
	close(release)
	<-done
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(firstPull) != 2 {
		t.Fatalf("first pull carried %v, want both decisions it had a slot for attempted at once", firstPull)
	}
	if why := passed[third]; !strings.Contains(why, "developer slot") || len(passed) != 1 {
		t.Fatalf("passed over = %#v, want the third decision handed to the record with why the pull did not reach it", passed)
	}
	// And the pull after reaches it.
	if len(harness.carried) != 3 || len(schedule.CarriedOut) != 3 {
		t.Fatalf("carried = %#v, want every decision attempted by the pass", harness.carried)
	}
}

// A decision a gate stopped started nothing, so it must not be counted as a run
// that failed: pricing it would charge the session for a run that does not
// exist, and counting it toward the failure storm would have the brake hold
// intake because a decision was waiting on the intake hold.
func TestADecisionAGateStoppedIsNotARunThatFailed(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.blockedRuns = 1
	harness.outstanding = outstandingUntilAttempted(decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(_ *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Gate: runstate.TriageGateIntakeHold, Waiting: true,
			Problem: "the re-run is waiting on the operator's hold on what the harness chooses",
		}, Outcome{}, nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.CarriedOut) != 0 {
		t.Fatalf("carried out = %#v, want an attempt a gate stopped reported as a problem rather than as work done", schedule.CarriedOut)
	}
	if !strings.Contains(schedule.CarryOutProblem, runstate.TriageGateIntakeHold) {
		t.Fatalf("problem = %q, want the gate that stopped it named on the pass", schedule.CarryOutProblem)
	}
	if schedule.Failed() {
		t.Fatalf("schedule = %#v, want a stopped carry-out not to fail the pass", schedule)
	}
	if schedule.BlockedInARow != 0 || schedule.Braked != nil {
		t.Fatalf("blocked in a row = %d, braked = %#v, want nothing counted toward the failure storm", schedule.BlockedInARow, schedule.Braked)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Failure != "" || schedule.Started[0].Declined == "" {
		t.Fatalf("started = %#v, want a start that never became a run rather than a run that failed", schedule.Started)
	}
}

// A reading of the decisions that failed leaves the queue's own work exactly as
// it was and says so, because a decision fired on a record nobody could read
// would be the one thing worse than one that waits.
func TestAReadingOfTheDecisionsThatFailedDoesNotStopThePass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
	harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) {
		return nil, errors.New("the triage record would not be read")
	}
	harness.carry = func(_ *scheduleHarness, _ CarryOutTask) (CarriedOut, Outcome, error) {
		t.Fatalf("nothing should be fired from a reading that failed")
		return CarriedOut{}, Outcome{}, nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !strings.Contains(schedule.CarryOutReadProblem, "would not be read") {
		t.Fatalf("problem = %q, want the reading that failed said out loud", schedule.CarryOutReadProblem)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-ifd.500" {
		t.Fatalf("started = %#v, want the queue's own work pulled regardless", schedule.Started)
	}
}

// The finding reaches the development manager where she is already looking. It is
// joined onto the entry where the docket is read rather than written into the log,
// because the attempt is made after the decision, which is made after the entry.
func TestTheDocketCarriesTheGateThatStoppedTheCarryOut(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "closed"
	carrying := harness.carryOut()
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	docket := docketerDeciding(nil, harness.docket, harness.runs.Triage(), harness.reruns)
	built, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("entries = %#v, want the one docketed stoppage", built.Entries)
	}
	entry := built.Entries[0]
	if entry.CarryOut == nil {
		t.Fatalf("entry = %#v, want the refusal joined onto it", entry)
	}
	if entry.CarryOut.Gate != runstate.TriageGateWorkItem || entry.CarryOut.Decision != runstate.TriageDecisionRerun {
		t.Fatalf("carry-out = %#v, want the gate that refused and the decision it was carrying out", entry.CarryOut)
	}
	if !strings.Contains(entry.Render(), runstate.TriageGateWorkItem) {
		t.Fatalf("rendered entry does not name the gate:\n%s", entry.Render())
	}
	// And it goes once the decision is carried out, because a finding standing over
	// a run that is happening is the worst thing this could say.
	harness.item.Status = "open"
	later := harness.carryOutAt(runstate.TriageCarryOutRetryDelay + time.Minute)
	if _, _, err := later.Carry(context.Background(), theOneOutstanding(t, later)); err != nil {
		t.Fatalf("Carry() after the item was put back error = %v", err)
	}
	cleared, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if cleared.Entries[0].CarryOut != nil {
		t.Fatalf("carry-out = %#v, want the finding gone once the decision was carried out", cleared.Entries[0].CarryOut)
	}
}

// carryOut wires the firing of a repair over the state a repair-continue acts
// on, so what the pass fires and what the verb fires are one action.
func (h *continueHarness) carryOut() CarryOut {
	return CarryOut{
		Docket:    h.docket,
		Decisions: h.runs.Triage(),
		Reruns:    h.runs.Reruns(),
		Runs:      h.runs,
		Repairer:  h.continuer(),
		Clock:     docketClock{},
	}
}

// A save can replace the run file and then fail at the sync or read-back.
// Scheduling must find that unserved transition even though it has consumed
// the grant and occupies the only developer slot. No explicit Continue call
// makes any part of this recovery happen.
func TestSchedulingRecoversARepairWhoseReplacementWasNotConfirmed(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"sync failed after replacement", "read-back failed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			harness := newContinueHarness(t, continuableState())
			harness.capacity = 1
			runs := &repairRecordRuns{RepairRuns: harness.runs}
			failure := errors.New(mode)
			if mode == "sync failed after replacement" {
				runs.saveErr, runs.writeBeforeErr = failure, true
			} else {
				runs.loadErr = failure
			}
			claims := 0
			harness.tracker.OnClaim = func() error { claims++; return nil }
			continuer := harness.continuer()
			continuer.Runs = runs
			continuer.Items = &repairRecordItems{Tracker: harness.tracker}
			carrying := harness.carryOut()
			carrying.Repairer = continuer
			pulls := newScheduleHarness()
			scheduler := Scheduler{Limit: 1, Open: func(ctx context.Context) (Pull, error) {
				pull, err := pulls.open(ctx)
				pull.Runs, pull.CarryOut = harness.runs, carrying
				return pull, err
			}}
			pass := func() Schedule {
				t.Helper()
				schedule, err := scheduler.Schedule(context.Background())
				if err != nil {
					t.Fatalf("Schedule() = %v: %s", err, schedule.Render())
				}
				return schedule
			}
			first := pass()
			if len(first.Started) != 1 || first.Started[0].Declined == "" || len(first.CarriedOut) != 0 || len(harness.started) != 0 {
				t.Fatalf("first pass = %s, dispatches = %+v; want an unconfirmed transition with no dispatch", first.Render(), harness.started)
			}
			if strings.Contains(harness.tracker.Notes, "and the harness re-entered") {
				t.Fatalf("an uncertain save announced success: %s", harness.tracker.Notes)
			}
			pending := harness.reload(t)
			if !pending.RepairDispatchPending() || len(pending.RepairContinuations) != 1 || claims != 1 {
				t.Fatalf("pending = %+v, claims = %d; want one charged, unserved continuation", pending, claims)
			}
			// The transient refusal is paced rather than immediately repeated.
			if paced := pass(); len(paced.Started) != 0 {
				t.Fatalf("the refusal was retried before its pacing passed: %s", paced.Render())
			}
			carrying.Clock = laterClock{after: runstate.TriageCarryOutRetryDelay + time.Minute}
			_, lease, err := harness.runs.AdoptRun(context.Background(), docketedRunID)
			if err != nil {
				t.Fatal(err)
			}
			// A live pipeline carrying the same pending record must not be
			// dispatched beside itself by another scheduling pass.
			leased := pass()
			lease.Release()
			if len(leased.Started) != 0 {
				t.Fatalf("a live leased run was offered for recovery: %s", leased.Render())
			}
			if task := theOneOutstanding(t, carrying); !task.Recover {
				t.Fatalf("task = %+v; want recovery of the already charged continuation", task)
			}
			// Another pass with storage still failing must remain unconfirmed,
			// rather than treating a readable replacement as a durable save.
			stillFailing := pass()
			if len(stillFailing.Started) != 1 || stillFailing.Started[0].Declined == "" || len(stillFailing.CarriedOut) != 0 || len(harness.started) != 0 || strings.Contains(harness.tracker.Notes, "and the harness re-entered") {
				t.Fatalf("recovery before storage cleared = %s; want no success or dispatch", stillFailing.Render())
			}
			// Once the storage failure clears, the next ordinary pass confirms
			// the same record before dispatching it, in its already held slot.
			runs.saveErr, runs.loadErr = nil, nil
			carrying.Clock = laterClock{after: 2 * (runstate.TriageCarryOutRetryDelay + time.Minute)}
			continuer.Start = func(ctx context.Context, workItemID, runID string) (Outcome, error) {
				state, lease, err := harness.runs.AdoptRun(ctx, runID)
				if err != nil {
					return Outcome{}, err
				}
				defer lease.Release()
				if !state.RepairDispatchPending() || len(state.RepairContinuations) != 1 || state.RepairAttempts != pending.RepairAttempts {
					t.Errorf("dispatched state = %+v; want the original continuation confirmed without another attempt", state)
				}
				if tasks, err := carrying.Outstanding(); err != nil || len(tasks) != 0 {
					t.Errorf("outstanding while dispatched = %+v, %v; want the lease to prevent another dispatch", tasks, err)
				}
				harness.started = append(harness.started, continuedRun{workItemID: workItemID, runID: runID})
				return harness.outcome, nil
			}
			carrying.Repairer = continuer
			recovered := pass()
			if len(recovered.Started) != 1 || len(recovered.CarriedOut) != 1 || !recovered.CarriedOut[0].Carried || len(harness.started) != 1 {
				t.Fatalf("recovered pass = %s, dispatches = %+v; want exactly one dispatch", recovered.Render(), harness.started)
			}
			state := harness.reload(t)
			if state.RepairDispatchPending() || len(state.RepairContinuations) != 1 || state.RepairAttempts != pending.RepairAttempts || claims != 1 || harness.carried(t) != continueGrantRounds {
				t.Fatalf("recovered = %+v, claims = %d; want one continuation, claim, and budget expenditure", state, claims)
			}
			if spent := harness.spent(t); spent.RepairGrants != 1 || spent.GrantedRounds != continueGrantRounds {
				t.Fatalf("triage = %+v; recovery granted another repair", spent)
			}
			if next := pass(); len(next.Started) != 0 || len(harness.started) != 1 {
				t.Fatalf("a served continuation was dispatched again: %s", next.Render())
			}
		})
	}
}

func TestSchedulingRecoversCompletedRepairNotesAfterDispatchAcknowledgementFailure(t *testing.T) {
	t.Parallel()
	for _, written := range []bool{false, true} {
		t.Run(fmt.Sprintf("note-written=%t", written), func(t *testing.T) {
			t.Parallel()
			harness := newContinueHarness(t, continuableState())
			items := &repairRecordItems{Tracker: harness.tracker}
			items.onRecord = func(note string) error {
				if !strings.Contains(note, "and the harness re-entered") {
					return nil
				}
				if written {
					_, _ = items.Tracker.RecordOutcome(context.Background(), docketedItem, note)
					items.Item.Notes = items.Notes
				}
				return errors.New("the success note could not be confirmed")
			}
			runs := &repairRecordRuns{RepairRuns: harness.runs}
			continuer := harness.continuer()
			continuer.Items, continuer.Runs = items, runs
			start := continuer.Start
			continuer.Start = func(ctx context.Context, workItemID, runID string) (Outcome, error) {
				outcome, err := start(ctx, workItemID, runID)
				// The pipeline saves completion before returning its accepted dispatch.
				state, lease, adoptErr := harness.runs.AdoptRun(ctx, runID)
				if adoptErr != nil {
					t.Fatal(adoptErr)
				}
				state.Status, state.Phase = runstate.StatusSucceeded, runstate.PhaseComplete
				completed := docketedNow.Add(time.Minute)
				state.CompletedAt, state.UpdatedAt = &completed, completed
				harness.save(t, state)
				lease.Release()
				harness.tracker.Item.Status, harness.tracker.Closed = "closed", true
				runs.saveErr = errors.New("the dispatch acknowledgement could not be saved")
				return outcome, err
			}
			first, err := continuer.Continue(context.Background(), continueRequest())
			if err != nil || !strings.Contains(first.RecordProblem, "success note") || !strings.Contains(first.RecordProblem, "dispatch acknowledgement could not be saved") {
				t.Fatalf("first = %+v, %v; want both recording failures", first, err)
			}
			before := harness.reload(t)
			if !before.Status.Terminal() || !before.RepairContinuations[0].DispatchPending || !before.RepairSuccessNotePending() {
				t.Fatalf("run = %+v; want completion with both acknowledgements pending", before)
			}
			spent := harness.spent(t)
			runs.saveErr = nil
			if !written {
				repeated, err := continuer.Continue(context.Background(), continueRequest())
				if err != nil || !repeated.AlreadyContinued || repeated.RecordProblem == "" || len(harness.started) != 1 || !reflect.DeepEqual(harness.reload(t), before) {
					t.Fatalf("repeated = %+v, %v; want the completed execution reported without another dispatch or transition", repeated, err)
				}
			}
			harness.docket.entries = nil
			carrying := harness.carryOut()
			carrying.Repairer = continuer
			pulls := newScheduleHarness()
			scheduler := Scheduler{Limit: 1, Open: func(ctx context.Context) (Pull, error) {
				pull, err := pulls.open(ctx)
				pull.Runs, pull.CarryOut = harness.runs, carrying
				return pull, err
			}}
			pass := func() Schedule {
				t.Helper()
				schedule, err := scheduler.Schedule(context.Background())
				if err != nil || len(schedule.Started) != 0 || len(harness.started) != 1 {
					t.Fatalf("Schedule() = %s, %v; want note recovery without another dispatch", schedule.Render(), err)
				}
				return schedule
			}
			_, lease, err := harness.runs.AdoptRun(context.Background(), docketedRunID)
			if err != nil {
				t.Fatal(err)
			}
			pass()
			lease.Release()
			if !reflect.DeepEqual(harness.reload(t), before) {
				t.Fatal("note recovery changed a leased completed run")
			}
			if !written {
				if failed := pass(); failed.CarryOutNoteProblem == "" || !harness.reload(t).RepairSuccessNotePending() {
					t.Fatalf("failed note retry = %s; want the missing note failure to remain recoverable", failed.Render())
				}
			}
			items.onRecord = nil
			if recovered := pass(); recovered.CarryOutNoteProblem != "" {
				t.Fatalf("recovered = %s; want the note confirmed", recovered.Render())
			}
			after := harness.reload(t)
			if after.RepairSuccessNotePending() || after.RepairContinuations[0].DispatchPending || strings.Count(items.Notes, first.Reason) != 1 {
				t.Fatalf("state = %+v, notes = %q; want both acknowledgements confirmed and one success note", after, items.Notes)
			}
			expected := before
			expected.RepairContinuations = append([]runstate.RepairContinuation(nil), before.RepairContinuations...)
			expected.RepairContinuations[0].DispatchPending, expected.RepairContinuations[0].SuccessNotePending = false, false
			if !reflect.DeepEqual(after, expected) || !reflect.DeepEqual(harness.spent(t), spent) {
				t.Fatal("note recovery changed the continuation, execution outcome, or expenditure")
			}
			calls := append([]string(nil), items.Calls...)
			pass()
			if !reflect.DeepEqual(harness.reload(t), after) || !reflect.DeepEqual(items.Calls, calls) {
				t.Fatal("a later pass repeated confirmed note delivery")
			}
		})
	}
}

func TestSchedulingRecoversRepairSuccessNotesWithoutAnotherDispatch(t *testing.T) {
	t.Parallel()
	for _, terminal := range []bool{false, true} {
		for _, written := range []bool{false, true} {
			t.Run(fmt.Sprintf("terminal=%t/note-written=%t", terminal, written), func(t *testing.T) {
				t.Parallel()
				harness := newContinueHarness(t, continuableState())
				items := &repairRecordItems{Tracker: harness.tracker}
				items.onRecord = func(note string) error {
					if !strings.Contains(note, "and the harness re-entered") {
						return nil
					}
					if written {
						_, _ = items.Tracker.RecordOutcome(context.Background(), docketedItem, note)
						items.Item.Notes = items.Notes
					}
					return errors.New("the success note could not be confirmed")
				}
				continuer := harness.continuer()
				continuer.Items = items
				first, err := continuer.Continue(context.Background(), continueRequest())
				if err != nil || first.RecordProblem == "" {
					t.Fatalf("first = %+v, %v; want accepted dispatch with an unconfirmed note", first, err)
				}
				before := harness.reload(t)
				if terminal {
					before.Status, before.Phase = runstate.StatusSucceeded, runstate.PhaseComplete
					completed := docketedNow.Add(time.Minute)
					before.CompletedAt = &completed
					harness.save(t, before)
					// Completed runs need no current docket entry to recover a note.
					harness.docket.entries = nil
				}
				carrying := harness.carryOut()
				carrying.Repairer = continuer
				pulls := newScheduleHarness()
				scheduler := Scheduler{Limit: 1, Open: func(ctx context.Context) (Pull, error) {
					pull, err := pulls.open(ctx)
					pull.Runs, pull.CarryOut = harness.runs, carrying
					return pull, err
				}}
				pass := func() Schedule {
					t.Helper()
					schedule, err := scheduler.Schedule(context.Background())
					if err != nil || len(schedule.Started) != 0 || len(harness.started) != 1 {
						t.Fatalf("Schedule() = %s, %v; want only note recovery and no dispatch", schedule.Render(), err)
					}
					return schedule
				}
				_, lease, err := harness.runs.AdoptRun(context.Background(), docketedRunID)
				if err != nil {
					t.Fatal(err)
				}
				pass()
				lease.Release()
				if !harness.reload(t).RepairSuccessNotePending() {
					t.Fatal("a note was recovered beside a live run lease")
				}
				if !written {
					if failed := pass(); failed.CarryOutNoteProblem == "" || !harness.reload(t).RepairSuccessNotePending() {
						t.Fatalf("failed note retry = %s; want the pending note failure reported", failed.Render())
					}
				}
				items.onRecord = nil
				if recovered := pass(); recovered.CarryOutNoteProblem != "" {
					t.Fatalf("recovered = %s; want the note confirmed", recovered.Render())
				}
				after := harness.reload(t)
				if after.RepairSuccessNotePending() || after.RepairDispatchPending() || after.RepairAttempts != before.RepairAttempts || len(after.RepairContinuations) != 1 || after.Status != before.Status || strings.Count(items.Notes, first.Reason) != 1 {
					t.Fatalf("state = %+v, notes = %q; want one success note and the same continuation and outcome", after, items.Notes)
				}
				calls := append([]string(nil), items.Calls...)
				pass()
				if !reflect.DeepEqual(harness.reload(t), after) || !reflect.DeepEqual(items.Calls, calls) {
					t.Fatal("a later pass repeated confirmed note delivery")
				}
			})
		}
	}
}

// grantedAgainstTheStoppage records the repair the development manager decided
// about the docketed stoppage itself, which is what her conversation writes: a
// decision naming another run of the same item is refused where it is recorded.
func grantedAgainstTheStoppage(t *testing.T, harness *continueHarness) {
	t.Helper()
	if _, err := harness.runs.Triage().GrantRepair(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRepair, docketedRunID), continueGrantRounds, docketedNow, continueCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
}

// The other half of the vocabulary the harness fires, and the half where the
// reasoning matters most: a repair records the development manager's words on the
// run and on the item, so they have to be the words she wrote rather than
// anything the pass composed.
func TestARecordedRepairIsCarriedOutOnTheReasoningSheRecorded(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	grantedAgainstTheStoppage(t, harness)
	carrying := harness.carryOut()
	task := theOneOutstanding(t, carrying)
	if task.Decision != runstate.TriageDecisionRepair || task.Reason != rerunReasoning {
		t.Fatalf("task = %#v, want the repair decided about this stoppage and the reasoning it was recorded with", task)
	}
	carried, outcome, err := carrying.Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want the stopped run continued once", carried, harness.started)
	}
	// The same run rather than a fresh one, which is the whole difference between
	// the two decisions the harness fires.
	if harness.started[0].runID != docketedRunID {
		t.Fatalf("started = %#v, want the stopped run re-entered rather than a fresh one", harness.started)
	}
	if outcome.RunID != docketedRunID {
		t.Fatalf("outcome = %#v, want the continued run", outcome)
	}
	if !strings.Contains(carried.Reason, rerunReasoning) {
		t.Fatalf("reason = %q, want the reasoning read from the record she wrote", carried.Reason)
	}
	// The grant is carried out, so nothing offers the same decision again.
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want the grant spent", outstanding)
	}
}

// The preserved-work rule the item names, met by the pass rather than by a
// person: what is in that worktree is what a continued developer would be handed
// back, so a repair the harness cannot make safely says so and spends nothing.
func TestAPreservedWorktreeSomebodyHasBeenInStopsTheCarryOutAndSaysWhy(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	grantedAgainstTheStoppage(t, harness)
	harness.ownership.Err = errors.New("HEAD is a commit the harness did not make")
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want the refusal reported rather than raised", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing continued in a worktree somebody has been in", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGatePreservedWork || carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the preserved work named as a gate a person has to open", carried.Gate, carried.Waiting)
	}
	if harness.carried(t) != 0 {
		t.Fatalf("the grant was spent on a repair that never happened")
	}
}

// The gate the pass itself would otherwise swallow. A held intake stops the pull
// before it chooses anything, so a carry-out placed after that would never be
// attempted while the hold stood — and a decision that cannot be carried out with
// nothing anywhere saying why is the one outcome this mechanism forbids. Nothing
// is claimed under the hold: the action reads it again and refuses, and what the
// pass keeps is the refusal.
func TestAHeldIntakeStillAttemptsTheCarryOutSoTheRefusalIsRecorded(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
	harness.capacity = 2
	harness.held = &runstate.IntakeHold{HeldAt: harness.now, Reason: "the queue is heading somewhere odd"}
	harness.outstanding = outstandingUntilAttempted(decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(_ *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		// What the action reports when it reads the same hold the pass just read.
		// Nothing is claimed and nothing is started; the finding is the whole of it.
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Gate: runstate.TriageGateIntakeHold, Waiting: true,
			Problem: "the \"rerun\" the development manager decided is waiting on " + runstate.TriageGateIntakeHold,
		}, Outcome{}, nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.carried) != 1 {
		t.Fatalf("carried = %#v, want the decision attempted so the hold has somewhere to be recorded", harness.carried)
	}
	if !strings.Contains(schedule.CarryOutProblem, runstate.TriageGateIntakeHold) {
		t.Fatalf("problem = %q, want the hold named as the gate that stopped it", schedule.CarryOutProblem)
	}
	// The pass still chose nothing, which is what the hold is for.
	if schedule.Stopped != ScheduleIntakeHeld || schedule.IntakeHeld == nil {
		t.Fatalf("stopped = %q, held = %#v, want the pass to have chosen nothing under the hold", schedule.Stopped, schedule.IntakeHeld)
	}
	for _, started := range schedule.Started {
		if started.WorkItemID == "yoyodyne-ifd.500" {
			t.Fatalf("started = %#v, want no queue work chosen under a held intake", schedule.Started)
		}
	}
}

// The same for the other gate the pass short-circuits on. A full harness stops
// the pull before it reaches the queue, so a carry-out placed after that would be
// silent for as long as every slot was taken.
func TestAFullHarnessStillAttemptsTheCarryOutSoTheRefusalIsRecorded(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 1
	// One slot, taken by a run this pass did not start.
	harness.inFlight["yoyodyne-ifd.400"] = runningState("run-aaaabbbbccccddddeeeeffff00002222", "yoyodyne-ifd.400")
	// The record offers a decision a full harness stopped on every pull, which is
	// what a drain that attempted every offer would loop on for ever.
	harness.outstanding = outstandingUntilFired(map[string]bool{}, decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(_ *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Gate: runstate.TriageGateCapacity, Waiting: true,
			Problem: "the \"rerun\" the development manager decided is waiting on " + runstate.TriageGateCapacity,
		}, Outcome{}, nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.carried) != 1 {
		t.Fatalf("carried = %#v, want the decision attempted once so the full harness has somewhere to be recorded, and not again while it stays full", harness.carried)
	}
	if schedule.Stopped != ScheduleCapacityFull {
		t.Fatalf("stopped = %q, want the drain to end on the full harness rather than loop on the decision it stopped", schedule.Stopped)
	}
	if !strings.Contains(schedule.CarryOutProblem, runstate.TriageGateCapacity) {
		t.Fatalf("problem = %q, want developer capacity named as the gate that stopped it", schedule.CarryOutProblem)
	}
}

// A reading that failed and an attempt that fired are different facts about one
// pass, and the pass has to keep both: folding them into one line had the
// successful attempt erase the account of the decision nothing could read.
func TestAFiredDecisionDoesNotEraseAReadingThatFailed(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 2
	readable := outstandingUntilAttempted(decidedTask("yoyodyne-ifd.346"))
	harness.outstanding = func(h *scheduleHarness) ([]CarryOutTask, error) {
		tasks, _ := readable(h)
		// Part of the record answered and part of it did not, which is what
		// Outstanding reports when one item's triage record cannot be read.
		return tasks, errors.New("the triage record of yoyodyne-ifd.347 would not be read")
	}
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.CarriedOut) != 1 {
		t.Fatalf("carried out = %#v, want the decision that could be read fired", schedule.CarriedOut)
	}
	if !strings.Contains(schedule.CarryOutReadProblem, "yoyodyne-ifd.347") {
		t.Fatalf("read problem = %q, want the decision nothing could read still accounted for", schedule.CarryOutReadProblem)
	}
	if schedule.CarryOutProblem != "" {
		t.Fatalf("problem = %q, want nothing said about a gate, since none stopped the attempt", schedule.CarryOutProblem)
	}
}

// A gate is the waiting kind when it is shut for every recorded decision at once
// and the other kind when it is shut for one item, whoever opens either. Getting
// that backwards is not cosmetic: the waiting kind is unpaced, so a per-item gate
// classified as waiting takes the pass's single carry-out on every poll for as
// long as it stands.
func TestOnlyThePausesShutForEveryDecisionAreTheWaitingKind(t *testing.T) {
	t.Parallel()

	for name, met := range map[string]struct {
		outcome Outcome
		gate    string
		waiting bool
	}{
		"the operator paused everything the harness spends": {
			Outcome{PausedByOperator: &runstate.OperatorHold{HeldAt: docketedNow}}, runstate.TriageGateSpendingPause, true,
		},
		"the operator held what the harness chooses": {
			Outcome{PausedByIntake: &runstate.IntakeHold{HeldAt: docketedNow}}, runstate.TriageGateIntakeHold, true,
		},
		"a directive pauses this item": {
			Outcome{PausedByDirective: &directive.Directive{ID: "dir-0123456789abcdef"}}, runstate.TriageGateDirective, false,
		},
		"this item waits on other work": {
			Outcome{PausedByDependency: &runstate.DependencyPause{}}, runstate.TriageGateWorkItem, false,
		},
	} {
		gate, clears, waiting := pausedGate(met.outcome)
		if gate != met.gate {
			t.Fatalf("%s: gate = %q, want %q", name, gate, met.gate)
		}
		if waiting != met.waiting {
			t.Fatalf("%s: waiting = %t, want %t — a gate shut for one item has to be paced, and one shut for everything must not be",
				name, waiting, met.waiting)
		}
		if strings.TrimSpace(clears) == "" {
			t.Fatalf("%s: nothing says what would clear it", name)
		}
	}
}

// The starvation this classification exists to stop. A directive pauses one item,
// so a decision about it can never fire until somebody resolves the directive —
// and the pass carries one decision out per pull. Left unpaced it would take every
// pull's carry-out for ever and no other decided stoppage would ever be reached,
// which is the standing backlog never clearing.
func TestADecisionADirectivePausesDoesNotPinThePass(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	// A second decided stoppage, of a different item, waiting behind the first.
	behind := stoppedState()
	behind.RunID = "run-dddd4444eeee5555ffff666600007777"
	behind.WorkItemID = "yoyodyne-ifd.347"
	if err := harness.runs.Create(behind); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := docketerOver(nil, harness.docket).RecordStoppedRun(behind); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	recordRerunDecision(t, harness.runs, behind.WorkItemID, behind.RunID)

	// The fresh run of the first item meets the directive where it would have
	// started, so nothing is reserved and the claim is given back.
	harness.outcome = Outcome{Paused: true, PausedByDirective: &directive.Directive{ID: "dir-0123456789abcdef"}}
	carrying := harness.carryOut()
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 2 || outstanding[0].WorkItemID != docketedItem {
		t.Fatalf("outstanding = %#v, want both decisions offered, oldest first", outstanding)
	}
	carried, _, err := carrying.Carry(context.Background(), outstanding[0])
	if err != nil {
		t.Fatalf("Carry() error = %v, want the directive reported rather than a failure", err)
	}
	if carried.Carried {
		t.Fatalf("carried = %#v, want nothing started while a directive pauses the item", carried)
	}
	if carried.Gate != runstate.TriageGateDirective || carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the directive named as a gate somebody has to open", carried.Gate, carried.Waiting)
	}

	// The next pull is offered the decision behind it rather than the same one
	// again: that is the whole of what the pacing buys, since the pass carries one
	// decision out per pull.
	next, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(next) != 1 || next[0].WorkItemID != behind.WorkItemID {
		t.Fatalf("outstanding = %#v, want only the decision behind the paused one", next)
	}
	// And the paused one is not abandoned: it comes back once its pacing has passed,
	// so resolving the directive is carried out without anybody asking.
	later, err := harness.carryOutAt(runstate.TriageCarryOutRetryDelay + time.Minute).Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(later) != 2 {
		t.Fatalf("outstanding = %#v, want the paused decision offered again once its pacing has passed", later)
	}
}

// The recurrence the record leaves unpaced, bounded by the pass. A gate shut for
// everything at once is not paced in the item's record, so the same decision is
// offered on every pull the gate stands — and a pass that attempted every offer
// would append a started entry and rewrite the item's record once per poll
// interval for the whole length of a pause. What the pass owes instead is one
// attempt per closing of the gate, so the refusal is on the record, and the
// decision fired on the first pull after the gate opens, so the latency the
// unpaced record exists to keep is kept.
func TestAWaitingGateStopsADecisionOnceAndFiresItThePullTheGateOpens(t *testing.T) {
	t.Parallel()

	for _, gate := range []struct {
		name string
		gate string
		// shut closes the gate before the session starts, open opens it between
		// polls, and closed reports what the carry-out meets when it is attempted.
		shut   func(h *scheduleHarness)
		open   func(h *scheduleHarness)
		closed func(h *scheduleHarness) bool
	}{
		{
			name: "the intake hold",
			gate: runstate.TriageGateIntakeHold,
			shut: func(h *scheduleHarness) {
				h.held = &runstate.IntakeHold{HeldAt: h.now, Reason: "the queue is heading somewhere odd"}
			},
			open:   func(h *scheduleHarness) { h.release() },
			closed: func(h *scheduleHarness) bool { return h.held != nil },
		},
		{
			name:   "the operator's pause",
			gate:   runstate.TriageGateSpendingPause,
			shut:   func(h *scheduleHarness) { h.seePaused, h.paused = true, true },
			open:   func(h *scheduleHarness) { h.mu.Lock(); h.paused = false; h.mu.Unlock() },
			closed: func(h *scheduleHarness) bool { return h.paused },
		},
		{
			name: "a full harness",
			gate: runstate.TriageGateCapacity,
			shut: func(h *scheduleHarness) {
				h.inFlight["yoyodyne-ifd.400"] = runningState("run-aaaabbbbccccddddeeeeffff00002222", "yoyodyne-ifd.400")
			},
			open:   func(h *scheduleHarness) { h.mu.Lock(); delete(h.inFlight, "yoyodyne-ifd.400"); h.mu.Unlock() },
			closed: func(h *scheduleHarness) bool { return len(h.inFlight) > 0 },
		},
	} {
		t.Run(gate.name, func(t *testing.T) {
			t.Parallel()

			harness := newScheduleHarness()
			harness.capacity = 1
			gate.shut(harness)
			fired := map[string]bool{}
			harness.outstanding = outstandingUntilFired(fired, decidedTask("yoyodyne-ifd.346"))
			harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
				h.mu.Lock()
				stillShut := gate.closed(h)
				h.mu.Unlock()
				if stillShut {
					// What the action reports when it reads the same switch the pass just
					// read: nothing claimed, nothing started, the finding written.
					return CarriedOut{
						WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
						Gate: gate.gate, Waiting: true,
						Problem: "the \"rerun\" the development manager decided is waiting on " + gate.gate,
					}, Outcome{}, nil
				}
				h.mu.Lock()
				fired[task.WorkItemID] = true
				h.mu.Unlock()
				return CarriedOut{
					WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
					Carried: true, Reason: rerunReasoning,
				}, h.complete(task.WorkItemID), nil
			}
			// Three polls with the gate shut, then it opens; the session is stopped
			// two polls after that, once the decision has had a pull to fire on.
			harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
				if sleeps == 3 {
					gate.open(h)
				}
				return sleeps < 5
			}
			// The limit is a guard rather than the test: a pass that attempted the
			// decision on every pull never sleeps — each attempt is a run to collect
			// — so without it a regression would hang here rather than fail.
			schedule, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Limit: 4}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if schedule.Stopped == ScheduleLimitReached {
				t.Fatalf("the pass attempted the decision on every pull %s stood: %#v", gate.name, harness.carried)
			}
			// Once while the gate stood, however many polls it stood for, and once
			// more to fire it: each attempt is a durable write and a started entry, so
			// one per poll would be the pause's length in both.
			if len(harness.carried) != 2 {
				t.Fatalf("carried = %d attempt(s) (%#v), want one refused while %s stood and one fired when it opened", len(harness.carried), harness.carried, gate.name)
			}
			if len(schedule.Started) != 2 || schedule.Started[0].Declined == "" || schedule.Started[1].Declined != "" {
				t.Fatalf("started = %#v, want one start that never became a run and then one that did", schedule.Started)
			}
			if len(schedule.CarriedOut) != 1 || !schedule.CarriedOut[0].Carried {
				t.Fatalf("carried out = %#v, want the decision fired once %s opened", schedule.CarriedOut, gate.name)
			}
			// The finding is not left standing on the pass once the decision fired.
			if schedule.CarryOutProblem != "" {
				t.Fatalf("problem = %q, want nothing said about a gate once the decision fired", schedule.CarryOutProblem)
			}
		})
	}
}

// The pass's memory is of the gate it saw, not of the decision: a decision one
// gate stopped is attempted again the pull that gate opens, even if the attempt
// then meets a different one, and what the pass remembers is the gate it met
// last. A memory that outlived the gate would be a decision this session never
// fires.
func TestAWaitingGateRefusalIsForgottenTheMomentThatGateOpens(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 1
	harness.seePaused, harness.paused = true, true
	fired := map[string]bool{}
	harness.outstanding = outstandingUntilFired(fired, decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		// What the action reports is read from the same switches the pass reads,
		// in the order the action asks them: the pause first, then the hold.
		h.mu.Lock()
		paused, held := h.paused, h.held != nil
		h.mu.Unlock()
		gate := ""
		switch {
		case paused:
			gate = runstate.TriageGateSpendingPause
		case held:
			gate = runstate.TriageGateIntakeHold
		}
		if gate != "" {
			return CarriedOut{
				WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
				Gate: gate, Waiting: true, Problem: "waiting on " + gate,
			}, Outcome{}, nil
		}
		h.mu.Lock()
		fired[task.WorkItemID] = true
		h.mu.Unlock()
		return CarriedOut{WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision, Carried: true, Reason: rerunReasoning},
			h.complete(task.WorkItemID), nil
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		switch sleeps {
		case 2:
			// The pause lifts and the hold goes up in the same gap between polls, so
			// the attempt the lifted pause earns meets the hold instead.
			h.mu.Lock()
			h.paused = false
			h.held = &runstate.IntakeHold{HeldAt: h.now, Reason: "held between polls"}
			h.mu.Unlock()
		case 4:
			h.release()
		}
		return sleeps < 6
	}
	schedule, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Limit: 5}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped == ScheduleLimitReached {
		t.Fatalf("the pass attempted the decision on every pull a gate stood: %#v", harness.carried)
	}
	if len(harness.carried) != 3 {
		t.Fatalf("carried = %d attempt(s), want one per gate closing and one to fire: %#v", len(harness.carried), harness.carried)
	}
	if len(schedule.CarriedOut) != 1 {
		t.Fatalf("carried out = %#v, want the decision fired once both gates had opened", schedule.CarriedOut)
	}
}

// The docket a decision closes is the docket the finding has to reach. A
// repair or a re-run closes the entry it settles, so a finding joined onto a
// settled entry nobody lists is a finding nobody reads — and a decision the
// harness tried and a gate stopped is a question for the development manager
// again, carrying the decision she made and the gate that stopped it. One
// nothing has been stopped on stays settled, and one whose finding was cleared
// by the carry-out leaves the docket again.
func TestASettledEntryWhoseCarryOutAGateStoppedIsAQuestionAgain(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	docket := docketerDeciding(nil, harness.docket, harness.runs.Triage(), harness.reruns)
	entryKey := triage.Key(triage.ClassStoppedRun, docketedRunID)
	// The decision closes the entry, as her conversation's decision does.
	harness.docket.close(entryKey, runstate.TriageDecisionRerun, docketedNow)
	settled, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(settled.Entries) != 0 || settled.Closed != 1 {
		t.Fatalf("entries = %#v, closed = %d, want a decided stoppage nothing has been stopped on off the docket", settled.Entries, settled.Closed)
	}

	// The harness tries to carry the decision out after it was made, and the
	// item's own state refuses it.
	harness.item.Status = "closed"
	refusing := harness.carryOutAt(time.Minute)
	if _, _, err := refusing.Carry(context.Background(), theOneOutstanding(t, refusing)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	asked, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(asked.Entries) != 1 || asked.Closed != 0 {
		t.Fatalf("entries = %#v, closed = %d, want the settled stoppage listed again with its refused carry-out", asked.Entries, asked.Closed)
	}
	entry := asked.Entries[0]
	if entry.Closed == nil || entry.Closed.Decision != runstate.TriageDecisionRerun {
		t.Fatalf("entry = %#v, want it to carry the decision that settled it, so she reads it as her own decision not happening", entry)
	}
	if entry.CarryOut == nil || entry.CarryOut.Gate != runstate.TriageGateWorkItem {
		t.Fatalf("entry = %#v, want the gate that stopped the carry-out on it", entry)
	}
	rendered := entry.Render()
	for _, want := range []string{`Decided: "rerun"`, `tried to carry out the "rerun" you decided`, runstate.TriageGateWorkItem, "What would clear it"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry does not say %q:\n%s", want, rendered)
		}
	}

	// The item is put back and the decision fires, which clears the finding: the
	// stoppage is settled again and off the docket.
	harness.item.Status = "open"
	firing := harness.carryOutAt(runstate.TriageCarryOutRetryDelay + time.Minute)
	carried, _, err := firing.Carry(context.Background(), theOneOutstanding(t, firing))
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried {
		t.Fatalf("carried = %#v, want the decision fired once the item was put back", carried)
	}
	fired, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(fired.Entries) != 0 || fired.Closed != 1 {
		t.Fatalf("entries = %#v, closed = %d, want the stoppage settled again once its decision was carried out", fired.Entries, fired.Closed)
	}
}

// A gate that clears on its own lists the entry too, worded as waiting: a
// decision waiting on the operator's hold is still a decision that is not
// happening, and the one outcome this forbids is the docket saying nothing.
func TestASettledEntryWaitingOnAGateIsListedAsWaiting(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	docket := docketerDeciding(nil, harness.docket, harness.runs.Triage(), harness.reruns)
	harness.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRerun, docketedNow)
	if _, err := harness.intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere odd", docketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	waiting := harness.carryOutAt(time.Minute)
	if _, _, err := waiting.Carry(context.Background(), theOneOutstanding(t, waiting)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	built, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 || built.Entries[0].CarryOut == nil || !built.Entries[0].CarryOut.Waiting {
		t.Fatalf("entries = %#v, want the decided stoppage listed as waiting on the hold", built.Entries)
	}
	if rendered := built.Entries[0].Render(); !strings.Contains(rendered, "is waiting on "+runstate.TriageGateIntakeHold) {
		t.Fatalf("rendered entry does not say what the decision is waiting on:\n%s", rendered)
	}
}

// A finding about an attempt made before the decision is about an earlier
// decision on the same stoppage, and says nothing about this one: the entry
// stays settled. A finding older than the entry itself is about a stoppage this
// one replaced, and is not joined at all.
func TestAFindingOlderThanTheDecisionOrTheEntryDoesNotReopenIt(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	docket := docketerDeciding(nil, harness.docket, harness.runs.Triage(), harness.reruns)
	entryKey := triage.Key(triage.ClassStoppedRun, docketedRunID)
	harness.item.Status = "closed"
	// Refused at docketedNow, decided (again) a minute later.
	refusing := harness.carryOut()
	if _, _, err := refusing.Carry(context.Background(), theOneOutstanding(t, refusing)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	harness.docket.close(entryKey, runstate.TriageDecisionRerun, docketedNow.Add(time.Minute))
	built, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 0 || built.Closed != 1 {
		t.Fatalf("entries = %#v, closed = %d, want a finding older than the decision to leave the stoppage settled", built.Entries, built.Closed)
	}

	// The same run stops again after the decision, and is docketed afresh: the
	// old finding is about the stoppage that was replaced.
	again := stoppedState()
	stoppedAgain := docketedNow.Add(2 * time.Hour)
	again.CompletedAt = &stoppedAgain
	later := docketerOver(nil, harness.docket)
	later.Clock = laterClock{after: 2 * time.Hour}
	if _, err := later.RecordStoppedRun(again); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	fresh, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(fresh.Entries) != 1 || fresh.Entries[0].Closed != nil {
		t.Fatalf("entries = %#v, want the fresh stoppage listed as one nobody has decided about", fresh.Entries)
	}
	if fresh.Entries[0].CarryOut != nil {
		t.Fatalf("entry = %#v, want no finding from the stoppage this one replaced", fresh.Entries[0])
	}
}

// The verb clears the finding too. Both hands go through the same action, and a
// finding cleared only by the pass would stand over a run the operator fired by
// hand — which reads exactly like the condition the finding exists to report.
func TestTheTypedVerbClearsTheFindingTheCarryOutLeft(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "closed"
	refusing := harness.carryOut()
	if _, _, err := refusing.Carry(context.Background(), theOneOutstanding(t, refusing)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if counters, err := harness.runs.Triage().Counters(docketedItem); err != nil {
		t.Fatalf("Counters() error = %v", err)
	} else if _, found := counters.CarryOutOf(docketedRunID); !found {
		t.Fatalf("counters = %#v, want the refusal recorded before the verb is typed", counters)
	}
	harness.item.Status = "open"
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil || !result.Started {
		t.Fatalf("Rerun() = %#v, %v, want the verb to fire the decision", result, err)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if stopped, found := counters.CarryOutOf(docketedRunID); found {
		t.Fatalf("carry-out = %#v, want the finding cleared by the verb that fired the decision", stopped)
	}
}

// And the repair half of it: the continuation clears the finding as it re-enters
// the run, before the run goes, so nothing reads the decision as not happening
// while it is.
func TestTheRepairVerbClearsTheFindingTheCarryOutLeft(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	grantedAgainstTheStoppage(t, harness)
	harness.tracker.SetItemStatus("closed")
	refusing := harness.carryOut()
	if _, _, err := refusing.Carry(context.Background(), theOneOutstanding(t, refusing)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if counters, err := harness.runs.Triage().Counters(docketedItem); err != nil {
		t.Fatalf("Counters() error = %v", err)
	} else if _, found := counters.CarryOutOf(docketedRunID); !found {
		t.Fatalf("counters = %#v, want the refusal recorded before the verb is typed", counters)
	}
	harness.tracker.SetItemStatus("blocked")
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil || !result.Continued {
		t.Fatalf("Continue() = %#v, %v, want the verb to fire the decision", result, err)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if stopped, found := counters.CarryOutOf(docketedRunID); found {
		t.Fatalf("carry-out = %#v, want the finding cleared by the verb that fired the decision", stopped)
	}
}

package orchestrator

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// answeringRerunner answers every re-run the same way, and counts the asking.
type answeringRerunner struct {
	result RerunResult
	err    error
	asked  int
}

func (r *answeringRerunner) Rerun(context.Context, RerunRequest) (RerunResult, error) {
	r.asked++
	return r.result, r.err
}

// recordlessRuns is a run store that no longer holds the record of one run.
type recordlessRuns struct{ RerunRuns }

func (recordlessRuns) Load(string) (runstate.State, error) {
	return runstate.State{}, &os.PathError{Op: "open", Path: "state.json", Err: os.ErrNotExist}
}

func TestARerunOfAClosedItemIsRefusedOnceAndNotTriedAgain(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "closed"
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil || carried.Carried || carried.Cause != triage.CarryOutItemClosed || carried.Gate != runstate.TriageGateWorkItem {
		t.Fatalf("Carry() = %+v, %v; want the closed item recorded as a refusal for good", carried, err)
	}
	if carried.ReleasedHold || len(harness.started) != 0 {
		t.Fatalf("carried = %+v, started = %v; want nothing started and nothing said of starting a closed item", carried, harness.started)
	}
	if later, err := harness.carryOutAt(24 * time.Hour).Outstanding(); err != nil || len(later) != 0 {
		t.Fatalf("Outstanding() a day later = %v, %v; want the refused decision not tried again", later, err)
	}

	built, err := docketerDeciding(nil, harness.docket, harness.runs.Triage(), harness.reruns).Build()
	if err != nil || len(built.Entries) != 1 {
		t.Fatalf("Build() = %+v, %v", built.Entries, err)
	}
	shown := built.Entries[0].CarryOut
	if shown == nil || shown.Cause != triage.CarryOutItemClosed || shown.Attempts != 1 || !strings.Contains(shown.Refusal, "is closed") {
		t.Fatalf("docketed refusal = %+v, want the one refusal with its reason", shown)
	}
	assertDoesNotAdviseTheRefusedRerun(t, shown.Clears, built.Entries[0].Render())

	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	if finding, found := counters.CarryOutOf(docketedRunID); !found ||
		finding.BlocksDecision(runstate.TriageDecisionRerun, docketedNow.Add(48*time.Hour), docketedNow.Add(48*time.Hour)) {
		t.Fatalf("finding = %+v; want a decision recorded later not answered by it", finding)
	}
}

// assertDoesNotAdviseTheRefusedRerun holds that neither what the record says
// would clear a refused re-run nor the entry she reads tells her to record the
// same re-run, and that both still say it is not tried again.
func assertDoesNotAdviseTheRefusedRerun(t *testing.T, clears, rendered string) {
	t.Helper()
	if !strings.Contains(clears, "no later pull retries") || !strings.Contains(rendered, "no further attempt of this decision is made") {
		t.Fatalf("clears = %q, entry:\n%s\nwant both to say the decision is not tried again", clears, rendered)
	}
	for _, text := range []string{clears, rendered} {
		for _, advice := range []string{"records a re-run", "recording a re-run", "records the re-run", "recording the re-run"} {
			if strings.Contains(text, advice) {
				t.Fatalf("%q advises the refused re-run:\n%s", advice, text)
			}
		}
	}
}

// A repair asks about its run before it reads the item, so what it refuses for
// can be something else; the item being closed still settles the decision.
func TestADecisionOnAClosedItemRefusedForAnotherReasonIsStillRefusedForGood(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	carrying := harness.carryOut()
	carrying.Notes = harness
	task := theOneOutstanding(t, carrying)
	harness.item.Status = "closed"
	// Closed before the decision, so nothing settles the recovery as not applying.
	harness.item.ClosedAt = task.DecidedAt.Add(-time.Hour)
	carrying.Rerunner = &answeringRerunner{err: os.ErrPermission}
	carried, _, err := carrying.Carry(context.Background(), task)
	if err != nil || carried.Cause != triage.CarryOutItemClosed {
		t.Fatalf("Carry() = %+v, %v; want the closed item named as the cause", carried, err)
	}
}

func TestADecisionNamingARunWithNoRecordIsRefusedForGood(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	carrying := harness.carryOut()
	rerunner := harness.rerunner()
	rerunner.Runs = recordlessRuns{harness.runs}
	carrying.Rerunner = rerunner
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil || carried.Cause != triage.CarryOutRunGone {
		t.Fatalf("Carry() = %+v, %v; want the missing run recorded as a refusal for good", carried, err)
	}
	built, err := docketerDeciding(nil, harness.docket, harness.runs.Triage(), harness.reruns).Build()
	if err != nil || len(built.Entries) != 1 || built.Entries[0].CarryOut == nil {
		t.Fatalf("Build() = %+v, %v; want the refusal shown", built.Entries, err)
	}
	assertDoesNotAdviseTheRefusedRerun(t, built.Entries[0].CarryOut.Clears, built.Entries[0].Render())
	if later, err := harness.carryOutAt(24 * time.Hour).Outstanding(); err != nil || len(later) != 0 {
		t.Fatalf("Outstanding() a day later = %v, %v; want the refused decision not tried again", later, err)
	}
}

func TestARefusalForWantOfASlotIsTriedAgain(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	carrying := harness.carryOut()
	carrying.Rerunner = &answeringRerunner{result: RerunResult{CapacityFull: &runstate.CapacityError{Limit: 2, Active: 2}}}
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil || carried.Carried || !carried.Waiting || carried.Cause != "" {
		t.Fatalf("Carry() = %+v, %v; want a wait on a slot", carried, err)
	}
	theOneOutstanding(t, carrying)
}

// A re-run whose item waits on another item is refused, and the slot it was
// given goes to ready work on the same pull; the decision is tried again later.
func TestARerunWaitingOnAnotherItemLeavesItsSlotToReadyWorkOnTheSamePull(t *testing.T) {
	t.Parallel()

	decided := newRerunHarness(t, stoppedState())
	decided.item.Dependencies = []beads.Dependency{{ID: "yoyodyne-ifd.9", Type: "blocks", Status: "open"}}
	queue := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
	queue.capacity = 1
	pulls := 0
	open := func(ctx context.Context) (Pull, error) {
		pulls++
		pull, err := queue.open(ctx)
		pull.CarryOut = decided.carryOut()
		return pull, err
	}
	// Two starts are the attempt and the ready item; the bound ends the session
	// with both made in the first pull.
	schedule, err := (Scheduler{Open: open, CarryOutStartWait: time.Minute, Limit: 2}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 || schedule.Started[0].WorkItemID != docketedItem || schedule.Started[1].WorkItemID != "yoyodyne-ifd.500" {
		t.Fatalf("started = %+v, want the refused re-run and then the ready item", schedule.Started)
	}
	if !schedule.Started[0].slotless {
		t.Fatal("the refused re-run is still counted against a slot, so a later pull would leave one empty")
	}
	if len(decided.started) != 0 {
		t.Fatalf("started = %v, want no run of an item that waits", decided.started)
	}
	counters, err := decided.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	if finding, found := counters.CarryOutOf(docketedRunID); !found || finding.Cause != "" {
		t.Fatalf("finding = %+v, want a refusal that can clear", finding)
	}
	theOneOutstanding(t, decided.carryOutAt(runstate.TriageCarryOutRetryDelay+time.Minute))
}

// unheardCarryOut is a carry-out the tracker keeps waiting past the pull's bound
// before it refuses.
type unheardCarryOut struct {
	*scheduleHarness
	release chan struct{}
}

func (unheardCarryOut) SaysWhenStarted() bool { return true }

func (c unheardCarryOut) Carry(_ context.Context, task CarryOutTask) (CarriedOut, Outcome, error) {
	c.mu.Lock()
	c.carried = append(c.carried, task)
	c.mu.Unlock()
	<-c.release
	return CarriedOut{WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
		Gate: runstate.TriageGateWorkItem, Problem: "the item waits on another item"}, Outcome{}, nil
}

func TestADecisionTheTrackerKeepsWaitingLeavesItsSlotToReadyWorkOnTheSamePull(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
	harness.capacity = 1
	harness.outstanding = outstandingUntilAttempted(decidedTaskOf("yoyodyne-4f8u", "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	carryOut := unheardCarryOut{scheduleHarness: harness, release: make(chan struct{})}
	// The decision answers only once the ready item has run, so a pull that
	// kept the slot for it would never finish.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		close(carryOut.release)
		return h.complete(id), nil
	}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CarryOut = carryOut
		return pull, err
	}
	schedule, err := (Scheduler{Open: open, CarryOutStartWait: time.Millisecond}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 || schedule.Started[1].WorkItemID != "yoyodyne-ifd.500" {
		t.Fatalf("started = %+v, want the ready item started beside the decision nobody has heard from", schedule.Started)
	}
}

// A decision held back for work that outranks it is fired in the walk of the
// queue; refused there, its slot goes to the next ready item of that walk.
func TestADecisionRefusedInTheWalkLeavesItsSlotToTheNextReadyItem(t *testing.T) {
	t.Parallel()

	items := readyItems("yoyodyne-first", "yoyodyne-decided", "yoyodyne-last")
	for index := range items {
		items[index].Priority = index
	}
	harness := newScheduleHarness(items...)
	harness.capacity = 2
	stuck := "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	harness.outstanding = outstandingUntilAttempted(decidedTaskOf("yoyodyne-decided", stuck))
	accounted := [][]string{}
	carryOut := startingCarryOut{scheduleHarness: harness, refused: map[string]bool{stuck: true}, release: make(chan struct{}), accounted: &accounted}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CarryOut = carryOut
		return pull, err
	}
	// Both ready items run at once, so a pull that kept the second slot for the
	// refused decision would leave the first waiting for company.
	harness.developersMeet(2)
	schedule, err := (Scheduler{Open: open, CarryOutStartWait: time.Minute, Limit: 3}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	var order []string
	for _, started := range schedule.Started {
		order = append(order, started.WorkItemID)
	}
	if strings.Join(order, " ") != "yoyodyne-first yoyodyne-decided yoyodyne-last" {
		t.Fatalf("started = %v, want the refused decision's slot given to the last item in the same walk", order)
	}
}

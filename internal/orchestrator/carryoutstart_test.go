package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// startingCarryOut is the schedule harness's carry-out as the real one behaves
// towards a pull: it says when the run it fires has begun. A decision about a
// run in refused comes back without starting anything, as one a gate refuses
// on every pass does; every other begins, and is held until release so the
// slot it took is still taken when the pull accounts for what it passed over.
type startingCarryOut struct {
	*scheduleHarness
	refused map[string]bool
	release chan struct{}
	// accounted is what had been carried when each pull wrote up what it
	// passed over, which is the end of that pull's carrying out.
	accounted *[][]string
}

func (startingCarryOut) SaysWhenStarted() bool { return true }

func (c startingCarryOut) RecordUnattempted(ctx context.Context, poll time.Duration, passed map[string]string) ([]CarriedOut, error) {
	c.mu.Lock()
	var carried []string
	for _, task := range c.carried {
		carried = append(carried, task.RunID)
	}
	*c.accounted = append(*c.accounted, carried)
	c.mu.Unlock()
	return c.scheduleHarness.RecordUnattempted(ctx, poll, passed)
}

func (c startingCarryOut) Carry(ctx context.Context, task CarryOutTask) (CarriedOut, Outcome, error) {
	c.mu.Lock()
	c.carried = append(c.carried, task)
	c.mu.Unlock()
	carried := CarriedOut{WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision}
	if c.refused[task.RunID] {
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = "the \"repair\" the development manager decided was refused by the harness's own records: run " + task.RunID + " ended carrying no durable blocker and left no change behind"
		return carried, Outcome{}, nil
	}
	carryOutBegun(ctx)
	<-c.release
	carried.Carried = true
	carried.Reason = rerunReasoning
	return carried, c.complete(task.WorkItemID), nil
}

// A decision refused on every pass is the oldest on the docket, so it is the
// one each pull fires first. It started nothing, so the slot it was given goes
// to the decision behind it in the same pull, and only the decision behind
// that is passed over for want of a slot.
func TestADecisionThatCannotStartDoesNotKeepTheSlotFromTheDecisionsBehindIt(t *testing.T) {
	t.Parallel()

	stuck := "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	behind := "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	last := "run-cccccccccccccccccccccccccccccccc"
	harness := newScheduleHarness()
	harness.capacity = 1
	harness.outstanding = outstandingUntilAttempted(
		decidedTaskOf("yoyodyne-ifd.309", stuck), decidedTaskOf("yoyodyne-ani", behind), decidedTaskOf("yoyodyne-wj4l.1", last))
	unattempted := make(chan struct{}, 1)
	harness.unattempted = unattempted
	var accounted [][]string
	carryOut := startingCarryOut{scheduleHarness: harness, refused: map[string]bool{stuck: true}, release: make(chan struct{}), accounted: &accounted}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CarryOut = carryOut
		return pull, err
	}

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = (Scheduler{Open: open, CarryOutStartWait: time.Minute}).Schedule(context.Background())
	}()
	<-unattempted
	harness.mu.Lock()
	firstPull := accounted[0]
	passed := harness.passedOver[0]
	harness.mu.Unlock()
	close(carryOut.release)
	<-done
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	if len(firstPull) != 2 || firstPull[0] != stuck || firstPull[1] != behind {
		t.Fatalf("first pull carried %v, want the refused decision and then the one behind it given the slot it handed back", firstPull)
	}
	if _, kept := passed[behind]; kept {
		t.Fatalf("passed over = %#v, want the decision that was given the slot not written up as passed over", passed)
	}
	if why := passed[last]; !strings.Contains(why, "spent on 1 decision(s) ahead of it") || len(passed) != 1 {
		t.Fatalf("passed over = %#v, want only the last decision passed over, for the one slot the decision that began took", passed)
	}
}

// With no slot free, a pull still attempts one decision so the full harness is
// recorded, and what it says of the rest is that the runs in flight hold every
// slot — not that a decision ahead of them took one.
func TestADecisionPassedOverWithNoSlotFreeNamesTheRunsHoldingThem(t *testing.T) {
	t.Parallel()

	first := decidedTaskOf("yoyodyne-ifd.309", "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	second := decidedTaskOf("yoyodyne-ani", "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	harness := newScheduleHarness()
	harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) {
		return []CarryOutTask{first, second}, nil
	}
	schedule := Schedule{}
	chosen, passed, _ := Scheduler{}.nextCarryOuts(&schedule, Pull{CarryOut: harness, Capacity: 3}, map[string]runstate.State{}, nil, nil, closedGates{capacity: true}, 0, 0, nil)
	if len(chosen) != 1 || chosen[0].RunID != first.RunID {
		t.Fatalf("chosen = %#v, want the one attempt that records the full harness", chosen)
	}
	why := passed[second.RunID]
	if !strings.Contains(why, "no developer slot was free at this pull: all 3 are held by runs in flight") || strings.Contains(why, "spent on") {
		t.Fatalf("passed over because %q, want the runs holding the slots named rather than the decision ahead", why)
	}
}

// goneWorkers is the run store's reading of a worker, for the runs the test
// names as having no process behind them.
type goneWorkers struct {
	*scheduleHarness
	gone map[string]bool
}

func (g goneWorkers) HoldsDeveloperSlotNow(state runstate.State, _ time.Time) bool {
	return state.HoldsDeveloperSlot() && !g.gone[state.RunID]
}

// A run recorded as in flight whose worker is gone holds no slot, so a pull
// whose only slot it filled still starts ready work. The same run with its
// worker going still fills the slot.
func TestSlotsAreNotCountedFullByARunWhoseWorkerIsGone(t *testing.T) {
	t.Parallel()

	dead := "run-aaaabbbbccccddddeeeeffff00002222"
	for _, test := range []struct {
		name  string
		gone  bool
		start bool
	}{
		{name: "its worker is gone", gone: true, start: true},
		{name: "its worker is working", gone: false, start: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
			harness.capacity = 1
			harness.inFlight["yoyodyne-ifd.400"] = runningState(dead, "yoyodyne-ifd.400")
			runs := goneWorkers{scheduleHarness: harness, gone: map[string]bool{dead: test.gone}}
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.Runs = runs
				return pull, err
			}
			schedule, err := (Scheduler{Open: open}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			started := false
			for _, run := range schedule.Started {
				started = started || run.WorkItemID == "yoyodyne-ifd.500"
			}
			if started != test.start {
				t.Fatalf("started = %#v, stopped = %q; want the ready item started %t", schedule.Started, schedule.Stopped, test.start)
			}
			if !test.start && schedule.Stopped != ScheduleCapacityFull {
				t.Fatalf("stopped = %q, want the slot the working run holds counted full", schedule.Stopped)
			}
		})
	}
}

// developingState is a run that stopped while its developer was still working,
// before any check or review judged its change, with that change kept.
func developingState() runstate.State {
	state := continuableState()
	state.Phase = runstate.PhaseDeveloping
	state.ReviewRounds = 0
	state.ReviewSummary = ""
	state.ReviewFindings = 0
	state.ReviewFindingDetails = nil
	state.CheckFailure = nil
	state.Blocker = ""
	state.Failure = "the developer could not start make vet in this worktree: You have not agreed to the Xcode license agreements."
	return state
}

// A repair decided on a run that failed at developing, with its change kept,
// is either started or refused with why written where the development manager
// reads it, against that run. One whose developer was handed a failing check
// is a repair like any other, and starts; one whose developer was never handed
// anything says so, and names the re-run that applies.
func TestARepairOfARunFailedAtDevelopingStartsOrRecordsWhy(t *testing.T) {
	t.Parallel()

	kept := &orchestratortest.Survival{Survival: gitworktree.Survival{BranchExists: true, WorktreePresent: true}}
	t.Run("handed a failing check", func(t *testing.T) {
		t.Parallel()
		state := developingState()
		state.CheckFailure = &runstate.CheckFailure{Command: "make test", ExitCode: 1, Output: "FAIL"}
		state.Blocker = "Yoyodyne stopped this item: the repair budget was spent."
		harness := newUndecidedHarness(t, state)
		grantedAgainstTheStoppage(t, harness)
		carrying := harness.carryOut()
		continuer := harness.continuer()
		continuer.Remains = kept
		carrying.Repairer = continuer
		begun := false
		ctx := withCarryOutBegun(context.Background(), func() { begun = true })
		carried, _, err := carrying.Carry(ctx, theOneOutstanding(t, carrying))
		if err != nil {
			t.Fatalf("Carry() error = %v", err)
		}
		if !carried.Carried || len(harness.started) != 1 || !begun {
			t.Fatalf("carried = %#v, started = %#v, begun = %t; want the repair started and the pull told it began", carried, harness.started, begun)
		}
	})
	t.Run("never handed anything", func(t *testing.T) {
		t.Parallel()
		state := developingState()
		harness := newUndecidedHarness(t, state)
		grantedAgainstTheStoppage(t, harness)
		carrying := harness.carryOut()
		continuer := harness.continuer()
		continuer.Remains = kept
		carrying.Repairer = continuer
		begun := false
		ctx := withCarryOutBegun(context.Background(), func() { begun = true })
		carried, _, err := carrying.Carry(ctx, theOneOutstanding(t, carrying))
		if err != nil {
			t.Fatalf("Carry() error = %v", err)
		}
		if carried.Carried || len(harness.started) != 0 || begun {
			t.Fatalf("carried = %#v, started = %#v, begun = %t; want nothing started and the pull told nothing began", carried, harness.started, begun)
		}
		counters, err := harness.runs.Triage().Counters(docketedItem)
		if err != nil {
			t.Fatalf("Counters() error = %v", err)
		}
		recorded, found := counters.CarryOutOf(state.RunID)
		if !found || recorded.Unattempted {
			t.Fatalf("finding = %#v (found %t), want the refusal of the attempt written against the run", recorded, found)
		}
		for _, want := range []string{"stopped while its developer was still working", "Xcode license", "before any check or review judged its change", "branch " + state.Branch, "A re-run of " + state.WorkItemID} {
			if !strings.Contains(recorded.Refusal, want) {
				t.Fatalf("refusal = %q, want it to say %q", recorded.Refusal, want)
			}
		}
	})
}

// A re-run tells the pull it began only once its fresh run has reserved a
// slot. One the fresh run's own start turns away — a hold met where it would
// start, or the last slot taken by another run first — has begun nothing, so
// the pull that fired it gives its slot to the decisions behind it.
func TestARerunTellsThePullItBeganOnlyOnceItsRunHoldsASlot(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		meet  func(t *testing.T, pipelined *pipelinedRerun)
		began bool
	}{
		{name: "a run that starts", began: true},
		{name: "a hold met where the fresh run would start", meet: func(t *testing.T, pipelined *pipelinedRerun) {
			if _, err := pipelined.intake.Hold(runstate.IntakeHolderOperator, "stop choosing while I reorder the queue", docketedNow); err != nil {
				t.Fatalf("Hold() error = %v", err)
			}
		}},
		{name: "the last slot taken first", meet: func(t *testing.T, pipelined *pipelinedRerun) {
			if err := pipelined.runs.Create(runningState("run-11112222333344445555666677778888", "yoyodyne-ifd.other")); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var pipelined *pipelinedRerun
			met := false
			pipelined = newPipelinedRerun(t, func() {
				if test.meet == nil || met {
					return
				}
				met = true
				test.meet(t, pipelined)
			})
			began := false
			ctx := withCarryOutBegun(context.Background(), func() { began = true })
			result, err := pipelined.rerunner.Rerun(ctx, RerunRequest{Run: priorRunID})
			if err != nil {
				t.Fatalf("Rerun() error = %v", err)
			}
			if began != test.began || result.Started != test.began {
				t.Fatalf("began = %t, started = %t; want the pull told the re-run began %t", began, result.Started, test.began)
			}
		})
	}
}

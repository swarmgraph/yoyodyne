package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The operator's direction of 2026-10-25: work on a change a run already wrote
// and kept goes ahead of fresh work of any priority. A ready priority-0 item and
// a decided repair of a priority-2 item's kept change: the freed slot starts the
// repair, and the run's reason names the priority-0 item it went ahead of. With a
// second slot the priority-0 item takes it.
func TestAFreedSlotStartsARepairOfAKeptChangeAheadOfReadyPriorityZeroWork(t *testing.T) {
	t.Parallel()

	for _, capacity := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d-slots", capacity), func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(
				beads.WorkItem{ID: "yoyodyne-94j", Title: "Rebuilds under held parents are offered", Status: "open", Priority: 0},
				beads.WorkItem{ID: "yoyodyne-xdj", Title: "Confined writers survive replacement races", Status: "blocked", Priority: 2},
			)
			harness.capacity = capacity
			harness.ReadyItems["yoyodyne-xdj"] = false
			harness.stoppages = preservedStoppage("yoyodyne-xdj")
			fired := map[string]bool{}
			task := decidedTask("yoyodyne-xdj")
			task.Decision = runstate.TriageDecisionRepair
			harness.outstanding = outstandingUntilFired(fired, task)
			harness.carry = carriedWhenASlotIsFree(fired)

			schedule, err := (Scheduler{Open: harness.open, Limit: capacity}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			want := "yoyodyne-xdj"
			if capacity == 2 {
				want += ",yoyodyne-94j"
			}
			if got := strings.Join(runsStarted(schedule), ","); got != want {
				t.Fatalf("started = %v, want %s: the repair of the kept change first", got, want)
			}
			carried := harness.carriedTask(t, "yoyodyne-xdj")
			if !carried.KeptChange {
				t.Fatalf("carried = %#v, want the repair marked as building on a kept change", carried)
			}
			// The reason the slot took it is on the run's recorded reason, naming
			// the priority-0 work it went ahead of by what it is.
			ahead := aheadOfQueue(carried)
			for _, want := range []string{"Rebuilds under held parents are offered (yoyodyne-94j)", "builds on a change a run already wrote and kept"} {
				if !strings.Contains(ahead, want) {
					t.Fatalf("what the run records of the slot it took = %q, want it to name %q", ahead, want)
				}
			}
			if reason := schedule.Started[0].Reason; schedule.Started[0].WorkItemID != "yoyodyne-xdj" || strings.TrimSpace(reason) == "" {
				t.Fatalf("first start = %#v, want the repair with its reason", schedule.Started[0])
			}
		})
	}
}

// A slot that prefers a label takes work on a kept change first too: the
// preference orders the fresh work it walks, and this work comes before the walk.
func TestASlotThatPrefersALabelStillTakesAKeptChangeFirst(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-rel", Title: "The store stops locking up", Status: "open", Priority: 0, Labels: []string{"reliability"}},
		beads.WorkItem{ID: "yoyodyne-rel2", Title: "The tracker read is bounded", Status: "open", Priority: 1, Labels: []string{"reliability"}},
		beads.WorkItem{ID: "yoyodyne-xdj", Title: "Confined writers survive replacement races", Status: "blocked", Priority: 3},
	)
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"reliability"}}}
	harness.ReadyItems["yoyodyne-xdj"] = false
	harness.stoppages = preservedStoppage("yoyodyne-xdj")
	fired := map[string]bool{}
	task := decidedTask("yoyodyne-xdj")
	task.Decision = runstate.TriageDecisionRepair
	harness.outstanding = outstandingUntilFired(fired, task)
	harness.carry = carriedWhenASlotIsFree(fired)

	schedule, err := (Scheduler{Open: harness.open, Limit: 1}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := strings.Join(runsStarted(schedule), ","); got != "yoyodyne-xdj" {
		t.Fatalf("started = %v, want the unlabelled repair of the kept change in the reliability slot ahead of its labelled work", got)
	}
	if pulled := harness.pullOrder(); len(pulled) != 0 {
		t.Fatalf("fresh runs = %v, want none: the one slot was the repair's", pulled)
	}
}

// Among several decisions on kept changes the Lead Product Manager's order
// decides, whatever order they were decided in: the priority-1 repair decided
// last goes before the priority-3 one decided first, and with one slot the
// priority-3 one waits for the next.
func TestAmongRecoveriesOfKeptChangesTheHigherPriorityStartsFirst(t *testing.T) {
	t.Parallel()

	for _, capacity := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d-slots", capacity), func(t *testing.T) {
			t.Parallel()
			const lowRun, highRun = "run-11111111111111111111111111111111", "run-22222222222222222222222222222222"
			harness := newScheduleHarness(
				beads.WorkItem{ID: "yoyodyne-fresh", Title: "Fresh work", Status: "open", Priority: 0},
				beads.WorkItem{ID: "yoyodyne-low", Title: "Lower-priority kept change", Status: "blocked", Priority: 3},
				beads.WorkItem{ID: "yoyodyne-high", Title: "Higher-priority kept change", Status: "blocked", Priority: 1},
			)
			harness.capacity = capacity
			harness.ReadyItems["yoyodyne-low"] = false
			harness.ReadyItems["yoyodyne-high"] = false
			low := preservedStoppage("yoyodyne-low").runs[0]
			low.RunID = lowRun
			high := preservedStoppage("yoyodyne-high").runs[0]
			high.RunID = highRun
			harness.stoppages = haltedWork{runs: []runstate.State{low, high}}
			decided := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
			lowTask := decidedTaskOf("yoyodyne-low", lowRun)
			lowTask.Decision, lowTask.DecidedAt = runstate.TriageDecisionRepair, decided
			highTask := decidedTaskOf("yoyodyne-high", highRun)
			highTask.Decision, highTask.DecidedAt = runstate.TriageDecisionRepair, decided.Add(time.Hour)
			fired := map[string]bool{}
			harness.outstanding = outstandingUntilFired(fired, lowTask, highTask)
			harness.carry = carriedWhenASlotIsFree(fired)

			schedule, err := (Scheduler{Open: harness.open, Limit: capacity}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			want := "yoyodyne-high"
			if capacity == 2 {
				want += ",yoyodyne-low"
			}
			if got := strings.Join(runsStarted(schedule), ","); got != want {
				t.Fatalf("started = %v, want %s: the higher-priority recovery first, and both ahead of the fresh priority-0 item", got, want)
			}
			if pulled := harness.pullOrder(); len(pulled) != 0 {
				t.Fatalf("fresh runs = %v, want none", pulled)
			}
		})
	}
}

// A re-run counts as work on a kept change only where it starts from that
// change, which is a re-run of a run that raised its item with its branch still
// there. A re-run of any other stopped run starts over from the target and waits
// behind higher-priority work like fresh work (passedover_priority_test.go).
func TestAReRunThatStartsFromTheKeptChangeGoesAheadOfHigherPriorityWork(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		escalated    bool
		branchGone   bool
		recoveryWins bool
	}{
		{name: "raised-with-its-branch", escalated: true, recoveryWins: true},
		{name: "raised-with-its-branch-gone", escalated: true, branchGone: true},
		{name: "stopped-with-its-branch", escalated: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(
				beads.WorkItem{ID: "yoyodyne-94j", Title: "Rebuilds under held parents are offered", Status: "open", Priority: 0},
				beads.WorkItem{ID: "yoyodyne-xdj", Title: "Confined writers survive replacement races", Status: "blocked", Priority: 2},
			)
			harness.ReadyItems["yoyodyne-xdj"] = false
			stopped := preservedStoppage("yoyodyne-xdj")
			if test.escalated {
				stopped.runs[0].LandingOutcome = runstate.LandingEscalate
			}
			if test.branchGone {
				stopped.runs[0].BranchRemoved = true
			}
			harness.stoppages = stopped
			fired := map[string]bool{}
			harness.outstanding = outstandingUntilFired(fired, decidedTask("yoyodyne-xdj"))
			harness.carry = carriedWhenASlotIsFree(fired)

			schedule, err := (Scheduler{Open: harness.open, Limit: 1}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			want := "yoyodyne-94j"
			if test.recoveryWins {
				want = "yoyodyne-xdj"
			}
			if got := strings.Join(runsStarted(schedule), ","); got != want {
				t.Fatalf("started = %v, want %s", got, want)
			}
		})
	}
}

// What stops one change holding developer slots for ever is the bound each kind
// of recovery already has, and every kind that takes a slot ahead of fresh work
// has one. A decided repair and a decided re-run are bounded by the item's caps,
// which the development manager may cross one step at a time and five times per
// item before only the operator can; a finished merge by the run's integration
// resumptions; a check-stage continuation by its bound per candidate.
func TestEveryRecoveryThatGoesAheadOfFreshWorkIsBounded(t *testing.T) {
	t.Parallel()

	kept := keptChange(context.Background(), Pull{Stoppages: preservedStoppage("yoyodyne-xdj")}, time.Now)
	for _, decision := range []string{DecisionContinueChecks, DecisionContinueStall, DecisionFinishMerge, runstate.TriageDecisionRepair} {
		task := decidedTask("yoyodyne-xdj")
		task.Decision = decision
		if !kept(task) {
			t.Fatalf("keptChange(%s) = false, want it taken ahead of fresh work", decision)
		}
	}
	// A kind nobody listed is not given the precedence, so a new kind of recovery
	// cannot take it without this bound being asked of it.
	other := decidedTask("yoyodyne-xdj")
	other.Decision = runstate.TriageDecisionRescope
	if kept(other) {
		t.Fatalf("keptChange(%s) = true, want only the kinds whose bound is named", other.Decision)
	}

	if runstate.MaxCheckStageContinuations < 1 || runstate.MaxCheckStageContinuations > 5 {
		t.Fatalf("MaxCheckStageContinuations = %d, want a small bound", runstate.MaxCheckStageContinuations)
	}
	spent := runstate.State{IntegrationResumptions: make([]runstate.IntegrationResumption, runstate.MaxIntegrationResumptions)}
	if spent.ResumptionsLeft() != 0 {
		t.Fatalf("ResumptionsLeft() after %d resumptions = %d, want none", runstate.MaxIntegrationResumptions, spent.ResumptionsLeft())
	}

	ctx := context.Background()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	triage := store.Triage()
	caps := runstate.TriageCaps{ReviewRounds: 100, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
	at := time.Date(2026, 10, 27, 17, 30, 0, 0, time.UTC)

	if _, err := triage.GrantRepair(ctx, "yoyodyne-repair", triageDecided(runstate.TriageDecisionRepair, fmt.Sprintf("run-%032x", 1)), 1, at, caps); err != nil {
		t.Fatalf("GrantRepair() within the cap error = %v", err)
	}
	if _, err := triage.GrantRepair(ctx, "yoyodyne-repair", triageDecided(runstate.TriageDecisionRepair, fmt.Sprintf("run-%032x", 2)), 1, at, caps); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("GrantRepair() past the cap error = %v, want the repair grant cap to refuse it", err)
	}

	// A re-run past the cap is refused; the development manager may cross the cap
	// one step at a time, five times on one item, and then only the operator can.
	item := "yoyodyne-rerun"
	for crossing := 0; ; crossing++ {
		run := fmt.Sprintf("run-%032x", 100+2*crossing)
		if _, err := triage.RecordRerun(ctx, item, triageDecided(runstate.TriageDecisionRerun, run), at, caps); err != nil {
			t.Fatalf("RecordRerun() %d within the cap as crossed error = %v", crossing, err)
		}
		next := fmt.Sprintf("run-%032x", 101+2*crossing)
		if _, err := triage.RecordRerun(ctx, item, triageDecided(runstate.TriageDecisionRerun, next), at, caps); !errors.Is(err, runstate.ErrTriageCapReached) {
			t.Fatalf("RecordRerun() past the cap after %d crossings error = %v, want the re-run cap to refuse it", crossing, err)
		}
		_, err := triage.CrossCap(ctx, item, domain.RoleDevelopmentManager, runstate.TriageRerunBudget, "the change was right and the ground moved under it", at, caps)
		if crossing == runstate.MaxDelegatedCapCrossings {
			if !errors.Is(err, runstate.ErrTriageCrossingsSpent) {
				t.Fatalf("CrossCap() number %d error = %v, want the development manager's crossings spent", crossing+1, err)
			}
			break
		}
		if err != nil {
			t.Fatalf("CrossCap() number %d error = %v", crossing+1, err)
		}
	}
}

// carriedTask is the task the pass handed the carry-out for an item.
func (h *scheduleHarness) carriedTask(t *testing.T, workItemID string) CarryOutTask {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, task := range h.carried {
		if task.WorkItemID == workItemID {
			return task
		}
	}
	t.Fatalf("carried = %#v, want a task for %s", h.carried, workItemID)
	return CarryOutTask{}
}

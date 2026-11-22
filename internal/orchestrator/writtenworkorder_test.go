package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Written work waiting for a developer slot is taken in one order: priority
// first, then the longest wait, and all of it ahead of fresh work. Three wait
// here, as on 2026-11-21: a priority 0 run whose GitHub checks passed 50
// minutes ago; a priority 1 run whose checks passed two hours ago; and a
// priority 1 hand-back the development manager decided half an hour ago, a
// repair of a kept change. Before, the hand-back took the first freed slot
// because decided repairs went before every paused run, and the older priority
// 1 run took the next because paused runs went oldest first; the priority 0 run
// was passed over at both. Now the priority 0 run is taken first, the older
// priority 1 run second, and the hand-back third, with each one passed over
// saying why, on its run where it is a paused run, and status showing it.
func TestAFreedSlotTakesThePriorityZeroRunWhoseChecksPassedAheadOfOlderPriorityOneWork(t *testing.T) {
	t.Parallel()

	const (
		zero     = "yoyodyne-fp3.8"
		older    = "yoyodyne-ifd.435.52"
		handBack = "yoyodyne-ifd.436.15"
		fresh    = "yoyodyne-ifd.431.13"
		zeroRun  = "run-fp38fp38fp38fp38fp38fp38fp38fp38"
		olderRun = "run-43552435524355243552435524355243"
	)
	for _, capacity := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("%d-slots", capacity), func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(
				beads.WorkItem{ID: fresh, Title: "Fresh priority 0 work", Status: "open", Priority: 0},
				beads.WorkItem{ID: zero, Title: "Documents publish again", Status: "in_progress", Priority: 0},
				beads.WorkItem{ID: older, Title: "Message intake piece 3", Status: "in_progress", Priority: 1},
				beads.WorkItem{ID: handBack, Title: "The release step is repaired", Status: "blocked", Priority: 1},
			)
			harness.capacity = capacity
			harness.ReadyItems[handBack] = false
			now := harness.now

			// Each run's wait is counted from when its checks were read as passed.
			passedAt := func(state runstate.State, at time.Time) runstate.State {
				state.CIWait.Decided = &runstate.CIWaitDecision{At: at, Passed: true, Says: ciAllPassed}
				return state
			}
			zeroState := passedAt(waitingOnGitHub(zero, zeroRun, now.Add(-time.Hour)), now.Add(-50*time.Minute))
			zeroState.UpdatedAt = now.Add(-time.Hour)
			harness.inFlight[zero] = zeroState
			olderState := passedAt(waitingOnGitHub(older, olderRun, now.Add(-3*time.Hour)), now.Add(-2*time.Hour))
			olderState.UpdatedAt = now.Add(-3 * time.Hour)
			harness.inFlight[older] = olderState

			harness.stoppages = preservedStoppage(handBack)
			task := decidedTask(handBack)
			task.Decision, task.DecidedAt = runstate.TriageDecisionRepair, now.Add(-30*time.Minute)
			fired := map[string]bool{}
			harness.outstanding = outstandingUntilFired(fired, task)
			harness.carry = carriedWhenASlotIsFree(fired)

			runs := recordingRuns{scheduleHarness: harness, gone: map[string]bool{}}
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.Runs = runs
				pull.CIWaits = passedGitHub{}
				pull.Continuations = waitingContinuations{}
				return pull, err
			}
			schedule, err := (Scheduler{Open: open, Limit: capacity, Now: harness.clock}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}

			order := []string{zero, older, handBack}
			want := order[:capacity]
			got := runsStarted(schedule)
			slices.Sort(got)
			sortedWant := slices.Clone(want)
			slices.Sort(sortedWant)
			if !slices.Equal(got, sortedWant) {
				t.Fatalf("started = %v, want %v: priority 0 first, then the longest-waiting priority 1 work", runsStarted(schedule), want)
			}
			if pulled := harness.pullOrder(); slices.Contains(pulled, fresh) {
				t.Fatalf("fresh runs = %v, want none while written work waits", pulled)
			}

			switch capacity {
			case 1:
				// The older priority 1 run says on its own record that the slot went
				// to the priority 0 run, and status shows it.
				record := runs.record(older)
				if record.ContinuationWait == nil || !strings.Contains(record.ContinuationWait.Why, "Documents publish again ("+zero+")") {
					t.Fatalf("older run's wait = %#v, want it to name the priority 0 run that took the slot", record.ContinuationWait)
				}
				if !record.UpdatedAt.Equal(olderState.UpdatedAt) {
					t.Fatalf("older run's updated time = %v, want %v: saying why it waits must not move its place in line", record.UpdatedAt, olderState.UpdatedAt)
				}
				fallthrough
			case 2:
				// The hand-back is passed over naming the paused runs ahead of it.
				var waits string
				for _, wait := range schedule.CarryOutWaiting {
					if wait.WorkItemID == handBack {
						waits = wait.Waits
					}
				}
				for _, says := range []string{"by priority and then by longest wait", zero} {
					if !strings.Contains(waits, says) {
						t.Fatalf("hand-back waits = %q, want it to say %q", waits, says)
					}
				}
				if capacity == 2 && !strings.Contains(waits, older) {
					t.Fatalf("hand-back waits = %q, want it to name the older priority 1 run too", waits)
				}
			}
		})
	}
}

// The order between paused runs and decisions on kept changes: priority first,
// then the longest wait, whichever kind each is; a decision that is fresh work
// has every paused run ahead of it.
func TestContinuationsAheadOrdersByPriorityThenLongestWait(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	paused := func(id string, priority int, since time.Time) pausedContinuation {
		return pausedContinuation{state: runstate.State{RunID: "run-" + id, WorkItemID: id, UpdatedAt: since}, priority: priority, named: id}
	}
	candidates := []pausedContinuation{
		paused("p0-new", 0, at.Add(-time.Minute)),
		paused("p1-old", 1, at.Add(-3*time.Hour)),
		paused("p1-new", 1, at.Add(time.Hour)),
		paused("p2", 2, at.Add(-5*time.Hour)),
	}
	for _, test := range []struct {
		name     string
		kept     bool
		priority int
		want     []string
	}{
		{name: "kept-priority-1", kept: true, priority: 1, want: []string{"p0-new", "p1-old"}},
		{name: "kept-priority-0", kept: true, priority: 0, want: []string{"p0-new"}},
		{name: "kept-priority-3", kept: true, priority: 3, want: []string{"p0-new", "p1-old", "p1-new", "p2"}},
		{name: "fresh", kept: false, priority: 0, want: []string{"p0-new", "p1-old", "p1-new", "p2"}},
	} {
		if got := continuationsAhead(candidates, test.kept, test.priority, at, "run-decision"); !slices.Equal(got, test.want) {
			t.Errorf("%s: ahead = %v, want %v", test.name, got, test.want)
		}
	}
}

// Status shows why a paused run was not continued, from its own record.
func TestStatusShowsWhyAPausedRunWasNotContinued(t *testing.T) {
	t.Parallel()

	state := waitingOnGitHub("yoyodyne-fp3.8", "run-1", time.Date(2026, 11, 21, 19, 0, 0, 0, time.UTC))
	next, changed, err := runstate.ApplyUpdate(state, runstate.ContinuationPassedOver{Why: "the slot went to written work ahead of it", At: time.Date(2026, 11, 21, 20, 38, 0, 0, time.UTC)})
	if err != nil || !changed || next.ContinuationWait == nil || next.ContinuationWait.Why != "the slot went to written work ahead of it" {
		t.Fatalf("ApplyUpdate() = %#v, %v, %v, want the reason recorded", next.ContinuationWait, changed, err)
	}
	if _, again, _ := runstate.ApplyUpdate(next, runstate.ContinuationPassedOver{Why: "the slot went to written work ahead of it", At: time.Date(2026, 11, 21, 20, 39, 0, 0, time.UTC)}); again {
		t.Fatalf("the same reason written again changed the record, want one write through a long wait")
	}
}

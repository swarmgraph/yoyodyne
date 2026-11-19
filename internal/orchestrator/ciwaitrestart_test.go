package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// decidedGitHub is the forge as a pull reads it for a run whose wait has run
// past its limit: the forge has decided, so the run is to be continued.
type decidedGitHub struct{}

func (decidedGitHub) Decided(context.Context, runstate.State) (bool, string, error) {
	return true, "test had not finished within the wait limit", nil
}

// workersOf is the scheduling harness's runs with the store's reading of
// whether each run's worker is alive: a run named gone is one whose program
// ended, and it holds no developer slot whatever its record says, as
// runstate.Store.HoldsDeveloperSlotNow reads it.
type workersOf struct {
	*scheduleHarness
	gone map[string]bool
}

func (w workersOf) HoldsDeveloperSlotNow(state runstate.State, _ time.Time) bool {
	return state.HoldsDeveloperSlot() && !w.gone[state.RunID]
}

// inSlot records a developer slot on a run, with the endpoint pair that slot
// is configured with, which is what makes the run wait for that slot rather
// than be taken up in another (runstate.Store.takeUpSlot).
func inSlot(state runstate.State, slot int, at time.Time) runstate.State {
	state.Routing = &runstate.RunRouting{
		Version:   2,
		Slot:      &runstate.RoutedSlot{Number: slot, Origin: runstate.RoutingClaimed, RecordedAt: at},
		Developer: &runstate.RoutingSnapshot{Slot: slot},
	}
	return state
}

// A scheduler restarted while a run waits on GitHub's checks takes the run up
// at its first pull, however far the old one had got. Here the old scheduler
// had begun continuing the run — it had taken the run's developer slot back,
// which marks the wait as holding a slot — when it was stopped for the
// restart, before it read GitHub. Nothing else picks such a run up: it carries
// no stop for the new scheduler to re-adopt, and its record says it holds a
// slot, so it read as neither waiting nor stopped, and it sat until the
// development manager continued it by hand.
func TestARestartedSchedulerTakesUpARunWaitingOnGitHubWhoseProgramEnded(t *testing.T) {
	t.Parallel()

	const id = "yoyodyne-ifd.429.146"
	harness := newScheduleHarness()
	harness.capacity = 2
	harness.poll = 5 * time.Millisecond
	pushed := harness.now.Add(-2 * time.Hour)
	waiting := waitingOnGitHub(id, "run-cdcf6432abab02715bd257eeee76f1be", pushed)
	waiting.CIWait.WaitLimitSeconds = int64(time.Hour / time.Second)
	harness.inFlight[id] = waiting

	// The scheduler before the restart: it continues the run, takes its slot
	// back, and is stopped for the restart before the run reads GitHub.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	tookSlot := make(chan struct{})
	before := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CIWaits = decidedGitHub{}
		pull.Continuations = waitingContinuations{}
		pull.Start = func(ctx context.Context, workItemID string, _ runstate.Selection) (Outcome, error) {
			harness.mu.Lock()
			state := harness.inFlight[workItemID]
			wait := *state.CIWait
			wait.SlotTaken = true
			state.CIWait = &wait
			harness.inFlight[workItemID] = state
			harness.mu.Unlock()
			close(tookSlot)
			<-ctx.Done()
			return Outcome{}, fmt.Errorf("load work item: %w", context.Cause(ctx))
		}
		return pull, err
	}
	go func() {
		<-tookSlot
		stop()
	}()
	harness.onSleep = func(*scheduleHarness, int) bool { return ctx.Err() == nil }
	_, _ = Scheduler{Open: before, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: &recordedSessions{}, SessionID: "watch-before"}.Schedule(ctx)

	harness.mu.Lock()
	left := harness.inFlight[id]
	harness.mu.Unlock()
	if left.CIWait == nil || !left.CIWait.SlotTaken || left.RedeployStop != nil {
		t.Fatalf("run after the restart = %#v, want it still waiting on GitHub, holding a slot on record, and carrying no stop", left)
	}

	// The scheduler after the restart: the old program is gone, so the run's
	// worker is, and its first pull takes the run up.
	runs := workersOf{scheduleHarness: harness, gone: map[string]bool{left.RunID: true}}
	after := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		pull.CIWaits = decidedGitHub{}
		pull.Continuations = waitingContinuations{}
		return pull, err
	}
	harness.onSleep = nil
	schedule, err := Scheduler{Open: after, Now: harness.clock, Sleep: harness.sleep, SessionID: "watch-after"}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() after the restart error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != id {
		t.Fatalf("after the restart = %s, want the run waiting on GitHub taken up", schedule.Render())
	}
	taken := schedule.Started[0]
	if taken.Failure != "" || taken.Declined != "" || taken.Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("taken up = %#v, want it continued to its end", taken)
	}
	for _, says := range []string{left.RunID, "the program carrying it ended", "test had not finished within the wait limit"} {
		if !strings.Contains(taken.Reason, says) {
			t.Fatalf("reason = %q, want it to say %q", taken.Reason, says)
		}
	}
	if order := harness.pullOrder(); len(order) != 1 || order[0] != id {
		t.Fatalf("starts after the restart = %v, want the run taken up once", order)
	}
}

// A run waiting on GitHub's checks past its limit, whose developer slot is held
// by another run, is passed over naming that slot rather than started to be
// refused, and is taken up at the first pull after the slot frees, before any
// new work can take it. Before, the refusal set the continuation aside for
// fifteen minutes, the slot freed meanwhile, and new work was pulled into it,
// again and again, until the development manager continued the run by hand.
func TestARunWaitingOnGitHubGetsItsOwnSlotBeforeNewWork(t *testing.T) {
	t.Parallel()

	const (
		id     = "yoyodyne-ifd.428.126.4.19"
		holder = "yoyodyne-ifd.428.164"
	)
	first := beads.WorkItem{ID: "yoyodyne-fresh-one", Title: "fresh one", Status: "open", Priority: 0}
	second := beads.WorkItem{ID: "yoyodyne-fresh-two", Title: "fresh two", Status: "open", Priority: 0}
	harness := newScheduleHarness(first, second)
	harness.capacity = 2
	harness.poll = 5 * time.Millisecond
	pushed := harness.now.Add(-2 * time.Hour)
	waiting := inSlot(waitingOnGitHub(id, "run-13060195cae66c2d869b6fc52a4ff914", pushed), 2, pushed)
	waiting.CIWait.WaitLimitSeconds = int64(time.Hour / time.Second)
	harness.inFlight[id] = waiting
	harness.inFlight[holder] = inSlot(runstate.State{RunID: "run-a7a810a69682b78c0f4c19df98de48ed", WorkItemID: holder,
		Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping}, 2, pushed)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	defer letGo()
	harness.run = func(h *scheduleHarness, workItemID string) (Outcome, error) {
		if workItemID == first.ID {
			// The first new item keeps slot 1 for the whole test, so slot 2 is the
			// only one the waiting run and the second new item can have.
			<-release
		}
		return h.complete(workItemID), nil
	}
	var refusals []string
	var mu sync.Mutex
	// The store's own refusal: a run keeping slot 2's endpoint pair is not taken
	// up while another run holds slot 2.
	start := func(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
		if workItemID == id {
			harness.mu.Lock()
			_, held := harness.inFlight[holder]
			harness.mu.Unlock()
			if held {
				mu.Lock()
				refusals = append(refusals, workItemID)
				mu.Unlock()
				return Outcome{}, fmt.Errorf("reserve developer run: %w", runstate.CapacityError{Limit: 2, Active: 1, Slot: 2, HeldBy: "run-a7a810a69682b78c0f4c19df98de48ed", Pinned: true})
			}
		}
		return harness.start(ctx, workItemID, selection)
	}
	var passedOver []string
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 3 {
			// The run holding slot 2 finishes.
			h.mu.Lock()
			delete(h.inFlight, holder)
			h.mu.Unlock()
		}
		if slices.Contains(h.pullOrder(), second.ID) || pulls > 40 {
			letGo()
			cancel()
		}
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return ctx.Err() == nil }
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CIWaits = decidedGitHub{}
		pull.Continuations = waitingContinuations{}
		pull.Start = start
		return pull, err
	}
	schedule, _ := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: &recordedSessions{}}.Schedule(ctx)
	for _, deferred := range schedule.Deferred {
		passedOver = append(passedOver, deferred.Reason)
	}

	order := harness.pullOrder()
	waited, fresh := slices.Index(order, id), slices.Index(order, second.ID)
	if waited < 0 || (fresh >= 0 && fresh < waited) {
		t.Fatalf("starts = %v, want the run waiting on GitHub taken up before the second new item", order)
	}
	if len(refusals) != 0 {
		t.Fatalf("the waiting run was started %d time(s) while its slot was held, want it passed over instead", len(refusals))
	}
	said := false
	for _, reason := range passedOver {
		if strings.Contains(reason, "developer slot 2 is free") && strings.Contains(reason, "run-a7a810a69682b78c0f4c19df98de48ed holds the slot now") &&
			strings.Contains(reason, "its wait passed its limit of 1h0m0s") {
			said = true
		}
	}
	if !said {
		t.Fatalf("passed over = %q, want the run named as waiting for slot 2, who holds it, and when its wait passed its limit", passedOver)
	}
}

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

// A run waiting on GitHub's checks past its limit that keeps slot 2's endpoint
// pair, while another run holds slot 2 for the whole test, is continued by the
// first slot to free — slot 1 — ahead of a fresh priority 0 item, and once.
// Before, it waited for slot 2 alone while slot 1 went to fresh work, which is
// how runs on slot 2 sat for hours on November 20. The store moves it and
// switches it to slot 1's own endpoints (runstate's takeUpSlot); the scheduler
// starts it there and says so.
func TestARunKeepingSlotTwosPairTakesTheFirstFreedSlotBeforeNewWork(t *testing.T) {
	t.Parallel()

	const (
		id     = "yoyodyne-ifd.429.127.2"
		holder = "yoyodyne-ifd.428.181"
		ending = "yoyodyne-ifd.432.25.15"
	)
	fresh := beads.WorkItem{ID: "yoyodyne-ifd.431.13", Title: "warn about a stale build", Status: "open", Priority: 0}
	harness := newScheduleHarness(fresh)
	harness.capacity = 2
	harness.poll = 5 * time.Millisecond
	pushed := harness.now.Add(-2 * time.Hour)
	waiting := inSlot(waitingOnGitHub(id, "run-e63fbad1", pushed), 2, pushed)
	waiting.CIWait.WaitLimitSeconds = int64(time.Hour / time.Second)
	harness.inFlight[id] = waiting
	harness.inFlight[holder] = inSlot(runstate.State{RunID: "run-a15c973e", WorkItemID: holder,
		Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping}, 2, pushed)
	harness.inFlight[ending] = runstate.State{RunID: "run-380d6f6a", WorkItemID: ending, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping,
		Routing: &runstate.RunRouting{Version: 2, Slot: &runstate.RoutedSlot{Number: 1, Origin: runstate.RoutingClaimed, RecordedAt: pushed}}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	holderBusyAtStart := false
	harness.run = func(h *scheduleHarness, workItemID string) (Outcome, error) {
		if workItemID == id {
			h.mu.Lock()
			_, held := h.inFlight[holder]
			h.mu.Unlock()
			mu.Lock()
			holderBusyAtStart = held
			mu.Unlock()
		}
		return h.complete(workItemID), nil
	}
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 3 {
			// The run in slot 1 finishes; slot 2 stays busy for the whole test.
			h.mu.Lock()
			delete(h.inFlight, ending)
			h.mu.Unlock()
		}
		if slices.Contains(h.pullOrder(), fresh.ID) || pulls > 40 {
			cancel()
		}
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return ctx.Err() == nil }
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CIWaits = decidedGitHub{}
		pull.Continuations = waitingContinuations{}
		return pull, err
	}
	schedule, _ := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: &recordedSessions{}}.Schedule(ctx)

	order := harness.pullOrder()
	continued, started := slices.Index(order, id), slices.Index(order, fresh.ID)
	if continued < 0 || (started >= 0 && started < continued) {
		t.Fatalf("starts = %v, want the run keeping slot 2's pair continued before the fresh item", order)
	}
	var attempts []Started
	for _, entry := range schedule.Started {
		if entry.WorkItemID == id {
			attempts = append(attempts, entry)
		}
	}
	if len(attempts) != 1 || attempts[0].Failure != "" || attempts[0].Declined != "" {
		t.Fatalf("starts for the run = %#v, want it started once", attempts)
	}
	mu.Lock()
	defer mu.Unlock()
	if !holderBusyAtStart {
		t.Fatalf("the run was continued only after slot 2 freed, want it continued in slot 1 while slot 2 was busy")
	}
	for _, says := range []string{"developer slot 2, which it recorded, is held by run run-a15c973e", "switched to that slot's own endpoints"} {
		if !strings.Contains(attempts[0].Reason, says) {
			t.Fatalf("reason = %q, want it to say %q", attempts[0].Reason, says)
		}
	}
}

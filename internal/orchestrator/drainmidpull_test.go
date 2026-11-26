package orchestrator

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A pull that is still reading the queue when the wind-down bound for a move
// onto a new build runs out starts nothing more and goes back to the top of
// the loop, where the session stops its runs and restarts itself. Before this
// the pull asked the drain once, near its start, and went on to start the work
// it then found; a pull slowed by the tracker started work minutes past the
// bound and kept the session from restarting until the supervisor stopped it
// (docs/diagnoses/yoyodyne-ifd-428-192-scheduler-past-its-wind-down-limit.md).
//
// Here the slow part is the reasons the pull keeps on items that are not ready:
// one write takes the clock past the bound, inside the pull that first finds
// work for the free slot.
func TestAPullStillReadingWhenTheWindDownBoundRunsOutStartsNothingAndRestarts(t *testing.T) {
	t.Parallel()

	const checking, late, waiting = "yoyodyne-checking", "yoyodyne-late", "yoyodyne-waiting"
	items := readyItems(checking, late)
	items = append(items, beads.WorkItem{ID: waiting, Title: waiting, Status: "open", Priority: 2,
		Dependencies: []beads.Dependency{{ID: checking, Type: beads.BlocksDependency}}})
	harness := newScheduleHarness(items...)
	harness.capacity = 2
	harness.poll = 5 * time.Millisecond
	// The second slot has nothing ready until the move has begun, and the third
	// item waits on the first for the whole test, so every pull keeps a reason
	// on it.
	harness.ReadyItems[late] = false
	harness.ReadyItems[waiting] = false
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	stageStarted := harness.clock()

	var mu sync.Mutex
	slow := false
	var boundPassedAt time.Time
	checkingStarted := make(chan struct{}, 1)
	harness.onPull = func(h *scheduleHarness, pulls int) {
		switch pulls {
		case 1:
			// The next look sees the deploy and the running stage.
			<-checkingStarted
		case 2:
			// The move has begun; work for the free slot is ready, and this
			// pull is slow.
			h.mu.Lock()
			h.ReadyItems[late] = true
			h.mu.Unlock()
			mu.Lock()
			slow = true
			mu.Unlock()
		}
	}
	harness.onSchedulingWait = func(h *scheduleHarness) {
		mu.Lock()
		defer mu.Unlock()
		if !slow {
			return
		}
		slow = false
		h.mu.Lock()
		h.now = h.now.Add(h.drainLimit + time.Minute)
		boundPassedAt = h.now
		h.mu.Unlock()
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseChecking
		state.CheckStage = &runstate.CheckStage{StartedAt: stageStarted, BoundSeconds: int64((5 * time.Hour) / time.Second), Command: "make race"}
		h.inFlight[id] = state
		h.mu.Unlock()
		if id == checking {
			deployment.deploy()
			checkingStarted <- struct{}{}
		}
		<-ctx.Done()
		var drained RedeployDrain
		if !errors.As(context.Cause(ctx), &drained) {
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: runstate.PhaseChecking, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduleWithin(scheduler, time.Minute)
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted on its own: %s", schedule.Stopped, schedule.Render())
	}
	mu.Lock()
	passed := boundPassedAt
	mu.Unlock()
	if passed.IsZero() {
		t.Fatal("no pull kept a reason after the move began, so the bound never ran out inside a pull")
	}
	// Nothing is started by the old build once the bound has run out: the free
	// slot is left to the session the restart brings up.
	if order := harness.pullOrder(); !slices.Equal(order, []string{checking}) {
		t.Fatalf("started = %v, want only the run started before the move", order)
	}
	// The restart follows the pull that found the bound out, rather than a
	// forced stop minutes later: no time passes on the session's clock between
	// the slow write and the restart.
	if restartedAfter := harness.clock().Sub(passed); restartedAfter != 0 {
		t.Fatalf("the session restarted %s after the bound ran out in its pull, want at the next look", restartedAfter)
	}
	if schedule.Drain == nil || !schedule.Drain.BoundReached || !slices.Contains(schedule.Drain.Stopped, checking) {
		t.Fatalf("drain = %#v, want the bound reached and the running run stopped for the session that comes back", schedule.Drain)
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "stopped and preserved") {
		t.Fatalf("stopped reason = %q, want the preserved run named", reason)
	}
}

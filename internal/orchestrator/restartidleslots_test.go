package orchestrator

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// restartCheckRun is a hosted run standing in a 45-minute check, launched as
// the build is deployed, which reports the stop it was given when its context
// ends. It records the cause it was stopped with, so a test can tell a check
// let finish from one cut short.
type restartCheckRun struct {
	began   time.Time
	running chan struct{}
	mu      sync.Mutex
	cause   *RedeployDrain
}

func (r *restartCheckRun) run(ctx context.Context, h *scheduleHarness, id string, deployment *deployedOver) (Outcome, error) {
	launched := r.began
	h.mu.Lock()
	state := h.inFlight[id]
	state.Phase = runstate.PhaseChecking
	state.CheckStage = &runstate.CheckStage{StartedAt: r.began, BoundSeconds: int64((5 * time.Hour) / time.Second), Command: "make race", CheckLimitSeconds: int64((45 * time.Minute) / time.Second), CheckStartedAt: &launched}
	h.inFlight[id] = state
	h.mu.Unlock()
	deployment.deploy()
	r.running <- struct{}{}
	<-ctx.Done()
	if drained, ok := drainedForRedeploy(ctx); ok {
		r.mu.Lock()
		r.cause = &drained
		r.mu.Unlock()
	}
	return pausedAtChecks(ctx, id)
}

func (r *restartCheckRun) stoppedWith() *RedeployDrain {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cause
}

// A build move past its wait limit, with one developer slot free and work
// ready for it, does not keep that slot empty while a check runs in another
// slot: the session stops waiting for the check, stops that run part-way
// through it for the session that comes back, and restarts at once, so the
// new build fills the free slot. Before this the session waited out the
// check's own limit with the slot empty, which on October 31 was 14:46 to
// 15:02 PDT with 231 items ready.
func TestARestartPastItsBoundStopsWaitingForACheckWhileAFreeSlotHasReadyWork(t *testing.T) {
	t.Parallel()

	const checking, next = "yoyodyne-racing", "yoyodyne-next"
	harness := newScheduleHarness(readyItems(checking, next)...)
	harness.capacity = 2
	// The second item is not ready until the bound has run out, so the free
	// slot is empty when the wait on the check begins.
	harness.ReadyItems[next] = false
	harness.drainLimit = 15 * time.Minute
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	run := &restartCheckRun{began: began, running: make(chan struct{}, 1)}
	var stage atomic.Int32
	var pullsPast atomic.Int32
	var readyAt time.Time
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 0 {
			return
		}
		switch stage.Load() {
		case 0:
			<-run.running
			stage.Store(1)
		case 1:
			h.mu.Lock()
			h.now = began.Add(h.drainLimit)
			h.mu.Unlock()
			if _, said := waitedOnCheck(sessions); said {
				// While the free slot has nothing ready, the check is let run.
				if pullsPast.Add(1) < 4 {
					return
				}
				if stopped := run.stoppedWith(); stopped != nil {
					t.Errorf("the check was stopped with %+v before the free slot had work", stopped)
				}
				h.mu.Lock()
				h.ReadyItems[next] = true
				readyAt = h.now
				h.mu.Unlock()
				stage.Store(2)
			}
		}
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		return run.run(ctx, h, id, deployment)
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduleWithin(scheduler, time.Minute)
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted: %s", schedule.Stopped, schedule.Render())
	}
	if stage.Load() != 2 {
		t.Fatalf("the session restarted before the free slot had ready work (stage %d)", stage.Load())
	}
	// The restart is the next look after the slot had work: within the wait
	// limit and one poll of the move beginning, and nowhere near the check's
	// own 45-minute limit.
	if waited := harness.clock().Sub(readyAt); waited > time.Minute {
		t.Fatalf("the session restarted %s after work was ready for the free slot, want within one poll", waited)
	}
	if elapsed := harness.clock().Sub(began); elapsed > harness.drainLimit+time.Minute {
		t.Fatalf("the session restarted %s after the move began, want within its %s limit and one poll", elapsed, harness.drainLimit)
	}
	if got := schedule.Drain.ChecksCut; !slices.Equal(got, []string{checking}) {
		t.Fatalf("checks cut = %v, want the run whose check kept the slot empty", got)
	}
	if len(schedule.Drain.ChecksWaited) != 0 {
		t.Fatalf("checks waited = %v, want none once the wait was given up", schedule.Drain.ChecksWaited)
	}
	if !slices.Contains(schedule.Drain.Stopped, checking) {
		t.Fatalf("drain stopped = %v, want the checking run stopped for the session that comes back", schedule.Drain.Stopped)
	}
	// The old build starts nothing past its bound: the free slot is left to the
	// session the restart brings up.
	if order := harness.pullOrder(); !slices.Equal(order, []string{checking}) {
		t.Fatalf("started = %v, want only the checking run started by the old build", order)
	}
	stopped := run.stoppedWith()
	if stopped == nil || stopped.WaitedFor != "" {
		t.Fatalf("the run was stopped with %+v, want a stop cutting its check short rather than one that waited for it", stopped)
	}
	if started := schedule.Started[0]; started.Outcome.RedeployStop == nil || started.Outcome.RedeployStop.Phase != runstate.PhaseChecking {
		t.Fatalf("started = %#v, want the run stopped at its checks for the session that comes back", started)
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "stopped part-way") || !strings.Contains(reason, checking) {
		t.Fatalf("stopped reason = %q, want the cut check named", reason)
	}
}

// A slot still held by a run stopped for the move is as idle as a free one:
// nothing runs in it, and only the new build can continue that run. So the
// session past its bound does not keep it that way while a check runs in its
// other slot, even with nothing else ready.
func TestARestartPastItsBoundStopsWaitingForACheckWhileARunStoppedForItHoldsASlot(t *testing.T) {
	t.Parallel()

	const checking, developing = "yoyodyne-racing", "yoyodyne-developing"
	harness := newScheduleHarness(readyItems(checking, developing)...)
	harness.capacity = 2
	harness.drainLimit = 15 * time.Minute
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	run := &restartCheckRun{began: began, running: make(chan struct{}, 2)}
	var stage atomic.Int32
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 0 {
			return
		}
		switch stage.Load() {
		case 0:
			<-run.running
			<-run.running
			stage.Store(1)
		case 1:
			h.mu.Lock()
			h.now = began.Add(h.drainLimit)
			h.mu.Unlock()
		}
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		if id == checking {
			return run.run(ctx, h, id, deployment)
		}
		// A developer attempt, which the bound stops where it is.
		h.mu.Lock()
		state := h.inFlight[id]
		state.ProviderSessionID = "session-" + id
		h.inFlight[id] = state
		h.mu.Unlock()
		run.running <- struct{}{}
		<-ctx.Done()
		outcome, err := pausedAtChecks(ctx, id)
		if outcome.RedeployStop != nil {
			outcome.RedeployStop.Phase = runstate.PhaseDeveloping
		}
		return outcome, err
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduleWithin(scheduler, time.Minute)
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted: %s", schedule.Stopped, schedule.Render())
	}
	if elapsed := harness.clock().Sub(began); elapsed > harness.drainLimit+time.Minute {
		t.Fatalf("the session restarted %s after the move began, want within its %s limit and one poll", elapsed, harness.drainLimit)
	}
	if got := schedule.Drain.ChecksCut; !slices.Equal(got, []string{checking}) {
		t.Fatalf("checks cut = %v, want the run whose check kept the stopped run's slot idle", got)
	}
	if got := schedule.Drain.Stopped; !slices.Equal(got, []string{developing, checking}) {
		t.Fatalf("drain stopped = %v, want both runs stopped for the session that comes back", got)
	}
}

// A free slot with nothing ready for it is not kept empty by the wait, so the
// session goes on letting the running check finish rather than starting it
// over on the new build.
func TestARestartPastItsBoundStillLetsACheckFinishWhenTheFreeSlotHasNoWork(t *testing.T) {
	t.Parallel()

	const checking = "yoyodyne-racing"
	harness := newScheduleHarness(readyItems(checking)...)
	harness.capacity = 2
	harness.drainLimit = 15 * time.Minute
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	launched := began
	running := make(chan context.Context, 1)
	finish := make(chan struct{})
	var stage atomic.Int32
	var pullsPast atomic.Int32
	var hosted context.Context
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 0 {
			return
		}
		switch stage.Load() {
		case 0:
			hosted = <-running
			stage.Store(1)
		case 1:
			h.mu.Lock()
			h.now = began.Add(h.drainLimit)
			h.mu.Unlock()
			if _, said := waitedOnCheck(sessions); said {
				stage.Store(2)
			}
		case 2:
			if pullsPast.Add(1) < 5 {
				return
			}
			if hosted.Err() != nil {
				t.Errorf("the running check was stopped with a free slot and nothing ready: %v", context.Cause(hosted))
			}
			stage.Store(3)
			close(finish)
		}
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseChecking
		state.CheckStage = &runstate.CheckStage{StartedAt: began, BoundSeconds: int64((5 * time.Hour) / time.Second), Command: "make race", CheckLimitSeconds: int64((45 * time.Minute) / time.Second), CheckStartedAt: &launched}
		h.inFlight[id] = state
		h.mu.Unlock()
		deployment.deploy()
		running <- ctx
		select {
		case <-finish:
		case <-ctx.Done():
			return pausedAtChecks(ctx, id)
		}
		if !yieldBetweenChecks(ctx) {
			t.Error("the run was not asked to stop before its next check")
			return h.complete(id), nil
		}
		return pausedAtChecks(ctx, id)
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session restarted once the check ended: %s", schedule.Stopped, schedule.Render())
	}
	if len(schedule.Drain.ChecksCut) != 0 {
		t.Fatalf("checks cut = %v, want none with nothing ready for the free slot", schedule.Drain.ChecksCut)
	}
	if got := schedule.Drain.ChecksWaited; !slices.Equal(got, []string{checking}) {
		t.Fatalf("checks waited = %v, want the check let finish", got)
	}
}

// scheduleWithin runs a pass that should restart on its own, under a deadline
// of its own: a session that goes on waiting for the check is cancelled at the
// deadline and reports that instead of a restart, so the test fails in a
// minute rather than at the test binary's own timeout.
func scheduleWithin(scheduler Scheduler, deadline time.Duration) (Schedule, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	return scheduler.Schedule(ctx)
}

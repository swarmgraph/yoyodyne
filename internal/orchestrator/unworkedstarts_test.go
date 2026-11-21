package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A move onto a new build that has fallen due does not wait on a start nobody
// is working. The start here has had no run on record for longer than
// countedGrace — a dispatch the tracker kept waiting, as it did for minutes at
// a time on 2026-11-21 — and the drain bound is an hour away. The session
// stops it with the drain as its cause, restarts at once rather than at the
// bound, names it, and the session that comes back chooses its item again.
func TestADueMoveDoesNotWaitOnAStartNobodyIsWorking(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-unworked")...)
	harness.drainLimit = time.Hour
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	var cause atomic.Value
	var secondSession atomic.Bool
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		if secondSession.Load() {
			return h.complete(id), nil
		}
		// No run on record, and the clock past the grace a dispatch is given to
		// reserve one: nothing holds a lease or works in a slot for this start.
		h.mu.Lock()
		delete(h.inFlight, id)
		h.now = h.now.Add(countedGrace + time.Minute)
		h.mu.Unlock()
		deployment.deploy()
		<-ctx.Done()
		cause.Store(context.Cause(ctx))
		return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
	}

	first := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-before", Deployment: deployment, stoppedRunGrace: 50 * time.Millisecond}
	done := make(chan Schedule, 1)
	go func() {
		schedule, err := first.Schedule(context.Background())
		if err != nil {
			t.Errorf("Schedule() error = %v", err)
		}
		done <- schedule
	}()
	var schedule Schedule
	select {
	case schedule = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the session went on waiting for a start nobody was working rather than moving onto the new build")
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted onto the new build: %s", schedule.Stopped, schedule.Render())
	}
	if elapsed := harness.clock().Sub(began); elapsed >= harness.drainLimit {
		t.Fatalf("the session restarted %s after it began, want it well inside its drain bound of %s", elapsed, harness.drainLimit)
	}
	if schedule.Drain == nil || schedule.Drain.BoundReached {
		t.Fatalf("drain = %#v, want the restart made before the bound was reached", schedule.Drain)
	}
	if got := schedule.Drain.Unworked; len(got) != 1 || got[0] != "yoyodyne-unworked" {
		t.Fatalf("drain unworked = %v, want the start nobody was working named", got)
	}
	var drained RedeployDrain
	if stoppedWith, _ := cause.Load().(error); !errors.As(stoppedWith, &drained) || drained.SessionID != "watch-before" {
		t.Fatalf("the start ended with %v, want the drain as its cause", cause.Load())
	}
	noted := false
	for _, transition := range sessions.recorded() {
		if strings.Contains(transition.reason, "nobody was working them, so the move does not wait on them") && strings.Contains(transition.reason, "yoyodyne-unworked") {
			noted = true
		}
	}
	if !noted {
		t.Fatal("the session did not say it stopped waiting on the start nobody was working")
	}

	// The session that comes back chooses the item again.
	secondSession.Store(true)
	harness.mu.Lock()
	harness.order = nil
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	harness.mu.Unlock()
	second := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, SessionID: "watch-after"}
	next, err := second.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(next.Started) != 1 || next.Started[0].WorkItemID != "yoyodyne-unworked" {
		t.Fatalf("started = %#v, want the item chosen again by the session that comes back", next.Started)
	}
}

// stopUnworked lets go only of a start with no run in flight on record past
// countedGrace. A start moments old is still reserving and is left; a start
// whose run is in flight on record, waiting on GitHub's checks included, is
// left to the drain's own rules and keeps everything it has; a run at its
// landing checks is left to stopHosted.
func TestOnlyAStartWithNoRunOnRecordPastItsGraceIsLetGo(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 21, 11, 8, 0, 0, time.UTC)
	scheduler := Scheduler{Now: func() time.Time { return now }, SessionID: "watch-drain"}
	schedule := Schedule{
		Started: []Started{
			{WorkItemID: "fresh", began: now.Add(-time.Minute)},
			{WorkItemID: "waiting-on-checks", began: now.Add(-time.Hour)},
			{WorkItemID: "stale", began: now.Add(-time.Hour)},
			{WorkItemID: "landing", began: now.Add(-time.Hour)},
		},
		Drain: &ScheduleDrain{},
	}
	drain := redeployDrain{active: true, since: now.Add(-time.Minute), limit: 15 * time.Minute}
	causes := map[int]error{}
	hosted := map[int]context.CancelCauseFunc{}
	for index := range schedule.Started {
		hosted[index] = func(cause error) { causes[index] = cause }
	}
	mine := map[string]int{"fresh": 0, "waiting-on-checks": 1, "stale": 2, "landing": 3}
	landings := &hostedLandings{landing: map[int]bool{3: true}}
	runs := fixedRuns{{RunID: "run-waiting", WorkItemID: "waiting-on-checks", Status: runstate.StatusRunning,
		CIWait: &runstate.CIWait{Commit: "abc123", PushedAt: now.Add(-30 * time.Minute)}}}

	unworked := scheduler.stopUnworked(&schedule, &drain, hosted, landings, mine, runs)
	if len(unworked) != 1 || unworked[0] != "stale" {
		t.Fatalf("unworked = %v, want only the start with no run on record past its grace", unworked)
	}
	if _, still := hosted[2]; still {
		t.Fatal("the start nobody was working is still counted as hosted, so the restart would wait on it")
	}
	for _, index := range []int{0, 1, 3} {
		if _, still := hosted[index]; !still {
			t.Fatalf("%s was let go of, want it left hosted", schedule.Started[index].WorkItemID)
		}
		if causes[index] != nil {
			t.Fatalf("%s was cancelled with %v, want it left running", schedule.Started[index].WorkItemID, causes[index])
		}
	}
	var drained RedeployDrain
	if !errors.As(causes[2], &drained) || !drained.At.Equal(now) || drained.Bound != 15*time.Minute {
		t.Fatalf("cause = %v, want the drain, dated now, under the session's bound", causes[2])
	}
	if got := schedule.Drain.Unworked; len(got) != 1 || got[0] != "stale" {
		t.Fatalf("drain unworked = %v, want the start named on the schedule", got)
	}
	// Asked again, nothing more is let go of and nothing is named twice.
	if again := scheduler.stopUnworked(&schedule, &drain, hosted, landings, mine, runs); len(again) != 0 || len(schedule.Drain.Unworked) != 1 {
		t.Fatalf("second look let go of %v and named %v, want nothing more", again, schedule.Drain.Unworked)
	}
}

// fixedRuns is a set of runs on record that does not change.
type fixedRuns []runstate.State

func (r fixedRuns) Incomplete() ([]runstate.State, error) { return r, nil }

package orchestrator

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// gitHubDecidesLater is the forge as a pull reads it for a run waiting on
// GitHub's checks: undecided until a test says GitHub has decided.
type gitHubDecidesLater struct{ decided *atomic.Bool }

func (g gitHubDecidesLater) Decided(context.Context, runstate.State) (bool, string, error) {
	if g.decided.Load() {
		return true, "every required check passed on the commit pushed", nil
	}
	return false, "GitHub has not finished the checks", nil
}

// hostedDeveloperRun is a run the session hosts at its developer attempt, with
// a developer session recorded, as the scheduler's own process carries it. It
// records the cause its context ended with, so a test can tell a run stopped
// for the move, which the session that comes back continues, from one
// cancelled.
type hostedDeveloperRun struct {
	mu     sync.Mutex
	ctx    context.Context
	cause  error
	ended  bool
	signal chan struct{}
}

func (r *hostedDeveloperRun) run(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
	h.mu.Lock()
	state := h.inFlight[id]
	state.Phase = runstate.PhaseDeveloping
	state.ProviderSessionID = "session-" + id
	state.CIWait = nil
	h.inFlight[id] = state
	h.mu.Unlock()
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
	if r.signal != nil {
		r.signal <- struct{}{}
	}
	<-ctx.Done()
	r.mu.Lock()
	r.cause, r.ended = context.Cause(ctx), true
	r.mu.Unlock()
	var drained RedeployDrain
	if !errors.As(context.Cause(ctx), &drained) {
		return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
	}
	return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: runstate.PhaseDeveloping, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
}

func (r *hostedDeveloperRun) endedWith() (error, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cause, r.ended
}

func (r *hostedDeveloperRun) live() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ctx != nil && r.ctx.Err() == nil
}

// drainRecorded reports the session having said it is moving onto a new build.
func drainRecorded(sessions *recordedSessions) bool {
	for _, transition := range sessions.recorded() {
		if transition.draining != nil {
			return true
		}
	}
	return false
}

// While the scheduler waits to move onto a new build and hosts a developer
// run in its own process, a run waiting on GitHub's checks that comes due is
// continued into the free developer slot within one pass, rather than left for
// the session that comes back; and when the move reaches its limit it goes
// ahead, stopping the hosted run for the new build to continue rather than
// cancelling it. On November 20 the continuation of the run for
// yoyodyne-ifd.429.95 was due from 07:43 PST while slots stood free, and the
// development manager asked whether the move was what held it.
func TestADueContinuationStartsWhileAMoveWaitsOnAHostedRunAndTheMoveGoesAheadAtItsLimit(t *testing.T) {
	t.Parallel()

	const (
		hostedID  = "yoyodyne-ifd.428.179"
		waitingID = "yoyodyne-ifd.429.95"
	)
	harness := newScheduleHarness(readyItems(hostedID)...)
	harness.capacity = 2
	harness.poll = 5 * time.Millisecond
	harness.drainLimit = 15 * time.Minute
	pushed := harness.now.Add(-30 * time.Minute)
	waiting := waitingOnGitHub(waitingID, "run-bf23510ae0e455ab463cdd6357ce4183", pushed)
	waiting.CIWait.WaitLimitSeconds = int64(time.Hour / time.Second)
	harness.inFlight[waitingID] = waiting

	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	began := harness.clock()
	hosted := &hostedDeveloperRun{signal: make(chan struct{}, 1)}
	continued := &hostedDeveloperRun{}
	var decided atomic.Bool
	var stage atomic.Int32
	dueAt, seenAt := -1, -1
	hostedLiveAtStart := false
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		if id == waitingID {
			hostedLiveAtStart = hosted.live()
			return continued.run(ctx, h, id)
		}
		deployment.deploy()
		return hosted.run(ctx, h, id)
	}
	harness.onPull = func(h *scheduleHarness, pulls int) {
		switch stage.Load() {
		case 0:
			if pulls == 0 {
				return
			}
			<-hosted.signal
			stage.Store(1)
		case 1:
			// The move is pending and the hosted run is working; GitHub now
			// decides the waiting run, so its continuation is due.
			if drainRecorded(sessions) {
				decided.Store(true)
				dueAt = pulls
				stage.Store(2)
			}
		case 2:
			if slices.Contains(h.pullOrder(), waitingID) {
				seenAt = pulls
				// The move reaches its limit.
				h.mu.Lock()
				h.now = began.Add(h.drainLimit + time.Minute)
				h.mu.Unlock()
				stage.Store(3)
			}
		}
	}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CIWaits = gitHubDecidesLater{decided: &decided}
		pull.Continuations = waitingContinuations{}
		return pull, err
	}

	// A real-time cap well past what the test needs: the race run on the forge
	// runs this package beside many others, and a cap of a minute is what a
	// loaded machine can overrun, which has failed this test with nothing wrong.
	scheduler := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-moving", Deployment: deployment}
	schedule, err := scheduleWithin(scheduler, 5*time.Minute)
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if stage.Load() != 3 {
		t.Fatalf("the continuation was never started while the move was pending (stage %d): %s", stage.Load(), schedule.Render())
	}
	if seenAt-dueAt > 1 {
		t.Fatalf("the continuation started %d pulls after it fell due, want within one", seenAt-dueAt)
	}
	if !hostedLiveAtStart {
		t.Fatal("the hosted run was not working when the continuation started, want both going while the move was pending")
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the move to go ahead at its limit: %s", schedule.Stopped, schedule.Render())
	}
	for _, run := range []struct {
		id  string
		run *hostedDeveloperRun
	}{{hostedID, hosted}, {waitingID, continued}} {
		cause, ended := run.run.endedWith()
		var drained RedeployDrain
		if !ended || !errors.As(cause, &drained) {
			t.Fatalf("%s ended with %v, want it stopped for the move so the new build continues it, not cancelled", run.id, cause)
		}
		if !slices.Contains(schedule.Drain.Stopped, run.id) {
			t.Fatalf("drain stopped = %v, want %s stopped and kept for the session that comes back", schedule.Drain.Stopped, run.id)
		}
		harness.mu.Lock()
		left := harness.inFlight[run.id]
		harness.mu.Unlock()
		if left.Status != runstate.StatusRunning || left.RedeployStop == nil || left.RedeployStop.Phase != runstate.PhaseDeveloping {
			t.Fatalf("%s left as %#v, want it in flight carrying its stop at the developer attempt", run.id, left)
		}
	}
}

// Past the move's limit, a wait on a running check is given up for a free
// slot with a due continuation or a decided re-run waiting for it, exactly as
// for ready work in the queue: neither is in the queue, because its item is
// claimed, and leaving the slot empty until the check ends costs the work the
// harness takes first.
func TestAWaitOnACheckIsGivenUpForAFreeSlotWithWorkOnAKeptChange(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	pull, err := harness.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	occupied := map[string]runstate.State{"yoyodyne-racing": {RunID: "run-racing", WorkItemID: "yoyodyne-racing", Status: runstate.StatusRunning, Phase: runstate.PhaseChecking}}
	mine := map[string]int{"yoyodyne-racing": 0}
	hosted := map[int]context.CancelCauseFunc{0: func(error) {}}
	ask := func(closed bool, keptDue func() bool) int {
		drain := redeployDrain{checkWaits: []runstate.WatchCheckWait{{RunID: "run-racing", WorkItemID: "yoyodyne-racing", Check: "make race"}}}
		return Scheduler{}.idleBesideCheckWaits(context.Background(), &drain, pull, occupied, map[string]bool{}, mine, hosted, 1, closed, nil, false, keptDue)
	}
	due := func() bool { return true }
	if got := ask(false, nil); got != 0 {
		t.Fatalf("idle slots = %d, want none with nothing waiting for the free slot", got)
	}
	if got := ask(false, due); got != 1 {
		t.Fatalf("idle slots = %d, want the free slot counted with a continuation due", got)
	}
	if got := ask(true, due); got != 0 {
		t.Fatalf("idle slots = %d, want none while intake is held or work is paused", got)
	}
}

// keptWorkWaiting finds a decision about an item with no run going, and a run
// waiting on GitHub's checks that GitHub has decided, and nothing else.
func TestKeptWorkWaitingFindsDecisionsAndDueContinuations(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	pushed := harness.now.Add(-2 * time.Hour)
	harness.inFlight["yoyodyne-waiting"] = waitingOnGitHub("yoyodyne-waiting", "run-waiting", pushed)
	var decided atomic.Bool
	pull, err := harness.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pull.CIWaits = gitHubDecidesLater{decided: &decided}
	pull.Continuations = waitingContinuations{}
	scheduler := Scheduler{Now: harness.clock}
	ask := func(pull Pull) bool {
		return scheduler.keptWorkWaiting(context.Background(), pull, map[string]runstate.State{}, map[string]int{}, map[string]refusedContinuation{}, 1, 0)
	}
	if ask(pull) {
		t.Fatal("kept work found while GitHub has not decided the waiting run and nothing is decided")
	}
	decided.Store(true)
	if !ask(pull) {
		t.Fatal("no kept work found with GitHub having decided the waiting run")
	}
}

// What a move past its limit is waiting on is said in plain words, naming the
// run and check it waits for and how many runs are still being stopped, rather
// than a list of everything it could be waiting on.
func TestAMovePastItsLimitSaysWhatItIsWaitingOn(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 11, 20, 17, 15, 16, 0, time.UTC)
	drain := redeployDrain{active: true, since: since, limit: 15 * time.Minute, boundReached: true, unready: 1, stopping: 2}
	said := drain.record(3).Says()
	for _, want := range []string{"2 run(s) it has stopped to finish recording where they got to", "1 run(s) to reach a step they can be continued from"} {
		if !strings.Contains(said, want) {
			t.Fatalf("Says() = %q, want %q in it", said, want)
		}
	}
	drain.checkWaits = []runstate.WatchCheckWait{{RunID: "run-racing", WorkItemID: "yoyodyne-racing", Check: "make race"}}
	drain.promoting = 1
	holding := drain.holdingRestart()
	for _, want := range []string{"1 run landing on the target branch", "run run-racing for yoyodyne-racing to finish the check make race", "1 run to reach a step that can be continued from", "2 runs it stopped, still recording how far each got"} {
		if !strings.Contains(holding, want) {
			t.Fatalf("holdingRestart() = %q, want %q in it", holding, want)
		}
	}
	if got := (redeployDrain{boundReached: true}).holdingRestart(); got != "the runs it hosts to be stopped" {
		t.Fatalf("holdingRestart() with nothing found = %q", got)
	}
}

// Two decided runs that each keep the endpoint pair of developer slot 2 both
// go on in one pass where two slots are free: the store moves whichever finds
// slot 2 taken to a free slot on that slot's own endpoints (runstate's
// takeUpSlot), so neither waits for slot 2 while another slot stands free, as
// the continuation for yoyodyne-ifd.429.95 did beside the one for
// yoyodyne-ifd.428.114 on November 20. With one slot free, the older goes on
// and the newer is passed over for the first free slot, ahead of new work.
func TestTwoContinuationsPinnedToOneSlotEachTakeAFreeSlot(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	pushed := harness.now.Add(-2 * time.Hour)
	pin := func(state runstate.State) runstate.State {
		state.Routing = &runstate.RunRouting{Slot: &runstate.RoutedSlot{Number: 2}, Developer: &runstate.RoutingSnapshot{}}
		return state
	}
	older := pin(waitingOnGitHub("yoyodyne-ifd.429.95", "run-older", pushed))
	newer := pin(waitingOnGitHub("yoyodyne-ifd.428.114", "run-newer", pushed))
	older.UpdatedAt, newer.UpdatedAt = pushed, pushed.Add(time.Minute)
	harness.inFlight[older.WorkItemID] = older
	harness.inFlight[newer.WorkItemID] = newer
	var decided atomic.Bool
	decided.Store(true)
	pull, err := harness.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pull.CIWaits = gitHubDecidesLater{decided: &decided}
	pull.Continuations = waitingContinuations{}
	continueWith := func(free int) ([]pausedContinuation, map[string]string) {
		t.Helper()
		passed := map[string]string{}
		got, err := Scheduler{Now: harness.clock}.nextContinuations(context.Background(), pull, map[string]runstate.State{}, map[string]int{}, map[string]refusedContinuation{}, free, 0,
			func(id, reason string) { passed[id] = reason })
		if err != nil {
			t.Fatal(err)
		}
		return got, passed
	}
	got, passed := continueWith(2)
	if len(got) != 2 || got[0].state.RunID != "run-older" || got[1].state.RunID != "run-newer" || len(passed) != 0 {
		t.Fatalf("continued %d runs, passed over %v; want both, the older first, with two slots free", len(got), passed)
	}
	got, passed = continueWith(1)
	if len(got) != 1 || got[0].state.RunID != "run-older" {
		t.Fatalf("continued %d runs with one slot free, want only the older one", len(got))
	}
	if reason := passed["yoyodyne-ifd.428.114"]; !strings.Contains(reason, "continued at the first pull with a developer slot free, ahead of any new work") {
		t.Fatalf("pass-over reason = %q, want it to say it goes on at the first free slot", reason)
	}
}

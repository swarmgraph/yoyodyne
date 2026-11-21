package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// moveStop is the stop a watch session makes for a move onto a new build.
func moveStop() RedeployDrain {
	return RedeployDrain{At: time.Date(2026, 11, 19, 18, 25, 0, 0, time.FixedZone("PST", -8*3600)), Bound: 15 * time.Minute, SessionID: "watch-before"}
}

// A step under way when its run is stopped is given its grace to finish, and
// is cancelled only once that has run out, with the stop as the cause.
func TestAStepUnderWayWhenItsRunIsStoppedIsLetFinish(t *testing.T) {
	t.Parallel()

	run, stop := context.WithCancelCause(context.Background())
	step, finished := finishingStep(run, 50*time.Millisecond)
	defer finished()
	stop(moveStop())
	time.Sleep(10 * time.Millisecond)
	if step.Err() != nil {
		t.Fatalf("the step was cancelled the moment its run was stopped: %v", context.Cause(step))
	}
	select {
	case <-step.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the step was never cancelled after its grace ran out")
	}
	var drained RedeployDrain
	if !errors.As(context.Cause(step), &drained) || drained.SessionID != "watch-before" {
		t.Fatalf("the step's cause = %v, want the stop it outlived", context.Cause(step))
	}
}

// A step whose run is never stopped ends when it returns, and nothing fires
// after it.
func TestAStepWhoseRunIsNeverStoppedEndsWithTheStep(t *testing.T) {
	t.Parallel()

	run, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	step, finished := finishingStep(run, time.Hour)
	if step.Err() != nil {
		t.Fatalf("a step under a live run started cancelled: %v", step.Err())
	}
	finished()
	if !errors.Is(step.Err(), context.Canceled) {
		t.Fatalf("the step's context after it returned = %v, want it ended", step.Err())
	}
}

// The supervisor restarts a scheduler that has gone past its limit for a move
// by asking the process to stop, which ends the session's context. A run the
// session hosts is then stopped for the move, as the drain bound would have
// stopped it, rather than ended as cancelled.
func TestASessionAskedToStopWhileMovingStopsItsRunsForTheMove(t *testing.T) {
	t.Parallel()

	session, stopSession := context.WithCancel(context.Background())
	landings := &hostedLandings{}
	runCtx, cancel := hostedContext(session, 0, landings)
	defer cancel(nil)
	before := time.Now()
	landings.moving(15*time.Minute, "watch-moving")
	stopSession()
	<-runCtx.Done()
	drained, forRedeploy := drainedForRedeploy(runCtx)
	if !forRedeploy {
		t.Fatalf("the run's cause = %v, want the move", context.Cause(runCtx))
	}
	if drained.SessionID != "watch-moving" || drained.Bound != 15*time.Minute || drained.At.Before(before) {
		t.Fatalf("the stop = %#v, want the moving session's, dated when it stopped", drained)
	}
}

// A session asked to stop with no move under way stops its runs as it always
// did, and one whose own deadline passes leaves them timed out.
func TestASessionStoppedWithNoMoveStopsItsRunsAsBefore(t *testing.T) {
	t.Parallel()

	session, stopSession := context.WithCancel(context.Background())
	runCtx, cancel := hostedContext(session, 0, &hostedLandings{})
	defer cancel(nil)
	stopSession()
	<-runCtx.Done()
	if _, forRedeploy := drainedForRedeploy(runCtx); forRedeploy || !errors.Is(runCtx.Err(), context.Canceled) {
		t.Fatalf("the run's context = %v (cause %v), want it cancelled and not for a move", runCtx.Err(), context.Cause(runCtx))
	}

	bounded, stopBounded := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopBounded()
	timed, cancelTimed := hostedContext(bounded, 1, &hostedLandings{})
	defer cancelTimed(nil)
	<-timed.Done()
	if !errors.Is(timed.Err(), context.DeadlineExceeded) {
		t.Fatalf("the run's context = %v, want it timed out with the session's deadline", timed.Err())
	}

	// And the session's own stop of one run is that run's alone.
	live, stopLive := context.WithCancel(context.Background())
	defer stopLive()
	one, cancelOne := hostedContext(live, 2, &hostedLandings{})
	cancelOne(moveStop())
	if _, forRedeploy := drainedForRedeploy(one); !forRedeploy || live.Err() != nil {
		t.Fatalf("stopping one hosted run: run cause %v, session %v", context.Cause(one), live.Err())
	}
}

// moveDuringCommit starts a move onto a new build the moment the harness begins
// committing what a developer attempt left, and then commits it, so the commit
// is the step under way when the run is stopped.
type moveDuringCommit struct {
	WorktreeManager
	stop func(error)
	cut  bool
	once sync.Once
}

func (w *moveDuringCommit) CommitAttempt(ctx context.Context, worktree gitworktree.Worktree, message string) (string, error) {
	first := false
	w.once.Do(func() {
		first = true
		w.stop(moveStop())
	})
	if first && w.cut {
		// A Git command the stop killed part-way, as the scheduler's own restart
		// did to two runs on 2026-11-19.
		return "", errors.New("list worktrees failed with exit code -1: ")
	}
	return w.WorktreeManager.CommitAttempt(ctx, worktree, message)
}

// A move that begins while the harness is committing what the developer left
// lets the commit finish: the change is on the run's branch, and the run is
// stopped for the restart, not failed, and is taken up again by the next
// process with nothing typed.
func TestAMoveDuringTheCommitOfTheDevelopersLeftoversLetsTheCommitFinish(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	develop := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	first := orchestratortest.RoleBackend(develop, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"true"}), first)
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	pipeline.Worktrees = &moveDuringCommit{WorktreeManager: pipeline.Worktrees, stop: stop}

	paused, err := pipeline.Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run stopped for the move reported as a pause", err)
	}
	if !paused.Paused || paused.RedeployStop == nil || paused.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a run stopped for the move and kept in flight", paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.HarnessCommit == "" {
		t.Fatalf("no harness commit recorded: the commit under way when the move began did not finish")
	}
	if stopped.RedeployStop == nil || stopped.RedeployStop.CutOff != "" {
		t.Fatalf("the stop = %#v, want one that cut off no step", stopped.RedeployStop)
	}
	if !stoppedForRedeployIsResumable(stopped) {
		t.Fatalf("the stopped run is not one the next session takes up: %#v", stopped)
	}
	if tracker.Blocked || tracker.Released {
		t.Fatalf("the item was blocked or released for the move: %v", tracker.Calls)
	}
	if !strings.Contains(tracker.Notes, "no step the harness had started was cut off") && !strings.Contains(tracker.Notes, "paused this run for a redeploy") {
		t.Fatalf("the item's notes do not say the run was stopped for the move:\n%s", tracker.Notes)
	}

	second := orchestratortest.RoleBackend(develop, approveVerdict)
	outcome, err := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"true"}), second).Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the run was not taken up again and finished: %#v", outcome)
	}
}

// A move that does cut a step off — a Git command killed part-way — is
// recorded as the harness's own restart, in plain words naming the step, and
// not as that step failing: nothing is charged, the run stays in flight
// holding its slot, and the next process takes it up and finishes it.
func TestAStepTheMoveCutsOffCountsAsTheRestartAndTheRunIsTakenUpAgain(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	develop := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	first := orchestratortest.RoleBackend(develop, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"true"}), first)
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	pipeline.Worktrees = &moveDuringCommit{WorktreeManager: pipeline.Worktrees, stop: stop, cut: true}

	paused, err := pipeline.Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run stopped for the move reported as a pause", err)
	}
	if !paused.Paused || paused.RedeployStop == nil || paused.Status != runstate.StatusRunning || paused.StopClass != "" {
		t.Fatalf("outcome = %#v, want a run stopped for the move rather than failed", paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.RedeployStop == nil || !strings.Contains(stopped.RedeployStop.CutOff, "list worktrees failed with exit code -1") {
		t.Fatalf("the stop = %#v, want the cut-off Git command named on it", stopped.RedeployStop)
	}
	if stopped.RepairAttempts != 0 || stopped.Status != runstate.StatusRunning || !stopped.HoldsDeveloperSlot() {
		t.Fatalf("the stopped run = status %s, repairs %d, holds slot %v; want it in flight, uncharged, holding its slot", stopped.Status, stopped.RepairAttempts, stopped.HoldsDeveloperSlot())
	}
	if !stoppedForRedeployIsResumable(stopped) {
		t.Fatalf("the stopped run is not one the next session takes up: %#v", stopped)
	}
	if said := describeRedeployStop(*stopped.RedeployStop); !strings.Contains(said, "the restart cut off a step the harness had started") || !strings.Contains(said, "not as that step failing") {
		t.Fatalf("the stop is described as %q, want it to say the restart cut a step off", said)
	}
	if !strings.Contains(tracker.Notes, "The restart cut off a step the harness had started") {
		t.Fatalf("the item's notes do not say a step was cut off by the restart:\n%s", tracker.Notes)
	}
	if tracker.Blocked || tracker.Released {
		t.Fatalf("the item was blocked or released for the move: %v", tracker.Calls)
	}

	second := orchestratortest.RoleBackend(develop, approveVerdict)
	outcome, err := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"true"}), second).Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the run was not taken up again and finished: %#v", outcome)
	}
}

// moveDuringTrackerWrite starts a move onto a new build while the run's
// outcome note is being written, and then writes it the way `bd` would: a call
// whose context is cancelled under it is killed and comes back cancelled.
type moveDuringTrackerWrite struct {
	*orchestratortest.Tracker
	stop func(error)
	once sync.Once
}

func (w *moveDuringTrackerWrite) RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error) {
	w.once.Do(func() { w.stop(moveStop()) })
	// The write takes a moment, as a busy store's does, and the move lands
	// inside it.
	time.Sleep(20 * time.Millisecond)
	if ctx.Err() != nil {
		return beads.WorkItem{}, errors.New("bd update failed with status cancelled and exit code -1: ")
	}
	return w.Tracker.RecordOutcome(ctx, id, notes)
}

// A move that begins while a run's tracker write is under way lets the write
// finish: the note lands once, and the run is not stopped as though the
// tracker had failed.
func TestAMoveDuringATrackerWriteLetsTheWriteFinish(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	develop := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	provider := orchestratortest.RoleBackend(develop, approveVerdict)
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	writes := &moveDuringTrackerWrite{Tracker: tracker, stop: stop}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, writes, provider, []string{"true"}), provider)

	outcome, err := pipeline.Run(ctx, tracker.Item.ID)
	if len(tracker.NoteRecords) == 0 {
		t.Fatalf("the note under way when the move began never landed (outcome %#v, error %v)", outcome, err)
	}
	if strings.Contains(outcome.Failure, "status cancelled") || outcome.StopClass == runstate.StopRecoveryWindow {
		t.Fatalf("the run was stopped as though the tracker had failed: %#v", outcome)
	}
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !tracker.Closed {
		t.Fatalf("outcome = %#v, item closed %v; want the run's last writes let finish and the run complete", outcome, tracker.Closed)
	}
}

// The supervisor restarts a scheduler whose move onto a new build has gone on
// past its limit by asking the process to stop, which ends the session's
// context. The runs the session hosts are then stopped for the move, and come
// back on the schedule as runs kept for the session that comes back rather than
// as cancelled ones for the development manager's list, as two runs did at
// 18:25 PST on November 19.
func TestASchedulerStoppedPartWayThroughItsMoveStopsItsRunsForTheMove(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 2
	harness.developersMeet(2)
	// A bound the test never reaches: the stop arrives from outside first.
	harness.drainLimit = time.Hour
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	var stopped sync.Map
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseDeveloping
		h.inFlight[id] = state
		h.mu.Unlock()
		<-ctx.Done()
		drained, forRedeploy := drainedForRedeploy(ctx)
		if !forRedeploy {
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled, Failure: "commit what the developer attempt left in the worktree: list worktrees failed with exit code -1: "}, nil
		}
		stopped.Store(id, drained)
		return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: state.Phase, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
	}

	ctx, stopProcess := context.WithCancel(context.Background())
	defer stopProcess()
	go func() {
		for ctx.Err() == nil {
			for _, transition := range sessions.recorded() {
				if transition.draining != nil {
					stopProcess()
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, SessionID: "watch-moving", Deployment: deployment}
	schedule, err := scheduler.Schedule(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d run(s), want both: %s", len(schedule.Started), schedule.Render())
	}
	for _, started := range schedule.Started {
		cause, found := stopped.Load(started.WorkItemID)
		if !found {
			t.Fatalf("%s was stopped as cancelled rather than for the move: %#v", started.WorkItemID, started)
		}
		if drained := cause.(RedeployDrain); drained.SessionID != "watch-moving" || drained.Bound != time.Hour {
			t.Fatalf("%s cause = %#v, want the moving session and its bound on it", started.WorkItemID, drained)
		}
		if started.Failure != "" || !started.Outcome.Paused || started.Outcome.RedeployStop == nil {
			t.Fatalf("%s = %#v, want a run kept for the session that comes back rather than failed", started.WorkItemID, started)
		}
	}
}

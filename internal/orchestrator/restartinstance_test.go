package orchestrator

// A run stopped for a restart onto a new build, and the workflow instance
// observing it. The run is kept in flight and carried on by the session that
// comes back, so its instance has to stay in the state the stop found it in
// rather than reach `abandoned`; and a stopped run that cannot be carried on is
// ended as cancelled, so its instance has to reach its ending too. Either way
// the instance and the run say the same thing about whether the run is going.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stopBetweenChecks runs pipeline under a hosting session's check yield and asks
// it to stop for a restart once the second of the restart checks is running, so
// the run stops before its third check launches.
func stopBetweenChecks(t *testing.T, pipeline Pipeline, itemID, dir string) (Outcome, error) {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx, yield := withCheckYield(ctx, cancel)
	go func() {
		for {
			if _, err := os.Stat(filepath.Join(dir, "running")); err == nil {
				yield.ask(RedeployDrain{At: time.Date(2026, 11, 13, 8, 12, 27, 0, time.FixedZone("PST", -8*3600)), Bound: 15 * time.Minute, SessionID: "watch-before"})
				time.Sleep(50 * time.Millisecond)
				_ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	return pipeline.Run(ctx, itemID)
}

// restartChecks are three checks; the second says it is running and runs until
// it is released, for at most a minute.
func restartChecks(dir string) []string {
	at := func(name string) string { return "'" + filepath.Join(dir, name) + "'" }
	return []string{
		"true",
		"touch " + at("running") + " && i=0 && while [ ! -f " + at("release") + " ] && [ $i -lt 1200 ]; do sleep 0.05; i=$((i+1)); done",
		"true",
	}
}

func writesFeature(request backend.RunRequest) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
}

// A run a restart stops between its checks is kept in flight, and its instance
// stays at the checks rather than reading `abandoned`. The session that comes
// back runs the rest of the checks, the change is approved and lands, and the
// instance follows it to `delivered` with no divergence recorded on the run.
func TestARunStoppedBetweenItsChecksForARestartKeepsItsInstanceAtTheChecks(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	dir := t.TempDir()
	commands := restartChecks(dir)
	first := orchestratortest.RoleBackend(writesFeature, approveVerdict)
	paused, err := stopBetweenChecks(t, automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, commands), first), tracker.Item.ID, dir)
	if err != nil {
		t.Fatalf("Run() error = %v, want a run stopped for a restart reported as a pause", err)
	}
	if !paused.Paused || paused.RedeployStop == nil || paused.RedeployStop.Phase != runstate.PhaseChecking {
		t.Fatalf("outcome = %#v, want a run paused at its checks", paused)
	}

	state, instance := observedRun(t, store)
	if state.Status.Terminal() {
		t.Fatalf("the stopped run is %s, want it kept in flight", state.Status)
	}
	if instance.Terminal || instance.State != deliveryCheck {
		t.Fatalf("the instance stands in %q (terminal=%t) while its run is kept in flight at its checks, want %q", instance.State, instance.Terminal, deliveryCheck)
	}
	if state.WorkflowDivergence != "" {
		t.Fatalf("the stopped run recorded the divergence %q", state.WorkflowDivergence)
	}

	second := orchestratortest.RoleBackend(writesFeature, approveVerdict)
	outcome, err := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, commands), second).Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the run was not taken up again and finished: %#v", outcome)
	}
	state, instance = observedRun(t, store)
	if state.WorkflowDivergence != "" {
		t.Fatalf("the finished run recorded the divergence %q", state.WorkflowDivergence)
	}
	if !instance.Terminal || instance.State != "delivered" {
		t.Fatalf("the instance of a run that landed ended in %q (terminal=%t), want delivered", instance.State, instance.Terminal)
	}
	for _, checkpoint := range instance.Checkpoints {
		if checkpoint.State == "abandoned" {
			t.Fatalf("the instance passed through abandoned on its way to delivered: %#v", instance.Checkpoints)
		}
	}
}

// A run a restart stops while its developer attempt is being committed keeps
// its instance at the developer's state, and the run that carries it on walks
// the instance to `delivered`.
func TestARunStoppedAtItsDeveloperForARestartKeepsItsInstanceThere(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	first := orchestratortest.RoleBackend(writesFeature, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"true"}), first)
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	pipeline.Worktrees = &moveDuringCommit{WorktreeManager: pipeline.Worktrees, stop: stop}

	paused, err := pipeline.Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run stopped for the restart reported as a pause", err)
	}
	if !paused.Paused || paused.RedeployStop == nil {
		t.Fatalf("outcome = %#v, want a run stopped for the restart and kept in flight", paused)
	}
	state, instance := observedRun(t, store)
	if instance.Terminal || instance.State != deliveryDevelop {
		t.Fatalf("the instance stands in %q (terminal=%t) while its run is kept in flight at its developer, want %q", instance.State, instance.Terminal, deliveryDevelop)
	}
	if state.WorkflowDivergence != "" {
		t.Fatalf("the stopped run recorded the divergence %q", state.WorkflowDivergence)
	}

	second := orchestratortest.RoleBackend(writesFeature, approveVerdict)
	outcome, err := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"true"}), second).Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the run was not taken up again and finished: %#v", outcome)
	}
	state, instance = observedRun(t, store)
	if state.WorkflowDivergence != "" || !instance.Terminal || instance.State != "delivered" {
		t.Fatalf("after the run landed: instance %q (terminal=%t), divergence %q; want delivered and none", instance.State, instance.Terminal, state.WorkflowDivergence)
	}
}

// restartStopUnrecorded is a store that cannot record a stop for a restart, so the
// run the restart stopped cannot be kept in flight.
type restartStopUnrecorded struct {
	*runstate.Store
}

func (s restartStopUnrecorded) Save(state runstate.State) error {
	if state.RedeployStop != nil {
		return errors.New("the disk is full")
	}
	return s.Store.Save(state)
}

// A run a restart stops and which cannot be kept in flight is ended as
// cancelled, and its instance reaches its ending with it rather than reading as
// still at the checks beside a run that is over.
func TestARunARestartEndsTakesItsInstanceToItsEnding(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	dir := t.TempDir()
	commands := restartChecks(dir)
	provider := orchestratortest.RoleBackend(writesFeature, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, restartStopUnrecorded{store}, tracker, provider, commands), provider)
	pipeline.Instances = store

	outcome, _ := stopBetweenChecks(t, pipeline, tracker.Item.ID, dir)
	if outcome.Paused {
		t.Fatalf("outcome = %#v, want the run ended because its stop could not be recorded", outcome)
	}
	state, instance := observedRun(t, store)
	if state.Status != runstate.StatusCancelled {
		t.Fatalf("the run is %s, want cancelled", state.Status)
	}
	if !instance.Terminal || instance.State != "abandoned" {
		t.Fatalf("the instance of a cancelled run stands in %q (terminal=%t), want abandoned", instance.State, instance.Terminal)
	}
	if state.WorkflowDivergence != "" {
		t.Fatalf("the cancelled run recorded the divergence %q; its instance followed it to its ending", state.WorkflowDivergence)
	}
}

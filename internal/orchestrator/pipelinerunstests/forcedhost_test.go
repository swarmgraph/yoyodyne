package pipelinerunstests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A developer run in flight when the supervisor forces its scheduler to stop is
// kept and continued, never ended as failed or started fresh. The scheduler
// was killed, so it recorded nothing: the run is left in flight at its
// developer attempt with no stop on its record, its worker the killed process,
// and in its worktree whatever the developer had written, some of it never
// committed. The supervisor records the stop the scheduler could not, and the
// scheduler started in its place continues the same run, in the same worktree
// and developer session, with everything the developer wrote, and finishes it
// with no repair spent.
func TestARunInFlightWhenItsSchedulerIsForcedToStopIsKeptAndContinued(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}

	// The first scheduler is stopped mid-attempt. The stop it would have
	// recorded is taken off the record below, so the record is what a scheduler
	// killed at that moment leaves.
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "partial.txt"), []byte("half done\n"), 0o600)
	}, testharness.ApproveVerdict)
	served := first.Respond
	first.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleDeveloper {
			return served(request)
		}
		if _, err := served(request); err != nil {
			return backend.RunResult{}, err
		}
		stop(orchestrator.RedeployDrain{At: time.Now(), Bound: 15 * time.Minute, SessionID: "watch-forced"})
		return backend.RunResult{
			Backend:    domain.BackendClaudeCode,
			SessionID:  first.DeveloperSession,
			IsError:    true,
			StopReason: string(execution.ProcessCancelled),
			Process:    execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
			LastEvent:  request.LastSequence,
		}, nil
	}
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)
	stopped, err := firstPipeline.Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	killed, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if killed.Worker == nil || killed.Phase != runstate.PhaseDeveloping {
		t.Fatalf("stopped run = %#v, want a run at its developer attempt with its worker recorded", killed)
	}
	killed.RedeployStop = nil
	if err := store.Save(killed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// Written after the last commit the run made, as a developer killed
	// mid-attempt leaves its latest work.
	if err := os.WriteFile(filepath.Join(killed.WorktreePath, "uncommitted.txt"), []byte("written as it was killed\n"), 0o600); err != nil {
		t.Fatalf("write the killed developer's last work: %v", err)
	}

	// A run of another process, also in flight, is not this scheduler's to keep.
	host := orchestrator.ForcedHost{
		PID: killed.Worker.PID, StartedAt: killed.Worker.StartedAt, SessionID: "watch-forced",
		At: time.Now(), Bound: 15 * time.Minute,
		Why: "the scheduler carrying this run did not stop when asked, so the supervisor forced it to stop at 07:37:31 PST",
	}
	other := host
	other.PID = killed.Worker.PID + 100000
	if done, err := orchestrator.PreserveRunsOfForcedHost(context.Background(), store, other); err != nil || len(done.Preserved) != 0 {
		t.Fatalf("PreserveRunsOfForcedHost(another process) = %+v, %v, want nothing changed", done, err)
	}
	done, err := orchestrator.PreserveRunsOfForcedHost(context.Background(), store, host)
	if err != nil {
		t.Fatalf("PreserveRunsOfForcedHost() error = %v", err)
	}
	if len(done.Preserved) != 1 || done.Preserved[0] != killed.RunID || len(done.Left) != 0 {
		t.Fatalf("PreserveRunsOfForcedHost() = %+v, want run %s kept", done, killed.RunID)
	}
	kept, err := store.Load(killed.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if kept.Status != runstate.StatusRunning || kept.RedeployStop == nil || kept.RedeployStop.Phase != runstate.PhaseDeveloping ||
		kept.RedeployStop.SessionID != "watch-forced" || kept.RedeployStop.CutOff == "" || kept.Failure != "" {
		t.Fatalf("kept run = %#v (stop %#v), want it in flight at its attempt, carrying the forced stop and no failure", kept, kept.RedeployStop)
	}
	again, err := orchestrator.PreserveRunsOfForcedHost(context.Background(), store, host)
	if err != nil || len(again.Preserved) != 0 {
		t.Fatalf("PreserveRunsOfForcedHost() again = %+v, %v, want nothing more to keep", again, err)
	}

	// The scheduler started in its place continues it.
	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	secondPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("continued Run() error = %v", err)
	}
	if outcome.RunID != killed.RunID || outcome.WorktreePath != killed.WorktreePath || outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("continued run = %#v (closed=%t blocked=%t), want run %s finished in %s", outcome, tracker.Closed, tracker.Blocked, killed.RunID, killed.WorktreePath)
	}
	developers := second.RequestsForRole(domain.RoleDeveloper)
	if len(developers) != 1 || developers[0].SessionID != first.DeveloperSession {
		t.Fatalf("continued developer invocations = %#v, want one, continuing session %q", developers, first.DeveloperSession)
	}
	if outcome.RepairAttempts != 0 || outcome.Failure != "" {
		t.Fatalf("repair attempts = %d failure = %q, want the forced stop to have cost nothing", outcome.RepairAttempts, outcome.Failure)
	}
	if claims := testharness.CountCalls(tracker.Calls, "claim"); claims != 1 {
		t.Fatalf("claims = %d, want the item claimed once across the forced stop", claims)
	}
	// Everything the developer wrote, before and as it was killed and after it
	// was continued, is in what landed.
	for _, file := range []string{"partial.txt", "uncommitted.txt", "feature.txt"} {
		if _, err := testharness.AttemptPipelineGit(repository, "cat-file", "-e", outcome.Integration.TargetCommit+":"+file); err != nil {
			t.Errorf("%s is not in the landed commit %s: %v", file, outcome.Integration.TargetCommit, err)
		}
	}
}

package pipelinetests

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
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run that dies of a provider hiccup used to leave a claimed item and a
// preserved worktree for a person to reconcile, reopen, and relaunch by hand.
// It relaunches itself now, in the same worktree and the same session, and
// nothing about the change is redone.
func TestRunRelaunchesAfterATransientProviderDeath(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := testharness.TransientDeathBackend(1, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(pipeline, clock, 6*time.Hour, 6*time.Hour)

	// The relaunch has to be counted on disk before it happens, or a process that
	// died here would come back to a fresh budget. The request is already
	// recorded by the time the provider is asked, so the relaunch is the second.
	var recorded runstate.State
	served := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleDeveloper && len(provider.RequestsForRole(domain.RoleDeveloper)) == 2 {
			loaded, err := store.Load(testharness.PipelineRunID)
			if err != nil {
				t.Errorf("Load() at the relaunch error = %v", err)
			}
			recorded = loaded
		}
		return served(request)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if recorded.TransientRelaunches != 1 {
		t.Fatalf("relaunches recorded before the relaunch = %d, want 1", recorded.TransientRelaunches)
	}
	// Nothing here is a wait: a connection that dropped is already gone, and the
	// provider's own retry ladder was spent before the harness saw the terminal.
	if clock.Waited() != 0 {
		t.Fatalf("waited %s before relaunching, want a relaunch to take no pause", clock.Waited())
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the relaunched run did not complete normally: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want the dead attempt and its relaunch", len(developerRequests))
	}
	// The attempt that died mid-response had already made part of the change, so
	// the relaunch continues its session rather than deriving the work again.
	if developerRequests[1].SessionID != provider.DeveloperSession {
		t.Fatalf("relaunched attempt session = %q, want %q", developerRequests[1].SessionID, provider.DeveloperSession)
	}
	// Nothing was wrong with the change, so nothing is charged to the developer.
	if outcome.RepairAttempts != 0 || outcome.TransientRelaunches != 1 {
		t.Fatalf("outcome = %#v, want one relaunch and no repair attempt", outcome)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.TransientRelaunches != 1 {
		t.Fatalf("finished state relaunches = %d, want the one it spent", finished.TransientRelaunches)
	}
}

// A review the provider killed was never made either, and it costs more to lose
// than a developer attempt: the change is already built and checked. It is asked
// for again on the same budget, without redeveloping anything.
func TestRunRelaunchesATransientlyKilledReview(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	reviews := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleDeveloper {
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.DeveloperSession,
				ResolvedModel: testharness.DeveloperResolved, FinalText: "implemented the work item",
				Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
			}, nil
		}
		reviews++
		if reviews == 1 {
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.ReviewerSession,
				IsError: true, StopReason: "api_error", FinalText: testharness.ConnectionClosedMessage,
				TransientFailure: &backend.TransientFailure{Detail: "api_error: " + testharness.ConnectionClosedMessage},
				Process:          execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
				LastEvent:        request.LastSequence,
			}, nil
		}
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: provider.ReviewerSession,
			ResolvedModel: testharness.ReviewerResolved, FinalText: testharness.ApproveVerdict,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
		}, nil
	}
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the run did not complete after the review was asked again: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	if developerRuns := len(provider.RequestsForRole(domain.RoleDeveloper)); developerRuns != 1 {
		t.Fatalf("developer invocations = %d, want the review relaunched without redeveloping", developerRuns)
	}
	if reviews != 2 {
		t.Fatalf("reviews = %d, want the dead review asked for again", reviews)
	}
	// One budget covers both roles, so a review that died is counted where a
	// developer death would be.
	if outcome.TransientRelaunches != 1 {
		t.Fatalf("relaunches = %d, want the review death counted against the shared budget", outcome.TransientRelaunches)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.TransientRelaunches != 1 {
		t.Fatalf("finished state relaunches = %d, want the one it spent", finished.TransientRelaunches)
	}
}

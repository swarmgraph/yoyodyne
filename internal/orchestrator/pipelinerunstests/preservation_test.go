package pipelinerunstests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestPipelinePreservesFailedWorkAndRecordsFailure(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Fail", Status: "open"}}
	provider := &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "partial.txt"), []byte("partial"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{
			SessionID:  "session-failed",
			FinalText:  "provider failed",
			IsError:    true,
			StopReason: "provider_error",
			Process:    execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
		}, nil
	}}
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopProvider)
	if err == nil || !strings.Contains(err.Error(), "developer reported failure") {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusFailed || outcome.WorktreePath == "" {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if !strings.Contains(tracker.Notes, "bootstrap run failed") || !strings.Contains(tracker.Notes, outcome.RunID) {
		t.Fatalf("failure notes = %q", tracker.Notes)
	}
	if !strings.Contains(tracker.Notes, "A partial.txt") {
		t.Fatalf("failure notes did not include preserved changes: %q", tracker.Notes)
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "partial.txt")); err != nil {
		t.Fatalf("failed worktree was not preserved: %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusFailed || state.CompletedAt == nil {
		t.Fatalf("state = %#v", state)
	}
}

// The order the refusals a newcomer meets come in. The adoption walk executes
// the README's claim that a run refuses an uncommitted primary checkout and
// names every file that is dirty, and it executes it where a newcomer stands:
// on a machine that has not installed Claude Code yet. A dispatch that asked the
// provider first answered that claim with "Claude Code is not installed" —
// true, unrelated, and not the thing the reader has to fix. The checkout is the
// same refusal on every machine, so it is reported first; the provider is still
// refused once there is nothing else to say.
func TestPipelineNamesADirtyCheckoutBeforeAskingWhetherTheProviderIsInstalled(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	dirty := []string{"notes.md", filepath.Join("docs", "product.md")}
	for _, name := range dirty {
		if err := os.WriteFile(filepath.Join(repository, name), []byte("uncommitted\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// A machine with no Claude Code on it. AuthMethod is answered so this reads as
	// a deliberate absence rather than as the zero value the fake fills in.
	provider := &orchestratortest.Backend{ReportedAvailability: backend.Availability{AuthMethod: "none"}}
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	_, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() started against an uncommitted primary checkout")
	}
	if !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() error = %v, want the primary checkout refused", err)
	}
	for _, name := range dirty {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("Run() error = %v, want it to name %s", err, name)
		}
	}
	if strings.Contains(err.Error(), "Claude Code is not installed") {
		t.Fatalf("Run() error = %v, want the checkout named rather than the provider", err)
	}
	if tracker.Claimed {
		t.Fatal("a run refused for its checkout claimed work")
	}

	// And with the checkout committed, the provider is exactly as missing as it
	// was: reporting the checkout first defers that refusal rather than dropping
	// it.
	testharness.RunPipelineGit(t, repository, "add", ".")
	testharness.RunPipelineGit(t, repository, "commit", "-m", "adopt yoyo")
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil ||
		!strings.Contains(err.Error(), "the claude-code backend is not installed") {
		t.Fatalf("Run() error = %v, want the missing provider refused once the checkout is clean", err)
	}
	if tracker.Claimed {
		t.Fatal("a run refused for its provider claimed work")
	}
}

func TestPipelineRefusesAutomaticIntegrationThatIsNotGatedByAReviewer(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		degrade func(*orchestrator.Pipeline)
		want    string
	}{
		{
			name:    "no reviewer wired",
			degrade: func(pipeline *orchestrator.Pipeline) { pipeline.Reviewer = nil },
			want:    "requires an independent reviewer",
		},
		{
			name:    "no reviewer agent configured",
			degrade: func(pipeline *orchestrator.Pipeline) { delete(pipeline.Config.Agents, "reviewer") },
			want:    "automatic integration requires at least one reviewer agent",
		},
		{
			// A declared provider can still omit the posture the reviewer needs.
			name: "reviewer agent on a backend that cannot hold the reviewer's posture",
			degrade: func(pipeline *orchestrator.Pipeline) {
				terminal, failed := true, true
				pipeline.Config.Providers = map[string]backend.ProviderPlugin{"writes-only": {
					Adapter:  domain.BackendCodex,
					Roles:    []domain.AgentRole{domain.RoleReviewer},
					Postures: []backend.Posture{backend.PostureWorktreeWrite},
					Dialect:  backend.DialectSpec{Rules: []backend.DialectRule{{Answer: backend.AnswerRefused, Terminal: &terminal, Failed: &failed}}},
				}}
				pipeline.Config.Agents["reviewer"] = config.AgentConfig{Role: domain.RoleReviewer, Backend: "writes-only", Model: testharness.TestReviewerModel, Instances: 1}
			},
			want: `backend "writes-only" cannot hold the "read-only" tool access`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, testharness.ApproveVerdict)
			pipeline, _ := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			test.degrade(&pipeline)

			if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
			if tracker.Claimed || len(provider.Requests) != 0 {
				t.Fatalf("ungated automatic integration started work: claimed = %t, requests = %d", tracker.Claimed, len(provider.Requests))
			}
		})
	}
}

// A check stopped at its budget is not a check that judged the change: the work
// may have been passing the whole time, as it was when a contended suite grew
// past a flat ten minutes. So the run ends rather than spending a repair attempt
// on a developer that would be stopped the same way, and what it reports is the
// two numbers a reader needs — what the check spent, and what it was allowed.
func TestPipelineReportsElapsedAndBudgetWhenACheckTimesOut(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"sleep 30"})
	runner, ok := pipeline.Checks.(checks.Runner)
	if !ok {
		t.Fatal("the test pipeline's check runner is not a checks.Runner")
	}
	runner.Timeout = 100 * time.Millisecond
	pipeline.Checks = runner
	pipeline.Config.Execution.CheckTimeout = config.Duration(100 * time.Millisecond)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	// Stopped at its own limit, the check is unfinished: a stop from outside the
	// work, which the harness runs again within its bounds.
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.CauseCheckTimeLimit.StopClass())
	if outcome.StopClass != runstate.CauseCheckTimeLimit.StopClass() {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.CauseCheckTimeLimit.StopClass())
	}
	if err == nil {
		t.Fatal("Run() error = nil, want the run stopped at the check budget")
	}
	for _, want := range []string{"verification timed out", "sleep 30", "ran for", "100ms execution.check_timeout", "max_concurrent_developers"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Run() error = %v, want it to name %q", err, want)
		}
	}
	// The developer is never asked to repair a check that never judged its
	// change, so the run stops on the first attempt rather than spending the
	// repair budget.
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 {
		t.Fatalf("developer invocations = %d, want only the first attempt", runs)
	}
	if len(outcome.Checks) != 1 || outcome.Checks[0].Timeout != 100*time.Millisecond {
		t.Fatalf("outcome checks = %#v, want the budget recorded", outcome.Checks)
	}
	if elapsed := outcome.Checks[0].Elapsed(); elapsed <= 0 {
		t.Fatalf("check elapsed = %s, want what the check actually spent", elapsed)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// A check the harness stopped on time is recorded as such rather than as a
	// change that failed its checks, and nothing is left waiting to be repaired.
	if state.Status != runstate.StatusTimedOut || state.Phase != runstate.PhaseChecking || state.CheckFailure != nil {
		t.Fatalf("state = %#v", state)
	}
}

func TestPipelineMakesCompletionDurableBeforeRemovingArtifacts(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	// Cleanup destroys the evidence of the run, so everything a crash would
	// otherwise strand must already be durable when it starts.
	var atCleanup runstate.State
	var closedAtCleanup bool
	pipeline.Worktrees = &testharness.HookedWorktrees{
		WorktreeManager: pipeline.Worktrees,
		BeforeCleanup: func() error {
			closedAtCleanup = tracker.Closed
			loaded, err := store.Load(testharness.PipelineRunID)
			if err != nil {
				return err
			}
			atCleanup = loaded
			return nil
		},
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !closedAtCleanup {
		t.Fatal("cleanup ran before the work item was closed")
	}
	if atCleanup.Status != runstate.StatusSucceeded || atCleanup.CompletedAt == nil {
		t.Fatalf("run was not durably terminal before cleanup: %#v", atCleanup)
	}
	if atCleanup.Integration == nil || atCleanup.Phase != runstate.PhaseCleaningUp || atCleanup.WorktreeRemoved {
		t.Fatalf("pre-cleanup state is not a resumable cleanup instruction: %#v", atCleanup)
	}

	final, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.Phase != runstate.PhaseComplete || !final.WorktreeRemoved || !final.BranchRemoved || final.CleanupFailure != "" {
		t.Fatalf("final state = %#v", final)
	}
	if !outcome.WorktreeRemoved || !outcome.BranchRemoved || outcome.CleanupFailure != "" {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestPipelineReportsPartialCleanupPerArtifact(t *testing.T) {
	t.Parallel()

	repository, tracker, _, pipeline, store := testharness.AutomaticFixture(t)
	// Cleanup removes the worktree and then fails deleting the branch, which is
	// exactly the state an interrupted two-step removal leaves behind.
	pipeline.Worktrees = &testharness.HookedWorktrees{
		WorktreeManager: pipeline.Worktrees,
		Cleanup: func(gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
			return gitworktree.Cleanup{WorktreeRemoved: true}, errors.New("branch is checked out elsewhere")
		},
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !outcome.WorktreeRemoved || outcome.BranchRemoved {
		t.Fatalf("outcome = %#v", outcome)
	}
	if !strings.Contains(outcome.CleanupFailure, "branch is checked out elsewhere") {
		t.Fatalf("cleanup failure = %q", outcome.CleanupFailure)
	}
	// A surviving artifact is an outstanding cleanup, never a mere recording
	// problem, and the phase must not claim completion.
	if outcome.CompletionRecordingFailure != "" || outcome.Phase != runstate.PhaseCleaningUp {
		t.Fatalf("partial cleanup was misclassified: %#v", outcome)
	}
	// The tracker must send an operator after the branch only, never after a
	// worktree that is already gone.
	if !strings.Contains(tracker.Notes, "Remaining branch: "+outcome.Branch) {
		t.Fatalf("notes omitted the surviving branch: %q", tracker.Notes)
	}
	if strings.Contains(tracker.Notes, "Remaining worktree:") {
		t.Fatalf("notes claim a removed worktree remains: %q", tracker.Notes)
	}
	if !strings.Contains(tracker.Notes, "Worktree removed: true") || !strings.Contains(tracker.Notes, "Branch removed: false") {
		t.Fatalf("notes do not report each artifact: %q", tracker.Notes)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !state.WorktreeRemoved || state.BranchRemoved || state.Phase != runstate.PhaseCleaningUp {
		t.Fatalf("state = %#v", state)
	}
	// The integration itself is untouched by a cleanup problem.
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != outcome.Integration.TargetCommit {
		t.Fatalf("main = %q, want %q", head, outcome.Integration.TargetCommit)
	}
}

func TestPipelineNeverNamesAnArtifactThatCleanupAlreadyRemoved(t *testing.T) {
	t.Parallel()

	_, tracker, _, pipeline, store := testharness.AutomaticFixture(t)
	// Both removals succeeded and only their confirmation failed, which is what
	// a broken `worktree list` or `show-ref` leaves behind.
	worktrees := pipeline.Worktrees
	pipeline.Worktrees = &testharness.HookedWorktrees{
		WorktreeManager: worktrees,
		Cleanup: func(request gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
			cleanup, err := worktrees.CleanupIntegrated(context.Background(), request)
			if err != nil {
				return cleanup, err
			}
			return cleanup, errors.New("verify removal of worktree: git process runner is unavailable")
		},
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.WorktreeRemoved || !outcome.BranchRemoved {
		t.Fatalf("completed removals were not preserved: %#v", outcome)
	}
	if _, err := os.Stat(outcome.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
	// Nothing survives, so nothing may be named or described as unfinished.
	for _, reject := range []string{"Remaining worktree:", "Remaining branch:", "cleanup did not finish"} {
		if strings.Contains(tracker.Notes, reject) {
			t.Fatalf("notes claim %q for an already-removed artifact: %q", reject, tracker.Notes)
		}
	}
	if !strings.Contains(tracker.Notes, "confirming their removal failed") ||
		!strings.Contains(tracker.Notes, "Worktree removed: true") ||
		!strings.Contains(tracker.Notes, "Branch removed: true") {
		t.Fatalf("notes do not report the completed removals: %q", tracker.Notes)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !state.WorktreeRemoved || !state.BranchRemoved || state.CleanupFailure == "" {
		t.Fatalf("state = %#v", state)
	}
}

func TestPipelineResumesCleanupThatWasInterruptedBetweenItsSteps(t *testing.T) {
	t.Parallel()

	repository, tracker, _, pipeline, store := testharness.AutomaticFixture(t)
	worktrees := pipeline.Worktrees
	// Interrupt the run's cleanup after the worktree is gone but before the
	// branch is deleted, exactly as a crash between the two steps would.
	pipeline.Worktrees = &testharness.HookedWorktrees{
		WorktreeManager: worktrees,
		Cleanup: func(request gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
			testharness.RunPipelineGit(t, repository, "worktree", "remove", request.Worktree.Path)
			return gitworktree.Cleanup{WorktreeRemoved: true}, errors.New("interrupted before deleting the branch")
		},
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.WorktreeRemoved || outcome.BranchRemoved {
		t.Fatalf("interrupted outcome = %#v", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// A retry driven only by the durable integration evidence must finish the
	// job rather than fail because the worktree is already unregistered.
	cleanup, err := worktrees.CleanupIntegrated(context.Background(), gitworktree.CleanupRequest{
		Worktree: gitworktree.Worktree{
			RunID:        state.RunID,
			WorkItemID:   state.WorkItemID,
			Path:         state.WorktreePath,
			Branch:       state.Branch,
			BaseCommit:   state.BaseCommit,
			TargetBranch: state.Integration.TargetBranch,
		},
		TargetBranch: state.Integration.TargetBranch,
		SourceCommit: state.Integration.SourceCommit,
	})
	if err != nil {
		t.Fatalf("resumed CleanupIntegrated() error = %v", err)
	}
	if !cleanup.Complete() {
		t.Fatalf("resumed cleanup = %#v", cleanup)
	}
	if branches := testharness.GitOutput(t, repository, "branch", "--list", outcome.Branch); strings.TrimSpace(branches) != "" {
		t.Fatalf("resumed cleanup left the branch: %q", branches)
	}
}

func TestPipelineRecordsRequestedAndResolvedModelsForBothInvocations(t *testing.T) {
	t.Parallel()

	_, tracker, provider, pipeline, store := testharness.AutomaticFixture(t)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	reviewerRequests := provider.RequestsForRole(domain.RoleReviewer)
	if len(developerRequests) != 1 || developerRequests[0].Model != testharness.TestDeveloperModel {
		t.Fatalf("developer invocation model = %#v", developerRequests)
	}
	if len(reviewerRequests) != 1 || reviewerRequests[0].Model != testharness.TestReviewerModel {
		t.Fatalf("reviewer invocation model = %#v", reviewerRequests)
	}
	if outcome.ProviderModel != testharness.TestDeveloperModel || outcome.ProviderResolvedModel != testharness.DeveloperResolved {
		t.Fatalf("outcome developer model evidence = %#v", outcome)
	}
	if outcome.ReviewModel != testharness.TestReviewerModel || outcome.ReviewResolvedModel != testharness.ReviewerResolved {
		t.Fatalf("outcome reviewer model evidence = %#v", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ProviderModel != testharness.TestDeveloperModel || state.ProviderResolvedModel != testharness.DeveloperResolved {
		t.Fatalf("durable developer model evidence = %#v", state)
	}
	if state.ReviewModel != testharness.TestReviewerModel || state.ReviewResolvedModel != testharness.ReviewerResolved {
		t.Fatalf("durable reviewer model evidence = %#v", state)
	}
	for _, want := range []string{
		"Developer model: " + testharness.TestDeveloperModel + " (resolved: " + testharness.DeveloperResolved + ")",
		"Reviewer model: " + testharness.TestReviewerModel + " (resolved: " + testharness.ReviewerResolved + ")",
	} {
		if !strings.Contains(tracker.Notes, want) {
			t.Fatalf("notes are missing %q: %q", want, tracker.Notes)
		}
	}
}

func TestPipelineRefusesRunsWhoseModelPolicyIsNotEnforced(t *testing.T) {
	t.Parallel()

	t.Run("developer declares no selector", func(t *testing.T) {
		t.Parallel()
		_, tracker, provider, pipeline, _ := testharness.AutomaticFixture(t)
		developer := pipeline.Config.Agents["developer"]
		developer.Model = ""
		pipeline.Config.Agents["developer"] = developer

		if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !strings.Contains(err.Error(), "model selector is required") {
			t.Fatalf("Run() error = %v", err)
		}
		if tracker.Claimed || len(provider.Requests) != 0 {
			t.Fatalf("a run with no declared model started work: claimed = %t", tracker.Claimed)
		}
	})

	t.Run("reviewer ran with an unconfigured selector", func(t *testing.T) {
		t.Parallel()
		repository, tracker, provider, pipeline, store := testharness.AutomaticFixture(t)
		// The reviewer reports what it actually ran with, so a reviewer wired
		// differently from configuration cannot silently authorize integration.
		pipeline.Reviewer = review.Reviewer{Backend: provider, Model: "some-other-model"}
		before := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err == nil || !strings.Contains(err.Error(), "configured reviewer model") {
			t.Fatalf("Run() error = %v", err)
		}
		if outcome.Integration != nil || tracker.Closed {
			t.Fatalf("an unaudited review integrated: %#v, closed = %t", outcome.Integration, tracker.Closed)
		}
		if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
			t.Fatalf("main moved: %q, want %q", head, before)
		}
		state, err := store.Load(outcome.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state.Integration != nil || state.ReviewModel != "some-other-model" {
			t.Fatalf("state = %#v", state)
		}
	})
}

func TestPipelineRefusesToIntegrateWithoutIndependentSessions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		developer string
		reviewer  string
		want      string
	}{
		{name: "developer session missing", developer: "", reviewer: "reviewer-session", want: "requires recorded developer and reviewer sessions"},
		{name: "reviewer session missing", developer: "developer-session", reviewer: "", want: "requires recorded developer and reviewer sessions"},
		{name: "sessions reused", developer: "shared-session", reviewer: "shared-session", want: "requires an independent reviewer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository, tracker, provider, pipeline, store := testharness.AutomaticFixture(t)
			provider.DeveloperSession = test.developer
			provider.ReviewerSession = test.reviewer
			before := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopReview)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
			if outcome.Integration != nil || tracker.Closed {
				t.Fatalf("work without proven independence was integrated: %#v, closed = %t", outcome.Integration, tracker.Closed)
			}
			if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
				t.Fatalf("main moved: %q, want %q", head, before)
			}
			if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
				t.Fatalf("worktree was not preserved: %v", err)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if state.Integration != nil || state.WorktreeRemoved {
				t.Fatalf("state = %#v", state)
			}
		})
	}
}

// dyingRepairBackend serves the first developer attempt and then dies the way a
// dropped connection does on every attempt after it. What it produces is a run
// that reaches its repair loop and is killed inside it, which is the interrupted
// run a later process actually picks up.

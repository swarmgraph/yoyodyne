package pipelinerunstests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An overload that never lifts must not become a run that reissues forever. Each
// wait spends the same aggregate budget, so the run walks into the configured
// maximum and stops with a blocker naming what refused it.
func TestRunWalksRepeatedServerOverloadsIntoThePauseBudget(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// More refusals than the budget can pay for, so what stops the run is the
	// budget rather than the provider relenting.
	provider := testharness.ServerOverloadBackend(10, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 4*time.Minute, 4*time.Minute)
	pipeline.Config.Execution.ServerOverloadPause = config.Duration(90 * time.Second)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopUsagePause)
	if outcome.StopClass != runstate.StopUsagePause {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.StopUsagePause)
	}
	if err == nil {
		t.Fatalf("Run() error = nil, want the run to stop once its budget was spent")
	}
	// Two waits fit in four minutes and a third does not, so the run takes the two
	// it can afford and refuses the one it cannot.
	if clock.Waited() != 3*time.Minute {
		t.Fatalf("waited %s in total, want the two 90s waits the 4m budget covers", clock.Waited())
	}
	if attempts := len(provider.RequestsForRole(domain.RoleDeveloper)); attempts != 3 {
		t.Fatalf("developer invocations = %d, want the refused attempt and the two reissues it paid for", attempts)
	}
	if !tracker.Blocked || !outcome.Blocked {
		t.Fatalf("the spent budget left no blocker: tracker=%t outcome=%t", tracker.Blocked, outcome.Blocked)
	}
	if !strings.Contains(tracker.BlockReason, "past the 4m0s maximum pause") {
		t.Fatalf("blocker did not name the budget that stopped the run:\n%s", tracker.BlockReason)
	}
	// The operator has to be able to tell an overloaded provider from an exhausted
	// account: one is weather, the other may be a decision about capacity.
	if !strings.Contains(tracker.BlockReason, "transient provider server overload") {
		t.Fatalf("blocker did not name what refused the run:\n%s", tracker.BlockReason)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.Status.Terminal() || stopped.UsageLimitResetsAt != nil {
		t.Fatalf("stopped state = %#v, want a terminal run carrying no deadline", stopped)
	}
	// The change is preserved for whoever picks the item up.
	if _, statErr := os.Stat(stopped.WorktreePath); statErr != nil {
		t.Fatalf("the stopped run's worktree did not survive: %v", statErr)
	}
}

// A wait longer than this process will hold open exits with the run still in
// flight, and a later invocation picks it up and finishes it. Nothing is cleaned
// up in between: the item stays claimed and the artifacts stay put.
func TestRunExitsResumableForALongPauseAndIsContinuedByALaterInvocation(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := testharness.BaseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	first := testharness.UsageLimitBackend(1, limit, testharness.ApproveVerdict)
	firstClock := &testharness.PausingClock{Instant: testharness.BaseTime}
	firstPipeline := testharness.Waiting(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		firstClock, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("paused Run() error = %v", err)
	}
	if !paused.Paused || paused.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a paused run still in flight", paused)
	}
	if paused.UsageLimitResetsAt == nil || !paused.UsageLimitResetsAt.Equal(resetsAt) || paused.UsageLimitKind != "five_hour" {
		t.Fatalf("paused outcome did not report the deadline it is waiting on: %#v", paused)
	}
	if len(firstClock.Slept) != 0 {
		t.Fatalf("waits = %v, want a run that exited rather than holding the process open", firstClock.Slept)
	}
	// A pause is not a failure and not a stop: nothing is blocked, nothing is
	// closed, and the claim is kept.
	if tracker.Blocked || tracker.Closed || !tracker.Claimed {
		t.Fatalf("the pause disturbed the work item: blocked=%t closed=%t claimed=%t", tracker.Blocked, tracker.Closed, tracker.Claimed)
	}
	pausedState, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if pausedState.Status.Terminal() || pausedState.UsageLimitResetsAt == nil {
		t.Fatalf("paused state = %#v, want a non-terminal run carrying its deadline", pausedState)
	}
	if _, err := os.Stat(pausedState.WorktreePath); err != nil {
		t.Fatalf("the paused run's worktree did not survive: %v", err)
	}

	// A restart still inside the wait honors the remainder rather than asking the
	// provider again and being refused by the same limit.
	duringWait := testharness.UsageLimitBackend(0, limit, testharness.ApproveVerdict)
	duringClock := &testharness.PausingClock{Instant: testharness.BaseTime.Add(30 * time.Minute)}
	duringPipeline := testharness.Waiting(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, duringWait, []string{"exit 0"}), duringWait),
		duringClock, 6*time.Hour, time.Minute)
	stillPaused, err := duringPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("restarted Run() during the wait error = %v", err)
	}
	if !stillPaused.Paused || stillPaused.RunID != paused.RunID {
		t.Fatalf("a restart during the wait did not re-enter the same paused run: %#v", stillPaused)
	}
	if len(duringWait.Requests) != 0 {
		t.Fatalf("a restart during the wait asked the provider anyway: %#v", duringWait.Requests)
	}

	// Once the deadline has passed the same run is picked up and finished.
	second := testharness.UsageLimitBackend(0, limit, testharness.ApproveVerdict)
	secondClock := &testharness.PausingClock{Instant: resetsAt.Add(time.Minute)}
	secondPipeline := testharness.Waiting(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second),
		secondClock, 6*time.Hour, time.Minute)
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.WorktreePath != paused.WorktreePath || outcome.Branch != paused.Branch {
		t.Fatalf("resumed run = %#v, want the paused run %s in %s", outcome, paused.RunID, paused.WorktreePath)
	}
	if len(secondClock.Slept) != 0 {
		t.Fatalf("waits = %v, want no wait once the deadline has passed", secondClock.Slept)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the resumed run did not complete normally: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	if claims := testharness.CountCalls(tracker.Calls, "claim"); claims != 1 {
		t.Fatalf("claims = %d, want the item claimed once across the pause", claims)
	}
	// The run paused before any failure was ever returned to the developer, so
	// what it is owed on resumption is its original attempt, not a repair.
	developerRequests := second.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 {
		t.Fatalf("resumed developer invocations = %d, want one", len(developerRequests))
	}
	if strings.Contains(developerRequests[0].Prompt, "repair required") {
		t.Fatalf("the resumed attempt was issued as a repair:\n%s", developerRequests[0].Prompt)
	}
	if !strings.Contains(developerRequests[0].Prompt, "You are the developer for one bounded Yoyodyne work item") {
		t.Fatalf("the resumed attempt lost the harness contract:\n%s", developerRequests[0].Prompt)
	}
}

// A reset time the harness cannot believe is no reset time at all. Guessing the
// wait is the one thing that must not happen, so the run stops and hands what it
// knows to a person.
func TestRunStopsWithABlockerWhenAUsageLimitResetIsUnusable(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		limit      backend.UsageLimit
		maxPause   time.Duration
		wantReason string
	}{
		{
			// A reset past the maximum pause is a wait with an end the harness will
			// not take, which is deliberately absent here: it is no person's to
			// decide, so it stops the run without a blocker.
			// TestAUsageWindowResettingPastTheMaximumPauseEndsTheRunUnjudged covers it.
			//
			// A limit still refusing while naming a reset that has already passed
			// is not describing a wait. Honoring it would reissue immediately into
			// the same refusal, with nothing bounding the attempts.
			name:       "reset that is not in the future",
			limit:      backend.UsageLimit{Kind: "five_hour", ResetsAt: testharness.BaseTime.Add(-time.Minute)},
			wantReason: "which is not in the future",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			limit := testCase.limit
			provider := testharness.UsageLimitBackend(1, &limit, testharness.ApproveVerdict)
			pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
			clock := &testharness.PausingClock{Instant: testharness.BaseTime}
			maxPause := testCase.maxPause
			if maxPause == 0 {
				maxPause = 6 * time.Hour
			}
			pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, maxPause, maxPause)

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err == nil {
				t.Fatalf("Run() error = nil, want the run to stop")
			}
			if outcome.Paused {
				t.Fatalf("a refused wait was reported as a pause: %#v", outcome)
			}
			if len(clock.Slept) != 0 {
				t.Fatalf("waits = %v, want a run that refused to wait at all", clock.Slept)
			}
			if !tracker.Blocked || !outcome.Blocked {
				t.Fatalf("the exhausted limit left no blocker: tracker=%t outcome=%t", tracker.Blocked, outcome.Blocked)
			}
			if !strings.Contains(tracker.BlockReason, testCase.wantReason) {
				t.Fatalf("blocker did not name why the wait was refused:\n%s", tracker.BlockReason)
			}
			// A blocked run is terminal, and a terminal run must not still be
			// promising somebody that it will resume.
			stopped, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !stopped.Status.Terminal() || stopped.UsageLimitResetsAt != nil {
				t.Fatalf("stopped state = %#v, want a terminal run carrying no deadline", stopped)
			}
			if stopped.UsageLimitKind != testCase.limit.Kind {
				t.Fatalf("the record does not name the limit that stopped the run: %q", stopped.UsageLimitKind)
			}
			// The change is preserved for whoever picks the item up.
			if _, statErr := os.Stat(stopped.WorktreePath); statErr != nil {
				t.Fatalf("the stopped run's worktree did not survive: %v", statErr)
			}
		})
	}
}

// A usage window resetting later than the harness will wait is a wait it will not
// take, not a verdict on anything. This replays 2026-09-23: a developer run
// refused on an exhausted seven_day limit whose reset is four days out, against
// the six-hour maximum pause. The run ends cancelled as a named environmental
// stop carrying the reset, gives its claim back, keeps its branch and worktree,
// and leaves every budget the item is counted against where it was. The brake
// and the watch session's side of the same stop are in
// TestAUsageWindowStopCountsTowardNothingInAWatchingSession.
func TestAUsageWindowResettingPastTheMaximumPauseEndsTheRunUnjudged(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name         string
		limit        backend.UsageLimit
		maxPause     time.Duration
		wantReason   string
		wantReset    time.Time
		resetUnknown bool
	}{
		{
			name:       "seven_day reset four days out",
			limit:      backend.UsageLimit{Kind: "seven_day", ResetsAt: testharness.BaseTime.Add(92*time.Hour + 10*time.Minute)},
			maxPause:   6 * time.Hour,
			wantReason: "would take this run past the 6h0m0s maximum pause",
			wantReset:  testharness.BaseTime.Add(92*time.Hour + 10*time.Minute),
		},
		{
			// A limit naming no reset is asked again after the configured probe
			// interval, and a probe the budget cannot cover is the same wait past
			// the bound, with the probe standing in for the reset.
			name:         "no reset time, beyond the configured maximum pause",
			limit:        backend.UsageLimit{Kind: "seven_day"},
			maxPause:     time.Minute,
			wantReason:   "named no reset time, and waiting",
			resetUnknown: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			limit := testCase.limit
			provider := testharness.UsageLimitBackend(1, &limit, testharness.ApproveVerdict)
			pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
			clock := &testharness.PausingClock{Instant: testharness.BaseTime}
			pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, testCase.maxPause, testCase.maxPause)
			before, err := store.Triage().Counters(tracker.Item.ID)
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.CauseUsageWindow.StopClass())
			if err != nil {
				t.Fatalf("Run() error = %v, want the run ended on the window rather than failed", err)
			}
			if outcome.Paused || len(clock.Slept) != 0 {
				t.Fatalf("paused=%t waits=%v, want a run that did not wait at all", outcome.Paused, clock.Slept)
			}
			// The stop class: cancelled rather than failed, no blocker, and the
			// environmental refusal naming the window and its reset.
			if outcome.Status != runstate.StatusCancelled || outcome.Blocked || tracker.Blocked {
				t.Fatalf("outcome = %s blocked=%t tracker blocked=%t; want cancelled with nothing blocked", outcome.Status, outcome.Blocked, tracker.Blocked)
			}
			stopped, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if stopped.Status != runstate.StatusCancelled || strings.TrimSpace(stopped.Blocker) != "" {
				t.Fatalf("stopped = %s with blocker %q, want a cancelled run nobody has to decide about", stopped.Status, stopped.Blocker)
			}
			if got := stopped.Outcome(); got != runstate.OutcomeCancelled {
				t.Fatalf("outcome = %q, want %q", got, runstate.OutcomeCancelled)
			}
			if stopped.DiedInItsOwnProcess() {
				t.Fatal("the stop reads as a run that died, which dockets it for the development manager")
			}
			refused := stopped.Environmental
			if refused == nil || refused.Cause != runstate.CauseUsageWindow || !refused.Settled || !refused.Refused || refused.Problem != "" {
				t.Fatalf("environmental = %#v, want a settled usage-window refusal", refused)
			}
			if refused.ResetsAt == nil || refused.ResetUnknown != testCase.resetUnknown {
				t.Fatalf("reset = %v unknown=%t, want the reset recorded", refused.ResetsAt, refused.ResetUnknown)
			}
			if !testCase.wantReset.IsZero() && !refused.ResetsAt.Equal(testCase.wantReset) {
				t.Fatalf("reset = %s, want the provider's %s", refused.ResetsAt, testCase.wantReset)
			}
			if !strings.Contains(stopped.Failure, testCase.wantReason) || !strings.Contains(stopped.Failure, "seven_day") {
				t.Fatalf("failure = %q, want the window and why it was not waited", stopped.Failure)
			}
			if stopped.UsageLimitKind != "seven_day" {
				t.Fatalf("the record does not name the limit that stopped the run: %q", stopped.UsageLimitKind)
			}
			// The claim is given back, saying when the item is pulled again.
			if !tracker.Released || tracker.Item.Status != "open" {
				t.Fatalf("released=%t status=%q, want the claim given back", tracker.Released, tracker.Item.Status)
			}
			if said := refused.ResetSays(); !strings.Contains(strings.ToLower(tracker.ReleaseReason), strings.ToLower(said)) {
				t.Fatalf("release note = %q, want it to say %q", tracker.ReleaseReason, said)
			}
			if !strings.Contains(tracker.Notes, "usage limit") {
				t.Fatalf("the item's notes do not name the usage limit:\n%s", tracker.Notes)
			}
			// The branch and worktree are kept as a stopped run's are.
			if _, statErr := os.Stat(stopped.WorktreePath); statErr != nil {
				t.Fatalf("the stopped run's worktree did not survive: %v", statErr)
			}
			if !stopped.Artifacts().Preserved() {
				t.Fatalf("artifacts = %#v, want the branch and worktree preserved", stopped.Artifacts())
			}
			// And nothing the item is counted against moved.
			after, err := store.Triage().Counters(tracker.Item.ID)
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if after.ReviewRounds != before.ReviewRounds || after.CommittedRounds != before.CommittedRounds ||
				after.RepairGrants != before.RepairGrants || after.Reruns != before.Reruns {
				t.Fatalf("counters after the stop = %#v, want them as they were: %#v", after, before)
			}
		})
	}
}

// The provider re-reports its limits whenever they change, so most reports
// arrive on work that is being served. A limit reported alongside an attempt
// that still finished is evidence, never a reason to stop and wait.
func TestRunDoesNotPauseWhenALimitIsReportedButTheAttemptStillFinished(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.ReviewerSession,
				ResolvedModel: testharness.ReviewerResolved, FinalText: testharness.ApproveVerdict,
				Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
			}, nil
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: provider.DeveloperSession,
			ResolvedModel: testharness.DeveloperResolved, FinalText: "implemented the work item",
			UsageLimit: &backend.UsageLimit{Kind: "five_hour", ResetsAt: testharness.BaseTime.Add(time.Hour)},
			Process:    execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
		}, nil
	}
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 6*time.Hour, 6*time.Hour)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(clock.Slept) != 0 || outcome.Paused {
		t.Fatalf("a served attempt was paused anyway: waits=%v paused=%t", clock.Slept, outcome.Paused)
	}
	if outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the run did not complete normally: %#v", outcome)
	}
}

// steppingUsageLimitBackend refuses the developer once per entry in resets,
// naming each reset in turn, and serves the work afterwards. Every refusal names
// a reset that is individually inside the maximum pause, which is what a bound
// applied per wait rather than per run would wave through.
func steppingUsageLimitBackend(resets []time.Time, verdict string) *orchestratortest.Backend {
	provider := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	refused := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.ReviewerSession,
				ResolvedModel: testharness.ReviewerResolved, FinalText: verdict,
				Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
			}, nil
		}
		if refused < len(resets) {
			reset := resets[refused]
			refused++
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.DeveloperSession,
				IsError: true, StopReason: "usage_limit",
				UsageLimit: &backend.UsageLimit{Kind: "five_hour", ResetsAt: reset},
				Process:    execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
				LastEvent:  request.LastSequence,
			}, nil
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: provider.DeveloperSession,
			ResolvedModel: testharness.DeveloperResolved, FinalText: "implemented the work item",
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
		}, nil
	}
	return provider
}

// The maximum pause bounds the run, not one wait. A provider that keeps refusing
// with a fresh, individually acceptable reset must not be able to walk a run past
// the bound an operator configured, one wait at a time.
func TestRunBoundsItsTotalUsageLimitWaitAcrossConsecutivePauses(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	// Each reset is roughly two hours after the refusal that names it, so no
	// single wait comes close to the three-hour maximum but the run walks into it
	// if the bound is applied per wait rather than per run.
	resets := []time.Time{
		testharness.BaseTime.Add(2 * time.Hour),
		testharness.BaseTime.Add(4 * time.Hour),
		testharness.BaseTime.Add(6 * time.Hour),
	}
	provider := steppingUsageLimitBackend(resets, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 3*time.Hour, 3*time.Hour)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run ended on the window rather than failed", err)
	}
	// One probe of thirty minutes is taken; the second refusal names a reset three
	// and a half hours out, which no longer fits in what the run has left of its
	// three-hour budget, so it is refused rather than waited.
	if clock.Waited() != 30*time.Minute {
		t.Fatalf("waited %s, want the one probe taken before the budget could not cover the next reset", clock.Waited())
	}
	if outcome.Status != runstate.StatusCancelled || tracker.Blocked || !tracker.Released {
		t.Fatalf("outcome = %s, blocked=%t released=%t; want the run ended on the window with its claim given back",
			outcome.Status, tracker.Blocked, tracker.Released)
	}
	if !strings.Contains(outcome.Failure, "already committed 30m0s to waiting") {
		t.Fatalf("the ending does not name what was spent:\n%s", outcome.Failure)
	}
	if developerRuns := len(provider.RequestsForRole(domain.RoleDeveloper)); developerRuns != 2 {
		t.Fatalf("developer invocations = %d, want the refusals the budget allowed and no more", developerRuns)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The committed wait is durable, so a restart cannot buy the run a fresh
	// budget by forgetting what it already spent — and it records what was
	// actually waited rather than the span to a deadline the run never reached.
	if stopped.UsageLimitPausedSeconds != int64(30*time.Minute/time.Second) {
		t.Fatalf("recorded pause total = %ds, want the thirty minutes actually waited", stopped.UsageLimitPausedSeconds)
	}
}

// recordedServed is every served invocation the pipeline wrote down, in order.
type recordedServed struct {
	mu     sync.Mutex
	served []runstate.CapacityServed
}

func (r *recordedServed) Record(served runstate.CapacityServed) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.served = append(r.served, served)
	return nil
}

func (r *recordedServed) forModel(model string) []runstate.CapacityServed {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matched []runstate.CapacityServed
	for _, served := range r.served {
		if served.Model == model {
			matched = append(matched, served)
		}
	}
	return matched
}

// A developer attempt that ended in error with no limit classified on it — a
// terminal api_error the dialect could not name, which may be a limit the
// provider is enforcing — returned no Go error and is still not a served
// attempt. Recording it would lift every refusal of its account and model and
// reopen intake into a window the provider never reopened.
func TestADeveloperAttemptEndingInErrorRecordsNothingServed(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: provider.DeveloperSession,
			IsError: true, StopReason: "api_error", FinalText: "API Error: 429 rate_limit_error",
			Process:   execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
			LastEvent: request.LastSequence,
		}, nil
	}
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	served := &recordedServed{}
	pipeline.CapacityServed = served

	_, _ = pipeline.Run(context.Background(), tracker.Item.ID)
	if len(provider.RequestsForRole(domain.RoleDeveloper)) == 0 {
		t.Fatal("no developer attempt was made, so the test proves nothing")
	}
	served.mu.Lock()
	defer served.mu.Unlock()
	if len(served.served) != 0 {
		t.Fatalf("served = %#v, want nothing recorded for an attempt that ended in error", served.served)
	}
}

// A review the provider refused is recorded as nothing served, and the review
// it then answers is recorded on the model that review asked for, after the
// refusal. A served record written for the refused review would lift the very
// refusal the run is waiting out.
func TestARefusedReviewRecordsNothingServedAndTheAnsweredOneRecordsItsModel(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := testharness.BaseTime.Add(45 * time.Minute)
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
				IsError: true, StopReason: "usage_limit",
				UsageLimit: &backend.UsageLimit{Kind: "seven_day", ResetsAt: resetsAt},
				Process:    execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
				LastEvent:  request.LastSequence,
			}, nil
		}
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: provider.ReviewerSession,
			ResolvedModel: testharness.ReviewerResolved, FinalText: testharness.ApproveVerdict,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
		}, nil
	}
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 6*time.Hour, 6*time.Hour)
	const reviewerModel = "sonnet"
	reviewer := pipeline.Config.Agents["reviewer"]
	reviewer.Model = reviewerModel
	pipeline.Config.Agents["reviewer"] = reviewer
	pipeline.Reviewer = review.Reviewer{Backend: provider, Model: reviewerModel}
	served := &recordedServed{}
	pipeline.CapacityServed = served

	var refusedAt time.Time
	servedDuringPause := -1
	clock.OnSleep = func() {
		loaded, err := store.Load(testharness.PipelineRunID)
		if err != nil {
			t.Errorf("Load() during the pause error = %v", err)
			return
		}
		if loaded.UsageLimitPausedSince != nil {
			refusedAt = *loaded.UsageLimitPausedSince
		}
		servedDuringPause = len(served.forModel(reviewerModel))
	}

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if servedDuringPause != 0 {
		t.Fatalf("served records for %s while the refused review was waited out = %d, want none", reviewerModel, servedDuringPause)
	}
	answered := served.forModel(reviewerModel)
	if len(answered) != 1 {
		t.Fatalf("served records for %s = %#v, want the one review that answered", reviewerModel, answered)
	}
	refusal := runstate.UsageLimitExhaustion{At: refusedAt, Model: reviewerModel, AccountAlias: answered[0].AccountAlias}
	if refusedAt.IsZero() || !answered[0].Lifts(refusal) {
		t.Fatalf("served %#v does not lift the refusal at %s, want the answered review recorded after it", answered[0], refusedAt)
	}
}

// TestRunPollsAUsageLimitThatNamesNoResetTime covers the correction the operator
// made on 2026-08-16: a limit reported without a reset time is unknown rather
// than unwaitable. The overage allowance reports this way while the ordinary
// rolling window keeps resetting on its usual schedule, so the run waits the
// configured interval and asks again instead of stopping.
func TestRunPollsAUsageLimitThatNamesNoResetTime(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	limit := backend.UsageLimit{Kind: "seven_day"}
	provider := testharness.UsageLimitBackend(1, &limit, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 6*time.Hour, 6*time.Hour)
	// The deadline the poll records is the harness's own, and the record has to
	// say so: read back as a reset the provider named, it would be a time the
	// provider never quoted.
	var pausedState runstate.State
	clock.OnSleep = func() {
		loaded, err := store.Load(testharness.PipelineRunID)
		if err != nil {
			t.Errorf("Load() during the pause error = %v", err)
			return
		}
		pausedState = loaded
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run to wait and continue rather than stop", err)
	}
	if outcome.Blocked {
		t.Fatalf("outcome = %#v, want the run continued rather than blocked", outcome)
	}
	if len(clock.Slept) == 0 {
		t.Fatal("nothing was waited for; a limit with no reset time should poll")
	}
	if !pausedState.UsageLimitResetUnknown || pausedState.UsageLimitResetsAt == nil {
		t.Fatalf("paused state = %#v, want the probe deadline recorded as the harness's own rather than the provider's", pausedState)
	}
	if got := clock.Waited(); got != 30*time.Minute {
		t.Errorf("waited %s, want the configured unknown-reset interval of 30m", got)
	}
	state, loadErr := store.Load(outcome.RunID)
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if state.UsageLimitPausedSeconds == 0 {
		t.Error("the poll did not spend the run's pause budget, so a refusing provider could poll forever")
	}
}

// A named reset is an upper bound on the wait rather than a gate on it. A reset
// time is a claim about the provider and claims go stale in both directions, so
// the run asks again at the configured interval beneath the deadline and re-parks
// on whatever the provider then reports. This is the same polling discipline the
// unknown-reset case above takes, which is the point of it: one rule covers both.
func TestRunProbesBeneathAKnownResetAndReparksOnTheCurrentReport(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first refusal quotes a reset three hours out. The probe half an hour
	// later finds the window still closed, and the provider now quotes a reset
	// only fifteen minutes further on — the rolling window freed room early, which
	// is exactly what waiting to the first quoted edge would have missed.
	resets := []time.Time{
		testharness.BaseTime.Add(3 * time.Hour),
		testharness.BaseTime.Add(45 * time.Minute),
	}
	provider := steppingUsageLimitBackend(resets, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 6*time.Hour, 6*time.Hour)

	// What the run is waiting on is read as it waits, so the re-park is checkable
	// as a fresh deadline rather than assumed from the run finishing.
	var deadlines []time.Time
	clock.OnSleep = func() {
		loaded, err := store.Load(testharness.PipelineRunID)
		if err != nil {
			t.Errorf("Load() during the pause error = %v", err)
			return
		}
		if loaded.UsageLimitResetsAt == nil {
			t.Error("a waiting run carries no deadline")
			return
		}
		if len(deadlines) == 0 || !deadlines[len(deadlines)-1].Equal(*loaded.UsageLimitResetsAt) {
			deadlines = append(deadlines, *loaded.UsageLimitResetsAt)
		}
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the probing run did not complete normally: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	// Thirty minutes to the first probe, then the fifteen the second report left.
	if clock.Waited() != 45*time.Minute {
		t.Fatalf("waited %s, want the 30m probe and then the 15m the re-park named", clock.Waited())
	}
	if developerRuns := len(provider.RequestsForRole(domain.RoleDeveloper)); developerRuns != 3 {
		t.Fatalf("developer invocations = %d, want the refusal, the probe, and the served attempt", developerRuns)
	}
	if len(deadlines) != 2 || !deadlines[0].Equal(resets[0]) || !deadlines[1].Equal(resets[1]) {
		t.Fatalf("recorded deadlines = %v, want the first report and then the one the probe earned", deadlines)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The accounting keeps what was actually spent across both waits.
	if finished.UsageLimitPausedSeconds != int64(45*time.Minute/time.Second) {
		t.Fatalf("recorded pause total = %ds, want the 45m actually waited", finished.UsageLimitPausedSeconds)
	}
}

// The in-process bound is on how long a process stays open, not on one probe.
// Probing splits a long wait into many short sleeps, and a bound checked against
// each of them separately would stop bounding anything: an hour's worth of it
// would hold a process open for a six-hour deadline, half an hour at a time.
//
// The default configuration cannot catch this, because it sets the in-process
// bound equal to the maximum pause and every probe fits under both readings. So
// this puts the bound between the probe interval and the maximum, where the two
// readings differ.
func TestRunBoundsHowLongOneProcessStaysOpenAcrossProbes(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// Every refusal quotes the same distant reset, so nothing but the in-process
	// bound can end this process's involvement.
	resets := []time.Time{
		testharness.BaseTime.Add(6 * time.Hour),
		testharness.BaseTime.Add(6 * time.Hour),
		testharness.BaseTime.Add(6 * time.Hour),
	}
	provider := steppingUsageLimitBackend(resets, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 12*time.Hour, time.Hour)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Two thirty-minute probes fit inside the hour; the third would take this
	// process past it, so the run is left in flight instead of being slept on.
	if clock.Waited() != time.Hour {
		t.Fatalf("waited %s, want the process to stay open for its configured hour and no longer", clock.Waited())
	}
	if !outcome.Paused || outcome.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a paused run still in flight", outcome)
	}
	if outcome.UsageLimitResetsAt == nil || !outcome.UsageLimitResetsAt.Equal(resets[0]) {
		t.Fatalf("paused outcome did not report the deadline it is waiting on: %#v", outcome)
	}
	// The probes it did take were real attempts, not sleeps it woke from and
	// went back to.
	if developerRuns := len(provider.RequestsForRole(domain.RoleDeveloper)); developerRuns != 3 {
		t.Fatalf("developer invocations = %d, want the refusal and the two probes the hour allowed", developerRuns)
	}
	// Nothing is cleaned up and nothing is terminal: the run is owed the attempt
	// a later invocation will reissue, with the whole bound available to it again.
	if tracker.Blocked || tracker.Closed || !tracker.Claimed {
		t.Fatalf("the pause disturbed the work item: blocked=%t closed=%t claimed=%t", tracker.Blocked, tracker.Closed, tracker.Claimed)
	}
	paused, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if paused.Status.Terminal() || paused.UsageLimitResetsAt == nil {
		t.Fatalf("paused state = %#v, want an in-flight run still carrying its deadline", paused)
	}
	if paused.UsageLimitPausedSeconds != int64(time.Hour/time.Second) {
		t.Fatalf("recorded pause total = %ds, want the hour actually waited", paused.UsageLimitPausedSeconds)
	}
}

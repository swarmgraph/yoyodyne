package pipelinerunstests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func dyingRepairBackend() *orchestratortest.Backend {
	provider := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	attempts := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleDeveloper {
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.ReviewerSession,
				ResolvedModel: testharness.ReviewerResolved, FinalText: testharness.ApproveVerdict,
				Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
			}, nil
		}
		attempts++
		if attempts > 1 {
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.DeveloperSession,
				IsError: true, StopReason: "api_error", FinalText: testharness.ConnectionClosedMessage,
				TransientFailure: &backend.TransientFailure{Detail: "api_error: " + testharness.ConnectionClosedMessage},
				Process:          execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
				LastEvent:        request.LastSequence,
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

// A provider that keeps dying must not become a run that relaunches forever.
// The budget is what stops it, and only a spent budget reaches a person.
func TestRunBlocksWhenTheRelaunchBudgetIsSpent(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// More deaths than the budget can pay for, so what stops the run is the budget
	// rather than the provider recovering. The deaths are of something the harness
	// cannot classify, which is what leaves the budget as the bound: a plainly
	// recoverable one carries on past it.
	provider := testharness.OpaqueDeathBackend(10, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopRelaunchBudget)
	if outcome.StopClass != runstate.StopRelaunchBudget {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.StopRelaunchBudget)
	}
	if err == nil {
		t.Fatalf("Run() error = nil, want the run to stop once its budget was spent")
	}
	if attempts := len(provider.RequestsForRole(domain.RoleDeveloper)); attempts != 3 {
		t.Fatalf("developer invocations = %d, want the dead attempt and the two relaunches it paid for", attempts)
	}
	if !tracker.Blocked || !outcome.Blocked {
		t.Fatalf("the spent budget left no blocker: tracker=%t outcome=%t", tracker.Blocked, outcome.Blocked)
	}
	for _, want := range []string{"Relaunches: 2 of 2 permitted", testharness.OpaqueDeathMessage, "nothing here says the change is wrong"} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Fatalf("blocker is missing %q:\n%s", want, tracker.BlockReason)
		}
	}
	// Nothing judged this run before the provider killed it, so the note claims
	// no repair evidence either.
	for _, unwanted := range []string{"Repair attempts already spent", "Last failing check"} {
		if strings.Contains(tracker.BlockReason, unwanted) {
			t.Fatalf("blocker reported %q on a run that never reached the gate:\n%s", unwanted, tracker.BlockReason)
		}
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.Status.Terminal() || stopped.TransientRelaunches != 2 {
		t.Fatalf("stopped state = %#v, want a terminal run that spent its whole budget", stopped)
	}
	// The change is preserved for whoever picks the item up.
	if _, statErr := os.Stat(stopped.WorktreePath); statErr != nil {
		t.Fatalf("the stopped run's worktree did not survive: %v", statErr)
	}
}

// The whole point of a durable budget is that a crash cannot refill it. A
// process that died having spent the budget comes back to a run with no room
// left, and the next death blocks rather than buying another relaunch.
func TestARestartCannotBuyAFreshRelaunchBudget(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// A check the first attempt cannot pass, so the run reaches its repair loop —
	// the interrupted run a later process picks up at all.
	command := `test -f fixed.txt || { echo "fixed.txt is missing" >&2; exit 3; }`

	// The first process is interrupted the moment its first relaunch is recorded:
	// that write is let through and nothing after it is, so what survives is a
	// non-terminal run carrying a relaunch it already spent.
	interrupted := &testharness.InterruptedStore{
		StateStore: store,
		Reached:    func(state runstate.State) bool { return state.TransientRelaunches >= 1 },
		AllowSaves: 1,
	}
	first := dyingRepairBackend()
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{command}), first)
	firstPipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
	firstOutcome, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !interrupted.Stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.Stopped)
	}
	interruptedState, err := store.Load(firstOutcome.RunID)
	if err != nil {
		t.Fatalf("Load() interrupted state error = %v", err)
	}
	if interruptedState.Status.Terminal() || interruptedState.TransientRelaunches != 1 || interruptedState.RepairAttempts != 1 {
		t.Fatalf("interrupted state = %#v, want a live run in its repair loop with one relaunch spent", interruptedState)
	}

	// The second process adopts it, with room for one relaunch that the run has
	// already spent, and the same provider keeps killing it.
	second := testharness.OpaqueDeathBackend(10, testharness.ApproveVerdict)
	resumed := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{command}), second)
	resumed.Config.Execution.TransientRelaunchesBeforeBlocking = 1
	outcome, err := resumed.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatalf("resumed Run() error = nil, want the spent budget to stop the run")
	}
	if outcome.RunID != firstOutcome.RunID {
		t.Fatalf("resumed run = %q, want the interrupted run %q", outcome.RunID, firstOutcome.RunID)
	}
	// One invocation and no more: the recorded relaunch is inherited rather than
	// forgotten, so this process has nothing left to spend.
	if attempts := len(second.RequestsForRole(domain.RoleDeveloper)); attempts != 1 {
		t.Fatalf("resumed developer invocations = %d, want the restart to buy no relaunch", attempts)
	}
	if !tracker.Blocked || !outcome.Blocked {
		t.Fatalf("the spent budget left no blocker: tracker=%t outcome=%t", tracker.Blocked, outcome.Blocked)
	}
	if !strings.Contains(tracker.BlockReason, "Relaunches: 1 of 1 permitted") {
		t.Fatalf("blocker did not name the inherited budget:\n%s", tracker.BlockReason)
	}
	// The provider killed this run inside its repair loop, so the run holds a
	// spent attempt and a check that was failing. A blocker that told the reader
	// nothing was wrong with the change would be denying evidence it prints.
	for _, want := range []string{"Repair attempts already spent: 1", "Last failing check: " + command + " (exit 3)", "unresolved rather than dismissed"} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Fatalf("blocker is missing %q:\n%s", want, tracker.BlockReason)
		}
	}
	if strings.Contains(tracker.BlockReason, "nothing here says the change is wrong") {
		t.Fatalf("blocker denied the repair evidence the run was carrying:\n%s", tracker.BlockReason)
	}
}

// A transiently overloaded provider is the most retryable refusal there is, and
// the harness used to fail a run on it outright. It waits the short configured
// interval and reissues, on the same budget every other wait spends.
func TestRunPausesForATransientServerOverloadAndReissues(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := testharness.ServerOverloadBackend(1, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 6*time.Hour, 6*time.Hour)
	pipeline.Config.Execution.ServerOverloadPause = config.Duration(90 * time.Second)

	// The deadline has to be on disk before the wait starts, for the same reason
	// an exhausted limit's is: a process that dies mid-wait must lose nothing.
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
		t.Fatalf("Run() error = %v", err)
	}
	if pausedState.UsageLimitResetsAt == nil || !pausedState.UsageLimitResetsAt.Equal(testharness.BaseTime.Add(90*time.Second)) {
		t.Fatalf("the overload deadline was not durable before the wait began: %#v", pausedState.UsageLimitResetsAt)
	}
	// What the run is waiting on has to be recorded, because the deadline alone
	// would describe an overload as an exhausted account to everyone who reads it.
	if pausedState.PauseCause != runstate.PauseServerOverload || pausedState.UsageLimitKind != "" {
		t.Fatalf("paused state = %#v, want a run recorded as waiting out a server overload", pausedState)
	}
	if clock.Waited() != 90*time.Second {
		t.Fatalf("waited %s in total, want the configured 90s overload pause", clock.Waited())
	}
	// The wait spends the aggregate budget, which is what stops an overload that
	// never lifts from reissuing forever.
	if pausedState.UsageLimitPaused() != 90*time.Second {
		t.Fatalf("committed %s to the pause budget, want the 90s it waited", pausedState.UsageLimitPaused())
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the reissued run did not complete normally: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want the refused attempt and its reissue", len(developerRequests))
	}
	// The reissue continues the session the refused attempt established rather
	// than starting the work over.
	if developerRequests[1].SessionID != provider.DeveloperSession {
		t.Fatalf("reissued attempt session = %q, want %q", developerRequests[1].SessionID, provider.DeveloperSession)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.UsageLimitResetsAt != nil || finished.PauseCause != "" {
		t.Fatalf("a finished run is still recorded as waiting: %#v", finished)
	}
}

// The operator reads the waiting run without holding its lease, so a release
// they typed against the pause they saw can land just after the run cleared it.
// Acting on that would release a pause the provider reported afterwards, which
// nobody has said anything about — so a release older than the pause being
// served is left alone.
func TestAReleaseOlderThanThePauseItFindsIsNotActedOn(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resets := []time.Time{
		testharness.BaseTime.Add(20 * time.Minute),
		testharness.BaseTime.Add(50 * time.Minute),
	}
	provider := testharness.SteppingUsageLimitBackend(resets, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 6*time.Hour, 6*time.Hour)

	// Recorded as of the moment the first pause began, but written to disk only
	// once the run is serving its second one: this is the operator whose release
	// was overtaken by the run reissuing on its own.
	stale := runstate.Release{
		SchemaVersion: runstate.ReleaseSchemaVersion,
		ProductID:     "yoyodyne",
		RunID:         testharness.PipelineRunID,
		WorkItemID:    tracker.Item.ID,
		ReleasedAt:    testharness.BaseTime,
	}
	written := false
	clock.OnSleep = func() {
		if written || clock.Instant.Before(resets[0]) {
			return
		}
		written = true
		if err := store.RecordRelease(stale); err != nil {
			t.Errorf("RecordRelease() error = %v", err)
		}
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the run did not carry on normally: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	// The second pause was served in full: twenty minutes to the first deadline
	// and thirty more to the second, with the stale release changing neither.
	if clock.Waited() != 50*time.Minute {
		t.Fatalf("waited %s, want both waits served with the stale release ignored", clock.Waited())
	}
	if developerRuns := len(provider.RequestsForRole(domain.RoleDeveloper)); developerRuns != 3 {
		t.Fatalf("developer invocations = %d, want the two refusals and the served attempt", developerRuns)
	}
}

// The operator can release a recorded wait, because the deadline is a claim
// about the provider and they are the one who can change what it is a claim
// about: on 2026-08-18 capacity was raised while two runs slept against an 18:50
// reset, and there was no verb for saying so. The verb wakes the waiting process
// rather than stopping it — killing a run leaves a cancelled run whose item stays
// claimed, which is recovery rather than release.
func TestOperatorReleaseWakesARunWaitingOnAUsageLimit(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// A reset five hours out: far enough that nothing but a release could end this
	// wait inside the minute the operator ends it in.
	resetsAt := testharness.BaseTime.Add(5 * time.Hour)
	provider := testharness.UsageLimitBackend(1, &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 6*time.Hour, 6*time.Hour)

	releasedAfter := testharness.BaseTime.Add(time.Minute)
	clock.OnSleep = func() {
		if clock.Instant.Before(releasedAfter) {
			return
		}
		if err := store.RecordRelease(runstate.Release{
			SchemaVersion: runstate.ReleaseSchemaVersion,
			ProductID:     "yoyodyne",
			RunID:         testharness.PipelineRunID,
			WorkItemID:    tracker.Item.ID,
			ReleasedAt:    clock.Instant,
		}); err != nil {
			t.Errorf("RecordRelease() error = %v", err)
		}
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The wait ended when the operator said so, not at the deadline and not at
	// the probe the run had planned.
	if clock.Waited() > 2*time.Minute {
		t.Fatalf("waited %s after a release a minute in, want the wait cut short", clock.Waited())
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the released run did not carry on normally: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	if developerRuns := len(provider.RequestsForRole(domain.RoleDeveloper)); developerRuns != 2 {
		t.Fatalf("developer invocations = %d, want the refused attempt and its reissue", developerRuns)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Paused-time accounting keeps what was actually spent: the release gives back
	// the rest of a probe the run committed to but never waited.
	if finished.UsageLimitPausedSeconds != int64(clock.Waited()/time.Second) {
		t.Fatalf("recorded pause total = %ds, want the %s actually waited",
			finished.UsageLimitPausedSeconds, clock.Waited())
	}
	// The release is consumed as it is acted on, so it cannot release whatever
	// pause the reissued attempt earns next.
	if _, still, err := store.ReleasedWait(outcome.RunID); err != nil || still {
		t.Fatalf("ReleasedWait() = %t, %v, want the release consumed", still, err)
	}
}

// A release into a window the provider has still closed costs one refused
// request and nothing else: the run re-parks on what the provider now reports,
// keeps everything it had, and is as waitable as it was before.
func TestAReleaseIntoAStillClosedWindowReparksCleanly(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resets := []time.Time{
		testharness.BaseTime.Add(5 * time.Hour),
		testharness.BaseTime.Add(20 * time.Minute),
	}
	provider := testharness.SteppingUsageLimitBackend(resets, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &testharness.PausingClock{Instant: testharness.BaseTime}
	pipeline = testharness.Waiting(testharness.Automatic(pipeline, provider), clock, 6*time.Hour, 6*time.Hour)

	// Released once, a minute in. The second refusal must not be released again by
	// the same record, which is what makes this a re-park rather than a spin.
	releases := 0
	clock.OnSleep = func() {
		if releases > 0 || clock.Instant.Before(testharness.BaseTime.Add(time.Minute)) {
			return
		}
		releases++
		if err := store.RecordRelease(runstate.Release{
			SchemaVersion: runstate.ReleaseSchemaVersion,
			ProductID:     "yoyodyne",
			RunID:         testharness.PipelineRunID,
			WorkItemID:    tracker.Item.ID,
			ReleasedAt:    clock.Instant,
		}); err != nil {
			t.Errorf("RecordRelease() error = %v", err)
		}
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the re-parked run did not carry on normally: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	// Just over a minute to the release, and then the rest of the twenty minutes
	// the refused probe's own report named — a fresh wait rather than a spin on
	// the release that had already been acted on.
	if clock.Waited() != 20*time.Minute {
		t.Fatalf("waited %s, want the released minute and then the wait the re-park named", clock.Waited())
	}
	if developerRuns := len(provider.RequestsForRole(domain.RoleDeveloper)); developerRuns != 3 {
		t.Fatalf("developer invocations = %d, want the refusal, the released probe, and the served attempt", developerRuns)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.UsageLimitPausedSeconds != int64(clock.Waited()/time.Second) {
		t.Fatalf("recorded pause total = %ds, want the %s actually waited",
			finished.UsageLimitPausedSeconds, clock.Waited())
	}
}

// A developer the harness stopped on time reported nothing, and what it made is
// still in the worktree. The run is left in flight and resumable rather than
// failed, and a later invocation continues the same run, in the same worktree
// and the same session, instead of spending a whole attempt over again.
func TestRunLeavesAProviderStoppedOnTimeResumableRatherThanFailed(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		status execution.ProcessStatus
		want   string
	}{
		{name: "stalled", status: execution.ProcessStalled, want: runstate.ProviderStopStalled},
		{name: "total budget exhausted", status: execution.ProcessTimedOut, want: runstate.ProviderStopBudgetExhausted},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository, worktreeRoot, store := testharness.RestartableFixture(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			first := testharness.ProviderStopBackend(1, testCase.status, testharness.ApproveVerdict)
			firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)

			paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v, want a stopped provider reported as a pause rather than a failure", err)
			}
			if !paused.Paused || paused.Status != runstate.StatusRunning {
				t.Fatalf("outcome = %#v, want a paused run still in flight", paused)
			}
			if paused.ProviderStop != testCase.want {
				t.Fatalf("outcome reported stop %q, want %q", paused.ProviderStop, testCase.want)
			}
			if paused.Failure != "" {
				t.Fatalf("a stopped provider was reported as a failure: %q", paused.Failure)
			}
			// The developer said nothing, so nothing may claim it did.
			if strings.Contains(tracker.Notes, "developer reported failure") {
				t.Fatalf("the harness blamed the developer for its own stop:\n%s", tracker.Notes)
			}
			if tracker.Blocked || tracker.Closed || !tracker.Claimed {
				t.Fatalf("the stop disturbed the work item: blocked=%t closed=%t claimed=%t", tracker.Blocked, tracker.Closed, tracker.Claimed)
			}
			stoppedState, err := store.Load(paused.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if stoppedState.Status.Terminal() || stoppedState.ProviderStop != testCase.want {
				t.Fatalf("stopped state = %#v, want a non-terminal run recording the stop", stoppedState)
			}
			// The worktree, branch, and developer session are what make the run
			// continuable; the partial work is what continuing it saves.
			if stoppedState.WorktreePath == "" || stoppedState.Branch == "" || stoppedState.ProviderSessionID != first.DeveloperSession {
				t.Fatalf("the stop did not preserve the run's artifacts or session: %#v", stoppedState)
			}
			if _, err := os.Stat(filepath.Join(stoppedState.WorktreePath, "partial.txt")); err != nil {
				t.Fatalf("the stopped attempt's work did not survive: %v", err)
			}

			// A later invocation picks up the same run and finishes it.
			second := testharness.ProviderStopBackend(0, testCase.status, testharness.ApproveVerdict)
			secondPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
			outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("resumed Run() error = %v", err)
			}
			if outcome.RunID != paused.RunID || outcome.WorktreePath != paused.WorktreePath {
				t.Fatalf("resumed run = %#v, want the stopped run %s in %s", outcome, paused.RunID, paused.WorktreePath)
			}
			if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
				t.Fatalf("the resumed run did not complete normally: %#v (blocked=%t)", outcome, tracker.Blocked)
			}
			if claims := testharness.CountCalls(tracker.Calls, "claim"); claims != 1 {
				t.Fatalf("claims = %d, want the item claimed once across the stop", claims)
			}
			developerRequests := second.RequestsForRole(domain.RoleDeveloper)
			if len(developerRequests) != 1 {
				t.Fatalf("resumed developer invocations = %d, want one", len(developerRequests))
			}
			// Continuing the stopped session is what makes this a continuation
			// rather than a re-run.
			if developerRequests[0].SessionID != first.DeveloperSession {
				t.Fatalf("the resumed attempt session = %q, want the stopped attempt's %q", developerRequests[0].SessionID, first.DeveloperSession)
			}
			// The stop is spent once the attempt it owed has run.
			finished, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if finished.ProviderStop != "" {
				t.Fatalf("a finished run still carries a provider stop: %q", finished.ProviderStop)
			}
		})
	}
}

// A reviewer the harness stopped on time never judged anything either, and the
// change waiting to be judged is untouched. The run keeps its change and is
// asked for another review rather than losing the developer's work.
func TestRunLeavesAStoppedReviewerResumableAndReviewsAgain(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	// The reviewer stalls; everything before it succeeded.
	first.Respond = testharness.StoppingReviewer(first.Respond, first.ReviewerSession)
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)

	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a stopped reviewer reported as a pause", err)
	}
	if !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("outcome = %#v, want a run paused for a stalled reviewer", paused)
	}
	if tracker.Blocked || tracker.Closed {
		t.Fatalf("the stopped review disturbed the work item: blocked=%t closed=%t", tracker.Blocked, tracker.Closed)
	}
	if strings.Contains(tracker.Notes, "reviewer reported failure") {
		t.Fatalf("the harness blamed the reviewer for its own stop:\n%s", tracker.Notes)
	}

	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	secondPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the resumed run did not finish the same change: %#v", outcome)
	}
	// The change was already made, so resuming re-reviews it rather than
	// redeveloping it or spending a repair attempt on it.
	if developers := second.RequestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("resumed developer invocations = %d, want none for a stopped review", len(developers))
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want a stopped review to cost none", outcome.RepairAttempts)
	}
}

// A run its hosting watch session stopped for a redeploy — the drain bound
// having run out with the run at its checks — is left in flight and resumable
// rather than cancelled: the stop is on the record with the phase it was at,
// the item stays claimed, and a later invocation re-adopts the same run in the
// same worktree, re-earns the gate from the checks, and finishes it without
// spending a developer attempt. This is the 07:35Z shape of 2026-09-19 from the
// run's side: a check suite the session gave up waiting on.
func TestRunLeavesARunStoppedForARedeployResumableAndReadoptsIt(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	// The check says when it is running and then runs for as long as the race
	// suite did under load: until the session stops it.
	running := filepath.Join(t.TempDir(), "checking")
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first,
		[]string{"touch '" + running + "' && sleep 60"}), first)

	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	drained := orchestrator.RedeployDrain{At: time.Date(2026, 9, 19, 7, 50, 0, 0, time.UTC), Bound: 15 * time.Minute, SessionID: "watch-before"}
	go func() {
		// The bound runs out once the run is at its checks. The check's own
		// marker is what says it is, so this never cancels an attempt the
		// developer was still making.
		for {
			if _, err := os.Stat(running); err == nil {
				stop(drained)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()

	paused, err := firstPipeline.Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a run stopped for a redeploy reported as a pause rather than a failure", err)
	}
	if !paused.Paused || paused.Status != runstate.StatusRunning || paused.RedeployStop == nil {
		t.Fatalf("outcome = %#v, want a paused run still in flight carrying the redeploy stop", paused)
	}
	if paused.RedeployStop.Phase != runstate.PhaseChecking || paused.RedeployStop.Bound() != 15*time.Minute || paused.RedeployStop.SessionID != "watch-before" {
		t.Fatalf("redeploy stop = %#v, want the phase, the bound, and the session recorded", paused.RedeployStop)
	}
	if paused.Failure != "" {
		t.Fatalf("a run stopped for a redeploy was reported as a failure: %q", paused.Failure)
	}
	if tracker.Blocked || tracker.Closed || !tracker.Claimed {
		t.Fatalf("the stop disturbed the work item: blocked=%t closed=%t claimed=%t", tracker.Blocked, tracker.Closed, tracker.Claimed)
	}
	if !strings.Contains(tracker.Notes, "paused this run for a redeploy") || !strings.Contains(tracker.Notes, "15m0s") {
		t.Fatalf("the item was not told the run was paused for a redeploy under its bound:\n%s", tracker.Notes)
	}
	stoppedState, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stoppedState.Status.Terminal() || stoppedState.RedeployStop == nil || stoppedState.Phase != runstate.PhaseChecking {
		t.Fatalf("stopped state = %#v, want a non-terminal run recording the stop at its checks", stoppedState)
	}
	if stoppedState.WorktreePath == "" || stoppedState.Branch == "" || stoppedState.ProviderSessionID != first.DeveloperSession {
		t.Fatalf("the stop did not preserve the run's artifacts or session: %#v", stoppedState)
	}
	if _, err := os.Stat(filepath.Join(stoppedState.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the stopped run's change did not survive: %v", err)
	}

	// The session that comes back re-adopts the run: the same run, the same
	// worktree, the gate re-earned from the checks, and no developer attempt
	// spent — the change was already made.
	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	secondPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.WorktreePath != paused.WorktreePath {
		t.Fatalf("resumed run = %#v, want the stopped run %s in %s", outcome, paused.RunID, paused.WorktreePath)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the resumed run did not complete normally: %#v (blocked=%t)", outcome, tracker.Blocked)
	}
	if developers := second.RequestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("resumed developer invocations = %d, want none for a run stopped at its checks", len(developers))
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want the stop to have cost none", outcome.RepairAttempts)
	}
	if claims := testharness.CountCalls(tracker.Calls, "claim"); claims != 1 {
		t.Fatalf("claims = %d, want the item claimed once across the stop", claims)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.RedeployStop != nil {
		t.Fatalf("a finished run still carries a redeploy stop: %#v", finished.RedeployStop)
	}
}

// The same stop mid-developer-attempt. The provider process the drain killed
// had established a session and made part of the change, so the run is left
// owed the rest of that attempt: the session that comes back continues it in
// the same developer session, in the same worktree, rather than deriving the
// change again — exactly as a provider the harness stopped on time is
// continued. The cancelled invocation is reported by the adapter the way a
// killed Claude Code process is: an error result with the session on it.
func TestRunLeavesARunStoppedForARedeployMidAttemptResumableInTheSameSession(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	drained := orchestrator.RedeployDrain{At: time.Date(2026, 9, 19, 7, 50, 0, 0, time.UTC), Bound: 15 * time.Minute, SessionID: "watch-before"}
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "partial.txt"), []byte("half done\n"), 0o600)
	}, testharness.ApproveVerdict)
	served := first.Respond
	first.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleDeveloper {
			return served(request)
		}
		// The drain bound runs out while the developer is working: the session
		// cancels the run, the process is killed, and the adapter reports it.
		if _, err := served(request); err != nil {
			return backend.RunResult{}, err
		}
		stop(drained)
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

	paused, err := firstPipeline.Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a run stopped mid-attempt reported as a pause rather than a failure", err)
	}
	if !paused.Paused || paused.Status != runstate.StatusRunning || paused.RedeployStop == nil || paused.RedeployStop.Phase != runstate.PhaseDeveloping {
		t.Fatalf("outcome = %#v, want a paused run carrying the redeploy stop at its developer attempt", paused)
	}
	if paused.Failure != "" || strings.Contains(tracker.Notes, "developer reported failure") {
		t.Fatalf("the harness blamed the developer for its own stop: %q\n%s", paused.Failure, tracker.Notes)
	}
	if tracker.Blocked || tracker.Closed || !tracker.Claimed {
		t.Fatalf("the stop disturbed the work item: blocked=%t closed=%t claimed=%t", tracker.Blocked, tracker.Closed, tracker.Claimed)
	}
	stoppedState, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stoppedState.Status.Terminal() || stoppedState.RedeployStop == nil || stoppedState.Phase != runstate.PhaseDeveloping {
		t.Fatalf("stopped state = %#v, want a non-terminal run recording the stop at its attempt", stoppedState)
	}
	if stoppedState.ProviderSessionID != first.DeveloperSession {
		t.Fatalf("session = %q, want the killed attempt's session %q preserved for the continuation", stoppedState.ProviderSessionID, first.DeveloperSession)
	}
	if _, err := os.Stat(filepath.Join(stoppedState.WorktreePath, "partial.txt")); err != nil {
		t.Fatalf("the stopped attempt's work did not survive: %v", err)
	}

	// The session that comes back continues the attempt in the same session,
	// and the run finishes: the same run, the same worktree, no repair spent.
	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	secondPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.WorktreePath != paused.WorktreePath || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("resumed run = %#v, want the stopped run %s finished in %s", outcome, paused.RunID, paused.WorktreePath)
	}
	developers := second.RequestsForRole(domain.RoleDeveloper)
	if len(developers) != 1 || developers[0].SessionID != first.DeveloperSession {
		t.Fatalf("resumed developer invocations = %#v, want one, continuing session %q", developers, first.DeveloperSession)
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want the stop to have cost none", outcome.RepairAttempts)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.RedeployStop != nil {
		t.Fatalf("a finished run still carries a redeploy stop: %#v", finished.RedeployStop)
	}
}

// A developer stopped for a redeploy before its provider reported any session is
// still left resumable: nothing is lost, because the worktree holds what it
// wrote, and the session that comes back starts a new developer session there.
// The run spends no repair attempt and is not cancelled.
func TestRunLeavesARunStoppedForARedeployBeforeAnySessionResumableInANewSession(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	drained := orchestrator.RedeployDrain{At: time.Date(2026, 11, 17, 8, 46, 0, 0, time.UTC), Bound: 15 * time.Minute, SessionID: "watch-before"}
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
		stop(drained)
		return backend.RunResult{
			Backend:    domain.BackendClaudeCode,
			IsError:    true,
			StopReason: string(execution.ProcessCancelled),
			Process:    execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
			LastEvent:  request.LastSequence,
		}, nil
	}
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)

	paused, err := firstPipeline.Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a run stopped before any session reported as a pause rather than a failure", err)
	}
	if !paused.Paused || paused.Status != runstate.StatusRunning || paused.RedeployStop == nil || paused.RedeployStop.Phase != runstate.PhaseDeveloping {
		t.Fatalf("outcome = %#v, want a paused run carrying the redeploy stop at its developer attempt", paused)
	}
	if tracker.Blocked || tracker.Closed || !tracker.Claimed {
		t.Fatalf("the stop disturbed the work item: blocked=%t closed=%t claimed=%t", tracker.Blocked, tracker.Closed, tracker.Claimed)
	}
	if !strings.Contains(tracker.Notes, "waiting to resume on the new build from its developing step") {
		t.Fatalf("the item does not say the run is waiting to resume, and from which step:\n%s", tracker.Notes)
	}
	stoppedState, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stoppedState.Status.Terminal() || stoppedState.ProviderSessionID != "" {
		t.Fatalf("stopped state = %#v, want a non-terminal run with no session", stoppedState)
	}
	if _, err := os.Stat(filepath.Join(stoppedState.WorktreePath, "partial.txt")); err != nil {
		t.Fatalf("the stopped attempt's work did not survive: %v", err)
	}

	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	secondPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.WorktreePath != paused.WorktreePath || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("resumed run = %#v, want the stopped run %s finished in %s", outcome, paused.RunID, paused.WorktreePath)
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want the stop to have cost none", outcome.RepairAttempts)
	}
}

// And mid-review. A verdict half-made is no verdict, so a run stopped for a
// redeploy while its reviewer was working keeps its change and is owed the gate
// again — the checks and a fresh review — with no developer attempt spent on a
// change that was already made.
func TestRunLeavesARunStoppedForARedeployMidReviewResumableAndReviewsAgain(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	drained := orchestrator.RedeployDrain{At: time.Date(2026, 9, 19, 7, 50, 0, 0, time.UTC), Bound: 15 * time.Minute, SessionID: "watch-before"}
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	served := first.Respond
	first.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleReviewer {
			return served(request)
		}
		stop(drained)
		return backend.RunResult{
			Backend:    domain.BackendClaudeCode,
			SessionID:  first.ReviewerSession,
			IsError:    true,
			StopReason: string(execution.ProcessCancelled),
			Process:    execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
			LastEvent:  request.LastSequence,
		}, nil
	}
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)

	paused, err := firstPipeline.Run(ctx, tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a run stopped mid-review reported as a pause rather than a failure", err)
	}
	if !paused.Paused || paused.RedeployStop == nil || paused.RedeployStop.Phase != runstate.PhaseReviewing {
		t.Fatalf("outcome = %#v, want a paused run carrying the redeploy stop at its review", paused)
	}
	if tracker.Blocked || tracker.Closed || strings.Contains(tracker.Notes, "reviewer reported failure") {
		t.Fatalf("the stopped review disturbed the work item or blamed the reviewer: blocked=%t closed=%t\n%s", tracker.Blocked, tracker.Closed, tracker.Notes)
	}
	stoppedState, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stoppedState.Status.Terminal() || stoppedState.RedeployStop == nil || stoppedState.ProviderSessionID != first.DeveloperSession {
		t.Fatalf("stopped state = %#v, want a non-terminal run with its developer session preserved", stoppedState)
	}

	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	secondPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the resumed run did not finish the same change: %#v", outcome)
	}
	if developers := second.RequestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("resumed developer invocations = %d, want none for a run stopped at its review", len(developers))
	}
	if reviews := second.RequestsForRole(domain.RoleReviewer); len(reviews) != 1 {
		t.Fatalf("resumed review invocations = %d, want the gate re-earned with one fresh review", len(reviews))
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want the stop to have cost none", outcome.RepairAttempts)
	}
}

// A run stopped for a redeploy before it recorded anything a continuation could
// pick up is not held with a marker nothing can act on: it is cancelled, with
// its branch and worktree preserved and its reason naming the redeploy, which is
// the one ending the item forbids being silent about.
func TestRunCancelsARunStoppedForARedeployItCannotContinueNamingTheRedeploy(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	// No session was ever established, so there is nothing for a later attempt
	// to continue in.
	provider.DeveloperSession = ""
	running := filepath.Join(t.TempDir(), "checking")
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"touch '" + running + "' && sleep 60"})
	pipeline = testharness.Automatic(pipeline, provider)

	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	go func() {
		for {
			if _, err := os.Stat(running); err == nil {
				stop(orchestrator.RedeployDrain{At: time.Now(), Bound: 15 * time.Minute, SessionID: "watch-before"})
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()

	outcome, err := pipeline.Run(ctx, tracker.Item.ID)
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopRedeploy)
	if err == nil {
		t.Fatal("Run() error = nil, want a run that could not be continued to end")
	}
	if outcome.Paused || outcome.Status != runstate.StatusCancelled || outcome.StopClass != runstate.StopRedeploy {
		t.Fatalf("outcome = %#v, want a cancelled run rather than one held for a session that cannot re-adopt it", outcome)
	}
	if !strings.Contains(err.Error(), "restart into the build deployed over it") || !strings.Contains(err.Error(), "cancelled with its branch and worktree preserved") {
		t.Fatalf("the failure did not name the redeploy and the preservation: %v", err)
	}
	state, loadErr := store.Load(outcome.RunID)
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if state.RedeployStop != nil || !state.Status.Terminal() || state.WorktreePath == "" || state.Branch == "" {
		t.Fatalf("terminal state = %#v, want no resumption marker on a run that ended, with its artifacts named", state)
	}
	if !strings.Contains(tracker.Notes, "deployed over it") {
		t.Fatalf("the item was not told the redeploy is what stopped the run:\n%s", tracker.Notes)
	}
}

// A target branch that moves while a run is working is what concurrency makes
// ordinary and what an operator committing to main already does today. The run
// replays its change onto where the target went and promotes it after all, and
// everything the promotion depends on is re-earned rather than carried over:
// the checks run again and a second, independent review judges the replayed
// change.

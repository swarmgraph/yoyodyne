package orchestrator

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
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Each check's own limit scales with the machine's load by the stage's rule.
// Under ordinary load make race is given the configured thirty minutes and a
// forty-minute run is stopped at it, unfinished: neither passed nor failed,
// nothing handed back to the developer, the run kept for the harness to run it
// again, and every figure named. Under three times the cores the same check is
// given ninety minutes, never more than the stage's own scaled bound, and an
// eighty-minute run passes.
func TestEachCheckLimitScalesWithLoadByTheStageRule(t *testing.T) {
	t.Parallel()
	for _, loaded := range []bool{false, true} {
		name := "ordinary load"
		if loaded {
			name = "heavy load"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"make test", "make race"})
			pipeline.Config.Execution.CheckTimeout = config.Duration(30 * time.Minute)
			pipeline.Config.Execution.CheckStageTimeout = config.Duration(3 * time.Hour)
			clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
			race := 40 * time.Minute
			load := 8.0
			if loaded {
				race, load = 80*time.Minute, 48
			}
			pipeline.Checks = checks.Runner{
				Process:      &timedChecks{clock: clock, takes: map[string]time.Duration{"make test": time.Minute, "make race": race}},
				Clock:        clock,
				Timeout:      12 * time.Hour,
				StageTimeout: 3 * time.Hour,
			}
			pipeline.Load = func() (float64, int, bool) { return load, 16, true }

			outcome, runErr := pipeline.Run(context.Background(), tracker.Item.ID)
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if loaded {
				if runErr != nil || outcome.Status != runstate.StatusSucceeded {
					t.Fatalf("Run() = %s, %v; want make race to pass inside its scaled limit", outcome.Status, runErr)
				}
				race := outcome.Checks[1]
				if race.Timeout != 90*time.Minute || race.Timeout > state.CheckStage.Bound() {
					t.Fatalf("make race limit = %s under a stage bound of %s, want the 30m limit scaled to 90m and no more than the stage's", race.Timeout, state.CheckStage.Bound())
				}
				if state.CheckStage.CheckConfiguredSeconds != 1800 || state.CheckStage.CheckLimitSeconds != 5400 {
					t.Fatalf("recorded stage = %#v, want the configured and scaled check limits", state.CheckStage)
				}
				return
			}
			if runErr == nil {
				t.Fatal("Run() error = nil, want make race stopped at its own limit")
			}
			if outcome.StopClass != runstate.CauseCheckTimeLimit.StopClass() {
				t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.CauseCheckTimeLimit.StopClass())
			}
			if outcome.Checks[1].Timeout != 30*time.Minute || outcome.Checks[1].StoppedByStage {
				t.Fatalf("make race = %#v, want it stopped at its own unscaled 30m limit", outcome.Checks[1])
			}
			stage := state.CheckStage
			if stage == nil || !stage.CheckStoppedAtLimit || stage.StoppedAtBound || stage.Command != "make race" || stage.CheckLimitSeconds != 1800 || stage.CheckElapsedSeconds != 1800 {
				t.Fatalf("recorded stage = %#v, want make race stopped at its own 30m limit", stage)
			}
			refused := state.Environmental
			if refused == nil || refused.Cause != runstate.CauseCheckTimeLimit || !refused.Settled || !refused.Refused {
				t.Fatalf("environmental record = %#v, want a settled %s stop", refused, runstate.CauseCheckTimeLimit)
			}
			for _, want := range []string{"make race", "30m0s execution.check_timeout limit", "not scaled: a one-minute load average of 8.0 on 16 cores", "attempt 0 at " + state.HarnessCommit, "neither passed nor failed", "30m0s of its 1h30m0s allowance", "run again 0 of 2 times"} {
				if !strings.Contains(refused.Detail, want) {
					t.Fatalf("environmental detail = %q, want it to name %q", refused.Detail, want)
				}
			}
			if state.ChecksPassed != nil || state.CheckFailure != nil {
				t.Fatalf("checks passed %#v, failure %#v; want an unfinished check to be neither", state.ChecksPassed, state.CheckFailure)
			}
			if !state.HarnessContinuesCheckStage() || state.WorktreeRemoved || state.BranchRemoved || state.RepairAttempts != 0 {
				t.Fatalf("stopped run = %#v, want it kept for the harness to run its checks again", state)
			}
			if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 {
				t.Fatalf("developer invocations = %d, want only the first attempt", runs)
			}
			if says := state.CheckStageStopSays(); !strings.Contains(says, "make race reached its own execution.check_timeout limit of 30m and is unfinished") || !strings.Contains(says, "rerun 1 of 2 on this candidate") {
				t.Fatalf("stop says %q", says)
			}
		})
	}
}

// The reruns are bounded across every path that makes them, and across
// restarts. make race stops at its own limit; the harness runs it again in a
// pipeline built afresh over the same store, where it stops at its own limit
// again; the harness runs it once more, where the stage's bound stops it. That
// is two reruns on one candidate, so the harness stops: the change, branch,
// worktree, and developer session are kept, the record says the check did not
// finish and why is unresolved, the stoppage goes to the development manager,
// a decided repair cannot run it a fourth time, and nothing reaches the gate.
func TestUnfinishedCheckRerunsAreBoundedAcrossPathsAndRestarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
	docket := &memoryDocket{}
	// Each build is a process starting again: nothing is carried between them
	// but the store, the repository, and the docket.
	build := func(stage time.Duration) Pipeline {
		pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"make test", "make race"}), provider)
		pipeline.Config.Execution.CheckTimeout = config.Duration(30 * time.Minute)
		pipeline.Config.Execution.CheckStageTimeout = config.Duration(stage)
		pipeline.Checks = checks.Runner{Process: &timedChecks{clock: clock, takes: map[string]time.Duration{"make test": time.Minute, "make race": 40 * time.Minute}}, Clock: clock, Timeout: 30 * time.Minute, StageTimeout: stage}
		pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)
		return pipeline
	}
	continueChecks := func(pipeline Pipeline, runID string) error {
		continuer := CheckStageContinuer{
			Docket: docket, Redocket: pipeline.Docket, Runs: store, Intake: newIntakeHoldStore(t), Items: tracker, Worktrees: pipeline.Worktrees.(*gitworktree.Manager),
			Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
			Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
				// The rerun is on the record before the checks run again.
				recorded, err := store.Load(runID)
				if err != nil {
					t.Fatal(err)
				}
				if last := recorded.CheckStageContinuations[len(recorded.CheckStageContinuations)-1]; last.Command != "make race" || last.Commit != recorded.HarnessCommit || last.AllowanceSeconds != 5400 {
					t.Fatalf("rerun recorded as %#v before the checks ran", last)
				}
				return pipeline.Continue(ctx, workItemID, runID)
			},
		}
		result, err := continuer.Continue(ctx, CheckStageContinueRequest{Run: runID})
		if !result.Continued {
			t.Fatalf("Continue() = %#v, %v; want the checks run again", result, err)
		}
		return err
	}

	outcome, err := build(3*time.Hour).Run(ctx, tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want make race stopped at its own limit")
	}
	runID := outcome.RunID
	// The stop is docketed as the run ends, for the harness to continue, naming
	// the check and the limit that stopped it.
	if len(docket.entries) == 0 {
		t.Fatal("nothing docketed for a check stopped at its own limit")
	}
	if stopped := docket.entries[len(docket.entries)-1]; !stopped.HarnessContinuesChecks || stopped.RunID != runID ||
		!strings.Contains(stopped.Render(), "make race reached its own execution.check_timeout limit of 30m") {
		t.Fatalf("docket entry = %#v, want the harness's continuation of make race at its 30m execution.check_timeout limit", stopped)
	}
	// A pass the run had earlier is no credit for an unfinished check.
	first, err := store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	first.ChecksPassed = &runstate.ChecksPassed{Content: "stale", Attempt: first.RepairAttempts, Commit: first.HarnessCommit, Commands: []string{"make race"}, At: clock.now}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}

	// Rerun 1, by the harness after the check's own limit, in a new process.
	if err := continueChecks(build(3*time.Hour), runID); err == nil {
		t.Fatal("the first rerun passed, want make race stopped at its own limit again")
	}
	second, err := store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	if !second.StoppedUnfinishedAtChecks() || !second.CheckStage.CheckStoppedAtLimit || len(second.CheckReruns()) != 1 || !second.HarnessContinuesCheckStage() {
		t.Fatalf("after the first rerun = %#v, want a second stop at the check's limit with one rerun left", second)
	}
	if remaining, bounded := second.CheckRerunRemaining("make race"); !bounded || remaining != time.Hour {
		t.Fatalf("remaining allowance = %s, %t; want an hour of the 90m left after the first 30m run", remaining, bounded)
	}
	if second.ChecksPassed != nil {
		t.Fatalf("checks passed = %#v, want the stale pass cleared by the unfinished check", second.ChecksPassed)
	}
	approved := second
	approved.ReviewDecision = runstate.ReviewApprove
	gate := activeRun{pipeline: build(3 * time.Hour), state: approved}
	if err := gate.integrationEarned(ctx); !errors.Is(err, ErrIntegrationUnearned) {
		t.Fatalf("integrationEarned() = %v, want an unfinished check refused at the gate", err)
	}

	// Rerun 2, by the harness again, where the stage's bound stops the check
	// first: switching which limit stopped it resets nothing.
	if err := continueChecks(build(20*time.Minute), runID); err == nil {
		t.Fatal("the second rerun passed, want the stage bound to stop make race")
	}
	spent, err := store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	if !spent.StoppedUnfinishedAtChecks() || !spent.CheckStage.StoppedAtBound || len(spent.CheckReruns()) != 2 {
		t.Fatalf("after the second rerun = %#v, want a stop at the stage bound with both reruns spent", spent)
	}
	if spent.HarnessContinuesCheckStage() {
		t.Fatal("the harness would run the check a third time on the same candidate")
	}
	says := spent.CheckStageStopSays()
	for _, want := range []string{"run again 2 times on this candidate", "the check did not finish", "unresolved", "branch, worktree, and developer session are kept", "development manager's decision"} {
		if !strings.Contains(says, want) {
			t.Fatalf("stop says %q, want %q", says, want)
		}
	}
	if spent.WorktreeRemoved || spent.BranchRemoved || spent.ProviderSessionID == "" || spent.ProviderSessionID != first.ProviderSessionID {
		t.Fatalf("spent run = %#v, want its branch, worktree, and session kept", spent)
	}
	if _, err := os.Stat(spent.WorktreePath); err != nil {
		t.Fatalf("worktree %s: %v, want it kept", spent.WorktreePath, err)
	}
	if branch := gitLine(t, repository, "rev-parse", "--verify", spent.Branch); branch == "" {
		t.Fatalf("branch %s is gone, want it kept", spent.Branch)
	}
	latest := docket.entries[len(docket.entries)-1]
	if latest.HarnessContinuesChecks || !strings.Contains(latest.Render(), "development manager's decision") {
		t.Fatalf("docket entry = %#v, want the stoppage the development manager's", latest)
	}
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 {
		t.Fatalf("developer invocations = %d, want only the first attempt", runs)
	}
	if spent.RepairAttempts != 0 || spent.CheckFailure != nil || spent.ChecksPassed != nil {
		t.Fatalf("spent run = %#v, want no repair attempt charged and no verdict recorded", spent)
	}

	// A decided repair is a third path, and it shares the same bounds.
	if _, err := store.Triage().GrantRepair(ctx, tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, runID), 2, docketedNow, handbackCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	decided := repairContinuerOver(t, build(3*time.Hour), store, docket, tracker)
	decided.Remains = decided.Worktrees.(*gitworktree.Manager)
	result, err := decided.Continue(ctx, RepairContinueRequest{Run: runID})
	var permanent PermanentCarryOutError
	if result.Continued || !errors.As(err, &permanent) || permanent.Cause != triage.CarryOutCheckRerunsSpent {
		t.Fatalf("decided Continue() = %#v, %v; want it refused because the reruns are spent", result, err)
	}
	after, err := store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.CheckStageContinuations) != 2 || after.Status != runstate.StatusTimedOut {
		t.Fatalf("after the refused decision = %#v, want the run left as the bounds stopped it", after)
	}

	// A new candidate starts the bounds again.
	next := after
	next.RepairAttempts++
	if len(next.CheckReruns()) != 0 || !next.HarnessContinuesCheckStage() {
		t.Fatalf("a new candidate inherits reruns %#v", next.CheckReruns())
	}
}

// A check run again is given no more than its allowance has left, however far
// the load has raised its limit since it first stopped.
func TestARerunCheckIsHeldToWhatItsAllowanceHasLeft(t *testing.T) {
	t.Parallel()
	pipeline := Pipeline{Config: config.Config{}}
	pipeline.Config.Execution.CheckTimeout = config.Duration(30 * time.Minute)
	run := activeRun{pipeline: pipeline, state: runstate.State{HarnessCommit: "abc", RepairAttempts: 1}}
	stage := runstate.CheckStage{BoundSeconds: int64(3 * time.Hour / time.Second), ConfiguredSeconds: 1800, Load: 48, Cores: 16}
	if limit := run.checkLimit(stage, "make race"); limit != 90*time.Minute {
		t.Fatalf("first limit = %s, want 30m scaled to 90m", limit)
	}
	stage.BoundSeconds = 3600
	if limit := run.checkLimit(stage, "make race"); limit != time.Hour {
		t.Fatalf("limit = %s, want no more than the stage's 1h bound", limit)
	}
	stage.BoundSeconds = int64(3 * time.Hour / time.Second)
	run.state.CheckStageContinuations = []runstate.CheckStageContinuation{
		{Command: "make race", ContinuedAt: time.Now(), Reason: "r", Attempt: 1, Commit: "abc", ElapsedSeconds: 1800, LimitSeconds: 1800, AllowanceSeconds: 5400},
		{Command: "make race", ContinuedAt: time.Now(), Reason: "r", Attempt: 1, Commit: "abc", ElapsedSeconds: 2400, LimitSeconds: 2400, AllowanceSeconds: 5400},
	}
	if limit := run.checkLimit(stage, "make race"); limit != 20*time.Minute {
		t.Fatalf("limit = %s, want the 20m the 90m allowance has left", limit)
	}
	if limit := run.checkLimit(stage, "make vet"); limit != 90*time.Minute {
		t.Fatalf("another check's limit = %s, want its own scaled 90m", limit)
	}
}

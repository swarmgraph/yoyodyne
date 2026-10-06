package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The check stage is bounded as a whole. Here is the 2026-09-19 shape with the
// clock turned down: a list whose checks are each inside their own budget and
// whose sum is not, which under the per-check bound alone ran for two hours.
// The stage ends where its bound is, the run ends as a stoppage rather than
// spending a repair attempt, and what it names is the bound, the check the
// bound stopped, and what the stage had spent — on the run, on the item's
// notes, and in the reason the run gives.
func TestTheCheckStageEndsAtItsBoundNamingTheBoundAndTheCheck(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// Two checks a minute each and a third that would run for ninety, each
	// inside a two-hour budget of its own, against a thirty-minute stage: the
	// stage's bound is what stops the third, not its own. The clock is the
	// test's rather than the wall's, so the shape is exact and costs no time.
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"make fmtcheck", "make test", "make race", "make vet"})
	// The run's own record is stamped by the wall clock, and an event stamped
	// before the run began is refused, so the stepped clock starts ahead of it.
	clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
	pipeline.Checks = checks.Runner{
		Process:      &timedChecks{clock: clock, takes: map[string]time.Duration{"make fmtcheck": time.Minute, "make test": time.Minute, "make race": 90 * time.Minute}},
		Clock:        clock,
		Timeout:      2 * time.Hour,
		StageTimeout: 30 * time.Minute,
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.CauseCheckStageBound.StopClass())
	if outcome.StopClass != runstate.CauseCheckStageBound.StopClass() {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.CauseCheckStageBound.StopClass())
	}
	if err == nil {
		t.Fatal("Run() error = nil, want the run stopped at the stage bound")
	}
	for _, want := range []string{"check stage reached its 30m0s execution.check_stage_timeout bound during make race, which had run for 28m0s", "the stage had spent 30m0s across 3 check(s)", "landing_checks", checks.ChangedGoPackagesVariable} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Run() error = %v, want it to name %q", err, want)
		}
	}
	// A stage the bound stopped never judged the change, so the developer is
	// not asked to repair anything and the run stops on the first attempt.
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 {
		t.Fatalf("developer invocations = %d, want only the first attempt", runs)
	}
	if len(outcome.Checks) != 3 || !outcome.Checks[2].StoppedByStage || outcome.Checks[2].Command != "make race" {
		t.Fatalf("outcome checks = %#v, want the third stopped by the stage and the fourth never started", outcome.Checks)
	}
	if outcome.CheckStage == nil || !outcome.CheckStage.StoppedAtBound || outcome.CheckStage.Command != "make race" {
		t.Fatalf("outcome stage = %#v, want it stopped at the bound during the third check", outcome.CheckStage)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusTimedOut || state.Phase != runstate.PhaseChecking || state.CheckFailure != nil {
		t.Fatalf("state = %#v, want a run the harness stopped on time with nothing to repair", state)
	}
	if state.CheckStage == nil || !state.CheckStage.StoppedAtBound || state.CheckStage.Running() {
		t.Fatalf("recorded stage = %#v, want it ended at the bound", state.CheckStage)
	}
	if !strings.Contains(state.Failure, "check_stage_timeout bound during make race") {
		t.Fatalf("recorded failure = %q, want the bound and the check named", state.Failure)
	}
	notes := strings.Join(tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "Check stage: 30m0s of the 30m0s execution.check_stage_timeout bound, stopped at the bound during make race") {
		t.Fatalf("item notes do not say the stage was stopped at its bound:\n%s", notes)
	}
}

// The stage's bound is the configured figure scaled for the machine's load by
// the reading and cap a local Git command's budget uses, read again as each
// check begins: here the stage starts on an idle machine at the configured
// thirty minutes, the load climbs to three times the cores as the second check
// begins, and make race is given the ninety minutes that makes. A stage the
// scaled bound still stops is a stop from outside the work: it names the bound,
// the load, and the check, keeps the run's branch, worktree, and session for the
// harness to continue, and counts toward nothing — no review round, repair
// grant, or re-run on the item, and not the intake brake.
func TestTheCheckStageBoundScalesWithLoadAndAStopUnderItCountsTowardNothing(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"make fmtcheck", "make test", "make race", "make vet"})
	clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
	started := clock.now
	pipeline.Checks = checks.Runner{
		Process:      &timedChecks{clock: clock, takes: map[string]time.Duration{"make fmtcheck": time.Minute, "make test": time.Minute, "make race": 10 * time.Hour, "make vet": time.Minute}},
		Clock:        clock,
		Timeout:      12 * time.Hour,
		StageTimeout: 30 * time.Minute,
	}
	pipeline.Load = func() (float64, int, bool) {
		if clock.now.Equal(started) {
			return 8, 16, true
		}
		return 48, 16, true
	}
	before, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}

	outcome, runErr := pipeline.Run(context.Background(), tracker.Item.ID)
	if runErr == nil {
		t.Fatal("Run() error = nil, want the run stopped at the scaled stage bound")
	}
	for _, want := range []string{
		"check stage reached its 1h30m0s execution.check_stage_timeout bound (the configured 30m0s scaled for a one-minute load average of 48.0 on 16 cores) during make race, which had run for 1h28m0s",
		"the stage had spent 1h30m0s across 3 check(s)",
	} {
		if !strings.Contains(runErr.Error(), want) {
			t.Fatalf("Run() error = %v, want it to name %q", runErr, want)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	stage := state.CheckStage
	if stage == nil || stage.Bound() != 90*time.Minute || stage.Configured() != 30*time.Minute || stage.Load != 48 || stage.Cores != 16 || !stage.StoppedAtBound {
		t.Fatalf("recorded stage = %#v, want the 30m bound scaled to 90m for a load of 48 on 16 cores and stopped at it", stage)
	}
	if says := stage.Describe(clock.now); !strings.HasPrefix(says, "checks: 90m of 90m (30m configured, scaled for a one-minute load average of 48.0 on 16 cores)") {
		t.Fatalf("Describe() = %q, want the scaled bound beside the configured figure", says)
	}
	notes := strings.Join(tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "Check stage: 1h30m0s of the 1h30m0s execution.check_stage_timeout bound (the configured 30m0s scaled for a one-minute load average of 48.0 on 16 cores), stopped at the bound during make race") {
		t.Fatalf("item notes do not say the bound was scaled:\n%s", notes)
	}

	// Recorded as a stop from outside the work, naming the bound, the load, and
	// the check, and settled as one that spent nothing.
	refused := state.Environmental
	if refused == nil || refused.Cause != runstate.CauseCheckStageBound || !refused.Settled || !refused.Refused || refused.Problem != "" {
		t.Fatalf("environmental record = %#v, want a settled %s refusal", refused, runstate.CauseCheckStageBound)
	}
	for _, want := range []string{"1h30m0s", "load average of 48.0 on 16 cores", "make race"} {
		if !strings.Contains(refused.Detail, want) {
			t.Fatalf("environmental detail = %q, want it to name %q", refused.Detail, want)
		}
	}
	// The run keeps everything the harness continues it from.
	if !state.HarnessContinuesCheckStage() || state.WorktreeRemoved || state.BranchRemoved || state.RepairAttempts != 0 {
		t.Fatalf("stopped run = %#v, want its branch, worktree, and session kept for the harness to continue", state)
	}
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 {
		t.Fatalf("developer invocations = %d, want only the first attempt", runs)
	}
	after, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if after.ReviewRounds != before.ReviewRounds || after.CommittedRounds != before.CommittedRounds || after.RepairGrants != before.RepairGrants ||
		after.GrantedRounds != before.GrantedRounds || after.Reruns != before.Reruns {
		t.Fatalf("the item's budgets moved:\nbefore %#v\nafter  %#v", before, after)
	}
	if claimed, _ := store.Reruns().Claimed(tracker.Item.ID); len(claimed) != 0 {
		t.Fatalf("re-runs claimed = %#v, want none", claimed)
	}
	// And the intake brake does not count it: the scheduler reads it as a stop
	// the environment made rather than a run that blocked on its change.
	var scheduled Started
	scheduled.record(completed{outcome: outcome, err: runErr})
	if !scheduled.environmental {
		t.Fatalf("scheduled run = %#v, want the stop read as environmental so the brake counts toward nothing", scheduled)
	}
}

// A stage the bound stopped judged nothing, so what the run is owed is its
// checks again on the change it already has — not a re-run from the target
// branch that redoes the development. The stopped run dockets itself saying load
// stopped it and the harness continues it; the carry-out takes it up with nobody
// deciding anything once a slot is free, even if load stays high; the continued
// run re-runs the checks on the preserved change with no developer invoked,
// then goes on to its review and promotion,
// with the run's and the item's counters exactly where the stop left them.
func TestAStageTheBoundStoppedIsContinuedAtItsChecksByTheHarnessChargingNothing(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		name := "checkout retained"
		if missing {
			name = "checkout missing"
		}
		t.Run(name, func(t *testing.T) {
			repository, worktreeRoot, store := restartableFixture(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			commands := []string{"make fmtcheck", "make test", "make race", "make vet"}
			clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
			docket := &memoryDocket{}
			build := func(takes map[string]time.Duration) (Pipeline, *timedChecks) {
				pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, commands), provider)
				checked := &timedChecks{clock: clock, takes: takes}
				pipeline.Checks = checks.Runner{Process: checked, Clock: clock, Timeout: 2 * time.Hour, StageTimeout: 30 * time.Minute}
				pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)
				return pipeline, checked
			}

			// Loaded: make race would run for ninety minutes, and the stage's bound
			// stops it at thirty.
			loaded, _ := build(map[string]time.Duration{"make fmtcheck": time.Minute, "make test": time.Minute, "make race": 90 * time.Minute})
			outcome, runErr := loaded.Run(context.Background(), tracker.Item.ID)
			if runErr == nil || !strings.Contains(runErr.Error(), "check_stage_timeout bound during make race") {
				t.Fatalf("Run() error = %v, want the run stopped at the stage bound", runErr)
			}
			stopped, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !stopped.StoppedAtStageBound() || !stopped.HarnessContinuesCheckStage() {
				t.Fatalf("stopped run = %#v, want one the stage bound stopped that the harness continues", stopped)
			}
			// The stoppage is on the docket, saying load stopped it and that the harness
			// is the one to move.
			if len(docket.entries) != 1 || !docket.entries[0].HarnessContinuesChecks {
				t.Fatalf("docket = %#v, want the stoppage docketed as one the harness continues (run error %v)", docket.entries, runErr)
			}
			rendered := docket.entries[0].Render()
			for _, want := range []string{"Check stage stopped by load", "not by the change", "the harness continues it itself", "Next mover: the harness", "make race"} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("docket entry does not say %q:\n%s", want, rendered)
				}
			}
			runs, err := store.Triage().Counters(tracker.Item.ID)
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}

			if missing {
				manager := loaded.Worktrees.(*gitworktree.Manager)
				removed, err := manager.RemovePreservedWorktree(context.Background(), worktreeOf(stopped), gitworktree.KeepUncommittedWork)
				if err != nil || !removed.Removed {
					t.Fatalf("remove the fixture checkout = %#v, %v", removed, err)
				}
				stopped.WorktreeRemoved = true
				stopped.WorktreeSweptAt = &stopped.UpdatedAt
				if err := store.Save(stopped); err != nil {
					t.Fatal(err)
				}
				// A docket recorded by the older implementation may advertise no
				// continuation once cleanup marked the directory gone. Selection must
				// re-read the standing obligation from the run.
				docket.entries[0].HarnessContinuesChecks = false
			}

			// Load stays high: just as for fresh work, it does not prevent offering
			// and continuing the preserved change.
			calm, checked := build(map[string]time.Duration{"make fmtcheck": time.Minute, "make test": time.Minute, "make race": 5 * time.Minute, "make vet": time.Minute})
			load := 120.0
			intake := newIntakeHoldStore(t)
			continuer := CheckStageContinuer{
				Docket: docket, Redocket: calm.Docket, Runs: store, Intake: intake, Items: tracker, Worktrees: calm.Worktrees.(*gitworktree.Manager),
				Load:     func() (float64, int, bool) { return load, 8, true },
				Capacity: calm.Config.Execution.MaxConcurrentDevelopers,
				Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
					// Restoration and continuation preserve the recorded counts.
					// The independent review that follows will add its own verdict.
					resumed, err := store.Load(runID)
					if err != nil {
						t.Fatal(err)
					}
					if resumed.RunID != stopped.RunID || resumed.ProviderSessionID != stopped.ProviderSessionID || resumed.RepairAttempts != stopped.RepairAttempts || resumed.ReviewRounds != stopped.ReviewRounds || resumed.IntegrationRetries != stopped.IntegrationRetries {
						t.Fatalf("continuation changed identity or consumed counts before checks: %#v", resumed)
					}
					return calm.Continue(ctx, workItemID, runID)
				},
			}
			carrying := CarryOut{Docket: docket, Decisions: store.Triage(), Reruns: store.Reruns(), Runs: store, CheckStages: continuer}
			tasks, err := carrying.Outstanding()
			if err != nil || len(tasks) != 1 || tasks[0].Decision != DecisionContinueChecks || tasks[0].RunID != outcome.RunID {
				t.Fatalf("Outstanding() = %#v, %v; want the harness's continuation of the stopped stage", tasks, err)
			}
			// The pass put it ahead of higher-priority ready work, because its change
			// is preserved, and the item's record has to say what it went ahead of.
			tasks[0].Preserved = true
			tasks[0].AheadOf = []string{"Codex token usage display (yoyodyne-ifd.435.16)"}
			carried, continued, err := carrying.Carry(context.Background(), tasks[0])
			if err != nil {
				t.Fatalf("Carry() error = %v", err)
			}
			if ahead := aheadOfQueue(tasks[0]); !strings.Contains(tracker.Notes, ahead) || !strings.Contains(carried.Reason, ahead) {
				t.Fatalf("item notes = %q, pass reason = %q; want both to say it went ahead of %q", tracker.Notes, carried.Reason, ahead)
			}
			if !carried.Carried || continued.Status != runstate.StatusSucceeded || continued.Integration == nil {
				t.Fatalf("carried = %#v, outcome status %s; want the continued run checked, reviewed, and promoted", carried, continued.Status)
			}
			// The checks ran again, every one of them, on the change the developer
			// attempt left; the developer was not invoked again.
			if len(checked.ran) != len(commands) {
				t.Fatalf("checks run on the continuation = %v, want all of %v", checked.ran, commands)
			}
			for _, dir := range checked.dirs {
				if dir != stopped.WorktreePath {
					t.Fatalf("a check ran in %s, want the preserved worktree %s", dir, stopped.WorktreePath)
				}
			}
			if developer := provider.RequestsForRole(domain.RoleDeveloper); len(developer) != 1 {
				t.Fatalf("developer invocations = %d, want only the first attempt", len(developer))
			}
			if reviewer := provider.RequestsForRole(domain.RoleReviewer); len(reviewer) != 1 {
				t.Fatalf("reviewer invocations = %d, want one independent review after the checks", len(reviewer))
			}
			if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
				t.Fatalf("integrated feature.txt = %q, want the change the first attempt made", integrated)
			}
			final, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			// ReviewRounds counts every verdict this run obtained, including an
			// approval. The approving review charges no triage budget, checked below.
			if final.RunID != outcome.RunID || final.ProviderSessionID != stopped.ProviderSessionID || final.RepairAttempts != stopped.RepairAttempts || final.ReviewRounds != stopped.ReviewRounds+1 || final.IntegrationRetries != 0 || len(final.CheckStageContinuations) != 1 {
				t.Fatalf("final run = %s, session %q, attempts %d, reviews %d, retries %d, continuations %d; want the same run and session, no attempt charged, one recorded approval and one continuation",
					final.RunID, final.ProviderSessionID, final.RepairAttempts, final.ReviewRounds, final.IntegrationRetries, len(final.CheckStageContinuations))
			}
			after, err := store.Triage().Counters(tracker.Item.ID)
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			// Every budget the record keeps is where the stop left it. What the review
			// that followed the checks wrote about itself — which verdict it last judged,
			// and when — is its own bookmark and spends nothing, so it is set aside.
			after.LastJudged, after.UpdatedAt = runs.LastJudged, runs.UpdatedAt
			if !reflect.DeepEqual(after, runs) {
				t.Fatalf("the item's triage budgets moved across the continuation:\nstopped   %#v\ncontinued %#v", runs, after)
			}
			if claimed, _ := store.Reruns().Claimed(tracker.Item.ID); len(claimed) != 0 {
				t.Fatalf("re-runs claimed = %#v, want none", claimed)
			}
			if missing && !strings.Contains(final.CheckStageContinuations[0].Reason, "missing checkout was restored") {
				t.Fatalf("restored continuation = %#v", final.CheckStageContinuations)
			}
			if !strings.Contains(tracker.Notes, "Continued at its checks") {
				t.Fatalf("item notes do not record the continuation:\n%s", tracker.Notes)
			}
			if closure, closed := docket.closed[docket.entries[0].Key]; !closed || closure.Decision != continuedChecksDocketDecision {
				t.Fatalf("docket closure = %#v, %t; want the entry closed as continued", closure, closed)
			}
		})
	}
}

// A decided repair of a first-attempt timeout also goes directly to the checks.
// Its continuation records no developer repair attempt, so adoption must read
// the decision's check-stage continuation rather than the attempt counter.
func TestADecidedCheckStageContinuationWithNoRepairAttemptsReachesTheChecks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	commands := []string{"make fmtcheck", "make test", "make race", "make vet"}
	clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
	docket := &memoryDocket{}
	build := func(race time.Duration) (Pipeline, *timedChecks) {
		pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, commands), provider)
		checked := &timedChecks{clock: clock, takes: map[string]time.Duration{
			"make fmtcheck": time.Minute, "make test": time.Minute, "make race": race, "make vet": time.Minute,
		}}
		pipeline.Checks = checks.Runner{Process: checked, Clock: clock, Timeout: 2 * time.Hour, StageTimeout: 30 * time.Minute}
		pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)
		return pipeline, checked
	}
	loaded, _ := build(90 * time.Minute)
	outcome, err := loaded.Run(ctx, tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "check_stage_timeout bound during make race") {
		t.Fatalf("Run() error = %v, want a check-stage timeout", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.StoppedAtStageBound() || stopped.RepairAttempts != 0 {
		t.Fatalf("stopped run = %#v, want a first-attempt check-stage timeout", stopped)
	}
	if _, err := store.Triage().GrantRepair(ctx, tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, stopped.RunID), 2, docketedNow, handbackCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	calm, checked := build(5 * time.Minute)
	continuer := repairContinuerOver(t, calm, store, docket, tracker)
	continuer.Remains = calm.Worktrees.(*gitworktree.Manager)
	result, err := continuer.Continue(ctx, RepairContinueRequest{Run: stopped.RunID})
	if err != nil || !result.Continued || !result.Checks || result.Outcome.Status != runstate.StatusSucceeded || result.Outcome.Integration == nil {
		t.Fatalf("Continue() = %#v, %v; want checks, review, and integration completed", result, err)
	}
	if !reflect.DeepEqual(checked.ran, commands) {
		t.Fatalf("continued checks = %v, want %v", checked.ran, commands)
	}
	for _, dir := range checked.dirs {
		if dir != stopped.WorktreePath {
			t.Fatalf("check directory = %s, want %s", dir, stopped.WorktreePath)
		}
	}
	if developer := provider.RequestsForRole(domain.RoleDeveloper); len(developer) != 1 {
		t.Fatalf("developer invocations = %d, want only the original attempt", len(developer))
	}
	final, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.RepairAttempts != 0 || len(final.CheckStageContinuations) != 0 || !final.ContinuedCheckStage() {
		t.Fatalf("final run = %#v, want only the decided check-stage continuation and no repair attempt", final)
	}
	if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature = %q, want the first attempt's change", integrated)
	}
}

func TestADecidedCheckStageContinuationDoesNotAdoptAnUnrecordedOrDifferentStep(t *testing.T) {
	t.Parallel()
	state := continuableState()
	state.Status, state.Phase, state.RepairAttempts = runstate.StatusRunning, runstate.PhaseChecking, 0
	if continuedAtCheckStage(state) {
		t.Fatal("a run with no check-stage continuation was adopted")
	}
	state.RepairContinuations = []runstate.RepairContinuation{{CheckStage: true}}
	if !continuedAtCheckStage(state) {
		t.Fatal("a decided check-stage continuation was not adopted")
	}
	state.Phase = runstate.PhaseDeveloping
	if continuedAtCheckStage(state) {
		t.Fatal("a check-stage continuation adopted a developer attempt")
	}
	state.Phase = runstate.PhaseChecking
	state.RepairContinuations = append(state.RepairContinuations, runstate.RepairContinuation{})
	if continuedAtCheckStage(state) {
		t.Fatal("an earlier check-stage continuation adopted a later repair")
	}
}

// A worktree somebody has been in since the bound stopped the stage is not one
// the harness continues on its own: what the checks would judge is no longer the
// change the attempt left. The refusal is written onto the run so the harness
// does not ask again, and the stoppage is put back on the docket as the
// development manager's.
func TestAStageTheHarnessCannotContinueIsHandedToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
	docket := &memoryDocket{}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"make race"}), provider)
	pipeline.Checks = checks.Runner{Process: &timedChecks{clock: clock, takes: map[string]time.Duration{"make race": 90 * time.Minute}}, Clock: clock, Timeout: 2 * time.Hour, StageTimeout: 30 * time.Minute}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)
	outcome, _ := pipeline.Run(context.Background(), tracker.Item.ID)
	stopped, err := store.Load(outcome.RunID)
	if err != nil || !stopped.HarnessContinuesCheckStage() {
		t.Fatalf("stopped run = %#v (error %v), want one the harness continues", stopped, err)
	}
	// Somebody commits in the preserved worktree.
	runPipelineGit(t, stopped.WorktreePath, "commit", "--allow-empty", "-m", "an edit nobody asked for")

	continuer := CheckStageContinuer{
		Docket: docket, Redocket: pipeline.Docket, Runs: store, Intake: newIntakeHoldStore(t), Items: tracker, Worktrees: pipeline.Worktrees.(*gitworktree.Manager),
		Load:     func() (float64, int, bool) { return 1, 8, true },
		Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(context.Context, string, string) (Outcome, error) {
			t.Fatal("a refused continuation started the run")
			return Outcome{}, nil
		},
	}
	result, err := continuer.Continue(context.Background(), CheckStageContinueRequest{Run: outcome.RunID})
	if !errors.Is(err, ErrWorktreeNotAsLeft) || result.Continued || result.Refused == "" || result.RecordProblem != "" {
		t.Fatalf("Continue() = %#v, %v; want it refused for the worktree with the refusal recorded", result, err)
	}
	refused, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if refused.Status != runstate.StatusTimedOut || refused.HarnessContinuesCheckStage() || len(refused.CheckStageContinuations) != 0 {
		t.Fatalf("refused run = %#v, want it still stopped and no longer the harness's to continue", refused)
	}
	// Docketed again after the harness closed its own entry, so the stoppage is
	// a question again rather than one the closure answered.
	latest := docket.entries[len(docket.entries)-1]
	if closure, closed := docket.closed[latest.Key]; !closed || !latest.RecordedAt.After(closure.ClosedAt) || latest.HarnessContinuesChecks {
		t.Fatalf("docket = %#v, closure %#v; want the stoppage docketed again, after the closure, as the development manager's", docket.entries, closure)
	}
	if rendered := latest.Render(); strings.Contains(rendered, "Next mover: the harness") || !strings.Contains(rendered, "development manager's decision") {
		t.Fatalf("re-docketed entry does not hand the stoppage to the development manager:\n%s", rendered)
	}
	if tasks, err := (CarryOut{Docket: docket, Decisions: store.Triage(), Reruns: store.Reruns(), Runs: store, CheckStages: continuer}).Outstanding(); err != nil || len(tasks) != 0 {
		t.Fatalf("Outstanding() = %#v, %v; want the harness not to ask again", tasks, err)
	}
}

// Past its bound the harness continues a stopped stage no more: the stoppage is
// the development manager's, and the entry and the record say so.
func TestAStageStoppedPastItsContinuationsIsLeftToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	state := runstate.State{
		RunID: "run-" + strings.Repeat("a", 32), WorkItemID: "yoyodyne-task", Status: runstate.StatusTimedOut, Phase: runstate.PhaseChecking,
		WorktreePath: "/tmp/w", Branch: "b", BaseCommit: "c", TargetBranch: "main", ProviderSessionID: "session",
		CheckStage: &runstate.CheckStage{StartedAt: time.Now(), BoundSeconds: 1800, Command: "make race", StoppedAtBound: true},
	}
	if !state.HarnessContinuesCheckStage() {
		t.Fatal("a first stop at the bound is not continued by the harness")
	}
	for range runstate.MaxCheckStageContinuations {
		state.CheckStageContinuations = append(state.CheckStageContinuations, runstate.CheckStageContinuation{ContinuedAt: time.Now(), Reason: "continued"})
	}
	if state.HarnessContinuesCheckStage() {
		t.Fatal("a run continued to the bound is still continued by the harness")
	}
	if says := state.CheckStageStopSays(); !strings.Contains(says, "which is its bound") || !strings.Contains(says, "development manager's decision") {
		t.Fatalf("stop says %q, want the bound spent and the decision the development manager's", says)
	}
	refused := state
	refused.CheckStageContinuations = nil
	refused.CheckStageContinuationRefused = "the worktree is not as the harness left it"
	if refused.HarnessContinuesCheckStage() {
		t.Fatal("a run whose continuation was refused is still continued by the harness")
	}
}

// While the checks run, the record says where the stage stands: when it began,
// the bound, and which check it is on. That is what `yoyo status` reads to say
// "checks: 14m of 30m" for a run instead of "checking, 2h elapsed".
func TestTheRecordSaysWhichCheckTheStageIsOnWhileItRuns(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, nil)
	// The second check reads the run's own record while it is the check the
	// stage is on, which is exactly what a status surface does.
	recordPath := filepath.Join(t.TempDir(), "stage.json")
	pipeline.Config.Checks = []string{
		"true",
		"cp " + filepath.Join(store.Root(), pipelineRunID+".json") + " " + recordPath,
	}
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("the check could not read the run's record: %v", err)
	}
	for _, want := range []string{`"check_stage"`, `"bound_seconds": 1800`, `"command": "cp `, `"narrowed":`} {
		if !strings.Contains(string(recorded), want) {
			t.Fatalf("record while the stage ran = %s, want it to carry %s", recorded, want)
		}
	}
	if strings.Contains(string(recorded), `"finished_at"`) {
		t.Fatalf("record while the stage ran = %s, want the stage still running", recorded)
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.CheckStage == nil || state.CheckStage.Running() || state.CheckStage.StoppedAtBound {
		t.Fatalf("recorded stage = %#v, want it ended inside its bound", state.CheckStage)
	}
}

// Every check is told what the change touches, in the shape the Go command
// takes, so a check the operator wrote to narrow itself runs over that and
// nothing else. A change to one package is that package; a repository that is
// no Go module is told the whole module, so a check written for one runs whole.
func TestEveryCheckIsToldWhichGoPackagesTheChangeTouches(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		module  bool
		changes string
		want    string
	}{
		{name: "a change to one package", module: true, changes: "internal/checks/narrow.go", want: "./internal/checks"},
		{name: "a repository that is no Go module", module: false, changes: "feature.txt", want: "./..."},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			if test.module {
				writeCommitted(t, repository, "go.mod", "module example.com/project\n")
				writeCommitted(t, repository, "internal/checks/runner.go", "package checks\n")
				writeCommitted(t, repository, "internal/orchestrator/pipeline.go", "package orchestrator\n")
			}
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				path := filepath.Join(request.WorkingDirectory, filepath.FromSlash(test.changes))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				return os.WriteFile(path, []byte("package checks\n"), 0o600)
			}, approveVerdict)
			told := filepath.Join(t.TempDir(), "told.txt")
			pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{
				`printf '%s' "$` + checks.ChangedGoPackagesVariable + `" > ` + told,
			})
			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			read, err := os.ReadFile(told)
			if err != nil {
				t.Fatalf("the check did not write what it was told: %v", err)
			}
			if string(read) != test.want {
				t.Fatalf("the check was told %q, want %q", read, test.want)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if state.CheckStage == nil || !strings.Contains(state.CheckStage.Narrowed, strings.TrimPrefix(test.want, "./...")) {
				t.Fatalf("recorded stage = %#v, want the narrowing recorded", state.CheckStage)
			}
			notes := strings.Join(tracker.NoteRecords, "\n")
			if !strings.Contains(notes, "gate narrowed to: ") {
				t.Fatalf("item notes do not say what the gate was narrowed to:\n%s", notes)
			}
		})
	}
}

// steppingClock is a clock the test moves, so a stage's arithmetic is checked
// against known spans rather than against wall-clock sleeps.
type steppingClock struct {
	now time.Time
}

func (c *steppingClock) Now() time.Time { return c.now }

// timedChecks stands in for a project's checks, each taking a known time: a
// command moves the clock by what it takes, cut to the budget it was given,
// and is timed out where the budget was what stopped it.
type timedChecks struct {
	clock *steppingClock
	takes map[string]time.Duration
	// ran and dirs are the checks this runner was asked to run and where.
	ran  []string
	dirs []string
}

func (r *timedChecks) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	started := r.clock.now
	r.ran = append(r.ran, command.Args[len(command.Args)-1])
	r.dirs = append(r.dirs, command.Dir)
	took := r.takes[command.Args[len(command.Args)-1]]
	status := execution.ProcessSucceeded
	if command.Timeout > 0 && took > command.Timeout {
		took = command.Timeout
		status = execution.ProcessTimedOut
	}
	r.clock.now = r.clock.now.Add(took)
	exitCode := 0
	if status != execution.ProcessSucceeded {
		exitCode = -1
	}
	return execution.ProcessResult{Status: status, ExitCode: exitCode, StartedAt: started, FinishedAt: r.clock.now}, nil
}

// writeCommitted puts a file into the repository's history, so a fixture can
// be a Go module with packages the change under test then touches.
func writeCommitted(t *testing.T, repository, relative, content string) {
	t.Helper()
	path := filepath.Join(repository, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", relative)
	runPipelineGit(t, repository, "commit", "-m", "add "+relative)
}

// A path check joins the gate for a change that touches what it vouches for,
// and costs a change that touches none of it nothing: the adoption walk runs
// for a change to the program it documents and not for one to a document
// elsewhere. Where it runs, its result is a check result like any other, so the
// review is shown it, and the stage's record says which path added it.
func TestAPathCheckRunsOnlyForAChangeTouchingWhatItVouchesFor(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		changes string
		added   bool
	}{
		{name: "a change to a walked path", changes: "internal/cli/status.go", added: true},
		{name: "a change to a walked path's test", changes: "internal/cli/status_test.go", added: false},
		{name: "an unrelated change", changes: "docs/notes.md", added: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			writeCommitted(t, repository, "scripts/walk.paths", "/README.md\n/internal/\n!*_test.go\n")
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				path := filepath.Join(request.WorkingDirectory, filepath.FromSlash(test.changes))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				return os.WriteFile(path, []byte("package cli\n"), 0o600)
			}, approveVerdict)
			walked := filepath.Join(t.TempDir(), "walked.txt")
			walk := "printf walked > " + walked
			pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
			pipeline.Config.PathChecks = []config.PathCheck{{Command: walk, Paths: "scripts/walk.paths"}}
			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			_, statErr := os.Stat(walked)
			if ran := statErr == nil; ran != test.added {
				t.Fatalf("the path check ran = %v, want %v", ran, test.added)
			}
			var commands []string
			for _, check := range outcome.Checks {
				commands = append(commands, check.Command)
			}
			want := []string{"true"}
			if test.added {
				want = append(want, walk)
			}
			if strings.Join(commands, "\n") != strings.Join(want, "\n") {
				t.Fatalf("the checks the review is shown = %q, want %q", commands, want)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			recorded := state.CheckStage != nil && strings.Contains(state.CheckStage.Narrowed, "the change touches "+test.changes+", which scripts/walk.paths lists")
			if recorded != test.added {
				t.Fatalf("recorded stage = %#v, want the added check named = %v", state.CheckStage, test.added)
			}
		})
	}
}

// A path check whose list cannot be read runs rather than being passed over:
// the gate has no declaration to skip it on, and the record says what to fix.
func TestAPathCheckWhoseListIsMissingRuns(t *testing.T) {
	t.Parallel()

	added := pathChecksFor(t.TempDir(), []config.PathCheck{{Command: "make adoption", Paths: "scripts/gone.paths"}}, []string{"docs/notes.md"})
	if len(added) != 1 || added[0].command != "make adoption" || !strings.Contains(added[0].reason, "could not be read") {
		t.Fatalf("added = %#v, want the check run and the unreadable list named", added)
	}
}

// A change cannot switch off the check that holds it by editing the list the
// check is chosen by: a change that empties the list and touches a path the
// list covered on the target branch still runs the check, and the record says
// the list's own change is why.
func TestAChangeThatEmptiesAPathChecksListStillRunsIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeCommitted(t, repository, "scripts/walk.paths", "/README.md\n/internal/\n")
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "scripts", "walk.paths"), []byte("# nothing\n"), 0o600); err != nil {
			return err
		}
		path := filepath.Join(request.WorkingDirectory, "internal", "cli", "status.go")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("package cli\n"), 0o600)
	}, approveVerdict)
	walked := filepath.Join(t.TempDir(), "walked.txt")
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Config.PathChecks = []config.PathCheck{{Command: "printf walked > " + walked, Paths: "scripts/walk.paths"}}
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Stat(walked); err != nil {
		t.Fatalf("a change that emptied the list switched off its check: %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.CheckStage == nil || !strings.Contains(state.CheckStage.Narrowed, "touches scripts/walk.paths itself") {
		t.Fatalf("recorded stage = %#v, want the list's own change named", state.CheckStage)
	}
}

// Narrowing the list is the same case as emptying it: whatever the edited list
// now says, the change that edited it runs the check.
func TestAChangeThatNarrowsAPathChecksListStillRunsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "walk.paths"), []byte("/README.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configured := []config.PathCheck{{Command: "make adoption", Paths: "scripts/walk.paths"}}
	if added := pathChecksFor(root, configured, []string{"./scripts/walk.paths", "internal/cli/status.go"}); len(added) != 1 {
		t.Fatalf("added = %#v, want the check run for a change narrowing its list", added)
	}
	if added := pathChecksFor(root, configured, []string{"internal/cli/status.go"}); len(added) != 0 {
		t.Fatalf("added = %#v, want nothing for a change the unedited list does not cover", added)
	}
}

// A hold may still stop a continuation, but its thirty-minute deadline is the
// durable ending of the stopped run, not the lifetime of the watcher.
func TestAnOverdueCheckStageContinuationNamesItsGateAcrossWatcherRestarts(t *testing.T) {
	t.Parallel()
	state := stoppedState()
	state.Status = runstate.StatusTimedOut
	state.Phase = runstate.PhaseChecking
	state.ProviderSessionID = "session"
	state.CheckFailure = nil
	state.CheckStage = &runstate.CheckStage{StartedAt: state.StartedAt, FinishedAt: state.CompletedAt, BoundSeconds: 1800, Command: "make race", StoppedAtBound: true}
	h := newDocketedHarness(t, state)
	tracker := &orchestratortest.Tracker{Item: h.item}
	clock := &steppingClock{now: state.CompletedAt.Add(runstate.CheckStageContinuationWait - time.Nanosecond)}
	if _, err := h.intake.Hold(runstate.IntakeHolderOperator, "held for this test", *state.CompletedAt); err != nil {
		t.Fatal(err)
	}
	started := 0
	build := func() CarryOut {
		return CarryOut{Docket: h.docket, Decisions: h.runs.Triage(), Reruns: h.reruns, Runs: h.runs,
			CheckStages: CheckStageContinuer{Docket: h.docket, Runs: h.runs, Intake: h.intake, Items: tracker,
				Worktrees: &fakeOwnership{}, Capacity: 1, Clock: clock,
				Load: func() (float64, int, bool) { return 160, 8, true },
				Start: func(context.Context, string, string) (Outcome, error) {
					started++
					return Outcome{}, errors.New("the check has not passed")
				}}, Clock: clock}
	}
	carrying := build()
	task := theOneOutstanding(t, carrying)
	if task.Decision != DecisionContinueChecks {
		t.Fatalf("task = %#v", task)
	}
	if carried, _, err := carrying.Carry(context.Background(), task); err != nil || carried.Gate != runstate.TriageGateIntakeHold {
		t.Fatalf("before deadline: %#v, %v", carried, err)
	}
	if len(tracker.NoteRecords) != 0 || started != 0 {
		t.Fatal("the hold did not preserve the run without an overdue note")
	}
	// A restarted watcher remembers that the hold stopped this item. It does
	// not attempt it again, but its unattempted sweep still owes the wait note.
	clock.now = state.CompletedAt.Add(runstate.CheckStageContinuationWait)
	carrying = build()
	passed := map[string]string{state.RunID: "intake is held; lifting the intake hold lets the next pull attempt it"}
	if _, err := carrying.RecordUnattempted(context.Background(), time.Minute, passed); err != nil {
		t.Fatal(err)
	}
	if len(tracker.NoteRecords) != 1 || !strings.Contains(tracker.Notes, "intake is held") || !strings.Contains(tracker.Notes, "What clears it:") {
		t.Fatalf("at deadline: notes = %v", tracker.NoteRecords)
	}
	// Reopen the durable store as a new process would. The elapsed wait and
	// the note survive, so the next pass does not append the same note.
	reopened, err := runstate.NewStore(filepath.Dir(filepath.Dir(filepath.Dir(h.runs.Root()))), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	h.runs = reopened
	h.reruns = reopened.Reruns()
	clock.now = clock.now.Add(14 * time.Hour)
	carrying = build()
	if _, err := carrying.RecordUnattempted(context.Background(), time.Minute, passed); err != nil || len(tracker.NoteRecords) != 1 {
		t.Fatalf("after restart: %v, notes = %v", err, tracker.NoteRecords)
	}
	if _, _, err := h.intake.Release(); err != nil {
		t.Fatal(err)
	}
	carried, outcome, err := carrying.Carry(context.Background(), task)
	if !carried.Carried || started != 1 || err == nil || outcome.Status == runstate.StatusSucceeded {
		t.Fatalf("under persistent high load: %#v, %#v, %v, starts %d", carried, outcome, err, started)
	}
	continued, err := reopened.Load(state.RunID)
	if err != nil || continued.Status != runstate.StatusRunning || len(continued.CheckStageContinuations) != 1 || continued.CheckStageContinuationWaitNoted != "" {
		t.Fatalf("continuation must remain unfinished and spend only its finite continuation: %#v, %v", continued, err)
	}
}

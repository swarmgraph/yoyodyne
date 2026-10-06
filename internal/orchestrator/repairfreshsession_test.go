package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A decided repair of a stopped run that recorded no developer session starts a
// fresh developer session on the preserved change rather than being refused for
// the missing session (see freshSessionWhy).

// sessionlessState is a run stopped with its repair budget spent and no developer
// session on its record, as a session whose budget ran out before the provider
// reported one leaves it.
func sessionlessState() runstate.State {
	state := stoppedState()
	state.ProviderSessionID = ""
	state.StopClass = runstate.StopProviderBudget
	return state
}

// unreturnedSessionState is a run whose provider never returned a session for
// its developer: nothing on its record says a budget ran out.
func unreturnedSessionState() runstate.State {
	state := stoppedState()
	state.ProviderSessionID = ""
	return state
}

// harnessContinuedState is the third way a run lacks a session: the harness
// carried it on itself past a silent session at its review, with no developer,
// and the checks then failed on the change it had.
func harnessContinuedState() runstate.State {
	state := unreturnedSessionState()
	state.ReviewSummary = ""
	state.ReviewFindings = 0
	state.ReviewFindingDetails = nil
	state.CheckFailure = &runstate.CheckFailure{Command: "make test", ExitCode: 1, Output: "panic: test timed out after 20m0s"}
	state.RepairContinuations = []runstate.RepairContinuation{{
		Reason:      "the harness continued the run at its review after a silent session",
		ContinuedAt: docketedNow.Add(-2 * time.Hour),
		Stall:       true,
		ByHarness:   true,
	}}
	return state
}

func TestADecidedRepairOfARunWithNoSessionStartsAFreshSessionRatherThanBeingRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		state runstate.State
		why   string
	}{
		{name: "the session's budget ran out", state: sessionlessState(), why: "ran out of budget"},
		{name: "the provider never returned a session", state: unreturnedSessionState(), why: "never returned one"},
		{name: "the harness continued it past a silent session", state: harnessContinuedState(), why: "past a silent session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			harness := newContinueHarness(t, tc.state)
			result, err := harness.continuer().Continue(context.Background(), continueRequest())
			if err != nil {
				t.Fatalf("Continue() error = %v, want the repair carried out in a fresh session", err)
			}
			if !result.Continued || len(harness.started) != 1 || harness.started[0].runID != docketedRunID {
				t.Fatalf("result = %#v, started = %#v, want the docketed run started once", result, harness.started)
			}
			if !strings.Contains(result.FreshSession, "recorded no developer session") || !strings.Contains(result.FreshSession, tc.why) {
				t.Fatalf("fresh session = %q, want it to say the run had no session and why (%q)", result.FreshSession, tc.why)
			}
			state := harness.reload(t)
			if state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseDeveloping {
				t.Fatalf("run = %s/%s, want it running at the developer attempt", state.Status, state.Phase)
			}
			last := state.RepairContinuations[len(state.RepairContinuations)-1]
			if last.FreshSession != result.FreshSession || !strings.HasPrefix(last.Reason, "Fresh developer session: ") {
				t.Fatalf("continuation = %#v, want the run record to say the repair started a fresh session and why", last)
			}
			// It costs exactly what re-entering a session would: the whole grant and
			// one counted attempt.
			if last.GrantedAttempts != continueGrantRounds || state.RepairAttempts != tc.state.RepairAttempts+1 {
				t.Fatalf("granted %d, attempts %d, want the grant of %d and one attempt counted", last.GrantedAttempts, state.RepairAttempts, continueGrantRounds)
			}
			if !strings.Contains(result.Render(), "started a fresh developer session") {
				t.Fatalf("render = %q, want it to say a fresh session was started", result.Render())
			}
			// The pipeline picks the continued run up rather than refusing it for the
			// session it does not have.
			if !resumableRepair(state) {
				t.Fatal("the continued run is not one the pipeline resumes")
			}
		})
	}
}

// A run with a session still re-enters it: nothing about the ordinary repair
// changes.
func TestARepairOfARunWithASessionStillReEntersThatSession(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	state := harness.reload(t)
	if result.FreshSession != "" || state.RepairContinuations[0].FreshSession != "" || strings.HasPrefix(state.RepairContinuations[0].Reason, "Fresh") {
		t.Fatalf("result = %#v, continuation = %#v, want the recorded session re-entered", result, state.RepairContinuations[0])
	}
	if state.ProviderSessionID != "developer-session" {
		t.Fatalf("session = %q, want the one the run recorded", state.ProviderSessionID)
	}
}

// A stall has no failure to hand a fresh developer, so it still needs the
// session it stopped in.
func TestAStallWithNoSessionIsStillRefused(t *testing.T) {
	t.Parallel()

	state := stalledState()
	state.ProviderSessionID = ""
	harness := newContinueHarness(t, state)
	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "no developer session") {
		t.Fatalf("Continue() error = %v, want the stall refused for its missing session", err)
	}
	if len(harness.started) != 0 || harness.carried(t) != 0 {
		t.Fatalf("started = %#v, want nothing continued and nothing spent", harness.started)
	}
}

// The decision stands with the harness to carry out, and the carry-out pass
// carries it out rather than recording a refusal that would hand it back to the
// development manager.
func TestTheCarryOutPassStartsASessionlessRepairAndLeavesNoRefusal(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, sessionlessState())
	grantedAgainstTheStoppage(t, harness)
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if !triage.AwaitingCarryOut(counters.StandingOf(harness.reload(t))) {
		t.Fatal("the decision does not read as the harness's to carry out")
	}
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || carried.Gate != "" || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want the repair carried out with no gate refusing it", carried, harness.started)
	}
	counters, err = harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.StandingOf(harness.reload(t)).Refused {
		t.Fatal("a refusal stands on the decision, which names the development manager as the next mover")
	}
}

// What a fresh session is handed: the repair input (here the failing check's
// output), the run's record, and the work item.
func TestAFreshSessionIsHandedTheFailureTheRunsRecordAndTheItem(t *testing.T) {
	t.Parallel()

	state := harnessContinuedState()
	state.Changes = &runstate.Changes{Files: "M\tinternal/orchestrator/pipeline.go"}
	state.DeveloperSummary = &runstate.DeveloperSummary{Text: "made the pipeline wait"}
	repair, err := resumedDeveloperPrompt(state, "persona", "", "", "/scratch", []string{"make test"}, protectedpath.Set{}, 3)
	if err != nil {
		t.Fatalf("resumedDeveloperPrompt() error = %v", err)
	}
	prompt := freshSessionRepairPrompt(repair, "be careful", "# Assigned work item\n\nID: yoyodyne-task\n", state)
	for _, want := range []string{
		"panic: test timed out after 20m0s",
		"This session is new",
		"Run: " + state.RunID,
		"Branch: " + state.Branch,
		"internal/orchestrator/pipeline.go",
		"made the pipeline wait",
		"be careful",
		"ID: yoyodyne-task",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}
}

// End to end: a run whose repair budget ran out with no developer session, a
// repair recorded about it, and the change landed by a fresh session.
func TestARepairOfASessionlessRunLandsTheChangeInAFreshSession(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopping := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("incomplete\n"), 0o600)
	}, repairVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stopping, []string{"test -f feature.txt"}), stopping)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "independent review requires repair") {
		t.Fatalf("Run() error = %v, want the repair budget spent", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The session is what the stopped run's record lacks.
	stopped.ProviderSessionID = ""
	if err := store.Save(stopped); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, pipeline.Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   worktreeRoot,
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	continuing := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, outcome.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	continuer := RepairContinuer{
		Docket:             docket,
		Runs:               store,
		Intake:             intake,
		Decisions:          store.Triage(),
		Items:              tracker,
		Worktrees:          worktrees,
		ConfiguredAttempts: pipeline.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, []string{"test -f feature.txt"}), continuing).
				Continue(ctx, workItemID, runID)
		},
	}

	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.FreshSession == "" || result.Outcome.RunID != outcome.RunID {
		t.Fatalf("result = %#v, want the same run continued in a fresh session", result)
	}
	if result.Outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the continued run did not land its change: %#v, closed = %t", result.Outcome.Integration, tracker.Closed)
	}
	developerRequests := continuing.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 {
		t.Fatalf("developer invocations = %d, want the one attempt the grant bought", len(developerRequests))
	}
	fresh := developerRequests[0]
	if fresh.SessionID != "" || fresh.WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("attempt = session %q in %q, want a fresh session in the preserved worktree %q", fresh.SessionID, fresh.WorkingDirectory, stopped.WorktreePath)
	}
	for _, want := range []string{`"message": "add the missing file"`, "This session is new", "Run: " + outcome.RunID, "yoyodyne-task"} {
		if !strings.Contains(fresh.Prompt, want) {
			t.Fatalf("fresh prompt is missing %q:\n%s", want, fresh.Prompt)
		}
	}
	if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the repaired content", integrated)
	}
	landed, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if landed.ProviderSessionID == "" || landed.RepairContinuations[len(landed.RepairContinuations)-1].FreshSession == "" {
		t.Fatalf("run = %#v, want the fresh session recorded and the continuation saying it started one", landed)
	}
}

// The harness's own continuation past a silent session keeps the developer
// session the run held, even where the continued attempt's provider reports
// none: the record is what any later repair of the run re-enters, so it must not
// lose the session in the first place.
func TestAHarnessContinuationKeepsTheSessionItContinuesUnder(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopping := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("incomplete\n"), 0o600)
	}, repairVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stopping, []string{"test -f feature.txt"}), stopping)
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() succeeded, want the repair budget spent")
	}
	stalled, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stalled.ProviderSessionID != stopping.DeveloperSession {
		t.Fatalf("session = %q, want the developer's %q on the stopped run", stalled.ProviderSessionID, stopping.DeveloperSession)
	}
	// Make the stopped run a first silent-stream stall in its developer attempt,
	// settled by the sweep, which is what the harness continues itself.
	stalled.Phase = runstate.PhaseDeveloping
	stalled.StopClass = runstate.StopProviderIdle
	stalled.Environmental = &runstate.EnvironmentalRefusal{
		Cause:        runstate.CauseProcessVanished,
		Detail:       "the harness stopped its provider because it produced no output for longer than the harness allows",
		RecordedAt:   time.Now().Add(-time.Hour),
		ProviderStop: runstate.ProviderStopStalled,
		Settled:      true,
	}
	if err := store.Save(stalled); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !stalled.HarnessContinuesStall() {
		t.Fatalf("run = %#v, want a stall the harness continues itself", stalled)
	}
	docket := &memoryDocket{}
	docketer := docketerOverStore(docket, store, pipeline.Config)
	if _, err := docketer.RecordStoppedRun(stalled); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	// The continued attempt fixes the change, and its provider reports no session.
	continuing := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	respond := continuing.Respond
	continuing.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := respond(request)
		if request.Role == domain.RoleDeveloper {
			result.SessionID = ""
		}
		return result, err
	}
	tracker.Item.Status = "in_progress"
	continuer := StallContinuer{
		Docket: docket, Redocket: docketer, Runs: store, Intake: newIntakeHoldStore(t), Items: tracker, Worktrees: worktrees,
		Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, []string{"test -f feature.txt"}), continuing).
				Continue(ctx, workItemID, runID)
		},
	}
	result, err := continuer.Continue(context.Background(), StallContinueRequest{Run: outcome.RunID})
	if err != nil || !result.Continued {
		t.Fatalf("Continue() = %#v, %v; want the harness to continue the stall", result, err)
	}
	developerRequests := continuing.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 || developerRequests[0].SessionID != stopping.DeveloperSession {
		t.Fatalf("developer requests = %#v, want one attempt continued in the run's own session", developerRequests)
	}
	continued, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if continued.ProviderSessionID != stopping.DeveloperSession {
		t.Fatalf("session after the continuation = %q, want the session %q it continued under kept on the record", continued.ProviderSessionID, stopping.DeveloperSession)
	}
	if result.Outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the continued change landed", result.Outcome)
	}
}

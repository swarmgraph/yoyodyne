package orchestrator

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run whose reviewer was still working when the harness stopped it because
// its session's total budget ran out is continued the way a silent session is:
// the sweep settles it, the docket entry names the harness as the one to move,
// and the next pull asks the review again on the same preserved change with
// nobody deciding anything and nothing counted. The stop record and the item's
// notes say it was the budget, not a silent session, and that the harness
// continued it.
func TestARunWhoseReviewRanOutOfBudgetIsContinuedLikeASilentSession(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	stateRoot := stateRootOf(store.Root())
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := providerStopBackend(0, execution.ProcessSucceeded, approveVerdict)
	approving := provider.Respond
	reviews := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			reviews++
			if reviews == 1 {
				return backend.RunResult{
					Backend: domain.BackendClaudeCode, SessionID: provider.ReviewerSession,
					IsError: true, StopReason: string(execution.ProcessTimedOut),
					Process:   execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1},
					LastEvent: request.LastSequence,
				}, nil
			}
		}
		return approving(request)
	}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	docket, err := runstate.NewDocketStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	docketer := docketerOverStore(docket, store, pipeline.Config)

	stopped, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !stopped.Paused || stopped.ProviderStop != runstate.ProviderStopBudgetExhausted {
		t.Fatalf("Run() = %#v, %v; want the review stopped because its budget ran out", stopped, err)
	}
	tracker.Item.Status = "in_progress"
	parked, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if parked.Phase != runstate.PhaseReviewing {
		t.Fatalf("stopped at phase %s, want the review", parked.Phase)
	}
	reconciler := Reconciler{
		Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store, Docket: docketer,
		Clock: &pausingClock{now: parked.UpdatedAt.Add(DefaultVanishedGrace)},
	}
	if results, err := reconciler.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("Reconcile() = %#v, %v; want the stopped run settled after the grace", results, err)
	}
	settled, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !settled.HarnessContinuesStall() {
		t.Fatalf("settled run = %#v, want a budget stop the harness continues itself", settled)
	}
	says := settled.StallStopSays()
	for _, want := range []string{"was still working when its total budget ran out", "the cause was outside the work", "the harness continues it itself"} {
		if !strings.Contains(says, want) {
			t.Fatalf("StallStopSays() = %q, want it to say %q", says, want)
		}
	}
	if strings.Contains(says, "produced no output") {
		t.Fatalf("StallStopSays() = %q, want the budget named rather than a silent session", says)
	}
	entries, err := docket.List()
	if err != nil || len(entries) != 1 || !entries[0].HarnessContinuesStall {
		t.Fatalf("docket = %#v, %v; want the stop docketed as one the harness continues", entries, err)
	}
	if rendered := entries[0].Render(); !strings.Contains(rendered, "total budget ran out") || !strings.Contains(rendered, "Next mover: the harness") {
		t.Fatalf("docket entry does not name the budget and the harness:\n%s", rendered)
	}
	countersBefore, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatal(err)
	}

	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatal(err)
	}
	intake := newIntakeHoldStore(t)
	continuer := StallContinuer{
		Docket: docket, Redocket: docketer, Runs: store, Intake: intake, Items: tracker, Worktrees: worktrees,
		Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return pipeline.Continue(ctx, workItemID, runID)
		},
	}
	holds := &pausedHolds{}
	carry := CarryOut{Docket: docket, Decisions: store.Triage(), Reruns: store.Reruns(), Runs: store, Stalls: continuer, Holds: holds}
	scheduler := Scheduler{Watching: true, Limit: 1, Open: func(ctx context.Context) (Pull, error) {
		pull, err := newScheduleHarness().open(ctx)
		pull.CarryOut = &carry
		pull.Runs = store
		pull.Intake = intake
		pull.Start = func(context.Context, string, runstate.Selection) (Outcome, error) {
			t.Error("a continuation started a fresh worker")
			return Outcome{}, nil
		}
		return pull, err
	}}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil || len(schedule.CarriedOut) != 1 || len(schedule.Started) != 1 {
		t.Fatalf("Schedule() = %s, %v; want one automatic continuation", schedule.Render(), err)
	}
	carried, continued := schedule.CarriedOut[0], schedule.Started[0].Outcome
	if !carried.Carried || carried.Decision != DecisionContinueStall || continued.RunID != stopped.RunID || continued.Blocked || continued.Paused || continued.Integration == nil {
		t.Fatalf("carried = %#v, outcome = %#v; want the same run continued at its review and integrated", carried, continued)
	}
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer attempts = %d, want the one before the stop and none after it", len(developers))
	}
	reviewRequests := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviewRequests) != 2 || reviewRequests[1].WorkingDirectory != settled.WorktreePath {
		t.Fatalf("review requests = %#v, want the review asked again on the preserved change", reviewRequests)
	}
	for _, want := range []string{"Continued after a stall", "was still working when its total budget ran out", "the cause was outside the work", "continued the run itself at the reviewing phase"} {
		if !strings.Contains(tracker.Notes, want) {
			t.Fatalf("item notes do not say %q:\n%s", want, tracker.Notes)
		}
	}
	entries, err = docket.List()
	if err != nil || len(entries) != 1 || entries[0].Closed == nil || entries[0].Closed.Decision != continuedStallDocketDecision || !strings.Contains(entries[0].Closed.DecidedBy, "total budget") {
		t.Fatalf("docket = %#v, %v; want the entry closed as continued by the harness after a budget stop", entries, err)
	}
	resumed, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RepairAttempts != settled.RepairAttempts || resumed.GrantedRepairAttempts() != settled.GrantedRepairAttempts() || resumed.HarnessStallContinuations() != 1 {
		t.Fatalf("continuation counted differently from a silent session's: before %#v, after %#v", settled, resumed)
	}
	countersAfter, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The continued review went on to approve, which records the verdict it
	// judged; everything spent or decided about the item is where it was.
	countersAfter.UpdatedAt = countersBefore.UpdatedAt
	countersAfter.LastJudged = countersBefore.LastJudged
	if !reflect.DeepEqual(countersAfter, countersBefore) {
		t.Fatalf("the item's triage record moved across the harness's continuation:\nbefore %#v\nafter  %#v", countersBefore, countersAfter)
	}
}

// A stop at review whose cause is inside the work — a reviewer's findings, a
// failing check — is not one the harness continues by itself, and nor is a
// settled stop whose recorded cause is anything but the harness's own two.
func TestAStopAtReviewForACauseInsideTheWorkIsNotContinuedByTheHarness(t *testing.T) {
	t.Parallel()

	stoppedAt := time.Date(2026, 10, 5, 22, 1, 0, 0, time.UTC)
	budgetStop := runstate.State{
		RunID: "run-" + strings.Repeat("c", 32), WorkItemID: "yoyodyne-task", Status: runstate.StatusFailed, Phase: runstate.PhaseReviewing,
		WorktreePath: "/tmp/w", Branch: "b", BaseCommit: "c", TargetBranch: "main", ProviderSessionID: "session",
		Environmental: &runstate.EnvironmentalRefusal{Cause: runstate.CauseProcessVanished, ProviderStop: runstate.ProviderStopBudgetExhausted, RecordedAt: stoppedAt, Settled: true},
	}
	if !budgetStop.HarnessContinuesStall() {
		t.Fatal("a settled budget stop at review is not continued by the harness")
	}
	for name, state := range map[string]runstate.State{
		"review findings": func() runstate.State {
			s := budgetStop
			s.Environmental = nil
			s.StopClass = runstate.StopReview
			s.ReviewFindingDetails = []runstate.Finding{{Severity: "blocker", Message: "add the missing file"}}
			return s
		}(),
		"failing check": func() runstate.State {
			s := budgetStop
			s.Environmental = nil
			s.Phase = runstate.PhaseChecking
			s.StopClass = runstate.StopChecks
			s.CheckFailure = &runstate.CheckFailure{Command: "make test"}
			return s
		}(),
		"process vanished without a harness stop": func() runstate.State {
			s := budgetStop
			s.Environmental = &runstate.EnvironmentalRefusal{Cause: runstate.CauseProcessVanished, RecordedAt: stoppedAt, Settled: true}
			return s
		}(),
	} {
		if state.SettledSilentStreamStall() || state.HarnessContinuesStall() || state.StallStopSays() != "" || state.HarnessStopSays() != "" {
			t.Errorf("%s: the harness would continue a stop whose cause is not its own: %#v", name, state)
		}
	}
}

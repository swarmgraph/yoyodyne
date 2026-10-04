package orchestrator

import (
	"context"
	"path/filepath"
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

// The two stalls of 2026-09-27 and 09-28, with the half hour turned into a
// clock: a first attempt whose provider stream went silent is stopped, left in
// flight, and settled by the sweep, and the docket entry names the harness as
// the one to move. The next pull continues it with nobody deciding anything —
// not while the operator's pause or the intake hold stands, and then in the
// same session and worktree once they are lifted — charging no attempt, round,
// grant, or re-run. The continued attempt stalls again, and the second stall is
// settled and docketed as the development manager's, saying the harness's
// continuation is spent; the harness offers nothing more.
func TestAFirstStallIsContinuedByTheHarnessAndASecondIsDocketed(t *testing.T) {
	t.Parallel()
	exerciseFirstStall(t, false, false, false)
}

func TestAFirstSilentSessionDuringRepairContinuesTheSameAttempt(t *testing.T) {
	t.Parallel()
	exerciseFirstStall(t, true, false, false)
}

func TestAWatchRestartStillContinuesAFirstSilentSessionInAGrantedRepair(t *testing.T) {
	t.Parallel()
	exerciseFirstStall(t, true, true, false)
}

func TestAFirstSilentReviewDuringRepairContinuesAtTheReview(t *testing.T) {
	t.Parallel()
	exerciseFirstStall(t, true, false, true)
}

func exerciseFirstStall(t *testing.T, repairing, restarted, reviewing bool) {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	stateRoot := filepath.Dir(filepath.Dir(filepath.Dir(store.Root())))
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The interrupted invocation and its continuation both go silent.
	stalling := providerStopBackend(2, execution.ProcessStalled, approveVerdict)
	if repairing {
		initial := providerStopBackend(0, execution.ProcessStalled, repairVerdict)
		respond := stalling.Respond
		developed := false
		reviews := 0
		stalling.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
			if reviewing {
				if request.Role == domain.RoleReviewer {
					reviews++
					if reviews > 1 {
						return backend.RunResult{
							Backend: domain.BackendClaudeCode, SessionID: initial.ReviewerSession,
							Process:   execution.ProcessResult{Status: execution.ProcessStalled, ExitCode: -1},
							LastEvent: request.LastSequence,
						}, nil
					}
				}
				return initial.Respond(request)
			}
			if request.Role == domain.RoleReviewer || !developed {
				developed = true
				return initial.Respond(request)
			}
			return respond(request)
		}
	}
	commands := []string{"exit 0"}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stalling, commands), stalling)
	docket, err := runstate.NewDocketStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	docketer := docketerOverStore(docket, store, pipeline.Config)

	initialWatch := newScheduleHarness(readyItems(tracker.Item.ID)...)
	firstSession := Scheduler{Watching: true, Limit: 1, Open: func(ctx context.Context) (Pull, error) {
		pull, err := initialWatch.open(ctx)
		pull.Runs = store
		pull.Start = func(ctx context.Context, id string, selection runstate.Selection) (Outcome, error) {
			selected := pipeline
			selected.Selection = selection
			return selected.Run(ctx, id)
		}
		return pull, err
	}}
	firstSchedule, err := firstSession.Schedule(context.Background())
	if err != nil || len(firstSchedule.Started) != 1 {
		t.Fatalf("first watch = %s, %v; want one run", firstSchedule.Render(), err)
	}
	paused := firstSchedule.Started[0].Outcome
	if !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("outcome = %#v, want the attempt stopped for a silent stream", paused)
	}
	tracker.Item.Status = "in_progress"
	if restarted {
		// The tracker-write run had a decided repair already carried out before
		// its first silent session. Reproduce those durable records; the old
		// decision must not suppress the harness's later continuation.
		state, err := store.Load(paused.RunID)
		if err != nil {
			t.Fatal(err)
		}
		decidedAt := state.StartedAt.Add(-time.Minute)
		grant, err := store.Triage().GrantRepair(context.Background(), state.WorkItemID,
			triageDecided(runstate.TriageDecisionRepair, state.RunID), 2, decidedAt,
			TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage))
		if err != nil {
			t.Fatal(err)
		}
		state.RepairAttempts = 3
		state.RepairContinuations = []runstate.RepairContinuation{{
			GrantedAttempts: grant.Rounds, Reason: "a prior decided repair", ContinuedAt: decidedAt,
		}}
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}
		// Discard the old watch's components between the silence and settlement.
		// The replacement reads only the records on disk, then opens another
		// set of components after settlement to find and fire the continuation.
		store, err = runstate.NewStore(stateRoot, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		pipeline.Store = store
	}
	settle := func() runstate.State {
		t.Helper()
		stopped, err := store.Load(paused.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		reconciler := Reconciler{
			Tracker:   tracker,
			Worktrees: newObserver(t, repository, worktreeRoot),
			Store:     store,
			Docket:    docketer,
			Clock:     &pausingClock{now: stopped.UpdatedAt.Add(DefaultVanishedGrace)},
		}
		results, err := reconciler.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if len(results) != 1 || results[0].Action != ActionBlocked {
			t.Fatalf("reconciliation = %#v, want the stalled run settled after the grace", results)
		}
		settled, err := store.Load(paused.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		return settled
	}

	// The first stall, settled: the harness continues it, and the entry says so.
	first := settle()
	if !first.SettledSilentStreamStall() || !first.HarnessContinuesStall() {
		t.Fatalf("settled run = %#v, want a first silent-stream stall the harness continues", first)
	}
	entries, err := docket.List()
	if err != nil || len(entries) != 1 || !entries[0].HarnessContinuesStall {
		t.Fatalf("docket = %#v, %v; want the stall docketed as one the harness continues", entries, err)
	}
	for _, want := range []string{"produced no output for longer than the harness allows", "the harness continues it itself", "Next mover: the harness", "continuation 1 of 1"} {
		if rendered := entries[0].Render(); !strings.Contains(rendered, want) {
			t.Fatalf("docket entry does not say %q:\n%s", want, rendered)
		}
	}
	countersBefore, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}

	if restarted {
		store, err = runstate.NewStore(stateRoot, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		docket, err = runstate.NewDocketStore(stateRoot, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		pipeline.Store = store
		docketer = docketerOverStore(docket, store, pipeline.Config)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake := newIntakeHoldStore(t)
	continuer := StallContinuer{
		Docket: docket, Redocket: docketer, Runs: store, Intake: intake, Items: tracker, Worktrees: worktrees,
		Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return pipeline.Continue(ctx, workItemID, runID)
		},
	}
	holds := &pausedHolds{held: true, hold: runstate.OperatorHold{HeldAt: time.Now()}}
	carrying := func() CarryOut {
		return CarryOut{Docket: docket, Decisions: store.Triage(), Reruns: store.Reruns(), Runs: store, Stalls: continuer, Holds: holds}
	}
	tasks, err := carrying().Outstanding()
	if err != nil || len(tasks) != 1 || tasks[0].Decision != DecisionContinueStall || tasks[0].RunID != paused.RunID {
		t.Fatalf("Outstanding() = %#v, %v; want the harness's continuation of the stall", tasks, err)
	}
	unchanged := func(gate string) {
		t.Helper()
		if again, _ := store.Load(paused.RunID); again.Status != first.Status || len(again.RepairContinuations) != len(first.RepairContinuations) {
			t.Fatalf("a continuation stopped by %s changed the run: %#v", gate, again)
		}
	}
	// The operator's pause stops it, as it stops a recorded decision's carry-out.
	if carried, _, err := carrying().Carry(context.Background(), tasks[0]); err != nil || carried.Carried || !carried.Waiting || carried.Gate != runstate.TriageGateSpendingPause {
		t.Fatalf("Carry() under the pause = %#v, %v; want it waiting on the pause", carried, err)
	}
	unchanged("the pause")
	// And so does the intake hold.
	holds.held = false
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere odd", time.Now()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if carried, _, err := carrying().Carry(context.Background(), tasks[0]); err != nil || carried.Carried || !carried.Waiting || carried.Gate != runstate.TriageGateIntakeHold {
		t.Fatalf("Carry() under the intake hold = %#v, %v; want it waiting on the hold", carried, err)
	}
	unchanged("the intake hold")
	if _, _, err := intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	// The next pull continues it, with no decision recorded, in its own session
	// and worktree — and the continued attempt stalls again.
	// Drive the scheduler's pull, rather than calling the continuation action
	// directly. A replacement watch must find this claimed item itself.
	scheduling := newScheduleHarness()
	scheduler := Scheduler{Watching: true, Limit: 1, Open: func(ctx context.Context) (Pull, error) {
		pull, err := scheduling.open(ctx)
		carry := carrying()
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
	if !carried.Carried || !continued.Paused || continued.ProviderStop != runstate.ProviderStopStalled || continued.RunID != paused.RunID {
		t.Fatalf("carried = %#v, outcome = %#v; want the same run continued and stopped for a silent stream again", carried, continued)
	}
	developer := stalling.RequestsForRole(domain.RoleDeveloper)
	wantDevelopers := 2
	if repairing && !reviewing {
		wantDevelopers++
	}
	if len(developer) != wantDevelopers || developer[len(developer)-1].SessionID != first.ProviderSessionID || developer[len(developer)-1].WorkingDirectory != first.WorktreePath {
		t.Fatalf("developer attempts = %#v, want the second in the stalled run's own session and worktree", developer)
	}
	if !strings.Contains(tracker.Notes, "Continued after a stall") {
		t.Fatalf("item notes do not record the continuation:\n%s", tracker.Notes)
	}
	entries, err = docket.List()
	if err != nil || len(entries) != 1 || entries[0].Closed == nil || entries[0].Closed.Decision != continuedStallDocketDecision {
		t.Fatalf("docket = %#v, %v; want the entry closed as continued by the harness", entries, err)
	}
	resumed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RepairAttempts != first.RepairAttempts || resumed.ReviewRounds != first.ReviewRounds || resumed.IntegrationRetries != first.IntegrationRetries || resumed.GrantedRepairAttempts() != first.GrantedRepairAttempts() || !reflect.DeepEqual(resumed.ReviewFindingDetails, first.ReviewFindingDetails) {
		t.Fatalf("continuation changed the counters or repair input: before %#v, after %#v", first, resumed)
	}
	if repairing && !reviewing && (len(first.ReviewFindingDetails) == 0 || !strings.Contains(developer[len(developer)-1].Prompt, first.ReviewFindingDetails[0].Message)) {
		t.Fatal("the continued repair did not receive its original findings")
	}
	if reviewing {
		reviews := stalling.RequestsForRole(domain.RoleReviewer)
		if len(reviews) != 3 || reviews[2].WorkingDirectory != first.WorktreePath || resumed.Phase != runstate.PhaseReviewing {
			t.Fatalf("review requests = %#v, resumed phase = %s; want the interrupted review repeated without another developer", reviews, resumed.Phase)
		}
	}
	incomplete, err := store.Incomplete()
	if err != nil || len(incomplete) != 1 || incomplete[0].RunID != first.RunID {
		t.Fatalf("incomplete runs = %#v, %v; want only the original run", incomplete, err)
	}
	countersAfter, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	countersAfter.UpdatedAt = countersBefore.UpdatedAt
	if !reflect.DeepEqual(countersAfter, countersBefore) {
		t.Fatalf("the item's triage record moved across the harness's continuation:\nbefore %#v\nafter  %#v", countersBefore, countersAfter)
	}
	if claimed, _ := store.Reruns().Claimed(tracker.Item.ID); len(claimed) != 0 {
		t.Fatalf("re-runs claimed = %#v, want none", claimed)
	}

	// The second stall, settled: docketed for the development manager, saying the
	// harness's continuation is spent, and the harness offers nothing more.
	second := settle()
	if !second.SettledSilentStreamStall() || second.HarnessContinuesStall() || second.HarnessStallContinuations() != 1 || second.RepairAttempts != first.RepairAttempts {
		t.Fatalf("second settled run = %#v, want a stall whose one harness continuation is spent, with no attempt charged", second)
	}
	entries, err = docket.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("second docket = %#v, %v", entries, err)
	}
	latest := entries[0]
	if latest.HarnessContinuesStall {
		t.Fatalf("second entry = %#v, want it the development manager's", latest)
	}
	rendered := latest.Render()
	wants := []string{"its continuation is spent", "development manager's decision", "Next mover: you"}
	if !repairing {
		wants = append(wants, "yoyo triage repair "+paused.RunID)
	}
	for _, want := range wants {
		if !strings.Contains(rendered, want) {
			t.Fatalf("second entry does not say %q:\n%s", want, rendered)
		}
	}
	if tasks, err := carrying().Outstanding(); err != nil || len(tasks) != 0 {
		t.Fatalf("Outstanding() after the second stall = %#v, %v; want nothing the harness continues", tasks, err)
	}
}

// A stall in a run a session re-adopted after a redeploy stop says so on the
// entry: that it began in the session the re-adoption resumed where it stalled
// at the phase it was re-adopted at, and only that it followed the re-adoption
// where it stalled later.
func TestAStallAfterARedeployReadoptionSaysSo(t *testing.T) {
	t.Parallel()

	stoppedAt := time.Date(2026, 9, 28, 18, 40, 0, 0, time.UTC)
	state := runstate.State{
		RunID: "run-" + strings.Repeat("b", 32), WorkItemID: "yoyodyne-task", Status: runstate.StatusFailed, Phase: runstate.PhaseDeveloping,
		WorktreePath: "/tmp/w", Branch: "b", BaseCommit: "c", TargetBranch: "main", ProviderSessionID: "session",
		Environmental: &runstate.EnvironmentalRefusal{Cause: runstate.CauseProcessVanished, ProviderStop: runstate.ProviderStopStalled, RecordedAt: stoppedAt, Settled: true},
		Readopted:     &runstate.RedeployStop{At: stoppedAt, Phase: runstate.PhaseDeveloping, BoundSeconds: 900},
	}
	if !state.HarnessContinuesStall() {
		t.Fatal("a first settled stall is not continued by the harness")
	}
	for _, want := range []string{"stopped for a redeploy at its developing phase at 2026-09-28T18:40:00Z", "re-adopted", "this stall began in the session that re-adoption resumed"} {
		if says := state.StallStopSays(); !strings.Contains(says, want) {
			t.Fatalf("StallStopSays() = %q, want it to say %q", says, want)
		}
	}
	// Re-adopted at its developer attempt and stalled later, in its review: the
	// stalled invocation is not the session the re-adoption resumed, and the
	// sentence does not say it was.
	state.Phase = runstate.PhaseReviewing
	says := state.StallStopSays()
	if strings.Contains(says, "began in the session that re-adoption resumed") || !strings.Contains(says, "before it went on to the reviewing phase it stalled in") {
		t.Fatalf("StallStopSays() = %q, want the re-adoption named without claiming the review stalled in the resumed session", says)
	}
	state.Phase = runstate.PhaseDeveloping
	// A first stall inside a repair continues the attempt already underway.
	state.RepairAttempts = 1
	if !state.SettledSilentStreamStall() || !state.HarnessContinuesStall() {
		t.Fatal("a first stall inside the repair loop is not continued by the harness")
	}
}

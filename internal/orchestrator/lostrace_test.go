package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
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

// movingTargetReviewer wraps a provider so that each of the first races reviews
// lands somebody else's work on the target branch as the verdict is given. That
// is the window a busy main opens: the change is approved, and by the time it is
// promoted the target is somewhere else.
func movingTargetReviewer(t *testing.T, repository string, provider *orchestratortest.Backend, races int) {
	t.Helper()
	inner := provider.Respond
	reviews := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := inner(request)
		if err != nil || request.Role != domain.RoleReviewer {
			return result, err
		}
		reviews++
		if reviews <= races {
			name := "elsewhere-" + strconv.Itoa(reviews) + ".txt"
			writePipelineFile(t, repository, name, "somebody else's work\n")
			runPipelineGit(t, repository, "add", name)
			runPipelineGit(t, repository, "commit", "-m", "concurrent target change "+strconv.Itoa(reviews))
		}
		return result, err
	}
}

func implementsFeature(request backend.RunRequest) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
}

// A run that loses its race four times, with every replay passing its checks and
// drawing an approval, lands — whatever the integration budget is, zero
// included. Losing the race says nothing about the change, so it spends nothing
// that ends in triage: nothing is blocked, nothing is docketed, and no decision
// is asked of anybody.
func TestARunWhoseReplaysKeepPassingLandsThroughFourLostRaces(t *testing.T) {
	t.Parallel()

	// Two is the default, which four lost races exceed twice over: under the rule
	// this replaced, the third race blocked the item. Zero permits no replay to
	// stop on the change, and a replay that passes is not one.
	for _, budget := range []int{2, 0} {
		t.Run("budget "+strconv.Itoa(budget), func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
			movingTargetReviewer(t, repository, provider, 4)
			pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
			pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = budget

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
				t.Fatalf("Run() outcome = %#v, want the change landed and the item closed", outcome)
			}
			if outcome.Blocked || tracker.Blocked {
				t.Fatalf("blocked = %t / %t (%q), want a lost race never to block the item", outcome.Blocked, tracker.Blocked, tracker.BlockReason)
			}
			if outcome.IntegrationRetries != 4 || outcome.ChargedReplays != 0 {
				t.Fatalf("lost races = %d, charged replays = %d; want four races recorded and none charged", outcome.IntegrationRetries, outcome.ChargedReplays)
			}
			// Every replay re-earned the gate: a fresh independent review each time,
			// and the developer never asked for anything.
			if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 5 {
				t.Fatalf("reviewer invocations = %d, want the first verdict and one per replay", len(reviews))
			}
			if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
				t.Fatalf("developer invocations = %d, want 1", len(developers))
			}
			for race := 1; race <= 4; race++ {
				if _, err := os.Stat(filepath.Join(repository, "elsewhere-"+strconv.Itoa(race)+".txt")); err != nil {
					t.Fatalf("main lost the work that won race %d: %v", race, err)
				}
			}

			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if state.IntegrationRetries != 4 || state.ChargedReplays != 0 || state.Blocker != "" {
				t.Fatalf("durable record: races %d, charged %d, blocker %q", state.IntegrationRetries, state.ChargedReplays, state.Blocker)
			}
			// The replay that landed passed its gate, so nothing is left waiting to
			// be charged on a run that has already promoted.
			if state.ReplayUnjudged {
				t.Fatal("replay_unjudged is still set on a run that landed")
			}
			record, err := store.Triage().Counters(tracker.Item.ID)
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if len(record.Decisions) != 0 || record.ReviewRounds != 0 {
				t.Fatalf("triage record = %#v, want no decision recorded and no round charged", record)
			}
		})
	}
}

// scriptedRaces moves the target branch as the reviewer gives the verdicts
// numbered in moves, so a test can say which approvals lose their race.
func scriptedRaces(t *testing.T, repository string, provider *orchestratortest.Backend, moves ...int) {
	t.Helper()
	inner := provider.Respond
	reviews := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := inner(request)
		if err != nil || request.Role != domain.RoleReviewer {
			return result, err
		}
		reviews++
		for _, move := range moves {
			if move == reviews {
				name := "elsewhere-" + strconv.Itoa(reviews) + ".txt"
				writePipelineFile(t, repository, name, "somebody else's work\n")
				runPipelineGit(t, repository, "add", name)
				runPipelineGit(t, repository, "commit", "-m", "concurrent target change")
			}
		}
		return result, err
	}
}

// What the budget bounds is a replay that stops on the change, and it is
// enforced there rather than at a lost race. With a budget of one, the first
// replay draws a repair verdict, is charged, and is repaired like any repair;
// the repaired change is approved and loses its race again, which costs
// nothing; the second replay draws a repair verdict too, and that charge is the
// one past the budget, so the run stops there — on the change, with the repair
// verdict as its reason — rather than at the race.
func TestAReplayThatStopsOnTheChangePastTheBudgetStopsThere(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// Verdicts: approve (loses race 1), repair (replay 1 charged), approve (loses
	// race 2), repair (replay 2 charged past the budget).
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict, repairVerdict, approveVerdict, repairVerdict)
	scriptedRaces(t, repository, provider, 1, 3)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 1
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 5

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.StopIntegrationBudget)
	if outcome.StopClass != runstate.StopIntegrationBudget {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.StopIntegrationBudget)
	}
	if err == nil || !strings.Contains(err.Error(), "2 of 1 permitted replay stop(s) spent") || !strings.Contains(err.Error(), "independent review requires repair") {
		t.Fatalf("Run() error = %v, want the second charged replay to stop the run on its repair verdict", err)
	}
	if !outcome.Blocked || outcome.Integration != nil {
		t.Fatalf("Run() outcome = %#v, want a blocked run with nothing promoted", outcome)
	}
	if !strings.Contains(tracker.BlockReason, "Replays that stopped on the change: 2 of 1 permitted") ||
		!strings.Contains(tracker.BlockReason, "Races lost to the moving target: 2") {
		t.Fatalf("blocker = %q", tracker.BlockReason)
	}
	// The first charged replay was repaired, and the second was not handed back.
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and the one repair the first charged replay bought", len(developers))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != 2 || state.ChargedReplays != 2 || state.ReplayUnjudged {
		t.Fatalf("durable record: races %d, charged %d, unjudged %t", state.IntegrationRetries, state.ChargedReplays, state.ReplayUnjudged)
	}
}

// At a budget of zero no replay may stop on the change: the first replay handed
// back for a failing check ends the run there, on the check.
func TestAtBudgetZeroTheFirstReplayThatFailsItsChecksStopsTheRun(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
	scriptedRaces(t, repository, provider, 1)
	// The check passes on the first attempt and fails on every run after it, so
	// the replayed change is the one that fails.
	counter := filepath.Join(t.TempDir(), "checks")
	check := "echo x >> " + strconv.Quote(counter) + " && test $(wc -l < " + strconv.Quote(counter) + ") -le 1"
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{check})
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 0

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.StopIntegrationBudget)
	if outcome.StopClass != runstate.StopIntegrationBudget {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.StopIntegrationBudget)
	}
	if err == nil || !strings.Contains(err.Error(), "1 of 0 permitted replay stop(s) spent") {
		t.Fatalf("Run() error = %v, want the failing replay to stop the run", err)
	}
	if !outcome.Blocked || outcome.Integration != nil {
		t.Fatalf("Run() outcome = %#v, want a blocked run with nothing promoted", outcome)
	}
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer invocations = %d, want nothing handed back", len(developers))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != 1 || state.ChargedReplays != 1 {
		t.Fatalf("durable record: races %d, charged %d", state.IntegrationRetries, state.ChargedReplays)
	}
}

// A run whose change keeps losing the race stops at the race that makes
// lostRacesBeforeGivingBackTheSlot rather than replaying it: the run is no
// longer in flight, so its developer slot is free; its branch and worktree are
// kept with the change in them; and the item is blocked and listed for the
// development manager with how many races it lost and that its work is kept.
// One race short of the bound, the same change lands as it always did.
func TestARunThatKeepsLosingTheRaceGivesBackItsSlotWithItsWorkKept(t *testing.T) {
	t.Parallel()

	t.Run("loses more races than the bound", func(t *testing.T) {
		t.Parallel()

		repository := pipelineRepository(t)
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
		provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
		movingTargetReviewer(t, repository, provider, lostRacesBeforeGivingBackTheSlot+2)
		pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
		docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
		if err != nil {
			t.Fatalf("NewDocketStore() error = %v", err)
		}
		pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		lost := strconv.Itoa(lostRacesBeforeGivingBackTheSlot) + " times"
		if err == nil || !strings.Contains(err.Error(), runstate.ContendedIntegrationFailure+" "+lost) {
			t.Fatalf("Run() error = %v, want the run stopped at its lost race number %d", err, lostRacesBeforeGivingBackTheSlot)
		}
		if outcome.Integration != nil || tracker.Closed {
			t.Fatalf("Run() outcome = %#v, want nothing promoted", outcome)
		}
		assertSavedStopClass(t, store, outcome.RunID, runstate.StopLostRaces)
		// Other work landing first refused the change, so the stop says nothing
		// about the work and the hold on new work leaves it out of its count.
		if !(Started{Failure: err.Error(), Outcome: outcome}).stoppedOutsideTheWork() {
			t.Fatalf("a run that lost its race %s is counted towards the hold on new work", lost)
		}
		// The race at the bound is not replayed: one review per race lost, and the
		// developer never asked for anything.
		if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != lostRacesBeforeGivingBackTheSlot {
			t.Fatalf("reviewer invocations = %d, want one per race lost and no replay after the last", len(reviews))
		}
		if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
			t.Fatalf("developer invocations = %d, want 1", len(developers))
		}

		// The slot: the run is terminal and nothing is left in flight to hold one.
		state, err := store.Load(outcome.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state.Status.InFlight() || state.CompletedAt == nil {
			t.Fatalf("status = %q, want a terminal run holding no slot", state.Status)
		}
		incomplete, err := store.Incomplete()
		if err != nil {
			t.Fatalf("Incomplete() error = %v", err)
		}
		if len(incomplete) != 0 {
			t.Fatalf("runs in flight = %d, want the run's slot given back", len(incomplete))
		}
		if !state.LostItsRace() {
			t.Fatalf("failure = %q, want it read as a run that lost its race", state.Failure)
		}
		if state.IntegrationRetries != lostRacesBeforeGivingBackTheSlot-1 || state.ChargedReplays != 0 {
			t.Fatalf("durable record: replays %d, charged %d", state.IntegrationRetries, state.ChargedReplays)
		}

		// The work: the branch and worktree are kept, with the change in them.
		if state.WorktreeRemoved || state.BranchRemoved {
			t.Fatalf("worktree removed %t, branch removed %t; want both kept", state.WorktreeRemoved, state.BranchRemoved)
		}
		if _, err := os.Stat(filepath.Join(state.WorktreePath, "feature.txt")); err != nil {
			t.Fatalf("the change is not in the kept worktree: %v", err)
		}
		if shown := gitOutput(t, repository, "show", state.Branch+":feature.txt"); !strings.Contains(shown, "implemented") {
			t.Fatalf("branch %s does not carry the change: %q", state.Branch, shown)
		}

		// The listing: the item is blocked, and the development manager's list
		// carries the run with the reason in plain words.
		reason := "lost the race to land on main " + lost
		if !tracker.Blocked || !strings.Contains(tracker.BlockReason, reason) || !strings.Contains(tracker.BlockReason, "Its work is kept.") {
			t.Fatalf("blocked = %t, blocker = %q; want the races lost and the work kept", tracker.Blocked, tracker.BlockReason)
		}
		entries, err := docket.List()
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(entries) != 1 || entries[0].RunID != outcome.RunID {
			t.Fatalf("docket = %#v, want the run listed once", entries)
		}
		if !strings.Contains(entries[0].Blocker, reason) || !strings.Contains(entries[0].Blocker, "Its work is kept.") {
			t.Fatalf("listed reason = %q, want the races lost and the work kept", entries[0].Blocker)
		}
	})

	t.Run("wins within the bound", func(t *testing.T) {
		t.Parallel()

		repository := pipelineRepository(t)
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
		provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
		movingTargetReviewer(t, repository, provider, lostRacesBeforeGivingBackTheSlot-1)
		pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed || tracker.Blocked {
			t.Fatalf("Run() outcome = %#v, want the change landed and the item closed", outcome)
		}
		if outcome.IntegrationRetries != lostRacesBeforeGivingBackTheSlot-1 {
			t.Fatalf("lost races = %d, want %d", outcome.IntegrationRetries, lostRacesBeforeGivingBackTheSlot-1)
		}
	})
}

// An approval carrying one minor note the reviewer put out of scope, which is
// the verdict the run in yoyodyne-gle stopped on: its note is what sent the
// repair to the developer on the old base.
const approveWithOutOfScopeNoteVerdict = `{"decision":"approve","approves":"implementation","summary":"the change matches the acceptance criteria","findings":[{"severity":"minor","disposition":"out_of_scope","message":"word the header so it covers this case too","location":{"file":"feature.txt","line":1}}]}`

// yoyodyne-gle: a run that gave back its slot after losing the race to land,
// whose target then moved again while it was stopped, is repaired by bringing
// its change up to date before anything else. The repair puts it back at its
// promotion, the promotion replays the change onto the target as it now stands
// without counting that as a race, the checks run on the replayed change, a
// fresh review approves it, and it lands — with no developer invoked, and with
// the races the stopped stint lost no longer counted against it.
func TestARepairOfARunThatLostItsRaceReplaysItOntoTheTargetBeforeItsChecksAndLands(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	build := func(provider *orchestratortest.Backend, checks ...string) Pipeline {
		return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, checks), provider)
	}
	stopping := orchestratortest.RoleBackend(implementsFeature, approveWithOutOfScopeNoteVerdict)
	movingTargetReviewer(t, repository, stopping, lostRacesBeforeGivingBackTheSlot)
	pipeline := build(stopping, "test -f feature.txt")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), runstate.ContendedIntegrationFailure) {
		t.Fatalf("Run() error = %v, want the run stopped on its lost races", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.LostItsRace() || stopped.IntegrationRetries != lostRacesBeforeGivingBackTheSlot-1 {
		t.Fatalf("stopped run: lost its race %t, races %d; want it stopped on its last permitted race", stopped.LostItsRace(), stopped.IntegrationRetries)
	}

	// Other work lands while the run is stopped, so the base it stopped on is
	// further behind still by the time the repair is carried out.
	writePipelineFile(t, repository, "while-stopped.txt", "landed while the run was stopped\n")
	runPipelineGit(t, repository, "add", "while-stopped.txt")
	runPipelineGit(t, repository, "commit", "-m", "work that landed while the run was stopped")

	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, pipeline.Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, outcome.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	continuing := orchestratortest.RoleBackend(func(backend.RunRequest) error {
		return errors.New("the developer was invoked for a repair that only brings the change up to date")
	}, approveVerdict)
	continuer := RepairContinuer{
		Docket: docket, Runs: store, Intake: intake, Decisions: store.Triage(), Items: tracker, Worktrees: worktrees,
		ConfiguredAttempts: pipeline.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			// The check passes only on a change that sits on the target as it now
			// stands, so a gate run on the old base would fail it.
			return build(continuing, "test -f feature.txt && test -f while-stopped.txt").Continue(ctx, workItemID, runID)
		},
	}

	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || !result.Replay || !result.LostRace || result.ResumesAt != runstate.PhaseIntegrating {
		t.Fatalf("result = %#v, want the run put back at its promotion to be brought up to date", result)
	}
	if result.Outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("outcome = %#v, closed = %t; want the replayed change landed", result.Outcome, tracker.Closed)
	}
	if developers := continuing.RequestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("developer invocations = %d, want none", len(developers))
	}
	if reviews := continuing.RequestsForRole(domain.RoleReviewer); len(reviews) != 1 {
		t.Fatalf("reviewer invocations = %d, want one fresh review of the replayed change", len(reviews))
	}
	for _, landed := range []string{"feature.txt", "while-stopped.txt", "elsewhere-1.txt"} {
		if _, err := os.Stat(filepath.Join(repository, landed)); err != nil {
			t.Fatalf("main is missing %s after the landing: %v", landed, err)
		}
	}

	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.BaseCommit == stopped.BaseCommit {
		t.Fatalf("recorded base is still %s, want the change replayed onto the target", state.BaseCommit)
	}
	// The stale base the repair found is not a race the run lost, and the races
	// the stopped stint lost were cleared with the stop they caused.
	if state.IntegrationRetries != 0 || result.Outcome.IntegrationRetries != 0 {
		t.Fatalf("races recorded = %d (outcome %d), want none", state.IntegrationRetries, result.Outcome.IntegrationRetries)
	}
	if state.RepairAttempts != stopped.RepairAttempts {
		t.Fatalf("repair attempts = %d, want %d: bringing the change up to date costs no attempt", state.RepairAttempts, stopped.RepairAttempts)
	}
	if !strings.Contains(state.RepairContinuations[len(state.RepairContinuations)-1].Reason, "lost the race to land") {
		t.Fatalf("continuation reason = %q, want it to say the run lost its race", state.RepairContinuations[len(state.RepairContinuations)-1].Reason)
	}
}

// A landing attempt refused because main moved once is followed by one prepared
// on main as it stands after the move, not on the main the first attempt saw:
// the change is replayed onto the new tip, checked and reviewed again there,
// and lands on it.
func TestTheLandingAttemptAfterMainMovesOnceIsPreparedOnTheNewTipAndLands(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
	scriptedRaces(t, repository, provider, 1)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	original := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "main"))

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("Run() outcome = %#v, want the change landed and the item closed", outcome)
	}
	moved := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "main~1"))
	if moved == original {
		t.Fatal("main never moved, so the test exercised nothing")
	}
	if outcome.IntegrationRetries != 1 {
		t.Fatalf("lost races = %d, want the one move", outcome.IntegrationRetries)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.BaseCommit != moved || state.Integration == nil || state.Integration.PreviousTargetCommit != moved {
		t.Fatalf("second attempt prepared on %s and landed onto %v; want both on the new tip %s (the first saw %s)",
			state.BaseCommit, state.Integration, moved, original)
	}
	if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 2 {
		t.Fatalf("reviewer invocations = %d, want the first verdict and one on the replayed change", len(reviews))
	}
}

// yoyodyne-ifd.428.99.16: races lost in a stint that has ended are not counted
// against the next one. A run loses four races, then stops on something else
// and is repaired by the development manager's decision; when main moves once
// after the repaired change is approved, the run replays it onto the new tip
// and lands, rather than reading that one move as its fifth lost race and
// giving up without replaying.
func TestARepairedRunWhoseTargetMovesOnceReplaysAndLandsWhateverItsEarlierStintLost(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	build := func(provider *orchestratortest.Backend, checks ...string) Pipeline {
		pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, checks), provider)
		pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 0
		return pipeline
	}
	// Four approvals that each lose their race, then a repair verdict on the
	// fourth replay, which at a budget of zero stops the run on the change.
	stopping := orchestratortest.RoleBackend(implementsFeature, approveVerdict, approveVerdict, approveVerdict, approveVerdict, repairVerdict)
	scriptedRaces(t, repository, stopping, 1, 2, 3, 4)
	pipeline := build(stopping, "test -f feature.txt")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the run stopped on its repair verdict")
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.LostItsRace() || stopped.IntegrationRetries != lostRacesBeforeGivingBackTheSlot-1 {
		t.Fatalf("stopped run: lost its race %t, races %d, failure %q; want it stopped on the change with %d races lost",
			stopped.LostItsRace(), stopped.IntegrationRetries, stopped.Failure, lostRacesBeforeGivingBackTheSlot-1)
	}

	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, pipeline.Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, outcome.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	// The repaired change is approved, main moves once as it is, and the replay
	// onto the new tip is approved too.
	continuing := orchestratortest.RoleBackend(implementsFeature, approveVerdict, approveVerdict)
	var moved string
	inner := continuing.Respond
	reviews := 0
	continuing.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := inner(request)
		if err != nil || request.Role != domain.RoleReviewer {
			return result, err
		}
		if reviews++; reviews == 1 {
			writePipelineFile(t, repository, "after-the-repair.txt", "somebody else's work\n")
			runPipelineGit(t, repository, "add", "after-the-repair.txt")
			runPipelineGit(t, repository, "commit", "-m", "main moves once after the repair")
			moved = strings.TrimSpace(gitOutput(t, repository, "rev-parse", "main"))
		}
		return result, err
	}
	continuer := RepairContinuer{
		Docket: docket, Runs: store, Intake: intake, Decisions: store.Triage(), Items: tracker, Worktrees: worktrees,
		ConfiguredAttempts: pipeline.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return build(continuing, "test -f feature.txt").Continue(ctx, workItemID, runID)
		},
	}

	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.Outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("result = %#v, closed = %t; want the repaired change replayed onto the new tip and landed", result, tracker.Closed)
	}
	if reviews != 2 {
		t.Fatalf("reviewer invocations = %d, want the repaired change's verdict and one on its replay", reviews)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.BaseCommit != moved || state.Integration == nil || state.Integration.PreviousTargetCommit != moved {
		t.Fatalf("landing prepared on %s onto %v; want it on the tip main moved to, %s", state.BaseCommit, state.Integration, moved)
	}
	if state.IntegrationRetries != 1 {
		t.Fatalf("races recorded = %d, want only the one lost since the repair", state.IntegrationRetries)
	}
}

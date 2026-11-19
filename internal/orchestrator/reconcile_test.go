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
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func newObserver(t *testing.T, repository, worktreeRoot string) ReconcileWorktrees {
	t.Helper()
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          worktreeRoot,
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
		Timeout:               testGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	return worktrees
}

// reconcileSweep runs exactly one reconciliation over the durable state a
// restarted process would find.
func reconcileSweep(t *testing.T, repository, worktreeRoot string, store ReconcileStore, tracker WorkTracker) []Reconciliation {
	t.Helper()
	worktrees := newObserver(t, repository, worktreeRoot)
	results, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	return results
}

// haltingStore stops accepting writes for good once a run reaches a chosen
// phase, after letting `after` writes through there. Nothing the run does
// afterwards survives, including the terminal failure record it would normally
// write, so what is left on disk is what a killed process leaves.
type haltingStore struct {
	StateStore
	at     runstate.Phase
	after  int
	seen   int
	halted bool
}

func (s *haltingStore) Save(state runstate.State) error {
	if !s.halted && state.Phase == s.at {
		if s.seen >= s.after {
			s.halted = true
		} else {
			s.seen++
		}
	}
	if s.halted {
		return errors.New("state store is unavailable")
	}
	return s.StateStore.Save(state)
}

// The other run of 2026-09-23, and the one nothing could carry on: a stall in
// the run's first attempt, with the developer session preserved and no failure
// ever returned. The sweep settles and dockets it after the half hour, the entry
// says the session is resumable, and the repair the development manager records
// is carried out as a continuation of that session at the point it stalled
// rather than refused for want of a repair input. Before this the refusal left
// a re-run as the only decision that could be carried out, and a re-run starts
// over from the target branch with the session gone and the uncommitted work in
// the preserved worktree gone with it.
func TestARepairContinuesAFirstAttemptStallInItsOwnSession(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first developer attempt writes half of the change and is then stopped
	// on time, which is the whole of what a stall leaves: a session, a worktree
	// with uncommitted work in it, and nothing anybody judged.
	stalling := providerStopBackend(1, execution.ProcessStalled, approveVerdict)
	checks := []string{"exit 0"}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stalling, checks), stalling)
	paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("Run() error = %v, outcome = %#v, want the first attempt stopped on time", err, paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Nothing was handed back, which is what used to refuse the repair: no
	// findings, no failing check, no refused paths, and no attempt spent.
	if stopped.RepairAttempts != 0 || stopped.CheckFailure != nil || len(stopped.ReviewFindingDetails) != 0 || stopped.PathRefusal != nil {
		t.Fatalf("stopped run = %#v, want a first attempt with nothing returned to its developer", stopped)
	}
	if stopped.ProviderSessionID == "" {
		t.Fatalf("stopped run = %#v, want the developer session it stalled in preserved", stopped)
	}
	tracker.Item.Status = "in_progress"

	docket := &memoryDocket{}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
		Clock:     &pausingClock{now: stopped.UpdatedAt.Add(DefaultVanishedGrace)},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the stalled run settled after the grace", results)
	}

	// The entry carries the fact the decision turns on: the session is there and
	// can simply be carried on, and the continuation costs the item neither a
	// round nor an attempt.
	if len(docket.entries) != 1 {
		t.Fatalf("docket = %#v, want the stalled run docketed once", docket.entries)
	}
	entry := docket.entries[0]
	if !entry.SessionResumable || entry.Artifacts.DeveloperSession != stopped.ProviderSessionID {
		t.Fatalf("entry = %#v, want the preserved session named and reported resumable", entry)
	}
	for _, want := range []string{
		"Nothing was judged",
		"the session it stopped in is preserved",
		"yoyo triage repair " + paused.RunID,
		"spends no review round and no repair attempt",
	} {
		if !strings.Contains(entry.Render(), want) {
			t.Fatalf("entry does not say %q:\n%s", want, entry.Render())
		}
	}

	// The development manager records a repair about that stoppage, exactly as
	// the conversation records one.
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, paused.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	roundsBefore, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	// What the continued developer finds is asked where it is handed over rather
	// than afterwards: a run that lands removes its worktree on purpose, so the
	// only moment the half-written work can be proved to have survived is the
	// moment the attempt it belongs to is resumed.
	survived := false
	continuing := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if _, err := os.Stat(filepath.Join(request.WorkingDirectory, "partial.txt")); err == nil {
			survived = true
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
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
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, checks), continuing).
				Continue(ctx, workItemID, runID)
		},
	}
	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: paused.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || !result.Stall || result.Outcome.RunID != paused.RunID || result.Outcome.Integration == nil {
		t.Fatalf("result = %#v, want the stalled run carried on and its change landed", result)
	}

	// The same session, in the same worktree, with the half-written work still
	// in it.
	continued := continuing.RequestsForRole(domain.RoleDeveloper)
	if len(continued) != 1 || continued[0].SessionID != stopped.ProviderSessionID || continued[0].WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("continued attempts = %#v, want one, in the stalled run's own session and worktree", continued)
	}
	if !survived {
		t.Fatal("the stalled attempt's uncommitted work was not in the worktree the continuation was handed")
	}

	// And a stall judged nothing, so the continuation is charged nothing: no
	// repair attempt on the run, and no review round on the item beyond the one
	// the continued run's own verdict bought.
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(landed.RepairContinuations) != 1 || !landed.RepairContinuations[0].Stall {
		t.Fatalf("continuations = %#v, want the one continuation recorded as a stall", landed.RepairContinuations)
	}
	if landed.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want a stall to count none", landed.RepairAttempts)
	}
	roundsAfter, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if roundsAfter.ReviewRounds != roundsBefore.ReviewRounds {
		t.Fatalf("review rounds = %d, want the %d the item stood at: an approving verdict charges none and a stall judges nothing",
			roundsAfter.ReviewRounds, roundsBefore.ReviewRounds)
	}
}

// The same stall one step later: a first attempt that completed, passed its
// checks, and whose reviewer the harness then stopped on time. The sweep settles
// and dockets it with nothing handed back, exactly as it does a stall in the
// attempt — and before this the repair was refused, because only a stall in the
// developing phase was continuable, so a re-run discarding the finished branch
// was the only decision left. The entry now names the review as where the
// repair continues it, and the carry-out asks the review again on the change the
// run already has: no developer is invoked, and the branch the attempt produced
// is the one that lands.
func TestARepairContinuesAFirstAttemptStalledInItsReviewAtTheReview(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stalling := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	stalling.Respond = stoppingReviewer(stalling.Respond, stalling.ReviewerSession)
	checks := []string{"test -f feature.txt"}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stalling, checks), stalling)
	paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("Run() error = %v, outcome = %#v, want the review stopped on time", err, paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.Phase != runstate.PhaseReviewing || stopped.RepairAttempts != 0 || handedBackRepair(stopped) {
		t.Fatalf("stopped run = %#v, want a first attempt stopped at its review with nothing handed back", stopped)
	}
	tracker.Item.Status = "in_progress"

	docket := &memoryDocket{}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
		Clock:     &pausingClock{now: stopped.UpdatedAt.Add(DefaultVanishedGrace)},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the stalled review settled after the grace", results)
	}
	settled, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Environmental == nil || settled.Environmental.Cause != runstate.CauseProcessVanished || settled.Phase != runstate.PhaseReviewing {
		t.Fatalf("settled run = %#v, want a vanished-process stoppage at the review", settled)
	}

	// The entry says the repair continues the run at its review. A first stall
	// is the harness's to continue itself (yoyodyne-a0s), so the harness is the
	// next mover; a repair she records first is what is carried out instead.
	if len(docket.entries) != 1 {
		t.Fatalf("docket = %#v, want the stalled review docketed once", docket.entries)
	}
	entry := docket.entries[0]
	if !entry.SessionResumable || entry.ResumesAt != string(runstate.PhaseReviewing) {
		t.Fatalf("entry = %#v, want the stall reported resumable at the review", entry)
	}
	rendered := entry.Render()
	for _, want := range []string{
		"`yoyo triage repair " + paused.RunID + "` continues the run at the reviewing phase",
		"with no developer attempt",
		"Next mover: the harness",
		"continues the run itself at the reviewing phase it stalled in",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("entry does not say %q:\n%s", want, rendered)
		}
	}

	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, paused.RunID),
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
	// What the reviewer is shown is asked where it is handed over: a run that
	// lands removes its worktree, so the moment the review is asked again is the
	// only moment the attempt's change can be proved to be what it judges.
	reviewedTheAttempt := false
	continuing := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return errors.New("a stall at the review is owed no developer attempt")
	}, approveVerdict)
	reviewing := continuing.Respond
	continuing.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer && request.WorkingDirectory == stopped.WorktreePath {
			if content, err := os.ReadFile(filepath.Join(request.WorkingDirectory, "feature.txt")); err == nil && string(content) == "implemented\n" {
				reviewedTheAttempt = true
			}
		}
		return reviewing(request)
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
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, checks), continuing).
				Continue(ctx, workItemID, runID)
		},
	}
	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: paused.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || !result.Stall || result.ResumesAt != runstate.PhaseReviewing {
		t.Fatalf("result = %#v, want the stall continued at the review", result)
	}
	if result.Outcome.RunID != paused.RunID || result.Outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("result = %#v, closed = %t, want the same run's change landed", result, tracker.Closed)
	}
	if !strings.Contains(result.Reason, "continued at the reviewing phase") || !strings.Contains(result.Render(), "at the reviewing phase, the step it stalled in") {
		t.Fatalf("result does not say it was continued at the review:\nreason: %s\n%s", result.Reason, result.Render())
	}

	// No developer attempt, and the review asked again on the attempt's change in
	// the preserved worktree.
	if developers := continuing.RequestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("developer invocations = %#v, want none for a stall at the review", developers)
	}
	if reviews := continuing.RequestsForRole(domain.RoleReviewer); len(reviews) != 1 {
		t.Fatalf("review invocations = %d, want the review asked again once", len(reviews))
	}
	if !reviewedTheAttempt {
		t.Fatal("the review was not asked again on the completed attempt's change in the preserved worktree")
	}

	// The branch kept is the one that landed, and the continuation counted no
	// attempt.
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if landed.Branch != stopped.Branch || landed.WorktreePath != stopped.WorktreePath || landed.BaseCommit != stopped.BaseCommit {
		t.Fatalf("landed run = %#v, want the stalled run's own branch and worktree carried through", landed)
	}
	if landed.RepairAttempts != 0 || len(landed.RepairContinuations) != 1 || !landed.RepairContinuations[0].Stall {
		t.Fatalf("landed run = %#v, want one stall continuation and no repair attempt", landed)
	}
	if landed.Environmental != nil || landed.Blocker != "" {
		t.Fatalf("landed run = %#v, want the settled stoppage superseded", landed)
	}
}

// The checks-phase companion: a first attempt stopped at its checks with
// nothing handed back is continued at the checks, which run again on the
// change the attempt left in the preserved worktree before the review is asked,
// with no developer invoked. The sweep settles a provider stop only from the
// developing or reviewing phase, because nothing else invokes a provider, so the
// settled record is moved to the checking phase by hand; what is under test is
// what the docket says of it and what the carry-out does with it.
func TestARepairContinuesAFirstAttemptStalledAtItsChecksAtTheChecks(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stalling := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	stalling.Respond = stoppingReviewer(stalling.Respond, stalling.ReviewerSession)
	checksLog := filepath.Join(t.TempDir(), "checks.log")
	checks := []string{"test -f feature.txt && echo ran >> " + checksLog}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stalling, checks), stalling)
	paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("Run() error = %v, outcome = %#v, want the run stopped on time", err, paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	tracker.Item.Status = "in_progress"

	docket := &memoryDocket{}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
		Clock:     &pausingClock{now: stopped.UpdatedAt.Add(DefaultVanishedGrace)},
	}
	if results, err := reconciler.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("Reconcile() = %#v, %v, want the stall settled", results, err)
	}
	settled, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	settled.Phase = runstate.PhaseChecking
	if err := store.Save(settled); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if len(docket.entries) != 1 {
		t.Fatalf("docket = %#v, want the stall docketed once", docket.entries)
	}
	entry := docket.entries[0]
	entry.ResumesAt = resumesAtOf(settled)
	if !entry.SessionResumable || entry.ResumesAt != string(runstate.PhaseChecking) {
		t.Fatalf("entry = %#v, want the stall reported resumable at the checks", entry)
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "continues the run at the checking phase") || !strings.Contains(rendered, "continues the run itself at the checking phase it stalled in") {
		t.Fatalf("entry does not name the checks as where the repair continues it:\n%s", rendered)
	}

	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, paused.RunID),
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
	continuing := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return errors.New("a stall at the checks is owed no developer attempt")
	}, approveVerdict)
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
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, checks), continuing).
				Continue(ctx, workItemID, runID)
		},
	}
	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: paused.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || !result.Stall || result.ResumesAt != runstate.PhaseChecking {
		t.Fatalf("result = %#v, want the stall continued at the checks", result)
	}
	if result.Outcome.RunID != paused.RunID || result.Outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("result = %#v, closed = %t, want the same run's change landed", result, tracker.Closed)
	}
	if developers := continuing.RequestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("developer invocations = %#v, want none for a stall at the checks", developers)
	}
	if reviews := continuing.RequestsForRole(domain.RoleReviewer); len(reviews) != 1 {
		t.Fatalf("review invocations = %d, want the review asked once after the checks", len(reviews))
	}
	// Once in the first attempt, and once more on the continuation over the
	// attempt's change — the check only passes where feature.txt is.
	ran, err := os.ReadFile(checksLog)
	if err != nil {
		t.Fatalf("ReadFile(checks log) error = %v", err)
	}
	if runs := strings.Count(string(ran), "ran\n"); runs != 2 {
		t.Fatalf("checks ran %d time(s), want the continuation to run them again on the attempt's change", runs)
	}
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if landed.Branch != stopped.Branch || landed.BaseCommit != stopped.BaseCommit || landed.RepairAttempts != 0 {
		t.Fatalf("landed run = %#v, want the stalled run's own branch carried through with no repair attempt", landed)
	}
}

// A stall at the review is continued only on the change it was stopped over:
// the continuation asks for it before the grant is spent, exactly as a repair
// does, rather than taking the exemption a stall mid-attempt has.
func TestAStallAtTheReviewIsHeldToTheRepairsContentCheck(t *testing.T) {
	t.Parallel()

	state := runstate.State{
		RunID:             "run-0123456789abcdef0123456789abcdef",
		WorkItemID:        "yoyodyne-task",
		Status:            runstate.StatusFailed,
		Phase:             runstate.PhaseReviewing,
		Blocker:           "settled",
		WorktreePath:      "/worktrees/yoyodyne-task",
		Branch:            "yoyodyne/yoyodyne-task/01234567",
		BaseCommit:        "base",
		TargetBranch:      "main",
		ProviderSessionID: "developer-session",
		Environmental:     &runstate.EnvironmentalRefusal{Cause: runstate.CauseProcessVanished, Settled: true},
	}
	if !continuableStall(state) || !stallResumesPastTheAttempt(state) || !resumesAnExistingChange(state) {
		t.Fatalf("a review-phase stall is continuable = %t, past the attempt = %t, owes a change = %t; want all three",
			continuableStall(state), stallResumesPastTheAttempt(state), resumesAnExistingChange(state))
	}
	// What the docket carries for either step is a well-formed entry, and reads
	// back through the run state's conversion as the phase it stalled in.
	for _, phase := range []runstate.Phase{runstate.PhaseChecking, runstate.PhaseReviewing} {
		at := state
		at.Phase = phase
		entry := triage.Entry{
			SchemaVersion:    triage.SchemaVersion,
			Key:              triage.Key(triage.ClassStoppedRun, at.RunID),
			Class:            triage.ClassStoppedRun,
			ProductID:        "yoyodyne",
			RunID:            at.RunID,
			WorkItemID:       at.WorkItemID,
			RecordedAt:       time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
			Blocker:          at.Blocker,
			Artifacts:        triage.Artifacts{Branch: at.Branch, WorktreePath: at.WorktreePath, DeveloperSession: at.ProviderSessionID},
			SessionResumable: true,
			ResumesAt:        resumesAtOf(at),
		}
		if back, ok := runstate.StallResumeStep(entry.ResumesAt); entry.Validate() != nil || !ok || back != phase {
			t.Fatalf("entry resumed at %q: Validate() = %v, read back as %q, want the %s phase", entry.ResumesAt, entry.Validate(), back, phase)
		}
	}
	if phase := continuedPhase(state, true); phase != runstate.PhaseReviewing {
		t.Fatalf("continued phase = %q, want the review it stalled in", phase)
	}
	state.Phase = runstate.PhaseChecking
	if !continuableStall(state) || continuedPhase(state, true) != runstate.PhaseChecking {
		t.Fatalf("a checks-phase stall is not continued at its checks")
	}
	// A run in its repair loop that stalls at its review or its checks had its
	// work judged before, and a failure returned: it is a repair's, not a stall's,
	// so nothing on its entry or its continuation says nothing was judged.
	for _, phase := range []runstate.Phase{runstate.PhaseReviewing, runstate.PhaseChecking} {
		repairing := state
		repairing.Phase = phase
		repairing.RepairAttempts = 1
		repairing.ReviewFindingDetails = []runstate.Finding{{Severity: "major", Message: "fix it"}}
		if continuableStall(repairing) || stallResumesPastTheAttempt(repairing) || continuedPhase(repairing, continuableStall(repairing)) != runstate.PhaseDeveloping {
			t.Fatalf("a %s-phase run carrying the reviewer's findings was admitted as a stall rather than a repair", phase)
		}
		if err := continuableRepair(repairing, triage.Found{BranchThere: true, WorktreeThere: true}); err != nil {
			t.Fatalf("continuableRepair() = %v, want a repair-loop run still carried out as a repair", err)
		}
	}
	// A stall mid-attempt is still continued at the attempt, and still only where
	// nothing was handed back.
	state.Phase = runstate.PhaseDeveloping
	if !continuableStall(state) || stallResumesPastTheAttempt(state) || continuedPhase(state, true) != runstate.PhaseDeveloping {
		t.Fatalf("a developing-phase stall is not continued at its attempt")
	}
	state.CheckFailure = &runstate.CheckFailure{Command: "exit 1", ExitCode: 1}
	if continuableStall(state) {
		t.Fatal("a developing-phase run carrying a failing check was admitted as a stall rather than a repair")
	}
}

// A vanished process that left nothing behind is a round the item must not have
// paid for, and the repair grant that bought it is given back — which is the
// environmental class's own rule, applied to the one round nothing else will
// ever settle.
func TestAVanishedProcessThatDeliveredNothingReturnsItsGrant(t *testing.T) {
	t.Parallel()

	now := baseTime
	state := runstate.State{
		RunID:        "run-0123456789abcdef0123456789abcdef",
		WorkItemID:   "yoyodyne-task",
		Status:       runstate.StatusRunning,
		Phase:        runstate.PhaseDeveloping,
		UpdatedAt:    now.Add(-time.Hour),
		WorktreePath: "/worktrees/yoyodyne-task",
		Branch:       "yoyodyne/yoyodyne-task/01234567",
		BaseCommit:   "base",
		ProviderStop: runstate.ProviderStopStalled,
		RepairContinuations: []runstate.RepairContinuation{{
			GrantedAttempts: 2, Reason: "granted", ContinuedAt: now.Add(-2 * time.Hour),
		}},
	}
	clean := gitworktree.Observation{WorktreePresent: true, BranchExists: true, BranchCommit: "base"}
	park := recordedPark{says: "the harness stopped its provider because it stalled", since: state.UpdatedAt}
	refusal := vanishedRefusal(&state, clean, park, now)
	if err := refusal.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !refusal.Refused || !refusal.GrantReturned || refusal.RoundReturned {
		t.Fatalf("refusal = %#v, want the round refused and the grant returned", refusal)
	}
	if state.CarriedOutRepairAttempts() != 0 {
		t.Fatalf("carried out = %d, want the returned grant not counted as carried out", state.CarriedOutRepairAttempts())
	}
	if !strings.Contains(refusal.Describe(), "granted repair round it consumed was returned") {
		t.Fatalf("Describe() = %q, want the return said", refusal.Describe())
	}

	// A branch that moved past the base is a delivery, however clean the
	// worktree is: the harness commits what a developer leaves before it advances.
	committed := runstate.State{RunID: state.RunID, WorktreePath: state.WorktreePath, BaseCommit: "base", ProviderStop: runstate.ProviderStopStalled, UpdatedAt: state.UpdatedAt}
	delivered := vanishedRefusal(&committed, gitworktree.Observation{WorktreePresent: true, BranchExists: true, BranchCommit: "ahead"}, park, now)
	if delivered.Refused || delivered.GrantReturned {
		t.Fatalf("refusal = %#v, want a round that delivered left spent", delivered)
	}
	if err := delivered.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

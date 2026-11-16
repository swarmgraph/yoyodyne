package recoverytests

import (
	"context"
	"fmt"
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
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A run waiting out a provider usage limit is not an interrupted run: it
// recorded a deadline and is owed the attempt it was refused. Settling it would
// throw away a claimed item and a preserved worktree over a wait that has not
// finished yet.
func TestReconcileLeavesARunPausedForAUsageLimitAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := testharness.BaseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	// Pause the run by refusing its first attempt with a wait too long to hold
	// this process open.
	first := testharness.UsageLimitBackend(1, limit, testharness.ApproveVerdict)
	firstClock := &testharness.PausingClock{Instant: testharness.BaseTime}
	firstPipeline := testharness.Waiting(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		firstClock, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}
	before, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionResumable {
		t.Fatalf("reconciliation = %#v, want the paused run left resumable", results)
	}
	if !strings.Contains(results[0].Detail, "paused for an exhausted five_hour usage limit") {
		t.Fatalf("reconciliation did not report why the run is waiting: %q", results[0].Detail)
	}
	after, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status != before.Status || after.Phase != before.Phase || after.UsageLimitResetsAt == nil {
		t.Fatalf("reconciliation disturbed a paused run: %#v", after)
	}
	if tracker.Blocked || tracker.Closed {
		t.Fatalf("reconciliation acted on the item of a paused run: blocked=%t closed=%t", tracker.Blocked, tracker.Closed)
	}

	// The run is still exactly as resumable afterwards: once the deadline passes
	// the pipeline picks it up and finishes it.
	second := testharness.UsageLimitBackend(0, limit, testharness.ApproveVerdict)
	secondClock := &testharness.PausingClock{Instant: resetsAt.Add(time.Minute)}
	resumed := testharness.Waiting(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second),
		secondClock, 6*time.Hour, time.Minute)
	outcome, err := resumed.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != before.RunID || outcome.Integration == nil {
		t.Fatalf("resumed run = %#v, want the reconciled run integrated", outcome)
	}
}

// A run whose provider the harness stopped on time is not an interrupted run
// either, for as long as the grace lasts: it is owed the rest of an attempt, in
// the worktree and session that attempt established, and settling it inside the
// grace would discard a change a `yoyo run` somebody is about to type can still
// finish. TestReconcileSettlesAStoppedRunNothingContinued is the other side of
// the grace.
func TestReconcileLeavesARunWithAStoppedProviderAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	first := testharness.ProviderStopBackend(1, execution.ProcessStalled, testharness.ApproveVerdict)
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)
	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}
	before, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionResumable {
		t.Fatalf("reconciliation = %#v, want the stopped run left resumable", results)
	}
	if !strings.Contains(results[0].Detail, "produced no output for longer than the harness allows") {
		t.Fatalf("reconciliation did not report why the provider was stopped: %q", results[0].Detail)
	}
	// The sweep says how long the run is left resumable, because a reading that
	// said only "resumable" was what let two of these sit for a day and a half.
	if !strings.Contains(results[0].Detail, "settles it as a stopped run") {
		t.Fatalf("reconciliation did not say the grace ends in a settlement: %q", results[0].Detail)
	}
	after, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status != before.Status || after.Phase != before.Phase || after.ProviderStop != before.ProviderStop {
		t.Fatalf("reconciliation disturbed a stopped run: %#v", after)
	}
	if tracker.Blocked || tracker.Closed {
		t.Fatalf("reconciliation acted on the item of a stopped run: blocked=%t closed=%t", tracker.Blocked, tracker.Closed)
	}

	second := testharness.ProviderStopBackend(0, execution.ProcessStalled, testharness.ApproveVerdict)
	resumed := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := resumed.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != before.RunID || outcome.Integration == nil {
		t.Fatalf("resumed run = %#v, want the reconciled run integrated", outcome)
	}
}

// A run the harness stopped on time that nothing then continued is a run whose
// process has vanished: its record goes on saying "running" with no live process
// behind it and no ending ever written, so it holds a developer slot, the
// in-flight guard refuses every item beside it, the claim audit leaves it as a
// wait, and every sweep reports it resumable while nothing resumes it. Two of
// these did exactly that from 2026-09-20 07:20 until somebody asked. Past the
// grace the sweep ends it, naming what it observed and the cause as outside the work,
// the item is blocked with the same account, the artifacts are left exactly as
// they were, and the stoppage is docketed — so the development manager's
// repair-continue has an entry to carry out against, and the slot is free.
func TestReconcileSettlesAStoppedRunNothingContinued(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	first := testharness.ProviderStopBackend(1, execution.ProcessStalled, testharness.ApproveVerdict)
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)
	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The process that hosted the run has returned: nothing holds the lease, and
	// the item still reads as claimed, which is exactly the state the two runs of
	// 2026-09-20 were found in.
	tracker.Item.Status = "in_progress"

	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	// The grace has passed and nothing continued the run. The clock is the
	// sweep's own rather than a shortened grace, so what is tested is the default
	// the operator gets.
	later := &testharness.PausingClock{Instant: stopped.UpdatedAt.Add(orchestrator.DefaultVanishedGrace)}
	reconciler := orchestrator.Reconciler{
		Tracker:   tracker,
		Worktrees: testharness.NewObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    testharness.DocketerOverStore(docket, store, firstPipeline.Config),
		Clock:     later,
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the vanished run settled as blocked", results)
	}
	for _, want := range []string{"no live process behind it", "produced no output for longer than the harness allows", "no ending was ever recorded", "so the harness ended the run. The cause was outside the work"} {
		if !strings.Contains(results[0].Detail, want) {
			t.Fatalf("reconciliation detail %q does not say %q", results[0].Detail, want)
		}
	}
	// A run ended by its idle bound is said to a person in ordinary words: what the
	// AI session did, what the harness did about it, and that the cause was
	// outside the work. The harness's own names for it — an idle bound, an
	// environmental stop — are nowhere in it.
	plain := fmt.Sprintf("the run was recorded as running in the %s phase with no live process behind it: at %s the harness stopped the AI session running it because it produced no output for longer than the harness allows, no ending was ever recorded, and nothing continued the run within %s of that, so the harness ended the run. The cause was outside the work, so nothing about the change was judged, and the change was kept: the branch and worktree are left exactly as the run left them",
		stopped.Phase, stopped.UpdatedAt.UTC().Format(time.RFC3339), orchestrator.DefaultVanishedGrace)
	if !strings.Contains(results[0].Detail, plain) {
		t.Fatalf("reconciliation detail does not say it plainly:\n got: %s\nwant: %s", results[0].Detail, plain)
	}
	for _, retired := range []string{"idle bound", "environmental stop", "environmental", "stall continuation"} {
		if strings.Contains(strings.ToLower(results[0].Detail), retired) {
			t.Fatalf("reconciliation detail says %q to a person:\n%s", retired, results[0].Detail)
		}
	}
	if results[0].Outcome != runstate.OutcomeStopped {
		t.Fatalf("outcome = %q, want the run read as stopped work somebody owns", results[0].Outcome)
	}

	// The record is terminal with the account on it, the stop it carried is now
	// evidence in the refusal rather than a promise to continue, and the change is
	// exactly where the developer left it.
	settled, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.Status.Terminal() || settled.CompletedAt == nil || settled.Blocker == "" || settled.ProviderStop != "" || settled.StopClass != runstate.StopProviderIdle {
		t.Fatalf("settled run = %#v, want a terminal record carrying the blocker and no provider stop", settled)
	}
	if !strings.Contains(settled.Failure, "so the harness ended the run. The cause was outside the work") {
		t.Fatalf("settled run's reason does not say the harness settled it: %q", settled.Failure)
	}
	// The settlement's end is when the sweep noticed, so the record keeps when the
	// run last moved: that is what the stall reading dates a dead line from.
	if settled.SettledQuietSince == nil || !settled.SettledQuietSince.Equal(stopped.UpdatedAt) {
		t.Fatalf("settled run was quiet since %v, want %s, when its record last moved", settled.SettledQuietSince, stopped.UpdatedAt)
	}
	refusal := settled.Environmental
	if refusal == nil || refusal.Cause != runstate.CauseProcessVanished || !refusal.Settled {
		t.Fatalf("environmental refusal = %#v, want the vanished process recorded and settled", refusal)
	}
	for _, want := range []string{"no live process held run " + paused.RunID, "no ending was recorded", stopped.UpdatedAt.UTC().Format(time.RFC3339), "produced no output for longer than the harness allows"} {
		if !strings.Contains(refusal.Detail, want) {
			t.Fatalf("refusal detail %q does not say %q", refusal.Detail, want)
		}
	}
	// The stopped attempt had written to its worktree, so the round delivered
	// something and is not refused in the class's sense: nothing is given back,
	// and the change is what the development manager decides about.
	if refusal.Refused || refusal.GrantReturned || refusal.RoundReturned {
		t.Fatalf("refusal = %#v, want a round that delivered left spent", refusal)
	}
	if settled.WorktreeRemoved || settled.BranchRemoved || settled.WorktreePath != stopped.WorktreePath || settled.Branch != stopped.Branch {
		t.Fatalf("settled run = %#v, want the branch and worktree preserved exactly as the stopped run's were", settled)
	}
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "partial.txt")); err != nil {
		t.Fatalf("the stopped attempt's work is not where it was left: %v", err)
	}
	if !tracker.Blocked || !strings.Contains(tracker.BlockReason, "so the harness ended the run. The cause was outside the work") {
		t.Fatalf("item blocked = %t with reason %q, want the item blocked with the sweep's account", tracker.Blocked, tracker.BlockReason)
	}

	// The slot and the in-flight guard read the same listing, and the run is no
	// longer in it.
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("in flight = %#v, want the settled run holding no slot", incomplete)
	}

	// And the stoppage is on the docket, carrying the environmental account, so a
	// repair-continue decided about it has something to carry out against.
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != paused.RunID || entries[0].Class != triage.ClassStoppedRun || entries[0].Closed != nil {
		t.Fatalf("docket = %#v, want the vanished run docketed as a stopped run nobody has decided about", entries)
	}
	entry := entries[0]
	if entry.Environmental == nil || entry.Environmental.Cause != string(runstate.CauseProcessVanished) || entry.Environmental.Account == "" {
		t.Fatalf("docketed environmental = %#v, want the vanished process and its accounting", entry.Environmental)
	}
	if !strings.Contains(entry.Blocker, "so the harness ended the run. The cause was outside the work") {
		t.Fatalf("docketed blocker is not the sweep's account:\n%s", entry.Blocker)
	}
	if entry.Artifacts.WorktreePath != stopped.WorktreePath || entry.Artifacts.Branch != stopped.Branch || entry.Artifacts.WorktreeRemoved || entry.Artifacts.BranchRemoved {
		t.Fatalf("artifacts = %#v, want the preserved worktree and branch", entry.Artifacts)
	}
	// Nothing was returned to this run's developer, so what it is owed is the
	// attempt the harness stopped it in, and the entry says so through the
	// durable store rather than only in the process that wrote it.
	// TestARepairContinuesAFirstAttemptStallInItsOwnSession is what carries that
	// decision out.
	if !entry.SessionResumable || entry.Artifacts.DeveloperSession != stopped.ProviderSessionID {
		t.Fatalf("entry = %#v, want the preserved developer session named and reported resumable", entry)
	}

	// A second sweep finds a settled run and nothing to do: the stoppage is keyed
	// to the run, so it is docketed once however many sweeps walk past it.
	again, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
	entries, err = docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want the one entry", entries)
	}
}

// The whole of it, over a real repository, for the case one of the two runs of
// 2026-09-20 was actually in: a run inside its repair loop — a failing check
// handed back to the developer — whose repair attempt the harness stopped on
// time and nothing continued. The sweep settles and dockets it, the development manager decides
// repair-continue about the entry, and the carry-out re-enters the same run in
// the same worktree and session and lands the change. Before this, the decision
// was refused for want of a docketed stoppage, because the run never recorded
// one.
func TestARepairContinueCarriesOutOnARunTheSweepSettledForAVanishedProcess(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first attempt leaves the check failing; the repair attempt the check
	// hands back is the one the harness stops on time.
	stalling := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	attempts := 0
	stalling.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleDeveloper {
			return backend.RunResult{}, fmt.Errorf("unexpected role %q before the repair attempt stalled", request.Role)
		}
		attempts++
		if attempts == 1 {
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("incomplete\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: stalling.DeveloperSession, ResolvedModel: testharness.DeveloperResolved,
				FinalText: "implemented the work item", Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
			}, nil
		}
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: stalling.DeveloperSession, IsError: true,
			StopReason: string(execution.ProcessStalled), Process: execution.ProcessResult{Status: execution.ProcessStalled, ExitCode: -1}, LastEvent: request.LastSequence,
		}, nil
	}
	checks := []string{"test -f fixed.txt"}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, stalling, checks), stalling)
	paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("Run() error = %v, outcome = %#v, want the repair attempt stopped on time", err, paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.CheckFailure == nil || stopped.RepairAttempts != 1 {
		t.Fatalf("stopped run = %#v, want the failing check on the record with one repair attempt spent", stopped)
	}
	tracker.Item.Status = "in_progress"

	docket := &testharness.MemoryDocket{}
	reconciler := orchestrator.Reconciler{
		Tracker:   tracker,
		Worktrees: testharness.NewObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    testharness.DocketerOverStore(docket, store, pipeline.Config),
		Clock:     &testharness.PausingClock{Instant: stopped.UpdatedAt.Add(orchestrator.DefaultVanishedGrace)},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the vanished run settled as blocked", results)
	}
	if len(docket.Entries) != 1 || docket.Entries[0].Check == nil || docket.Entries[0].Environmental == nil {
		t.Fatalf("docket = %#v, want the stoppage docketed with the failing check and the vanished process on it", docket.Entries)
	}

	// The development manager decides repair-continue about the docketed
	// stoppage, exactly as the conversation records one, and the carry-out
	// re-enters the run rather than refusing for want of a stoppage.
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, testharness.TriageDecided(runstate.TriageDecisionRepair, paused.RunID),
		orchestrator.TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), orchestrator.TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testharness.TestGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	continuing := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "fixed.txt"), []byte("fixed\n"), 0o600)
	}, testharness.ApproveVerdict)
	continuer := orchestrator.RepairContinuer{
		Docket:             docket,
		Runs:               store,
		Intake:             intake,
		Decisions:          store.Triage(),
		Items:              tracker,
		Worktrees:          worktrees,
		ConfiguredAttempts: pipeline.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
			return testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, checks), continuing).
				Continue(ctx, workItemID, runID)
		},
	}
	result, err := continuer.Continue(context.Background(), orchestrator.RepairContinueRequest{Run: paused.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.Outcome.RunID != paused.RunID || result.Outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("result = %#v, closed = %t, want the same run continued and its change landed", result, tracker.Closed)
	}
	continued := continuing.RequestsForRole(domain.RoleDeveloper)
	if len(continued) != 1 || continued[0].SessionID != stalling.DeveloperSession || continued[0].WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("continued attempts = %#v, want one, in the stopped run's own session and worktree", continued)
	}
	// The vanished process's account belonged to the round the continuation
	// superseded, and a run that landed carries neither it nor a stop.
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if landed.Environmental != nil || landed.ProviderStop != "" || landed.Blocker != "" {
		t.Fatalf("landed run = %#v, want the settled stoppage superseded", landed)
	}
}

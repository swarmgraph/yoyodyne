package recoverytests

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
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TestReconcileSettlesAnInterruptionAtEveryPhaseBoundary drives a real run to
// each boundary and then kills its persistence, which is what a killed process
// leaves behind: the last state it managed to write, with no terminal record
// after it. Every one of those states has to reconcile to a correct terminal
// state or to an explicit blocker, and never by restarting a developer.
func TestReconcileSettlesAnInterruptionAtEveryPhaseBoundary(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		// haltAt and haltAfter choose the boundary: writes stop for good once
		// the run reaches haltAt, after haltAfter writes there were allowed.
		haltAt    runstate.Phase
		haltAfter int
		// wantInterrupted is the phase that survives on disk.
		wantInterrupted runstate.Phase
		wantAction      orchestrator.ReconcileAction
		// wantPromoted reports whether the interrupted run had already moved
		// the target branch before it died.
		wantPromoted bool
	}{
		{
			name: "interrupted while developing", haltAt: runstate.PhaseChecking,
			wantInterrupted: runstate.PhaseDeveloping, wantAction: orchestrator.ActionBlocked,
		},
		{
			name: "interrupted after checks and before review", haltAt: runstate.PhaseReviewing,
			wantInterrupted: runstate.PhaseChecking, wantAction: orchestrator.ActionBlocked,
		},
		{
			name: "interrupted while reviewing", haltAt: runstate.PhaseIntegrating,
			wantInterrupted: runstate.PhaseReviewing, wantAction: orchestrator.ActionBlocked,
		},
		// The promotion landed but nothing recorded it. Only the repository can
		// say so, and reconciliation has to believe it rather than block work
		// that is already in the target branch.
		{
			name: "interrupted inside integration", haltAt: runstate.PhaseCompleting,
			wantInterrupted: runstate.PhaseIntegrating, wantAction: orchestrator.ActionCompleted, wantPromoted: true,
		},
		{
			name: "interrupted between integration and cleanup", haltAt: runstate.PhaseCleaningUp,
			wantInterrupted: runstate.PhaseCompleting, wantAction: orchestrator.ActionCompleted, wantPromoted: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store := testharness.RestartableFixture(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, testharness.ApproveVerdict)
			halting := &testharness.HaltingStore{StateStore: store, At: test.haltAt, After: test.haltAfter}
			pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)

			if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.Halted {
				t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.Halted)
			}
			interrupted, err := store.Load(testharness.PipelineRunID)
			if err != nil {
				t.Fatalf("Load() interrupted state error = %v", err)
			}
			if interrupted.Status.Terminal() || interrupted.Phase != test.wantInterrupted {
				t.Fatalf("interrupted state = %#v, want a live run in phase %q", interrupted, test.wantInterrupted)
			}
			base := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")
			if promoted := base != interrupted.BaseCommit; promoted != test.wantPromoted {
				t.Fatalf("main promoted = %t, want %t", promoted, test.wantPromoted)
			}
			providerRequests := len(provider.Requests)

			results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
			if len(results) != 1 || results[0].RunID != interrupted.RunID {
				t.Fatalf("reconciliation = %#v, want the interrupted run", results)
			}
			if results[0].Action != test.wantAction || results[0].Failure != "" {
				t.Fatalf("reconciliation = %#v, want action %q", results[0], test.wantAction)
			}
			// The whole point of reconciliation is that a lost process handle
			// never buys the item a second developer.
			if len(provider.Requests) != providerRequests {
				t.Fatalf("reconciliation issued %d provider request(s)", len(provider.Requests)-providerRequests)
			}

			settled, err := store.Load(interrupted.RunID)
			if err != nil {
				t.Fatalf("Load() settled state error = %v", err)
			}
			if !settled.Status.Terminal() || settled.CompletedAt == nil {
				t.Fatalf("settled state = %#v, want a terminal run", settled)
			}
			if test.wantAction == orchestrator.ActionBlocked {
				assertBlockedAndPreserved(t, repository, tracker, settled)
			} else {
				assertCompletedAndCleaned(t, repository, tracker, settled)
			}

			// Reconciling again settles nothing, because nothing is left
			// outstanding: a second sweep must not re-close or re-block an item.
			calls := len(tracker.Calls)
			if again := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker); len(again) != 0 {
				t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
			}
			if len(tracker.Calls) != calls {
				t.Fatalf("second reconciliation made %v", tracker.Calls[calls:])
			}
		})
	}
}

// assertBlockedAndPreserved checks the half of the contract that applies to a
// run nothing could finish: the item carries a blocker, the work survives, and
// nothing was promoted.
func assertBlockedAndPreserved(t *testing.T, repository string, tracker *orchestratortest.Tracker, settled runstate.State) {
	t.Helper()
	if settled.Status != runstate.StatusFailed || settled.Integration != nil {
		t.Fatalf("settled state = %#v, want a failed run with nothing integrated", settled)
	}
	if !tracker.Blocked || tracker.Closed {
		t.Fatalf("tracker = %#v, want a blocked and unclosed item", tracker)
	}
	if !strings.Contains(tracker.BlockReason, "No developer was restarted") ||
		!strings.Contains(tracker.BlockReason, "Preserved worktree: "+settled.WorktreePath) ||
		!strings.Contains(tracker.BlockReason, "Preserved branch: "+settled.Branch) {
		t.Fatalf("blocker reason = %q", tracker.BlockReason)
	}
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("blocked run lost its preserved change: %v", err)
	}
	if branches := testharness.GitOutput(t, repository, "branch", "--list", settled.Branch); strings.TrimSpace(branches) == "" {
		t.Fatal("blocked run lost its preserved branch")
	}
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != settled.BaseCommit {
		t.Fatalf("main = %q, want the untouched base %q", head, settled.BaseCommit)
	}
}

// assertCompletedAndCleaned checks the half that applies to a run whose work is
// already promoted: the item is closed exactly once and both artifacts are gone.
func assertCompletedAndCleaned(t *testing.T, repository string, tracker *orchestratortest.Tracker, settled runstate.State) {
	t.Helper()
	if settled.Status != runstate.StatusSucceeded || settled.Phase != runstate.PhaseComplete {
		t.Fatalf("settled state = %#v, want a completed run", settled)
	}
	if settled.Integration == nil || !settled.WorktreeRemoved || !settled.BranchRemoved || settled.CleanupFailure != "" {
		t.Fatalf("settled state = %#v, want recorded integration and removed artifacts", settled)
	}
	if !tracker.Closed || tracker.Blocked {
		t.Fatalf("tracker = %#v, want a closed and unblocked item", tracker)
	}
	if closes := testharness.CountCalls(tracker.Calls, "complete"); closes != 1 {
		t.Fatalf("item closed %d time(s), want exactly once", closes)
	}
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != settled.Integration.SourceCommit {
		t.Fatalf("main = %q, want the integrated commit %q", head, settled.Integration.SourceCommit)
	}
	if _, err := os.Stat(settled.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("completed run left its worktree behind: %v", err)
	}
	if branches := testharness.GitOutput(t, repository, "branch", "--list", settled.Branch); strings.TrimSpace(branches) != "" {
		t.Fatalf("completed run left its branch behind: %q", branches)
	}
}

// A cleanup that never ran leaves a succeeded, closed run with real artifacts
// still on disk. Reconciliation owes exactly the removal, and owes it without
// re-running anything the run already did.
func TestReconcileFinishesCleanupThatNeverRan(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.Worktrees = &testharness.HookedWorktrees{
		WorktreeManager: pipeline.Worktrees,
		BeforeCleanup:   func() error { return errors.New("worktree is busy") },
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.CleanupFailure == "" || outcome.WorktreeRemoved {
		t.Fatalf("outcome = %#v, want a succeeded run with outstanding cleanup", outcome)
	}

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v", results)
	}
	settled, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertCompletedAndCleaned(t, repository, tracker, settled)
	if len(provider.Requests) != 2 {
		t.Fatalf("provider requests = %d, want the original developer and reviewer only", len(provider.Requests))
	}
}

// The artifacts of a run whose completion was never written down are already
// gone. Repeating cleanup over them has to be a no-op rather than an error,
// because that is the only way reconciliation can be safe to run repeatedly.
func TestReconcileSettlesARunWhoseArtifactsAreAlreadyGone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	// Cleanup runs for real and removes both artifacts; only the record of it
	// never lands, which is exactly the pre-cleanup marker left on disk.
	losing := &testharness.InterruptingStore{StateStore: store, FailPhase: runstate.PhaseComplete, FailAlways: true}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, losing, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Phase != runstate.PhaseCleaningUp || recorded.WorktreeRemoved || recorded.BranchRemoved {
		t.Fatalf("recorded state = %#v, want the pre-cleanup marker", recorded)
	}
	if _, err := os.Stat(outcome.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("the worktree this test needs gone is still there: %v", err)
	}

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want a clean completion over absent artifacts", results)
	}
	settled, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertCompletedAndCleaned(t, repository, tracker, settled)
	// The item was already closed by the run itself, so reconciliation records
	// no second closure and no second outcome note.
	if closes := testharness.CountCalls(tracker.Calls, "complete"); closes != 1 {
		t.Fatalf("item closed %d time(s), want exactly once", closes)
	}
}

// A run its own pipeline can still continue is not reconciliation's to settle.
// Ending it would throw away a change that can still be finished.
func TestReconcileLeavesAResumableRepairLoopAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	write := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	interrupted := &testharness.InterruptedStore{StateStore: store, AtAttempt: 2}
	first := orchestratortest.RoleBackend(write, testharness.RepairVerdict)
	firstPipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{"exit 0"}), first)
	if _, err := firstPipeline.Run(context.Background(), tracker.Item.ID); err == nil || !interrupted.Stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.Stopped)
	}
	before, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionResumable {
		t.Fatalf("reconciliation = %#v, want the run left resumable", results)
	}
	after, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status != before.Status || after.Phase != before.Phase || after.RepairAttempts != before.RepairAttempts {
		t.Fatalf("reconciliation changed a resumable run: %#v", after)
	}
	if tracker.Blocked || tracker.Closed {
		t.Fatalf("reconciliation acted on the item of a resumable run: %#v", tracker)
	}

	// The run is still exactly as resumable afterwards, so the pipeline picks
	// it up and finishes it rather than starting a second run for the item.
	second := orchestratortest.RoleBackend(write, testharness.ApproveVerdict)
	resumed := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := resumed.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != before.RunID || outcome.Integration == nil {
		t.Fatalf("resumed run = %#v, want the reconciled run integrated", outcome)
	}
}

// A run somebody is still acting on has an owner. Reconciliation must not
// decide anything about it from outside, however interrupted its state looks.
func TestReconcileLeavesRunsALiveProcessHolds(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseChecking}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}

	_, lease, err := store.AdoptRun(context.Background(), testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	defer lease.Release()

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionHeld || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the held run left alone", results)
	}
	held, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if held.Status.Terminal() || tracker.Blocked {
		t.Fatalf("reconciliation settled a held run: state = %#v, blocked = %t", held, tracker.Blocked)
	}
}

// A run interrupted before it created anything leaves nothing for a person to
// act on, so it is recorded terminal without blocking the item.
func TestReconcileFailsARunThatLeftNothingBehind(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	now := execution.RealClock{}.Now()
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         testharness.PipelineRunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    tracker.Item.ID,
		Backend:       "claude-code",
		Status:        runstate.StatusPending,
		StartedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionFailed || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want a plain terminal failure", results)
	}
	// What the sweep did and what became of the run are different facts, and the
	// second is what says whether anybody's work is gone. This run made nothing,
	// which is stated as itself rather than as work thrown away.
	if outcome := results[0].Outcome; outcome != runstate.OutcomeFailed {
		t.Errorf("outcome = %q, want %q", outcome, runstate.OutcomeFailed)
	}
	if remains := results[0].Artifacts().Describe(); remains != "no artifacts recorded" {
		t.Errorf("what remains = %q, want the absence stated rather than work reported as removed", remains)
	}
	if tracker.Blocked {
		t.Fatal("reconciliation blocked an item that has nothing preserved")
	}
	if !strings.Contains(tracker.Notes, "left nothing behind") || !strings.Contains(tracker.Notes, "No worktree was recorded") {
		t.Fatalf("notes = %q", tracker.Notes)
	}
	settled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Status != runstate.StatusFailed || settled.CompletedAt == nil {
		t.Fatalf("settled state = %#v", settled)
	}
}

// A stoppage can be settled onto a record that is already terminal: the last
// write of the process that died lands after the sweep listed the run and
// before it adopts it. The stoppage still has to reach that record. Every
// surface answers "why did this stop" from the reason and the blocker on the
// run, so a sweep that left the record as it found it handed a person three
// empty answers.
func TestReconcileRecordsAStoppageSettledOntoAnAlreadyTerminalRun(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseChecking}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}

	// The terminal record the interrupted process was still to write is a
	// cancellation with no reason on it, which is what stopping a run leaves.
	cancelled := &cancelledBetweenListingAndAdoption{ReconcileStore: store, at: execution.RealClock{}.Now()}
	results := testharness.ReconcileSweep(t, repository, worktreeRoot, cancelled, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the stoppage settled onto the terminal run", results)
	}
	if !cancelled.settled {
		t.Fatal("the run was never made terminal, so this settled an interruption rather than a terminal record")
	}
	settled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The run keeps the status it recorded for itself; what the sweep adds is
	// what stopped it and why.
	if settled.Status != runstate.StatusCancelled {
		t.Fatalf("settled status = %q, want the status the run recorded for itself", settled.Status)
	}
	if !strings.Contains(settled.Blocker, "no attempt of the harness can finish it") {
		t.Fatalf("blocker = %q, want the words the item was blocked in", settled.Blocker)
	}
	if !strings.Contains(settled.Failure, "reconciled after an interrupted run") ||
		!strings.Contains(settled.Failure, "no attempt of the harness can finish it") {
		t.Fatalf("failure = %q, want the reason a person reads for why this stopped", settled.Failure)
	}
	if outcome := settled.Outcome(); outcome != runstate.OutcomeStopped {
		t.Errorf("outcome = %q, want %q", outcome, runstate.OutcomeStopped)
	}
	// `yoyo status` prints its reason from the read model rather than from the
	// record, so the reason has to survive into the summary.
	history, err := store.History(runstate.RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 || history.Runs[0].Failure != settled.Failure {
		t.Fatalf("history = %#v, want the settled run's own reason", history.Runs)
	}
	// The reason line `yoyo status` prints is the summary's Reason, and the
	// channel's is the record's: one derivation, so one answer for this run.
	if reason := history.Runs[0].Reason(); !strings.Contains(reason, "no attempt of the harness can finish it") ||
		reason != settled.Reason() {
		t.Fatalf("status reason = %q, channel reason = %q, want the same reason naming the stoppage", reason, settled.Reason())
	}
	// A record settled this way before the sweep wrote its own reason carries
	// the blocker and no failure. Both surfaces still say why from the blocker,
	// rather than saying the record names no reason over words it holds.
	unreasoned := settled
	unreasoned.Failure = ""
	if err := store.Save(unreasoned); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	history, err = store.History(runstate.RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history = %#v, want the one run", history.Runs)
	}
	if reason := history.Runs[0].Reason(); reason != runstate.StopReason(unreasoned.StopClass, settled.Blocker) || reason != unreasoned.Reason() {
		t.Fatalf("status reason = %q, channel reason = %q, want the blocker %q on both", reason, unreasoned.Reason(), settled.Blocker)
	}
	// The triage docket is the other surface a person triages from, and a build
	// after this sweep reads the same record rather than the sweep's own copy of
	// it.
	docket := &testharness.MemoryDocket{}
	build, err := testharness.DocketerOver([]runstate.State{settled}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(build.Entries) != 1 || build.Entries[0].Blocker != settled.Blocker {
		t.Fatalf("docket = %#v, want one entry carrying the run's blocker", build.Entries)
	}
}

// cancelledBetweenListingAndAdoption records the terminal write of the process
// that died in the window a sweep leaves for it: after the runs to settle are
// listed and before the one it settles is adopted and re-read. What it writes is
// a cancellation, which is the terminal record that names no reason of its own.
type cancelledBetweenListingAndAdoption struct {
	orchestrator.ReconcileStore
	at      time.Time
	settled bool
}

func (s *cancelledBetweenListingAndAdoption) Outstanding() ([]runstate.State, error) {
	outstanding, err := s.ReconcileStore.Outstanding()
	if err != nil || s.settled {
		return outstanding, err
	}
	for _, state := range outstanding {
		cancelled := state
		cancelled.Status = runstate.StatusCancelled
		completedAt := s.at
		cancelled.CompletedAt = &completedAt
		cancelled.UpdatedAt = completedAt
		if err := s.ReconcileStore.Save(cancelled); err != nil {
			return nil, err
		}
		s.settled = true
	}
	return outstanding, nil
}

// A promotion found in the target branch is only this run's to claim if this
// run's reviewer approved it. Without that the commit needs a person.
func TestReconcileBlocksAnUnapprovedPromotionItFinds(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseCompleting}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	// Strip the approval the run recorded, leaving the promoted commit without
	// the evidence that authorized it. What it approved goes with it: the two are
	// one verdict, and the record refuses one without the other.
	interrupted, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	interrupted.ReviewDecision = ""
	interrupted.ReviewApproves = ""
	if err := store.Save(interrupted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("reconciliation = %#v, want an unapproved promotion blocked", results)
	}
	// The run is handed to a person with its change intact, and the sweep says
	// both. Reporting the durable status alone would put "failed" over a branch
	// and worktree that are still there, which is the reading this vocabulary
	// exists to stop.
	if outcome := results[0].Outcome; outcome != runstate.OutcomeStopped {
		t.Errorf("outcome = %q, want %q", outcome, runstate.OutcomeStopped)
	}
	if remains := results[0].Artifacts().Describe(); remains != "work preserved" {
		t.Errorf("what remains = %q, want the preserved change named", remains)
	}
	if !tracker.Blocked || tracker.Closed {
		t.Fatalf("tracker = %#v, want a blocked and unclosed item", tracker)
	}
	if !strings.Contains(tracker.BlockReason, "no approving review") {
		t.Fatalf("blocker reason = %q", tracker.BlockReason)
	}
	settled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Integration != nil || settled.Status != runstate.StatusFailed {
		t.Fatalf("settled state = %#v, want no integration claimed", settled)
	}
}

// A promotion the durable invariants refuse to describe is handed to a person
// rather than claimed. The item must not be closed behind evidence that cannot
// be written down, which would leave it settled nowhere.
func TestReconcileBlocksAPromotionItCannotRecord(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseCompleting}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	// Integration insists on two demonstrably separate invocations. A run whose
	// reviewer session is missing cannot claim one, however the repository looks.
	interrupted, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	interrupted.ReviewSessionID = ""
	if err := store.Save(interrupted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the unrecordable promotion blocked", results)
	}
	if !tracker.Blocked || tracker.Closed {
		t.Fatalf("tracker = %#v, want a blocked and unclosed item", tracker)
	}
	if !strings.Contains(tracker.BlockReason, "does not support recording that promotion") {
		t.Fatalf("blocker reason = %q", tracker.BlockReason)
	}
	settled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Integration != nil || settled.Status != runstate.StatusFailed {
		t.Fatalf("settled state = %#v, want no integration claimed", settled)
	}
}

// A recorded integration is a claim a process wrote down, and reconciliation
// exists for state a process that died wrote. A promotion the target does not
// carry has to reach a person rather than close an item over work that is not
// there.
func TestReconcileBlocksARecordedIntegrationTheTargetDoesNotCarry(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseCleaningUp}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	interrupted, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if interrupted.Integration == nil {
		t.Fatalf("interrupted state = %#v, want the recorded integration this test contradicts", interrupted)
	}
	// Put the target back where it was before the promotion, which is what the
	// repository looks like when the record outran what actually landed.
	testharness.GitOutput(t, repository, "update-ref", "refs/heads/main", interrupted.BaseCommit)

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the uncarried promotion blocked", results)
	}
	// The run itself closed the item when it recorded the promotion, so the
	// blocker is what reopens the question rather than a note beside it.
	if !tracker.Blocked || tracker.Item.Status != "blocked" {
		t.Fatalf("tracker = %#v, want the item blocked for a person", tracker)
	}
	if !strings.Contains(tracker.BlockReason, "does not contain it") ||
		!strings.Contains(tracker.BlockReason, interrupted.Integration.SourceCommit) {
		t.Fatalf("blocker reason = %q, want the recorded commit and the target that lacks it", tracker.BlockReason)
	}
	settled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Status != runstate.StatusFailed || settled.Integration != nil {
		t.Fatalf("settled state = %#v, want a failed run claiming no promotion", settled)
	}
	// Nothing the run built was swept away on the strength of a record the
	// repository denies, and the target was not moved to make the record true.
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("blocked run lost its preserved change: %v", err)
	}
	if branches := testharness.GitOutput(t, repository, "branch", "--list", settled.Branch); strings.TrimSpace(branches) == "" {
		t.Fatal("blocked run lost its preserved branch")
	}
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != settled.BaseCommit {
		t.Fatalf("main = %q, want the untouched base %q", head, settled.BaseCommit)
	}
	// The blocked run keeps nothing that would make it owe cleanup, so the next
	// sweep leaves it alone instead of deciding it over again.
	if again := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// The same contradiction on a record that already said the run succeeded, which
// is the shape a process killed after the completing save leaves. The sweep
// deliberately keeps the status such a record wrote for itself, so what stops it
// reading as work that landed is the precedence rule in the read model: the
// blocker this sweep saves outranks the status, and every surface says "stopped".
func TestReconcileDoesNotLeaveAContradictedPromotionReadingAsSucceeded(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseCleaningUp}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	interrupted, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if interrupted.Integration == nil {
		t.Fatalf("interrupted state = %#v, want the recorded integration this test contradicts", interrupted)
	}
	// The run reached its terminal status before the process died, short of the
	// complete phase, which is what leaves the cleanup outstanding.
	completedAt := interrupted.UpdatedAt
	interrupted.Status = runstate.StatusSucceeded
	interrupted.CompletedAt = &completedAt
	if err := store.Save(interrupted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	testharness.GitOutput(t, repository, "update-ref", "refs/heads/main", interrupted.BaseCommit)

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the uncarried promotion blocked", results)
	}
	if !tracker.Blocked || tracker.Item.Status != "blocked" {
		t.Fatalf("tracker = %#v, want the item blocked for a person", tracker)
	}
	settled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The status stays what the run recorded — the sweep's own rule, unchanged —
	// and the blocker beside it is what the outcome is read from.
	if settled.Status != runstate.StatusSucceeded || settled.Integration != nil {
		t.Fatalf("settled state = %#v, want the recorded status kept and no promotion claimed", settled)
	}
	if settled.Outcome() != runstate.OutcomeStopped {
		t.Fatalf("settled outcome = %q, want a stoppage somebody owns", settled.Outcome())
	}
	if !strings.Contains(settled.Failure, "does not contain it") {
		t.Fatalf("settled failure = %q, want the reason the sweep settled it on", settled.Failure)
	}
	if !strings.Contains(settled.Blocker, "does not contain it") {
		t.Fatalf("settled blocker = %q, want the blocker the item carries", settled.Blocker)
	}
	// Nothing the run built was swept away on the strength of a record the
	// repository denies. The run closed the item when it recorded the promotion,
	// and the blocker above is what reopens the question.
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("blocked run lost its preserved change: %v", err)
	}
	// The settled run owes nothing further, so the next sweep leaves it alone
	// rather than deciding it again.
	if again := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// Observing the target must not turn every target that moved on into a
// disagreement. A promotion other work was committed on top of is still in the
// target, and the run that made it is owed its completion.
func TestReconcileCompletesARecordedIntegrationTheTargetMovedPast(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseCleaningUp}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	// Somebody else's commit lands on the target, so it no longer stands where
	// this run's promotion left it.
	if err := os.WriteFile(filepath.Join(repository, "later.txt"), []byte("later work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	testharness.RunPipelineGit(t, repository, "add", "later.txt")
	testharness.RunPipelineGit(t, repository, "commit", "-m", "later work")

	results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the promotion the target still carries completed", results)
	}
	if tracker.Blocked {
		t.Fatalf("reconciliation blocked a promotion the target carries: %q", tracker.BlockReason)
	}
	settled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Phase != runstate.PhaseComplete || !settled.WorktreeRemoved || !settled.BranchRemoved {
		t.Fatalf("settled state = %#v, want a completed run with its artifacts removed", settled)
	}
}

// A sweep that cannot finish must settle nothing and hide nothing. The run
// stays outstanding and a later sweep completes it, which is what makes
// reconciliation safe to simply run again.
func TestReconcileLeavesARunOutstandingWhenItsCleanupFails(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseCleaningUp}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}

	worktrees := &refusingCleanup{ReconcileWorktrees: testharness.NewObserver(t, repository, worktreeRoot), refusals: 1}
	results, err := orchestrator.Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != orchestrator.ActionUnsettled || !strings.Contains(results[0].Failure, "worktree is busy") {
		t.Fatalf("reconciliation = %#v, want the run left unsettled", results)
	}
	unsettled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !unsettled.Outstanding() || unsettled.CleanupFailure == "" {
		t.Fatalf("unsettled state = %#v, want an outstanding run carrying its cleanup failure", unsettled)
	}
	if _, err := os.Stat(filepath.Join(unsettled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("refused cleanup removed the worktree anyway: %v", err)
	}

	// The next sweep finishes the job, and the item is still closed exactly
	// once across both of them.
	again := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(again) != 1 || again[0].Action != orchestrator.ActionCompleted || again[0].Failure != "" {
		t.Fatalf("second reconciliation = %#v", again)
	}
	settled, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertCompletedAndCleaned(t, repository, tracker, settled)
}

// Reconcile is a sweep, not a single-run command: one run it cannot settle must
// not hide the others from the operator reading the report.
func TestReconcileReportsEveryRunEvenWhenOneCannotBeSettled(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseCleaningUp}
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	// A second recorded run, interrupted before it built anything. It is
	// settled on its own regardless of what happens to the first one.
	now := execution.RealClock{}.Now()
	bare := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-abcdef0123456789abcdef0123456789",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    tracker.Item.ID,
		Backend:       "claude-code",
		Status:        runstate.StatusPending,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.Create(bare); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	worktrees := &refusingCleanup{ReconcileWorktrees: testharness.NewObserver(t, repository, worktreeRoot), refusals: 1}
	results, err := orchestrator.Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("reconciliation = %#v, want both outstanding runs reported", results)
	}
	actions := map[string]orchestrator.ReconcileAction{}
	for _, result := range results {
		actions[result.RunID] = result.Action
	}
	// The bare run built nothing and its item is already closed by the first
	// run, so it settles as a plain terminal failure rather than a blocker.
	if actions[testharness.PipelineRunID] != orchestrator.ActionUnsettled || actions[bare.RunID] != orchestrator.ActionFailed {
		t.Fatalf("reconciliation = %#v, want the failed cleanup unsettled and the bare run settled", results)
	}
}

// refusingCleanup refuses the first cleanup attempts and then delegates, which
// is how a sweep that could not finish is exercised end to end.
type refusingCleanup struct {
	orchestrator.ReconcileWorktrees
	refusals int
}

func (w *refusingCleanup) CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
	if w.refusals > 0 {
		w.refusals--
		return gitworktree.Cleanup{}, errors.New("worktree is busy")
	}
	return w.ReconcileWorktrees.CleanupIntegrated(ctx, request)
}

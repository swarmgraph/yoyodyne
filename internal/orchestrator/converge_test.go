package orchestrator

import (
	"context"
	"errors"
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
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Deleting the branch is written onto the run, which is the half that was
// missing while the sweep deleted branches and recorded nothing. Every reader of
// a run asks BranchRemoved for whether the change survived, so a deletion
// nothing wrote down leaves the run advertising a branch that is not there —
// which is what run-48216ea9 was still doing, both its artifacts gone, when a
// developer went looking for the work and reported it destroyed.
func TestSweepingRecordsTheBranchItDeleted(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The round produces nothing, which is what leaves a branch the sweep may
	// delete: every attempt that writes anything is committed onto the run branch
	// before the checks run, so a branch with work on it is one the sweep keeps.
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseChecking}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}
	if results := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the interrupted run blocked with its artifacts preserved", results)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.BranchRemoved || !settled.Artifacts().Preserved() {
		t.Fatalf("record = %#v, want a stopped run whose change is preserved on its branch", settled)
	}
	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store}

	// The checkout goes first, as the sweep does it: a branch a checkout still
	// holds is kept, and that checkout is the one being retired.
	if _, swept := reconciler.sweepWorktree(context.Background(), settled); !swept {
		t.Fatal("the checkout was not retired, so the branch cannot be swept")
	}
	retired, err := store.Load(settled.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The branch carries nothing the target does not, because the round wrote
	// nothing for the harness to commit onto it — which is what earns the
	// deletion.
	sweep, swept := reconciler.sweepBranch(context.Background(), retired, nil)
	if !swept || !sweep.Removed || sweep.Kept != "" || sweep.Failure != "" || sweep.RecordProblem != "" {
		t.Fatalf("sweep = %#v, swept = %t, want the branch deleted and written down", sweep, swept)
	}
	recorded, err := store.Load(settled.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.BranchRemoved || recorded.BranchSweptAt == nil {
		t.Fatalf("record = %#v, want the deletion written down", recorded)
	}
	// Which is the whole point: nothing reading this run says its work is
	// preserved on a branch that is gone.
	if recorded.Artifacts().Preserved() {
		t.Errorf("artifacts = %#v, still read as preserved with both of them gone", recorded.Artifacts())
	}
	// Asking again says there was nothing there, so a sweep that runs on every
	// pass does not report the same long-gone branch forever.
	if again, swept := reconciler.sweepBranch(context.Background(), recorded, nil); swept {
		t.Fatalf("sweep = %#v, want a branch that is already gone reported as nothing to do", again)
	}
}

// The accumulation this bound exists for: every worktree registration is a path
// an agent's sandbox profile denies on every command it spawns, so a machine
// that keeps them all eventually cannot spawn a command in the next worktree at
// all. Only the most recent settled runs keep their checkout; the rest are past
// the tail and swept.
func TestSweepableWorktreesHoldsBackTheMostRecentSettledRuns(t *testing.T) {
	t.Parallel()

	stoppedAt := baseTime
	settled := func(runID string) runstate.State {
		stoppedAt = stoppedAt.Add(time.Minute)
		completed := stoppedAt
		return runstate.State{
			RunID:        runID,
			WorkItemID:   "yoyodyne-task",
			Status:       runstate.StatusFailed,
			CompletedAt:  &completed,
			WorktreePath: "/worktrees/" + runID,
			Branch:       "yoyodyne/" + runID,
		}
	}
	// Three more settled runs than the tail holds, oldest first, so the store's
	// order is not what decides which ones survive.
	recorded := make([]runstate.State, 0, settledWorktreeTail+3)
	for index := 0; index < settledWorktreeTail+3; index++ {
		recorded = append(recorded, settled(fmt.Sprintf("run-%02d", index)))
	}
	// A run still in flight, a run whose record already says the checkout is
	// gone, and a run that never had one are none of this sweep's business.
	inFlight := settled("run-in-flight")
	inFlight.Status = runstate.StatusRunning
	inFlight.CompletedAt = nil
	cleanedUp := settled("run-cleaned-up")
	cleanedUp.WorktreeRemoved = true
	neverHadOne := settled("run-no-worktree")
	neverHadOne.WorktreePath = ""
	recorded = append(recorded, inFlight, cleanedUp, neverHadOne)

	swept := sweepableWorktrees(recorded)
	if len(swept) != 3 {
		t.Fatalf("sweepable = %#v, want the three runs past the tail", swept)
	}
	for _, state := range swept {
		switch state.RunID {
		case "run-00", "run-01", "run-02":
		default:
			t.Errorf("run %s was swept, want only the three oldest", state.RunID)
		}
	}
	// Nothing past the tail means nothing to sweep, which is the ordinary state
	// of a machine that is keeping up with itself.
	if held := sweepableWorktrees(recorded[:settledWorktreeTail]); len(held) != 0 {
		t.Fatalf("sweepable = %#v, want the whole tail held back", held)
	}
}

// The case that decides whether this bounds anything: a stopped run's checkout
// with the developer's half-finished change still in it. That is the ordinary
// shape of the population the sweep exists for, so keeping those would leave
// most of the registrations exactly where they were. It is retired — and the
// work is moved somewhere durable first, which is what makes retiring it lose
// nothing.
func TestSweepingRetiresACheckoutAndPreservesTheWorkInIt(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseChecking}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}
	if results := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the interrupted run blocked with its artifacts preserved", results)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store}

	// What the attempt produced is on the branch — every attempt is committed
	// before the checks run — and what is uncommitted is whatever an invocation
	// the process died inside had written but not finished. That half exists in no
	// other record, and it is the half this preserves, so the fixture leaves one.
	if err := os.WriteFile(filepath.Join(settled.WorktreePath, "half-finished.txt"), []byte("mid-invocation\n"), 0o600); err != nil {
		t.Fatalf("seed the checkout's uncommitted work: %v", err)
	}
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the fixture left no work in the checkout at all: %v", err)
	}
	retired, swept := reconciler.sweepWorktree(context.Background(), settled)
	if !swept || !retired.Removed || retired.Kept != "" || retired.Failure != "" ||
		retired.RecordProblem != "" || retired.ItemProblem != "" {
		t.Fatalf("sweep = %#v, swept = %t, want the checkout retired", retired, swept)
	}
	if _, err := os.Stat(settled.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("the checkout is still on disk: %v", err)
	}

	// Nothing was lost: the change is readable from the ref the sweep named.
	if retired.PreservedWork != gitworktree.PreservedWorkRef(settled.RunID) {
		t.Fatalf("preserved work = %q, want %q", retired.PreservedWork, gitworktree.PreservedWorkRef(settled.RunID))
	}
	if preserved := gitOutput(t, repository, "show", retired.PreservedWork+":half-finished.txt"); preserved != "mid-invocation\n" {
		t.Errorf("preserved half-finished.txt = %q, want the work the checkout alone held", preserved)
	}
	if preserved := gitOutput(t, repository, "show", retired.PreservedWork+":feature.txt"); preserved != "implemented\n" {
		t.Errorf("preserved feature.txt = %q, want the developer's change", preserved)
	}
	// The branch is deliberately untouched, and the capture did not move it:
	// whether it may go is the branch sweep's question and it needs the target to
	// answer it.
	if branches := strings.TrimSpace(gitOutput(t, repository, "for-each-ref", "--format=%(refname)", "refs/heads/"+settled.Branch)); branches == "" {
		t.Error("the branch was deleted with the checkout")
	}
	if commit := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "refs/heads/"+settled.Branch)); commit != settled.HarnessCommit {
		t.Errorf("branch = %q, want it left at the attempt's commit %q rather than carrying the capture", commit, settled.HarnessCommit)
	}

	// The removal is written onto the run it belongs to, and so is where the work
	// went. `yoyo status`, the docket, and a re-run all read that record as the
	// answer to whether the directory is there, so one that still said "preserved"
	// would send every one of them after a checkout that is gone — and the ref is
	// the only thing connecting the captured work back to the item.
	recorded, err := store.Load(settled.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.WorktreeRemoved || recorded.WorktreeSweptAt == nil {
		t.Fatalf("record = %#v, want the retirement written down", recorded)
	}
	if recorded.PreservedWorkRef != retired.PreservedWork {
		t.Errorf("recorded preserved work = %q, want %q", recorded.PreservedWorkRef, retired.PreservedWork)
	}
	if recorded.BranchRemoved {
		t.Error("the record claims the branch was removed, which the sweep never touches")
	}

	// And the work item is told, which the run record alone does not do. What that
	// item already carries is the failed run's own note naming this checkout, and
	// from the moment above it describes a directory that is not there; the person
	// who picks the item up reads the item rather than the run's state file, and
	// the ref is the only route from what they read back to the work.
	if !strings.Contains(tracker.Notes, retired.PreservedWork) ||
		!strings.Contains(tracker.Notes, "Retired worktree: "+settled.WorktreePath) ||
		!strings.Contains(tracker.Notes, "git worktree add --detach") {
		t.Errorf("the item was not told where the retired checkout's work went: %q", tracker.Notes)
	}

	// Asking again says there was nothing there, so a sweep that runs on every
	// pass does not report the same long-gone checkout forever.
	if again, swept := reconciler.sweepWorktree(context.Background(), recorded); swept {
		t.Fatalf("sweep = %#v, want a checkout that is already gone reported as nothing to do", again)
	}
}

// The case the by-hand cleanup produces across many records at once: the
// checkout is gone and this sweep is not what removed it. Nothing needs printing
// — nobody has to read about a directory that was already not there — but the
// record does need writing, or every reader of that run keeps being sent to it
// and the run keeps occupying a slot in the tail it has no checkout to fill.
func TestSweepingRecordsACheckoutSomethingElseAlreadyRemoved(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseChecking}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}
	if results := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the interrupted run blocked with its artifacts preserved", results)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// An operator clears the machine by hand: the checkout is removed and its
	// registration with it, exactly as `git worktree remove` does. The second
	// --force is what Git wants for a working tree the developer left unclean,
	// which is every preserved checkout worth removing by hand.
	runPipelineGit(t, repository, "worktree", "remove", "--force", "--force", settled.WorktreePath)
	if _, err := os.Stat(settled.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("the checkout is still on disk: %v", err)
	}

	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store}
	sweep, swept := reconciler.sweepWorktree(context.Background(), settled)
	if swept {
		t.Fatalf("sweep = %#v, want nothing reported for a checkout that was already gone", sweep)
	}
	recorded, err := store.Load(settled.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.WorktreeRemoved || recorded.WorktreeSweptAt == nil {
		t.Fatalf("record = %#v, want the run told its checkout is gone even though this sweep did not remove it", recorded)
	}
	// Which is what takes it out of the candidates, so it stops holding a slot in
	// the tail that was meant for a checkout somebody can still open. A full tail
	// of newer runs stands beside it: without the record this one is the oldest
	// candidate and would be probed again on every pass forever.
	beside := make([]runstate.State, 0, settledWorktreeTail+1)
	for index := 0; index < settledWorktreeTail; index++ {
		later := recorded.CompletedAt.Add(time.Duration(index+1) * time.Minute)
		beside = append(beside, runstate.State{
			RunID:        fmt.Sprintf("run-later-%02d", index),
			Status:       runstate.StatusFailed,
			CompletedAt:  &later,
			WorktreePath: fmt.Sprintf("/worktrees/later-%02d", index),
		})
	}
	if candidates := sweepableWorktrees(append(beside, recorded)); len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want the run dropped once its record says the checkout is gone", candidates)
	}
}

// The whole of it through the front door: more settled runs than the tail holds,
// swept by Converge itself. This is what proves the loop, the tail bound, and
// the ordering — the checkout goes before the branch sweep asks about the branch,
// because a branch a checkout still holds is one the branch sweep keeps.
func TestConvergeRetiresSettledCheckoutsPastTheTail(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	worktrees := newSweepManager(t, repository, worktreeRoot)

	// One more settled run than the tail keeps, oldest first, each with a real
	// checkout and a real branch. The oldest holds a half-finished change, which
	// is what a stopped run's checkout normally holds.
	settled := make([]runstate.State, 0, settledWorktreeTail+1)
	for index := 0; index <= settledWorktreeTail; index++ {
		state := settledRunWithCheckout(t, worktrees, store, index)
		tracker.HoldsItem(beads.WorkItem{ID: state.WorkItemID, Title: "Task", Status: "open"})
		if index == 0 {
			writeSweepFile(t, filepath.Join(state.WorktreePath, "half-done.txt"), "the developer got this far\n")
		}
		settled = append(settled, state)
	}
	registeredBefore := len(linkedWorktrees(t, repository))
	if registeredBefore != settledWorktreeTail+1 {
		t.Fatalf("registered worktrees = %d, want %d before the sweep", registeredBefore, settledWorktreeTail+1)
	}

	convergence, err := (Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}).Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}

	// Exactly the one run past the tail is retired, and the tail is left standing.
	if len(convergence.Worktrees) != 1 {
		t.Fatalf("worktree sweeps = %#v, want only the run past the tail", convergence.Worktrees)
	}
	swept := convergence.Worktrees[0]
	oldest := settled[0]
	if swept.RunID != oldest.RunID || !swept.Removed || swept.Kept != "" || swept.Failure != "" || swept.RecordProblem != "" || swept.ItemProblem != "" {
		t.Fatalf("sweep = %#v, want run %s retired", swept, oldest.RunID)
	}
	if registered := len(linkedWorktrees(t, repository)); registered != settledWorktreeTail {
		t.Errorf("registered worktrees = %d, want the tail of %d left", registered, settledWorktreeTail)
	}
	// Its half-finished change survived the retirement, which is the only reason
	// retiring a dirty checkout is allowed at all.
	if swept.PreservedWork == "" {
		t.Fatal("the retired checkout held uncommitted work and nothing says where it went")
	}
	if preserved := gitOutput(t, repository, "show", swept.PreservedWork+":half-done.txt"); preserved != "the developer got this far\n" {
		t.Errorf("preserved half-done.txt = %q, want the developer's work", preserved)
	}

	// The ordering: the branch sweep ran after the checkout was unregistered, so
	// the branch it was holding could go in the same pass. A branch still checked
	// out is one RemoveMergedBranch keeps, which is what this would show instead.
	var branch BranchSweep
	for _, candidate := range convergence.Branches {
		if candidate.RunID == oldest.RunID {
			branch = candidate
		}
	}
	if !branch.Removed || branch.Kept != "" {
		t.Fatalf("branch sweep = %#v, want the retired run's branch removed in the same pass", branch)
	}
	// Every branch still held by a tail checkout is kept, which is the other half
	// of that ordering being real rather than incidental.
	for _, candidate := range convergence.Branches {
		if candidate.RunID != oldest.RunID && candidate.Removed {
			t.Errorf("branch sweep = %#v, want a branch its checkout still holds left alone", candidate)
		}
	}

	// Sweeping again takes nothing: the retired run records that its checkout is
	// gone, and the rest are still inside the tail.
	repeated, err := (Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}).Converge(context.Background())
	if err != nil {
		t.Fatalf("second Converge() error = %v", err)
	}
	if len(repeated.Worktrees) != 0 {
		t.Fatalf("second sweep = %#v, want nothing left to retire", repeated.Worktrees)
	}
	if registered := len(linkedWorktrees(t, repository)); registered != settledWorktreeTail {
		t.Errorf("registered worktrees = %d after a second sweep, want the tail of %d", registered, settledWorktreeTail)
	}
}

// A run whose merge the forge still holds on a protected target is over and not
// settled: nothing proves its change is on the target, and its kept branch and
// worktree are what a head that falls behind is replayed from. So however old
// it is, the sweep never retires either while the merge is unsettled — the
// settlement that confirms the merge is what removes them, and nothing earlier.
func TestConvergeNeverRetiresTheCheckoutOrBranchOfAnUnsettledQueuedMerge(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	protectBranch(t, fixture.remote, "main")
	fixture.forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "branch protection"})
	outcome := fixture.run(t)
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	queued := loadRun(t, fixture.store, pipelineRunID)
	if queued.WorktreeRemoved || queued.BranchRemoved || queued.CompletedAt == nil {
		t.Fatalf("queued run = %#v, want a finished run keeping its branch and worktree", queued)
	}

	// One settled run older than the queued one, and a full tail newer than it,
	// so the queued run sits past the tail beside a run the sweep does retire.
	worktrees := newSweepManager(t, fixture.repository, fixture.worktreeRoot)
	settle := func(index int, at time.Time) runstate.State {
		state := settledRunWithCheckout(t, worktrees, fixture.store, index)
		state.StartedAt, state.UpdatedAt, state.CompletedAt = at, at, &at
		if err := fixture.store.Save(state); err != nil {
			t.Fatalf("Save() run %d error = %v", index, err)
		}
		return state
	}
	older := settle(0, queued.CompletedAt.Add(-time.Hour))
	for index := 1; index <= settledWorktreeTail; index++ {
		settle(index, queued.CompletedAt.Add(time.Duration(index)*time.Minute))
	}
	candidates := make([]string, 0)
	for _, candidate := range sweepableWorktrees(mustRecorded(t, fixture.store)) {
		candidates = append(candidates, candidate.RunID)
	}
	if len(candidates) != 1 || candidates[0] != older.RunID {
		t.Fatalf("candidates = %v, want only the older settled run %s and never the queued merge", candidates, older.RunID)
	}

	convergence := fixture.converge(t)
	for _, sweep := range convergence.Worktrees {
		if sweep.RunID == pipelineRunID {
			t.Fatalf("worktree sweep = %#v, want the unsettled queued merge's checkout left alone", sweep)
		}
	}
	for _, sweep := range convergence.Branches {
		if sweep.RunID == pipelineRunID {
			t.Fatalf("branch sweep = %#v, want the unsettled queued merge's branch left alone", sweep)
		}
	}
	if len(convergence.Worktrees) != 1 || convergence.Worktrees[0].RunID != older.RunID || !convergence.Worktrees[0].Removed {
		t.Fatalf("worktree sweeps = %#v, want the older settled run retired beside it", convergence.Worktrees)
	}
	if _, err := os.Stat(outcome.WorktreePath); err != nil {
		t.Errorf("queued merge's worktree %s: %v, want it standing while the merge is unsettled", outcome.WorktreePath, err)
	}
	if head := gitOutput(t, fixture.repository, "branch", "--list", outcome.Branch); strings.TrimSpace(head) == "" {
		t.Errorf("queued merge's branch %s is gone, want it standing while the merge is unsettled", outcome.Branch)
	}
	if kept := loadRun(t, fixture.store, pipelineRunID); kept.WorktreeRemoved || kept.BranchRemoved {
		t.Errorf("queued run = worktree removed %t, branch removed %t; want both recorded as standing", kept.WorktreeRemoved, kept.BranchRemoved)
	}

	// The forge merges, and the settlement that confirms it is what retires both.
	fixture.forge.PerformQueuedMerge(t)
	settled := false
	for _, result := range fixture.reconcile(t) {
		if result.RunID == pipelineRunID {
			settled = result.Action == ActionCompleted && result.Failure == ""
		}
	}
	if !settled {
		t.Fatal("reconciliation did not settle the queued merge as completed")
	}
	fixture.converge(t)
	if _, err := os.Stat(outcome.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("queued merge's worktree %s: %v, want it retired once the merge is confirmed", outcome.WorktreePath, err)
	}
	if head := gitOutput(t, fixture.repository, "branch", "--list", outcome.Branch); strings.TrimSpace(head) != "" {
		t.Errorf("queued merge's branch %s is standing, want it retired once the merge is confirmed", outcome.Branch)
	}
	if retired := loadRun(t, fixture.store, pipelineRunID); !retired.WorktreeRemoved || !retired.BranchRemoved {
		t.Errorf("settled run = worktree removed %t, branch removed %t; want both retired", retired.WorktreeRemoved, retired.BranchRemoved)
	}
}

func mustRecorded(t *testing.T, store *runstate.Store) []runstate.State {
	t.Helper()
	recorded, err := store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	return recorded
}

// settledRunWithCheckout creates a real worktree and records a settled run for
// it, so a sweep over the store meets artifacts that are exactly what the
// pipeline would have left. The index orders them: run 0 is the oldest.
func settledRunWithCheckout(t *testing.T, worktrees *gitworktree.Manager, store *runstate.Store, index int) runstate.State {
	t.Helper()
	runID := fmt.Sprintf("run-%032x", index)
	workItemID := fmt.Sprintf("yoyodyne-swept.%d", index)
	worktree, err := worktrees.Create(context.Background(), gitworktree.CreateRequest{
		RunID:        runID,
		WorkItemID:   workItemID,
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() worktree %d error = %v", index, err)
	}
	settledAt := baseTime.Add(time.Duration(index) * time.Minute)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "repository",
		WorkItemID:    workItemID,
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusFailed,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     settledAt,
		UpdatedAt:     settledAt,
		CompletedAt:   &settledAt,
		WorktreePath:  worktree.Path,
		Branch:        worktree.Branch,
		BaseCommit:    worktree.BaseCommit,
		TargetBranch:  worktree.TargetBranch,
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() run %d error = %v", index, err)
	}
	return state
}

// newSweepManager is the real worktree manager, which the sweep needs as a
// concrete type here because these tests create the checkouts it retires.
func newSweepManager(t *testing.T, repository, worktreeRoot string) *gitworktree.Manager {
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

// linkedWorktrees names every worktree registered against the repository apart
// from the primary checkout — the count this whole item exists to bound.
func linkedWorktrees(t *testing.T, repository string) []string {
	t.Helper()
	var paths []string
	for _, line := range strings.Split(gitOutput(t, repository, "worktree", "list", "--porcelain"), "\n") {
		path, found := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !found || samePath(t, path, repository) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

func samePath(t *testing.T, left, right string) bool {
	t.Helper()
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	if leftErr != nil || rightErr != nil {
		return left == right
	}
	return resolvedLeft == resolvedRight
}

func writeSweepFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// The seam the operator drew a line under: a merge the forge performs after its
// run is over leaves the primary checkout behind, and pulling it forward was a
// person's job. It is judgement-free — a fast-forward onto a commit that
// already contains the local branch — so the sweep takes it.
func TestConvergeCatchesUpATargetNoRunIsGoingToFinish(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	// The forge merges after the run is over, and nothing settles the run: the
	// sweep is driven on its own, so what it does here is its own work rather
	// than the settle path's. That is the case it exists for — a target branch
	// left behind the forge by something no run is going to finish.
	fixture.forge.PerformQueuedMerge(t)
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Fatalf("local main = %q, want the promoted commit %q before the sweep", local, outcome.Integration.TargetCommit)
	}

	convergence := fixture.converge(t)
	merge := publishedCommit(t, fixture.remote, "main")
	if len(convergence.Targets) != 1 || !convergence.Targets[0].Advanced || convergence.Targets[0].TargetBranch != "main" {
		t.Fatalf("targets = %#v, want main advanced", convergence.Targets)
	}
	if convergence.Targets[0].RemoteCommit != merge {
		t.Errorf("caught up to %q, want the forge's merge commit %q", convergence.Targets[0].RemoteCommit, merge)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != merge {
		t.Errorf("local main = %q, want the forge's merge commit %q", local, merge)
	}

	// Sweeping again is what every later `yoyo reconcile` does, and a repository
	// already level with the forge has nothing left to converge.
	repeated := fixture.converge(t)
	if len(repeated.Targets) != 1 || repeated.Targets[0].Advanced || repeated.Targets[0].Held != "" {
		t.Fatalf("second convergence = %#v, want nothing left to catch up", repeated)
	}
	if len(repeated.Branches) != 0 {
		t.Fatalf("second convergence = %#v, want no branch left to sweep", repeated)
	}
}

// Settling a merge is complete on its own. The convergence sweep runs in the
// same `yoyo reconcile` today, so a catch-up left to it would look identical
// from the command line — but it would make a converged checkout depend on who
// called what, and a caller that only settles runs would leave the branch
// silently behind. So the settle path catches up itself, and this drives
// Reconcile alone to prove it.
func TestReconcileSettlesAQueuedMergeAndCatchesTheTargetUpItself(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", results)
	}
	merge := publishedCommit(t, fixture.remote, "main")
	if merge == outcome.Integration.TargetCommit {
		t.Fatalf("remote main = %q, want the forge's merge commit above the promoted commit", merge)
	}
	catchup := results[0].Catchup
	if catchup == nil || !catchup.Advanced || catchup.Held != "" {
		t.Fatalf("catch-up = %#v, want main advanced by the settle itself", catchup)
	}
	if catchup.TargetBranch != "main" || catchup.RemoteCommit != merge {
		t.Errorf("catch-up = %#v, want main brought onto %q", catchup, merge)
	}
	// The whole point: no convergence sweep has run, and the checkout is level
	// with the forge anyway.
	if local := publishedCommit(t, fixture.repository, "main"); local != merge {
		t.Errorf("local main = %q, want the forge's merge commit %q without a sweep", local, merge)
	}
	if !strings.Contains(fixture.tracker.Record().Notes, "caught up to "+merge) {
		t.Errorf("tracker notes do not report the catch-up:\n%s", fixture.tracker.Record().Notes)
	}
}

// A merge the forge dropped is one of the two settle outcomes that reach a person. The
// publication is outstanding, nothing about it is confirmed, and the local
// branch must not be moved on it — deciding that is exactly what the harness is
// not allowed to do here.
func TestReconcileDoesNotCatchUpWhenTheForgeDroppedTheQueuedMerge(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.DropQueuedMerge()

	results := fixture.reconcile(t)
	if len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want the dropped merge settled", results)
	}
	if results[0].Catchup != nil {
		t.Fatalf("catch-up = %#v, want none for a publication nothing confirmed", results[0].Catchup)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want it left at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}
}

// Dead local branches are the other half of the same hygiene. A settled run
// whose branch survived — a cleanup that could not finish, an interruption
// between the two removals — leaves a branch whose work the target already
// carries, and nothing about deleting it is a decision.
func TestConvergeRemovesTheLeftoverBranchOfASettledRun(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)
	if settled := fixture.reconcile(t); len(settled) != 1 || settled[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", settled)
	}
	// The branch this run's cleanup removed, back where a cleanup that never
	// finished would have left it.
	runPipelineGit(t, fixture.repository, "branch", outcome.Branch, outcome.Integration.SourceCommit)

	convergence := fixture.converge(t)
	if len(convergence.Branches) != 1 {
		t.Fatalf("branches = %#v, want the leftover branch swept", convergence.Branches)
	}
	swept := convergence.Branches[0]
	if !swept.Removed || swept.Failure != "" || swept.Kept != "" {
		t.Fatalf("sweep = %#v, want the branch removed", swept)
	}
	if swept.Branch != outcome.Branch || swept.RunID != outcome.RunID || swept.TargetBranch != "main" {
		t.Errorf("sweep = %#v, want it to name run %q's branch %q", swept, outcome.RunID, outcome.Branch)
	}
	if branches := strings.TrimSpace(gitOutput(t, fixture.repository, "for-each-ref", "--format=%(refname)", "refs/heads/"+outcome.Branch)); branches != "" {
		t.Errorf("leftover branch survived: %q", branches)
	}
}

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

// A run that still owes a step is left entirely alone. Settling it may yet need
// the branch, and deciding that is reconciliation's rather than hygiene's — so a
// live developer's branch is never a candidate here.
func TestConvergeLeavesTheBranchOfARunThatStillOwesAStep(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	// The forge is still holding the merge, so the run is outstanding.
	runPipelineGit(t, fixture.repository, "branch", outcome.Branch, outcome.Integration.SourceCommit)

	convergence := fixture.converge(t)
	if len(convergence.Branches) != 0 {
		t.Fatalf("branches = %#v, want an outstanding run's branch left alone", convergence.Branches)
	}
	if commit := strings.TrimSpace(gitOutput(t, fixture.repository, "rev-parse", "refs/heads/"+outcome.Branch)); commit != outcome.Integration.SourceCommit {
		t.Errorf("branch = %q, want it left at %q", commit, outcome.Integration.SourceCommit)
	}
	// The target is still caught up, because a run in flight says nothing about
	// where the branch it will promote into belongs.
	if len(convergence.Targets) != 1 || convergence.Targets[0].TargetBranch != "main" {
		t.Errorf("targets = %#v, want main considered", convergence.Targets)
	}
}

// The composition the settle path's self-catch-up leans on: what the settle
// does not catch up — a remote that moved past the merge, evidence in the way
// — is finished by the sweep, and a sweep held on evidence finishes on the
// next pass once the evidence clears. Settle owning the common case is only
// safe because the sweep still owns the rest.
func TestACatchupHeldDuringSettleIsFinishedByALaterSweep(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)

	// Another machine's work lands on the remote target above the merge,
	// changing the same file the run shipped — and the primary checkout holds
	// an unsaved edit to that file, so catching up would overwrite it.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if _, err := fixture.forge.Git("worktree", "add", elsewhere, "main"); err != nil {
		t.Fatalf("open a worktree on the remote: %v", err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "feature.txt"), []byte("another machine's change\n"), 0o600); err != nil {
		t.Fatalf("change the file remotely: %v", err)
	}
	gitAt(t, elsewhere, "-c", "user.name=Elsewhere", "-c", "user.email=elsewhere@example.invalid", "commit", "-am", "another machine's merge")
	if err := os.WriteFile(filepath.Join(fixture.repository, "feature.txt"), []byte("unsaved edit\n"), 0o600); err != nil {
		t.Fatalf("dirty the primary checkout: %v", err)
	}

	// The settle completes the run and leaves the moved target to the sweep.
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", results)
	}
	if catchup := results[0].Catchup; catchup != nil && catchup.Advanced {
		t.Fatalf("catch-up = %#v, want the settle to leave the moved target alone", catchup)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Fatalf("local main = %q, want it left at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}

	// The sweep refuses on the evidence while the edit is in the way.
	held := fixture.converge(t)
	if len(held.Targets) != 1 || held.Targets[0].Advanced || held.Targets[0].Held == "" {
		t.Fatalf("convergence = %#v, want main held on the unsaved edit", held)
	}

	// The evidence clears, and the next sweep finishes the catch-up.
	if err := os.WriteFile(filepath.Join(fixture.repository, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
		t.Fatalf("restore the primary checkout: %v", err)
	}
	convergence := fixture.converge(t)
	if len(convergence.Targets) != 1 || !convergence.Targets[0].Advanced || convergence.Targets[0].Held != "" {
		t.Fatalf("convergence = %#v, want main caught up by the sweep", convergence)
	}
	remote := publishedCommit(t, fixture.remote, "main")
	if local := publishedCommit(t, fixture.repository, "main"); local != remote {
		t.Errorf("local main = %q, want the remote tip %q after the sweep", local, remote)
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

// The registrations no sweep driven from run state can see: a checkout somebody
// deleted without telling Git. It costs every later command a deny path in its
// sandbox profile all the same, and the prune is what reaches it.
func TestConvergePrunesARegistrationWhoseCheckoutIsGone(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.run(t)
	stray := filepath.Join(t.TempDir(), "checkout-somebody-deleted")
	runPipelineGit(t, fixture.repository, "worktree", "add", "--detach", stray, "HEAD")
	if err := os.RemoveAll(stray); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	convergence := fixture.converge(t)
	if convergence.Registrations.Failure != "" {
		t.Fatalf("the prune failed: %s", convergence.Registrations.Failure)
	}
	if len(convergence.Registrations.Pruned) != 1 {
		t.Fatalf("pruned = %#v, want only the stale registration", convergence.Registrations.Pruned)
	}
	// The recorded path is Git's own, which resolves the symlinks a temporary
	// directory is reached through, so the name is what identifies it.
	if pruned := filepath.Base(convergence.Registrations.Pruned[0]); pruned != filepath.Base(stray) {
		t.Errorf("pruned %q, want the registration of %q", convergence.Registrations.Pruned[0], stray)
	}
	// Sweeping again finds nothing stale, so a sweep on every pass does not
	// repeat itself.
	repeated := fixture.converge(t)
	if len(repeated.Registrations.Pruned) != 0 || repeated.Registrations.Failure != "" {
		t.Fatalf("second prune = %#v, want nothing stale left", repeated.Registrations)
	}
}

// A registration a killed `git worktree add` left half-written is the one that
// used to stop every later run on the repository at its own creation, until a
// person removed the directory by hand. The sweep clears it and says which
// entry it was and why, so the next run creates its worktree with nobody
// having noticed anything.
func TestConvergeClearsARegistrationAKilledAddLeftBehind(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.run(t)
	registration := filepath.Join(fixture.repository, ".git", "worktrees", "yoyodyne-killed-0a1b2c3d")
	orphan := filepath.Join(t.TempDir(), "yoyodyne-killed-0a1b2c3d")
	for _, directory := range []string{registration, orphan} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", directory, err)
		}
	}
	// What an add killed while registering leaves: still locked as initializing,
	// gitdir written, commondir created and never written — the file every
	// registration walk then dies over — and no index, because it never reached
	// the checkout. Aged so it cannot be an add somebody else is still running.
	writeSweepFile(t, filepath.Join(registration, "locked"), "initializing")
	writeSweepFile(t, filepath.Join(registration, "gitdir"), filepath.Join(orphan, ".git")+"\n")
	writeSweepFile(t, filepath.Join(registration, "commondir"), "")
	then := time.Now().Add(-10 * time.Minute)
	for _, file := range []string{"locked", "gitdir", "commondir", ""} {
		if err := os.Chtimes(filepath.Join(registration, file), then, then); err != nil {
			t.Fatalf("Chtimes(%s) error = %v", file, err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(registration) })
	if output, err := attemptPipelineGit(fixture.repository, "worktree", "add", "--quiet", "--detach", filepath.Join(t.TempDir(), "probe"), "HEAD"); err == nil {
		t.Fatalf("git created a worktree with a killed add's registration present, so the failure the sweep clears is gone:\n%s", output)
	}

	convergence := fixture.converge(t)
	if convergence.Registrations.Failure != "" {
		t.Fatalf("the prune failed: %s", convergence.Registrations.Failure)
	}
	if len(convergence.Registrations.Unfinished) != 1 {
		t.Fatalf("unfinished = %#v, want the killed add's registration alone", convergence.Registrations.Unfinished)
	}
	cleared := convergence.Registrations.Unfinished[0]
	if !cleared.Cleared || cleared.Name != filepath.Base(registration) || cleared.Path != orphan || !strings.Contains(cleared.Reason, "initializing") {
		t.Fatalf("unfinished = %#v, want the entry cleared, named, and its directory named", cleared)
	}
	if _, err := os.Lstat(registration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want the registration gone", registration, err)
	}
	// The whole point: the next run can create its worktree.
	next := filepath.Join(t.TempDir(), "next")
	runPipelineGit(t, fixture.repository, "worktree", "add", "--quiet", "--detach", next, "HEAD")
	runPipelineGit(t, fixture.repository, "worktree", "remove", "--force", next)
	// Sweeping again meets nothing, so a sweep on every pass does not repeat
	// itself.
	repeated := fixture.converge(t)
	if len(repeated.Registrations.Unfinished) != 0 || repeated.Registrations.Failure != "" {
		t.Fatalf("second sweep = %#v, want no unfinished registration left", repeated.Registrations)
	}
}

// gitAt runs one git command somewhere a fixture helper is not already bound.
func gitAt(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", arguments, directory, err, output)
	}
}

// The second outcome that reaches a person: the forge performed the merge and
// the finishing steps failed. The break on that path is the only thing keeping
// the local branch off a publication nothing confirmed, and this is what fails
// if it is removed.
func TestASettleThatCannotFinishThePublicationLeavesTheLocalBranchAlone(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.forge.SetReplayMerge(true)
	outcome := fixture.run(t)
	// The forge merges as a replay: the remote's new tip carries the change's
	// content without carrying the promoted commit, so confirming the
	// publication honestly fails even though a merge really happened.
	fixture.forge.PerformQueuedMerge(t)

	results := fixture.reconcile(t)
	if len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want the one queued merge examined", results)
	}
	if results[0].Catchup != nil {
		t.Fatalf("catch-up = %#v, want none on a publication that could not be confirmed", results[0].Catchup)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want it still at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}
}

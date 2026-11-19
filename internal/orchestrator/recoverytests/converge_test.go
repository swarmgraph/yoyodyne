package recoverytests

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The seam the operator drew a line under: a merge the forge performs after its
// run is over leaves the primary checkout behind, and pulling it forward was a
// person's job. It is judgement-free — a fast-forward onto a commit that
// already contains the local branch — so the sweep takes it.
func TestConvergeCatchesUpATargetNoRunIsGoingToFinish(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	// The forge merges after the run is over, and nothing settles the run: the
	// sweep is driven on its own, so what it does here is its own work rather
	// than the settle path's. That is the case it exists for — a target branch
	// left behind the forge by something no run is going to finish.
	fixture.Forge.PerformQueuedMerge(t)
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != outcome.Integration.TargetCommit {
		t.Fatalf("local main = %q, want the promoted commit %q before the sweep", local, outcome.Integration.TargetCommit)
	}

	convergence := fixture.Converge(t)
	merge := testharness.PublishedCommit(t, fixture.Remote, "main")
	if len(convergence.Targets) != 1 || !convergence.Targets[0].Advanced || convergence.Targets[0].TargetBranch != "main" {
		t.Fatalf("targets = %#v, want main advanced", convergence.Targets)
	}
	if convergence.Targets[0].RemoteCommit != merge {
		t.Errorf("caught up to %q, want the forge's merge commit %q", convergence.Targets[0].RemoteCommit, merge)
	}
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != merge {
		t.Errorf("local main = %q, want the forge's merge commit %q", local, merge)
	}

	// Sweeping again is what every later `yoyo reconcile` does, and a repository
	// already level with the forge has nothing left to converge.
	repeated := fixture.Converge(t)
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

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)

	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", results)
	}
	merge := testharness.PublishedCommit(t, fixture.Remote, "main")
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
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != merge {
		t.Errorf("local main = %q, want the forge's merge commit %q without a sweep", local, merge)
	}
	if !strings.Contains(fixture.Tracker.Record().Notes, "caught up to "+merge) {
		t.Errorf("tracker notes do not report the catch-up:\n%s", fixture.Tracker.Record().Notes)
	}
}

// A merge the forge dropped is one of the two settle outcomes that reach a person. The
// publication is outstanding, nothing about it is confirmed, and the local
// branch must not be moved on it — deciding that is exactly what the harness is
// not allowed to do here.
func TestReconcileDoesNotCatchUpWhenTheForgeDroppedTheQueuedMerge(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	fixture.Forge.DropQueuedMerge()

	results := fixture.Reconcile(t)
	if len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want the dropped merge settled", results)
	}
	if results[0].Catchup != nil {
		t.Fatalf("catch-up = %#v, want none for a publication nothing confirmed", results[0].Catchup)
	}
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want it left at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}
}

// Dead local branches are the other half of the same hygiene. A settled run
// whose branch survived — a cleanup that could not finish, an interruption
// between the two removals — leaves a branch whose work the target already
// carries, and nothing about deleting it is a decision.
func TestConvergeRemovesTheLeftoverBranchOfASettledRun(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)
	if settled := fixture.Reconcile(t); len(settled) != 1 || settled[0].Action != orchestrator.ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", settled)
	}
	// The branch this run's cleanup removed, back where a cleanup that never
	// finished would have left it.
	testharness.RunPipelineGit(t, fixture.Repository, "branch", outcome.Branch, outcome.Integration.SourceCommit)

	convergence := fixture.Converge(t)
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
	if branches := strings.TrimSpace(testharness.GitOutput(t, fixture.Repository, "for-each-ref", "--format=%(refname)", "refs/heads/"+outcome.Branch)); branches != "" {
		t.Errorf("leftover branch survived: %q", branches)
	}
}

// A run that still owes a step is left entirely alone. Settling it may yet need
// the branch, and deciding that is reconciliation's rather than hygiene's — so a
// live developer's branch is never a candidate here.
func TestConvergeLeavesTheBranchOfARunThatStillOwesAStep(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	// The forge is still holding the merge, so the run is outstanding.
	testharness.RunPipelineGit(t, fixture.Repository, "branch", outcome.Branch, outcome.Integration.SourceCommit)

	convergence := fixture.Converge(t)
	if len(convergence.Branches) != 0 {
		t.Fatalf("branches = %#v, want an outstanding run's branch left alone", convergence.Branches)
	}
	if commit := strings.TrimSpace(testharness.GitOutput(t, fixture.Repository, "rev-parse", "refs/heads/"+outcome.Branch)); commit != outcome.Integration.SourceCommit {
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

	fixture := testharness.NewQueuedFixture(t)
	outcome := fixture.Run(t)
	fixture.Forge.PerformQueuedMerge(t)

	// Another machine's work lands on the remote target above the merge,
	// changing the same file the run shipped — and the primary checkout holds
	// an unsaved edit to that file, so catching up would overwrite it.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if _, err := fixture.Forge.Git("worktree", "add", elsewhere, "main"); err != nil {
		t.Fatalf("open a worktree on the remote: %v", err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "feature.txt"), []byte("another machine's change\n"), 0o600); err != nil {
		t.Fatalf("change the file remotely: %v", err)
	}
	gitAt(t, elsewhere, "-c", "user.name=Elsewhere", "-c", "user.email=elsewhere@example.invalid", "commit", "-am", "another machine's merge")
	if err := os.WriteFile(filepath.Join(fixture.Repository, "feature.txt"), []byte("unsaved edit\n"), 0o600); err != nil {
		t.Fatalf("dirty the primary checkout: %v", err)
	}

	// The settle completes the run and leaves the moved target to the sweep.
	results := fixture.Reconcile(t)
	if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", results)
	}
	if catchup := results[0].Catchup; catchup != nil && catchup.Advanced {
		t.Fatalf("catch-up = %#v, want the settle to leave the moved target alone", catchup)
	}
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != outcome.Integration.TargetCommit {
		t.Fatalf("local main = %q, want it left at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}

	// The sweep refuses on the evidence while the edit is in the way.
	held := fixture.Converge(t)
	if len(held.Targets) != 1 || held.Targets[0].Advanced || held.Targets[0].Held == "" {
		t.Fatalf("convergence = %#v, want main held on the unsaved edit", held)
	}

	// The evidence clears, and the next sweep finishes the catch-up.
	if err := os.WriteFile(filepath.Join(fixture.Repository, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
		t.Fatalf("restore the primary checkout: %v", err)
	}
	convergence := fixture.Converge(t)
	if len(convergence.Targets) != 1 || !convergence.Targets[0].Advanced || convergence.Targets[0].Held != "" {
		t.Fatalf("convergence = %#v, want main caught up by the sweep", convergence)
	}
	remote := testharness.PublishedCommit(t, fixture.Remote, "main")
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != remote {
		t.Errorf("local main = %q, want the remote tip %q after the sweep", local, remote)
	}
}

// The registrations no sweep driven from run state can see: a checkout somebody
// deleted without telling Git. It costs every later command a deny path in its
// sandbox profile all the same, and the prune is what reaches it.
func TestConvergePrunesARegistrationWhoseCheckoutIsGone(t *testing.T) {
	t.Parallel()

	fixture := testharness.NewQueuedFixture(t)
	fixture.Run(t)
	stray := filepath.Join(t.TempDir(), "checkout-somebody-deleted")
	testharness.RunPipelineGit(t, fixture.Repository, "worktree", "add", "--detach", stray, "HEAD")
	if err := os.RemoveAll(stray); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	convergence := fixture.Converge(t)
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
	repeated := fixture.Converge(t)
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

	fixture := testharness.NewQueuedFixture(t)
	fixture.Run(t)
	registration := filepath.Join(fixture.Repository, ".git", "worktrees", "yoyodyne-killed-0a1b2c3d")
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
	testharness.WriteSweepFile(t, filepath.Join(registration, "locked"), "initializing")
	testharness.WriteSweepFile(t, filepath.Join(registration, "gitdir"), filepath.Join(orphan, ".git")+"\n")
	testharness.WriteSweepFile(t, filepath.Join(registration, "commondir"), "")
	then := time.Now().Add(-10 * time.Minute)
	for _, file := range []string{"locked", "gitdir", "commondir", ""} {
		if err := os.Chtimes(filepath.Join(registration, file), then, then); err != nil {
			t.Fatalf("Chtimes(%s) error = %v", file, err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(registration) })
	if output, err := testharness.AttemptPipelineGit(fixture.Repository, "worktree", "add", "--quiet", "--detach", filepath.Join(t.TempDir(), "probe"), "HEAD"); err == nil {
		t.Fatalf("git created a worktree with a killed add's registration present, so the failure the sweep clears is gone:\n%s", output)
	}

	convergence := fixture.Converge(t)
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
	testharness.RunPipelineGit(t, fixture.Repository, "worktree", "add", "--quiet", "--detach", next, "HEAD")
	testharness.RunPipelineGit(t, fixture.Repository, "worktree", "remove", "--force", next)
	// Sweeping again meets nothing, so a sweep on every pass does not repeat
	// itself.
	repeated := fixture.Converge(t)
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

	fixture := testharness.NewQueuedFixture(t)
	fixture.Forge.SetReplayMerge(true)
	outcome := fixture.Run(t)
	// The forge merges as a replay: the remote's new tip carries the change's
	// content without carrying the promoted commit, so confirming the
	// publication honestly fails even though a merge really happened.
	fixture.Forge.PerformQueuedMerge(t)

	results := fixture.Reconcile(t)
	if len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want the one queued merge examined", results)
	}
	if results[0].Catchup != nil {
		t.Fatalf("catch-up = %#v, want none on a publication that could not be confirmed", results[0].Catchup)
	}
	if local := testharness.PublishedCommit(t, fixture.Repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want it still at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}
}

// The settlement a sweep makes once the forge has merged a queued change is a
// tracker write, and a stalled one used to leave the landed change's item open
// until some later sweep. It is waited out on the run's window, and each attempt
// reads the item first: a closure that timed out after the tracker took it is
// not asked for again, and one that timed out before is.
func TestTheSettlementAfterAForgeMergeIsWaitedOutAndNeverMadeTwice(t *testing.T) {
	t.Parallel()
	for _, landed := range []bool{false, true} {
		fixture := testharness.NewQueuedFixture(t)
		fixture.Sleep = func(context.Context, time.Duration) error { return nil }
		outcome := fixture.Run(t)
		fixture.Forge.PerformQueuedMerge(t)
		tracker := fixture.Tracker.(*orchestratortest.Tracker)
		tracker.CompleteFailures = 2
		tracker.TransientCompleteErr = testharness.TrackerStalled
		tracker.LandsBeforeFailing = landed

		results := fixture.Reconcile(t)
		if len(results) != 1 || results[0].Action != orchestrator.ActionCompleted || results[0].Failure != "" {
			t.Fatalf("landed %t: reconciliation = %#v, want the queued merge settled through the stall", landed, results)
		}
		if !tracker.Closed {
			t.Fatalf("landed %t: the item was left open", landed)
		}
		want := 3
		if landed {
			want = 1
		}
		if closes := testharness.Calls(tracker, "complete"); closes != want {
			t.Errorf("landed %t: closures asked for = %d, want %d: %v", landed, closes, want, tracker.Calls)
		}
		state, err := fixture.Store.Load(outcome.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if waits := state.RetryAttempts(runstate.RetryTrackerWrite); waits == 0 {
			t.Errorf("landed %t: no wait recorded on the run, want each stall's wait there: %#v", landed, state.Retries)
		}
	}
}

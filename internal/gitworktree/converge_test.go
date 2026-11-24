package gitworktree

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The seam this closes. A forge merge leaves the remote target one commit ahead
// of the local one and identical in content, so the checkout an operator reads
// is behind the truth the forge holds — and pulling it forward was theirs to do
// by hand. It is a fast-forward onto a commit that already contains the local
// branch, which is nobody's judgement, so the harness takes it.
func TestCatchUpTargetBringsThePrimaryCheckoutOntoTheForgeMerge(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	promoted := promoteOnMain(t, repository, "feature.txt", "the promoted work\n")
	merge := mergeInRemote(t, remote, "main", promoted)

	catchup, err := manager.CatchUpTarget(context.Background(), "main")
	if err != nil {
		t.Fatalf("CatchUpTarget() error = %v", err)
	}
	if !catchup.Advanced || catchup.Held != "" {
		t.Fatalf("catch-up = %#v, want main advanced", catchup)
	}
	if catchup.LocalCommit != promoted || catchup.RemoteCommit != merge {
		t.Errorf("catch-up = %#v, want %q brought onto %q", catchup, promoted, merge)
	}
	if local := gitLine(t, repository, "rev-parse", "refs/heads/main"); local != merge {
		t.Errorf("local main = %q, want the forge's merge commit %q", local, merge)
	}
	// The primary checkout is on main, so its working tree has to follow the
	// branch rather than be left describing the commit it moved off.
	if head := gitLine(t, repository, "rev-parse", "HEAD"); head != merge {
		t.Errorf("primary HEAD = %q, want %q", head, merge)
	}

	// Repeating it is what a sweep does, and a branch already level with the
	// remote has nothing to catch up to rather than something to fail over.
	repeated, err := manager.CatchUpTarget(context.Background(), "main")
	if err != nil {
		t.Fatalf("CatchUpTarget() repeated error = %v", err)
	}
	if repeated.Advanced || repeated.Held != "" || repeated.RemoteCommit != merge {
		t.Errorf("repeated catch-up = %#v, want nothing to do", repeated)
	}
}

// Export churn is the other half of the same seam: the work tracker's passive
// JSONL dump is rewritten constantly in the primary checkout, and an operator
// who tried to pull was told their local changes would be overwritten. It is
// derived from a store that is authoritative elsewhere, so it is thrown away
// and the pull goes through.
func TestCatchUpTargetDiscardsOnlyTheDeclaredExportChurn(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	commitExport(t, repository, "exported here\n")
	// A commit only the remote has, which rewrites the export: somebody else's
	// machine pushed it, so catching up means taking their version of the file.
	elsewhere := commitElsewhere(t, remote, map[string]string{".beads/issues.jsonl": "exported elsewhere\n"})
	writeFile(t, repository, ".beads/issues.jsonl", "local churn\n")

	catchup, err := manager.CatchUpTarget(context.Background(), "main")
	if err != nil {
		t.Fatalf("CatchUpTarget() error = %v", err)
	}
	if !catchup.Advanced || catchup.Held != "" {
		t.Fatalf("catch-up = %#v, want main advanced through the churn", catchup)
	}
	if !slices.Equal(catchup.Discarded, []string{".beads/issues.jsonl"}) {
		t.Errorf("discarded = %v, want the declared export", catchup.Discarded)
	}
	if local := gitLine(t, repository, "rev-parse", "refs/heads/main"); local != elsewhere {
		t.Errorf("local main = %q, want %q", local, elsewhere)
	}
	if content := readFile(t, repository, ".beads/issues.jsonl"); content != "exported elsewhere\n" {
		t.Errorf("export content = %q, want the version that was caught up to", content)
	}
}

// Churn is discarded because it is derived. Somebody's unsaved edit is not, and
// a branch one commit behind costs far less than an overwritten file, so the
// catch-up is held and says which path held it.
func TestCatchUpTargetHoldsForUncommittedWorkItMayNotDiscard(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	elsewhere := commitElsewhere(t, remote, map[string]string{"README.txt": "someone else's edit\n"})
	writeFile(t, repository, "README.txt", "work in progress\n")
	local := gitLine(t, repository, "rev-parse", "refs/heads/main")

	catchup, err := manager.CatchUpTarget(context.Background(), "main")
	if err != nil {
		t.Fatalf("CatchUpTarget() error = %v", err)
	}
	if catchup.Advanced || !strings.Contains(catchup.Held, "README.txt") {
		t.Fatalf("catch-up = %#v, want it held by the uncommitted README", catchup)
	}
	if catchup.RemoteCommit != elsewhere {
		t.Errorf("catch-up = %#v, want the remote reported at %q", catchup, elsewhere)
	}
	if moved := gitLine(t, repository, "rev-parse", "refs/heads/main"); moved != local {
		t.Errorf("local main = %q, want it left at %q", moved, local)
	}
	if content := readFile(t, repository, "README.txt"); content != "work in progress\n" {
		t.Errorf("uncommitted work = %q, want it untouched", content)
	}
}

// A file the incoming commits do not rewrite survives a fast-forward untouched,
// so holding the catch-up for it would be holding it for nothing. This is the
// ordinary case for the export in this project: the promoted commit and the
// merge commit above it carry the same tree.
func TestCatchUpTargetIgnoresChangesTheIncomingCommitsDoNotTouch(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	promoted := promoteOnMain(t, repository, "feature.txt", "the promoted work\n")
	merge := mergeInRemote(t, remote, "main", promoted)
	writeFile(t, repository, "untouched.txt", "nothing incoming rewrites this\n")

	catchup, err := manager.CatchUpTarget(context.Background(), "main")
	if err != nil {
		t.Fatalf("CatchUpTarget() error = %v", err)
	}
	if !catchup.Advanced || catchup.Held != "" || len(catchup.Discarded) != 0 {
		t.Fatalf("catch-up = %#v, want main advanced with nothing discarded", catchup)
	}
	if local := gitLine(t, repository, "rev-parse", "refs/heads/main"); local != merge {
		t.Errorf("local main = %q, want %q", local, merge)
	}
	if content := readFile(t, repository, "untouched.txt"); content != "nothing incoming rewrites this\n" {
		t.Errorf("untracked file = %q, want it untouched", content)
	}
}

// The one thing here that does need a person. A remote that does not contain
// the local branch is a history somebody rewrote or work the harness never
// promoted, and which of the two is right is not a sweep's answer.
func TestCatchUpTargetHoldsWhenTheRemoteHasDivergedFromTheLocalBranch(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	// The local branch gains a commit the remote does not have, and the remote
	// is rewritten onto an unrelated one. Neither contains the other.
	writeFile(t, repository, "local.txt", "only here\n")
	runGit(t, repository, "add", "local.txt")
	runGit(t, repository, "commit", "-m", "local only")
	local := gitLine(t, repository, "rev-parse", "refs/heads/main")
	rewritten := rewriteRemoteBranch(t, remote, "main")

	catchup, err := manager.CatchUpTarget(context.Background(), "main")
	if err != nil {
		t.Fatalf("CatchUpTarget() error = %v", err)
	}
	if catchup.Advanced {
		t.Fatalf("catch-up = %#v, want the divergence held", catchup)
	}
	if !strings.Contains(catchup.Held, rewritten) || !strings.Contains(catchup.Held, local) {
		t.Errorf("held = %q, want both commits named", catchup.Held)
	}
	if moved := gitLine(t, repository, "rev-parse", "refs/heads/main"); moved != local {
		t.Errorf("local main = %q, want it left at %q", moved, local)
	}
}

// TargetDivergence is the catch-up's question asked without the catch-up: it
// holds on a diverged remote in the catch-up's words, answers nothing held once
// the remote only gained a merge above the local branch, and moves the branch in
// neither case — the resumed run's own catch-up is what moves it.
func TestTargetDivergenceAsksTheCatchUpsQuestionAndMovesNothing(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	promoted := promoteOnMain(t, repository, "feature.txt", "the promoted work\n")
	mergeInRemote(t, remote, "main", promoted)

	fastForward, err := manager.TargetDivergence(context.Background(), "main")
	if err != nil {
		t.Fatalf("TargetDivergence() error = %v", err)
	}
	if fastForward.Held != "" || fastForward.Advanced {
		t.Fatalf("divergence = %#v, want a remote the local branch fast-forwards onto held on nothing", fastForward)
	}
	if local := gitLine(t, repository, "rev-parse", "refs/heads/main"); local != promoted {
		t.Fatalf("local main = %q, want it left at %q", local, promoted)
	}

	rewritten := rewriteRemoteBranch(t, remote, "main")
	diverged, err := manager.TargetDivergence(context.Background(), "main")
	if err != nil {
		t.Fatalf("TargetDivergence() error = %v", err)
	}
	if !strings.Contains(diverged.Held, rewritten) || !strings.Contains(diverged.Held, promoted) {
		t.Fatalf("held = %q, want both commits named", diverged.Held)
	}
	if local := gitLine(t, repository, "rev-parse", "refs/heads/main"); local != promoted {
		t.Fatalf("local main = %q, want it left at %q", local, promoted)
	}
}

// A remote that refused the key is ErrRemoteAuthRefused whichever command met
// it, and a remote refusing for any other reason is not.
func TestARemoteRefusingTheCredentialIsItsOwnClass(t *testing.T) {
	t.Parallel()

	args := []string{"-C", "/repository", "-c", "core.hooksPath=/dev/null", "push", "origin", "abc:refs/heads/run"}
	refused := authRefusal(args, execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 128,
		Stderr:   "git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.\n",
	})
	if !errors.Is(refused, ErrRemoteAuthRefused) || !strings.Contains(refused.Error(), "git push exited 128") || !strings.Contains(refused.Error(), "Permission denied (publickey)") {
		t.Fatalf("authRefusal(publickey) = %v, want the refused key named with the command", refused)
	}
	if errors.Is(refused, ErrRemoteCredentialNotPresented) {
		t.Fatalf("authRefusal(publickey) = %v, want a key that was presented and refused", refused)
	}
	for _, stderr := range []string{"No user exists for uid 501", "Host key verification failed."} {
		unasked := authRefusal(args, execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 128, Stderr: stderr})
		if !errors.Is(unasked, ErrRemoteAuthRefused) || !errors.Is(unasked, ErrRemoteCredentialNotPresented) {
			t.Fatalf("authRefusal(%q) = %v, want it marked as ending before a credential was presented", stderr, unasked)
		}
	}
	if rejected := authRefusal(args, execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   " ! [rejected]        abc -> run (non-fast-forward)\n",
	}); rejected != nil {
		t.Fatalf("authRefusal(rejected push) = %v, want a rejected push left to its own wording", rejected)
	}
	if succeeded := authRefusal(args, execution.ProcessResult{Status: execution.ProcessSucceeded}); succeeded != nil {
		t.Fatalf("authRefusal(success) = %v", succeeded)
	}
}

// A target no checkout is on is moved as a compare-and-swap on where it was
// read, exactly as the promotion moves one. There is no working tree to keep
// consistent, and nothing about the primary checkout's own state can hold it.
func TestCatchUpTargetAdvancesABranchNoCheckoutIsOn(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	runGit(t, repository, "checkout", "-b", "elsewhere")
	elsewhere := commitElsewhere(t, remote, map[string]string{"remote-only.txt": "pushed elsewhere\n"})
	writeFile(t, repository, "README.txt", "uncommitted, and on another branch\n")

	catchup, err := manager.CatchUpTarget(context.Background(), "main")
	if err != nil {
		t.Fatalf("CatchUpTarget() error = %v", err)
	}
	if !catchup.Advanced || catchup.Held != "" {
		t.Fatalf("catch-up = %#v, want main advanced", catchup)
	}
	if local := gitLine(t, repository, "rev-parse", "refs/heads/main"); local != elsewhere {
		t.Errorf("local main = %q, want %q", local, elsewhere)
	}
	if content := readFile(t, repository, "README.txt"); content != "uncommitted, and on another branch\n" {
		t.Errorf("working tree = %q, want the checkout untouched", content)
	}
}

// A repository with no counterpart on the remote has nothing to catch up to,
// which is an observation rather than a failure: it is the ordinary state of a
// project that has never published.
func TestCatchUpTargetHoldsWhenTheRemoteHasNoSuchBranch(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	runGit(t, remote, "update-ref", "-d", "refs/heads/main")

	catchup, err := manager.CatchUpTarget(context.Background(), "main")
	if err != nil {
		t.Fatalf("CatchUpTarget() error = %v", err)
	}
	if catchup.Advanced || !strings.Contains(catchup.Held, "does not exist on origin") {
		t.Fatalf("catch-up = %#v, want nothing to catch up to", catchup)
	}
}

// Dead local branches: a run branch whose work the target already carries has
// nothing on it that deleting it could lose. One that carries unpromoted work
// is somebody's preserved change and is kept, whatever a record says about it.
func TestRemoveMergedBranchDeletesOnlyWhatTheTargetAlreadyCarries(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	merged := branchAt(t, repository, "yoyodyne/merged", "landed.txt", "this work landed\n")
	runGit(t, repository, "checkout", "main")
	runGit(t, repository, "merge", "--ff-only", merged)
	preserved := branchAt(t, repository, "yoyodyne/preserved", "unpromoted.txt", "nothing promoted this\n")
	runGit(t, repository, "checkout", "main")

	removal, err := manager.RemoveMergedBranch(context.Background(), "yoyodyne/merged", "main")
	if err != nil {
		t.Fatalf("RemoveMergedBranch() error = %v", err)
	}
	if !removal.Removed || removal.Kept != "" || removal.Commit != merged {
		t.Fatalf("removal = %#v, want the merged branch deleted at %q", removal, merged)
	}
	if refs := strings.TrimSpace(gitOutput(t, repository, "for-each-ref", "--format=%(refname)", "refs/heads/yoyodyne/merged")); refs != "" {
		t.Errorf("merged branch survived: %q", refs)
	}

	kept, err := manager.RemoveMergedBranch(context.Background(), "yoyodyne/preserved", "main")
	if err != nil {
		t.Fatalf("RemoveMergedBranch() preserved error = %v", err)
	}
	if kept.Removed || !strings.Contains(kept.Kept, "not contained in main") || kept.Commit != preserved {
		t.Fatalf("removal = %#v, want the unpromoted branch kept", kept)
	}

	// A branch a previous sweep already deleted is nothing to do rather than a
	// removal, which is what lets a caller stay quiet about it.
	gone, err := manager.RemoveMergedBranch(context.Background(), "yoyodyne/merged", "main")
	if err != nil {
		t.Fatalf("RemoveMergedBranch() repeated error = %v", err)
	}
	if gone.Commit != "" || gone.Removed || gone.Kept != "" {
		t.Errorf("removal = %#v, want nothing to do", gone)
	}
}

// A branch a working tree still holds is what somebody is using it for, and a
// sweep that failed over it would fail on every later pass too.
func TestRemoveMergedBranchKeepsABranchAWorktreeStillHolds(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	merged := branchAt(t, repository, "yoyodyne/held", "landed.txt", "this work landed\n")
	runGit(t, repository, "checkout", "main")
	runGit(t, repository, "merge", "--ff-only", merged)
	checkout := filepath.Join(t.TempDir(), "held")
	runGit(t, repository, "worktree", "add", checkout, "yoyodyne/held")

	removal, err := manager.RemoveMergedBranch(context.Background(), "yoyodyne/held", "main")
	if err != nil {
		t.Fatalf("RemoveMergedBranch() error = %v", err)
	}
	if removal.Removed || !strings.Contains(removal.Kept, "still checked out in") {
		t.Fatalf("removal = %#v, want the checked-out branch kept", removal)
	}
	if commit := gitLine(t, repository, "rev-parse", "refs/heads/yoyodyne/held"); commit != merged {
		t.Errorf("branch = %q, want it left at %q", commit, merged)
	}
}

func TestRemoveMergedBranchRefusesABranchThatIsItsOwnTarget(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	if _, err := manager.RemoveMergedBranch(context.Background(), "main", "main"); err == nil {
		t.Fatal("RemoveMergedBranch() accepted a branch that is its own target")
	}
}

// The catch-up is a fast-forward and nothing else, so a Git that refuses one is
// a failure rather than a branch quietly left where it was.
func TestCatchUpTargetReportsARefusedFastForward(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	commitElsewhere(t, remote, map[string]string{"remote-only.txt": "pushed elsewhere\n"})
	runner := &commandFailureRunner{delegate: execution.OSProcessRunner{}, failOn: "merge", armed: true}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Remote:         "origin",
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	catchup, err := manager.CatchUpTarget(context.Background(), "main")
	if err == nil {
		t.Fatalf("CatchUpTarget() error = nil, catch-up = %#v", catchup)
	}
	if catchup.Advanced {
		t.Errorf("catch-up = %#v, want nothing reported as advanced", catchup)
	}
}

func newConvergeManager(t *testing.T, repository string) *Manager {
	t.Helper()
	manager, err := New(Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          filepath.Join(t.TempDir(), "worktrees"),
		Remote:                "origin",
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
		Timeout:               testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return manager
}

// promoteOnMain is what a published run leaves behind just before the forge
// merges: the work committed on the local main, which is the authoritative
// branch and the one that moves first, and the same commit on the remote as the
// branch the pull request carries.
func promoteOnMain(t *testing.T, repository, relative, content string) string {
	t.Helper()
	writeFile(t, repository, relative, content)
	runGit(t, repository, "add", relative)
	runGit(t, repository, "commit", "-m", "the promoted work")
	promoted := gitLine(t, repository, "rev-parse", "refs/heads/main")
	runGit(t, repository, "push", "origin", promoted+":refs/heads/yoyodyne/run")
	return promoted
}

// commitElsewhere puts a commit on the remote's main that this repository has
// never seen, which is what another machine pushing looks like from here.
func commitElsewhere(t *testing.T, remote string, files map[string]string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "elsewhere")
	runGit(t, filepath.Dir(clone), "clone", remote, clone)
	runGit(t, clone, "config", "user.name", "Someone Else")
	runGit(t, clone, "config", "user.email", "someone@example.invalid")
	disableBackgroundMaintenance(t, clone)
	for relative, content := range files {
		writeFile(t, clone, relative, content)
		runGit(t, clone, "add", relative)
	}
	runGit(t, clone, "commit", "-m", "work from elsewhere")
	runGit(t, clone, "push", "origin", "HEAD:refs/heads/main")
	return gitLine(t, clone, "rev-parse", "HEAD")
}

// rewriteRemoteBranch replaces the remote branch with an unrelated history,
// which is what somebody force-pushing over it leaves behind.
func rewriteRemoteBranch(t *testing.T, remote, branch string) string {
	t.Helper()
	tree := gitLine(t, remote, "rev-parse", "refs/heads/"+branch+"^{tree}")
	commit := gitLine(t, remote,
		"-c", "user.name=Someone Else",
		"-c", "user.email=someone@example.invalid",
		"commit-tree", tree, "-m", "an unrelated history")
	runGit(t, remote, "update-ref", "refs/heads/"+branch, commit)
	return commit
}

// branchAt creates a branch carrying one commit of its own and returns its tip.
func branchAt(t *testing.T, repository, branch, relative, content string) string {
	t.Helper()
	runGit(t, repository, "checkout", "-b", branch)
	writeFile(t, repository, relative, content)
	runGit(t, repository, "add", relative)
	runGit(t, repository, "commit", "-m", "work on "+branch)
	return gitLine(t, repository, "rev-parse", "refs/heads/"+branch)
}

// commitExport puts the project's declared control-plane export under version
// control, which is what makes churn in it discardable rather than untracked
// work with no committed version behind it.
func commitExport(t *testing.T, repository, content string) {
	t.Helper()
	writeFile(t, repository, ".beads/issues.jsonl", content)
	runGit(t, repository, "add", ".beads/issues.jsonl")
	runGit(t, repository, "commit", "-m", "record the work tracker export")
	runGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
}

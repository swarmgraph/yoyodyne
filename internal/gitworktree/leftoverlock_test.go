package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// lockedCheckout is a run's checkout holding an uncommitted change, with a Git
// lock file of the given name left in its own Git directory, as a Git process
// killed part-way through a write leaves one.
func lockedCheckout(t *testing.T, name string) (*Manager, Worktree, string) {
	t.Helper()
	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-locked", BaseRef: "HEAD", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "the developer's change\n")
	gitDirectory := strings.TrimSpace(gitOutput(t, worktree.Path, "rev-parse", "--absolute-git-dir"))
	lock := filepath.Join(gitDirectory, name)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", lock, err)
	}
	return manager, worktree, lock
}

// clearingsOn is a context that keeps what the manager says about each lock.
func clearingsOn(t *testing.T) (context.Context, func() []GitLockClearing) {
	t.Helper()
	var mu sync.Mutex
	var said []GitLockClearing
	ctx := WithGitLockClearings(context.Background(), func(clearing GitLockClearing) {
		mu.Lock()
		defer mu.Unlock()
		said = append(said, clearing)
	})
	return ctx, func() []GitLockClearing {
		mu.Lock()
		defer mu.Unlock()
		return append([]GitLockClearing(nil), said...)
	}
}

// index.lock left behind in the checkout's Git directory by a Git process that is gone. The harness removes
// it, says so, and the commit it was about to make goes ahead.
func TestALeftoverIndexLockIsRemovedAndTheCommitGoesAhead(t *testing.T) {
	t.Parallel()
	manager, worktree, lock := lockedCheckout(t, "index.lock")
	var asked []string
	manager.lockUsers = func(_ context.Context, lock string, places []string) ([]gitLockUser, error) {
		asked = append(asked, lock)
		if len(places) != 2 || places[0] != worktree.Path {
			t.Errorf("places = %v, want the checkout and its Git directory", places)
		}
		return nil, nil
	}
	ctx, said := clearingsOn(t)

	commit, err := manager.CommitAttempt(ctx, worktree, "")
	if err != nil {
		t.Fatalf("CommitAttempt() over a leftover lock error = %v", err)
	}
	if commit == "" || commit == worktree.BaseCommit {
		t.Fatalf("CommitAttempt() = %q, want a commit above the base", commit)
	}
	if _, err := os.Lstat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the leftover lock is still there: %v", err)
	}
	if len(asked) != 1 || asked[0] != lock {
		t.Fatalf("asked about %v, want only %s", asked, lock)
	}
	clearings := said()
	if len(clearings) != 1 || !clearings[0].Removed || clearings[0].Path != lock || clearings[0].At.IsZero() {
		t.Fatalf("said %#v, want the one lock removed", clearings)
	}
	if !strings.Contains(clearings[0].Why, "removed") || !strings.Contains(clearings[0].Why, "no live process") {
		t.Fatalf("why = %q, want it to say it was removed because nothing live was using it", clearings[0].Why)
	}
}

// A lock a live process is using is the process's, and the harness leaves it
// exactly where it is, says which process, and stops with the sentinel that
// marks the stop as the machine's rather than the change's.
func TestALockALiveProcessIsUsingIsLeftAloneAndSaysWhy(t *testing.T) {
	t.Parallel()
	manager, worktree, lock := lockedCheckout(t, "index.lock")
	manager.lockUsers = func(context.Context, string, []string) ([]gitLockUser, error) {
		return []gitLockUser{{PID: 4242, Command: "git"}}, nil
	}
	ctx, said := clearingsOn(t)

	_, err := manager.CommitAttempt(ctx, worktree, "")
	if !errors.Is(err, ErrGitLockHeld) {
		t.Fatalf("CommitAttempt() error = %v, want ErrGitLockHeld", err)
	}
	if !strings.Contains(err.Error(), "git (process 4242)") {
		t.Fatalf("error = %q, want it to name the live process", err)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("the live lock was touched: %v", statErr)
	}
	clearings := said()
	if len(clearings) != 1 || clearings[0].Removed || !strings.Contains(clearings[0].Why, "left Git's lock file") {
		t.Fatalf("said %#v, want the one lock left alone with why", clearings)
	}
	// Nothing was committed: the change is still the developer's, uncommitted.
	if head := strings.TrimSpace(gitOutput(t, worktree.Path, "rev-parse", "HEAD")); head != worktree.BaseCommit {
		t.Fatalf("HEAD = %s, want the base %s untouched", head, worktree.BaseCommit)
	}
}

// Not being able to ask is not an answer that a lock is free: it is left alone
// and the stop says what could not be asked.
func TestALockNobodyCouldAskAboutIsLeftAlone(t *testing.T) {
	t.Parallel()
	manager, worktree, lock := lockedCheckout(t, "HEAD.lock")
	manager.lockUsers = func(context.Context, string, []string) ([]gitLockUser, error) {
		return nil, errors.New("lsof could not be run: executable file not found")
	}

	_, err := manager.CommitAttempt(context.Background(), worktree, "")
	if !errors.Is(err, ErrGitLockHeld) || !strings.Contains(err.Error(), "could not find out whether a live process is using it") {
		t.Fatalf("CommitAttempt() error = %v, want ErrGitLockHeld saying what could not be asked", err)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("the lock was touched: %v", statErr)
	}
}

// The move of a change onto its target for its author to reconcile is the step
// the lock stopped in the field; it clears a leftover lock before it writes.
func TestTheMoveForReconcilingClearsALeftoverLockFirst(t *testing.T) {
	t.Parallel()
	manager, worktree, lock := lockedCheckout(t, "index.lock")
	manager.lockUsers = func(context.Context, string, []string) ([]gitLockUser, error) { return nil, nil }
	ctx, said := clearingsOn(t)

	if _, err := manager.ReplayForRepair(ctx, worktree, ""); err != nil {
		t.Fatalf("ReplayForRepair() over a leftover lock error = %v", err)
	}
	if _, err := os.Lstat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the leftover lock is still there: %v", err)
	}
	if clearings := said(); len(clearings) != 1 || !clearings[0].Removed {
		t.Fatalf("said %#v, want the lock removed", clearings)
	}
}

// The real question, put to lsof: a lock this process holds open is in use,
// and the same lock once closed is left over.
func TestLsofTellsALiveLockFromALeftoverOne(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is not installed here")
	}
	manager, worktree, lock := lockedCheckout(t, "index.lock")
	held, err := os.OpenFile(lock, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}

	_, err = manager.CommitAttempt(context.Background(), worktree, "")
	if !errors.Is(err, ErrGitLockHeld) {
		_ = held.Close()
		t.Fatalf("CommitAttempt() with the lock held open error = %v, want ErrGitLockHeld", err)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("the held lock was touched: %v", statErr)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CommitAttempt(context.Background(), worktree, ""); err != nil {
		t.Fatalf("CommitAttempt() once the lock was let go error = %v", err)
	}
	if _, err := os.Lstat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the leftover lock is still there: %v", err)
	}
}

func TestReadLsofTellsNothingOpenFromNoAnswer(t *testing.T) {
	t.Parallel()
	processes, err := readLsof(execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "p12\ncgit\nfcwd\nn/a/checkout\np13\nczsh\nn/b\n"})
	if err != nil || len(processes) != 2 || processes[0].PID != 12 || processes[0].Command != "git" || processes[0].Names[0] != "/a/checkout" {
		t.Fatalf("readLsof() = %#v, %v", processes, err)
	}
	if processes, err := readLsof(execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}); err != nil || len(processes) != 0 {
		t.Fatalf("readLsof() of nothing open = %#v, %v, want nothing and no error", processes, err)
	}
	if _, err := readLsof(execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "lsof: WARNING: can't stat()"}); err == nil {
		t.Fatal("readLsof() of a failure that said something = no error, want one")
	}
	if !withinAny("/a/checkout/sub", []string{"/a/checkout"}) || withinAny("/a/checkout-other", []string{"/a/checkout"}) {
		t.Fatal("withinAny() does not keep to the checkout")
	}
}

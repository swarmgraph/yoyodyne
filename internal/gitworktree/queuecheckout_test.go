package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// slowedQueueRepository is queueRepository with a tree whose checkout is slow:
// every file under slow/ passes through a filter that waits before writing it,
// so a checkout of the target takes seconds on any machine, as a whole
// repository does on a loaded one.
func slowedQueueRepository(t *testing.T) (repository, head string) {
	t.Helper()
	repository, head = queueRepository(t)
	writeFile(t, repository, ".gitattributes", "slow/* filter=slow\n")
	for index := 0; index < 20; index++ {
		writeFile(t, repository, fmt.Sprintf("slow/file-%02d.txt", index), "slowly written\n")
	}
	runGit(t, repository, "add", "--all")
	runGit(t, repository, "commit", "-m", "a tree that is slow to check out")
	runGit(t, repository, "config", "filter.slow.smudge", "sleep 0.1; cat")
	runGit(t, repository, "config", "filter.slow.clean", "cat")
	return repository, head
}

// firstCheckoutCut holds the first `git worktree add` to a limit the slowed
// checkout cannot finish inside, and leaves every later one, and every other
// command, on the limit the manager gave it. It keeps the limit the manager
// gave each add.
type firstCheckoutCut struct {
	delegate execution.ProcessRunner
	limit    time.Duration

	mu     sync.Mutex
	limits []time.Duration
}

func (r *firstCheckoutCut) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if containsArguments(command.Args, "worktree", "add") {
		r.mu.Lock()
		r.limits = append(r.limits, command.Timeout)
		first := len(r.limits) == 1
		r.mu.Unlock()
		if first {
			command.Timeout = r.limit
		}
	}
	return r.delegate.Run(ctx, command, observer)
}

func (r *firstCheckoutCut) addLimits() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.limits...)
}

// A candidate checkout stopped at its time limit is said in plain words — the
// limit and how far it got — and is removed at once, registration and all, so
// the very next attempt builds the candidate. The limit is the one a run's
// worktree creation gets, sized for the whole tree, and a retry's is doubled.
func TestACandidateCheckoutStoppedAtItsLimitIsClearedAndBuiltOnTheNextAttempt(t *testing.T) {
	t.Parallel()

	repository, head := slowedQueueRepository(t)
	runner := &firstCheckoutCut{delegate: execution.OSProcessRunner{}, limit: 500 * time.Millisecond}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	request := QueueCandidateRequest{Entry: testQueueEntry, TargetBranch: "main", Heads: []string{head}}

	_, err = manager.BuildQueueCandidate(ctx, request)
	var stopped *QueueCheckoutStopped
	if !errors.As(err, &stopped) || !errors.Is(err, ErrCheckoutKilled) || !QueueCheckoutRetryable(err) {
		t.Fatalf("BuildQueueCandidate() error = %v, want the checkout reported as stopped at its time limit", err)
	}
	// The runner here cut the add short of the limit the manager gave it, so the
	// limit named is the one the manager gave, which is the one a real stop
	// happens at.
	if limits := runner.addLimits(); len(limits) != 1 || stopped.Limit != limits[0] ||
		!strings.Contains(err.Error(), "stopped at its time limit of "+limits[0].Round(time.Second).String()) {
		t.Fatalf("BuildQueueCandidate() error = %q, want the limit %v the checkout ran under named", err, limits)
	}
	if !strings.Contains(err.Error(), "files written") && !strings.Contains(err.Error(), "before Git reported writing any of it") {
		t.Fatalf("BuildQueueCandidate() error = %q, want how far the checkout got", err)
	}
	if strings.Contains(err.Error(), "exit code") || strings.Contains(err.Error(), "Updating files") {
		t.Fatalf("BuildQueueCandidate() error = %q, want the harness's own sentence rather than the runner's exit code or Git's progress meter", err)
	}
	path := filepath.Join(manager.worktreeRoot, queueCandidateDirectoryName(testQueueEntry))
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("Lstat() = %v, want the half-made checkout removed", err)
	}
	if listing := gitLine(t, repository, "worktree", "list", "--porcelain"); strings.Contains(listing, path) {
		t.Fatalf("worktree list = %q, want the half-made checkout's registration removed with it", listing)
	}

	candidate, err := manager.BuildQueueCandidate(WithQueueCheckoutRetry(ctx, 1), request)
	if err != nil {
		t.Fatalf("BuildQueueCandidate() on the retry error = %v, want the candidate built", err)
	}
	if candidate.Path != path || readFile(t, candidate.Path, "change.txt") != "the change\n" || readFile(t, candidate.Path, "slow/file-19.txt") != "slowly written\n" {
		t.Fatalf("candidate = %#v, want the whole tree with the change merged onto it", candidate)
	}

	files, counted, err := manager.checkoutFiles(ctx, candidate.BaseCommit)
	if err != nil || !counted {
		t.Fatalf("checkoutFiles() = %d, %t, %v", files, counted, err)
	}
	first := testGitBudget + time.Duration(files)*checkoutFileBudget
	if limits := runner.addLimits(); len(limits) != 2 || limits[0] != first || limits[1] != 2*first {
		t.Fatalf("checkout limits = %v, want %s for the whole tree of %d files and then twice that for the retry", limits, first, files)
	}
}

// What Git reported of a checkout's progress is read back as how far it got,
// from the last report rather than the first.
func TestCheckoutProgressIsTheLastReportGitMade(t *testing.T) {
	t.Parallel()

	written, total, ok := checkoutProgress("Preparing worktree (detached HEAD 1234567)\nUpdating files:   0% (1/2671)\rUpdating files:   1% (37/2671)\r")
	if !ok || written != 37 || total != 2671 {
		t.Fatalf("checkoutProgress() = %d, %d, %t; want 37 of 2671", written, total, ok)
	}
	if _, _, ok := checkoutProgress("Preparing worktree (detached HEAD 1234567)\n"); ok {
		t.Fatal("checkoutProgress() read progress from output that reported none")
	}
	stopped := &QueueCheckoutStopped{Limit: 2*time.Minute + 43*time.Second, Files: 2671, Counted: true, Written: 37, Total: 2671}
	if got, want := stopped.Error(), "the checkout was stopped at its time limit of 2m43s, 1% of the way through (37 of 2671 files written)"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

// A retry's limit doubles up to its cap on doublings, and a manager left to
// the default never has a doubled limit above the retry cap, nor below what
// its first attempt got.
func TestARetriedCandidateCheckoutGetsALongerLimit(t *testing.T) {
	t.Parallel()

	named := &Manager{timeout: time.Minute}
	first := named.queueCheckoutTimeout(100, true, 0)
	if want := time.Minute + 100*checkoutFileBudget; first != want {
		t.Fatalf("first limit = %s, want %s", first, want)
	}
	for retry, want := range []time.Duration{first, 2 * first, 4 * first, 4 * first} {
		if got := named.queueCheckoutTimeout(100, true, retry); got != want {
			t.Fatalf("limit on retry %d = %s, want %s", retry, got, want)
		}
	}
	unnamed := &Manager{}
	base := unnamed.queueCheckoutTimeout(1_000_000, true, 0)
	if got := unnamed.queueCheckoutTimeout(1_000_000, true, 2); got < base || (got > maxQueueCheckoutRetryLimit && got != base) {
		t.Fatalf("doubled limit = %s with a first limit of %s, want it capped at %s but never below the first", got, base, maxQueueCheckoutRetryLimit)
	}
}

// An add killed before it wrote the checkout's .git file leaves a registration
// `git worktree remove` cannot find by the path and `git worktree prune` skips
// while it is locked as initializing. Removing the checkout clears it too, so
// the next add at the same path is not refused over it.
func TestRemovingACheckoutClearsTheRegistrationOfAnAddKilledBeforeItsGitFile(t *testing.T) {
	t.Parallel()

	repository, head := queueRepository(t)
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	path := filepath.Join(manager.worktreeRoot, queueCandidateDirectoryName(testQueueEntry))
	runGit(t, repository, "worktree", "add", "--detach", path, head)
	// Put the entry back the way a kill early in the add leaves it: still
	// locked as initializing, no index, HEAD not yet written, and no .git file
	// in the checkout.
	entry := strings.TrimSpace(gitLine(t, path, "rev-parse", "--absolute-git-dir"))
	for _, name := range []string{"index", "HEAD"} {
		if err := os.Remove(filepath.Join(entry, name)); err != nil {
			t.Fatalf("Remove(%s) error = %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(entry, "HEAD"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(HEAD) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "locked"), []byte("initializing\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(locked) error = %v", err)
	}
	if err := os.Remove(filepath.Join(path, ".git")); err != nil {
		t.Fatalf("Remove(.git) error = %v", err)
	}

	ctx, lease, err := manager.leaseRegistry(context.Background())
	if err != nil {
		t.Fatalf("leaseRegistry() error = %v", err)
	}
	removeErr := manager.removeCheckout(ctx, path)
	_ = lease.release()
	if removeErr != nil {
		t.Fatalf("removeCheckout() error = %v", removeErr)
	}
	if _, err := os.Lstat(entry); !os.IsNotExist(err) {
		t.Fatalf("Lstat(%s) = %v, want the killed add's registration removed", entry, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("Lstat(%s) = %v, want the half-made checkout removed", path, err)
	}
	runGit(t, repository, "worktree", "add", "--detach", path, head)
}

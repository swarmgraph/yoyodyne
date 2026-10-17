package gitworktree

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A replay's budget is bought for what it does: the budget a local Git command
// gets, plus the idle figure again for each commit it replays and the
// checkout's per-file allowance for each file it writes. A replay nobody could
// count is bought for the uncounted figures rather than for nothing.
func TestAReplaysBudgetGrowsWithTheCommitsAndFilesItReplays(t *testing.T) {
	t.Parallel()

	manager := &Manager{timeout: 7 * time.Second}
	for _, tc := range []struct {
		name string
		work replayWork
		want time.Duration
	}{
		{name: "nothing to replay", work: replayWork{counted: true}, want: 7 * time.Second},
		{name: "one commit writing two files", work: replayWork{commits: 1, files: 2, counted: true}, want: 7*time.Second + 30*time.Second + 100*time.Millisecond},
		{name: "three commits writing sixty-two files", work: replayWork{commits: 3, files: 62, counted: true}, want: 7*time.Second + 90*time.Second + 3100*time.Millisecond},
		{name: "uncounted", work: replayWork{commits: uncountedReplayCommits, files: uncountedCheckoutFiles}, want: 7*time.Second + 300*time.Second + 100*time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := manager.replayTimeout(tc.work); got != tc.want {
				t.Fatalf("replayTimeout(%+v) = %s, want %s", tc.work, got, tc.want)
			}
		})
	}
}

// On a manager left to the default budget, a replay's budget is never more
// than maxReplayBudget, however much it bought and however often it was
// doubled; a manager given a budget is not capped.
func TestAReplaysBudgetIsCappedOnTheDefaultBudget(t *testing.T) {
	t.Parallel()

	huge := replayWork{commits: 1000, files: 100000, counted: true}
	if got := (&Manager{}).retriedReplayTimeout(huge, 9); got != maxReplayBudget {
		t.Fatalf("retriedReplayTimeout() on the default budget = %s, want the %s cap", got, maxReplayBudget)
	}
	small := replayWork{commits: 1, counted: true}
	if got := (&Manager{}).retriedReplayTimeout(small, 0); got < defaultTimeout+replayCommitBudget || got > maxReplayBudget {
		t.Fatalf("retriedReplayTimeout() for one commit = %s, want at least %s and at most the cap", got, defaultTimeout+replayCommitBudget)
	}
	named := &Manager{timeout: time.Minute}
	if got, want := named.retriedReplayTimeout(huge, 9), 8*named.replayTimeout(huge); got != want {
		t.Fatalf("retriedReplayTimeout() on a named budget = %s, want %s uncapped", got, want)
	}
}

// The rebase a replay runs is given the budget its work bought, doubled for
// each retry the caller marked, up to maxReplayDoublings; the replay's other
// Git commands keep the budget one local command gets.
func TestTheReplaysRebaseIsGivenItsDerivedBudgetDoubledForEachRetry(t *testing.T) {
	t.Parallel()

	// One commit, the developer's, writing work.txt; and one file the target
	// changed, concurrent.txt.
	bought := testGitBudget + replayCommitBudget + 2*checkoutFileBudget
	for _, tc := range []struct {
		retry int
		want  time.Duration
	}{
		{retry: 0, want: bought},
		{retry: 1, want: 2 * bought},
		{retry: 2, want: 4 * bought},
		{retry: 9, want: 8 * bought},
	} {
		t.Run("retry "+strconv.Itoa(tc.retry), func(t *testing.T) {
			t.Parallel()

			repository := newRepository(t)
			runner := &rebaseBudgetRunner{delegate: execution.OSProcessRunner{}}
			manager, err := New(Options{Runner: runner, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: testGitBudget})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			worktree, err := manager.Create(context.Background(), CreateRequest{
				RunID: testRunID, WorkItemID: "yoyodyne-budget", BaseRef: "main", TargetBranch: "main",
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			writeFile(t, worktree.Path, "work.txt", "developer work\n")
			writeFile(t, repository, "concurrent.txt", "somebody else's work\n")
			runGit(t, repository, "add", "concurrent.txt")
			runGit(t, repository, "commit", "-m", "concurrent target change")

			replay, err := manager.RebaseOntoTarget(WithReplayRetry(context.Background(), tc.retry), worktree, "")
			if err != nil {
				t.Fatalf("RebaseOntoTarget() error = %v", err)
			}
			if !replay.Moved() {
				t.Fatalf("RebaseOntoTarget() = %#v, want the change replayed", replay)
			}
			rebase, others := runner.budgets()
			if rebase != tc.want {
				t.Fatalf("the rebase's budget = %s, want %s", rebase, tc.want)
			}
			for _, other := range others {
				if other != testGitBudget {
					t.Fatalf("another of the replay's commands had budget %s, want the %s one local command gets", other, testGitBudget)
				}
			}
		})
	}
}

// rebaseBudgetRunner keeps the budget the `git rebase` was handed, and the
// budgets of every other command it ran.
type rebaseBudgetRunner struct {
	delegate execution.ProcessRunner

	mu     sync.Mutex
	rebase time.Duration
	others []time.Duration
}

func (r *rebaseBudgetRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.mu.Lock()
	if hasArgument(command.Args, "rebase") && !hasArgument(command.Args, "--abort") {
		r.rebase = command.Timeout
	} else if !hasArgument(command.Args, "add") || !hasArgument(command.Args, "worktree") {
		r.others = append(r.others, command.Timeout)
	}
	r.mu.Unlock()
	return r.delegate.Run(ctx, command, observer)
}

func (r *rebaseBudgetRunner) budgets() (time.Duration, []time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebase, append([]time.Duration(nil), r.others...)
}

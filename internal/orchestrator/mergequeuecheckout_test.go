package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// slowFirstCheckout stops the first `git worktree add` it is handed at a limit
// the slowed checkout cannot finish inside, as a loaded machine stops one at
// the limit it was given, and leaves every later add and every other command
// on the limit the manager gave it.
type slowFirstCheckout struct {
	delegate execution.ProcessRunner
	limit    time.Duration

	mu   sync.Mutex
	adds int
}

func (r *slowFirstCheckout) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if index := slices.Index(command.Args, "worktree"); index >= 0 && index+1 < len(command.Args) && command.Args[index+1] == "add" {
		r.mu.Lock()
		r.adds++
		first := r.adds == 1
		r.mu.Unlock()
		if first {
			command.Timeout = r.limit
		}
	}
	return r.delegate.Run(ctx, command, observer)
}

func (r *slowFirstCheckout) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.adds
}

// A candidate checkout cut off at its time limit is cleared and asked for again
// within the same call, so the candidate is built and its checks run without
// waiting for the queue's next round, and the cut-off is said in plain words on
// the outcome rather than charged to the change.
func TestACandidateCheckoutCutOffByItsLimitIsRetriedAndItsChecksStartInTheSamePass(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	// The target's tree is slow to check out: every file under slow/ passes
	// through a filter that waits before writing it.
	writeQueueFile(t, f.repository, ".gitattributes", "slow/* filter=slow\n")
	for index := 0; index < 20; index++ {
		writeQueueFile(t, f.repository, fmt.Sprintf("slow/file-%02d.txt", index), "slowly written\n")
	}
	runPipelineGit(t, f.repository, "add", "--all")
	runPipelineGit(t, f.repository, "commit", "-m", "a tree that is slow to check out")
	runPipelineGit(t, f.repository, "config", "filter.slow.smudge", "sleep 0.1; cat")
	runPipelineGit(t, f.repository, "config", "filter.slow.clean", "cat")

	runner := &slowFirstCheckout{delegate: execution.OSProcessRunner{}, limit: 300 * time.Millisecond}
	candidates, err := gitworktree.New(gitworktree.Options{
		Runner:         runner,
		RepositoryRoot: f.repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "queue"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	f.worker.Candidates = candidates

	verification, err := f.work()
	if err != nil {
		t.Fatalf("Work() error = %v, want the cut-off checkout retried and the candidate verified", err)
	}
	if !verification.Verified || verification.Generation.Number != 1 || verification.Generation.CheckRun == nil {
		t.Fatalf("Work() = %#v, want the first generation built and verified in this call", verification)
	}
	if runner.count() != 2 {
		t.Fatalf("checkout asked for %d times, want the cut-off one and its retry", runner.count())
	}
	if f.checks.calls() != 1 || f.checks.directory() != verification.Generation.Checkout {
		t.Fatalf("checks ran %d times in %s, want once in the retried checkout %s", f.checks.calls(), f.checks.directory(), verification.Generation.Checkout)
	}
	if readQueueFile(t, verification.Generation.Checkout, "slow/file-19.txt") != "slowly written\n" {
		t.Fatal("the retried checkout does not hold the whole tree")
	}
	if len(verification.CheckoutRetries) != 1 {
		t.Fatalf("checkout retries = %q, want the one cut-off said", verification.CheckoutRetries)
	}
	said := verification.CheckoutRetries[0]
	if !strings.Contains(said, "attempt 1 of 3") || !strings.Contains(said, "stopped at its time limit of") ||
		!strings.Contains(said, "removed and tried again at once") || strings.Contains(said, "exit code") {
		t.Fatalf("checkout retry said %q, want the attempt, the time limit, and that it was removed and tried again", said)
	}
}

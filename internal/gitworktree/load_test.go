package gitworktree

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The local Git budget is the idle figure on an idle machine and grows with how
// oversubscribed the machine is, up to the cap: a load average at the core
// count is idle for this purpose, twice the cores is twice the budget, and
// nothing about the load makes the budget shorter than the idle figure.
func TestTheLocalGitBudgetScalesWithTheMachinesLoad(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		load  float64
		cores int
		want  time.Duration
	}{
		{name: "idle", load: 0.5, cores: 8, want: 30 * time.Second},
		{name: "at the cores", load: 8, cores: 8, want: 30 * time.Second},
		{name: "twice the cores", load: 16, cores: 8, want: 60 * time.Second},
		{name: "the load this was written against", load: 37, cores: 16, want: 69375 * time.Millisecond},
		{name: "capped", load: 400, cores: 8, want: 5 * time.Minute},
		{name: "no cores reported", load: 2, cores: 0, want: 60 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scaledTimeout(30*time.Second, tc.load, tc.cores); got != tc.want {
				t.Fatalf("scaledTimeout(30s, %v, %d) = %s, want %s", tc.load, tc.cores, got, tc.want)
			}
		})
	}
}

// A manager left to the default budget reads the load for every command, so a
// budget on a loaded machine is at least the idle figure and never more than
// the cap; one given a budget gets that budget whatever the machine is doing.
func TestAManagerLeftToTheDefaultBudgetScalesItAndANamedBudgetIsKept(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	byDefault, err := New(Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := byDefault.localTimeout(); got < defaultTimeout || got > maxLoadFactor*defaultTimeout {
		t.Fatalf("localTimeout() = %s, want at least the idle %s and at most %d times it", got, defaultTimeout, maxLoadFactor)
	}
	// The platforms the suite runs on report a load, and a load is what the
	// budget above was read against; anywhere else the idle figure is the whole
	// of it.
	load, ok := loadAverage()
	if !ok && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
		t.Fatalf("loadAverage() read nothing on %s, where the load is what scales the budget", runtime.GOOS)
	}
	if ok && load < 0 {
		t.Fatalf("loadAverage() = %.2f, want a load average", load)
	}
	if got := byDefault.remoteTimeout(); got < pushTimeout {
		t.Fatalf("remoteTimeout() = %s, want at least %s", got, pushTimeout)
	}

	named, err := New(Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: 7 * time.Second})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := named.localTimeout(); got != 7*time.Second {
		t.Fatalf("localTimeout() = %s, want the 7s the caller named", got)
	}
	if got := named.remoteTimeout(); got != pushTimeout {
		t.Fatalf("remoteTimeout() = %s, want %s for a named local budget under it", got, pushTimeout)
	}

	if _, err := New(Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: -time.Second}); err == nil {
		t.Fatal("New() accepted a negative timeout")
	}

	// The budget reaches the command the runner is handed.
	runner := &budgetRecordingRunner{delegate: execution.OSProcessRunner{}}
	recorded, err := New(Options{Runner: runner, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := recorded.run(context.Background(), "-C", repository, "rev-parse", "HEAD"); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if runner.timeout < defaultTimeout {
		t.Fatalf("the command's budget = %s, want at least the idle %s", runner.timeout, defaultTimeout)
	}
}

// testGitBudget is the budget this package's tests give every Git command they
// run, through a manager or beside one, except where the production budget is
// itself the thing under test (the default-budget tests above).
//
// It is the tests' own figure rather than the manager's, because the manager's
// is sized for a run, and under a suite's load it has been reached by Git that
// was working: a one-file checkout was killed at its 36.4s scaled budget with
// the load at 19, and took 1.6s on its own. A test here is about
// what Git and the manager do, never about how long Git took, so the only
// thing its budget has to catch is a Git command that has hung — and that is
// caught by any figure, however long. Ten minutes sits under the Makefile's
// TEST_TIMEOUT, so a hung command is still ended by the runner and reported
// as the command it was, rather than by the binary's own timeout.
//
// A named budget is not scaled, and a creation's checkout gets its allowance on
// top of it (checkoutTimeout), so what every test here runs under is this
// figure plus the tree, whatever the machine is doing.
const testGitBudget = 10 * time.Minute

// budgetRecordingRunner keeps the budget of the last command it was handed.
type budgetRecordingRunner struct {
	delegate execution.ProcessRunner
	timeout  time.Duration
}

func (r *budgetRecordingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.timeout = command.Timeout
	return r.delegate.Run(ctx, command, observer)
}

// Linux reports its free memory, which is what a heavy check's measurement
// records beside the load as it launches. macOS reports it too, except inside
// a sandbox that refuses the reading, as a developer's own session does; the
// harness reading it is not in one, and a refused reading leaves the figure
// out of the measurement rather than recording nothing as zero.
func TestFreeMemoryIsReadWhereThePlatformSaysIt(t *testing.T) {
	t.Parallel()

	free, ok := MachineFreeMemory()
	if !ok && runtime.GOOS == "linux" {
		t.Fatalf("MachineFreeMemory() read nothing on %s", runtime.GOOS)
	}
	if ok && free == 0 {
		t.Fatalf("MachineFreeMemory() = 0 bytes, want what the machine has free")
	}
}

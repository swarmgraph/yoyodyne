//go:build darwin || linux

package maintain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// leftoverHelperVariable makes this test binary a stand-in for a detached
// test server: it waits a minute and exits, however the test went.
const leftoverHelperVariable = "GO_WANT_LEFTOVER_HELPER"

func TestLeftoverHelper(t *testing.T) {
	if os.Getenv(leftoverHelperVariable) != "serve" {
		return
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

// startMarked starts a process that carries runID's identifier in its
// environment, in a session of its own as a detached test server is, and ends
// on its own after a minute however the test went. The returned channel closes
// once it has been reaped.
func startMarked(t *testing.T, runID string) (int, <-chan struct{}) {
	t.Helper()
	// The test binary rather than a system program, whose starting
	// environment macOS does not let another process read.
	server := exec.Command(os.Args[0], "-test.run=^TestLeftoverHelper$")
	server.Env = execution.WithRunIdentity([]string{"PATH=/bin:/usr/bin", leftoverHelperVariable + "=serve"}, runID)
	server.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	pid := server.Process.Pid
	reaped := make(chan struct{})
	go func() { _ = server.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL); <-reaped })
	return pid, reaped
}

// A harness that stopped between a session's end and the stop that follows it
// leaves the session's detached server running, and the run's record still
// says it is running though nothing holds it. The next scheduler start or
// maintenance pass finds the server by its identifier and stops it. Another
// run's server, whose session is still going, is untouched.
func TestLeftoversStopAServerAHarnessThatStoppedLeftBehind(t *testing.T) {
	leftovers, runs, _ := newLeftovers(t, execution.RunProcesses{TermWait: 5 * time.Second, KillWait: 5 * time.Second})
	crashed := leftoverRecord(t, runs, "calc-1", runstate.StatusRunning)
	working := leftoverRecord(t, runs, "calc-2", runstate.StatusRunning)
	ours, oursReaped := startMarked(t, crashed.RunID)
	theirs, theirsReaped := startMarked(t, working.RunID)

	marked, err := (execution.RunProcesses{}).MarkedRuns()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, id := range marked {
		found[id] = true
	}
	if !found[crashed.RunID] || !found[working.RunID] {
		t.Skipf("this machine does not let the harness read the test's own processes' environments (found %v), so the stop cannot be shown", marked)
	}

	lease, held, err := runs.TryIdleRun(working.RunID)
	if err != nil || !held {
		t.Fatalf("TryIdleRun() = %v, %v; the test cannot stand in for the working run's session", held, err)
	}
	defer lease.Release()
	step := leftovers.Stop(context.Background())
	select {
	case <-oursReaped:
	case <-time.After(15 * time.Second):
		t.Fatalf("process %d, left by the run whose harness stopped, is still running; step = %+v", ours, step)
	}
	select {
	case <-theirsReaped:
		t.Fatalf("process %d, of a run whose session is still going, was stopped", theirs)
	default:
	}
	if errors.Is(syscall.Kill(theirs, 0), syscall.ESRCH) {
		t.Fatalf("process %d, of a run whose session is still going, is gone", theirs)
	}
	if step.Outcome != runstate.StepRan {
		t.Fatalf("step = %+v", step)
	}
}

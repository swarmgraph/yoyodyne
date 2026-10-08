package cli

import (
	"errors"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A process a test starts — a supervisor `yoyo start` detaches, a part it would
// start — is stopped however the test ends: a test that fails, or calls
// t.Fatal partway, still runs its cleanups, and a cleanup registered as the
// process starts is the one exit path every ending shares. A process left over
// outlives the test and is read afterwards as a copy of the part it played,
// which is how a supervisor a test started came to be listed as running beside
// the installed one.

// cleanups is the part of testing.TB the stop is registered with, so the
// test below can drive both endings against a test of its own.
type cleanups interface {
	Cleanup(func())
}

// stopWhenDone stops pid when the test ends, on every exit path.
func stopWhenDone(t cleanups, pid int) {
	t.Cleanup(func() { stopTestProcess(pid) })
}

// stopTestProcess asks the process to stop, ends it if it has not within a few
// seconds, and collects it where the test is its parent, so nothing of it is
// left in the process table either.
func stopTestProcess(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	if testProcessGone(pid, 5*time.Second) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	testProcessGone(pid, 5*time.Second)
}

// testProcessGone waits up to bound for pid to leave, collecting it as soon as
// it has exited, and reports whether it left.
func testProcessGone(pid int, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for {
		var status syscall.WaitStatus
		if got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); got == pid || errors.Is(err, syscall.ECHILD) {
			look, lookErr := runstate.LookProcess(pid)
			if lookErr != nil || !look.Running() {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// recordedCleanups runs the cleanups a test registered, last first, once the
// test's own body has ended — the order and the moment testing.T runs them in.
type recordedCleanups struct {
	mu    sync.Mutex
	funcs []func()
}

func (r *recordedCleanups) Cleanup(cleanup func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.funcs = append(r.funcs, cleanup)
}

func (r *recordedCleanups) run() {
	r.mu.Lock()
	funcs := r.funcs
	r.funcs = nil
	r.mu.Unlock()
	for index := len(funcs) - 1; index >= 0; index-- {
		funcs[index]()
	}
}

// A process a test started is gone once the test is over, whether the test
// passed or failed partway: the failure is t.Fatal's own, which ends the test's
// goroutine where it is called and runs the cleanups after it.
func TestATestStartedProcessIsGoneAfterTheTestOnSuccessAndOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("processes are signalled on the Unix hosts Yoyodyne supports")
	}
	t.Parallel()

	for _, ending := range []string{"success", "failure"} {
		ending := ending
		t.Run(ending, func(t *testing.T) {
			t.Parallel()
			// The process sets its own end as well: a minute, however this test ends.
			command := exec.Command("/bin/sleep", "60")
			if err := command.Start(); err != nil {
				t.Fatalf("start a process: %v", err)
			}
			pid := command.Process.Pid
			if err := command.Process.Release(); err != nil {
				t.Fatalf("release the process: %v", err)
			}
			t.Cleanup(func() { stopTestProcess(pid) })

			test := &recordedCleanups{}
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				stopWhenDone(test, pid)
				if ending == "failure" {
					// What t.Fatal does after recording the failure.
					runtime.Goexit()
				}
			}()
			<-ended
			if look, err := runstate.LookProcess(pid); err != nil || !look.Running() {
				t.Fatalf("the process was gone before the test ended (%+v, %v), so this proves nothing", look, err)
			}
			test.run()

			look, err := runstate.LookProcess(pid)
			if err != nil {
				t.Fatalf("LookProcess() error = %v", err)
			}
			if look.Exists {
				t.Fatalf("after a test ending in %s, pid %d is still there: %+v", ending, pid, look)
			}
		})
	}
}

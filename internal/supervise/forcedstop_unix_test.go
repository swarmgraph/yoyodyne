//go:build darwin || linux

package supervise

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A process that ignores the stop signal and keeps its lease is not left on
// its own schedule by a stop that forces: once the wait runs out it is killed
// with what it started — here a child leading a process group of its own, as
// every AI session a scheduler hosts does — within a bounded time, the stop
// says when and why, and what it left is settled only once it is gone.
func TestAStopThatForcesKillsAProcessIgnoringTheStopAndWhatItStarted(t *testing.T) {
	t.Parallel()

	// The test binary started again ignores the stop signal and starts a child
	// in a group of its own (ignoreTheStop).
	command := exec.Command(os.Args[0], "-test.run=^$")
	command.Env = append(os.Environ(), ignoringStopVariable+"=1")
	if err := command.Start(); err != nil {
		t.Fatalf("start a process that ignores the stop signal: %v", err)
	}
	pid := command.Process.Pid
	if err := command.Process.Release(); err != nil {
		t.Fatalf("release the process: %v", err)
	}
	endOnCleanup(t, pid)
	started := waitForAChild(t, pid)
	if len(started) != 1 || started[0].group != started[0].pid {
		t.Fatalf("children = %+v, want one child leading its own group", started)
	}
	grandchild := started[0].pid
	endOnCleanup(t, grandchild)

	alive := func(of int) bool {
		look, err := runstate.LookProcess(of)
		return err == nil && look.Exists && !look.Exited
	}
	held := func() (bool, error) { return alive(pid), nil }
	var mu sync.Mutex
	var settledPID int
	var settledWhileAlive bool
	settle := func(_ context.Context, forced int, look runstate.ProcessLook, at time.Time) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		settledPID = forced
		settledWhileAlive = alive(forced)
		return "it was carrying no run that needed keeping", nil
	}

	const grace = 500 * time.Millisecond
	began := time.Now()
	stopped, err := stopHolder(context.Background(), pid, held, grace, &forcing{kill: killTree, wait: 10 * time.Second, settle: settle})
	took := time.Since(began)
	if err != nil {
		t.Fatalf("stopHolder() error = %v", err)
	}
	if took > grace+10*time.Second {
		t.Fatalf("the forced stop took %s, want it within the wait and the force's own bound", took)
	}
	if stopped.Detail != "" || stopped.ForcedAt.IsZero() || !strings.Contains(stopped.Forced, "still holding its lease") ||
		!strings.Contains(stopped.Forced, "forced it and everything it had started to stop at") || !strings.Contains(stopped.Forced, "carrying no run") {
		t.Fatalf("stopHolder() = %+v, want a forced stop that says when and why, and nothing outstanding", stopped)
	}
	mu.Lock()
	if settledPID != pid || settledWhileAlive {
		t.Fatalf("settled pid %d (alive %t), want pid %d settled once it was gone", settledPID, settledWhileAlive, pid)
	}
	mu.Unlock()
	if look, err := runstate.LookProcess(pid); err == nil && look.Exists {
		t.Fatalf("pid %d is still in the process table after the forced stop: %+v", pid, look)
	}
	for end := time.Now().Add(5 * time.Second); alive(grandchild); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the process it started, pid %d, is still running after the forced stop", grandchild)
		}
	}
}

// A stop that does not force leaves the same process on its own schedule, as
// it always has, and says so.
func TestAStopThatDoesNotForceLeavesAProcessIgnoringTheStopRunning(t *testing.T) {
	t.Parallel()

	// The trailing command keeps the shell from replacing itself with sleep, so
	// sleep appearing as its child says the trap is set.
	command := exec.Command("/bin/sh", "-c", `trap "" TERM; /bin/sleep 60; :`)
	if err := command.Start(); err != nil {
		t.Fatalf("start a process that ignores the stop signal: %v", err)
	}
	pid := command.Process.Pid
	if err := command.Process.Release(); err != nil {
		t.Fatalf("release the process: %v", err)
	}
	endOnCleanup(t, pid)
	waitForAChild(t, pid)
	held := func() (bool, error) {
		look, err := runstate.LookProcess(pid)
		return err == nil && look.Exists && !look.Exited, nil
	}
	stopped, err := stopHolder(context.Background(), pid, held, 300*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("stopHolder() error = %v", err)
	}
	if stopped.Forced != "" || !strings.Contains(stopped.Detail, "stopping on its own schedule") {
		t.Fatalf("stopHolder() = %+v, want an unforced stop left on its own schedule", stopped)
	}
}

// The process table is read from the kernel, so the children of a process are
// found where a sandbox refuses ps: here, a child this test started, with the
// group it leads.
func TestTheChildrenOfAProcessAreReadFromTheProcessTable(t *testing.T) {
	t.Parallel()

	command := exec.Command("/bin/sleep", "30")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start a child: %v", err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	children, err := childrenOf(os.Getpid())
	if err != nil {
		t.Fatalf("childrenOf() error = %v", err)
	}
	for _, child := range children {
		if child.pid == command.Process.Pid {
			if child.parent != os.Getpid() || child.group != child.pid {
				t.Fatalf("child = %+v, want parent %d and a group of its own", child, os.Getpid())
			}
			return
		}
	}
	t.Fatalf("children of %d = %+v, want pid %d among them", os.Getpid(), children, command.Process.Pid)
}

// waitForAChild waits for pid to have started a child, which for the
// processes these tests start says the stop signal is already ignored.
func waitForAChild(t *testing.T, pid int) []process {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		children, err := childrenOf(pid)
		if err != nil {
			t.Fatalf("childrenOf() error = %v", err)
		}
		if len(children) > 0 {
			return children
		}
	}
	t.Fatalf("pid %d started no child within ten seconds", pid)
	return nil
}

// endOnCleanup ends a process this test started, and what it started, when the
// test ends, but only while the process with that number is still the one the
// test started: a process the test has already ended and collected may have
// given its number to some other process on the machine — another package's
// tests run beside this one — and that one is left alone.
func endOnCleanup(t *testing.T, pid int) {
	t.Helper()
	started, err := runstate.LookProcess(pid)
	if err != nil || !started.Exists || started.StartedAt.IsZero() {
		t.Fatalf("pid %d could not be identified to end it when the test ends: %+v, %v", pid, started, err)
	}
	t.Cleanup(func() {
		look, err := runstate.LookProcess(pid)
		if err != nil || !look.Exists || !look.StartedAt.Equal(started.StartedAt) {
			return
		}
		if !look.Exited {
			_ = killTree(pid)
		}
		_, _ = reap(pid)
	})
}

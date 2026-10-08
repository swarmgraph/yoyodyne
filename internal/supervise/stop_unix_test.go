//go:build darwin || linux

package supervise

import (
	"context"
	"os/exec"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A stop is not over when the lease is let go: the process is collected, so
// nothing of it is left in the process table for a reading of the records to
// count as a copy still running. The child here is let go the way the launcher
// lets every part go, without anything waiting on it, which is what left every
// stopped sink behind as an exited process nothing collected.
func TestAStoppedChildIsConfirmedGoneAndLeavesNothingInTheProcessTable(t *testing.T) {
	t.Parallel()

	// It ends itself after a minute however this test ends.
	command := exec.Command("/bin/sleep", "60")
	if err := command.Start(); err != nil {
		t.Fatalf("start a child: %v", err)
	}
	pid := command.Process.Pid
	if err := command.Process.Release(); err != nil {
		t.Fatalf("release the child: %v", err)
	}
	t.Cleanup(func() {
		if look, err := runstate.LookProcess(pid); err == nil && look.Exists {
			_, _ = StopHolder(context.Background(), pid, func() (bool, error) { return false, nil })
		}
	})

	stopped, err := StopHolder(context.Background(), pid, func() (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("StopHolder() error = %v", err)
	}
	if !stopped.WasRunning || stopped.PID != pid || stopped.Detail != "" {
		t.Fatalf("StopHolder() = %+v, want pid %d stopped with nothing outstanding", stopped, pid)
	}
	look, err := runstate.LookProcess(pid)
	if err != nil {
		t.Fatalf("LookProcess() error = %v", err)
	}
	if look.Exists {
		t.Fatalf("after the stop, pid %d is still in the process table (%+v), want it collected", pid, look)
	}
}

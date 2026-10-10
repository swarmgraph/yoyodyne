package runstate

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// unreaped starts script as the leader of a process group of its own and
// waits, for a bounded while, until its leader has exited without being
// reaped — the state a provider shell is in once its launcher has died and
// nothing has collected it yet. It is reaped when the test ends.
func unreaped(t *testing.T, script string) int {
	t.Helper()
	leader := exec.Command("/bin/sh", "-c", script)
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	pid := leader.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = leader.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if exited, err := processHasExited(pid); err == nil && exited {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the group's leader did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A group whose only member has exited and not been reaped still answers a
// signal on Linux. Nothing in it runs, so the attempt is confirmed stopped
// rather than waited on as uncertain.
func TestAGroupWhoseOnlyMemberHasExitedUnreapedIsStopped(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	pid := unreaped(t, "exit 0")
	if err := syscall.Kill(-pid, 0); err != nil {
		t.Fatalf("the group of an unreaped leader does not answer a signal (%v), so this test shows nothing", err)
	}
	f.recordExecution(t, "att-1", pid)
	found, err := f.store.ReconcileLaunch(context.Background(), f.runID, "op-1", LaunchRecovery{})
	if err != nil {
		t.Fatal(err)
	}
	if found.Verdict != LaunchInterrupted {
		t.Fatalf("reconciliation with only an exited member left = %+v", found)
	}
	f.assertUnspent(t)
}

// A group whose leader has exited unreaped while a process it started is still
// running in the group is not stopped: the attempt stays uncertain, and
// nothing is launched or spent.
func TestAGroupWithALiveMemberBesideAnExitedLeaderStaysUncertain(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	pid := unreaped(t, "sleep 3 & exit 0")
	f.recordExecution(t, "att-1", pid)
	found, err := f.store.ReconcileLaunch(context.Background(), f.runID, "op-1", LaunchRecovery{})
	if err != nil {
		t.Fatal(err)
	}
	if found.Verdict != LaunchUncertain || !strings.Contains(found.Reason, "still has members") {
		t.Fatalf("reconciliation with a live member left = %+v", found)
	}
	if attempt := f.attempt(t, "att-1"); attempt.State != AttemptLaunched {
		t.Fatalf("recovery changed the attempt: %+v", attempt)
	}
	if got := f.launches(t); got != 0 {
		t.Fatalf("providers launched = %d, want 0", got)
	}
	f.assertUnspent(t)
}

// A process that cannot be found has exited: one reaped while the process list
// is being read is gone, not a reason the group cannot be judged.
func TestAProcessThatCannotBeFoundHasExited(t *testing.T) {
	t.Parallel()
	gone := exec.Command("/bin/sh", "-c", "exit 0")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	exited, err := processHasExited(gone.Process.Pid)
	if err != nil || !exited {
		t.Fatalf("processHasExited(a reaped process) = %v, %v; want true, nil", exited, err)
	}
	if !notFound(syscall.ESRCH) || !notFound(os.ErrNotExist) || notFound(os.ErrPermission) {
		t.Fatal("notFound does not tell a vanished process from a refusal")
	}
}

// A command name with spaces and parentheses does not move the fields read
// after it.
func TestATaskStatIsReadPastAnAwkwardCommandName(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/stat"
	line := "4242 (a (b) c) Z 1 4242 4242 0 -1 " + strconv.Itoa(exitingFlag) + " 0 0\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := readTaskStat(path)
	if err != nil {
		t.Fatal(err)
	}
	if task.state != "Z" || task.group != 4242 || task.flags != exitingFlag || !task.exited() {
		t.Fatalf("readTaskStat() = %+v", task)
	}
}

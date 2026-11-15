//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAStageIsHeldForAsLongAsAProcessItStartedIsAlive(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	lease := workerLease(t, store, mainQueue)
	generation := testGeneration(t, 1)
	if running, _, err := store.StageRunning(mainQueue, generation, MergeQueueStageChecks); err != nil || running {
		t.Fatalf("StageRunning(never started) = %t, %v", running, err)
	}
	if _, err := store.HoldStage(nil, mainQueue, generation, MergeQueueStageChecks); !errors.Is(err, ErrMergeQueueWorkerLeaseRequired) {
		t.Fatalf("HoldStage(no lease) = %v, want it refused", err)
	}
	hold, err := store.HoldStage(lease, mainQueue, generation, MergeQueueStageChecks)
	if err != nil {
		t.Fatalf("HoldStage() = %v", err)
	}
	inherited, err := hold.Inherited()
	if err != nil {
		t.Fatal(err)
	}
	// A process the stage started, in a process group of its own, outliving
	// the worker that started it: the worker lets go of its own copy and the
	// child keeps the hold taken.
	child := exec.Command("/bin/sh", "-c", "end=$(( $(date +%s) + 60 )); while [ \"$(date +%s)\" -lt \"$end\" ]; do sleep 1; done")
	child.ExtraFiles = append(child.ExtraFiles, inherited)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	inherited.Close()
	if err := hold.Close(); err != nil {
		t.Fatal(err)
	}
	running, reason, err := store.StageRunning(mainQueue, generation, MergeQueueStageChecks)
	if err != nil || !running || reason == "" {
		t.Fatalf("StageRunning() = %t, %q, %v; want the live child found", running, reason, err)
	}
	var waiting MergeQueueStageRunningError
	if _, err := store.HoldStage(lease, mainQueue, generation, MergeQueueStageChecks); !errors.As(err, &waiting) {
		t.Fatalf("HoldStage() beside the live child = %v, want it refused", err)
	}

	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for {
		running, _, err = store.StageRunning(mainQueue, generation, MergeQueueStageChecks)
		if err != nil || !running || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || running {
		t.Fatalf("StageRunning() after the child ended = %t, %v", running, err)
	}
	again, err := store.HoldStage(lease, mainQueue, generation, MergeQueueStageChecks)
	if err != nil {
		t.Fatalf("HoldStage() after the child ended = %v", err)
	}
	_ = again.Close()
}

// A process forked while the worker held the stage carries a copy of the hold
// until it replaces itself with the program it was starting. That copy is the
// hold of nothing the stage started, so a stage whose own processes have all
// ended is not read as running for the moment such a copy outlives them; it is
// waited out within MergeQueueHoldForkGrace.
func TestAMomentaryCopyOfAStagesHoldIsNotAStageStillRunning(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	lease := workerLease(t, store, mainQueue)
	generation := testGeneration(t, 1)
	hold, err := store.HoldStage(lease, mainQueue, generation, MergeQueueStageChecks)
	if err != nil {
		t.Fatalf("HoldStage() = %v", err)
	}
	inherited, err := hold.Inherited()
	if err != nil {
		t.Fatal(err)
	}
	// The stand-in for the forked copy lives a fraction of the grace and is
	// nothing the stage recorded starting.
	copyHolder := exec.Command("/bin/sh", "-c", "sleep 0.5")
	copyHolder.ExtraFiles = append(copyHolder.ExtraFiles, inherited)
	copyHolder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := copyHolder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = copyHolder.Process.Kill(); _ = copyHolder.Wait() })
	inherited.Close()
	if err := hold.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	running, reason, err := store.StageRunning(mainQueue, generation, MergeQueueStageChecks)
	if err != nil || running {
		t.Fatalf("StageRunning() = %t, %q, %v; want the momentary copy waited out", running, reason, err)
	}
	if waited := time.Since(started); waited > MergeQueueHoldForkGrace {
		t.Fatalf("StageRunning() waited %s, past the grace of %s", waited, MergeQueueHoldForkGrace)
	}
	again, err := store.HoldStage(lease, mainQueue, generation, MergeQueueStageChecks)
	if err != nil {
		t.Fatalf("HoldStage() once the copy is gone = %v", err)
	}
	_ = again.Close()
}

// heldThenFreed is a stage whose hold was taken and let go with nothing
// holding it, and which recorded one process on this machine leading the
// given process group: what a restart finds once every process the stage
// started has let go of the hold.
func heldThenFreed(t *testing.T, store *MergeQueueStore, group int) (*Lease, MergeQueueGeneration) {
	t.Helper()
	lease := workerLease(t, store, mainQueue)
	generation := testGeneration(t, 1)
	hold, err := store.HoldStage(lease, mainQueue, generation, MergeQueueStageChecks)
	if err != nil {
		t.Fatalf("HoldStage() = %v", err)
	}
	if err := hold.Close(); err != nil {
		t.Fatal(err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	generation.Launches = []MergeQueueLaunch{{
		Stage: MergeQueueStageChecks, Command: "sh ./check.sh", Host: host,
		PID: group, ProcessGroup: group, StartedAt: time.Now().UTC(),
	}}
	return lease, generation
}

// The last process of a stage lets go of the hold as it exits, a moment before
// it leaves its process group. A restart that looks at the group in that
// moment sees a member, and looks again before believing it: the group gone at
// the second look is a stage that has stopped.
func TestAStageGroupGoneBetweenTheTwoLooksIsNotRunning(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	_, generation := heldThenFreed(t, store, 999999)
	looks := 0
	store.signalGroup = func(int) error {
		looks++
		if looks == 1 {
			return nil
		}
		return syscall.ESRCH
	}
	running, reason, err := store.StageRunning(mainQueue, generation, MergeQueueStageChecks)
	if err != nil || running {
		t.Fatalf("StageRunning() with the group gone at the second look = %t, %q, %v; want it stopped", running, reason, err)
	}
	if looks != 2 {
		t.Fatalf("the group was looked at %d times, want 2", looks)
	}
}

// A group that cannot be found at the first look has gone, and is not looked
// at again.
func TestAStageGroupThatCannotBeFoundIsGone(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	_, generation := heldThenFreed(t, store, 999999)
	looks := 0
	store.signalGroup = func(int) error { looks++; return syscall.ESRCH }
	running, reason, err := store.StageRunning(mainQueue, generation, MergeQueueStageChecks)
	if err != nil || running {
		t.Fatalf("StageRunning() with no group = %t, %q, %v; want it stopped", running, reason, err)
	}
	if looks != 1 {
		t.Fatalf("the group was looked at %d times, want 1", looks)
	}
}

// A group that has members at both looks is a stage still running, and
// nothing is started beside it.
func TestAStageGroupWithMembersAtBothLooksIsRunning(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	_, generation := heldThenFreed(t, store, 999999)
	looks := 0
	store.signalGroup = func(int) error { looks++; return nil }
	running, reason, err := store.StageRunning(mainQueue, generation, MergeQueueStageChecks)
	if err != nil || !running || !strings.Contains(reason, "still has members") {
		t.Fatalf("StageRunning() with members at both looks = %t, %q, %v; want it running", running, reason, err)
	}
	if looks < 2 {
		t.Fatalf("the group was looked at %d times, want at least 2", looks)
	}
}

// A process the stage started that let go of the hold but is still alive in
// its process group keeps the stage running, looked at the real way, and
// nothing is started beside it.
func TestAStageWhoseGroupStillHasALiveMemberIsRunning(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	member := exec.Command("/bin/sh", "-c", "end=$(( $(date +%s) + 60 )); while [ \"$(date +%s)\" -lt \"$end\" ]; do sleep 1; done")
	member.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := member.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-member.Process.Pid, syscall.SIGKILL); _ = member.Wait() })
	lease, generation := heldThenFreed(t, store, member.Process.Pid)
	running, reason, err := store.StageRunning(mainQueue, generation, MergeQueueStageChecks)
	if err != nil || !running || !strings.Contains(reason, "still has members") {
		t.Fatalf("StageRunning() beside a live member = %t, %q, %v; want it running", running, reason, err)
	}
	var waiting MergeQueueStageRunningError
	if _, err := store.HoldStage(lease, mainQueue, generation, MergeQueueStageChecks); !errors.As(err, &waiting) {
		t.Fatalf("HoldStage() beside a live member = %v, want it refused", err)
	}

	// The whole group is ended, the shell and the sleep it is waiting on. The
	// sleep is reaped by whoever inherits it, so the group is looked at again
	// for a bounded while.
	if err := syscall.Kill(-member.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = member.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for {
		running, reason, err = store.StageRunning(mainQueue, generation, MergeQueueStageChecks)
		if err != nil || !running || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || running {
		t.Fatalf("StageRunning() once the group has ended = %t, %q, %v", running, reason, err)
	}
}

//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"errors"
	"os/exec"
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

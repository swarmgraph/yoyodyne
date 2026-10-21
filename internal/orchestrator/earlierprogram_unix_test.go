//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// earlierProgramWorker is a worker this very process recorded while it was
// running an earlier program: the scheduler takes up a deployed build by
// replacing its program in place, so the number and the start time the
// operating system reports are this process's own, and only the program's start
// time is not.
func earlierProgramWorker(t *testing.T, runID string) runstate.WorkerIdentity {
	t.Helper()
	look, err := runstate.LookProcess(os.Getpid())
	if err != nil || look.StartedAt.IsZero() {
		t.Skipf("the operating system reports no start time here (%+v, %v)", look, err)
	}
	return runstate.WorkerIdentity{
		PID: os.Getpid(), StartedAt: look.StartedAt.UTC(),
		ProgramStartedAt: runstate.ProgramStartedAt().Add(-15 * 24 * time.Hour),
		Build:            "abcdef1", RunID: runID, Attempt: 1,
	}
}

// leaveStamp writes the stamp beside a run's lease that an earlier program
// holding the run left behind when it was replaced without letting go.
func leaveStamp(t *testing.T, runs *runstate.Store, worker runstate.WorkerIdentity) {
	t.Helper()
	stamp, err := json.Marshal(map[string]any{"pid": worker.PID, "held_at": time.Now().UTC(), "worker": worker})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs.Root(), worker.RunID+".holder"), stamp, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A decided re-run starts when the stopped run it replaces names the
// scheduler's own process number, recorded by the program the scheduler ran
// before it took up a new build: that run is not one this scheduler started,
// and nothing about it holds the item.
func TestADecidedRerunStartsWhenItsOldRunNamesTheSchedulersOwnProcess(t *testing.T) {
	t.Parallel()

	state := stoppedState()
	worker := earlierProgramWorker(t, state.RunID)
	state.Worker = &worker
	harness := newRerunHarness(t, state)
	leaveStamp(t, harness.runs, worker)
	if held, err := harness.runs.Held(state.RunID); err != nil || held {
		t.Fatalf("Held() over an earlier program's stamp = %v, %v; want not held", held, err)
	}
	carrying := harness.carryOut()
	pulls := newScheduleHarness()
	scheduler := Scheduler{Limit: 1, Open: func(ctx context.Context) (Pull, error) {
		pull, err := pulls.open(ctx)
		pull.Runs, pull.CarryOut = harness.runs, carrying
		return pull, err
	}}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() = %v: %s", err, schedule.Render())
	}
	if len(schedule.CarriedOut) != 1 || !schedule.CarriedOut[0].Carried || len(harness.started) != 1 || harness.started[0].workItemID != docketedItem {
		t.Fatalf("schedule = %s, started = %#v; want the decided re-run started", schedule.Render(), harness.started)
	}
}

// A repair whose dispatch was charged and never served is offered again when the
// only thing beside its lease is a stamp the scheduler's earlier program left:
// that program is gone, and the run is not one this scheduler is carrying. The
// same stamp written by this program keeps it back, because this program is.
func TestAPendingRepairIsNotHeldByTheSchedulersEarlierProgram(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.capacity = 1
	runs := &repairRecordRuns{RepairRuns: harness.runs, loadErr: errors.New("read-back failed")}
	continuer := harness.continuer()
	continuer.Runs = runs
	continuer.Items = &repairRecordItems{Tracker: harness.tracker}
	carrying := harness.carryOut()
	carrying.Repairer = continuer
	pulls := newScheduleHarness()
	scheduler := Scheduler{Limit: 1, Open: func(ctx context.Context) (Pull, error) {
		pull, err := pulls.open(ctx)
		pull.Runs, pull.CarryOut = harness.runs, carrying
		return pull, err
	}}
	if first, err := scheduler.Schedule(context.Background()); err != nil || len(harness.started) != 0 {
		t.Fatalf("first pass = %v: %s; want an unconfirmed continuation and no dispatch", err, first.Render())
	}
	pending := harness.reload(t)
	if !pending.RepairDispatchPending() {
		t.Fatalf("pending = %+v; want a charged, unserved continuation", pending)
	}
	carrying.Clock = laterClock{after: runstate.TriageCarryOutRetryDelay + time.Minute}

	earlier := earlierProgramWorker(t, pending.RunID)
	leaveStamp(t, harness.runs, earlier)
	if task := theOneOutstanding(t, carrying); !task.Recover {
		t.Fatalf("task = %+v; want the continuation offered for recovery past the earlier program's stamp", task)
	}

	current := earlier
	current.ProgramStartedAt = runstate.ProgramStartedAt()
	leaveStamp(t, harness.runs, current)
	if outstanding, err := carrying.Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %+v, %v; want nothing offered while this program holds the run", outstanding, err)
	}
}

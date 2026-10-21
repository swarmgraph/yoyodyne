//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// A decided re-run waits while another run of its item is still recorded in
// flight, because one item is never given two runs at once. Where that run
// names the scheduler's own process number from the program the scheduler ran
// before it took up a new build, it is not one this scheduler started: the
// account says so and says its worker is gone, the earlier program's stamp is
// let go of when the scheduler's program starts, and once the run is ended the
// decided re-run starts on the next pass.
func TestADecidedRerunStartsWhenItsOldRunNamesTheSchedulersOwnProcess(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	left := stoppedState()
	left.RunID = "run-0123456789abcdef0123456789abcdee"
	left.Status, left.CompletedAt, left.Blocker = runstate.StatusRunning, nil, ""
	left.CheckFailure, left.ReviewFindingDetails, left.ReviewFindings, left.ReviewSummary = nil, nil, 0, ""
	worker := earlierProgramWorker(t, left.RunID)
	left.Worker = &worker
	if err := harness.runs.Create(left); err != nil {
		t.Fatalf("Create() of the run the earlier program left = %v", err)
	}
	leaveStamp(t, harness.runs, worker)

	// Every reader, in this process or any other, takes the earlier program's
	// worker for gone rather than for the scheduler's own.
	if reading := harness.runs.Worker(left, time.Now()); reading.Liveness != runstate.WorkerAbsent {
		t.Fatalf("Worker() of the earlier program's run = %+v, want gone", reading)
	}
	carrying := harness.carryOut()
	reading, err := carrying.read()
	if err != nil || len(reading.tasks) != 0 || len(reading.held) != 1 {
		t.Fatalf("read() = %+v, %v; want the re-run held while the earlier program's run is in flight", reading, err)
	}
	why := reading.held[0].why
	for _, want := range []string{left.RunID, "not started by this session", "its worker is gone", "earlier program"} {
		if !strings.Contains(why, want) {
			t.Fatalf("why the re-run waits = %q, want it to say %q", why, want)
		}
	}

	// The scheduler's program starting lets go of what its earlier program left.
	if err := harness.runs.StartProgram(); err != nil {
		t.Fatalf("StartProgram() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(harness.runs.Root(), left.RunID+".holder")); !os.IsNotExist(err) {
		t.Fatalf("the earlier program's stamp is still beside the lease after the program started (%v)", err)
	}
	// The run is ended the way the sweep ends a run with no process behind it:
	// its lease, which nothing holds now, is taken and the run recorded as ended.
	adopted, lease, err := harness.runs.AdoptRun(context.Background(), left.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() of the earlier program's run = %v", err)
	}
	ended := time.Now().UTC()
	adopted.Status, adopted.CompletedAt, adopted.Blocker = runstate.StatusFailed, &ended, "its worker was an earlier program in the scheduler's process"
	if err := harness.runs.Save(adopted); err != nil {
		t.Fatalf("Save() ending the earlier program's run = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

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

// The scheduler's check of whether it already started a run of an item names a
// reserved run by whose it is: one an earlier program in this process left is
// never described as one this session started.
func TestTheSchedulerNeverCallsAnEarlierProgramsRunItsOwn(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	left := stoppedState()
	left.Status, left.CompletedAt = runstate.StatusRunning, nil
	worker := earlierProgramWorker(t, left.RunID)
	left.Worker = &worker
	leaveStamp(t, harness.runs, worker)
	why := occupiedWhy(harness.runs, left, time.Now())
	for _, want := range []string{left.RunID, "not started by this session", "its worker is gone"} {
		if !strings.Contains(why, want) {
			t.Fatalf("occupiedWhy() = %q, want it to say %q", why, want)
		}
	}
	current := worker
	current.ProgramStartedAt = runstate.ProgramStartedAt()
	left.Worker = &current
	if why := occupiedWhy(harness.runs, left, time.Now()); !strings.Contains(why, "this session's own") {
		t.Fatalf("occupiedWhy() for this program's run = %q, want it named as this session's", why)
	}
	if why := occupiedWhy(harness.runs, runstate.State{WorkItemID: left.WorkItemID}, time.Now()); !strings.Contains(why, "had already started a run of the item that had not yet reserved") {
		t.Fatalf("occupiedWhy() before the run reserved = %q", why)
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

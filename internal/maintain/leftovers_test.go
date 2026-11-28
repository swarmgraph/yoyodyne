package maintain

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// scriptedProcesses is a process search a test controls: the run identifiers
// it says it found, and what each run's stop leaves running.
type scriptedProcesses struct {
	mu      sync.Mutex
	marked  []string
	left    map[string][]execution.LeftProcess
	stopped []string
	// asked is the other runs each stop asked Idle about, and what it said.
	ask   []string
	asked map[string]bool
}

func (s *scriptedProcesses) MarkedRuns() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.marked...), nil
}

func (s *scriptedProcesses) Stop(_ context.Context, request execution.RunProcessesRequest) []execution.LeftProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = append(s.stopped, request.RunID)
	if request.Idle != nil {
		if s.asked == nil {
			s.asked = map[string]bool{}
		}
		for _, other := range s.ask {
			s.asked[other] = request.Idle(other)
		}
	}
	return append([]execution.LeftProcess(nil), s.left[request.RunID]...)
}

func (s *scriptedProcesses) stops() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stopped...)
}

// leftoverRecord writes one run record with the status given.
func leftoverRecord(t *testing.T, runs *runstate.Store, item string, status runstate.Status) runstate.State {
	t.Helper()
	id, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: id, ProductID: "calc", RepositoryID: "calc",
		WorkItemID: item, Backend: "claude-code", Status: status, StartedAt: at, UpdatedAt: at,
	}
	if status.Terminal() {
		state.CompletedAt = &at
	}
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return state
}

func newLeftovers(t *testing.T, processes LeftoverProcesses) (*Leftovers, *runstate.Store, *runstate.ReportStore) {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	reports, err := runstate.NewReportStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	return &Leftovers{Runs: runs, Processes: processes, StateRoot: root, Reports: reports, Product: "calc", RepositoryID: "calc",
		Now: func() time.Time { return time.Date(2026, 11, 27, 13, 0, 0, 0, time.UTC) }}, runs, reports
}

// A run whose record still says a session is going, though the harness that
// held it has gone, is stopped: nothing holds its lease. A run whose lease is
// held, because a session or check of it is going, is left alone until it is
// let go. An identifier no run of this product has is never stopped.
func TestLeftoversStopOnlyTheRunsNothingHolds(t *testing.T) {
	t.Parallel()
	processes := &scriptedProcesses{left: map[string][]execution.LeftProcess{}}
	leftovers, runs, _ := newLeftovers(t, processes)
	crashed := leftoverRecord(t, runs, "calc-1", runstate.StatusRunning)
	working := leftoverRecord(t, runs, "calc-2", runstate.StatusRunning)
	processes.marked = []string{crashed.RunID, working.RunID, "mqe-0123-generation-1", "run-not-of-this-product"}

	lease, held, err := runs.TryIdleRun(working.RunID)
	if err != nil || !held {
		t.Fatalf("TryIdleRun() = %v, %v; the test cannot hold the working run", held, err)
	}
	step := leftovers.Stop(context.Background())
	if got := processes.stops(); !slices.Equal(got, []string{crashed.RunID}) {
		t.Fatalf("stopped %v, want only the run nothing holds, %s", got, crashed.RunID)
	}
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "nothing is left running for "+crashed.RunID) ||
		!strings.Contains(step.Detail, "left alone the processes of "+working.RunID) {
		t.Fatalf("step = %+v", step)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	leftovers.Stop(context.Background())
	if got := processes.stops(); !slices.Contains(got[1:], working.RunID) {
		t.Fatalf("stopped %v; the run let go of was not stopped at the next pass", got)
	}
}

// A process that will not stop is recorded on its run, shown, and tried again
// on every pass, from the record once nothing marks it any more. On the third
// pass that finds it running it is reported to the development manager, once:
// not on the fourth pass, and not after a session's own stop has replaced the
// record. A process named only because it works inside the run's folders is
// never reported. Once the process is gone its line leaves the record.
func TestLeftoversReportAProcessStillRunningAfterThreePassesOnce(t *testing.T) {
	t.Parallel()
	stubborn := execution.LeftProcess{PID: 4242, Command: "java", Listening: []string{"127.0.0.1:25565"}, Why: "the harness could not stop it: permission refused"}
	shell := execution.LeftProcess{PID: 77, Command: "zsh", Unmarked: true, Why: "not stopped: it carries no sign that this run started it"}
	processes := &scriptedProcesses{left: map[string][]execution.LeftProcess{}}
	leftovers, runs, reports := newLeftovers(t, processes)
	ended := leftoverRecord(t, runs, "calc-3", runstate.StatusFailed)
	processes.marked = []string{ended.RunID}
	processes.left[ended.RunID] = []execution.LeftProcess{stubborn, shell}

	filed := func() int {
		t.Helper()
		listed, err := reports.List()
		if err != nil {
			t.Fatal(err)
		}
		return len(listed)
	}
	for pass := 1; pass <= 5; pass++ {
		step := leftovers.Stop(context.Background())
		if pass == 1 {
			// From here on only the record names the run.
			processes.mu.Lock()
			processes.marked = nil
			processes.mu.Unlock()
		}
		if !strings.Contains(step.Detail, "process 4242, java, listening on 127.0.0.1:25565") {
			t.Fatalf("pass %d: step does not name the process still running: %s", pass, step.Detail)
		}
		want := 0
		if pass >= 3 {
			want = 1
		}
		if got := filed(); got != want {
			t.Fatalf("pass %d: %d reports filed, want %d", pass, got, want)
		}
		if pass == 3 {
			// A session's own stop replaces the record and counts nothing.
			if _, err := runs.Update(context.Background(), ended.RunID, runstate.LeftProcessesFound{Left: []execution.LeftProcess{stubborn, shell}, At: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
	}
	listed, err := reports.List()
	if err != nil {
		t.Fatal(err)
	}
	message := listed[0].Message
	for _, want := range []string{"Process 4242 (java)", "run " + ended.RunID, "calc-3", "127.0.0.1:25565", "permission refused"} {
		if !strings.Contains(message, want) {
			t.Errorf("the report does not say %q: %s", want, message)
		}
	}
	if listed[0].For != domain.RoleDevelopmentManager {
		t.Errorf("the report is for %q, want the development manager", listed[0].For)
	}
	recorded, err := runs.Read(ended.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded.LeftProcesses) != 2 || recorded.LeftProcesses[0].Passes != 5 || recorded.LeftProcesses[0].Reported != listed[0].ID || recorded.LeftProcesses[1].Reported != "" {
		t.Fatalf("recorded %+v", recorded.LeftProcesses)
	}

	// Gone: the next pass clears the line, and status no longer names it.
	processes.mu.Lock()
	processes.left[ended.RunID] = nil
	processes.mu.Unlock()
	leftovers.Stop(context.Background())
	recorded, err = runs.Read(ended.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded.LeftProcesses) != 0 {
		t.Fatalf("the record still names %+v after the processes went", recorded.LeftProcesses)
	}
	if left, err := runs.LeftRunning(); err != nil || len(left) != 0 {
		t.Fatalf("LeftRunning() = %v, %v; want no run named", left, err)
	}
	if got := filed(); got != 1 {
		t.Fatalf("%d reports filed in all, want 1", got)
	}
}

// A pass with nothing found says so as a skipped step, and an unwired stop
// says why it looks for nothing.
func TestLeftoversWithNothingToStop(t *testing.T) {
	t.Parallel()
	leftovers, _, _ := newLeftovers(t, &scriptedProcesses{})
	if step := leftovers.Stop(context.Background()); step.Outcome != runstate.StepSkipped || step.Name != StepLeftovers {
		t.Fatalf("step = %+v, want it skipped", step)
	}
	if step := (&Pass{}).stopLeftovers(context.Background()); step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "no stop") {
		t.Fatalf("unwired step = %+v", step)
	}
}

// A process in use, because something the stop leaves alone is connected to
// it, is recorded and shown under its run, counts no passes, and is never
// reported, however many passes find it; the pass that finds nothing
// connected any more stops it and clears the line. The stop is told which
// other runs have nothing going: one of this product whose lease nobody
// holds, and not one something holds or one of no run of this product.
func TestLeftoversShowAProcessInUseAndNeverReportIt(t *testing.T) {
	t.Parallel()
	helper := execution.LeftProcess{PID: 4242, Command: "gradle", InUse: true, Why: "left running because process 5150 (java, run run-other) is connected to it over a local socket"}
	processes := &scriptedProcesses{left: map[string][]execution.LeftProcess{}}
	leftovers, runs, reports := newLeftovers(t, processes)
	ended := leftoverRecord(t, runs, "calc-4", runstate.StatusFailed)
	idle := leftoverRecord(t, runs, "calc-5", runstate.StatusFailed)
	working := leftoverRecord(t, runs, "calc-6", runstate.StatusRunning)
	lease, held, err := runs.TryIdleRun(working.RunID)
	if err != nil || !held {
		t.Fatalf("TryIdleRun() = %v, %v", held, err)
	}
	defer lease.Release()
	processes.marked = []string{ended.RunID}
	processes.left[ended.RunID] = []execution.LeftProcess{helper}
	processes.ask = []string{idle.RunID, working.RunID, "run-not-of-this-product"}

	for pass := 1; pass <= 4; pass++ {
		step := leftovers.Stop(context.Background())
		if !strings.Contains(step.Detail, "in use, so left running: run "+ended.RunID+": a process this run started is still running: process 4242, gradle, left running because process 5150") ||
			strings.Contains(step.Detail, "STILL RUNNING") {
			t.Fatalf("pass %d: step = %s", pass, step.Detail)
		}
		recorded, err := runs.Read(ended.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if len(recorded.LeftProcesses) != 1 || !recorded.LeftProcesses[0].InUse || recorded.LeftProcesses[0].Passes != 0 {
			t.Fatalf("pass %d: recorded %+v", pass, recorded.LeftProcesses)
		}
	}
	if listed, err := reports.List(); err != nil || len(listed) != 0 {
		t.Fatalf("reports = %+v, %v; want none for a process in use", listed, err)
	}
	processes.mu.Lock()
	asked := processes.asked
	processes.mu.Unlock()
	if !asked[idle.RunID] || asked[working.RunID] || asked["run-not-of-this-product"] {
		t.Fatalf("Idle answered %v; want only %s idle", asked, idle.RunID)
	}

	processes.mu.Lock()
	processes.left[ended.RunID] = nil
	processes.mu.Unlock()
	leftovers.Stop(context.Background())
	if left, err := runs.LeftRunning(); err != nil || len(left) != 0 {
		t.Fatalf("LeftRunning() = %v, %v; want the line cleared once nothing is connected", left, err)
	}
}

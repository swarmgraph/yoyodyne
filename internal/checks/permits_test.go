package checks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// fakePermits gives a place to the commands it was told are heavy, after
// moving the test's clock by wait, and records what it was asked and how each
// place ended.
type fakePermits struct {
	clock   *steppingClock
	dir     string
	heavy   map[string]bool
	wait    time.Duration
	refuse  error
	asked   []PermitAsk
	granted []*fakePermit
}

func (f *fakePermits) Acquire(ctx context.Context, ask PermitAsk, waiting func(string, time.Time)) (Permit, error) {
	f.asked = append(f.asked, ask)
	if !f.heavy[ask.Command] {
		return nil, nil
	}
	if f.wait > 0 {
		waiting(ask.Command, f.clock.Now())
		f.clock.advance(f.wait)
	}
	if f.refuse != nil {
		return nil, f.refuse
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hold, err := os.CreateTemp(f.dir, "hold")
	if err != nil {
		return nil, err
	}
	permit := &fakePermit{check: ask.Command, waited: f.wait, hold: hold}
	f.granted = append(f.granted, permit)
	return permit, nil
}

type fakePermit struct {
	check      string
	waited     time.Duration
	hold       *os.File
	registered bool
	finished   *execution.ProcessResult
}

func (p *fakePermit) Check() string         { return p.check }
func (p *fakePermit) Waited() time.Duration { return p.waited }
func (p *fakePermit) Hold() *os.File        { return p.hold }
func (p *fakePermit) Register(execution.StartedProcess) error {
	p.registered = true
	return nil
}
func (p *fakePermit) Finish(result execution.ProcessResult) error {
	p.finished = &result
	return nil
}

// gatingRunner is advancingRunner that also says which gate it was handed and
// registers through it, the way the real runner does before a gate opens.
type gatingRunner struct {
	advancingRunner
	gates []*execution.LaunchGate
}

func (r *gatingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.gates = append(r.gates, command.Gate)
	if command.Gate != nil {
		if err := command.Gate.Register(execution.StartedProcess{PID: 42, ProcessGroup: 42, StartedAt: r.clock.Now()}); err != nil {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: -1}, err
		}
	}
	return r.advancingRunner.Run(ctx, command, observer)
}

func newPermitClock(*testing.T) *steppingClock {
	return &steppingClock{now: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}
}

// A heavy check's wait is never its own or its stage's: with a stage bound of
// ten minutes, a check that waited an hour for its place still has the whole
// of its budget and of the stage, and the stage's spend leaves the wait out.
func TestAHeavyChecksWaitIsNotCountedAgainstItsBudgetOrItsStage(t *testing.T) {
	t.Parallel()

	clock := newPermitClock(t)
	process := &gatingRunner{advancingRunner: advancingRunner{clock: clock, runs: 4 * time.Minute}}
	permits := &fakePermits{clock: clock, dir: t.TempDir(), heavy: map[string]bool{"make race": true}, wait: time.Hour}
	var waitingFor []string
	var granted []time.Duration
	results, _, err := (Runner{Process: process, Clock: clock, Timeout: 5 * time.Minute, StageTimeout: 10 * time.Minute, Permits: permits}).Run(
		context.Background(),
		Request{
			RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"make test", "make race"},
			Permit: PermitRequest{
				WorkItemID: "yoyodyne-1", Commit: "abc", Priority: domain.HeavyCheckForLanding,
				Waiting: func(check string, _ time.Time) { waitingFor = append(waitingFor, check) },
				Granted: func(_ string, waited time.Duration) { granted = append(granted, waited) },
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 2 || !results[0].Passed || !results[1].Passed {
		t.Fatalf("results = %#v, want both checks to pass", results)
	}
	if want := []time.Duration{5 * time.Minute, 5 * time.Minute}; !reflect.DeepEqual(process.budgets, want) {
		t.Fatalf("budgets = %v, want %v: the wait must not shorten the heavy check's budget", process.budgets, want)
	}
	if got := results[1].StageElapsed; got != 8*time.Minute {
		t.Fatalf("stage elapsed = %s, want 8m: the hour waited is not the stage's", got)
	}
	if !reflect.DeepEqual(waitingFor, []string{"make race"}) || !reflect.DeepEqual(granted, []time.Duration{time.Hour}) {
		t.Fatalf("waiting = %q, granted = %v", waitingFor, granted)
	}
	if want := (PermitAsk{RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-1", Commit: "abc", Command: "make race", Priority: domain.HeavyCheckForLanding}); permits.asked[1] != want {
		t.Fatalf("asked = %#v, want %#v", permits.asked[1], want)
	}
	if process.gates[0] != nil || process.gates[1] == nil {
		t.Fatalf("gates = %#v: only the heavy check starts behind its permit's gate", process.gates)
	}
	permit := permits.granted[0]
	if !permit.registered || permit.finished == nil || permit.finished.Status != execution.ProcessSucceeded {
		t.Fatalf("permit = %#v: it is registered before the check works and finished with its result", permit)
	}
}

// A check with its caller's own gate starts behind one gate that holds both
// files and writes the process down to both, the permit first.
func TestAHeavyCheckWithItsCallersGateHoldsBothFiles(t *testing.T) {
	t.Parallel()

	clock := newPermitClock(t)
	process := &gatingRunner{advancingRunner: advancingRunner{clock: clock, runs: time.Minute}}
	permits := &fakePermits{clock: clock, dir: t.TempDir(), heavy: map[string]bool{"make race": true}}
	callerHold, err := os.CreateTemp(t.TempDir(), "caller")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	_, _, err = (Runner{Process: process, Clock: clock, Permits: permits}).Run(
		context.Background(),
		Request{
			RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"make race"},
			Gate: func(string) (*execution.LaunchGate, error) {
				return &execution.LaunchGate{Hold: callerHold, Register: func(execution.StartedProcess) error {
					if !permits.granted[0].registered {
						order = append(order, "caller before permit")
					}
					order = append(order, "caller")
					return nil
				}}, nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gate := process.gates[0]
	if gate.Hold != callerHold || len(gate.Holds) != 1 || gate.Holds[0] != permits.granted[0].hold {
		t.Fatalf("gate = %#v, want the caller's hold and the permit's", gate)
	}
	if !reflect.DeepEqual(order, []string{"caller"}) {
		t.Fatalf("order = %q, want the permit written down before the caller", order)
	}
}

// A wait the run's own context cuts short ends the stage the way a check its
// caller stopped does, with nothing launched.
func TestAWaitTheCallerStopsEndsTheStageWithNothingLaunched(t *testing.T) {
	t.Parallel()

	clock := newPermitClock(t)
	process := &gatingRunner{advancingRunner: advancingRunner{clock: clock, runs: time.Minute}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	permits := &fakePermits{clock: clock, dir: t.TempDir(), heavy: map[string]bool{"make race": true}, wait: time.Minute}
	results, _, err := (Runner{Process: process, Clock: clock, Permits: permits}).Run(
		ctx,
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"make race", "make vet"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || !results[0].StoppedByCaller || results[0].Process.Status != execution.ProcessCancelled {
		t.Fatalf("results = %#v, want one check stopped by its caller", results)
	}
	if len(process.gates) != 0 {
		t.Fatalf("%d checks launched, want none", len(process.gates))
	}
}

// A record of places that cannot be read is the harness's failure, never a
// check that ran: the stage stops with the error and launches nothing.
func TestARecordOfPlacesThatFailsLaunchesNothing(t *testing.T) {
	t.Parallel()

	clock := newPermitClock(t)
	process := &gatingRunner{advancingRunner: advancingRunner{clock: clock, runs: time.Minute}}
	permits := &fakePermits{clock: clock, dir: t.TempDir(), heavy: map[string]bool{"make race": true}, refuse: errors.New("the record is unreadable")}
	results, _, err := (Runner{Process: process, Clock: clock, Permits: permits}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"make race"}},
		nil,
	)
	if err == nil || len(results) != 0 || len(process.gates) != 0 {
		t.Fatalf("results = %#v, err = %v, launched = %d; want an error and nothing launched", results, err, len(process.gates))
	}
}

// A check the stage has no time left for gives its place straight back.
func TestAHeavyCheckTheStageHasNothingLeftForGivesItsPlaceBack(t *testing.T) {
	t.Parallel()

	clock := newPermitClock(t)
	process := &gatingRunner{advancingRunner: advancingRunner{clock: clock, runs: 10 * time.Minute}}
	permits := &fakePermits{clock: clock, dir: t.TempDir(), heavy: map[string]bool{"make race": true}}
	results, _, err := (Runner{Process: process, Clock: clock, Timeout: 30 * time.Minute, StageTimeout: 10 * time.Minute, Permits: permits}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: filepath.Clean(t.TempDir()), Commands: []string{"make test", "make race"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 2 || !results[1].StoppedByStage || len(process.gates) != 1 {
		t.Fatalf("results = %#v, launched = %d", results, len(process.gates))
	}
	if permit := permits.granted[0]; permit.registered || permit.finished == nil {
		t.Fatalf("permit = %#v, want it given back unlaunched", permit)
	}
}

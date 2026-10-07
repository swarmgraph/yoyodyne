package maintain

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordingRunner answers every command it is given and remembers each one,
// so a test can say what the pass ran — and that it ran nothing else.
type recordingRunner struct {
	mu       sync.Mutex
	commands [][]string
	environs [][]string
	result   execution.ProcessResult
}

func (r *recordingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, append([]string{command.Name}, command.Args...))
	r.environs = append(r.environs, append([]string(nil), command.Env...))
	return r.result, nil
}

func (r *recordingRunner) ran() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.commands...)
}

// fakeHost is the supervisor as the pass sees it: children it knows, the build
// it runs and the one on disk, and a record of every restart and take-up the
// pass asked of it. Like the real supervisor, it refuses what the provider
// guard holds.
type fakeHost struct {
	states   map[config.ServiceName]runstate.SupervisedChild
	running  string
	deployed string
	moving   string
	hold     func() string

	restarts []config.ServiceName
	takenUp  string
}

func (h *fakeHost) ChildState(name config.ServiceName) (runstate.SupervisedChild, bool) {
	state, known := h.states[name]
	return state, known
}

func (h *fakeHost) Hosted(name config.ServiceName) string {
	if _, known := h.states[name]; known {
		return "a process the supervisor starts"
	}
	return "not enabled in the services section"
}

func (h *fakeHost) RunningBuild() string { return h.running }
func (h *fakeHost) Deployed() string     { return h.deployed }
func (h *fakeHost) Moving() string       { return h.moving }

func (h *fakeHost) TakeUp(into string) error {
	if held := h.hold(); held != "" {
		return &heldError{held}
	}
	h.takenUp = into
	return nil
}

func (h *fakeHost) RestartOnRequest(_ context.Context, name config.ServiceName, _ time.Time) string {
	if held := h.hold(); held != "" {
		return "not restarted: nothing is restarted while " + held
	}
	h.restarts = append(h.restarts, name)
	return "stopped pid 7; started again after its backoff"
}

type heldError struct{ held string }

func (e *heldError) Error() string { return "not taken up while " + e.held }

type rebuildStanding struct {
	said   string
	failed bool
}

func (r rebuildStanding) Standing() (string, bool) { return r.said, r.failed }

type fixture struct {
	pass     *Pass
	host     *fakeHost
	runner   *recordingRunner
	sweeps   *runstate.SweepStore
	outages  *runstate.ProviderOutageStore
	requests *runstate.RestartRequestStore
	now      time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	sweeps, err := runstate.NewSweepStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	outages, err := runstate.NewProviderOutageStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	requests, err := runstate.NewRestartRequestStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		runner:   &recordingRunner{result: execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "settled 0 runs"}},
		sweeps:   sweeps,
		outages:  outages,
		requests: requests,
		now:      time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC),
	}
	guard := func() string {
		outage, standing, err := outages.Standing()
		if err != nil {
			return err.Error()
		}
		if standing {
			return outage.Says()
		}
		return ""
	}
	f.host = &fakeHost{
		states: map[config.ServiceName]runstate.SupervisedChild{
			config.ServiceSlack:     {Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 7},
			config.ServiceScheduler: {Service: config.ServiceScheduler, State: runstate.ChildRunning, PID: 8},
		},
		running:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		deployed: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		hold:     guard,
	}
	f.pass = &Pass{
		Product:    "calc",
		Every:      10 * time.Minute,
		Program:    "/opt/calc/bin/yoyo",
		Config:     "/opt/calc/.yoyodyne/config.yaml",
		Repository: "/opt/calc",
		Claims:     sweeps,
		Outages:    outages,
		Requests:   requests,
		Rebuild:    rebuildStanding{said: "/opt/calc/bin/yoyo is built from main's tip aaaaaaaaaaaa"},
		Host:       f.host,
		Runner:     f.runner,
		Now:        func() time.Time { return f.now },
	}
	return f
}

// pass1 takes one whole pass and returns what it recorded.
func (f *fixture) pass1(t *testing.T) runstate.Sweep {
	t.Helper()
	f.pass.Look(context.Background(), f.now)
	f.pass.Wait(context.Background())
	recorded, found := f.pass.Last()
	if !found {
		t.Fatal("no pass was recorded")
	}
	return recorded
}

func stepNamed(t *testing.T, recorded runstate.Sweep, name string) runstate.SweepStep {
	t.Helper()
	for _, step := range recorded.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("the pass recorded no %s step: %+v", name, recorded.Steps)
	return runstate.SweepStep{}
}

// onlyReconcile holds a pass to the one command it may run: `yoyo reconcile`
// from the supervisor's own binary. No `bd`, nothing else, so no tracker status
// is written by the pass itself.
func onlyReconcile(t *testing.T, runner *recordingRunner) {
	t.Helper()
	for _, command := range runner.ran() {
		if command[0] != "/opt/calc/bin/yoyo" || len(command) < 2 || command[1] != "reconcile" {
			t.Errorf("the pass ran %q, and the only command it runs is yoyo reconcile", strings.Join(command, " "))
		}
	}
}

// A pass records every step, in order, in the sweep log under the reserved
// name, with the harness as the one who took it; a step it did not take says
// why; and the claim is settled so the next pass is due a cadence later.
func TestAPassRecordsEveryStepAndWhySkippedOnesWereSkipped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	recorded := f.pass1(t)
	var names []string
	for _, step := range recorded.Steps {
		names = append(names, step.Name)
		if step.Outcome != runstate.StepRan && strings.TrimSpace(step.Detail) == "" {
			t.Errorf("step %s was %s and says nothing about why", step.Name, step.Outcome)
		}
	}
	want := []string{StepProvider, StepReconcile, StepRebuild, StepRestartRequests, StepRedeploy, StepSlack}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v, want %v", names, want)
	}
	if step := stepNamed(t, recorded, StepReconcile); step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "settled 0 runs") {
		t.Errorf("reconcile step = %+v, want it run with what it said", step)
	}
	if step := stepNamed(t, recorded, StepRestartRequests); step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "no program manager has an open restart request") {
		t.Errorf("restart-requests step = %+v, want it skipped saying there were none", step)
	}
	if step := stepNamed(t, recorded, StepRedeploy); step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "every part is on the deployed build") {
		t.Errorf("redeploy step = %+v, want it skipped saying nothing is behind", step)
	}
	if want := [][]string{{"/opt/calc/bin/yoyo", "reconcile", "--config", "/opt/calc/.yoyodyne/config.yaml"}}; len(f.runner.ran()) != 1 || strings.Join(f.runner.ran()[0], " ") != strings.Join(want[0], " ") {
		t.Errorf("ran %v, want %v", f.runner.ran(), want)
	}
	onlyReconcile(t, f.runner)
	// The reconcile it starts is marked as the pass's, so the verb does not count
	// it as the operator settling runs by hand.
	f.runner.mu.Lock()
	environs := f.runner.environs
	f.runner.mu.Unlock()
	if len(environs) != 1 {
		t.Fatalf("recorded %d environments, want 1", len(environs))
	}
	if by, marked := execution.StartedBy(environs[0]); !marked || by != StartedByMaintenance {
		t.Errorf("the reconcile was started marked %q (%v), want %q", by, marked, StartedByMaintenance)
	}

	logged, unreadable, err := f.sweeps.List()
	if err != nil || len(unreadable) > 0 || len(logged) != 1 {
		t.Fatalf("sweep log = %+v, %+v, %v, want the one pass", logged, unreadable, err)
	}
	if got := logged[0]; got.Task != config.MaintenanceTaskName || got.Role != "" || !got.HarnessPass() || len(got.Steps) != len(want) {
		t.Errorf("recorded pass = %+v, want the harness's own pass under %q", got, config.MaintenanceTaskName)
	}
	claim, found, err := f.sweeps.Find(config.MaintenanceTaskName)
	if err != nil || !found || !claim.Settled() || claim.Problem != "" {
		t.Errorf("claim = %+v, %t, %v, want it settled with no problem", claim, found, err)
	}

	// The next look inside the cadence takes nothing; one past it takes the next.
	f.now = f.now.Add(5 * time.Minute)
	f.pass.Look(context.Background(), f.now)
	if f.pass.running != nil {
		t.Errorf("a pass began five minutes into a ten-minute cadence")
	}
	f.now = f.now.Add(6 * time.Minute)
	f.pass.Look(context.Background(), f.now)
	if f.pass.running == nil {
		t.Errorf("no pass began once the cadence came round")
	}
	f.pass.Wait(context.Background())
}

// The provider guard: while the provider is not logged in, the pass restarts
// nothing — no requested restart, no take-up of a deployed build — and says so
// on each step it held. Reconcile, which restarts nothing, still runs.
func TestNothingIsRestartedWhileTheProviderIsNotLoggedIn(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, err := f.outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnauthenticated, Detail: "Invalid API key · Please run /login", At: f.now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.requests.Request(runstate.RestartRequest{SchemaVersion: runstate.RestartRequestSchemaVersion, ProductID: "calc", ID: "restart-1", Agent: "flow-pm", Part: config.ServiceSlack, Reason: "the sink has posted nothing for an hour", RequestedAt: f.now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	f.host.deployed = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	recorded := f.pass1(t)
	if step := stepNamed(t, recorded, StepProvider); step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "nothing is restarted while it stands") {
		t.Errorf("provider step = %+v, want the outage read and the hold said", step)
	}
	if step := stepNamed(t, recorded, StepReconcile); step.Outcome != runstate.StepRan {
		t.Errorf("reconcile step = %+v, want it run through the outage", step)
	}
	if step := stepNamed(t, recorded, StepRedeploy); step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "nothing is restarted while") {
		t.Errorf("redeploy step = %+v, want it skipped naming the outage", step)
	}
	if len(f.host.restarts) != 0 || f.host.takenUp != "" {
		t.Errorf("restarted %v and took up %q during an outage, want nothing", f.host.restarts, f.host.takenUp)
	}
	answered, err := f.requests.List()
	if err != nil || len(answered) != 1 || answered[0].Open() || !strings.Contains(answered[0].Answer, "not restarted: nothing is restarted while") {
		t.Errorf("request = %+v, %v, want it answered as refused for the outage", answered, err)
	}
	onlyReconcile(t, f.runner)

	// Once the provider answers again, the same deployed build is taken up.
	if _, _, err := f.outages.Clear(); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(11 * time.Minute)
	recorded = f.pass1(t)
	if step := stepNamed(t, recorded, StepRedeploy); step.Outcome != runstate.StepRan || f.host.takenUp != f.host.deployed {
		t.Errorf("redeploy step = %+v and took up %q, want the deployed build taken up once the provider answers", step, f.host.takenUp)
	}
}

// A program manager's request is answered on the request with what the
// supervisor did, and the step says it.
func TestAnOpenRestartRequestIsActedOnAndAnswered(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, err := f.requests.Request(runstate.RestartRequest{SchemaVersion: runstate.RestartRequestSchemaVersion, ProductID: "calc", ID: "restart-1", Agent: "flow-pm", Part: config.ServiceSlack, Reason: "stuck", RequestedAt: f.now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}

	recorded := f.pass1(t)
	if step := stepNamed(t, recorded, StepRestartRequests); step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "restart-1 (flow-pm asked for the slack): stopped pid 7") {
		t.Errorf("restart-requests step = %+v", step)
	}
	if len(f.host.restarts) != 1 || f.host.restarts[0] != config.ServiceSlack {
		t.Errorf("restarts = %v, want the sink", f.host.restarts)
	}
	open, err := f.requests.Open()
	if err != nil || len(open) != 0 {
		t.Errorf("open requests = %+v, %v, want the one answered", open, err)
	}
}

// A supervisor behind the deployed build takes it up after the pass is
// recorded, and waits a pass where a part is still being moved.
func TestTheSupervisorTakesADeployedBuildUpOnceNothingIsMoving(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.host.deployed = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	f.host.moving = "slack"

	recorded := f.pass1(t)
	if step := stepNamed(t, recorded, StepRedeploy); step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "once the slack has settled") || f.host.takenUp != "" {
		t.Errorf("redeploy step = %+v, took up %q, want it waiting for the sink", step, f.host.takenUp)
	}

	f.host.moving = ""
	f.now = f.now.Add(11 * time.Minute)
	recorded = f.pass1(t)
	if step := stepNamed(t, recorded, StepRedeploy); step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "re-executes into the deployed build") || f.host.takenUp != f.host.deployed {
		t.Errorf("redeploy step = %+v, took up %q, want the build taken up", step, f.host.takenUp)
	}
	if logged, _, err := f.sweeps.List(); err != nil || len(logged) != 2 {
		t.Errorf("sweep log holds %d passes (%v), want the take-up recorded before it happens", len(logged), err)
	}
}

// A failed step is failed on the record, on its problem, and on the claim, so
// a schedule that runs and achieves nothing is findable; and the sink left
// degraded is a failure rather than something the pass passes over.
func TestAFailedStepIsOnTheRecordAndTheClaim(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.runner.result = execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "reconcile failed: the tracker is locked"}
	f.pass.Rebuild = nil
	f.pass.NoRebuild = "the binary /opt/calc/bin/yoyo is not built from this checkout"
	f.host.states[config.ServiceSlack] = runstate.SupervisedChild{Service: config.ServiceSlack, State: runstate.ChildDegraded, Reason: "its tokens are not stored"}

	recorded := f.pass1(t)
	if step := stepNamed(t, recorded, StepReconcile); step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "the tracker is locked") {
		t.Errorf("reconcile step = %+v, want the failure with what it said", step)
	}
	if step := stepNamed(t, recorded, StepRebuild); step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "not built from this checkout") {
		t.Errorf("rebuild step = %+v, want it skipped saying why", step)
	}
	if step := stepNamed(t, recorded, StepSlack); step.Outcome != runstate.StepFailed {
		t.Errorf("slack step = %+v, want a degraded sink failed", step)
	}
	if !strings.Contains(recorded.Problem, "reconcile:") || !strings.Contains(recorded.Problem, "slack:") {
		t.Errorf("problem = %q, want both failures", recorded.Problem)
	}
	claim, _, err := f.sweeps.Find(config.MaintenanceTaskName)
	if err != nil || claim.Problem != recorded.Problem {
		t.Errorf("claim problem = %q, %v, want the pass's", claim.Problem, err)
	}
	if err := recorded.Validate(); err != nil {
		t.Errorf("the recorded pass does not validate: %v", err)
	}
}

// The pass's reconcile runs beside the supervisor's looks rather than holding
// them: a look while it is still running returns at once, and the pass is
// recorded at the first look after it comes back.
func TestReconcileDoesNotHoldTheSupervisorsLooks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	release := make(chan struct{})
	f.pass.Runner = blockingRunner{release: release, result: f.runner.result}

	f.pass.Look(context.Background(), f.now)
	done := make(chan struct{})
	go func() {
		f.pass.Look(context.Background(), f.now.Add(5*time.Second))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a look waited on the pass's reconcile")
	}
	if _, found := f.pass.Last(); found {
		t.Fatal("a pass was recorded before its reconcile came back")
	}
	close(release)
	f.pass.Wait(context.Background())
	if _, found := f.pass.Last(); !found {
		t.Fatal("no pass was recorded once reconcile came back")
	}
}

type blockingRunner struct {
	release chan struct{}
	result  execution.ProcessResult
}

func (r blockingRunner) Run(ctx context.Context, _ execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return r.result, nil
}

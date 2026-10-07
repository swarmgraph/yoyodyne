//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// These tests launch real processes and kill the launcher at each point a
// harness can die, then recover from what the record says. The launcher runs
// as a separate process — this test binary again, under launchHelperMode — so
// that dying at a launch point is the kernel ending a process, closing its
// descriptors and leaving its children behind, exactly as a crashed harness
// does. The provider is a shell script that appends a line to a count file as
// the first thing it does, so the number of provider processes that began is
// read off the file rather than inferred. Every process a test starts ends on
// its own within a few seconds, whatever the test does.

const (
	launchHelperMode    = "YOYODYNE_LAUNCH_HELPER"
	launchHelperConfig  = "YOYODYNE_LAUNCH_CONFIG"
	launchHelperReady   = "YOYODYNE_LAUNCH_READY"
	launchHelperRelease = "YOYODYNE_LAUNCH_RELEASE"
)

// launchConfig is what one launcher process is asked to do.
type launchConfig struct {
	Base      string `json:"base"`
	RunID     string `json:"run_id"`
	Operation string `json:"operation"`
	Attempt   string `json:"attempt"`
	// Crash is the launch point the launcher kills itself at, "output" for the
	// provider's first line of output, or empty to run to the end.
	Crash string `json:"crash,omitempty"`
	// Script is the provider: a shell script.
	Script string `json:"script"`
	// Classification is how the launcher classifies a provider that exited
	// successfully, "succeeded" when empty.
	Classification string `json:"classification,omitempty"`
}

// Exit statuses of a recovering launcher.
const (
	recoverLaunched   = 0
	recoverHeld       = 3
	recoverGaveUp     = 4
	recoverNoLaunch   = 5
	launchRefusedExit = 2
)

func TestLaunchHelperProcess(t *testing.T) {
	mode := os.Getenv(launchHelperMode)
	if mode == "" {
		return
	}
	if mode == "escape" {
		// A descendant that leaves the provider's process group, as a tool that
		// puts itself in a session of its own does, still holding what it
		// inherited. It ends on its own.
		_ = syscall.Setpgid(0, 0)
		if ready := os.Getenv(launchHelperReady); ready != "" {
			_ = os.WriteFile(ready, nil, 0o600)
		}
		// It stays until the test lets it go, and never past five seconds.
		release := os.Getenv(launchHelperRelease)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(release); err == nil {
				break
			}
		}
		os.Exit(0)
	}
	var config launchConfig
	if err := json.Unmarshal([]byte(os.Getenv(launchHelperConfig)), &config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	store, err := NewStore(config.Base, "yoyodyne")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch mode {
	case "launch":
		os.Exit(helperLaunch(store, config))
	case "recover":
		os.Exit(helperRecover(store, config))
	}
	os.Exit(1)
}

func crashHere() {
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}

func helperLaunch(store *Store, config launchConfig) int {
	ctx := context.Background()
	launch, err := store.BeginLaunch(ctx, config.RunID, config.Operation, config.Attempt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "begin launch:", err)
		return launchRefusedExit
	}
	launch.pause = func(point launchPoint) {
		if string(point) == config.Crash {
			crashHere()
		}
	}
	result, runErr := execution.OSProcessRunner{}.Run(ctx, execution.Command{
		Name: "/bin/sh", Args: []string{"-c", config.Script}, Env: os.Environ(),
		Gate: launch.Gate(), Timeout: 20 * time.Second,
	}, func(execution.Output) {
		if config.Crash == "output" {
			crashHere()
		}
	})
	classification := string(result.Status)
	if result.Status == execution.ProcessSucceeded && config.Classification != "" {
		classification = config.Classification
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "run:", runErr)
		classification = "not_started"
	}
	termination, err := launch.Finish(AttemptEnding{Classification: classification})
	if err != nil {
		fmt.Fprintln(os.Stderr, "finish:", err)
		return 1
	}
	fmt.Println(termination)
	return 0
}

func helperRecover(store *Store, config launchConfig) int {
	ctx := context.Background()
	_, lease, err := store.AdoptRun(ctx, config.RunID)
	if errors.Is(err, ErrRunHeld) {
		return recoverHeld
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "adopt:", err)
		return 1
	}
	defer lease.Release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		found, err := store.ReconcileLaunch(ctx, config.RunID, config.Operation, LaunchRecovery{})
		if err != nil {
			fmt.Fprintln(os.Stderr, "reconcile:", err)
			return 1
		}
		switch found.Verdict {
		case LaunchNeverStarted:
			config.Attempt = found.Attempt
			return helperLaunch(store, config)
		case LaunchRunning, LaunchUncertain:
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return recoverNoLaunch
	}
	return recoverGaveUp
}

// launchFixture is one run with an open developer operation and its first
// attempt reserved, and the files its provider reports through.
type launchFixture struct {
	store   *Store
	base    string
	runID   string
	count   string
	outcome string
}

func newLaunchFixture(t *testing.T) launchFixture {
	t.Helper()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	route(t, store, state, prepare("op-1", "att-1"))
	scratch := t.TempDir()
	return launchFixture{
		store: store, base: filepath.Dir(filepath.Dir(filepath.Dir(store.Root()))), runID: state.RunID,
		count: filepath.Join(scratch, "launches"), outcome: filepath.Join(scratch, "outcome"),
	}
}

// provider is a provider script that records it began, then does body.
func (f launchFixture) provider(body string) string {
	return fmt.Sprintf(`printf '%%s\n' started >> %q; %s`, f.count, body)
}

// escaping starts a descendant that leaves the provider's process group and
// stays until release exists, and waits, for a bounded while, until it has
// left.
func (f launchFixture) escaping(t *testing.T, release string) string {
	ready := filepath.Join(t.TempDir(), "escaped")
	return fmt.Sprintf(`%s=%q %s=%q %s=escape "$TEST_BINARY" -test.run='^TestLaunchHelperProcess$' >/dev/null 2>&1 & i=0; while [ ! -e %q ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`,
		launchHelperReady, ready, launchHelperRelease, release, launchHelperMode, ready)
}

// staying starts a descendant that stays in the provider's process group
// until release exists, and never past five seconds.
func staying(release string) string {
	return fmt.Sprintf(`(i=0; while [ ! -e %q ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done) &`, release)
}

// letGo releases the descendants a test started.
func letGo(t *testing.T, release string) {
	t.Helper()
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// finishing is a provider that writes its result and exits.
func (f launchFixture) finishing() string {
	return f.provider(fmt.Sprintf(`echo finished > %q`, f.outcome))
}

func (f launchFixture) config(attempt, crash, script string) launchConfig {
	return launchConfig{Base: f.base, RunID: f.runID, Operation: "op-1", Attempt: attempt, Crash: crash, Script: script}
}

// runLauncher runs one launcher process to its end, which for a crash is the
// kernel killing it, and returns its exit status.
func runLauncher(t *testing.T, mode string, config launchConfig) int {
	t.Helper()
	command := launcherCommand(t, mode, config)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		t.Logf("%s launcher for %s at %q ended %v: %s", mode, config.Attempt, config.Crash, exit, output)
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return -1
		}
		return exit.ExitCode()
	}
	if err != nil {
		t.Fatalf("run %s launcher: %v", mode, err)
	}
	return 0
}

func launcherCommand(t *testing.T, mode string, config launchConfig) *exec.Cmd {
	t.Helper()
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-test.run=^TestLaunchHelperProcess$")
	command.Env = append(os.Environ(), launchHelperMode+"="+mode, launchHelperConfig+"="+string(encoded),
		"TEST_BINARY="+binary)
	return command
}

func (f launchFixture) launches(t *testing.T) int {
	t.Helper()
	encoded, err := os.ReadFile(f.count)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(encoded), "started\n")
}

func (f launchFixture) operation(t *testing.T) RoutedOperation {
	t.Helper()
	operation, ok := load(t, f.store, f.runID).Routing.Operation("op-1")
	if !ok {
		t.Fatal("operation op-1 is not recorded")
	}
	return *operation
}

func (f launchFixture) attempt(t *testing.T, id string) InvocationAttempt {
	t.Helper()
	operation := f.operation(t)
	attempt, ok := operation.attempt(id)
	if !ok {
		t.Fatalf("attempt %s is not recorded: %+v", id, operation.Attempts)
	}
	return *attempt
}

// readOutcome is the result a finished provider left, as a caller reading the
// run's event log would find its terminal event.
func (f launchFixture) readOutcome(InvocationAttempt) (*AttemptEnding, error) {
	encoded, err := os.ReadFile(f.outcome)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &AttemptEnding{Classification: "succeeded", Result: strings.TrimSpace(string(encoded))}, nil
}

// reconcileUntil reconciles until the verdict is no longer one recovery waits
// on, and returns every verdict it saw on the way.
func (f launchFixture) reconcileUntil(t *testing.T, recovery LaunchRecovery) []LaunchReconciliation {
	t.Helper()
	var seen []LaunchReconciliation
	deadline := time.Now().Add(10 * time.Second)
	for {
		found, err := f.store.ReconcileLaunch(context.Background(), f.runID, "op-1", recovery)
		if err != nil {
			t.Fatalf("ReconcileLaunch() error = %v", err)
		}
		seen = append(seen, found)
		if found.Verdict != LaunchRunning && found.Verdict != LaunchUncertain {
			return seen
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconciliation still waits: %+v", seen[len(seen)-1])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// assertUnspent fails if the operation's endpoint selection or switch
// allowance moved from what an operation that never switched has.
func (f launchFixture) assertUnspent(t *testing.T) {
	t.Helper()
	operation := f.operation(t)
	if operation.Selected != EndpointPrimary || operation.SwitchAllowance != 1 || operation.Switch != nil {
		t.Fatalf("recovery spent the operation's switch: %+v", operation)
	}
	if relaunches := operation.TransientRelaunches; relaunches == nil || *relaunches != 0 {
		t.Fatalf("recovery counted a relaunch: %v", relaunches)
	}
}

// A launcher that dies before it starts anything leaves the attempt reserved
// and nothing running: recovery launches that same attempt, once.
func TestALauncherKilledBeforeLaunchLeavesTheReservedAttemptToLaunchOnce(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	if status := runLauncher(t, "launch", f.config("att-1", string(launchHoldTaken), f.finishing())); status != -1 {
		t.Fatalf("launcher exit = %d, want killed", status)
	}
	seen := f.reconcileUntil(t, LaunchRecovery{Outcome: f.readOutcome})
	if last := seen[len(seen)-1]; last.Verdict != LaunchNeverStarted || last.Attempt != "att-1" {
		t.Fatalf("reconciliation after a kill before launch = %+v", last)
	}
	if got := f.launches(t); got != 0 {
		t.Fatalf("providers launched before recovery = %d, want 0", got)
	}
	if status := runLauncher(t, "launch", f.config("att-1", "", f.finishing())); status != 0 {
		t.Fatalf("relaunch exit = %d", status)
	}
	if got := f.launches(t); got != 1 {
		t.Fatalf("providers launched = %d, want 1", got)
	}
	attempt := f.attempt(t, "att-1")
	if attempt.State != AttemptEnded || attempt.Ended.Termination != TerminationConfirmed || len(f.operation(t).Attempts) != 1 {
		t.Fatalf("the reserved attempt after its one launch = %+v", attempt)
	}
	f.assertUnspent(t)
}

// A launcher that dies after the process exists but before the launch is
// acknowledged leaves a shell waiting at a gate nobody will open: it exits
// without running the provider, and the same reserved attempt is launched again
// once it has gone — never while it might still be there.
func TestALauncherKilledAfterSpawnBeforeAcknowledgementRunsNoProviderAndReusesTheAttempt(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	if status := runLauncher(t, "launch", f.config("att-1", string(launchRegistered), f.finishing())); status != -1 {
		t.Fatalf("launcher exit = %d, want killed", status)
	}
	registered := f.attempt(t, "att-1")
	if registered.State != AttemptPrepared || registered.Execution == nil || registered.Execution.PID <= 0 {
		t.Fatalf("the attempt the launcher registered = %+v", registered)
	}
	seen := f.reconcileUntil(t, LaunchRecovery{Outcome: f.readOutcome})
	for _, found := range seen[:len(seen)-1] {
		if found.Verdict == LaunchUncertain {
			t.Fatalf("an unreleased launch was reported uncertain rather than waited out: %+v", found)
		}
	}
	if last := seen[len(seen)-1]; last.Verdict != LaunchNeverStarted || last.Attempt != "att-1" {
		t.Fatalf("reconciliation = %+v", last)
	}
	if got := f.launches(t); got != 0 {
		t.Fatalf("the gate opened: providers launched = %d, want 0", got)
	}
	if status := runLauncher(t, "launch", f.config("att-1", "", f.finishing())); status != 0 {
		t.Fatalf("relaunch exit = %d", status)
	}
	attempt := f.attempt(t, "att-1")
	if got := f.launches(t); got != 1 || attempt.State != AttemptEnded || len(attempt.Unreleased) != 1 || attempt.Unreleased[0].PID != registered.Execution.PID {
		t.Fatalf("after relaunch: providers = %d, attempt = %+v", got, attempt)
	}
	f.assertUnspent(t)
}

// Marked launched is written before the gate opens, so a launcher killed
// between the two leaves an attempt that may have begun as far as the record
// can say. It is not launched again: it is ended as interrupted, confirmed
// stopped, and whatever follows is a new attempt.
func TestALauncherKilledBetweenMarkingAndOpeningTheGateEndsTheAttemptInterrupted(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	if status := runLauncher(t, "launch", f.config("att-1", string(launchMarked), f.finishing())); status != -1 {
		t.Fatalf("launcher exit = %d, want killed", status)
	}
	seen := f.reconcileUntil(t, LaunchRecovery{Outcome: f.readOutcome})
	last := seen[len(seen)-1]
	attempt := f.attempt(t, "att-1")
	if last.Verdict != LaunchInterrupted || attempt.Ended == nil || attempt.Ended.Classification != InterruptedClassification || attempt.Ended.Termination != TerminationConfirmed {
		t.Fatalf("reconciliation = %+v, attempt = %+v", last, attempt)
	}
	if got := f.launches(t); got != 0 {
		t.Fatalf("providers launched = %d, want 0", got)
	}
	f.assertUnspent(t)
}

// A provider that went on running after its launcher died is adopted while it
// runs, and its result is adopted once it finishes: nothing is launched again.
func TestARunningProviderIsAdoptedAndItsResultIsAdoptedAfterTheLauncherDies(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	release := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	script := f.provider(fmt.Sprintf(`echo working; i=0; while [ ! -e %q ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done; echo finished > %q`, release, f.outcome))
	if status := runLauncher(t, "launch", f.config("att-1", "output", script)); status != -1 {
		t.Fatalf("launcher exit = %d, want killed", status)
	}
	found, err := f.store.ReconcileLaunch(context.Background(), f.runID, "op-1", LaunchRecovery{Outcome: f.readOutcome})
	if err != nil || found.Verdict != LaunchRunning {
		t.Fatalf("reconciliation while the provider runs = %+v, %v", found, err)
	}
	letGo(t, release)
	seen := f.reconcileUntil(t, LaunchRecovery{Outcome: f.readOutcome})
	last := seen[len(seen)-1]
	if last.Verdict != LaunchFinished || last.Ending == nil || last.Ending.Result != "finished" {
		t.Fatalf("reconciliation once it finished = %+v", last)
	}
	attempt := f.attempt(t, "att-1")
	if got := f.launches(t); got != 1 || attempt.Ended.Classification != "succeeded" || attempt.Ended.Termination != TerminationConfirmed {
		t.Fatalf("providers = %d, attempt = %+v", got, attempt)
	}
	if f.operation(t).Reconciling != nil {
		t.Fatalf("recovery still records a wait after it settled: %+v", f.operation(t).Reconciling)
	}
	f.assertUnspent(t)
}

// A launcher that dies after its provider finished but before it recorded the
// outcome leaves an outcome recovery can read: it is adopted, not relaunched.
func TestALauncherKilledAfterCompletionBeforeRecordingTheOutcomeHasItAdopted(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	if status := runLauncher(t, "launch", f.config("att-1", string(launchExited), f.finishing())); status != -1 {
		t.Fatalf("launcher exit = %d, want killed", status)
	}
	if attempt := f.attempt(t, "att-1"); attempt.State != AttemptLaunched {
		t.Fatalf("the attempt before recovery = %+v", attempt)
	}
	seen := f.reconcileUntil(t, LaunchRecovery{Outcome: f.readOutcome})
	if last := seen[len(seen)-1]; last.Verdict != LaunchFinished || last.Ending.Result != "finished" {
		t.Fatalf("reconciliation = %+v", last)
	}
	if got := f.launches(t); got != 1 {
		t.Fatalf("providers launched = %d, want 1", got)
	}
	f.assertUnspent(t)
}

// A provider whose own process has exited while a child it started is still
// running has not stopped. The provider's process identifier answers nothing,
// yet the attempt is reported running until the child is gone too — whether
// the child stayed in the provider's process group or left it.
func TestASurvivingChildKeepsTheAttemptRunningAfterTheProviderProcessExits(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	release := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	script := f.provider(fmt.Sprintf(`%s %s; echo working; sleep 0.2; echo finished > %q`, staying(release), f.escaping(t, release), f.outcome))
	if status := runLauncher(t, "launch", f.config("att-1", "output", script)); status != -1 {
		t.Fatalf("launcher exit = %d, want killed", status)
	}
	identity := f.attempt(t, "att-1").Execution
	deadline := time.Now().Add(5 * time.Second)
	for {
		if running, _ := processIsRunning(identity.PID); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the provider process did not exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The provider's own process is gone; a check of its identifier alone
	// would call the attempt stopped here.
	found, err := f.store.ReconcileLaunch(context.Background(), f.runID, "op-1", LaunchRecovery{Outcome: f.readOutcome})
	if err != nil {
		t.Fatal(err)
	}
	if found.Verdict != LaunchRunning {
		t.Fatalf("reconciliation with the provider gone and its children alive = %+v", found)
	}
	if waiting := f.operation(t).Reconciling; waiting == nil || waiting.Attempt != "att-1" {
		t.Fatalf("recovery recorded no wait: %+v", waiting)
	}
	if _, err := f.store.BeginLaunch(context.Background(), f.runID, "op-1", "att-1"); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("BeginLaunch() while a child survives: error = %v", err)
	}
	letGo(t, release)
	seen := f.reconcileUntil(t, LaunchRecovery{Outcome: f.readOutcome})
	if last := seen[len(seen)-1]; last.Verdict != LaunchFinished {
		t.Fatalf("reconciliation once every child has gone = %+v", last)
	}
	if got := f.launches(t); got != 1 {
		t.Fatalf("providers launched = %d, want 1", got)
	}
	f.assertUnspent(t)
}

// A recorded process identifier that now belongs to an unrelated live process
// is not the attempt running: what decides is the attempt's hold and its
// process group, and both are free.
func TestAReusedProcessIdentifierIsNotTakenForTheAttempt(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	unrelated := exec.Command("sleep", "3")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() })
	identity := f.recordExecution(t, "att-1", unrelated.Process.Pid)
	if running, _ := processIsRunning(identity.PID); !running {
		t.Fatal("the unrelated process is not running")
	}
	found, err := f.store.ReconcileLaunch(context.Background(), f.runID, "op-1", LaunchRecovery{})
	if err != nil {
		t.Fatal(err)
	}
	if found.Verdict != LaunchInterrupted {
		t.Fatalf("reconciliation with the identifier reused = %+v", found)
	}
	if got := f.launches(t); got != 0 {
		t.Fatalf("providers launched = %d, want 0", got)
	}
	f.assertUnspent(t)
}

// recordExecution writes the record a launch made for an execution with the
// given process identifier and a hold nothing holds, as if its launcher had
// died after marking it launched.
func (f launchFixture) recordExecution(t *testing.T, attempt string, pid int) ExecutionIdentity {
	t.Helper()
	path, err := f.store.holdPath(f.runID, attempt)
	if err != nil {
		t.Fatal(err)
	}
	file := "mark-of-" + attempt
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		Host: host, Boot: currentBoot(), Launcher: "launcher-earlier", PID: pid, ProcessGroup: pid,
		StartedAt: routingAt, Hold: holdName(attempt), HoldFile: file, RegisteredAt: routingAt,
	}
	state := load(t, f.store, f.runID)
	state = route(t, f.store, state, func(r *RunRouting) (bool, error) { return r.RegisterExecution("op-1", attempt, identity) })
	route(t, f.store, state, func(r *RunRouting) (bool, error) { return r.MarkLaunched("op-1", attempt, routingAt) })
	return identity
}

// Evidence nothing can read is not evidence of a stop: recovery waits, records
// why, launches nothing, and spends nothing.
func TestUnreadableExecutionEvidenceWaitsWithItsReasonRecorded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spoil   func(t *testing.T, f launchFixture, identity ExecutionIdentity)
		signal  func(int) error
		because string
	}{
		{name: "hold missing", because: "is missing", spoil: func(t *testing.T, f launchFixture, identity ExecutionIdentity) {
			path, _ := f.store.holdPath(f.runID, "att-1")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "hold unreadable", because: "cannot be opened", spoil: func(t *testing.T, f launchFixture, identity ExecutionIdentity) {
			if os.Geteuid() == 0 {
				t.Skip("a process running as root opens a file whatever its permissions")
			}
			path, _ := f.store.holdPath(f.runID, "att-1")
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "hold replaced", because: "has been replaced", spoil: func(t *testing.T, f launchFixture, identity ExecutionIdentity) {
			path, _ := f.store.holdPath(f.runID, "att-1")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "hold rewritten in place", because: "has been replaced", spoil: func(t *testing.T, f launchFixture, identity ExecutionIdentity) {
			// The same inode with different contents, which is what a file
			// system that reuses a removed file's inode hands a new file.
			path, _ := f.store.holdPath(f.runID, "att-1")
			if err := os.WriteFile(path, []byte("another launch's mark"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "group not permitted", because: "not permitted", signal: func(int) error { return syscall.EPERM }},
		{name: "group has members", because: "still has members", signal: func(int) error { return nil }},
		{name: "hold is a link", because: "cannot be opened", spoil: func(t *testing.T, f launchFixture, identity ExecutionIdentity) {
			path, _ := f.store.holdPath(f.runID, "att-1")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newLaunchFixture(t)
			f.store.signalGroup = tc.signal
			identity := f.recordExecution(t, "att-1", 999999)
			if tc.spoil != nil {
				tc.spoil(t, f, identity)
			}
			found, err := f.store.ReconcileLaunch(context.Background(), f.runID, "op-1", LaunchRecovery{})
			if err != nil {
				t.Fatal(err)
			}
			if found.Verdict != LaunchUncertain || !strings.Contains(found.Reason, tc.because) {
				t.Fatalf("reconciliation = %+v, want uncertain because it %s", found, tc.because)
			}
			operation := f.operation(t)
			if operation.Reconciling == nil || operation.Reconciling.Reason != found.Reason {
				t.Fatalf("the recorded wait = %+v", operation.Reconciling)
			}
			if attempt := f.attempt(t, "att-1"); attempt.State != AttemptLaunched || len(operation.Attempts) != 1 {
				t.Fatalf("recovery changed the attempts: %+v", operation.Attempts)
			}
			state := load(t, f.store, f.runID)
			if _, err := f.store.UpdateRouting(context.Background(), state, prepare("op-1", "att-2")); !errors.Is(err, ErrRoutingConflict) {
				t.Fatalf("preparing another attempt while uncertain: error = %v", err)
			}
			if got := f.launches(t); got != 0 {
				t.Fatalf("providers launched = %d, want 0", got)
			}
			f.assertUnspent(t)
		})
	}
	t.Run("another host", func(t *testing.T) {
		t.Parallel()
		f := newLaunchFixture(t)
		identity := f.recordExecution(t, "att-1", 999999)
		identity.Host = "elsewhere.invalid"
		f.store.signalGroup = func(int) error { return syscall.ESRCH }
		found, reason, settle := f.store.observeExecution(f.runID, identity)
		settle(false)
		if found != executionUnknown || !strings.Contains(reason, "elsewhere.invalid") {
			t.Fatalf("observing an execution on another host = %v, %q", found, reason)
		}
	})
}

// Two recoveries started at once for one run launch the reserved attempt once
// between them: the run's lease admits one, and the attempt's hold and record
// refuse the other even where it got that far.
func TestTwoCompetingRecoveriesLaunchTheAttemptOnce(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	if status := runLauncher(t, "launch", f.config("att-1", string(launchRegistered), f.finishing())); status != -1 {
		t.Fatalf("launcher exit = %d, want killed", status)
	}
	var wait sync.WaitGroup
	statuses := make([]int, 2)
	for index := range statuses {
		wait.Add(1)
		go func() {
			defer wait.Done()
			statuses[index] = runLauncher(t, "recover", f.config("att-1", "", f.finishing()))
		}()
	}
	wait.Wait()
	launched := 0
	for _, status := range statuses {
		switch status {
		case recoverLaunched:
			launched++
		case recoverHeld, recoverNoLaunch, launchRefusedExit:
		default:
			t.Fatalf("recovery exit statuses = %v", statuses)
		}
	}
	if got := f.launches(t); launched != 1 || got != 1 {
		t.Fatalf("recoveries that launched = %d, providers launched = %d, statuses %v; want 1 and 1", launched, got, statuses)
	}
	if attempt := f.attempt(t, "att-1"); attempt.State != AttemptEnded || len(f.operation(t).Attempts) != 1 {
		t.Fatalf("the attempt = %+v", attempt)
	}
	f.assertUnspent(t)
}

// Two launches of one reserved attempt racing inside one process: the hold
// admits one, so one provider runs.
func TestTwoLaunchesOfOneAttemptCannotBothStart(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	first, err := f.store.BeginLaunch(context.Background(), f.runID, "op-1", "att-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.BeginLaunch(context.Background(), f.runID, "op-1", "att-1"); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("a second BeginLaunch() while the first holds the attempt: error = %v", err)
	}
	runner := execution.OSProcessRunner{}
	if _, err := runner.Run(context.Background(), execution.Command{
		Name: "/bin/sh", Args: []string{"-c", f.finishing()}, Gate: first.Gate(), Timeout: 10 * time.Second,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if termination, err := first.Finish(AttemptEnding{Classification: "succeeded"}); err != nil || termination != TerminationConfirmed {
		t.Fatalf("Finish() = %v, %v", termination, err)
	}
	if _, err := f.store.BeginLaunch(context.Background(), f.runID, "op-1", "att-1"); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("BeginLaunch() of an ended attempt: error = %v", err)
	}
	if got := f.launches(t); got != 1 {
		t.Fatalf("providers launched = %d", got)
	}
	if _, err := os.Stat(filepath.Join(f.store.Root(), f.runID+"."+holdName("att-1"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the hold of a confirmed stopped attempt is left behind: %v", err)
	}
}

// A switch's destination cannot start while its source, or anything the
// source started, may still be running; once the source is confirmed stopped
// the destination launches under its reserved identity, survives its own
// launcher dying before acknowledgement, and launches once — and the switch
// allowance the operation spent is spent once.
func TestAReplacementWaitsForTheSourceTreeAndSurvivesItsOwnLauncherDying(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	// The source reaches its usage limit and exits, leaving behind a child that
	// left its process group.
	release := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	source := f.config("att-1", "", f.provider(f.escaping(t, release)+"; exit 0"))
	source.Classification = UsageLimitClassification
	if status := runLauncher(t, "launch", source); status != 0 {
		t.Fatalf("source launcher exit = %d", status)
	}
	ended := f.attempt(t, "att-1")
	if ended.Ended == nil || ended.Ended.Classification != UsageLimitClassification || ended.Ended.Termination != TerminationUncertain {
		t.Fatalf("the source with a child still running = %+v", ended.Ended)
	}
	state := route(t, f.store, load(t, f.store, f.runID), planSwitch("op-1", "sw-1", "att-1", "att-2"))
	if _, err := f.store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) { return r.ReconcileSource("op-1", "sw-1", routingAt) }); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("reconciling a source whose child is alive: error = %v", err)
	}
	if _, err := f.store.UpdateRouting(context.Background(), state, prepare("op-1", "att-2")); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("preparing the destination before the source stopped: error = %v", err)
	}
	found, err := f.store.ReconcileLaunch(context.Background(), f.runID, "op-1", LaunchRecovery{})
	if err != nil || found.Verdict != LaunchRunning || found.Attempt != "att-1" {
		t.Fatalf("reconciliation with the source's child alive = %+v, %v", found, err)
	}
	letGo(t, release)
	seen := f.reconcileUntil(t, LaunchRecovery{})
	if last := seen[len(seen)-1]; last.Verdict != LaunchFinished || last.Ending.Termination != TerminationConfirmed {
		t.Fatalf("reconciliation once the child has gone = %+v", last)
	}
	operation := f.operation(t)
	if operation.Switch.Progress != TransitionSourceReconciled || operation.SwitchAllowance != 0 {
		t.Fatalf("the switch once its source stopped = %+v, allowance %d", operation.Switch, operation.SwitchAllowance)
	}
	route(t, f.store, load(t, f.store, f.runID), prepare("op-1", "att-2"))
	if status := runLauncher(t, "launch", f.config("att-2", string(launchRegistered), f.finishing())); status != -1 {
		t.Fatalf("destination launcher exit = %d, want killed", status)
	}
	seen = f.reconcileUntil(t, LaunchRecovery{})
	if last := seen[len(seen)-1]; last.Verdict != LaunchNeverStarted || last.Attempt != "att-2" {
		t.Fatalf("reconciliation of the destination = %+v", last)
	}
	if status := runLauncher(t, "launch", f.config("att-2", "", f.finishing())); status != 0 {
		t.Fatalf("destination relaunch exit = %d", status)
	}
	operation = f.operation(t)
	if got := f.launches(t); got != 2 {
		t.Fatalf("providers launched = %d, want the source and the destination once each", got)
	}
	if len(operation.Attempts) != 2 || operation.SwitchAllowance != 0 || operation.Selected != EndpointAlternate || operation.Switch.Progress != TransitionOutcomeRecorded {
		t.Fatalf("the operation after its replacement = %+v", operation)
	}
	if destination := f.attempt(t, "att-2"); len(destination.Unreleased) != 1 || destination.Ended.Termination != TerminationConfirmed {
		t.Fatalf("the destination = %+v", destination)
	}
}

// A boot the machine has since left behind is a stop whatever else is found,
// where the platform names its boots. Where it does not, the hold being free
// is what recognises a restart, and this says so rather than passing quietly.
func TestAnExecutionFromAnEarlierBootIsStopped(t *testing.T) {
	t.Parallel()
	if currentBoot() == "" {
		t.Skip("this platform, or the sandbox this test runs in, will not name the current boot, so a restart is recognised only by the execution's hold being free")
	}
	f := newLaunchFixture(t)
	identity := f.recordExecution(t, "att-1", 999999)
	identity.Boot = "an-earlier-boot"
	found, _, settle := f.store.observeExecution(f.runID, identity)
	settle(false)
	if found != executionStopped {
		t.Fatalf("observing an execution from an earlier boot = %v", found)
	}
}

// The file that shows whether an attempt's processes are alive is created
// through the pinned run state directory, so a link planted at its name is
// refused rather than followed out of that directory.
func TestALinkPlantedAtAnAttemptsFileIsRefusedRatherThanFollowed(t *testing.T) {
	t.Parallel()
	f := newLaunchFixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	path, err := f.store.holdPath(f.runID, "att-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.BeginLaunch(context.Background(), f.runID, "op-1", "att-1"); err == nil {
		t.Fatal("BeginLaunch() followed a link planted at the attempt's file")
	}
	if _, err := os.Lstat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the link's target was created outside the run state directory: %v", err)
	}
	if got := f.launches(t); got != 0 {
		t.Fatalf("providers launched = %d, want 0", got)
	}
	if attempt := f.attempt(t, "att-1"); attempt.State != AttemptPrepared || attempt.Execution != nil {
		t.Fatalf("the refused launch changed the attempt: %+v", attempt)
	}
}

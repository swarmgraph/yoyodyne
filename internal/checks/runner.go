package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// DefaultTimeout is what a runner built without a budget gives each check. The
// harness wires execution.check_timeout into every runner it builds, so this
// only covers a runner assembled in code, and it matches that configured
// default so the two never describe different behavior. It is exported so the
// record a pipeline writes of a check's limit names the figure the runner
// actually used where the configuration named none.
const DefaultTimeout = 30 * time.Minute

// DefaultStageTimeout is what a runner built without a stage bound gives the
// whole list, for the same reason and matching execution.check_stage_timeout's
// default the same way. It is exported so the record a pipeline writes of a
// stage names the bound the runner actually used where the configuration named
// none.
const DefaultStageTimeout = 30 * time.Minute

// DefaultLandingCheckTimeout is what a landing check is given where the
// configuration names no execution.landing_check_timeout, matching that
// default: a landing runs the suite the gate's stage cannot hold, once, after
// the run is over and its seat is free.
const DefaultLandingCheckTimeout = 2 * time.Hour

type Result struct {
	Command       string                  `json:"command"`
	Process       execution.ProcessResult `json:"process"`
	FailureOutput string                  `json:"failure_output,omitempty"`
	Passed        bool                    `json:"passed"`
	// Timeout is the budget this check was given. It is recorded beside the
	// result rather than left implicit because elapsed time alone says nothing
	// about how close a suite is to the ceiling: a check that grows past the
	// budget kills work that was passing, and the only warning is the two
	// numbers side by side before it happens.
	Timeout time.Duration `json:"timeout"`
	// StageTimeout is the bound on the whole stage this check ran in, and
	// StageElapsed is what the stage had spent when this check ended. They are
	// recorded on every check for the reason the per-check pair is: a stage
	// walking toward its bound is visible check by check before the check that
	// reaches it.
	StageTimeout time.Duration `json:"stage_timeout"`
	StageElapsed time.Duration `json:"stage_elapsed"`
	// StoppedByStage reports a check stopped because the stage reached its bound
	// rather than because the check reached its own. The two are the same
	// process status and different facts: raising the per-check budget does
	// nothing for a check the stage stopped, and a check the stage never let
	// start has no elapsed time to read a budget from.
	StoppedByStage bool `json:"stopped_by_stage,omitempty"`
	// StoppedByCaller reports a check stopped because the context the checks
	// were run under ended — the deadline or cancellation of whatever started
	// the run — rather than because the check or the stage reached a bound. The
	// process reads as timed out either way, so without this a check stopped
	// four minutes in would be reported as stopped at a thirty-minute budget.
	StoppedByCaller bool `json:"stopped_by_caller,omitempty"`
	// NotLaunched reports a check stopped by its caller before its process was
	// launched — a run asked to stop between two checks stops there — so a
	// reader can tell a check the stop never started from one it cut short.
	NotLaunched bool `json:"not_launched,omitempty"`
	// CouldNotRun is the reason a check that exited non-zero gave for not having
	// run at all, by the convention CouldNotRunPrefix describes or by the machine
	// refusing to start its tool (machineRefusals), and empty for every other
	// check. Passed is false beside it, because nothing passed; a
	// reader that judges the change skips such a check rather than counting it
	// as a failure.
	CouldNotRun string `json:"could_not_run,omitempty"`
}

// Elapsed is how long the check actually ran.
func (r Result) Elapsed() time.Duration {
	return r.Process.FinishedAt.Sub(r.Process.StartedAt)
}

// Request is one run's check stage: where it runs, what it runs, and what
// every check is told.
type Request struct {
	RunID        string
	Directory    string
	Commands     []string
	LastSequence uint64
	// Env is what every check is given beyond this process's own environment,
	// in KEY=VALUE form. It is how the harness tells a check what it knows about
	// the change — the Go packages it touches — without the check having to ask.
	Env []string
	// Started is told each check as it begins, with what the stage has spent so
	// far. It is optional, and it is what lets a run's durable record say which
	// check the stage is on while it is still running rather than only once it
	// has ended.
	Started func(command string, stageElapsed time.Duration)
	// Finished is told each check's result as the check ends, before the next
	// one begins. It is optional, and it is what lets a run keep the checks that
	// have already passed if the stage is stopped part way through.
	Finished func(result Result)
	// Timeout, where set, is the budget each check of this request gets in
	// place of the runner's. Unbounded runs the request with no stage bound at
	// all: each check has its budget and the list may take the sum. Both are
	// what a landing asks for — the suite moved to the landing is the one too
	// long for the gate's stage bound, so a landing run under that bound would
	// be stopped every time — and neither reaches a request that leaves them
	// out, which is every per-run gate.
	Timeout   time.Duration
	Unbounded bool
	// ProviderCLIs marks, by position in Commands, the checks run with the
	// provider CLIs left on their search path: a path check whose configuration
	// says it needs them, because what it vouches for is how the harness drives
	// a real one. Every other check has them hidden; searchpath.go says why.
	ProviderCLIs map[int]bool
	// StageBound, where set, is asked for the stage's bound as each check
	// begins, in place of the runner's StageTimeout. Its first answer is the
	// bound; a later answer only ever raises it, so a bound scaled for the
	// machine's load grows as the load does and never takes back time a check
	// was already given. Unbounded still wins over it.
	StageBound func() time.Duration
	// CheckBound, where set, is asked for a check's own limit as that check
	// begins, after StageBound, in place of Timeout and the runner's. A caller
	// uses it to scale each check's limit for the machine's load and to hold a
	// check run again to what its allowance has left. An answer of zero or less
	// leaves the check the budget it would otherwise have had.
	CheckBound func(command string) time.Duration
	// Gate, when set, is asked for the launch gate each check is started
	// behind, so a caller can write down which process a check is before the
	// check does any work, and hand it a file that shows afterwards whether any
	// process it started is still alive (execution.LaunchGate). Nil starts every
	// check the ordinary way.
	Gate func(command string) (*execution.LaunchGate, error)
	// Permit is what this stage's heavy checks ask for their places with
	// (permits.go). It is read only by a runner given Permits.
	Permit PermitRequest
}

type Runner struct {
	Process execution.ProcessRunner
	Clock   execution.Clock
	Shell   string
	// Timeout is the total budget each check gets, the whole time it may run
	// rather than the time it may stay quiet: a suite that keeps printing is
	// still spending it. Zero falls back to DefaultTimeout.
	Timeout time.Duration
	// StageTimeout is the total budget the whole list gets, from the first
	// check starting to the last one ending. A check is given the smaller of its
	// own budget and what the stage has left, and a check the stage has nothing
	// left for is recorded as stopped without being started. Zero falls back to
	// defaultStageTimeout.
	StageTimeout time.Duration
	RedactValues []string
	// HiddenExecutables are the provider executables no check may find on its
	// search path: every provider the project can run, by the executable each
	// one launches. searchpath.go says why.
	HiddenExecutables []string
	// Permits, where set, is asked for a place before each heavy check
	// launches (permits.go). Nil launches every check at once.
	Permits Permits
}

func (r Runner) Run(ctx context.Context, request Request, sink func(execution.Event) error) ([]Result, uint64, error) {
	if r.Process == nil {
		return nil, request.LastSequence, errors.New("check process runner is required")
	}
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.Directory) == "" {
		return nil, request.LastSequence, errors.New("run id and check directory are required")
	}
	clock := r.Clock
	if clock == nil {
		clock = execution.RealClock{}
	}
	shell := r.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	timeout := r.Timeout
	if request.Timeout > 0 {
		timeout = request.Timeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	stageTimeout := r.StageTimeout
	if stageTimeout == 0 {
		stageTimeout = DefaultStageTimeout
	}
	if request.Unbounded {
		stageTimeout = 0
	}
	// The checks are the project's own commands, so a toolchain that cannot
	// write its build cache fails them at setup with nothing about the change to
	// show for it. The environment is built from the allowlist every run's
	// process is built from -- nothing the harness's own environment happened to
	// carry reaches a check -- with the cache pointed inside the repository being
	// checked, the same redirect the run's own probe was given, so the two share
	// what has already been compiled. What the request adds comes after it, so a
	// check reads what the harness knows about the change from its environment.
	environment := execution.WithGoBuildCache(execution.ExplicitEnvironment(nil), request.Directory)
	environment = append(environment, request.Env...)
	withProviders := environment
	environment, restore, err := withoutExecutables(environment, request.Directory, r.HiddenExecutables)
	if err != nil {
		return nil, request.LastSequence, err
	}
	defer restore()
	sequence := execution.NewSequence(request.LastSequence)
	lastAccepted := request.LastSequence
	results := make([]Result, 0, len(request.Commands))
	redactor := execution.NewRedactor(r.RedactValues...)
	// The stage starts as its first check does, so the first check is given the
	// smaller of the two budgets whole rather than that less the instant it took
	// to get here. waited is the time the stage's checks have spent asking for
	// and waiting for places among the machine's heavy checks, which is never
	// counted against the stage: a check's time starts when it launches.
	var stageStarted time.Time
	var waited time.Duration
	for index, command := range request.Commands {
		if strings.TrimSpace(command) == "" {
			return results, lastAccepted, errors.New("check command cannot be empty")
		}
		safeCommand := redactor.Redact(command)
		now := clock.Now()
		if stageStarted.IsZero() {
			stageStarted = now
		}
		stageElapsed := now.Sub(stageStarted) - waited
		if request.Started != nil {
			request.Started(safeCommand, stageElapsed)
		}
		if err := emit(request.RunID, sequence, clock, sink, execution.EventCommandStarted, map[string]any{"command": safeCommand, "kind": "check"}); err != nil {
			return results, lastAccepted, err
		}
		lastAccepted = sequence.Last()
		// A heavy check waits here for its place, before anything about its
		// budget is worked out, so the wait is the stage's to subtract and the
		// load the stage's bound is scaled for is read as the check launches.
		permit, waitedNow, stopped, err := r.acquire(ctx, request, command, safeCommand, sequence, clock, sink, &lastAccepted)
		waited += waitedNow
		if err != nil {
			return results, lastAccepted, err
		}
		checkTimeout := timeout
		if !stopped {
			stageElapsed = clock.Now().Sub(stageStarted) - waited
			if request.StageBound != nil && !request.Unbounded {
				if bound := request.StageBound(); bound > 0 && (len(results) == 0 || bound > stageTimeout) {
					stageTimeout = bound
				}
			}
			if request.CheckBound != nil {
				if limit := request.CheckBound(safeCommand); limit > 0 {
					checkTimeout = limit
				}
			}
			// A caller that has ended by now — the caller's own Started or
			// CheckBound may be what ended it, for a run asked to stop between two
			// checks — gets no further check launched: the check is recorded as
			// stopped by its caller without having run, which judges nothing.
			if ctx.Err() != nil {
				finishPermit(permit, execution.ProcessResult{}, 0)
				stopped = true
			}
		}
		if stopped {
			now := clock.Now()
			result := Result{
				Command:         safeCommand,
				Process:         execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1, StartedAt: now, FinishedAt: now},
				Timeout:         checkTimeout,
				StageTimeout:    stageTimeout,
				StageElapsed:    now.Sub(stageStarted) - waited,
				StoppedByCaller: true,
				NotLaunched:     true,
			}
			results = append(results, result)
			if err := emitCompleted(request.RunID, sequence, clock, sink, result); err != nil {
				return results, lastAccepted, err
			}
			lastAccepted = sequence.Last()
			break
		}
		// What the stage has left is what this check may have, and a check the
		// stage has nothing left for is not started at all: a check given a
		// budget of nothing would be killed as it began and read as a check that
		// ran, which is the one thing a stopped stage must not record.
		remaining := stageTimeout - stageElapsed
		if stageTimeout > 0 && remaining <= 0 {
			finishPermit(permit, execution.ProcessResult{}, 0)
			now := clock.Now()
			result := Result{
				Command: safeCommand,
				Process: execution.ProcessResult{
					Status:     execution.ProcessTimedOut,
					ExitCode:   -1,
					StartedAt:  now,
					FinishedAt: now,
				},
				Timeout:        checkTimeout,
				StageTimeout:   stageTimeout,
				StageElapsed:   stageElapsed,
				StoppedByStage: true,
			}
			results = append(results, result)
			if err := emitCompleted(request.RunID, sequence, clock, sink, result); err != nil {
				return results, lastAccepted, err
			}
			lastAccepted = sequence.Last()
			break
		}
		budget := checkTimeout
		boundByStage := stageTimeout > 0 && remaining < checkTimeout
		if boundByStage {
			budget = remaining
		}
		var observerErrors []error
		var failure failureCapture
		checkEnvironment := environment
		if request.ProviderCLIs[index] {
			checkEnvironment = withProviders
		}
		var gate *execution.LaunchGate
		if request.Gate != nil {
			var gateErr error
			if gate, gateErr = request.Gate(safeCommand); gateErr != nil {
				finishPermit(permit, execution.ProcessResult{}, 0)
				return results, lastAccepted, fmt.Errorf("prepare the launch of check %q: %w", safeCommand, gateErr)
			}
		}
		gate = gateWithPermit(gate, permit)
		processResult, err := r.Process.Run(ctx, execution.Command{
			Name:    shell,
			Args:    []string{"-c", command},
			Dir:     request.Directory,
			Env:     checkEnvironment,
			Timeout: budget,
			// Every line this check writes is emitted below, so the run's own
			// event log holds the whole of a suite too verbose to retain, and
			// the marker in the cut copy is what says so.
			OutputRecord: execution.EventLogOf(request.RunID),
			Redactor:     redactor,
			Gate:         gate,
		}, func(output execution.Output) {
			failure.add(redactor.Redact(output.Text))
			if len(observerErrors) > 0 {
				return
			}
			if observerErr := emit(request.RunID, sequence, clock, sink, execution.EventProcessOutput, map[string]any{
				"kind":    "check",
				"command": safeCommand,
				"stream":  output.Stream,
				"text":    output.Text,
			}); observerErr != nil {
				observerErrors = append(observerErrors, observerErr)
				return
			}
			lastAccepted = sequence.Last()
		})
		// The place is given back before anything else is made of the result,
		// whatever the result is, once every process the check started is gone.
		if finishErr := finishPermit(permit, processResult, budget); finishErr != nil && err == nil {
			if emitErr := emit(request.RunID, sequence, clock, sink, execution.EventProcessOutput, map[string]any{
				"kind": "check", "command": safeCommand, "stream": execution.StreamStderr,
				"text": fmt.Sprintf("the end of %s could not be written to the record of heavy check places, so its place is given back by the next run that asks once its processes are gone: %v", safeCommand, finishErr),
			}); emitErr == nil {
				lastAccepted = sequence.Last()
			}
		}
		// The gate's copy of the hold is this runner's to let go of once the check
		// has ended, whatever runner it was handed to: the process runner closes it
		// as the check starts, and a runner that never started one may not have.
		// Closing an already closed file does nothing.
		if gate != nil && gate.Hold != nil {
			_ = gate.Hold.Close()
		}
		if err != nil {
			return results, lastAccepted, fmt.Errorf("run check %q: %w", safeCommand, err)
		}
		if len(observerErrors) > 0 {
			return results, lastAccepted, errors.Join(observerErrors...)
		}
		// A check too verbose to retain is not a failing check, so nothing here
		// stops. What the marker buys is that the retained output the failure
		// report is written from is never read as the whole of what the check
		// said: the line saying so is in the stream a follower is watching, and
		// it names the log that has the rest.
		if processResult.OutputTruncation != "" {
			if err := emit(request.RunID, sequence, clock, sink, execution.EventProcessOutput, map[string]any{
				"kind":    "check",
				"command": safeCommand,
				"stream":  execution.StreamStdout,
				"text":    processResult.OutputTruncation,
			}); err != nil {
				return results, lastAccepted, err
			}
			lastAccepted = sequence.Last()
		}
		passed := processResult.Status == execution.ProcessSucceeded
		// A check that said it could not run and exited non-zero on its own
		// judged nothing, so it is recorded as such and the list goes on to the
		// next check: what it would have found is unknown, and what the others
		// find is not. A check whose output also names a failing test or
		// package did run something that failed — a make target whose one step
		// could not run and another failed, or code under test that printed the
		// line itself — and a real failure is still a failure, so it is judged
		// as one. A check whose tool the machine refused to start
		// (machineRefusals) could not run either, and is judged the same way.
		var couldNotRun string
		if processResult.Status == execution.ProcessFailed && len(failure.names) == 0 {
			couldNotRun = failure.couldNotRun
			if couldNotRun == "" {
				couldNotRun = failure.machineRefused
			}
		}
		result := Result{
			Command:      safeCommand,
			Process:      processResult,
			Passed:       passed,
			Timeout:      checkTimeout,
			StageTimeout: stageTimeout,
			StageElapsed: clock.Now().Sub(stageStarted) - waited,
			// A check killed on time under a budget the stage cut short was
			// stopped by the stage, whatever its own budget would have allowed.
			StoppedByStage:  boundByStage && processResult.Status == execution.ProcessTimedOut && ctx.Err() == nil,
			StoppedByCaller: ctx.Err() != nil && (processResult.Status == execution.ProcessTimedOut || processResult.Status == execution.ProcessCancelled),
			CouldNotRun:     couldNotRun,
		}
		if !passed && failure.observed {
			result.FailureOutput = failure.render()
		}
		results = append(results, result)
		if request.Finished != nil {
			request.Finished(result)
		}
		if err := emitCompleted(request.RunID, sequence, clock, sink, result); err != nil {
			return results, lastAccepted, err
		}
		lastAccepted = sequence.Last()
		if !passed && couldNotRun == "" {
			break
		}
	}
	return results, lastAccepted, nil
}

// acquire asks for a heavy check's place, waiting for it, and says how long
// that took. A command that is not heavy, or a runner with no Permits, gets
// no permit and waits for nothing. stopped is a wait the caller's context cut
// short, which ends the stage the way a check stopped by its caller does.
func (r Runner) acquire(ctx context.Context, request Request, command, safeCommand string, sequence *execution.Sequence, clock execution.Clock, sink func(execution.Event) error, lastAccepted *uint64) (Permit, time.Duration, bool, error) {
	if r.Permits == nil {
		return nil, 0, false, nil
	}
	began := clock.Now()
	var observerErr error
	permit, err := r.Permits.Acquire(ctx, PermitAsk{
		RunID:      request.RunID,
		WorkItemID: request.Permit.WorkItemID,
		Commit:     request.Permit.Commit,
		Command:    command,
		Priority:   request.Permit.Priority,
	}, func(check string, since time.Time) {
		if request.Permit.Waiting != nil {
			request.Permit.Waiting(check, since)
		}
		if observerErr = emit(request.RunID, sequence, clock, sink, execution.EventProcessOutput, map[string]any{
			"kind": "check", "command": safeCommand, "stream": execution.StreamStdout,
			"text": fmt.Sprintf("waiting for a free place to run %s: only so many may run at once on this machine (execution.heavy_checks), and its time limit starts when it launches", check),
		}); observerErr == nil {
			*lastAccepted = sequence.Last()
		}
	})
	took := clock.Now().Sub(began)
	if took < 0 {
		took = 0
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, took, true, nil
		}
		return nil, took, false, fmt.Errorf("ask for a place to run check %q: %w", safeCommand, err)
	}
	if observerErr != nil {
		finishPermit(permit, execution.ProcessResult{}, 0)
		return nil, took, false, observerErr
	}
	if permit != nil && request.Permit.Granted != nil {
		request.Permit.Granted(permit.Check(), permit.Waited())
	}
	return permit, took, false, nil
}

// finishPermit gives a heavy check's place back, where it had one.
func finishPermit(permit Permit, result execution.ProcessResult, budget time.Duration) error {
	if permit == nil {
		return nil
	}
	return permit.Finish(result, budget)
}

// emitCompleted records what a check spent against what it was allowed, not
// only for the one that ran out: a suite walking toward its ceiling is visible
// in the event stream long before it reaches it, and so is a stage walking
// toward its own.
func emitCompleted(runID string, sequence *execution.Sequence, clock execution.Clock, sink func(execution.Event) error, result Result) error {
	return emit(runID, sequence, clock, sink, execution.EventCommandCompleted, map[string]any{
		"command":           result.Command,
		"kind":              "check",
		"passed":            result.Passed,
		"status":            result.Process.Status,
		"exit_code":         result.Process.ExitCode,
		"elapsed":           result.Elapsed().String(),
		"timeout":           result.Timeout.String(),
		"stage_elapsed":     result.StageElapsed.String(),
		"stage_timeout":     result.StageTimeout.String(),
		"stopped_by_stage":  result.StoppedByStage,
		"stopped_by_caller": result.StoppedByCaller,
		"could_not_run":     result.CouldNotRun,
	})
}

func emit(runID string, sequence *execution.Sequence, clock execution.Clock, sink func(execution.Event) error, eventType execution.EventType, payload any) error {
	event, err := execution.NewEvent(runID, sequence.Next(), clock.Now(), eventType, "harness.checks", payload)
	if err != nil {
		return err
	}
	if sink != nil {
		if err := sink(event); err != nil {
			return fmt.Errorf("persist check event: %w", err)
		}
	}
	return nil
}

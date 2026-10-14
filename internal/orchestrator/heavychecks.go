package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// HeavyCheckPermits is the check runner's way to the record of places for the
// machine's heavy checks (runstate's heavycheck.go): a command the project's
// execution.heavy_checks names asks the record for a place and waits for it,
// and any other command is launched at once. It is what the runner is wired
// with only where the project names a heavy check, so a project that names
// none never opens the record.
type HeavyCheckPermits struct {
	Store     *runstate.HeavyCheckStore
	Product   domain.ProductID
	Execution config.Execution
	// Machine, where set, reads the machine as each heavy check launches, for
	// the measurements its limit is set from (runstate's heavycheckmeasure.go).
	Machine func() runstate.HeavyCheckReading
}

// Acquire asks for a place for a heavy command, waiting for it.
func (h HeavyCheckPermits) Acquire(ctx context.Context, ask checks.PermitAsk, waiting func(check string, since time.Time)) (checks.Permit, error) {
	name, limit, heavy := h.Execution.HeavyCheck(ask.Command)
	if !heavy || h.Store == nil {
		return nil, nil
	}
	priority := ask.Priority
	if priority == "" {
		priority = domain.HeavyCheckOrdinary
	}
	grant, err := h.Store.Acquire(ctx, runstate.HeavyCheckAsk{
		Product: string(h.Product), RunID: ask.RunID, WorkItemID: ask.WorkItemID, Commit: ask.Commit,
		Check: name, Command: ask.Command, Limit: limit, Priority: priority, Read: h.Machine,
	}, func(permit runstate.HeavyCheckPermit) {
		if waiting != nil {
			waiting(permit.Check, permit.AskedAt)
		}
	})
	if err != nil {
		return nil, err
	}
	return heavyCheckPermit{grant: grant}, nil
}

// UnopenedHeavyChecks is what a project that declared heavy checks is wired
// with where the record of places could not be opened: every heavy check is
// refused with the reason, and every other check is launched as usual. A
// declared limit is never dropped without a word.
type UnopenedHeavyChecks struct {
	Execution config.Execution
	Err       error
}

// Acquire refuses a heavy command, naming why, and gives every other command
// no permit.
func (u UnopenedHeavyChecks) Acquire(_ context.Context, ask checks.PermitAsk, _ func(string, time.Time)) (checks.Permit, error) {
	name, _, heavy := u.Execution.HeavyCheck(ask.Command)
	if !heavy {
		return nil, nil
	}
	return nil, fmt.Errorf("the record of places for the machine's heavy checks could not be opened, so %s is not started without its execution.heavy_checks limit: %w", name, u.Err)
}

// heavyCheckPermit is one granted place, as the runner holds it.
type heavyCheckPermit struct {
	grant *runstate.HeavyCheckGrant
}

func (p heavyCheckPermit) Check() string         { return p.grant.Permit.Check }
func (p heavyCheckPermit) Waited() time.Duration { return p.grant.Waited }
func (p heavyCheckPermit) Hold() *os.File        { return p.grant.Hold() }
func (p heavyCheckPermit) Register(process execution.StartedProcess) error {
	return p.grant.Register(process)
}

// Finish records how the check ended, in the words a process result is read
// in, and gives its place back once its processes are gone.
func (p heavyCheckPermit) Finish(result execution.ProcessResult, budget time.Duration) error {
	ended := "not started"
	if result.Status != "" {
		ended = fmt.Sprintf("%s, exit code %d", result.Status, result.ExitCode)
	}
	p.grant.Measure(heavyCheckOutcome(result.Status), budget)
	_, err := p.grant.Finish(ended)
	return err
}

// heavyCheckOutcome is a process's end as a heavy check's measurement records
// it.
func heavyCheckOutcome(status execution.ProcessStatus) runstate.HeavyCheckOutcome {
	switch status {
	case execution.ProcessSucceeded:
		return runstate.HeavyCheckPassed
	case execution.ProcessFailed:
		return runstate.HeavyCheckFailed
	case execution.ProcessTimedOut:
		return runstate.HeavyCheckTimedOut
	case execution.ProcessStalled:
		return runstate.HeavyCheckStalled
	case execution.ProcessCancelled:
		return runstate.HeavyCheckStopped
	}
	return ""
}

// machineListingTimeout bounds the one process listing a heavy check's launch
// reads; a listing that takes longer is left out of the measurement.
const machineListingTimeout = 5 * time.Second

// HeavyCheckMachine reads the machine as a heavy check launches: the load, the
// free memory, and every process whose command is one of the project's heavy
// checks, from the machine's process listing taken through runner. Those
// processes are what tells a suite run in its place from one a developer runs
// inside its own session, which asks for none: the record of places tells them
// apart by process group. A suite started under a name execution.heavy_checks
// does not give — a make target that runs the race suite among others — is not
// in the listing's count, and shows only in the load.
func HeavyCheckMachine(runner execution.ProcessRunner, heavy config.Execution) func() runstate.HeavyCheckReading {
	return func() runstate.HeavyCheckReading {
		var reading runstate.HeavyCheckReading
		if load, cores, ok := gitworktree.MachineLoad(); ok {
			reading.Load, reading.Cores = &load, cores
		} else {
			reading.Cores = runtime.NumCPU()
		}
		if free, ok := gitworktree.MachineFreeMemory(); ok {
			reading.FreeMemory = &free
		}
		if runner == nil {
			return reading
		}
		ctx, cancel := context.WithTimeout(context.Background(), machineListingTimeout)
		defer cancel()
		var seen []runstate.HeavyCheckProcessSeen
		result, err := runner.Run(ctx, execution.Command{
			Name: "ps", Args: []string{"-axo", "pid=,pgid=,command="}, Timeout: machineListingTimeout,
		}, func(output execution.Output) {
			if output.Stream != execution.StreamStdout {
				return
			}
			if process, ok := heavyCheckProcess(output.Text, heavy); ok {
				seen = append(seen, process)
			}
		})
		if err == nil && result.Status == execution.ProcessSucceeded {
			reading.Listed, reading.Seen = true, seen
		}
		return reading
	}
}

// heavyCheckProcess reads one line of the process listing — its process, its
// process group, and its command line — as a process running a heavy check,
// where its command is one. The program is read by its own name, so a make
// the shell found at a full path is the same make.
func heavyCheckProcess(line string, heavy config.Execution) (runstate.HeavyCheckProcessSeen, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return runstate.HeavyCheckProcessSeen{}, false
	}
	pid, pidErr := strconv.Atoi(fields[0])
	group, groupErr := strconv.Atoi(fields[1])
	if pidErr != nil || groupErr != nil {
		return runstate.HeavyCheckProcessSeen{}, false
	}
	command := append([]string{filepath.Base(fields[2])}, fields[3:]...)
	name, _, ok := heavy.HeavyCheck(strings.Join(command, " "))
	if !ok {
		return runstate.HeavyCheckProcessSeen{}, false
	}
	return runstate.HeavyCheckProcessSeen{PID: pid, ProcessGroup: group, Check: name}, true
}

// heavyCheckPriority is the order this run's gate checks wait for places in: a
// replay onto a target branch that moved is checked for a promotion and goes
// first; a run continuing work an earlier attempt preserved — a decided
// repair, or a re-run holding that work — goes next; anything else goes last.
func heavyCheckPriority(state runstate.State) domain.HeavyCheckPriority {
	switch {
	case state.IntegrationRetries > 0:
		return domain.HeavyCheckForLanding
	case state.LiftedCommit != "" || len(state.RepairContinuations) > 0:
		return domain.HeavyCheckForPreservedWork
	}
	return domain.HeavyCheckOrdinary
}

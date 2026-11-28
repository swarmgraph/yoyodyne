package maintain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The leftovers step: what runs started and left running, stopped where
// nothing of the run is going any more (docs/designs/stopping-what-a-run-started.md,
// decision 3 point 3, and decision 5).
//
// A run's processes are stopped as each of its sessions ends and as the run
// ends (internal/orchestrator, runprocesses.go). A harness that stopped or
// crashed between a session's end and that stop leaves them running with
// nothing left to stop them, so the scheduler's start and every maintenance
// pass look again: they find every process that started with a run's
// identifier, for any run, and stop the processes of each run whose lease
// nobody holds. Every session and check of a run runs under its lease, so a
// lease nobody holds is a run with none of them going, and the step holds the
// lease while it stops, so none can start under it. An identifier that names no
// run of this product is left alone: it is another product's run, or a merge
// queue candidate's checks, and only its own owner can say it is over.
//
// The same look tries again for every process a run's record names as still
// running, and the line goes from the record, and so from yoyo status, once
// the process is gone. A process still running after three passes is reported
// to the development manager once, naming the run, the process, its command
// and its ports. One named only because it works inside the run's folders is
// shown and never reported, because only a person can say whether it is
// theirs.

// StepLeftovers is the step that stops what ended runs left running.
const StepLeftovers = "leftovers"

// leftoverPassesBeforeReport is how many passes find a process still running
// before it is reported.
const leftoverPassesBeforeReport = 3

// maxLeftoverProcesses bounds what one stop writes onto a run's record, as the
// stop at a session's end does.
const maxLeftoverProcesses = 20

// LeftoverRuns is the run records the step reads and writes. It is satisfied by
// *runstate.Store.
type LeftoverRuns interface {
	Read(runID string) (runstate.State, error)
	LeftRunning() ([]runstate.State, error)
	LaunchHolds(runID string) ([]string, error)
	TryIdleRun(runID string) (*runstate.Lease, bool, error)
	Update(ctx context.Context, runID string, update runstate.RunUpdate) (runstate.State, error)
}

// LeftoverProcesses finds and stops a run's processes. It is satisfied by
// execution.RunProcesses.
type LeftoverProcesses interface {
	MarkedRuns() ([]string, error)
	Stop(ctx context.Context, request execution.RunProcessesRequest) []execution.LeftProcess
}

// Leftovers stops what runs with nothing going left running. The maintenance
// pass takes it on every pass, and a scheduler once as it starts.
type Leftovers struct {
	Runs      LeftoverRuns
	Processes LeftoverProcesses
	// StateRoot is where each run's scratch folder is, which a process left
	// working inside is named from.
	StateRoot string
	// Reports is where a process still running after three passes is reported,
	// and Product, RepositoryID and Build what the report is attributed to.
	Reports      Reports
	Product      domain.ProductID
	RepositoryID string
	Build        string
	Now          func() time.Time
}

// Stop finds and stops what runs with nothing going left running, and says
// what it did as a step of the pass that took it.
func (l *Leftovers) Stop(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepLeftovers}
	if l == nil || l.Runs == nil || l.Processes == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no run store or process search is wired, so what ended runs left running is not looked for"
		return step
	}
	var problems []string
	ids := map[string]bool{}
	marked, err := l.Processes.MarkedRuns()
	if err != nil {
		problems = append(problems, err.Error())
	}
	for _, id := range marked {
		ids[id] = true
	}
	recorded, err := l.Runs.LeftRunning()
	if err != nil {
		problems = append(problems, fmt.Sprintf("the runs recorded as having left a process running could not be listed: %v", err))
	}
	for _, state := range recorded {
		ids[state.RunID] = true
	}
	order := make([]string, 0, len(ids))
	for id := range ids {
		order = append(order, id)
	}
	sort.Strings(order)

	var stopped, busy, left, reported []string
	for _, id := range order {
		found, err := l.stopRun(ctx, id)
		if err != nil {
			problems = append(problems, err.Error())
		}
		switch {
		case found.notOurs:
		case found.busy:
			busy = append(busy, id)
		case len(found.left) == 0:
			stopped = append(stopped, id)
		default:
			for _, process := range found.left {
				left = append(left, fmt.Sprintf("run %s: %s", id, process.Describe()))
			}
		}
		reported = append(reported, found.reported...)
	}

	var said []string
	if len(stopped) > 0 {
		said = append(said, fmt.Sprintf("nothing is left running for %s, whose sessions and checks have all ended", strings.Join(stopped, ", ")))
	}
	if len(left) > 0 {
		said = append(said, "STILL RUNNING: "+strings.Join(left, "; "))
	}
	if len(reported) > 0 {
		said = append(said, "reported to the development manager after three passes: "+strings.Join(reported, ", "))
	}
	if len(busy) > 0 {
		said = append(said, fmt.Sprintf("left alone the processes of %s, which still have a session or check going", strings.Join(busy, ", ")))
	}
	if len(problems) > 0 {
		said = append(said, strings.Join(problems, "; "))
	}
	switch {
	case len(problems) > 0:
		step.Outcome = runstate.StepFailed
	case len(said) == 0:
		step.Outcome, step.Detail = runstate.StepSkipped, "no process of a run of this product is running outside a session or check"
		return step
	default:
		step.Outcome = runstate.StepRan
	}
	step.Detail = strings.Join(said, "; ")
	return step
}

// leftoverRun is what the step came to for one run.
type leftoverRun struct {
	// notOurs is an identifier no run of this product has.
	notOurs bool
	// busy is a run something holds the lease of.
	busy     bool
	left     []execution.LeftProcess
	reported []string
}

// stopRun stops one run's processes where nothing holds the run, and records
// on the run what is still running.
func (l *Leftovers) stopRun(ctx context.Context, runID string) (leftoverRun, error) {
	if !runstate.ValidRunID(runID) {
		return leftoverRun{notOurs: true}, nil
	}
	state, err := l.Runs.Read(runID)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return leftoverRun{notOurs: true}, nil
	case err != nil:
		return leftoverRun{busy: true}, fmt.Errorf("run %s's record could not be read, so its processes were left alone: %v", runID, err)
	}
	lease, free, err := l.Runs.TryIdleRun(runID)
	if err != nil {
		return leftoverRun{busy: true}, fmt.Errorf("whether anything holds run %s could not be read, so its processes were left alone: %v", runID, err)
	}
	if !free {
		return leftoverRun{busy: true}, nil
	}
	defer lease.Release()

	var folders []string
	if state.WorktreePath != "" {
		folders = append(folders, state.WorktreePath)
	}
	if scratch, err := execution.ScratchDirectoryPath(l.StateRoot, runID); err == nil && l.StateRoot != "" {
		folders = append(folders, scratch)
	}
	holds, _ := l.Runs.LaunchHolds(runID)
	left := l.Processes.Stop(context.WithoutCancel(ctx), execution.RunProcessesRequest{RunID: runID, Holds: holds, Folders: folders})
	if len(left) > maxLeftoverProcesses {
		left = left[:maxLeftoverProcesses]
	}
	now := l.now()
	var found leftoverRun
	var problems []string
	for index := range left {
		process := &left[index]
		process.Passes = 1
		for _, before := range state.LeftProcesses {
			if runstate.SameLeftProcess(before, *process) {
				process.Passes, process.Reported = before.Passes+1, before.Reported
				break
			}
		}
		if process.Unmarked || process.PID <= 0 || process.Passes < leftoverPassesBeforeReport || process.Reported != "" {
			continue
		}
		id, err := l.report(state, *process, now)
		if err != nil {
			problems = append(problems, fmt.Sprintf("process %d of run %s has been running through three passes, and the report to the development manager could not be filed, so the next pass tries again: %v", process.PID, runID, err))
			continue
		}
		process.Reported = id
		found.reported = append(found.reported, fmt.Sprintf("process %d of run %s (%s)", process.PID, runID, id))
	}
	found.left = left
	// A report filed and then not recorded is filed again by the next pass;
	// the record is written straight after the report so that is rare.
	if _, err := l.Runs.Update(context.WithoutCancel(ctx), runID, runstate.LeftProcessesFound{Left: left, At: now}); err != nil {
		problems = append(problems, fmt.Sprintf("what is still running for run %s could not be recorded on it: %v", runID, err))
	}
	if len(problems) > 0 {
		return found, errors.New(strings.Join(problems, "; "))
	}
	return found, nil
}

// report files one report to the development manager about a process still
// running after three passes, and returns its id.
func (l *Leftovers) report(state runstate.State, process execution.LeftProcess, now time.Time) (string, error) {
	if l.Reports == nil {
		return "", errors.New("no report pile is wired")
	}
	listening := "it listens on no port the harness could read"
	if len(process.Listening) > 0 {
		listening = "it listens on " + strings.Join(process.Listening, ", ")
	}
	command := process.Command
	if command == "" {
		command = "a command the harness could not read"
	}
	message := fmt.Sprintf("Process %d (%s), which run %s for %s started, is still running after %d maintenance passes tried to stop it, and %s; %s. "+
		"A process left holding a port can make a later run's checks fail for a reason that has nothing to do with that run's change; stopping it takes `kill -9 %d` from a shell outside any sandbox, and the next pass then clears it from the run and from yoyo status.",
		process.PID, command, state.RunID, state.WorkItemID, process.Passes, listening, process.Why, process.PID)
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleDevelopmentManager,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        state.RunID,
		WorkItemID:   state.WorkItemID,
		Build:        l.Build,
		ProductID:    l.Product,
		RepositoryID: l.RepositoryID,
	}, now)
	if err != nil {
		return "", err
	}
	if err := l.Reports.Append(collected[0]); err != nil {
		return "", err
	}
	return collected[0].ID, nil
}

func (l *Leftovers) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// stopLeftovers is the pass's step: the leftovers stop where one is wired.
func (p *Pass) stopLeftovers(ctx context.Context) runstate.SweepStep {
	if p.Leftovers == nil {
		return runstate.SweepStep{Name: StepLeftovers, Outcome: runstate.StepSkipped, Detail: "no stop of what ended runs left running is wired, so it happens only as each session and run ends"}
	}
	return p.Leftovers.Stop(ctx)
}

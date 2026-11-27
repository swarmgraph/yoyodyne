package orchestrator

// Every process a run started is stopped when its developer or reviewer session
// ends and when the run ends, however it ends, including a process that left the
// session's process group (docs/designs/stopping-what-a-run-started.md). The
// search and the stop are internal/execution's (execution.RunProcesses); this
// file makes the calls and records on the run what was left running, which
// yoyo status shows under the run.

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RunProcessStopper stops every process that started with a run's identifier
// and returns what it left running. It is satisfied by execution.RunProcesses;
// a nil one stops nothing.
type RunProcessStopper interface {
	Stop(ctx context.Context, request execution.RunProcessesRequest) []execution.LeftProcess
}

// stopWhatItStarted stops what the run started and records what is still
// running. It is called only when no session or check of the run is going: once
// a developer or reviewer session has ended, before the run's next step, and as
// the run ends, before the run is let go. A record whose save fails costs the
// surfaces the line and nothing else: what was stopped is stopped either way.
func (a *activeRun) stopWhatItStarted(ctx context.Context) {
	stopper := a.pipeline.RunProcesses
	if stopper == nil || a.state.RunID == "" {
		return
	}
	var folders []string
	if a.state.WorktreePath != "" {
		folders = append(folders, a.state.WorktreePath)
	}
	if a.scratch != "" {
		folders = append(folders, a.scratch)
	}
	left := stopper.Stop(context.WithoutCancel(ctx), execution.RunProcessesRequest{RunID: a.state.RunID, Folders: folders})
	if len(left) > maxLeftProcesses {
		left = left[:maxLeftProcesses]
	}
	if len(left) == 0 && len(a.state.LeftProcesses) == 0 {
		return
	}
	held := a.state.Revision
	written, err := a.pipeline.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.LeftProcessesFound{Left: left, At: a.pipeline.clock().Now()})
	switch {
	case err != nil:
	case written.Revision == held+1:
		a.state.LeftProcesses, a.state.Revision, a.state.UpdatedAt = written.LeftProcesses, written.Revision, written.UpdatedAt
	default:
		a.state = written
	}
	for _, process := range left {
		a.recordBackgroundWork(process.Describe())
	}
}

// maxLeftProcesses bounds what one stop writes onto the run's record: past this
// many, the record names the first of them and the run's machine is the place
// to look.
const maxLeftProcesses = 20

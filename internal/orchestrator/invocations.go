package orchestrator

import (
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// invocationRecorder is where the developer invocation about to be made keeps
// what it is sent: the run's own recorder, at the commit its worktree is at. It
// is nil for a pipeline wired with no record of invocations, which sends
// exactly what it always did and keeps nothing.
func (a *activeRun) invocationRecorder() backend.InvocationRecorder {
	if a.pipeline.Invocations == nil {
		return nil
	}
	commit := a.worktree.HarnessCommit
	if commit == "" {
		commit = a.worktree.BaseCommit
	}
	return a.pipeline.Invocations.Recorder(a.state.RunID, domain.RoleDeveloper, commit, a.pipeline.clock().Now)
}

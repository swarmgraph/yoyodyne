package testharness

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// orphan records the run a relaunch left behind: an earlier attempt at the same
// item that published a branch and opened a pull request, and then died without
// integrating anything.
func (f QueuedFixture) Orphan(t *testing.T, landed orchestrator.Outcome, number int) runstate.State {
	t.Helper()
	return f.OrphanStarted(t, landed, number, time.Now().UTC().Add(-2*time.Hour))
}

// orphanStarted is the same run with its start stated, because when the dead run
// began relative to the landing is part of what says its publication is
// superseded rather than pending.
func (f QueuedFixture) OrphanStarted(t *testing.T, landed orchestrator.Outcome, number int, started time.Time) runstate.State {
	t.Helper()
	return f.DeadRun(t, landed, f.Tracker.Record().Item.ID, "run-11112222333344445555666677778888", number, started)
}

// orphanOf is a dead run of some other item, which is the shape of a request no
// landing of this harness supersedes.
func (f QueuedFixture) OrphanOf(t *testing.T, landed orchestrator.Outcome, workItemID, runID string, number int) runstate.State {
	t.Helper()
	return f.DeadRun(t, landed, workItemID, runID, number, time.Now().UTC().Add(-2*time.Hour))
}

// deadRun records a run that published and then died before anything was
// judged, leaving the pair the operator found: the branch on the remote exactly
// as Publishing left it, and the request the forge holds for it.
func (f QueuedFixture) DeadRun(t *testing.T, landed orchestrator.Outcome, workItemID, runID string, number int, started time.Time) runstate.State {
	t.Helper()
	short := runID[len("run-"):][:8]
	branch := fmt.Sprintf("yoyodyne/%s/%s", workItemID, short)
	// The branch sits on the remote exactly as Publishing left it, which is what
	// the deletion is a compare-and-swap against.
	RunPipelineGit(t, f.Repository, "push", "origin", landed.BaseCommit+":refs/heads/"+branch)
	head := PublishedCommit(t, f.Remote, branch)
	// Locally the run left the pair the operator found: the branch, and a worktree
	// still checked out on it. Neither is this sweep's to remove, and the checkout
	// is what proves the remote deletion does not need it released first.
	root, err := filepath.EvalSymlinks(f.WorktreeRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	worktreePath := filepath.Join(root, workItemID+"-"+short)
	RunPipelineGit(t, f.Repository, "branch", branch, landed.BaseCommit)
	RunPipelineGit(t, f.Repository, "worktree", "add", worktreePath, branch)
	completed := started.Add(time.Minute)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		WorkItemTitle: "Task",
		Backend:       "claude-code",
		Status:        runstate.StatusFailed,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     completed,
		CompletedAt:   &completed,
		WorktreePath:  worktreePath,
		Branch:        branch,
		BaseCommit:    landed.BaseCommit,
		TargetBranch:  "main",
		Failure:       "the process was killed before the change was judged",
		PullRequest: &runstate.PullRequest{
			Remote:     "origin",
			Branch:     branch,
			Number:     number,
			URL:        fmt.Sprintf("https://example.invalid/pull/%d", number),
			HeadCommit: head,
			State:      "OPEN",
		},
	}
	if err := f.Store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	f.Forge.Hold(branch, publish.PullRequest{Number: number, URL: fmt.Sprintf("https://example.invalid/pull/%d", number), State: "OPEN"})
	return state
}

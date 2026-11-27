package orchestrator

// An approved run whose change has no file differences from its base ends
// complete rather than failing on a commit it has nothing to put in
// (approvedunchanged.go): approved as the work, its item closes; approved as
// evidence, its item stays open where the landing put it; and a run that
// changed nothing and was not approved is not touched by any of this.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// unchangedRun is a run whose developer writes nothing, judged by verdict,
// with the merge queue wired in and switched on where queued is set.
func unchangedRun(t *testing.T, queued bool, finalText string, verdicts ...string) (*queueRun, *orchestratortest.Backend) {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Investigate the stall", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, verdicts...)
	provider.DeveloperFinalText = finalText
	pipeline, runs := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue
	pipeline.Config.Execution.MergeQueue = queued
	return &queueRun{repository: repository, tracker: tracker, pipeline: pipeline, runs: runs, queue: queue}, provider
}

func TestAnApprovedRunWithNoFileChangesEndsCompleteWithNoQueueEntry(t *testing.T) {
	t.Parallel()

	for _, queued := range []bool{true, false} {
		name := "merge queue off"
		if queued {
			name = "merge queue on"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, _ := unchangedRun(t, queued, "The investigation found the cause; nothing needed changing.", approveVerdict)
			base := gitLine(t, r.repository, "rev-parse", "main")
			outcome := r.run(t)
			if outcome.Status != runstate.StatusSucceeded || outcome.Phase != runstate.PhaseComplete || !outcome.ApprovedWithNoChanges {
				t.Fatalf("Run() = status %s, phase %s, no changes %t, stop %q; want the run ended complete with nothing to land",
					outcome.Status, outcome.Phase, outcome.ApprovedWithNoChanges, outcome.StopClass)
			}
			if outcome.MergeQueue != nil || outcome.Integration != nil {
				t.Fatalf("queue %#v, integration %#v; want nothing admitted and nothing promoted", outcome.MergeQueue, outcome.Integration)
			}
			if keys, err := r.queue.Keys(); err != nil || len(keys) != 0 {
				t.Fatalf("queues = %v, %v; want no merge queue entry", keys, err)
			}
			if got := gitLine(t, r.repository, "rev-parse", "main"); got != base {
				t.Fatalf("main moved to %s from %s for a change with nothing in it", got, base)
			}
			if !r.tracker.Closed || r.tracker.Blocked || r.tracker.Reopened || !outcome.WorkItemClosed {
				t.Fatalf("tracker closed %t, blocked %t (%q), reopened %t; want the item closed as finished and nothing held for the development manager",
					r.tracker.Closed, r.tracker.Blocked, r.tracker.BlockReason, r.tracker.Reopened)
			}
			if !strings.Contains(r.tracker.CloseReason, "no file changes") {
				t.Fatalf("close reason = %q, want it to say the approved change had no file changes", r.tracker.CloseReason)
			}
			if !strings.Contains(r.tracker.Notes, "changed no files") || !strings.Contains(r.tracker.Notes, "closed as finished") {
				t.Fatalf("notes = %q, want them to say the run changed no files and how the item was closed", r.tracker.Notes)
			}
			state, err := r.runs.Load(outcome.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status != runstate.StatusSucceeded || state.Phase != runstate.PhaseComplete || !state.ApprovedWithNoChanges ||
				state.StopClass != "" || state.MergeQueue != nil || state.HoldsDeveloperSlot() {
				t.Fatalf("run = status %s, phase %s, no changes %t, stop %q; want a completed run holding nothing", state.Status, state.Phase, state.ApprovedWithNoChanges, state.StopClass)
			}
			// Nothing on the branch is unlanded, but the cleanup step removes only
			// what it can prove reached the target, so both are kept and owe nothing.
			if state.WorktreeRemoved || state.BranchRemoved || state.Outstanding() {
				t.Fatalf("worktree removed %t, branch removed %t, outstanding %t; want both kept with nothing owed", state.WorktreeRemoved, state.BranchRemoved, state.Outstanding())
			}
		})
	}
}

// Approved only as evidence, the run changed nothing and names the work it
// waits on: nothing is queued, and the item stays open waiting on that work,
// with no decision owed by anybody.
func TestAnEvidenceApprovalWithNoFileChangesLeavesTheItemWaitingWithNoQueueEntry(t *testing.T) {
	t.Parallel()

	claim := "The trial this corrects is not on main.\n\n```yoyodyne-landing\n{\"outcome\":\"evidence\",\"why\":\"the trial has to land first\",\"blocked_by\":\"yoyodyne-trial\"}\n```\n"
	r, _ := unchangedRun(t, true, claim, approveEvidenceVerdict)
	r.tracker.Holds("yoyodyne-trial")
	outcome := r.run(t)
	if outcome.Status != runstate.StatusSucceeded || outcome.Phase != runstate.PhaseComplete || !outcome.ApprovedWithNoChanges {
		t.Fatalf("Run() = status %s, phase %s, stop %q; want the run ended complete", outcome.Status, outcome.Phase, outcome.StopClass)
	}
	if keys, err := r.queue.Keys(); err != nil || len(keys) != 0 {
		t.Fatalf("queues = %v, %v; want no merge queue entry", keys, err)
	}
	if r.tracker.Closed || r.tracker.Blocked || !r.tracker.Reopened {
		t.Fatalf("closed %t, blocked %t, reopened %t; want the item put back open, not closed and not held", r.tracker.Closed, r.tracker.Blocked, r.tracker.Reopened)
	}
	if len(r.tracker.Blockers) != 1 || r.tracker.Blockers[0] != "yoyodyne-trial" {
		t.Fatalf("blockers = %v, want the item waiting on the work the landing named", r.tracker.Blockers)
	}
	if !strings.Contains(r.tracker.ReopenReason, "no file changes") || !strings.Contains(r.tracker.ReopenReason, "nothing was put in the merge queue or integrated") {
		t.Fatalf("reopen reason = %q, want it to say nothing was integrated because no files changed", r.tracker.ReopenReason)
	}
	if !strings.Contains(r.tracker.Notes, "waiting on yoyodyne-trial") {
		t.Fatalf("notes = %q, want them to say the item waits on the named work", r.tracker.Notes)
	}
}

// A run that changed nothing and whose review did not approve is handed back
// to its developer as it always was, and is not ended complete.
func TestAnUnapprovedRunWithNoFileChangesIsNotEndedComplete(t *testing.T) {
	t.Parallel()

	r, provider := unchangedRun(t, true, "Nothing changed.", repairVerdict, repairVerdict, repairVerdict, repairVerdict, repairVerdict, repairVerdict)
	outcome, _ := r.pipeline.Run(context.Background(), "yoyodyne-task")
	if outcome.ApprovedWithNoChanges || outcome.Status == runstate.StatusSucceeded || r.tracker.Closed {
		t.Fatalf("Run() = status %s, no changes %t, closed %t; want an unapproved run left as review leaves it", outcome.Status, outcome.ApprovedWithNoChanges, r.tracker.Closed)
	}
	developed := 0
	for _, request := range provider.Requests {
		if request.Role == "developer" {
			developed++
		}
	}
	if developed < 2 {
		t.Fatalf("developer invoked %d times, want the repair verdict handed back to it", developed)
	}
}

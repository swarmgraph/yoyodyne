package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// closingForge closes every request it is asked to and says it did not merge.
type closingForge struct{ closed []int }

func (f *closingForge) Close(_ context.Context, request publish.CloseRequest) (publish.Closure, error) {
	f.closed = append(f.closed, request.Number)
	return publish.Closure{State: "CLOSED"}, nil
}

// keptBranches deletes nothing and says it did.
type keptBranches struct{}

func (keptBranches) DeleteRemoteBranch(context.Context, gitworktree.Worktree, string) error {
	return nil
}

// TestNoWriterOfARunLosesAnotherWritersField drives every writer the merge
// queue path has on one run, in one fixed order that interleaves them the way
// the outside review traced: the admission writing its copy, the queue
// recording the landing and retiring the pull request while the admission
// waits on the tracker, the admission's stale copy trying to save over them,
// the admission ending, a transfer, and reconciliation recording its cleanup
// in two steps. Each writer's field is on the record at the end, and the one
// save made from an older copy is refused and names what it would have lost.
func TestNoWriterOfARunLosesAnotherWritersField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	run := recordedRun(t, "yoyodyne-task", runstate.StatusRunning, at)
	run.Phase = runstate.PhaseIntegrating
	run.WorktreePath, run.Branch, run.BaseCommit, run.TargetBranch = t.TempDir(), "yoyodyne/yoyodyne-task/run", strings.Repeat("d", 40), "main"
	run.ReviewDecision = runstate.ReviewApprove
	run.ProviderSessionID, run.ReviewSessionID = "developer-session", "reviewer-session"
	run.ProviderModel, run.ReviewModel = "opus", "gpt-6.1-sol"
	run.PullRequest = &runstate.PullRequest{Remote: "origin", Branch: run.Branch, Number: 41, URL: "https://example.test/pull/41", HeadCommit: strings.Repeat("a", 40), State: "OPEN"}
	if err := store.Create(run); err != nil {
		t.Fatal(err)
	}

	// The admission, which holds the run, records the entry with a whole save.
	admission, err := store.Load(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	entry := runstate.MergeQueueEntry{
		EntryID: "mqe-" + strings.Repeat("1", 32), Order: 1, Repository: "yoyodyne", TargetBranch: "main",
		Mode: runstate.MergeQueueHarness, RunID: run.RunID, WorkItemID: "yoyodyne-task", ApprovedHead: strings.Repeat("a", 40), AdmittedAt: at,
	}
	admitted := entry.AdmissionRecord()
	admission.MergeQueue = &admitted
	if err := store.Save(admission); err != nil {
		t.Fatalf("the admission's save: %v", err)
	}

	// While the admission waits on the tracker, the queue lands the change and
	// retires its pull request.
	landed := runstate.MergeQueueCompletion{Path: runstate.MergeQueueLandThroughPullRequest, TargetBase: strings.Repeat("b", 40), Landed: strings.Repeat("c", 40)}
	if err := (MergeQueueRunCompletion{Store: store, Now: func() time.Time { return at }}).RecordRun(ctx, entry, landed); err != nil {
		t.Fatalf("the queue's completion: %v", err)
	}
	forge := &closingForge{}
	retirer := MergeQueuePublicationRetirer{Store: store, Forge: forge, Branches: keptBranches{}, Now: func() time.Time { return at }}
	if err := retirer.retireRunRequest(ctx, entry, landed); err != nil {
		t.Fatalf("the queue's retirement: %v", err)
	}

	// The admission's copy, read before both, is refused rather than saved over
	// them, and the refusal names the two fields it would have lost.
	admission.Retries = append(admission.Retries, runstate.Retry{Boundary: runstate.RetryTrackerWrite, Attempt: 1, At: at})
	err = store.Save(admission)
	var stale runstate.StaleRunError
	if !errors.As(err, &stale) || strings.Join(stale.Fields, ",") != "integration,pull_request,retries" {
		t.Fatalf("a save of the admission's older copy: error = %v, want it refused naming integration, pull_request and retries", err)
	}

	// The admission ends with a narrow update, on the run as stored.
	if _, err := store.Update(ctx, run.RunID, runstate.QueuedRunCompleted{At: at}); err != nil {
		t.Fatalf("the admission's ending: %v", err)
	}

	// A transfer names the entry the change moved to.
	moved := entry
	moved.EntryID, moved.Mode = "mqe-"+strings.Repeat("2", 32), runstate.MergeQueueForge
	promoter := MergeQueuePromoter{Pipeline: &Pipeline{Store: store}}
	if err := promoter.recordReadmission(ctx, moved); err != nil {
		t.Fatalf("the transfer's record: %v", err)
	}

	// Reconciliation records a cleanup that left the branch, and then the rest.
	if _, err := store.Update(ctx, run.RunID, runstate.CleanupRecorded{WorktreeRemoved: true, Failure: "the branch could not be deleted", At: at}); err != nil {
		t.Fatalf("reconciliation's first cleanup: %v", err)
	}
	if _, err := store.Update(ctx, run.RunID, runstate.CleanupRecorded{WorktreeRemoved: true, BranchRemoved: true, At: at}); err != nil {
		t.Fatalf("reconciliation's second cleanup: %v", err)
	}

	final, err := store.Load(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case final.Integration == nil || final.Integration.TargetCommit != landed.Landed:
		t.Errorf("the landing is lost: integration = %+v", final.Integration)
	case final.PullRequest == nil || final.PullRequest.Superseded == "" || final.PullRequest.State != "CLOSED":
		t.Errorf("the retirement is lost: pull request = %+v", final.PullRequest)
	case final.MergeQueue == nil || final.MergeQueue.EntryID != moved.EntryID:
		t.Errorf("the transfer is lost: merge queue = %+v", final.MergeQueue)
	case final.Status != runstate.StatusSucceeded || final.CompletedAt == nil:
		t.Errorf("the admission's ending is lost: status %s, completed %v", final.Status, final.CompletedAt)
	case !final.WorktreeRemoved || !final.BranchRemoved || final.CleanupFailure != "" || final.Phase != runstate.PhaseComplete:
		t.Errorf("the cleanup is lost: worktree %v, branch %v, failure %q, phase %s", final.WorktreeRemoved, final.BranchRemoved, final.CleanupFailure, final.Phase)
	}
	if len(forge.closed) != 1 {
		t.Errorf("the forge was asked to close %v, want pull request 41 once", forge.closed)
	}
}

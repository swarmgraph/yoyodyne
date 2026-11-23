package runstate

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A queue's wait for GitHub is kept beside the queue until it is cleared, is
// refused where it cannot say what it waits for, and rides on the latest pass
// but not on the history.
func TestAQueuesWaitForGitHubIsKeptUntilCleared(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewMergeQueueStore() error = %v", err)
	}
	if _, found, err := store.GitHubWait(mainQueue); err != nil || found {
		t.Fatalf("GitHubWait() before any = %t, %v; want none", found, err)
	}
	since := time.Date(2026, 11, 13, 14, 4, 0, 0, time.UTC)
	wait := MergeQueueGitHubWait{EntryID: "mqe-" + strings.Repeat("a", 32), Wait: ForgeWait{
		Step: "pushing the candidate to yoyodyne/merge-queue/x", Since: since, Tries: 2, NextTryAt: since.Add(time.Second),
		Failure: strings.Repeat("Could not resolve host: github.com ", 200),
	}}
	if err := store.RecordGitHubWait(ctx, mainQueue, wait); err != nil {
		t.Fatalf("RecordGitHubWait() error = %v", err)
	}
	kept, found, err := store.GitHubWait(mainQueue)
	if err != nil || !found || kept.EntryID != wait.EntryID || kept.Wait.Tries != 2 || !kept.Wait.NextTryAt.Equal(wait.Wait.NextTryAt) || len(kept.Wait.Failure) > MaxRetryFailureBytes {
		t.Fatalf("GitHubWait() = %#v, %t, %v; want the wait kept with its failure bounded", kept, found, err)
	}
	if says := kept.Says(); !strings.HasPrefix(says, "the merge queue is waiting for GitHub to be reachable again") || strings.Contains(says, "development manager") {
		t.Fatalf("Says() = %q, want the queue's wait in plain words and no report claimed", says)
	}

	pass := MergeQueuePass{At: since, Outcome: MergeQueuePassWaiting, Says: kept.Says(), GitHubWait: &kept}
	if err := store.RecordPass(ctx, mainQueue, pass); err != nil {
		t.Fatalf("RecordPass() error = %v", err)
	}
	last, _, err := store.LastPass(mainQueue)
	if err != nil || last.GitHubWait == nil || last.GitHubWait.Wait.Step != wait.Wait.Step {
		t.Fatalf("LastPass() = %#v, %v; want the wait on the latest pass", last, err)
	}
	history, err := store.PassHistory(mainQueue)
	if err != nil || len(history) != 1 || history[0].GitHubWait != nil {
		t.Fatalf("PassHistory() = %#v, %v; want the pass without its wait", history, err)
	}

	if err := store.RecordGitHubWait(ctx, mainQueue, MergeQueueGitHubWait{EntryID: wait.EntryID}); err == nil {
		t.Fatal("RecordGitHubWait() kept a wait that says neither what it waits for nor when it next tries")
	}
	if err := store.ClearGitHubWait(ctx, mainQueue); err != nil {
		t.Fatalf("ClearGitHubWait() error = %v", err)
	}
	if _, found, err := store.GitHubWait(mainQueue); err != nil || found {
		t.Fatalf("GitHubWait() after clearing = %t, %v; want none", found, err)
	}
}

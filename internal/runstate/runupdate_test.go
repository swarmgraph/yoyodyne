package runstate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var updateAt = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

// publishedRun is a run in flight whose change is published as pull request 41.
func publishedRun(t *testing.T, store *Store) State {
	t.Helper()
	state := testState(t, StatusRunning)
	state.PullRequest = &PullRequest{Remote: "origin", Branch: "yoyodyne/item/run", Number: 41, URL: "https://example.test/pull/41", HeadCommit: strings.Repeat("a", 40), State: "OPEN"}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return load(t, store, state.RunID)
}

func TestAWholeSaveFromACopyReadBeforeANarrowUpdateIsRefusedAndLosesNothing(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	older := publishedRun(t, store) // the run's holder, which read the run first

	written, err := store.Update(context.Background(), older.RunID, PullRequestSuperseded{Number: 41, Vehicle: "merge queue entry 3", State: "CLOSED", At: updateAt})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if written.Revision != older.Revision+1 || written.PullRequest.Superseded != "merge queue entry 3" {
		t.Fatalf("Update() wrote %+v at revision %d, want the request superseded at revision %d", written.PullRequest, written.Revision, older.Revision+1)
	}
	if older.PullRequest.Superseded != "" {
		t.Fatalf("Update() changed the caller's copy through its pointer: %+v", older.PullRequest)
	}

	older.Phase = PhaseChecking
	err = store.Save(older)
	var stale StaleRunError
	if !errors.As(err, &stale) || stale.Held != older.Revision || stale.Stored != written.Revision {
		t.Fatalf("a whole save of the older copy: error = %v, want StaleRunError from revision %d to %d", err, older.Revision, written.Revision)
	}
	var refused RefusedStateError
	if !errors.As(err, &refused) {
		t.Fatalf("a stale save is not a RefusedStateError, so a run that gets it back would not end over the stored record: %v", err)
	}
	if strings.Join(stale.Fields, ",") != "phase,pull_request" || !strings.Contains(err.Error(), older.RunID) {
		t.Fatalf("the refusal names %v in %q, want the run and the fields the copy would overwrite, phase and pull_request", stale.Fields, err)
	}
	stored := load(t, store, older.RunID)
	if stored.PullRequest.Superseded != "merge queue entry 3" || stored.Phase == PhaseChecking {
		t.Fatalf("a refused save changed the record: %+v", stored)
	}

	// Read again, the same change is made and saved, and the holder saves its
	// own copy as often as it likes.
	stored.Phase = PhaseChecking
	if err := store.Save(stored); err != nil {
		t.Fatalf("a save of the copy read again: %v", err)
	}
	stored.Phase = PhaseReviewing
	if err := store.Save(stored); err != nil {
		t.Fatalf("a second save of the holder's own copy: %v", err)
	}
	final := load(t, store, older.RunID)
	if final.Phase != PhaseReviewing || final.PullRequest.Superseded != "merge queue entry 3" || final.Revision != written.Revision {
		t.Fatalf("after reading again and saving the record is %+v at revision %d", final, final.Revision)
	}
}

func TestANarrowUpdateThatChangesNothingWritesNothing(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := publishedRun(t, store)
	for _, update := range []RunUpdate{
		PullRequestSuperseded{Number: 7, Vehicle: "another request's landing", State: "CLOSED", At: updateAt},
		MergeQueueAdmitted{Record: MergeQueueAdmissionRecord{EntryID: "entry-1"}, Readmission: true, At: updateAt},
	} {
		got, err := store.Update(context.Background(), state.RunID, update)
		if err != nil || got.Revision != state.Revision {
			t.Fatalf("Update(%T) = revision %d, %v; want nothing written", update, got.Revision, err)
		}
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("the holder's copy is still current after updates that changed nothing: %v", err)
	}
}

func TestACleanupIsNotRecordedOnARunSomethingMovedOn(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := publishedRun(t, store)
	_, err := store.Update(context.Background(), state.RunID, CleanupRecorded{WorktreeRemoved: true, BranchRemoved: true, At: updateAt})
	if err == nil || !strings.Contains(err.Error(), "rather than cleaning up") {
		t.Fatalf("Update(CleanupRecorded) on a run that is not cleaning up: error = %v, want it refused", err)
	}
	if stored := load(t, store, state.RunID); stored.Revision != state.Revision || stored.WorktreeRemoved {
		t.Fatalf("a refused update wrote %+v", stored)
	}
}

func TestAQueuedRunEndsOnlyWithItsAdmissionRecorded(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := publishedRun(t, store)
	if _, err := store.Update(context.Background(), state.RunID, QueuedRunCompleted{At: updateAt}); err == nil {
		t.Fatal("a run with no admission recorded ended as queued")
	}
	record := MergeQueueAdmissionRecord{EntryID: "mqe-" + strings.Repeat("c", 32), Order: 1, Repository: "yoyodyne", TargetBranch: "main", Mode: MergeQueueHarness, ApprovedHead: strings.Repeat("b", 40), AdmittedAt: updateAt}
	if _, err := store.Update(context.Background(), state.RunID, MergeQueueAdmitted{Record: record, At: updateAt}); err != nil {
		t.Fatalf("Update(MergeQueueAdmitted) error = %v", err)
	}
	ended, err := store.Update(context.Background(), state.RunID, QueuedRunCompleted{At: updateAt})
	if err != nil {
		t.Fatalf("Update(QueuedRunCompleted) error = %v", err)
	}
	if ended.Status != StatusSucceeded || ended.Phase != PhaseCleaningUp || ended.CompletedAt == nil || ended.MergeQueue == nil || ended.PullRequest == nil {
		t.Fatalf("the queued run ended as %+v", ended)
	}
	if ended.Revision != state.Revision+2 {
		t.Fatalf("two updates left the revision at %d, want %d", ended.Revision, state.Revision+2)
	}
}

func TestARunRaisedForDesignEndsCompleteOnce(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := publishedRun(t, store)
	raised := RaisedForDesignCompleted{At: updateAt}
	ended, err := store.Update(context.Background(), state.RunID, raised)
	if err != nil {
		t.Fatalf("Update(RaisedForDesignCompleted) error = %v", err)
	}
	if ended.Status != StatusSucceeded || ended.Phase != PhaseComplete || ended.StopClass != StopNeedsDesign || ended.CompletedAt == nil || ended.PullRequest == nil {
		t.Fatalf("the run raised for design ended as %+v", ended)
	}
	// Made again after a lost reply, it writes nothing.
	again, err := store.Update(context.Background(), state.RunID, RaisedForDesignCompleted{At: updateAt.Add(time.Minute)})
	if err != nil || again.Revision != ended.Revision {
		t.Fatalf("Update(RaisedForDesignCompleted) again = %+v, %v; want nothing written", again, err)
	}
	// A run that ended some other way does not end again as raised for design.
	other := publishedRun(t, store)
	if _, err := store.Update(context.Background(), other.RunID, MergeQueueAdmitted{Record: MergeQueueAdmissionRecord{EntryID: "mqe-" + strings.Repeat("d", 32), Order: 1, Repository: "yoyodyne", TargetBranch: "main", Mode: MergeQueueHarness, ApprovedHead: strings.Repeat("b", 40), AdmittedAt: updateAt}, At: updateAt}); err != nil {
		t.Fatalf("Update(MergeQueueAdmitted) error = %v", err)
	}
	if _, err := store.Update(context.Background(), other.RunID, QueuedRunCompleted{At: updateAt}); err != nil {
		t.Fatalf("Update(QueuedRunCompleted) error = %v", err)
	}
	if _, err := store.Update(context.Background(), other.RunID, raised); err == nil {
		t.Fatal("a run that already ended queued ended again as raised for design")
	}
}

func TestALostMergeQueueEntryStopsOnlyARunWaitingOnTheQueue(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := publishedRun(t, store)
	lost := MergeQueueEntryLost{Blocker: "the queue no longer has the entry", Failure: "the queue no longer has the entry", At: updateAt}
	// A run that is not waiting on the queue is left exactly as it is.
	if unchanged, err := store.Update(context.Background(), state.RunID, lost); err != nil || unchanged.Revision != state.Revision {
		t.Fatalf("Update(MergeQueueEntryLost) on a run not waiting on the queue = %+v, %v; want nothing written", unchanged, err)
	}
	record := MergeQueueAdmissionRecord{EntryID: "mqe-" + strings.Repeat("c", 32), Order: 1, Repository: "yoyodyne", TargetBranch: "main", Mode: MergeQueueHarness, ApprovedHead: strings.Repeat("b", 40), AdmittedAt: updateAt}
	if _, err := store.Update(context.Background(), state.RunID, MergeQueueAdmitted{Record: record, At: updateAt}); err != nil {
		t.Fatalf("Update(MergeQueueAdmitted) error = %v", err)
	}
	if _, err := store.Update(context.Background(), state.RunID, QueuedRunCompleted{At: updateAt}); err != nil {
		t.Fatalf("Update(QueuedRunCompleted) error = %v", err)
	}
	if _, err := store.Update(context.Background(), state.RunID, MergeQueueEntryLost{At: updateAt}); err == nil {
		t.Fatal("a run waiting on the queue was stopped with no reason given")
	}
	stopped, err := store.Update(context.Background(), state.RunID, lost)
	if err != nil {
		t.Fatalf("Update(MergeQueueEntryLost) error = %v", err)
	}
	if stopped.Status != StatusFailed || stopped.StopClass != StopIntegration || stopped.Blocker != lost.Blocker || stopped.Failure != lost.Failure || stopped.AwaitingMergeQueue() {
		t.Fatalf("the run stopped for a lost entry as %+v", stopped)
	}
}

func TestARebuiltWorktreeIsRecordedOnlyWhereItsBranchWasKept(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := publishedRun(t, store)
	swept := updateAt.Add(-time.Hour)
	state.WorktreeRemoved = true
	state.WorktreeSweptAt = &swept
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	state = load(t, store, state.RunID)

	rebuilt, err := store.Update(context.Background(), state.RunID, WorktreeRebuilt{At: updateAt})
	if err != nil {
		t.Fatalf("Update(WorktreeRebuilt) error = %v", err)
	}
	if rebuilt.WorktreeRemoved || rebuilt.WorktreeSweptAt != nil || rebuilt.Revision != state.Revision+1 {
		t.Fatalf("rebuilt = removed %t, swept %v, revision %d; want the checkout recorded back at revision %d", rebuilt.WorktreeRemoved, rebuilt.WorktreeSweptAt, rebuilt.Revision, state.Revision+1)
	}
	// Recording it again changes nothing.
	if again, err := store.Update(context.Background(), state.RunID, WorktreeRebuilt{At: updateAt}); err != nil || again.Revision != rebuilt.Revision {
		t.Fatalf("Update(WorktreeRebuilt) again = revision %d, %v; want nothing written", again.Revision, err)
	}

	// A run whose branch is recorded gone had nothing to rebuild from.
	other := publishedRun(t, store)
	other.WorktreeRemoved = true
	other.WorktreeSweptAt = &swept
	other.BranchRemoved = true
	other.BranchSweptAt = &swept
	if err := store.Save(other); err != nil {
		t.Fatal(err)
	}
	other = load(t, store, other.RunID)
	if _, err := store.Update(context.Background(), other.RunID, WorktreeRebuilt{At: updateAt}); err == nil || !strings.Contains(err.Error(), "as removed") {
		t.Fatalf("Update(WorktreeRebuilt) on a run without its branch: error = %v, want it refused", err)
	}
	if stored := load(t, store, other.RunID); !stored.WorktreeRemoved || stored.Revision != other.Revision {
		t.Fatalf("a refused update wrote %+v", stored)
	}
}

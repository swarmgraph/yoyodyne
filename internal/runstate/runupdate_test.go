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

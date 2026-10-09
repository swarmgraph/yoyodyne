package runstate

// An entry's landing record: written only by the queue's worker, every
// mutation on it before it is asked for, nothing on it ever taken back, and a
// save that may not have landed read back before it is answered.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

func admittedEntry(t *testing.T, store *MergeQueueStore) MergeQueueEntry {
	t.Helper()
	entry, _, err := store.Admit(context.Background(), testAdmission(t, mainQueue))
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	return entry
}

func intendedAttempt(entryID string, number uint64) MergeQueuePromotionAttempt {
	attempt := MergeQueuePromotionAttempt{
		Number: number, Path: MergeQueueLandThroughPullRequest, Generation: number, Binding: strings.Repeat("b", 64),
		TargetBase: strings.Repeat("c", 40), Candidate: strings.Repeat("d", 40),
		CandidateBranch: MergeQueueCandidateBranch(entryID), IntendedAt: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC),
	}
	attempt.Key = MergeQueuePromotionKey(entryID, number, attempt.Path, attempt.Binding, attempt.Candidate)
	return attempt
}

func requested(attempt MergeQueuePromotionAttempt, mutation MergeQueueMutation, result MergeQueueMutationResult) MergeQueuePromotionAttempt {
	record := MergeQueueMutationRecord{Mutation: mutation, Key: attempt.MutationKey(mutation), Commit: attempt.Candidate, RequestedAt: time.Date(2026, 10, 8, 9, 1, 0, 0, time.UTC)}
	if result != "" {
		record.Settled = &MergeQueueSettlement{Result: result, At: time.Date(2026, 10, 8, 9, 2, 0, 0, time.UTC)}
	}
	attempt.Mutations = append(append([]MergeQueueMutationRecord(nil), attempt.Mutations...), record)
	return attempt
}

func TestOnlyTheQueuesWorkerWritesALanding(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	entry := admittedEntry(t, store)
	landing := NewMergeQueueLanding(entry)
	landing.Attempts = []MergeQueuePromotionAttempt{intendedAttempt(entry.EntryID, 1)}
	if err := store.RecordLanding(nil, mainQueue, landing); !errors.Is(err, ErrMergeQueueWorkerLeaseRequired) {
		t.Fatalf("RecordLanding(no lease) = %v, want the worker lease required", err)
	}
	promotion, err := (&Store{root: t.TempDir()}).LeasePromotion(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer promotion.Release()
	if err := store.RecordLanding(promotion, mainQueue, landing); !errors.Is(err, ErrMergeQueueWorkerLeaseRequired) {
		t.Fatalf("RecordLanding(the promotion lease) = %v, want the worker lease required", err)
	}
	if err := store.RecordLanding(workerLease(t, store, mainQueue), mainQueue, landing); err != nil {
		t.Fatalf("RecordLanding() = %v", err)
	}
	read, found, err := store.Landing(mainQueue, entry.EntryID)
	if err != nil || !found || read.RunID != entry.RunID || read.Publication != entry.Publication || len(read.Attempts) != 1 {
		t.Fatalf("Landing() = %#v, %v, %v", read, found, err)
	}
}

func TestALandingRecordIsOnlyEverAddedTo(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	entry := admittedEntry(t, store)
	lease := workerLease(t, store, mainQueue)
	record := func(attempts ...MergeQueuePromotionAttempt) error {
		landing := NewMergeQueueLanding(entry)
		landing.Attempts = attempts
		return store.RecordLanding(lease, mainQueue, landing)
	}
	attempt := intendedAttempt(entry.EntryID, 1)
	pushing := requested(attempt, MergeQueuePushCandidate, "")
	if err := record(pushing); err != nil {
		t.Fatalf("RecordLanding(push requested) = %v", err)
	}
	if err := record(requested(pushing, MergeQueueOpenPullRequest, "")); err == nil {
		t.Fatal("RecordLanding(a mutation requested before the one ahead settled) = nil, want it refused")
	}
	if err := record(attempt); !errors.As(err, new(MergeQueueLandingConflictError)) {
		t.Fatalf("RecordLanding(the requested push removed) = %v, want a conflict", err)
	}
	pushed := requested(attempt, MergeQueuePushCandidate, MergeQueueMutationDone)
	if err := record(pushed); err != nil {
		t.Fatalf("RecordLanding(push settled) = %v", err)
	}
	unsettled := requested(attempt, MergeQueuePushCandidate, MergeQueueMutationNotMade)
	if err := record(unsettled); !errors.As(err, new(MergeQueueLandingConflictError)) {
		t.Fatalf("RecordLanding(a settled push settled again) = %v, want a conflict", err)
	}
	merging := requested(requested(pushed, MergeQueueOpenPullRequest, MergeQueueMutationDone), MergeQueueRequestMerge, "")
	if err := record(merging); err != nil {
		t.Fatalf("RecordLanding(merge requested) = %v", err)
	}
	// A merge nobody knows the answer to keeps its attempt standing: setting it
	// aside, or starting another beside it, could promote the entry twice.
	abandoned := merging
	abandoned.SetAside = &MergeQueueSetAside{At: time.Now().UTC(), Reason: "gave up"}
	if err := record(abandoned); err == nil {
		t.Fatal("RecordLanding(set aside with its merge unsettled) = nil, want it refused")
	}
	if err := record(merging, intendedAttempt(entry.EntryID, 2)); err == nil {
		t.Fatal("RecordLanding(a second attempt beside an open one) = nil, want it refused")
	}
	queued := merging
	queued.Mutations = append([]MergeQueueMutationRecord(nil), merging.Mutations...)
	queued.Mutations[2].Settled = &MergeQueueSettlement{Result: MergeQueueMutationQueued, At: time.Now().UTC()}
	if err := record(queued); err != nil {
		t.Fatalf("RecordLanding(merge queued) = %v", err)
	}
	// An attempt that has asked the forge to merge is never set aside either.
	queuedAside := queued
	queuedAside.SetAside = &MergeQueueSetAside{At: time.Now().UTC(), Reason: "gave up"}
	if err := record(queuedAside); err == nil {
		t.Fatal("RecordLanding(set aside after the merge was queued) = nil, want it refused")
	}
	otherLanding := queued
	otherLanding.Landed = &MergeQueueLanded{Commit: strings.Repeat("9", 40), ConfirmedAt: time.Now().UTC()}
	if err := record(otherLanding); err == nil {
		t.Fatal("RecordLanding(a landing of another commit) = nil, want it refused")
	}
	landed := queued
	landed.Landed = &MergeQueueLanded{Commit: attempt.Candidate, ConfirmedAt: time.Now().UTC()}
	if err := record(landed); err != nil {
		t.Fatalf("RecordLanding(landed) = %v", err)
	}
	if err := record(queued); !errors.As(err, new(MergeQueueLandingConflictError)) {
		t.Fatalf("RecordLanding(landing withdrawn) = %v, want a conflict", err)
	}

	complete := NewMergeQueueLanding(entry)
	complete.Attempts = []MergeQueuePromotionAttempt{landed}
	complete.Completion = &MergeQueueCompletion{
		Attempt: 1, Path: landed.Path, Generation: landed.Generation, Binding: landed.Binding,
		TargetBase: landed.TargetBase, Landed: landed.Candidate, QueueAt: time.Now().UTC(),
	}
	if err := store.RecordLanding(lease, mainQueue, complete); err != nil {
		t.Fatalf("RecordLanding(completion) = %v", err)
	}
	at := time.Now().UTC()
	complete.Completion.RunAt = &at
	if err := store.RecordLanding(lease, mainQueue, complete); err != nil {
		t.Fatalf("RecordLanding(the run's part) = %v", err)
	}
	withdrawn := complete
	withdrawn.Completion = &MergeQueueCompletion{}
	*withdrawn.Completion = *complete.Completion
	withdrawn.Completion.RunAt = nil
	if err := store.RecordLanding(lease, mainQueue, withdrawn); !errors.As(err, new(MergeQueueLandingConflictError)) {
		t.Fatalf("RecordLanding(the run's part withdrawn) = %v, want a conflict", err)
	}
}

func TestAMutationFoundNotMadeMayBeAskedForAgainUnderItsKey(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	entry := admittedEntry(t, store)
	lease := workerLease(t, store, mainQueue)
	attempt := intendedAttempt(entry.EntryID, 1)
	attempt.Path, attempt.Key = MergeQueueLandLocallyThenPullRequest, MergeQueuePromotionKey(entry.EntryID, 1, MergeQueueLandLocallyThenPullRequest, attempt.Binding, attempt.Candidate)
	moved := requested(attempt, MergeQueueMoveTarget, MergeQueueMutationDone)
	again := requested(requested(moved, MergeQueuePushCandidate, MergeQueueMutationNotMade), MergeQueuePushCandidate, "")
	landing := NewMergeQueueLanding(entry)
	landing.Attempts = []MergeQueuePromotionAttempt{again}
	if err := store.RecordLanding(lease, mainQueue, landing); err != nil {
		t.Fatalf("RecordLanding(a push asked for again) = %v", err)
	}
	skipped := requested(requested(moved, MergeQueuePushCandidate, MergeQueueMutationNotMade), MergeQueueOpenPullRequest, "")
	landing.Attempts = []MergeQueuePromotionAttempt{skipped}
	if err := store.RecordLanding(lease, mainQueue, landing); err == nil {
		t.Fatal("RecordLanding(the next mutation after one not made) = nil, want it refused")
	}
}

func TestALandingSaveThatMayNotHaveLandedIsReadBack(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	entry := admittedEntry(t, store)
	lease := workerLease(t, store, mainQueue)
	landing := NewMergeQueueLanding(entry)
	landing.Attempts = []MergeQueuePromotionAttempt{intendedAttempt(entry.EntryID, 1)}

	// A write that landed and then reported failure is a write that landed.
	store.saveLanding = func(queue *repowrite.PinnedRoot, name string, encoded []byte) error {
		if err := queue.WriteFile(name, encoded, 0o600, false); err != nil {
			return err
		}
		return errors.New("the directory would not sync")
	}
	if err := store.RecordLanding(lease, mainQueue, landing); err != nil {
		t.Fatalf("RecordLanding(landed, sync failed) = %v, want it found landed", err)
	}
	// A write whose readback fails too says it may or may not have landed.
	store.readLandingBack = func(*repowrite.PinnedRoot, string) ([]byte, error) {
		return nil, errors.New("the disk would not answer")
	}
	landing.Attempts[0] = requested(landing.Attempts[0], MergeQueuePushCandidate, "")
	if err := store.RecordLanding(lease, mainQueue, landing); !errors.Is(err, ErrMergeQueueLandingSaveUncertain) {
		t.Fatalf("RecordLanding(readback failed) = %v, want it uncertain", err)
	}
	// A write that never landed is reported as not saved.
	store.saveLanding = func(*repowrite.PinnedRoot, string, []byte) error { return errors.New("the disk is full") }
	store.readLandingBack = nil
	landing.Attempts[0].Mutations[0].Settled = &MergeQueueSettlement{Result: MergeQueueMutationDone, At: time.Now().UTC()}
	if err := store.RecordLanding(lease, mainQueue, landing); err == nil || errors.Is(err, ErrMergeQueueLandingSaveUncertain) || !strings.Contains(err.Error(), "not saved") {
		t.Fatalf("RecordLanding(not landed) = %v, want it reported not saved", err)
	}
}

func TestTheForgesQueueRecordsTheCommitItLandedNotTheHeadItWasHanded(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	entry := admittedEntry(t, store)
	lease := workerLease(t, store, mainQueue)
	attempt := MergeQueuePromotionAttempt{
		Number: 1, Path: MergeQueueLandThroughForgeQueue, TargetBase: strings.Repeat("c", 40), Candidate: entry.ApprovedHead,
		CandidateBranch: "yoyodyne/some-item/0123abcd", IntendedAt: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC),
	}
	attempt.Key = MergeQueuePromotionKey(entry.EntryID, 1, attempt.Path, "", attempt.Candidate)
	merged := requested(requested(attempt, MergeQueueOpenPullRequest, MergeQueueMutationDone), MergeQueueRequestMerge, MergeQueueMutationDone)
	record := func(landed MergeQueueLanded) error {
		landing := NewMergeQueueLanding(entry)
		withLanding := merged
		withLanding.Landed = &landed
		landing.Attempts = []MergeQueuePromotionAttempt{withLanding}
		return store.RecordLanding(lease, mainQueue, landing)
	}
	forgeCommit := strings.Repeat("e", 40)
	if err := record(MergeQueueLanded{Commit: entry.ApprovedHead, RemoteMerge: forgeCommit, ConfirmedAt: time.Now().UTC()}); err == nil {
		t.Fatal("RecordLanding(the head handed over, as what landed) = nil, want it refused")
	}
	if err := record(MergeQueueLanded{Commit: entry.ApprovedHead, ConfirmedAt: time.Now().UTC()}); err == nil {
		t.Fatal("RecordLanding(a landing the forge named no commit for) = nil, want it refused")
	}
	if err := record(MergeQueueLanded{Commit: forgeCommit, RemoteMerge: forgeCommit, ConfirmedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("RecordLanding(the forge's combined commit) = %v", err)
	}
}

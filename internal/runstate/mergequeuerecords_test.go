package runstate

// What the merge queue keeps of each entry — the mode it was admitted in, what
// its worker last found, the candidates verified for it, and what became of
// it — is read back unchanged by a process that did not write it, and a record
// that cannot be read is said to be unreadable rather than read as nothing.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

func TestEveryMergeQueueRecordReadsBackUnchangedAfterARestart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	ctx := context.Background()
	released := admittedEntry(t, store)
	waiting, _, err := store.Admit(ctx, testAdmission(t, mainQueue))
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}

	lease := workerLease(t, store, mainQueue)
	generation := verifiedGeneration(t)
	generation.EntryID, generation.EntryOrder = waiting.EntryID, waiting.Order
	binding := generation.Binding()
	generation.Paths = &MergeQueuePathEvidence{Binding: binding, CheckedAt: generation.CreatedAt, Changed: 2}
	generation.CheckRun.Binding, generation.Review.Binding = binding, binding
	finished := generation.CreatedAt.Add(3 * time.Minute)
	generation.BaseCheck = &MergeQueueBaseEvidence{
		Binding: binding, Base: generation.TargetBase, Command: "npm test", StartedAt: generation.CreatedAt,
		FinishedAt: &finished, Passed: true,
	}
	if err := store.RecordGeneration(lease, mainQueue, generation); err != nil {
		t.Fatalf("RecordGeneration() = %v", err)
	}
	landing := NewMergeQueueLanding(released)
	landing.Handback = &MergeQueueHandback{
		At: time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), Continuation: MergeQueueReleased,
		Mover: ownership.MoverOf(domain.RoleDevelopmentManager), Reason: "moved to the forge's queue",
		ApprovedHead: released.ApprovedHead, TransferTo: MergeQueueForge,
	}
	if err := store.RecordLanding(lease, mainQueue, landing); err != nil {
		t.Fatalf("RecordLanding() = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	evidence := MergeQueueModeEvidence{Forge: "github", ObservedAt: time.Date(2026, 10, 8, 10, 1, 0, 0, time.UTC), TargetProtected: true, ForgeQueue: true, TimeoutMinutes: 60, Explanation: "the forge's queue gates the combined commit"}
	moved, created, err := store.Transfer(ctx, mainQueue, released.EntryID, MergeQueueForge, evidence, time.Date(2026, 10, 8, 10, 2, 0, 0, time.UTC))
	if err != nil || !created {
		t.Fatalf("Transfer() = %#v, %t, %v", moved, created, err)
	}
	if moved.Supersedes != released.EntryID || moved.WorkPlace() != released.Order || moved.Mode != MergeQueueForge {
		t.Fatalf("the moved entry = %#v, want it to supersede %s and keep place %d", moved, released.EntryID, released.Order)
	}
	refusal := MergeQueueRefusal{
		At: time.Date(2026, 10, 8, 10, 3, 0, 0, time.UTC), WorkItemID: "yoyodyne-test", RunID: waiting.RunID,
		Requirement: "main requires GitHub's merge queue.", Step: "remove the merge queue requirement from main's protection", PersonOnly: true,
	}
	if err := store.RecordRefusal(ctx, mainQueue, refusal); err != nil {
		t.Fatalf("RecordRefusal() = %v", err)
	}
	pass := MergeQueuePass{
		At: time.Date(2026, 10, 8, 10, 4, 0, 0, time.UTC), Outcome: MergeQueuePassWaiting,
		EntryID: waiting.EntryID, WorkItemID: "yoyodyne-test", Says: "the review waits on the operator's pause",
	}
	if err := store.RecordPass(ctx, mainQueue, pass); err != nil {
		t.Fatalf("RecordPass() = %v", err)
	}

	// Everything below is read by a store that wrote none of it.
	reread := newMergeQueueStore(t, root)
	keys, err := reread.Keys()
	if err != nil || !reflect.DeepEqual(keys, []MergeQueueKey{mainQueue}) {
		t.Fatalf("Keys() = %v, %v; want the one queue", keys, err)
	}
	assertQueue(t, reread, released, waiting, moved)
	generations, err := reread.Generations(mainQueue, waiting.EntryID)
	if err != nil || len(generations) != 1 || !reflect.DeepEqual(generations[0], generation) {
		t.Fatalf("Generations() = %#v, %v; want %#v", generations, err, generation)
	}
	gotLanding, found, err := reread.Landing(mainQueue, released.EntryID)
	if err != nil || !found || !reflect.DeepEqual(gotLanding.Handback, landing.Handback) {
		t.Fatalf("Landing() = %#v, %t, %v; want the release recorded", gotLanding, found, err)
	}
	gotRefusal, found, err := reread.Refusal(mainQueue)
	if err != nil || !found || !reflect.DeepEqual(gotRefusal, refusal) {
		t.Fatalf("Refusal() = %#v, %t, %v; want %#v", gotRefusal, found, err, refusal)
	}
	gotPass, found, err := reread.LastPass(mainQueue)
	if err != nil || !found || !reflect.DeepEqual(gotPass, pass) {
		t.Fatalf("LastPass() = %#v, %t, %v; want %#v", gotPass, found, err, pass)
	}
	if standing, held, err := reread.Standing(mainQueue, released.RunID); err != nil || !held || standing.EntryID != moved.EntryID {
		t.Fatalf("Standing() = %#v, %t, %v; want the moved entry", standing, held, err)
	}

	// A refusal cleared is gone, and the queue still reads.
	if err := reread.ClearRefusal(ctx, mainQueue); err != nil {
		t.Fatal(err)
	}
	if _, found, err := newMergeQueueStore(t, root).Refusal(mainQueue); err != nil || found {
		t.Fatalf("Refusal() after it was cleared = %t, %v", found, err)
	}
}

// A run records the entry its change was admitted as, and reads it back.
func TestARunsAdmissionToTheQueueReadsBackAfterARestart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	queues := newMergeQueueStore(t, root)
	entry := admittedEntry(t, queues)
	runs, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	state := testState(t, StatusSucceeded)
	state.RunID = entry.RunID
	admission := entry.AdmissionRecord()
	state.MergeQueue = &admission
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	reopened, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if !reflect.DeepEqual(loaded.MergeQueue, &admission) || !loaded.AwaitingMergeQueue() {
		t.Fatalf("the run's admission read back as %#v, awaiting %t; want %#v", loaded.MergeQueue, loaded.AwaitingMergeQueue(), admission)
	}
	broken := admission
	broken.ApprovedHead = "not a commit"
	state.MergeQueue = &broken
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "merge_queue") {
		t.Fatalf("Validate() of an admission naming no commit = %v, want it refused", err)
	}
}

// A torn record — the queue's own, a candidate's, a landing's, or one kept
// beside the queue — is an error naming it, never an empty reading; and a
// queue whose record is torn does not hide the queues beside it.
func TestATornMergeQueueRecordIsReportedUnreadableNotDropped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	ctx := context.Background()
	entry := admittedEntry(t, store)
	other := MergeQueueKey{Repository: "yoyodyne", TargetBranch: "release"}
	if _, _, err := store.Admit(ctx, testAdmission(t, other)); err != nil {
		t.Fatal(err)
	}
	lease := workerLease(t, store, mainQueue)
	generation := verifiedGeneration(t)
	generation.EntryID = entry.EntryID
	generation.CheckRun.Binding, generation.Review.Binding, generation.Paths.Binding = generation.Binding(), generation.Binding(), generation.Binding()
	if err := store.RecordGeneration(lease, mainQueue, generation); err != nil {
		t.Fatalf("RecordGeneration() = %v", err)
	}
	if err := store.RecordLanding(lease, mainQueue, NewMergeQueueLanding(entry)); err != nil {
		t.Fatalf("RecordLanding() = %v", err)
	}
	if err := store.RecordRefusal(ctx, mainQueue, MergeQueueRefusal{At: time.Now(), WorkItemID: "yoyodyne-test", RunID: entry.RunID, Requirement: "r", Step: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPass(ctx, mainQueue, MergeQueuePass{At: time.Now(), Outcome: MergeQueuePassIdle}); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(store.Root(), mainQueue.directory())
	tear := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(`{"at": "2026-10-`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{generationsRecordName(entry.EntryID), landingRecordName(entry.EntryID), mergeQueueRefusalName, mergeQueuePassName} {
		tear(name)
	}
	if _, err := store.Generations(mainQueue, entry.EntryID); err == nil {
		t.Error("Generations() read a torn record as no candidates")
	}
	if _, _, err := store.Landing(mainQueue, entry.EntryID); err == nil {
		t.Error("Landing() read a torn record as nothing landed")
	}
	if _, _, err := store.Refusal(mainQueue); err == nil {
		t.Error("Refusal() read a torn record as no refusal")
	}
	if _, _, err := store.LastPass(mainQueue); err == nil {
		t.Error("LastPass() read a torn record as no pass")
	}
	tear(mergeQueueRecordName)
	keys, err := store.Keys()
	if err == nil || !strings.Contains(err.Error(), mainQueue.directory()) {
		t.Fatalf("Keys() with main's record torn = %v, want an error naming it", err)
	}
	if !reflect.DeepEqual(keys, []MergeQueueKey{other}) {
		t.Fatalf("Keys() with main's record torn = %v, want the release queue still listed", keys)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Keys() reported a torn record as a missing one: %v", err)
	}
}

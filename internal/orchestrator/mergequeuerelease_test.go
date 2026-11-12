package orchestrator

// The development manager releasing the entry at the head of a merge queue,
// whatever stopped it: recorded as landed outside the queue where its approved
// commit is already on the target, or handed back to its work item, with who
// released it and why on the entry and on the item, and the queue going on.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const releasedBy = "the development manager in conversation chat-1, turn 4"

func (f *promotionFixture) release(release MergeQueueRelease) (MergeQueuePromotion, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if release.By == "" {
		release.By = releasedBy
	}
	return f.promoter.ReleaseHead(ctx, queueKey, release)
}

// mergeOutsideTheQueue puts the entry's approved commit on the target the way
// a person merging its pull request by hand does.
func (f *promotionFixture) mergeOutsideTheQueue() {
	f.t.Helper()
	gitLine(f.t, f.repository, "-c", "user.name=A person", "-c", "user.email=person@example.com",
		"merge", "--no-ff", "--no-edit", f.entry.ApprovedHead)
}

func TestTheDevelopmentManagerReleasesAHeadEntryAlreadyOnTheTargetAsLanded(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	behind := f.admitAnother(queueKey, "behind", "behind.txt", "implemented behind\n")
	// The run was cut from where main stood before its change, as a real run is.
	f.runs.(*admittedRun).state.BaseCommit = gitLine(t, f.repository, "merge-base", "main", f.entry.ApprovedHead)
	f.mergeOutsideTheQueue()
	target := f.local("main")

	var refusal MergeQueueEntryRefusal
	if _, err := f.release(MergeQueueRelease{EntryID: behind.EntryID, Landed: true, Reason: "wrong entry"}); !errors.As(err, &refusal) ||
		!strings.Contains(refusal.Reason, "head of the queue") {
		t.Fatalf("ReleaseHead() of the entry behind the head error = %v, want it refused as not the head", err)
	}
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, WorkItemID: "yoyodyne-other", Landed: true, Reason: "wrong item"}); !errors.As(err, &refusal) {
		t.Fatalf("ReleaseHead() naming another item error = %v, want it refused", err)
	}
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, Landed: true}); err == nil {
		t.Fatal("ReleaseHead() with no reason = nil, want it refused")
	}

	released, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, WorkItemID: f.entry.WorkItemID, Landed: true,
		Reason: "a person merged pull request 1727 by hand, so the queue has nothing to build"})
	if err != nil || !released.Completed || released.HandedBack != nil {
		t.Fatalf("ReleaseHead() = %#v, %v; want the entry completed as landed outside the queue", released, err)
	}
	completion := f.landing().Completion
	if completion == nil || completion.Outside == nil || completion.Outside.By != releasedBy ||
		!strings.Contains(completion.Outside.Reason, "merged pull request 1727 by hand") ||
		completion.Landed != f.entry.ApprovedHead || completion.TargetBase != target || !completion.Whole() {
		t.Fatalf("completion = %#v, want it landed outside the queue, naming who released it and why", completion)
	}
	note := f.tracker.Item.Notes
	if !strings.Contains(note, "recorded as landed outside the queue") || !strings.Contains(note, releasedBy) ||
		!strings.Contains(note, "merged pull request 1727 by hand") || !f.tracker.Closed {
		t.Fatalf("work item notes %q, closed %t; want who released it and why recorded, and the item settled", note, f.tracker.Closed)
	}
	state, err := f.run.Load(f.entry.RunID)
	if err != nil || state.Integration == nil || state.Integration.SourceCommit != f.entry.ApprovedHead {
		t.Fatalf("run integration = %#v, %v; want the approved commit recorded as integrated", state.Integration, err)
	}
	if f.local("main") != target {
		t.Fatal("the release moved the target; it must move nothing")
	}

	// The queue goes on to the entry behind it.
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, Landed: true, Reason: "again"}); !errors.As(err, &refusal) {
		t.Fatalf("ReleaseHead() a second time error = %v, want it refused", err)
	}
	if pass := f.pass(queueKey); pass.Outcome == runstate.MergeQueuePassStopped {
		t.Fatalf("Pass() after the release = %#v, want the queue to go on past the released entry", pass)
	}
	behindLanding, _, err := f.queue.Landing(queueKey, behind.EntryID)
	if err != nil || behindLanding.Completion == nil || !behindLanding.Completion.Whole() {
		t.Fatalf("the entry behind = %#v, %v; want it landed by the next pass", behindLanding, err)
	}
}

func TestReleasingAsLandedIsRefusedWhereTheTargetDoesNotHoldTheChange(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	var refusal MergeQueueEntryRefusal
	_, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, Landed: true, Reason: "I think it merged"})
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "hand it back instead") {
		t.Fatalf("ReleaseHead() as landed error = %v, want it refused because main does not hold the change", err)
	}
	if landing := f.landing(); landing.Completion != nil || landing.Handback != nil {
		t.Fatalf("landing = %#v, want nothing recorded", landing)
	}
}

func TestTheDevelopmentManagerHandsBackAHeadEntryStoppedOnARefusedPush(t *testing.T) {
	t.Parallel()

	f, lander := pushingFixture(t)
	f.promoter.Repair = MergeQueueRunHandback{Store: f.run, Tracker: f.tracker}
	f.verify()
	// The queue stops at its push, and the record says the remote refused it,
	// with no hand-back written: the stop the release has to get past.
	f.lander.stop[runstate.MergeQueuePushCandidate] = "before"
	if _, err := f.promote(); !errors.Is(err, errStopped) {
		t.Fatalf("Promote() error = %v, want the stop", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	worker, held, err := f.landings.LeaseWorker(ctx, queueKey)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %t, %v", held, err)
	}
	landing := f.landing()
	attempt := &landing.Attempts[len(landing.Attempts)-1]
	attempt.Mutations[len(attempt.Mutations)-1].Settled = &runstate.MergeQueueSettlement{
		Result: runstate.MergeQueueMutationNotMade, At: time.Now().UTC(),
		Detail: "remote rejected the push: ! [remote rejected] (protected branch hook declined)",
	}
	if err := f.landings.RecordLanding(worker, queueKey, landing); err != nil {
		t.Fatal(err)
	}
	_ = worker.Release()
	pushes := lander.count()

	released, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, WorkItemID: f.entry.WorkItemID,
		Reason: "the remote keeps refusing the candidate branch, and the fix for that is queued behind this entry"})
	if err != nil || released.HandedBack == nil {
		t.Fatalf("ReleaseHead() = %#v, %v; want the entry handed back", released, err)
	}
	handback := f.landing().Handback
	if handback.Continuation != runstate.MergeQueueReleased || !strings.Contains(handback.Reason, "released from the head of the queue by "+releasedBy) ||
		!strings.Contains(handback.Reason, "fix for that is queued behind this entry") {
		t.Fatalf("handback = %#v, want it to say who released it and why", handback)
	}
	if !f.tracker.Blocked || !strings.Contains(f.tracker.BlockReason, releasedBy) || !strings.Contains(f.tracker.BlockReason, "fix for that is queued behind this entry") {
		t.Fatalf("work item blocked %t with %q, want it blocked saying who released it and why", f.tracker.Blocked, f.tracker.BlockReason)
	}
	if lander.count() != pushes {
		t.Fatal("the release pushed the candidate; it must push nothing")
	}
	if next, err := f.promote(); err != nil || next.Entry.EntryID != "" {
		t.Fatalf("Promote() after the release = %#v, %v; want nothing left at the head", next, err)
	}
}

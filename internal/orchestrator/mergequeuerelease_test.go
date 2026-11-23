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

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
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
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, WorkItemID: "yoyodyne-other", Landed: true, Reason: "wrong item"}); !errors.As(err, &refusal) ||
		!strings.Contains(refusal.Reason, MergeQueueEntryItem(f.entry)) || !strings.Contains(refusal.Reason, "yoyodyne-other, the item the release named") {
		t.Fatalf("ReleaseHead() naming another item error = %v, want it refused naming the entry's item by what it is", err)
	}
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, Landed: true}); err == nil {
		t.Fatal("ReleaseHead() with no reason = nil, want it refused")
	}

	released, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, WorkItemID: f.entry.WorkItemID, Landed: true,
		Reason: "a person merged pull request 1727 by hand, so the queue has nothing to build"})
	if err != nil || released.HandedBack == nil {
		t.Fatalf("ReleaseHead() = %#v, %v; want the entry to leave the queue as landed outside it", released, err)
	}
	handback := f.landing().Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueLandedOutside || handback.HandedBackAt == nil ||
		!strings.Contains(handback.Reason, "released from the head of the queue by "+releasedBy) ||
		!strings.Contains(handback.Reason, "merged pull request 1727 by hand") ||
		handback.ApprovedHead != f.entry.ApprovedHead || handback.TargetBase != target {
		t.Fatalf("handback = %#v, want it landed outside the queue, naming who released it and why", handback)
	}
	note := f.tracker.Item.Notes
	if !strings.Contains(note, "outside the merge queue") || !strings.Contains(note, releasedBy) ||
		!strings.Contains(note, "merged pull request 1727 by hand") || !f.tracker.Closed {
		t.Fatalf("work item notes %q, closed %t; want who released it and why recorded, and the item settled", note, f.tracker.Closed)
	}
	state, err := f.run.Load(f.entry.RunID)
	if err != nil || state.Integration == nil || state.Integration.TargetCommit != target {
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

func TestReleasingAsLandedLeavesAClosedItemClosedAndOffersNoRepair(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	f.mergeOutsideTheQueue()
	// A person closed the item when they merged its pull request by hand.
	f.tracker.Item.Status = "closed"

	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, Landed: true,
		Reason: "merged by hand as pull request 1727"}); err != nil {
		t.Fatalf("ReleaseHead() error = %v", err)
	}
	if f.tracker.Item.Status != "closed" || f.tracker.Blocked {
		t.Fatalf("item status %q, blocked %t; want it left closed", f.tracker.Item.Status, f.tracker.Blocked)
	}
	if !strings.Contains(f.tracker.Item.Notes, releasedBy) || !strings.Contains(f.tracker.Item.Notes, "merged by hand as pull request 1727") {
		t.Fatalf("work item notes %q, want who released it and why recorded on the closed item", f.tracker.Item.Notes)
	}
	if repair.count() != 0 {
		t.Fatalf("repair given %#v, want no repair offered for a change that landed", repair.given)
	}
}

func TestMergeQueueEntryItemNamesTheItemByWhatItIs(t *testing.T) {
	t.Parallel()

	titled := runstate.MergeQueueEntry{WorkItemID: "yoyodyne-task", WorkItemTitle: "Release the queue head"}
	if got := MergeQueueEntryItem(titled); got != `"Release the queue head" (yoyodyne-task)` {
		t.Errorf("MergeQueueEntryItem() = %q, want the title with the identifier after it", got)
	}
	if got := MergeQueueEntryItem(runstate.MergeQueueEntry{WorkItemID: "yoyodyne-task"}); got != "work item yoyodyne-task" {
		t.Errorf("MergeQueueEntryItem() with no title = %q, want it said to be a work item", got)
	}
}

func TestTheHeadReleasedIsTheEntryTheQueueWorksPastOneWaitingOnTheTarget(t *testing.T) {
	t.Parallel()

	// The target fails a check, and the change that fixes it is queued behind
	// an entry whose candidate fails the same check: the queue passes over that
	// entry while it waits for the target, so the fix is the head.
	f := newPromotionFixture(t, landLocally)
	f.worker.Pipeline.Config.Checks = []string{"test -f fixed.txt"}
	fix := f.admitAnother(queueKey, "fix", "fixed.txt", "fixed\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if verification, err := f.worker.Work(ctx, queueKey); err != nil || verification.Verified {
		t.Fatalf("Work() = %#v, %v; want the first entry's candidate failing", verification, err)
	}
	if generations := f.generations(); len(generations) != 1 || !generations[0].WaitsOnTarget() {
		t.Fatalf("generations = %#v, want the first entry waiting on its target", generations)
	}

	var refusal MergeQueueEntryRefusal
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, Reason: "the wrong one"}); !errors.As(err, &refusal) ||
		!strings.Contains(refusal.Reason, fix.EntryID) {
		t.Fatalf("ReleaseHead() of the waiting entry error = %v, want it refused naming the fix as the head", err)
	}
	released, err := f.release(MergeQueueRelease{EntryID: fix.EntryID, WorkItemID: fix.WorkItemID,
		Reason: "the fix keeps stopping at its push and has to be handed back"})
	if err != nil || released.HandedBack == nil || released.HandedBack.Continuation != runstate.MergeQueueReleased {
		t.Fatalf("ReleaseHead() of the fix = %#v, %v; want it released as the head", released, err)
	}
	if landing := f.landing(); landing.Handback != nil || landing.Completion != nil {
		t.Fatalf("the waiting entry's landing = %#v, want it left waiting", landing)
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

// setAsideFixture is a head entry whose run ended succeeded with its checks
// passed and its change admitted, a second entry behind it, and a hand-back
// that puts set-aside runs on a real docket. The run store refuses a record
// that breaks a rule of the run's record, as the real store does.
func setAsideFixture(t *testing.T) (*promotionFixture, validatingRun, runstate.MergeQueueEntry, *runstate.DocketStore) {
	t.Helper()
	f, runs, _ := releasedRunHandback(t)
	behind := f.admitAnother(queueKey, "behind", "behind.txt", "implemented behind\n")
	store := f.run.admittedRun.StateStore.(*runstate.Store)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	docketer := &Docketer{Docket: docket, Runs: latestRun{f.run}, Decisions: store.Triage(), Reruns: store.Reruns()}
	f.promoter.Repair = MergeQueueRunHandback{Store: runs, Tracker: f.tracker, Docket: docketer}
	// The entry behind passes its checks on a target without the head's change,
	// so it lands once the queue goes past the head.
	f.worker.Pipeline.Config.Checks = []string{"test -f behind.txt"}
	return f, runs, behind, docket
}

func TestTheDevelopmentManagerSetsAsideTheHeadEntryWritingNothingToItsRun(t *testing.T) {
	t.Parallel()

	f, _, behind, docket := setAsideFixture(t)
	before := f.run.latest()
	reason := "its run's record refuses every hand-back, and the entries behind it have waited since 01:36"

	released, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, WorkItemID: f.entry.WorkItemID, SetAside: true, Reason: reason})
	if err != nil || released.HandedBack == nil || !released.HandedBack.DecidedSetAside() {
		t.Fatalf("ReleaseHead() set aside = %#v, %v; want the entry set aside", released, err)
	}
	handback := f.landing().Handback
	if handback == nil || handback.SetAsideBy != releasedBy || handback.SetAside != reason || handback.DocketedAt == nil ||
		handback.HandedBackAt != nil || handback.ApprovedHead != f.entry.ApprovedHead || handback.Continuation != runstate.MergeQueueReleased {
		t.Fatalf("handback = %#v, want it set aside by %q with the reason, put on the list, and not given to the run", handback, releasedBy)
	}

	// Nothing was written to the run: it keeps its approval and its checks.
	if f.run.saveCount() != 0 {
		t.Fatalf("the run was saved %d times; a set-aside writes nothing to it", f.run.saveCount())
	}
	after := f.run.latest()
	if after.Status != runstate.StatusSucceeded || after.ChecksPassed == nil || after.MergeQueue == nil ||
		after.MergeQueue.ApprovedHead != before.MergeQueue.ApprovedHead || after.Blocker != "" {
		t.Fatalf("run = %#v, want it exactly as it was, approved commit and checks kept", after)
	}

	// The run is on the development manager's list of stopped runs, naming the
	// approved commit it keeps, who set it aside, and why.
	entries, err := docket.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("docket = %#v, %v; want the run docketed once", entries, err)
	}
	listed := entries[0]
	if listed.Class != triage.ClassStoppedRun || listed.RunID != f.entry.RunID || listed.WorkItemID != f.entry.WorkItemID ||
		!strings.Contains(listed.Failure, f.entry.ApprovedHead) || !strings.Contains(listed.Failure, releasedBy) || !strings.Contains(listed.Failure, reason) {
		t.Fatalf("docket entry = %#v, want the stopped run naming its approved commit, who set it aside, and why", listed)
	}
	if note := f.tracker.Item.Notes; !strings.Contains(note, releasedBy) || !strings.Contains(note, reason) || f.tracker.Blocked {
		t.Fatalf("work item notes %q, blocked %t; want a note saying who set it aside and why", note, f.tracker.Blocked)
	}

	// The queue goes on to the entry behind it at its next pass.
	if pass := f.pass(queueKey); pass.Outcome == runstate.MergeQueuePassStopped {
		t.Fatalf("Pass() after the set-aside = %#v, want the queue to go on", pass)
	}
	behindLanding, _, err := f.queue.Landing(queueKey, behind.EntryID)
	if err != nil || behindLanding.Completion == nil || !behindLanding.Completion.Whole() {
		t.Fatalf("the entry behind = %#v, %v; want it landed by the next pass", behindLanding, err)
	}
	if f.run.saveCount() != 0 {
		t.Fatal("a pass after the set-aside wrote to the set-aside run")
	}
	if again, err := docket.List(); err != nil || len(again) != 1 {
		t.Fatalf("docket after the pass = %#v, %v; want the run listed once", again, err)
	}
}

func TestASetAsideIsRefusedForAnEntryTheQueueIsNotStoppedOnOrDoesNotHave(t *testing.T) {
	t.Parallel()

	f, _, behind, docket := setAsideFixture(t)
	var refusal MergeQueueEntryRefusal
	if _, err := f.release(MergeQueueRelease{EntryID: behind.EntryID, SetAside: true, Reason: "the wrong one"}); !errors.As(err, &refusal) ||
		!strings.Contains(refusal.Reason, "only the head holds the queue back") || !strings.Contains(refusal.Reason, f.entry.EntryID) {
		t.Fatalf("ReleaseHead() set aside of the entry behind the head error = %v, want it refused naming the head", err)
	}
	if _, err := f.release(MergeQueueRelease{EntryID: "mqe-nothing", SetAside: true, Reason: "no such entry"}); err == nil ||
		!strings.Contains(err.Error(), "admits no entry mqe-nothing") {
		t.Fatalf("ReleaseHead() set aside of an entry the queue does not have error = %v, want it refused saying so", err)
	}
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, SetAside: true, Landed: true, Reason: "both"}); err == nil {
		t.Fatal("ReleaseHead() set aside and landed = nil, want it refused")
	}
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, SetAside: true}); err == nil {
		t.Fatal("ReleaseHead() set aside with no reason = nil, want it refused")
	}
	// A change the target already carries is recorded as landed, not set aside.
	f.mergeOutsideTheQueue()
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, SetAside: true, Reason: "merged by hand"}); !errors.As(err, &refusal) ||
		!strings.Contains(refusal.Reason, "record it as landed instead") {
		t.Fatalf("ReleaseHead() set aside of a landed change error = %v, want it refused pointing at landed", err)
	}
	for _, id := range []string{f.entry.EntryID, behind.EntryID} {
		if landing, _, err := f.queue.Landing(queueKey, id); err != nil || landing.Handback != nil {
			t.Fatalf("landing of %s = %#v, %v; want nothing recorded by a refused set-aside", id, landing, err)
		}
	}
	if entries, err := docket.List(); err != nil || len(entries) != 0 {
		t.Fatalf("docket = %#v, %v; want nothing listed by a refused set-aside", entries, err)
	}
}

func TestASetAsideWhoseListCannotBeWrittenIsFinishedByALaterPass(t *testing.T) {
	t.Parallel()

	f, runs, behind, docket := setAsideFixture(t)
	repair := f.promoter.Repair.(MergeQueueRunHandback)
	// The tracker cannot be reached when the set-aside is recorded.
	f.promoter.Repair = MergeQueueRunHandback{Store: runs, Tracker: unreachableTracker{f.tracker}, Docket: repair.Docket}
	released, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, SetAside: true, Reason: "cannot be handed back"})
	if err != nil || released.HandedBack == nil || released.HandedBack.DocketedAt != nil {
		t.Fatalf("ReleaseHead() set aside = %#v, %v; want it recorded with the list still to write", released, err)
	}
	f.promoter.Repair = repair
	f.pass(queueKey)
	if handback := f.landing().Handback; handback == nil || handback.DocketedAt == nil {
		t.Fatalf("handback after the pass = %#v, want the list written by the pass", handback)
	}
	if entries, err := docket.List(); err != nil || len(entries) != 1 {
		t.Fatalf("docket = %#v, %v; want the run listed once", entries, err)
	}
	if f.run.saveCount() != 0 {
		t.Fatal("finishing the set-aside wrote to its run")
	}
	if landing, _, err := f.queue.Landing(queueKey, behind.EntryID); err != nil || landing.Completion == nil {
		t.Fatalf("the entry behind = %#v, %v; want it landed", landing, err)
	}
}

// unreachableTracker is the fixture's tracker failing every read of an item,
// as a tracker that cannot be reached does.
type unreachableTracker struct{ *orchestratortest.Tracker }

func (unreachableTracker) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{}, errors.New("the tracker could not be reached")
}

func (f *promotionFixture) readmit(readmit MergeQueueReadmission) (MergeQueueReadmitted, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if readmit.By == "" {
		readmit.By = releasedBy
	}
	return f.promoter.ReadmitSetAside(ctx, queueKey, readmit)
}

// After a set-aside, her "readmit" answer puts the same approved commit back in
// the queue behind what waits, the run leaves her list, and the queue lands it
// without anything redone.
func TestASetAsideChangeIsPutBackInTheQueueAtTheSameApprovedCommit(t *testing.T) {
	t.Parallel()

	f, _, behind, docket := setAsideFixture(t)
	// The run as a developer run that was reviewed leaves it, so the queue's own
	// reviewer can be shown independent of its author.
	f.run.admittedRun.state.ProviderSessionID = "developer-session"
	var refusal MergeQueueEntryRefusal
	if _, err := f.readmit(MergeQueueReadmission{EntryID: f.entry.EntryID, Reason: "too early"}); !errors.As(err, &refusal) ||
		!strings.Contains(refusal.Reason, "was not set aside") {
		t.Fatalf("ReadmitSetAside() of an entry not set aside error = %v, want it refused", err)
	}
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, SetAside: true, Reason: "cannot be handed back"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.readmit(MergeQueueReadmission{EntryID: "mqe-nothing", Reason: "no such entry"}); err == nil ||
		!strings.Contains(err.Error(), "admits no entry mqe-nothing") {
		t.Fatalf("ReadmitSetAside() of an entry the queue does not have error = %v, want it refused", err)
	}
	if _, err := f.readmit(MergeQueueReadmission{EntryID: behind.EntryID, Reason: "the wrong one"}); !errors.As(err, &refusal) {
		t.Fatalf("ReadmitSetAside() of an entry not set aside error = %v, want it refused", err)
	}
	if _, err := f.readmit(MergeQueueReadmission{EntryID: f.entry.EntryID}); err == nil {
		t.Fatal("ReadmitSetAside() with no reason = nil, want it refused")
	}

	reason := "the run's record is mended, so the same approved change can land"
	done, err := f.readmit(MergeQueueReadmission{EntryID: f.entry.EntryID, WorkItemID: f.entry.WorkItemID, Reason: reason})
	if err != nil || done.Entry.Supersedes != f.entry.EntryID || done.Entry.ApprovedHead != f.entry.ApprovedHead ||
		done.Entry.RunID != f.entry.RunID || done.Entry.Order <= behind.Order {
		t.Fatalf("ReadmitSetAside() = %#v, %v; want the same approved commit of the same run admitted behind what waits", done, err)
	}
	if note := f.tracker.Item.Notes; !strings.Contains(note, releasedBy) || !strings.Contains(note, reason) || !strings.Contains(note, f.entry.ApprovedHead) {
		t.Fatalf("work item notes %q, want who put it back, why, and the same commit", note)
	}
	all, err := docket.Closures()
	closures := all[triage.Key(triage.ClassStoppedRun, f.entry.RunID)]
	if err != nil || len(closures) != 1 || closures[0].RunID != f.entry.RunID || closures[0].Decision != "readmit" || closures[0].DecidedBy != releasedBy {
		t.Fatalf("docket closures = %#v, %v; want the run's entry on her list settled by her decision", closures, err)
	}
	// Asking again names the entry that holds it rather than admitting it twice.
	again, err := f.readmit(MergeQueueReadmission{EntryID: f.entry.EntryID, Reason: "again"})
	if err != nil || again.Entry.EntryID != done.Entry.EntryID {
		t.Fatalf("ReadmitSetAside() again = %#v, %v; want the same entry reported", again, err)
	}
	if entries, err := f.queue.Entries(queueKey); err != nil || len(entries) != 3 {
		t.Fatalf("entries = %#v, %v; want one entry added", entries, err)
	}

	// The queue lands the entry behind, then the readmitted change.
	f.pass(queueKey)
	landing, _, err := f.queue.Landing(queueKey, done.Entry.EntryID)
	if err != nil || landing.Completion == nil || !landing.Completion.Whole() {
		t.Fatalf("the readmitted entry = %#v, %v; want it landed", landing, err)
	}
	if !strings.Contains(gitLine(t, f.repository, "log", "--format=%P", "-1", "main"), f.entry.ApprovedHead) {
		t.Fatal("main does not carry the same approved commit the run kept")
	}
}

func TestAChangeWhoseTargetAlreadyCarriesItIsNotPutBack(t *testing.T) {
	t.Parallel()

	f, _, _, _ := setAsideFixture(t)
	if _, err := f.release(MergeQueueRelease{EntryID: f.entry.EntryID, SetAside: true, Reason: "cannot be handed back"}); err != nil {
		t.Fatal(err)
	}
	f.mergeOutsideTheQueue()
	var refusal MergeQueueEntryRefusal
	if _, err := f.readmit(MergeQueueReadmission{EntryID: f.entry.EntryID, Reason: "put it back"}); !errors.As(err, &refusal) ||
		!strings.Contains(refusal.Reason, "nothing to land") {
		t.Fatalf("ReadmitSetAside() of a change main carries error = %v, want it refused", err)
	}
	if entries, err := f.queue.Entries(queueKey); err != nil || len(entries) != 2 {
		t.Fatalf("entries = %#v, %v; want nothing admitted", entries, err)
	}
}

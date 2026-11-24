package orchestrator

// When the target moves, every waiting entry that no longer merges onto it is
// handed back at once, wherever it stands in the order and with no check run,
// and every entry that still merges keeps its place.

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// mergeCountingLander counts the merges tried in memory.
type mergeCountingLander struct {
	MergeQueueLander
	mu    sync.Mutex
	heads []string
}

func (l *mergeCountingLander) MergeTree(ctx context.Context, base, head string) (string, error) {
	l.mu.Lock()
	l.heads = append(l.heads, head)
	l.mu.Unlock()
	return l.MergeQueueLander.MergeTree(ctx, base, head)
}

func (l *mergeCountingLander) tried() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.heads)
}

// holdWork pauses the operator's harness activity, so a pass builds its head
// candidate and then waits with nothing checked, reviewed or landed.
func (f *promotionFixture) holdWork() {
	f.t.Helper()
	if _, err := f.worker.Pipeline.Holds.(*runstate.OperatorHoldStore).Hold(time.Now()); err != nil {
		f.t.Fatal(err)
	}
}

// landOnTarget commits content to file on main.
func (f *promotionFixture) landOnTarget(file, content string) {
	f.t.Helper()
	writeQueueFile(f.t, f.repository, file, content)
	runPipelineGit(f.t, f.repository, "add", file)
	runPipelineGit(f.t, f.repository, "commit", "-m", "somebody else's "+file)
}

func (f *promotionFixture) handbackOf(entry runstate.MergeQueueEntry) *runstate.MergeQueueHandback {
	f.t.Helper()
	landing, _, err := f.queue.Landing(queueKey, entry.EntryID)
	if err != nil {
		f.t.Fatal(err)
	}
	return landing.Handback
}

func TestAWaitingEntryThatConflictsAfterTheTargetMovesIsHandedBackBeforeItsTurn(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	lander := &mergeCountingLander{MergeQueueLander: f.lander}
	f.promoter.Lander = lander
	clean := f.admitAnother(queueKey, "clean", "clean.txt", "no conflict\n")
	conflicting := f.admitAnother(queueKey, "conflicting", "shared file.txt", "the change's\n")
	f.holdWork()
	driver := f.driver()
	driver.MergeTests = &MergeQueueMergeTests{}
	pass := func() runstate.MergeQueuePass {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return driver.Pass(ctx, queueKey)
	}

	if got := pass(); got.Outcome != runstate.MergeQueuePassWaiting || repair.count() != 0 {
		t.Fatalf("Pass() before the target moved = %#v with %d handed back, want none", got, repair.count())
	}
	before := len(lander.tried())
	// A target that has not moved is not merged again.
	pass()
	if tried := lander.tried()[before:]; slices.Contains(tried, clean.ApprovedHead) || slices.Contains(tried, conflicting.ApprovedHead) {
		t.Fatalf("merges tried on a target that has not moved = %v, want none of the waiting entries", tried)
	}

	f.landOnTarget("shared file.txt", "the target's\n")
	target := f.local("main")
	pass()
	handback := f.handbackOf(conflicting)
	if handback == nil || handback.Conflict == nil || handback.HandedBackAt == nil ||
		!slices.Equal(handback.Conflict.Paths, []string{"shared file.txt"}) || handback.Conflict.TargetBase != target {
		t.Fatalf("handback = %#v, want the entry handed back naming shared file.txt at %s", handback, target)
	}
	if lines := handback.Conflict.Lines; lines == nil || *lines != 2 {
		t.Fatalf("conflict = %#v, want its lines counted as a conflict met building a candidate is", handback.Conflict)
	}
	if repair.count() != 1 {
		t.Fatalf("%d entries handed back, want only the one that conflicts", repair.count())
	}
	if f.checks.calls() != 0 || f.reviewer.calls() != 0 {
		t.Fatal("finding the conflict ran a check or a review")
	}
	waits := MergeQueueWaiting(f.queue)
	if !waits(clean) || !waits(f.entry) {
		t.Fatal("an entry that still merges left the queue")
	}
	if generations, err := f.queue.Generations(queueKey, clean.EntryID); err != nil || len(generations) != 0 {
		t.Fatalf("generations of the entry that merges = %v, %v; want it untouched", generations, err)
	}
}

func TestATargetThatMovesWhileACandidateIsCheckedHandsBackWhatConflictsMeanwhile(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	f.promoter.Repair = &recordingRepair{}
	f.worker.TargetWatch = 5 * time.Millisecond
	conflicting := f.admitAnother(queueKey, "conflicting", "shared.txt", "the change's\n")
	during := false
	f.checks.before = func(call int) {
		if call != 1 {
			return
		}
		f.landOnTarget("shared.txt", "the target's\n")
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end) && !during; time.Sleep(5 * time.Millisecond) {
			during = f.handbackOf(conflicting) != nil
		}
	}
	pass := f.pass(queueKey)
	if !during {
		t.Fatalf("the conflicting entry was not handed back while the head's candidate was being checked; pass = %#v", pass)
	}
	if completion := f.landing().Completion; completion == nil || !completion.Whole() {
		t.Fatalf("Pass() = %#v, want the head built again on the moved target and landed", pass)
	}
}

func TestAConflictingEntryWhoseChangeLandedOutsideTheQueueIsNotHandedBack(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	// Merged by hand on the forge, then changed again on the target.
	f.landOnTarget("feature.txt", "implemented\n")
	merged := f.local("main")
	f.landOnTarget("feature.txt", "changed since\n")
	f.publishedBy(publish.PullRequest{Number: 1759, State: "MERGED", Merged: true, MergeCommit: merged, HeadCommit: f.head})
	found, err := f.promoter.handBackConflicts(context.Background(), queueKey, &MergeQueueMergeTests{})
	if err != nil || found.landed != 1 || found.handedBack != 0 || repair.count() != 0 {
		t.Fatalf("handBackConflicts() = %#v, %v with %d handed back; want it left as landed", found, err, repair.count())
	}
	f.assertLandedOutside(merged)
}

func TestLookingForConflictsLeavesAPromotionUnderWayAlone(t *testing.T) {
	t.Parallel()

	f, _, _ := queuedMergeFixture(t)
	f.promoter.Repair = &recordingRepair{}
	f.landOnTarget("feature.txt", "the target's\n")
	found, err := f.promoter.handBackConflicts(context.Background(), queueKey, &MergeQueueMergeTests{})
	if err != nil || found.handedBack != 0 || len(found.problems) != 0 || f.landing().Handback != nil {
		t.Fatalf("handBackConflicts() = %#v, %v; want the entry whose merge the forge holds left alone", found, err)
	}
}

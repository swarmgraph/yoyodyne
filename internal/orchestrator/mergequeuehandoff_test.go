package orchestrator

// How soon the merge queue moves on after a merge, and how long it took: a
// queue pass that ends waiting goes round again on its own, however long the
// watch's pull takes, and the wait from each landing to the next check is
// written beside the queue.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// mergingQueues is a queue whose entry the forge holds queued until merged is
// closed, and whose next entry's check starts at the first pass after that.
type mergingQueues struct {
	merged       chan struct{}
	nextStarted  chan struct{}
	mu           sync.Mutex
	passes       int
	startedAt    time.Time
	startedOnce  sync.Once
	finishedOnce bool
}

func (q *mergingQueues) Pending() ([]runstate.MergeQueueKey, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finishedOnce {
		return nil, nil
	}
	return []runstate.MergeQueueKey{queueKey}, nil
}

func (q *mergingQueues) Pass(ctx context.Context, key runstate.MergeQueueKey) runstate.MergeQueuePass {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.passes++
	select {
	case <-q.merged:
	default:
		return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassWaiting, Says: "the forge holds the merge of pull request 1 queued until its own requirements are met"}
	}
	q.startedOnce.Do(func() {
		q.startedAt = time.Now()
		close(q.nextStarted)
	})
	q.finishedOnce = true
	return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassLanded, Says: "landed, and the next entry's checks began"}
}

func (q *mergingQueues) TakeHeld(context.Context) MergeQueueHeldPass { return MergeQueueHeldPass{} }

// The watch's own pull is held slow — every pull after the first takes as long
// as the test lets it, as a pull does while every tracker call times out — and
// the forge merges the held entry while it is. The queue learns of the merge
// and starts the next entry's checks within its own interval, without waiting
// for the pull to come round.
func TestTheQueueNoticesAMergeAndStartsTheNextCandidateWhileThePullIsSlow(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	queues := &mergingQueues{merged: make(chan struct{}), nextStarted: make(chan struct{})}
	release := make(chan struct{})
	open := func(ctx context.Context) (Pull, error) {
		harness.mu.Lock()
		pulls := harness.pulls
		harness.mu.Unlock()
		if pulls >= 1 {
			// The slow pull: it does not end until the test has seen what it
			// came to see, or gives up on it.
			select {
			case <-release:
			case <-time.After(30 * time.Second):
			}
		}
		pull, err := harness.open(ctx)
		pull.MergeQueues = queues
		return pull, err
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool { return sleeps < 2 }
	// The bound the item sets is five minutes from merge to the next check;
	// the interval is shortened here in the same proportion as a test's clock.
	const again = 20 * time.Millisecond
	scheduler := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, MergeQueueAgain: again}

	done := make(chan Schedule, 1)
	go func() {
		schedule, err := scheduler.Schedule(context.Background())
		if err != nil {
			t.Errorf("Schedule() error = %v", err)
		}
		done <- schedule
	}()

	// Let the first pass find the merge held, then merge it while the second
	// pull is stuck.
	deadline := time.After(30 * time.Second)
	for {
		queues.mu.Lock()
		passes := queues.passes
		queues.mu.Unlock()
		if passes >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the queue was never passed")
		case <-time.After(5 * time.Millisecond):
		}
	}
	mergedAt := time.Now()
	close(queues.merged)
	select {
	case <-queues.nextStarted:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("the queue did not notice the merge while the pull was slow")
	}
	waited := queues.startedAt.Sub(mergedAt)
	harness.mu.Lock()
	pulls := harness.pulls
	harness.mu.Unlock()
	close(release)
	schedule := <-done

	if pulls > 1 {
		t.Fatalf("the pull came round (%d pulls) before the next check started, so the test did not hold it slow", pulls)
	}
	// Two of the queue's own intervals, with room for a loaded machine, is far
	// inside the slow pull and in proportion far inside five minutes.
	if waited > 2*time.Second {
		t.Fatalf("the next check started %s after the merge, want within a few of the queue's %s intervals", waited, again)
	}
	if len(schedule.QueuePasses) == 0 || schedule.QueuePasses[len(schedule.QueuePasses)-1].Pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("queue passes = %#v, want the landing collected", schedule.QueuePasses)
	}
}

// A queue pass that ends waiting stops going round once the session settles —
// when it drains for a restart, or ends — so a queue the forge holds never
// keeps the session from ending.
func TestAQueuePassWaitingOnTheForgeEndsWhenTheSessionSettles(t *testing.T) {
	t.Parallel()

	queues := &mergingQueues{merged: make(chan struct{}), nextStarted: make(chan struct{})}
	passes := newMergeQueuePasses(context.Background())
	passes.again = time.Hour
	result := make(chan runstate.MergeQueuePass, 1)
	go func() { result <- passes.work(passes.ctx, queues, queueKey) }()
	time.Sleep(20 * time.Millisecond)
	passes.settle()
	passes.settle()
	select {
	case pass := <-result:
		if pass.Outcome != runstate.MergeQueuePassWaiting {
			t.Fatalf("work() = %#v, want the waiting pass it last made", pass)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a pass waiting on the forge went on going round after the session settled")
	}
}

// The wait from a landing to the next entry's check is written beside the
// queue, from the landing to the check, once.
func TestTheWaitFromALandingToTheNextCheckIsRecorded(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	other := f.admitAnother(queueKey, "another", "another.txt", "another change\n")
	if pass := f.pass(queueKey); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want both changes landed", pass)
	}
	handoffs, err := f.queue.Handoffs(queueKey)
	if err != nil {
		t.Fatalf("Handoffs() error = %v", err)
	}
	if len(handoffs) != 1 {
		t.Fatalf("handoffs = %#v, want the one from the first landing to the second entry's check", handoffs)
	}
	handoff := handoffs[0]
	landed := f.landing().Attempts
	first := landed[len(landed)-1].Landed
	generations, err := f.queue.Generations(queueKey, other.EntryID)
	if err != nil || len(generations) == 0 || generations[0].CheckRun == nil {
		t.Fatalf("Generations() = %#v, %v; want the second entry checked", generations, err)
	}
	if handoff.LandedEntry != f.entry.EntryID || handoff.NextEntry != other.EntryID ||
		!handoff.MergedAt.Equal(first.ConfirmedAt) || !handoff.NoticedAt.Equal(first.ConfirmedAt) ||
		!handoff.FirstCheckAt.Equal(generations[0].CheckRun.StartedAt) {
		t.Fatalf("handoff = %#v, want from %s's landing at %s to %s's check at %s", handoff, f.entry.EntryID, first.ConfirmedAt, other.EntryID, generations[0].CheckRun.StartedAt)
	}
	if want := handoff.FirstCheckAt.Sub(handoff.MergedAt) / time.Second; handoff.WaitSeconds != int64(want) {
		t.Fatalf("wait = %ds, want %ds", handoff.WaitSeconds, int64(want))
	}
}

// A landing through a pull request records when the forge says it merged, so
// the wait is counted from the merge rather than from when the queue noticed.
func TestALandingThroughTheForgeRecordsTheForgesMergeTime(t *testing.T) {
	t.Parallel()

	f, _, _ := queuedMergeFixture(t)
	mergedAt := time.Now().Add(-3 * time.Minute).UTC().Truncate(time.Second)
	f.forge.MergedAt = mergedAt
	f.forge.PerformQueuedMerge(t)
	promoted, err := f.promote()
	if err != nil || !promoted.Landed {
		t.Fatalf("Promote() = %#v, %v; want the merge confirmed", promoted, err)
	}
	attempts := f.landing().Attempts
	landed := attempts[len(attempts)-1].Landed
	if landed == nil || !landed.MergedAt.Equal(mergedAt) || !landed.LandedAt().Equal(mergedAt) || !landed.ConfirmedAt.After(mergedAt) {
		t.Fatalf("landed = %#v, want the forge's merge time %s kept beside the confirmation", landed, mergedAt)
	}
}

// slowTracker is a tracker that does not answer while held: recording a
// landing on its work item waits until it gives up, as a tracker call does
// while the store times out.
type slowTracker struct {
	MergeQueueCompletion
	mu    sync.Mutex
	held  bool
	tries int
}

func (c *slowTracker) RecordWorkItem(ctx context.Context, entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion) error {
	c.mu.Lock()
	held := c.held
	c.tries++
	c.mu.Unlock()
	if held {
		<-ctx.Done()
		return ctx.Err()
	}
	return c.MergeQueueCompletion.RecordWorkItem(ctx, entry, completion)
}

func (c *slowTracker) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held = false
}

// A tracker too slow to record a landing on its work item holds up nothing
// behind it: the next entry is built, checked and landed in the same pass,
// the pass ends waiting so it goes round again soon, and once the tracker
// answers the work items are recorded.
func TestASlowTrackerAfterALandingDoesNotHoldTheNextCandidate(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	tracker := &slowTracker{MergeQueueCompletion: f.promoter.Completion, held: true}
	f.promoter.Completion = tracker
	f.promoter.WorkItemBound = 50 * time.Millisecond
	other := f.admitAnother(queueKey, "another", "another.txt", "another change\n")

	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassWaiting {
		t.Fatalf("Pass() = %#v, want it waiting on the work items so it goes round again", pass)
	}
	generations, err := f.queue.Generations(queueKey, other.EntryID)
	if err != nil || len(generations) == 0 || generations[0].CheckRun == nil {
		t.Fatalf("Generations() = %#v, %v; want the next entry checked while the tracker was slow", generations, err)
	}
	for _, entry := range []runstate.MergeQueueEntry{f.entry, other} {
		landing, _, err := f.queue.Landing(queueKey, entry.EntryID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt, ok := landing.Current(); !ok || attempt.Landed == nil || landing.Completion == nil || landing.Completion.WorkItemAt != nil {
			t.Fatalf("entry %s landing = %#v, want it landed with its work item still to record", entry.EntryID, landing)
		}
	}
	if handoffs, err := f.queue.Handoffs(queueKey); err != nil || len(handoffs) != 1 {
		t.Fatalf("Handoffs() = %#v, %v; want the wait to the next check recorded", handoffs, err)
	}

	tracker.release()
	if pass := f.pass(queueKey); pass.Outcome == runstate.MergeQueuePassWaiting || pass.Outcome == runstate.MergeQueuePassStopped {
		t.Fatalf("Pass() once the tracker answers = %#v, want both work items recorded", pass)
	}
	for _, entry := range []runstate.MergeQueueEntry{f.entry, other} {
		landing, _, err := f.queue.Landing(queueKey, entry.EntryID)
		if err != nil || landing.Completion == nil || !landing.Completion.Whole() {
			t.Fatalf("entry %s landing = %#v, %v; want its completion whole", entry.EntryID, landing, err)
		}
	}
	if pending := f.pending(); len(pending) != 0 {
		t.Fatalf("Pending() = %v, want nothing left", pending)
	}
}

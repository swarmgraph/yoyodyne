package orchestrator

// A move onto a new build while the merge queue is checking a candidate: the
// queue records no stop for it, and the session that comes back carries the
// candidate on from the checks that had passed.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// passAgain is a pass asked for again by the session that comes back, retried
// for a bounded while where it waits on a stage an earlier start may still
// hold, for the reason queueFixture.restart gives.
func (f *promotionFixture) passAgain(key runstate.MergeQueueKey) runstate.MergeQueuePass {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		pass := f.pass(key)
		if pass.Outcome != runstate.MergeQueuePassWaiting || !strings.Contains(pass.Says, "may still be running from an earlier start") || time.Now().After(deadline) {
			return pass
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The watch session restarts onto a new build while the candidate's second
// check is running, past the longest it waits for one. The check is cut off,
// and the pass's next step, reading where main stands, fails on the same
// cancellation. Neither is the queue's: the pass is recorded as waiting on the
// next pass rather than stopped, and the next pass — the session on the new
// build — keeps the first check's result, runs the second again, and lands the
// change.
func TestARestartDuringAQueueCheckRecordsNoStopAndTheNextPassCarriesItOn(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	stages := &queueCheckStages{}
	ctx, cancel := context.WithTimeout(withQueueCheckStages(context.Background(), stages), 5*time.Minute)
	defer cancel()
	f.checks.finished = func(call int, result checks.Result) {
		if call == 1 && result.Command == queueChecks[0] {
			stages.close()
			cancel()
		}
	}
	cut := f.driver().Pass(ctx, queueKey)
	if cut.Outcome != runstate.MergeQueuePassWaiting {
		t.Fatalf("Pass() cut off by the restart = %#v, want it waiting on the next pass rather than stopped", cut)
	}
	if !strings.Contains(cut.Says, "restarted onto a new build") || !strings.Contains(cut.Says, "the next pass carries on from the records") {
		t.Fatalf("Pass() says %q, want it to say the restart cut it short and the next pass carries on", cut.Says)
	}
	recorded, found, err := f.queue.LastPass(queueKey)
	if err != nil || !found || recorded.Outcome == runstate.MergeQueuePassStopped {
		t.Fatalf("LastPass() = %#v, %t, %v; want no stop recorded for the queue", recorded, found, err)
	}
	// The pass is marked as ended for the restart, and as having got the queue
	// moving when its check stage began, so a move's cost can be read off the
	// queue's history.
	if !recorded.ForRestart || recorded.Moved == nil {
		t.Fatalf("LastPass() = %#v, want it marked as ended for the restart, with when its checks began", recorded)
	}

	f.checks.finished = nil
	landed := f.passAgain(queueKey)
	if landed.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() on the new build = %#v, want the change landed", landed)
	}
	if ran := f.checks.ran(); len(ran) != 2 || !slices.Equal(ran[0], queueChecks) || !slices.Equal(ran[1], queueChecks[1:]) {
		t.Fatalf("checks ran %v, want every check once and then only the one the restart cut off", ran)
	}
	generations := f.generations()
	last := generations[len(generations)-1]
	if len(last.Interruptions) != 1 || !strings.Contains(last.Interruptions[0].Problem, "cut off by the restart") {
		t.Fatalf("generation = %#v, want the restart's cut recorded once on the candidate that landed", last)
	}
	if f.local("main") != last.Candidate {
		t.Fatalf("main = %s, want the candidate that landed, %s", f.local("main"), last.Candidate)
	}
	history, err := f.queue.PassHistory(queueKey)
	if err != nil || len(history) < 2 {
		t.Fatalf("PassHistory() = %#v, %v; want the cut pass and the pass on the new build", history, err)
	}
	first, final := history[0], history[len(history)-1]
	if !first.ForRestart || final.ForRestart || final.Outcome != runstate.MergeQueuePassLanded || final.Moved == nil || final.Moved.Before(first.At) {
		t.Fatalf("PassHistory() = %#v, want the cut pass for the restart first and the landing on the new build, moving after it, last", history)
	}
}

// A session about to restart onto a new build asks for no review: the pass ends
// waiting rather than starting one the restart would cut off, and the session
// that comes back asks for it.
func TestAQueueAboutToRestartStartsNoReview(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	stages := &queueCheckStages{}
	ctx, cancel := context.WithTimeout(withQueueCheckStages(context.Background(), stages), 5*time.Minute)
	defer cancel()
	// The restart closes the queues just as the last check passes, so the
	// checks finish and the next stage is the review.
	f.checks.finished = func(call int, result checks.Result) {
		if result.Command == queueChecks[len(queueChecks)-1] {
			stages.close()
		}
	}
	pass := f.driver().Pass(ctx, queueKey)
	if pass.Outcome != runstate.MergeQueuePassWaiting || !strings.Contains(pass.Says, "review is asked for by the session that comes back") {
		t.Fatalf("Pass() = %#v, want it waiting with the review left for the session that comes back", pass)
	}
	if f.reviewer.calls() != 0 {
		t.Fatalf("reviews asked for = %d, want none once the restart was coming", f.reviewer.calls())
	}
	f.checks.finished = nil
	if landed := f.passAgain(queueKey); landed.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() on the new build = %#v, want the change landed", landed)
	}
	if f.checks.calls() != 1 {
		t.Fatalf("checks ran %d times, want once: the review alone was left for the new build", f.checks.calls())
	}
}

// A pass the session stopped for an ordinary ending, not a restart, records no
// stop either, and says the session stopped.
func TestAPassItsSessionStoppedRecordsNoStop(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	f.checks.finished = func(call int, result checks.Result) {
		if call == 1 && result.Command == queueChecks[0] {
			cancel()
		}
	}
	pass := f.driver().Pass(ctx, queueKey)
	if pass.Outcome == runstate.MergeQueuePassStopped || !strings.Contains(pass.Says, "the session working the queue stopped") {
		t.Fatalf("Pass() = %#v, want it waiting, saying its session stopped", pass)
	}
}

// pendingQueues is queues with work in them, and nothing else.
type pendingQueues struct{ keys []runstate.MergeQueueKey }

func (q pendingQueues) Pending() ([]runstate.MergeQueueKey, error) { return q.keys, nil }
func (q pendingQueues) Pass(context.Context, runstate.MergeQueueKey) runstate.MergeQueuePass {
	return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassIdle}
}
func (q pendingQueues) TakeHeld(context.Context) MergeQueueHeldPass { return MergeQueueHeldPass{} }

// A session stopping its passes for a restart says it turned a queue away only
// where a queue with work in it had no pass going: that is work the move held
// back. A queue whose one pass is still going was turned away from nothing.
func TestAQueueTurnedAwayForARestartIsWorkHeldBack(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	passes := newMergeQueuePasses(ctx)
	passes.checks.close()
	queues := pendingQueues{keys: []runstate.MergeQueueKey{queueKey}}
	var schedule Schedule
	if !(Scheduler{}).startQueuePasses(&schedule, queues, passes) {
		t.Fatal("a queue with work and no pass going was turned away without it being said")
	}
	passes.inFlight[queueKey] = true
	if (Scheduler{}).startQueuePasses(&schedule, queues, passes) {
		t.Fatal("a queue whose pass is still going was said to be turned away")
	}
	if len(schedule.QueuePasses) != 0 || len(passes.stops) != 0 {
		t.Fatal("a pass was started once the session was stopping its passes for a restart")
	}
}

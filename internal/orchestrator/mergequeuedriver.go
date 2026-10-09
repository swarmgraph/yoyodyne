package orchestrator

// What works the merge queues. docs/designs/integration-through-a-merge-queue.md
// is the design; the worker (mergequeueworker.go), the promoter
// (mergequeuepromotion.go) and recovery (mergequeuerecovery.go) are the parts,
// and this is the loop that takes one queue through them until it has nothing
// more it can do now.
//
// One pass of one queue, in order: a failed candidate is read for what it says
// and handed back where it is the change's (Recover); the first unfinished
// entry is landed, or taken up where an earlier pass stopped (Promote); and
// where nothing is under way the next waiting entry's candidate is built,
// checked and reviewed (Work). Each of the three takes the queue's worker lease
// for itself, so one process works a queue at a time however many sessions are
// watching, and a pass that finds the lease taken does nothing. A pass ends on
// its own: when the queue has nothing waiting, when what is left waits on
// something outside the harness — the operator's pause, a provider, a merge the
// forge holds — or when it stops on something a person or the development
// manager has to look at. It is bounded in steps as well, and the next pass
// carries on from the records.
//
// Neither a pass nor a queued entry is a run, so neither holds a developer
// slot; the reviews a pass asks for are charged to the run whose change they
// judge, as every review is. What the pass ended on is written beside the
// queue (runstate's MergeQueuePass) for status and the dashboard to read.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxMergeQueuePassSteps bounds the steps one pass takes. A step lands an
// entry, hands one back, or verifies one, so this is several entries' worth;
// what is left is the next pass's.
const maxMergeQueuePassSteps = 24

// MergeQueueDirectory is what the driver reads to find the queues with work in
// them, and where it writes what a pass found. It is satisfied by
// *runstate.MergeQueueStore.
type MergeQueueDirectory interface {
	Keys() ([]runstate.MergeQueueKey, error)
	Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error)
	Landing(key runstate.MergeQueueKey, entryID string) (runstate.MergeQueueLanding, bool, error)
	RecordPass(ctx context.Context, key runstate.MergeQueueKey, pass runstate.MergeQueuePass) error
}

// MergeQueueDriver works the product's merge queues.
type MergeQueueDriver struct {
	Worker   MergeQueueWorker
	Promoter MergeQueuePromoter
	Queues   MergeQueueDirectory
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

func (d MergeQueueDriver) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Pending is every queue with an entry the queue has not finished with: one
// not landed and not handed back, or handed back and not yet given to whoever
// takes it up. It reads every queue whether or not execution.merge_queue is
// on, because turning the switch off admits nothing new and drains what was
// already admitted.
func (d MergeQueueDriver) Pending() ([]runstate.MergeQueueKey, error) {
	keys, err := d.Queues.Keys()
	if err != nil {
		return nil, err
	}
	var pending []runstate.MergeQueueKey
	for _, key := range keys {
		unfinished, err := d.unfinished(key)
		if err != nil {
			return nil, err
		}
		if unfinished {
			pending = append(pending, key)
		}
	}
	return pending, nil
}

func (d MergeQueueDriver) unfinished(key runstate.MergeQueueKey) (bool, error) {
	entries, err := d.Queues.Entries(key)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		landing, found, err := d.Queues.Landing(key, entry.EntryID)
		if err != nil {
			return false, err
		}
		switch {
		case !found:
			return true, nil
		case landing.Handback != nil:
			if handbackOwed(*landing.Handback) {
				return true, nil
			}
		case landing.Completion == nil || !landing.Completion.Whole():
			return true, nil
		}
	}
	return false, nil
}

// Pass works one queue as far as it can go now, records what it ended on, and
// reports it. It never returns an error: what stopped it is the pass's account.
func (d MergeQueueDriver) Pass(ctx context.Context, key runstate.MergeQueueKey) runstate.MergeQueuePass {
	pass := d.pass(ctx, key)
	pass.At = d.now()
	if err := d.Queues.RecordPass(context.WithoutCancel(ctx), key, pass); err != nil && pass.Outcome != runstate.MergeQueuePassStopped {
		pass.Says = fmt.Sprintf("%s; and what the pass found could not be recorded: %v", pass.Says, err)
	}
	return pass
}

func (d MergeQueueDriver) pass(ctx context.Context, key runstate.MergeQueueKey) runstate.MergeQueuePass {
	landed, handedBack := 0, 0
	done := func(outcome runstate.MergeQueuePassOutcome, entry runstate.MergeQueueEntry, says string) runstate.MergeQueuePass {
		if outcome == runstate.MergeQueuePassIdle {
			switch {
			case handedBack > 0:
				outcome = runstate.MergeQueuePassHandedBack
			case landed > 0:
				outcome = runstate.MergeQueuePassLanded
			}
		}
		if landed > 0 || handedBack > 0 {
			says = fmt.Sprintf("%s (this pass landed %d and handed back %d)", says, landed, handedBack)
		}
		return runstate.MergeQueuePass{Outcome: outcome, EntryID: entry.EntryID, WorkItemID: entry.WorkItemID, Says: says}
	}
	stopped := func(entry runstate.MergeQueueEntry, err error) runstate.MergeQueuePass {
		if errors.Is(err, ErrMergeQueueWorkerBusy) {
			return done(runstate.MergeQueuePassWaiting, entry, "another process is working this queue")
		}
		return done(runstate.MergeQueuePassStopped, entry, err.Error())
	}
	repeated := ""
	for step := 0; step < maxMergeQueuePassSteps; step++ {
		if ctx.Err() != nil {
			return done(runstate.MergeQueuePassWaiting, runstate.MergeQueueEntry{}, "the session working the queue stopped; the next pass carries on from the records")
		}
		recovered, err := d.Promoter.Recover(ctx, key)
		if err != nil {
			return stopped(recovered.Entry, err)
		}
		if recovered.Handback != nil {
			handedBack++
			continue
		}
		promoted, err := d.Promoter.Promote(ctx, key)
		if err != nil {
			return stopped(promoted.Entry, err)
		}
		// The promoter steps past an entry waiting on its target, so finding
		// nothing to land still leaves the worker to look: the target may have
		// moved under that entry, which is when it is built again.
		underWay := promoted.Attempt.Number != 0 && promoted.Attempt.SetAside == nil
		switch {
		case promoted.Landed && promoted.Completed:
			landed++
			continue
		case promoted.Landed:
			continue
		case promoted.Drift:
			continue
		case promoted.Waiting != "":
			return done(runstate.MergeQueuePassWaiting, promoted.Entry, promoted.Waiting)
		case promoted.Unresolved != "":
			return done(runstate.MergeQueuePassStopped, promoted.Entry, promoted.Unresolved)
		case underWay && promoted.Refusal != "":
			return done(runstate.MergeQueuePassStopped, promoted.Entry, promoted.Refusal)
		}
		verified, err := d.Worker.Work(ctx, key)
		if err != nil {
			return stopped(verified.Entry, err)
		}
		switch {
		case verified.Conflict != nil:
			if _, err := d.Promoter.HandBackConflict(ctx, key, verified.Entry.EntryID, *verified.Conflict); err != nil {
				return stopped(verified.Entry, err)
			}
			handedBack++
		case verified.Waiting != "":
			return done(runstate.MergeQueuePassWaiting, verified.Entry, verified.Waiting)
		case verified.Entry.EntryID == "" && promoted.Entry.EntryID == "":
			return done(runstate.MergeQueuePassIdle, runstate.MergeQueueEntry{}, "nothing in the queue can move now: every entry has landed, been handed back, or waits on its target being made to pass a check")
		case verified.Entry.EntryID == "":
			return done(runstate.MergeQueuePassStopped, promoted.Entry, nonEmpty(promoted.Refusal, "the first unfinished entry is not one the worker can verify"))
		case verified.Verified:
			repeated = ""
		case verified.Refusal != "":
			// A candidate that did not pass is read by Recover on the next step.
			// The same refusal twice in a row is one Recover found nothing to do
			// about, so the pass stops on it rather than going round.
			signature := verified.Entry.EntryID + "\x00" + verified.Refusal
			if signature == repeated {
				return done(runstate.MergeQueuePassStopped, verified.Entry, verified.Refusal)
			}
			repeated = signature
		}
	}
	return done(runstate.MergeQueuePassWaiting, runstate.MergeQueueEntry{}, fmt.Sprintf("the pass took its %d steps; the next pass carries on", maxMergeQueuePassSteps))
}

// ScheduleMergeQueues is the merge queues a watching session works beside its
// pulls. It is satisfied by MergeQueueDriver. A pull wired without one works no
// queue: what is admitted waits until a session that has one is running.
type ScheduleMergeQueues interface {
	Pending() ([]runstate.MergeQueueKey, error)
	Pass(ctx context.Context, key runstate.MergeQueueKey) runstate.MergeQueuePass
}

// maxMergeQueuePassesInFlight bounds how many queues a session works at once.
// Each queue has one pass at a time; this bounds the queues.
const maxMergeQueuePassesInFlight = 8

// mergeQueuePasses is the queue passes a session has in flight, each in a
// goroutine of its own, collected at the top of every pull and waited out when
// the session ends, like its recurring firings.
type mergeQueuePasses struct {
	inFlight map[runstate.MergeQueueKey]bool
	done     chan mergeQueuePassDone
	ctx      context.Context
	cancel   context.CancelFunc
}

type mergeQueuePassDone struct {
	key  runstate.MergeQueueKey
	pass runstate.MergeQueuePass
}

func newMergeQueuePasses(ctx context.Context) *mergeQueuePasses {
	passCtx, cancel := context.WithCancel(ctx)
	return &mergeQueuePasses{
		inFlight: map[runstate.MergeQueueKey]bool{},
		done:     make(chan mergeQueuePassDone, maxMergeQueuePassesInFlight),
		ctx:      passCtx, cancel: cancel,
	}
}

func (m *mergeQueuePasses) idle() bool { return len(m.inFlight) == 0 }

// start launches a pass of every queue with work in it and no pass in flight.
func (s Scheduler) startQueuePasses(schedule *Schedule, queues ScheduleMergeQueues, passes *mergeQueuePasses) {
	pending, err := queues.Pending()
	if err != nil {
		schedule.QueueProblem = fmt.Sprintf("the merge queues could not be read, so nothing queued moves until they can: %v", err)
		return
	}
	for _, key := range pending {
		if passes.inFlight[key] || len(passes.inFlight) >= maxMergeQueuePassesInFlight {
			continue
		}
		passes.inFlight[key] = true
		go func(key runstate.MergeQueueKey) {
			passes.done <- mergeQueuePassDone{key: key, pass: queues.Pass(passes.ctx, key)}
		}(key)
	}
}

// collectQueuePasses takes every pass that has ended into the schedule, and
// with block waits for every one still in flight.
func (s Scheduler) collectQueuePasses(schedule *Schedule, passes *mergeQueuePasses, block bool) {
	for !passes.idle() {
		var done mergeQueuePassDone
		if block {
			done = <-passes.done
		} else {
			select {
			case done = <-passes.done:
			default:
				return
			}
		}
		delete(passes.inFlight, done.key)
		schedule.QueuePasses = append(schedule.QueuePasses, ScheduledQueuePass{Key: done.key, Pass: done.pass})
		switch done.pass.Outcome {
		case runstate.MergeQueuePassStopped:
			schedule.QueueProblem = fmt.Sprintf("the merge queue for %s stopped: %s", done.key.TargetBranch, done.pass.Says)
		default:
			schedule.QueueProblem = ""
		}
	}
}

// ScheduledQueuePass is one merge queue pass a session made.
type ScheduledQueuePass struct {
	Key  runstate.MergeQueueKey  `json:"queue"`
	Pass runstate.MergeQueuePass `json:"pass"`
}

package orchestrator

// Finding a conflict as soon as the target moves.
//
// The worker learns that an entry no longer merges onto the target only when
// it builds that entry's candidate, which for an entry deep in the queue is
// hours after the target moved. So every waiting entry's approved head is
// merged onto the target in memory — no checkout, no check, no review — each
// time the target is found to have moved, and one that conflicts is handed back
// at once exactly as the worker's own conflict is (handBackConflict): its run
// gets its catch-up (mergequeuecatchup.go), and it leaves the order. One that
// merges is left as it is.
//
// The driver looks in every step of a pass, so after each landing, and, while a
// candidate's checks and review run, the worker looks again every
// targetWatchEvery (watchTarget), under the worker lease its call already
// holds. What was merged cleanly is remembered per target commit
// (MergeQueueMergeTests), so a look at a target that has not moved merges only
// the entries admitted since.
//
// Left alone: an entry with a promotion under way or a withdrawn merge, which
// the promoter and recovery decide; one the forge's own queue lands; the entry
// the worker is verifying, which finds its own target moved; and one with a
// stage recorded as started and not finished, whose process may still be
// running and which the worker settles first. An entry whose merge could not be
// tried is tried again at the next look.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// targetWatchEvery is how often the worker looks for a moved target while a
// candidate's checks and review run.
const targetWatchEvery = time.Minute

// MergeQueueMergeTests remembers which waiting entries merged cleanly onto
// which target commit. Merging two commits always answers the same, so a
// remembered answer is never asked for again. It is kept in memory only: a new
// process merges each entry once more. It is safe for concurrent use.
type MergeQueueMergeTests struct {
	mu    sync.Mutex
	clean map[runstate.MergeQueueKey]map[string]string
}

func (t *MergeQueueMergeTests) merged(key runstate.MergeQueueKey) map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.clean[key]
}

func (t *MergeQueueMergeTests) remember(key runstate.MergeQueueKey, clean map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.clean == nil {
		t.clean = map[runstate.MergeQueueKey]map[string]string{}
	}
	t.clean[key] = clean
}

// mergeQueueConflictsFound is what looking for conflicts found and did.
type mergeQueueConflictsFound struct {
	// handedBack and landed count the entries that left the queue: for a
	// conflict or a closed pull request, or as already landed outside it.
	handedBack, landed int
	// problems is what could not be done, none of which holds the queue up.
	problems []error
}

func (f *mergeQueueConflictsFound) add(other mergeQueueConflictsFound) {
	f.handedBack += other.handedBack
	f.landed += other.landed
	f.problems = append(f.problems, other.problems...)
}

// handBackConflicts takes the queue's worker lease and hands back every waiting
// entry that conflicts with the target as it stands. It refuses with
// ErrMergeQueueWorkerBusy while another process works the queue.
func (p MergeQueuePromoter) handBackConflicts(ctx context.Context, key runstate.MergeQueueKey, tests *MergeQueueMergeTests) (mergeQueueConflictsFound, error) {
	worker, held, err := p.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return mergeQueueConflictsFound{}, err
	}
	if !held {
		return mergeQueueConflictsFound{}, ErrMergeQueueWorkerBusy
	}
	defer func() { _ = worker.Release() }()
	return p.handBackConflictsUnder(ctx, worker, key, "", tests), nil
}

// handBackConflictsUnder is handBackConflicts under a worker lease already
// held, leaving the entry named by working alone.
func (p MergeQueuePromoter) handBackConflictsUnder(ctx context.Context, worker *runstate.Lease, key runstate.MergeQueueKey, working string, tests *MergeQueueMergeTests) (found mergeQueueConflictsFound) {
	entries, err := p.Queue.Entries(key)
	if err != nil {
		found.problems = append(found.problems, fmt.Errorf("read the merge queue's entries to look for conflicts: %w", err))
		return found
	}
	if working != "" {
		// A pass gives what is owed as it recovers. While a candidate is verified,
		// which can outlast a returned change's wait, it is given here.
		for _, entry := range entries {
			if ctx.Err() != nil {
				break
			}
			given, err := p.giveReturned(ctx, worker, key, entry)
			if err != nil {
				found.problems = append(found.problems, err)
			} else if given {
				found.handedBack++
			}
		}
	}
	target, err := p.Lander.TargetCommit(ctx, key.TargetBranch)
	if err != nil {
		found.problems = append(found.problems, fmt.Errorf("read where %s stands to look for conflicts: %w", key.TargetBranch, err))
		return found
	}
	before, clean := tests.merged(key), map[string]string{}
	waits := MergeQueueWaiting(p.Queue)
	var untried error
	failed := 0
	for _, entry := range runstate.MergeQueueWorkOrder(entries) {
		if ctx.Err() != nil {
			break
		}
		if entry.EntryID == working || entry.Mode != runstate.MergeQueueHarness || !waits(entry) {
			continue
		}
		if before[entry.EntryID] == target {
			clean[entry.EntryID] = target
			continue
		}
		if p.stageUnfinished(key, entry) {
			continue
		}
		conflict, err := p.conflictWith(ctx, key, entry, target)
		switch {
		case err != nil:
			failed++
			if untried == nil {
				untried = fmt.Errorf("entry %d: %w", entry.Order, err)
			}
		case conflict != nil:
			handback, err := p.handBackFoundConflict(ctx, worker, key, entry, *conflict)
			if err != nil {
				found.problems = append(found.problems, err)
			}
			switch {
			case handback == nil:
			case handback.Continuation == runstate.MergeQueueLandedOutside:
				found.landed++
			default:
				found.handedBack++
			}
		default:
			clean[entry.EntryID] = target
		}
	}
	if ctx.Err() == nil {
		tests.remember(key, clean)
		if untried != nil {
			found.problems = append(found.problems, fmt.Errorf("%d waiting entries could not be tested for a conflict with %s at %s, and are tested again at the next look (%w)",
				failed, key.TargetBranch, shortCommit(target), untried))
		}
	}
	return found
}

// giveReturned gives an entry handed back for a conflict to its run, where that
// is still owed, and reports whether it was given.
func (p MergeQueuePromoter) giveReturned(ctx context.Context, worker *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry) (bool, error) {
	landing, found, err := p.Queue.Landing(key, entry.EntryID)
	if err != nil || !found || landing.Handback == nil || landing.Handback.Conflict == nil || !handbackOwed(*landing.Handback) || p.Repair == nil {
		// A landing record that cannot be read is said by the pass.
		return false, nil
	}
	q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
	if err := q.handBack(ctx); err != nil {
		return false, err
	}
	return !handbackOwed(*q.landing.Handback), nil
}

// conflictWith merges the entry's approved head onto target in memory, and is
// nil where they merge.
func (p MergeQueuePromoter) conflictWith(ctx context.Context, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, target string) (*gitworktree.QueueCandidateConflict, error) {
	_, err := p.Lander.MergeTree(ctx, target, entry.Head())
	var conflict *gitworktree.QueueCandidateConflict
	if errors.As(err, &conflict) {
		conflict.TargetBranch = key.TargetBranch
		return conflict, nil
	}
	return nil, err
}

// stageUnfinished reports an entry whose newest generation records a stage
// started and not finished, or whose generations cannot be read.
func (p MergeQueuePromoter) stageUnfinished(key runstate.MergeQueueKey, entry runstate.MergeQueueEntry) bool {
	generations, err := p.Queue.Generations(key, entry.EntryID)
	if err != nil {
		return true
	}
	if len(generations) == 0 {
		return false
	}
	newest := generations[len(generations)-1]
	return (newest.CheckRun != nil && newest.CheckRun.FinishedAt == nil) ||
		(newest.Review != nil && newest.Review.FinishedAt == nil) ||
		(newest.BaseCheck != nil && newest.BaseCheck.FinishedAt == nil)
}

// handBackFoundConflict ends the turn of an entry that conflicts with the
// target, and returns how it left the queue. A change that already landed, or
// whose pull request was closed, outside the queue can conflict with what came
// after it, so that is asked first (mergequeueoutside.go).
func (p MergeQueuePromoter) handBackFoundConflict(ctx context.Context, worker *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, conflict gitworktree.QueueCandidateConflict) (*runstate.MergeQueueHandback, error) {
	entry, landing, err := p.entry(key, entry.EntryID)
	if err != nil {
		return nil, err
	}
	q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
	if err := q.settleOutside(ctx); err != nil || q.landing.Handback != nil {
		return q.landing.Handback, err
	}
	err = q.handBackConflict(ctx, conflict)
	return q.landing.Handback, err
}

// watchTarget calls TargetMoved every targetWatchEvery until the stop it
// returns is called, which waits for a look already begun to finish, so none
// outlives the worker lease.
func (w MergeQueueWorker) watchTarget(ctx context.Context, lease *runstate.Lease, entry runstate.MergeQueueEntry) (stop func()) {
	if w.TargetMoved == nil {
		return func() {}
	}
	every := w.TargetWatch
	if every <= 0 {
		every = targetWatchEvery
	}
	ended, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ended:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.TargetMoved(ctx, lease, entry.EntryID)
			}
		}
	}()
	return func() {
		close(ended)
		<-done
	}
}

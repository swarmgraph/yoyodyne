package orchestrator

// A change that reached the target outside the merge queue. A person can merge
// a run's own pull request on the forge, or put its approved head on the target
// by hand, while the change still waits in the queue. Building a candidate for
// it then would check and review a change the target already carries, and a
// promotion of that candidate lands nothing new; so before the queue builds a
// candidate for an entry, it looks (settleOutside):
//
//   - The run's own pull request merged on the forge, or the target already
//     holding the approved head: the entry leaves the queue as landed outside
//     it (runstate.MergeQueueLandedOutside), and the landing is recorded on the
//     run as its integration and on the work item, which is settled as any
//     landing settles it (recordLandedOutside). No check runs and nothing is
//     charged.
//   - The run's own pull request closed without merging: the entry is released
//     with that as its reason, and its change is handed back to its run, kept,
//     for the development manager, as any released entry is.
//
// Either ends the entry's turn, and the queue goes on to the entry behind it.
// A forge or a target that cannot be read holds nothing up: the entry is built
// as it would have been. An entry with a promotion under way is the
// promoter's, and is not looked at here.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fullCommitID is a full commit id, as the queue's records require one.
var fullCommitID = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)

// MergeQueueOutside is what one look for a change that reached the target
// outside the queue found and did.
type MergeQueueOutside struct {
	// Entry is the entry whose turn this call ended, and zero where it ended
	// none.
	Entry runstate.MergeQueueEntry
	// Handback is how it left the queue: landed outside it, or released
	// because its pull request was closed without merging.
	Handback *runstate.MergeQueueHandback
}

// SettleOutside looks at the waiting entries of a queue, in the order the
// queue is worked and up to the one the worker would build next, for a change
// that already reached the target or whose pull request was closed outside the
// queue, and ends the turn of the first it finds. It refuses with
// ErrMergeQueueWorkerBusy while another process works the queue.
func (p MergeQueuePromoter) SettleOutside(ctx context.Context, key runstate.MergeQueueKey) (MergeQueueOutside, error) {
	return p.settleOutside(ctx, key, nil)
}

// settleOutside is SettleOutside, passing over the entries in looked, which
// this pass has already looked at, so the forge is asked about an entry once a
// pass rather than at every step of it. Every entry it looks at is added.
func (p MergeQueuePromoter) settleOutside(ctx context.Context, key runstate.MergeQueueKey, looked map[string]bool) (MergeQueueOutside, error) {
	if err := p.validate(); err != nil {
		return MergeQueueOutside{}, err
	}
	worker, held, err := p.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return MergeQueueOutside{}, err
	}
	if !held {
		return MergeQueueOutside{}, ErrMergeQueueWorkerBusy
	}
	defer func() { _ = worker.Release() }()

	entries, err := p.Queue.Entries(key)
	if err != nil {
		return MergeQueueOutside{}, err
	}
	for _, entry := range runstate.MergeQueueWorkOrder(entries) {
		if looked[entry.EntryID] {
			continue
		}
		landing, found, err := p.Queue.Landing(key, entry.EntryID)
		if err != nil {
			return MergeQueueOutside{Entry: entry}, err
		}
		if !found {
			landing = runstate.NewMergeQueueLanding(entry)
		}
		if landing.Handback != nil || landing.Completion != nil {
			continue
		}
		if looked != nil {
			looked[entry.EntryID] = true
		}
		if attempt, attempted := landing.Current(); attempted && attempt.SetAside == nil {
			// A promotion under way lands, is withdrawn, or is set aside by the
			// promoter, which reads the forge itself; nothing here moves it.
			return MergeQueueOutside{}, nil
		}
		q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
		if err := q.settleOutside(ctx); err != nil || q.landing.Handback != nil {
			return MergeQueueOutside{Entry: entry, Handback: q.landing.Handback}, err
		}
		// The worker builds this entry next unless it waits on its target being
		// made to pass a check, in which case it passes over it to the next.
		generations, err := p.Queue.Generations(key, entry.EntryID)
		if count := len(generations); err != nil || count == 0 || !generations[count-1].WaitsOnTarget() {
			return MergeQueueOutside{}, nil
		}
	}
	return MergeQueueOutside{}, nil
}

// settleOutside ends the entry's turn where its change reached the target, or
// its pull request was closed, outside the queue, and leaves it waiting
// otherwise.
func (q *queuePromotion) settleOutside(ctx context.Context) error {
	target := q.key.TargetBranch
	run, err := q.p.Pipeline.Store.Load(q.entry.RunID)
	if published := run.PullRequest; err == nil && published != nil && published.Number > 0 &&
		strings.TrimSpace(published.Branch) != "" && q.p.Pipeline.Publisher != nil {
		// The branch is the handle the run keeps on its request, so a request the
		// forge answers about under another number is not the run's.
		observed, err := q.p.Pipeline.Publisher.State(ctx, published.Branch)
		if err == nil && observed.Number == published.Number {
			switch {
			case observed.Merged:
				return q.landedOutside(ctx, strings.TrimSpace(observed.MergeCommit),
					fmt.Sprintf("pull request #%d, which carries this change, was merged into %s on the forge outside the merge queue", published.Number, target))
			case strings.EqualFold(strings.TrimSpace(observed.State), "closed"):
				return q.closedOutside(ctx, published.Number)
			}
		}
	}
	if !fullCommitID.MatchString(q.entry.ApprovedHead) {
		return nil
	}
	carried, err := q.p.Lander.TargetHolds(ctx, target, q.entry.ApprovedHead)
	if err != nil || !carried {
		return nil
	}
	return q.landedOutside(ctx, "", fmt.Sprintf("%s already carries the approved head %s, put there outside the merge queue", target, q.entry.ApprovedHead))
}

// landedOutside ends the entry's turn as landed outside the queue, and records
// the landing on its run and its item. carrying is the commit the forge named
// as its merge, or empty where the target as it stands is that commit.
func (q *queuePromotion) landedOutside(ctx context.Context, carrying, how string) error {
	target := q.key.TargetBranch
	if !fullCommitID.MatchString(carrying) {
		tip, err := q.p.Lander.TargetCommit(ctx, target)
		if err != nil {
			return fmt.Errorf("read where %s stands to record merge queue entry %d as landed outside the queue: %w", target, q.entry.Order, err)
		}
		carrying = tip
	}
	q.landing.Handback = &runstate.MergeQueueHandback{
		At: q.now(), Continuation: runstate.MergeQueueLandedOutside, Mover: ownership.MoverHarness,
		Reason: oneline.Fold(fmt.Sprintf("%s; %s carries it at %s, so the queue built no candidate for it and ran no check, and the entry left the queue as landed",
			how, target, carrying), 1500),
		TargetBase: carrying, ApprovedHead: q.entry.ApprovedHead,
	}
	if err := q.save(); err != nil {
		q.landing.Handback = nil
		return err
	}
	return q.handBack(ctx)
}

// closedOutside releases an entry whose run's pull request was closed without
// merging: nobody asked the queue to land it any more, so it leaves the queue,
// and its change is handed back to its run, kept, for the development manager.
func (q *queuePromotion) closedOutside(ctx context.Context, number int) error {
	q.landing.Handback = &runstate.MergeQueueHandback{
		At: q.now(), Continuation: runstate.MergeQueueReleased, Mover: ownership.MoverDevelopmentManager,
		Reason: oneline.Fold(fmt.Sprintf("pull request #%d, which carries this change, was closed without being merged outside the merge queue, so the queue built no candidate for it and ran no check; the entry left the queue, and its change is kept on its run's branch for the development manager to decide on",
			number), 1500),
		ApprovedHead: q.entry.ApprovedHead,
	}
	if err := q.save(); err != nil {
		q.landing.Handback = nil
		return err
	}
	return q.handBack(ctx)
}

// recordLandedOutside records an entry that landed outside the queue on its run
// and its work item, once, and writes down that it did.
func (q *queuePromotion) recordLandedOutside(ctx context.Context) error {
	handback := q.landing.Handback
	completion := MergeQueueRunCompletion{Store: q.p.Pipeline.Store, Tracker: q.p.Pipeline.Tracker, Now: func() time.Time { return q.now() }}
	if err := completion.RecordOutside(ctx, q.entry, *handback); err != nil {
		return MergeQueueHandbackUndelivered{Entry: q.entry, Err: fmt.Errorf("record on run %s and %s that merge queue entry %d landed outside the queue: %w",
			q.entry.RunID, q.entry.WorkItemID, q.entry.Order, err)}
	}
	at := q.now()
	handback.HandedBackAt = &at
	return q.save()
}

// RecordOutside records a change that landed outside the queue: on the run as
// its integration onto the commit found carrying it, and on the work item as a
// note saying so, after which the item is settled as any landing settles it.
// Each part asks first whether it is already made.
func (c MergeQueueRunCompletion) RecordOutside(ctx context.Context, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback) error {
	state, err := c.Store.Load(entry.RunID)
	if err != nil {
		return err
	}
	landed := handback.TargetBase
	if state.Integration == nil || state.Integration.TargetCommit != landed {
		previous := strings.TrimSpace(state.BaseCommit)
		if previous == "" {
			previous = landed
		}
		if _, err := c.Store.Update(ctx, entry.RunID, runstate.IntegrationRecorded{
			Integration: runstate.Integration{
				TargetBranch:         entry.TargetBranch,
				SourceCommit:         landed,
				TargetCommit:         landed,
				PreviousTargetCommit: previous,
			},
			At: c.now(),
		}); err != nil {
			return err
		}
	}
	item, err := c.Tracker.Show(ctx, entry.WorkItemID)
	if err != nil {
		return err
	}
	headline := mergeQueueLandedOutsideHeadline(entry)
	if item.Status != "closed" && !strings.Contains(item.Notes, headline) {
		note := strings.Join([]string{
			headline,
			"What happened: " + handback.Reason + ".",
			"Run: " + entry.RunID,
		}, "\n")
		if _, err := c.Tracker.RecordOutcome(ctx, entry.WorkItemID, note); err != nil {
			return err
		}
	}
	return c.settleLandedItem(ctx, entry, landed, fmt.Sprintf("Reviewed by Yoyodyne run %s and landed on %s outside the merge queue, at %s; the queue built no candidate for it.",
		entry.RunID, entry.TargetBranch, landed))
}

// mergeQueueLandedOutsideHeadline is the first line of the note a change that
// landed outside the queue leaves on its item, which is how asking again finds
// the note already there.
func mergeQueueLandedOutsideHeadline(entry runstate.MergeQueueEntry) string {
	return fmt.Sprintf("This change reached %s outside the merge queue, so merge queue entry %d (%s) left the queue as landed without being built or checked again.",
		entry.TargetBranch, entry.Order, entry.EntryID)
}

package orchestrator

// What a person or the development manager does about a merge queue entry the
// queue has stopped on (`yoyo queue retry`, `yoyo queue hand-back`, and the
// development manager's "queue" action in her conversation, which is
// ReleaseHead). The promotion keeps a mutation it could not settle by itself as
// it is rather than asking for it again on a guess (mergequeuepromotion.go),
// and a pass then ends on that entry every time; so does an entry whose
// candidate cannot be built, such as one whose approved commit is already on
// the target. These are the supported ways past it: take the entry up again, so
// the next pass intends a fresh attempt on the same verified candidate; hand it
// back to its run with a reason, so the entries behind it go on; or, for the
// head entry whose approved commit the target already holds, record it as
// landed outside the queue. None moves a target or pushes anything itself, and
// each refuses where something the queue asked for could still land the
// change.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueEntryRefusal is a request about one entry that changed nothing,
// and why.
type MergeQueueEntryRefusal struct {
	EntryID string
	Reason  string
}

func (e MergeQueueEntryRefusal) Error() string {
	return fmt.Sprintf("merge queue entry %s was left as it is: %s", e.EntryID, e.Reason)
}

// Retry takes up again an entry whose newest promotion attempt the queue
// stopped on: the attempt is set aside with the reason, and the next pass
// intends a fresh one under everything read again, on the same verified
// candidate where it still stands. It refuses an entry with nothing stopped,
// one whose last request is still waiting to be looked at (the next pass does
// that by itself), and one where something the queue asked for could still
// land the change.
func (p MergeQueuePromoter) Retry(ctx context.Context, key runstate.MergeQueueKey, entryID, reason string) (MergeQueuePromotion, error) {
	q, release, err := p.stoppedEntry(ctx, key, entryID, reason)
	if err != nil {
		return MergeQueuePromotion{}, err
	}
	defer release()
	refuse := func(format string, args ...any) (MergeQueuePromotion, error) {
		return q.result(), MergeQueueEntryRefusal{EntryID: entryID, Reason: fmt.Sprintf(format, args...)}
	}
	attempt := q.attempt()
	switch {
	case attempt == nil || attempt.SetAside != nil:
		return refuse("no promotion attempt is under way, so there is nothing to take up again; the next pass begins one by itself")
	case attempt.Landed != nil:
		return refuse("promotion attempt %d landed the change", attempt.Number)
	}
	if record, unsettled := attempt.Unsettled(); unsettled {
		return refuse("the %s of promotion attempt %d has no recorded answer yet, and the next pass looks at whether it was made before asking for it again", record.Mutation, attempt.Number)
	}
	if refusal := q.landing.HeadRewriteRefusal(); refusal != "" {
		return refuse("%s", refusal)
	}
	if err := q.setAside(attempt, "taken up again by hand: "+reason, false); err != nil {
		return q.result(), err
	}
	return q.result(), nil
}

// HandBack ends an entry's turn in the queue with its change kept on its run
// for the development manager, with the reason given, so the entries behind it
// go on. Whatever the queue asked the forge for is withdrawn first, exactly as
// Withdraw does, and the hand-back is refused while that withdrawal is not
// confirmed.
func (p MergeQueuePromoter) HandBack(ctx context.Context, key runstate.MergeQueueKey, entryID, reason string) (MergeQueuePromotion, error) {
	q, release, err := p.stoppedEntry(ctx, key, entryID, reason)
	if err != nil {
		return MergeQueuePromotion{}, err
	}
	defer release()
	refuse := func(format string, args ...any) (MergeQueuePromotion, error) {
		return q.result(), MergeQueueEntryRefusal{EntryID: entryID, Reason: fmt.Sprintf(format, args...)}
	}
	if q.landing.Handback != nil {
		return refuse("it already left the queue (%s): %s", q.landing.Handback.Continuation, q.landing.Handback.Reason)
	}
	return p.handBackStopped(ctx, q, refuse, "handed back by hand: "+reason)
}

// MergeQueueRelease is the development manager releasing the entry at the head
// of a queue, so the entries behind it go on whatever stopped it.
type MergeQueueRelease struct {
	// EntryID is the entry released, and has to be the one at the head.
	EntryID string
	// WorkItemID is the item the entry has to be for, where the caller names
	// one: a release written against one item and naming another's entry is
	// refused rather than recorded on the wrong item.
	WorkItemID string
	// Landed records the entry as landed outside the queue rather than handing
	// it back, and is refused unless the target already holds its approved
	// commit.
	Landed bool
	// Reason is why, and By who released it, in words a person reads on the
	// queue entry and the work item.
	Reason string
	By     string
}

// ReleaseHead ends the turn of the entry at the head of the queue, for the
// development manager, whatever stopped it there: a push the remote refused, a
// push cut off before it was answered, an approved commit already on the
// target, or a candidate that could not be built. Two things can become of it.
// Handed back, it leaves the queue exactly as HandBack leaves it, with its
// change kept on its run and the item blocked with the reason. Recorded as
// landed, which is only for an approved commit the target already holds, it is
// completed as landed outside the queue: the run records the integration, and
// the item says who released it and why and is settled as its run's claim says.
//
// Only the head is released, because that is the entry that holds every other
// one back; an entry behind it is not stopped, only waiting. Whatever the queue
// asked the forge for is withdrawn first, as a hand-back's is, and the release
// is refused while that is not confirmed, so nothing released can still be
// landed by a request the queue made. The next pass of the queue goes on to the
// entry behind it.
func (p MergeQueuePromoter) ReleaseHead(ctx context.Context, key runstate.MergeQueueKey, release MergeQueueRelease) (MergeQueuePromotion, error) {
	if strings.TrimSpace(release.By) == "" {
		return MergeQueuePromotion{}, errors.New("releasing the head of a merge queue says who released it")
	}
	why := fmt.Sprintf("released from the head of the queue by %s: %s", strings.TrimSpace(release.By), strings.TrimSpace(release.Reason))
	q, unlock, err := p.stoppedEntry(ctx, key, release.EntryID, release.Reason)
	if err != nil {
		return MergeQueuePromotion{}, err
	}
	defer unlock()
	refuse := func(format string, args ...any) (MergeQueuePromotion, error) {
		return q.result(), MergeQueueEntryRefusal{EntryID: release.EntryID, Reason: fmt.Sprintf(format, args...)}
	}
	head, found, err := p.head(key)
	if err != nil {
		return q.result(), err
	}
	if !found || head.EntryID != q.entry.EntryID {
		if !found {
			return refuse("the queue for %s has no unfinished entry at its head", key.TargetBranch)
		}
		return refuse("it is entry %d, and the head of the queue for %s is entry %d (%s, for %s); only the head holds the queue back, so only the head is released",
			q.entry.Order, key.TargetBranch, head.Order, head.EntryID, head.WorkItemID)
	}
	if want := strings.TrimSpace(release.WorkItemID); want != "" && want != q.entry.WorkItemID {
		return refuse("it is the entry of %s, not of %s; nothing was recorded", q.entry.WorkItemID, want)
	}
	if q.landing.Handback != nil {
		return refuse("it already left the queue (%s): %s", q.landing.Handback.Continuation, q.landing.Handback.Reason)
	}
	if !release.Landed {
		return p.handBackStopped(ctx, q, refuse, why)
	}
	return p.recordLandedOutside(ctx, q, refuse, release, why)
}

// handBackStopped withdraws whatever the entry's queue asked the forge for and
// hands the entry back with the reason, which is HandBack's whole work.
func (p MergeQueuePromoter) handBackStopped(ctx context.Context, q *queuePromotion, refuse func(string, ...any) (MergeQueuePromotion, error), why string) (MergeQueuePromotion, error) {
	if err := q.withdraw(ctx, why); err != nil {
		return q.result(), err
	}
	if q.landing.Completion != nil {
		return refuse("its change has landed")
	}
	if !q.outcome.Withdrawn {
		return refuse("what the queue asked for is not confirmed withdrawn (%s)",
			nonEmpty(q.outcome.Unresolved, q.outcome.Refusal, q.outcome.Waiting, "no answer was recorded"))
	}
	q.outcome.Refusal, q.outcome.Unresolved, q.outcome.Waiting = "", "", ""
	if refusal := q.landing.HeadRewriteRefusal(); refusal != "" {
		return refuse("%s", refusal)
	}
	if err := q.handBackEntry(ctx, why); err != nil {
		return q.result(), err
	}
	return q.result(), nil
}

// recordLandedOutside completes an entry whose approved commit the target
// already holds as landed outside the queue. The target is read first, and
// a commit it does not hold is refused, so nothing is recorded as landed on a
// guess.
func (p MergeQueuePromoter) recordLandedOutside(ctx context.Context, q *queuePromotion, refuse func(string, ...any) (MergeQueuePromotion, error), release MergeQueueRelease, why string) (MergeQueuePromotion, error) {
	target := q.key.TargetBranch
	held, err := p.Lander.TargetHolds(ctx, target, q.entry.ApprovedHead)
	if err != nil {
		return q.result(), fmt.Errorf("read whether %s holds the approved commit %s: %w", target, q.entry.ApprovedHead, err)
	}
	if !held {
		return refuse("%s does not hold its approved commit %s, so it has not landed; hand it back instead", target, q.entry.ApprovedHead)
	}
	if err := q.withdraw(ctx, why); err != nil {
		return q.result(), err
	}
	if q.landing.Completion != nil {
		// The withdrawal found the queue's own attempt landed and completed it as
		// a landing through the queue, which is what it is.
		return q.result(), nil
	}
	if !q.outcome.Withdrawn {
		return refuse("what the queue asked for is not confirmed withdrawn (%s)",
			nonEmpty(q.outcome.Unresolved, q.outcome.Refusal, q.outcome.Waiting, "no answer was recorded"))
	}
	q.outcome.Refusal, q.outcome.Unresolved, q.outcome.Waiting = "", "", ""
	base, err := p.Lander.TargetCommit(ctx, target)
	if err != nil {
		return q.result(), fmt.Errorf("read where %s stands: %w", target, err)
	}
	now := q.now()
	q.landing.Completion = &runstate.MergeQueueCompletion{
		Landed: q.entry.ApprovedHead, TargetBase: base, QueueAt: now,
		Outside: &runstate.MergeQueueOutsideLanding{
			At:     now,
			By:     oneline.Fold(strings.TrimSpace(release.By), 300),
			Reason: oneline.Fold(strings.TrimSpace(release.Reason), 1500),
		},
	}
	if err := q.save(); err != nil {
		q.landing.Completion = nil
		return q.result(), err
	}
	if err := q.recordCompletion(ctx); err != nil {
		return q.result(), err
	}
	return q.result(), nil
}

// head is the entry at the head of the queue: the first, in the order the
// queue is worked, that has neither left it nor been completed whole. It is
// the entry every pass begins with, so it is the one a stop holds the rest
// behind.
func (p MergeQueuePromoter) head(key runstate.MergeQueueKey) (runstate.MergeQueueEntry, bool, error) {
	entries, err := p.Queue.Entries(key)
	if err != nil {
		return runstate.MergeQueueEntry{}, false, err
	}
	for _, entry := range runstate.MergeQueueWorkOrder(entries) {
		landing, found, err := p.Queue.Landing(key, entry.EntryID)
		if err != nil {
			return runstate.MergeQueueEntry{}, false, err
		}
		if found && (landing.Handback != nil || (landing.Completion != nil && landing.Completion.Whole())) {
			continue
		}
		return entry, true, nil
	}
	return runstate.MergeQueueEntry{}, false, nil
}

// stoppedEntry takes the queue's worker lease and reads one unfinished entry,
// for a request about it that says why it is made.
func (p MergeQueuePromoter) stoppedEntry(ctx context.Context, key runstate.MergeQueueKey, entryID, reason string) (*queuePromotion, func(), error) {
	if err := p.validate(); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, nil, errors.New("taking up, handing back, or releasing a merge queue entry says why")
	}
	worker, held, err := p.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	if !held {
		return nil, nil, ErrMergeQueueWorkerBusy
	}
	release := func() { _ = worker.Release() }
	entry, landing, err := p.entry(key, entryID)
	if err != nil {
		release()
		return nil, nil, err
	}
	if landing.Completion != nil {
		release()
		return nil, nil, MergeQueueEntryRefusal{EntryID: entryID, Reason: "its change has landed"}
	}
	q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
	q.outcome.Entry = entry
	return q, release, nil
}

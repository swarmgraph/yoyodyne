package orchestrator

// What a person or the development manager does about a merge queue entry the
// queue has stopped on (`yoyo queue retry` and `yoyo queue hand-back`). The
// promotion keeps a mutation it could not settle by itself as it is rather
// than asking for it again on a guess (mergequeuepromotion.go), and a pass then
// ends on that entry every time. These are the two supported ways past it:
// take the entry up again, so the next pass intends a fresh attempt on the
// same verified candidate, or hand it back to its run with a reason, so the
// entries behind it go on. Neither moves a target or pushes anything itself,
// and each refuses where something the queue asked for could still land the
// change.

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	if err := q.withdraw(ctx, "handed back by hand: "+reason); err != nil {
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
	if err := q.handBackEntry(ctx, "handed back by hand: "+reason); err != nil {
		return q.result(), err
	}
	return q.result(), nil
}

// stoppedEntry takes the queue's worker lease and reads one unfinished entry,
// for a request about it that says why it is made.
func (p MergeQueuePromoter) stoppedEntry(ctx context.Context, key runstate.MergeQueueKey, entryID, reason string) (*queuePromotion, func(), error) {
	if err := p.validate(); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, nil, errors.New("taking up or handing back a merge queue entry says why")
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

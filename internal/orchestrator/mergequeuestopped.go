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
// confirmed. An entry whose change the target already carries — merged by hand
// while the queue was stopped on it — leaves the queue as landed instead, and
// its run and item are settled as after any landing (reachedTarget); one whose
// landing cannot be confirmed is handed back saying why.
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
	// A person hands an entry back most often because they merged its change
	// by hand. Where the target shows that it did, the entry leaves the queue as
	// landed and its item is settled, rather than its run being stopped for a
	// development manager decision nothing needs.
	reached := q.reachedTarget(ctx)
	if reached.landed {
		err := q.landedOutside(ctx, reached.carrying, reached.how+"; it was handed back by hand ("+reason+")")
		q.outcome.HandedBack = q.landing.Handback
		return q.result(), err
	}
	if err := q.handBackEntry(ctx, "handed back by hand: "+reason+"; "+reached.unconfirmed); err != nil {
		return q.result(), err
	}
	return q.result(), nil
}

// reachedTarget is what the target and the run's own pull request show of
// whether an entry's change already landed on the target outside the queue.
type reachedTarget struct {
	// landed is the change confirmed on the target.
	landed bool
	// carrying is the commit found carrying it, or empty for the target as it
	// stands; how says how it got there.
	carrying, how string
	// unconfirmed says in plain words why the change could not be confirmed on
	// the target, where landed is false.
	unconfirmed string
}

// reachedTarget looks for an entry's change on the target: its approved head
// in the target's history, or the run's own pull request reading as merged
// with that approved head as its head and its merge commit in the target's
// history, which is how a squash merge shows. Only the target confirms a
// landing; a pull request that reads as merged with nothing on the target to
// show for it is not one.
func (q *queuePromotion) reachedTarget(ctx context.Context) reachedTarget {
	target, head := q.key.TargetBranch, q.entry.ApprovedHead
	holds := func(commit string) (bool, string) {
		if !fullCommitID.MatchString(commit) {
			return false, fmt.Sprintf("%q is not a full commit id", commit)
		}
		held, err := q.p.Lander.TargetHolds(ctx, target, commit)
		if err != nil {
			return false, fmt.Sprintf("whether %s carries %s could not be read (%v)", target, commit, err)
		}
		return held, ""
	}
	headHeld, headProblem := holds(head)
	var said []string
	if headProblem != "" {
		said = append(said, headProblem)
	}

	run, err := q.p.Pipeline.Store.Load(q.entry.RunID)
	if published := run.PullRequest; err == nil && published != nil && published.Number > 0 &&
		strings.TrimSpace(published.Branch) != "" && q.p.Pipeline.Publisher != nil {
		observed, err := q.p.Pipeline.Publisher.State(ctx, published.Branch)
		switch {
		case err != nil:
			said = append(said, fmt.Sprintf("pull request #%d could not be read (%v)", published.Number, err))
		case observed.Number != published.Number:
			said = append(said, fmt.Sprintf("the forge answers about pull request #%d for %s, not the run's own #%d", observed.Number, published.Branch, published.Number))
		case observed.Merged:
			merge := strings.TrimSpace(observed.MergeCommit)
			if headHeld {
				return reachedTarget{landed: true, carrying: merge,
					how: fmt.Sprintf("pull request #%d, which carries this change, was merged into %s by hand, and %s carries its approved head %s", published.Number, target, target, head)}
			}
			mergeHeld, mergeProblem := holds(merge)
			if mergeHeld && strings.TrimSpace(observed.HeadCommit) == head {
				return reachedTarget{landed: true, carrying: merge,
					how: fmt.Sprintf("pull request #%d, which carries this change at its approved head %s, was merged into %s by hand as %s", published.Number, head, target, merge)}
			}
			switch {
			case mergeHeld:
				said = append(said, fmt.Sprintf("pull request #%d reads as merged as %s, but its head was %s rather than the approved head %s", published.Number, merge, nonEmpty(strings.TrimSpace(observed.HeadCommit), "not reported"), head))
			case mergeProblem != "":
				said = append(said, fmt.Sprintf("pull request #%d reads as merged, but %s", published.Number, mergeProblem))
			default:
				said = append(said, fmt.Sprintf("pull request #%d reads as merged, but %s does not carry its merge commit %s", published.Number, target, merge))
			}
		default:
			said = append(said, fmt.Sprintf("pull request #%d reads %s, not merged", published.Number, strings.ToLower(nonEmpty(strings.TrimSpace(observed.State), "with no state"))))
		}
	}
	if headHeld {
		return reachedTarget{landed: true, how: fmt.Sprintf("%s already carries the approved head %s, put there outside the merge queue", target, head)}
	}
	if headProblem == "" {
		said = append([]string{fmt.Sprintf("%s does not carry the approved head %s", target, head)}, said...)
	}
	return reachedTarget{unconfirmed: "the queue could not confirm the change reached " + target + ": " + strings.Join(said, ", and ")}
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

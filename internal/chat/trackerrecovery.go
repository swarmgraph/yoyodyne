package chat

// A conversation's writes carry every decision the roles make — a triage
// decision, an admission, a note, a closure — and until yoyodyne-ifd.366 not one
// of them was retried. yoyodyne-ifd.264 put the operator's rule, that the harness
// never fails outright on anything that can recover, around the run pipeline's
// boundaries and nowhere else; the path that lost yoyodyne-ifd.142's re-run
// recording, a `bd update` that timed out under the development manager's
// conversation, was left with the honest report yoyodyne-ifd.327 added as its
// only mitigation.
//
// So every call a conversation makes to the tracker goes through the wrapper
// below, which is the pipeline's rule with the pipeline's numbers: a failure
// whose class internal/recovery recognizes is waited out on the same Fibonacci
// backoff, under the same cap and the same window, and asked again. Only a call
// that has spent the window is reported the way it always was — with the
// attempts and the time in front of it, and then with the settling yoyodyne-ifd.327
// added, which is exactly the order the item asked for: the timed-out write is
// reported per 327 only after the retries are spent.
//
// Not every write is asked for again the same way, because not every write is
// safe to repeat. Reads are, and so are the writes that set something to a value
// — a dependency linked or unlinked, a field replaced — since the second of those
// leaves the tracker as the first did. A write that adds something is not: a note
// appended again is the note twice, and an item created again is a second item.
// So a write in that class is asked for again only after the tracker has been
// asked whether the attempt that failed landed, and one that did is reported as
// what landed rather than made a second time. yoyodyne-ifd.433.2 taught that to
// the creation alone; yoyodyne-ifd.433.3 made it the rule for every write it
// covers, and the test that lists which write is in which class
// (TestEveryTrackerOperationIsClassifiedForRetry) is what stops a new one being
// added here without anybody deciding which it is.
//
// It is wrapped once, where the session opens, rather than at each of the thirty
// call sites, so no call site can opt out and no later one can forget. And every
// wait is recorded on the conversation's log before it is taken, for the reason a
// run records its own: a turn that took two minutes because the store was
// contended must not read as a turn that took two minutes thinking.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recoveringTracker is the configured tracker with the recovery rule around every
// operation. It holds the session because the record of a wait is the
// conversation's, and so is the sleep a test drives.
type recoveringTracker struct {
	session *Session
	tracker Tracker
}

// recovering wraps a tracker in the rule, and leaves a conversation that has no
// tracker exactly as it was: the nil checks at every call site still answer
// "no work tracker is configured" rather than reaching a wrapper around nothing.
func (s *Session) recovering(tracker Tracker) Tracker {
	if tracker == nil {
		return nil
	}
	return recoveringTracker{session: s, tracker: tracker}
}

func (r recoveringTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	return recoveringTrackerValue(ctx, r.session, func(ctx context.Context) (beads.WorkItem, error) {
		return r.tracker.Show(ctx, id)
	})
}

func (r recoveringTracker) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	return recoveringTrackerValue(ctx, r.session, func(ctx context.Context) ([]beads.WorkItem, error) {
		return r.tracker.List(ctx, status)
	})
}

// Create is not safe to ask for twice. An item created again is a second item,
// and on 2026-09-24 that is how yoyodyne-ifd.428.21 and 428.22 came from one
// admission: the `bd create` that made 428.21 was killed at the timeout after the
// store had taken it, and the retry made 428.22. What the tracker is asked first
// is whether it now holds the item the failed attempt would have made.
func (r recoveringTracker) Create(ctx context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	return recoveringTrackerWrite(ctx, r.session, "creation",
		func(ctx context.Context) (beads.WorkItem, bool, error) { return r.landedCreation(ctx, item) },
		func(ctx context.Context) (beads.WorkItem, error) { return r.tracker.Create(ctx, item) })
}

// landedCreation finds the item a failed creation left behind, if it left one.
// An item is taken for it only where it carries everything the creation wrote
// that identifies it — the title, the parent, and the notes, which name the
// conversation and the turn that asked — so an item somebody admitted earlier
// under the same title is never mistaken for this one. The parent is read the
// way every other reader of decomposition reads it, from the field or from the
// parent-child edge, so a listing that states it only as the edge still matches.
// That bd's own listing carries the field is pinned against a capture of it in
// internal/beads (TestACapturedListingStatesParentageAsAnEdgeAttributedToTheChild),
// and that it gives back the title, the parent, and multi-line notes byte for
// byte is pinned against bd itself (TestListedNotesConformance), without which
// this match could stop firing with nothing failing.
func (r recoveringTracker) landedCreation(ctx context.Context, item beads.NewWorkItem) (beads.WorkItem, bool, error) {
	held, err := r.tracker.List(ctx, "")
	if err != nil {
		return beads.WorkItem{}, false, err
	}
	title := strings.TrimSpace(item.Title)
	parent := strings.TrimSpace(item.Parent)
	notes := strings.TrimSpace(item.Notes)
	for _, candidate := range held {
		if strings.TrimSpace(candidate.Title) == title &&
			candidate.DecomposedFrom() == parent &&
			strings.TrimSpace(candidate.Notes) == notes {
			return candidate, true, nil
		}
	}
	return beads.WorkItem{}, false, nil
}

// Update is not safe to repeat where it appends a note, which nearly every
// conversation's update does, because the note is what it adds. Everything else
// an update carries — a title, a priority, a parking, a parent, a label added or
// taken off — is set to a value, so an update that appends nothing is asked for
// again as it stands. One that does is asked for again only once the item's
// notes are read and found not to end with the note: bd applies the whole update
// in one invocation, so the note there is the whole update there.
func (r recoveringTracker) Update(ctx context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	return recoveringTrackerWrite(ctx, r.session, "update",
		func(ctx context.Context) (beads.WorkItem, bool, error) {
			return r.landedNote(ctx, id, change.AppendNotes)
		},
		func(ctx context.Context) (beads.WorkItem, error) { return r.tracker.Update(ctx, id, change) })
}

// Block and Unblock set a status and append the reason for it in one
// invocation, so they are checked the way an update's note is.
func (r recoveringTracker) Block(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	return recoveringTrackerWrite(ctx, r.session, "block",
		func(ctx context.Context) (beads.WorkItem, bool, error) { return r.landedNote(ctx, id, reason) },
		func(ctx context.Context) (beads.WorkItem, error) { return r.tracker.Block(ctx, id, reason) })
}

func (r recoveringTracker) Unblock(ctx context.Context, id, note string) (beads.WorkItem, error) {
	return recoveringTrackerWrite(ctx, r.session, "clearing of a blocked status",
		func(ctx context.Context) (beads.WorkItem, bool, error) { return r.landedNote(ctx, id, note) },
		func(ctx context.Context) (beads.WorkItem, error) { return r.tracker.Unblock(ctx, id, note) })
}

// landedNote reads whether an item's notes end with a note a failed write was
// appending. The end rather than anywhere, because notes are only appended to:
// the same words earlier in them are an older write. A write that appends
// nothing has nothing to find and is reported as not landed without a read,
// which is what asks it again as it stands.
//
// What this cannot tell apart is the failed write from an identical note
// appended immediately before it. A conversation's notes name the conversation
// and the turn that wrote them, so that is two writes of one note from one turn,
// and reading it as landed errs toward the single copy.
func (r recoveringTracker) landedNote(ctx context.Context, id, note string) (beads.WorkItem, bool, error) {
	if strings.TrimSpace(note) == "" {
		return beads.WorkItem{}, false, nil
	}
	item, err := r.tracker.Show(ctx, id)
	if err != nil {
		return beads.WorkItem{}, false, err
	}
	return item, beads.NotesEndWith(item.Notes, note), nil
}

// A dependency linked or unlinked again leaves the tracker as the first attempt
// did, so these are asked for again as they stand.
func (r recoveringTracker) AddBlocker(ctx context.Context, id, blockerID string) error {
	return r.session.recoveringTrackerCall(ctx, func(ctx context.Context) error {
		return r.tracker.AddBlocker(ctx, id, blockerID)
	})
}

func (r recoveringTracker) RemoveBlocker(ctx context.Context, id, blockerID string) error {
	return r.session.recoveringTrackerCall(ctx, func(ctx context.Context) error {
		return r.tracker.RemoveBlocker(ctx, id, blockerID)
	})
}

// Complete is checked rather than repeated. A close asked for again leaves the
// item closed either way, but what it answers need not be the same: a close the
// store took is followed by a `bd close` on an item that is already closed, and
// that is a different request from the one that failed. Every conversation that
// closes an item has read it open first and refuses to close work that has
// already left the backlog, so an item that now reads as closed is this close
// having landed.
func (r recoveringTracker) Complete(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	return recoveringTrackerWrite(ctx, r.session, "close",
		func(ctx context.Context) (beads.WorkItem, bool, error) {
			item, err := r.tracker.Show(ctx, id)
			if err != nil {
				return beads.WorkItem{}, false, err
			}
			return item, strings.TrimSpace(item.Status) == "closed", nil
		},
		func(ctx context.Context) (beads.WorkItem, error) { return r.tracker.Complete(ctx, id, reason) })
}

// recoveringTrackerWrite is recoveringTrackerValue around a write that is not
// safe to repeat. Every attempt after the first begins by asking whether the one
// before it landed, and a write that did is answered with what the tracker now
// holds rather than asked for again. A write nothing could settle is not asked
// for again either: the read's own failure goes through the same rule, so a store
// that answers on the next attempt is asked both questions again then.
func recoveringTrackerWrite[T any](ctx context.Context, s *Session, write string, landed func(context.Context) (T, bool, error), attempt func(context.Context) (T, error)) (T, error) {
	attempted := false
	return recoveringTrackerValue(ctx, s, func(ctx context.Context) (T, error) {
		if attempted {
			value, found, err := landed(ctx)
			if err != nil {
				var zero T
				return zero, fmt.Errorf("could not tell whether the %s that failed reached the tracker, so it is not asked for again: %w", write, err)
			}
			if found {
				return value, nil
			}
		}
		attempted = true
		return attempt(ctx)
	})
}

// recoveringTrackerValue is recoveringTrackerCall around a call that answers with
// something. The value the last attempt produced is kept whichever way it went,
// as the run's recoveringValue keeps it.
func recoveringTrackerValue[T any](ctx context.Context, s *Session, attempt func(context.Context) (T, error)) (T, error) {
	var value T
	err := s.recoveringTrackerCall(ctx, func(ctx context.Context) error {
		var attemptErr error
		value, attemptErr = attempt(ctx)
		return attemptErr
	})
	return value, err
}

// recoveringTrackerCall runs one tracker call and asks it again while it goes on
// failing recoverably. Anything else — an answer, or a failure whose class says
// the next attempt earns the identical one — is returned exactly as the tracker
// produced it, which is what keeps this a wait around the existing behavior
// rather than a second opinion about it. A `bd` the harness stopped waiting on
// is in the first class, which is the whole of what yoyodyne-ifd.142 met.
func (s *Session) recoveringTrackerCall(ctx context.Context, attempt func(context.Context) error) error {
	// A recurring pass has already named itself on ctx, which says more than
	// the conversation the pass's turn is taken in.
	if beads.Caller(ctx) == "" {
		ctx = beads.OnBehalfOf(ctx, "the "+s.state.Role.Title()+"'s conversation")
	}
	for {
		err := attempt(ctx)
		if err == nil || !recovery.Recoverable(err) {
			return err
		}
		retried, recordErr := s.recoverTrackerFrom(ctx, err)
		if recordErr != nil {
			return errors.Join(err, recordErr)
		}
		if !retried {
			return s.trackerRetriesExhausted(err)
		}
	}
}

// recoverTrackerFrom records one recoverable failure and waits the interval its
// place in the backoff earns. It reports false when there is no wait left to
// take — the message's window is spent, or the turn itself is ending — which is
// what hands the failure back to the caller to report the way it always
// reported it.
//
// The record is written before the wait, not after it, as a run's is. A
// conversation is not resumed mid-turn the way a run is, so the reason is not a
// restart: it is that the wait is visible while it is being taken, in the event
// log and on the operator's screen, rather than only once something has come of
// it.
func (s *Session) recoverTrackerFrom(ctx context.Context, cause error) (bool, error) {
	// A wait the turn's ending cut short closes the window for the rest of the
	// message. The check is here rather than on the context alone because the
	// call that follows a failed write — the settling read — runs under a context
	// nothing can cancel, exactly so it reaches the tracker after the turn's own
	// deadline; a wait taken there would be one the operator had already stopped
	// and could not stop again.
	if s.trackerWaitStopped {
		return false, nil
	}
	attempt := s.trackerRetryAttempts() + 1
	delay := recovery.Interval(attempt)
	if s.trackerRetryWaited()+delay > recovery.Window {
		return false, nil
	}
	now := s.options.clock().Now()
	retry := runstate.Retry{
		Boundary:     runstate.RetryTracker,
		Attempt:      attempt,
		DelaySeconds: int64(delay / time.Second),
		At:           now,
		Failure:      singleLine(cause.Error(), runstate.MaxRetryFailureBytes),
	}
	s.trackerRetries = append(s.trackerRetries, retry)
	if err := s.emit(execution.EventTrackerRetried, map[string]any{
		"turn":          s.state.Turns,
		"role":          string(s.state.Role),
		"boundary":      retry.Boundary,
		"attempt":       retry.Attempt,
		"delay_seconds": retry.DelaySeconds,
		"failure":       retry.Failure,
	}); err != nil {
		return false, fmt.Errorf("record the wait before asking the tracker again: %w", err)
	}
	// Where somebody is watching, this is what stops a turn waiting out a
	// contended store from looking exactly like a turn that has hung. The phase
	// the turn was in is put back once the wait is over, because the call is
	// then being made again rather than waited on.
	phase := s.activity.current()
	s.activity.doing(describeTrackerWait(attempt, delay, now.Add(delay)))
	err := s.options.sleep(ctx, delay)
	// The phase is put back either way: the wait is over, and what the turn does
	// next — the call again, or the recording of how it ended — is not waiting.
	if phase != "" {
		s.activity.doing(phase)
	}
	if err != nil {
		// The turn is over — the operator stopped it, or it ran out of time — so the
		// call is left as it failed rather than asked again under a context that has
		// already ended, and so is every call after it in this message.
		s.trackerWaitStopped = true
		return false, nil
	}
	return true, nil
}

// trackerRetriesExhausted is what a call that went on failing recoverably
// produces. It wraps the failure rather than replacing it, so everything
// downstream — the outcome's failure line, the settling of what a timed-out
// write left behind, the results the role is handed — reads as what it always
// read, with the attempts and the time in front of it. A window the turn's
// ending closed is said as that rather than as one that ran out, because the
// two ask different things of whoever reads it: one says the store was down for
// two hours, the other that somebody stopped waiting.
func (s *Session) trackerRetriesExhausted(cause error) error {
	attempts := s.trackerRetryAttempts()
	waited := s.trackerRetryWaited().Round(time.Second)
	if s.trackerWaitStopped {
		return fmt.Errorf("%s failed on something a later attempt could have survived, and the turn was stopped while waiting to ask again, after %d retr(ies) over %s, so it is reported rather than retried further: %w",
			runstate.RetryTracker, attempts, waited, cause)
	}
	if attempts == 0 {
		// Nothing was waited at all, which is a turn that ended under the call
		// rather than a window that ran out. Saying it was retried would be untrue.
		return cause
	}
	return fmt.Errorf("%s kept failing on something a later attempt could have survived, and %d retr(ies) over %s did not outlast it, so it is reported rather than retried further: %w",
		runstate.RetryTracker, attempts, waited, cause)
}

// trackerRetryAttempts is how many times this message has already asked the
// tracker again, and trackerRetryWaited how much of its window it has committed.
// Both are counted when a wait is committed rather than as it elapses, for the
// reason the usage limit's budget is: a wait interrupted part way must not buy
// the message a fresh window by forgetting it.
func (s *Session) trackerRetryAttempts() int {
	return len(s.trackerRetries)
}

func (s *Session) trackerRetryWaited() time.Duration {
	var waited time.Duration
	for _, retry := range s.trackerRetries {
		waited += retry.Delay()
	}
	return waited
}

// trackerRetriesSince is what has been waited out since a point in the record,
// which is how one action learns which of the message's waits were its own.
func (s *Session) trackerRetriesSince(from int) []runstate.Retry {
	if from < 0 || from > len(s.trackerRetries) {
		return nil
	}
	return s.trackerRetries[from:]
}

// describeTrackerWait says what the turn is waiting on and until when, in the
// operator's language. The moment it names is when the tracker is asked again,
// which is what somebody watching wants to know.
func describeTrackerWait(attempt int, delay time.Duration, asksAgainAt time.Time) string {
	return fmt.Sprintf("waiting out a tracker failure a later attempt may survive; asking again at %s (attempt %d, after %s)",
		asksAgainAt.Local().Format(time.Kitchen), attempt, delay)
}

// retriedClause is what one line about an action says about the waits it took.
// It is said exactly when there were some, for the reason the target's state is
// said only when it is not open: an action that waited is the one case that
// matters, and a clause on every action would bury it.
func retriedClause(retries []runstate.Retry) string {
	if len(retries) == 0 {
		return ""
	}
	var waited time.Duration
	for _, retry := range retries {
		waited += retry.Delay()
	}
	return fmt.Sprintf("; the tracker was asked %d further time(s) over %s first, after a failure a later attempt could survive",
		len(retries), waited.Round(time.Second))
}

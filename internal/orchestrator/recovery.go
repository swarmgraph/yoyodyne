package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run meets the network at its most expensive moments. The publish step, the
// remote checks the promotion is made against, the merge request, and every
// provider invocation all go over somebody else's connection, and until
// yoyodyne-ifd.264 a single reset at any of them ended the run: on 2026-09-03
// four runs died that way, each at its final publish or integrate step, each
// with the work already completed and some of it already reviewed, and the
// intake brake then held the whole line three times over.
//
// None of those failures judged anything. So a boundary that fails on a class
// nobody has to argue about — the connection reset, the network drop, the
// transport-level refusal internal/recovery recognizes — is waited out on a
// Fibonacci backoff and asked again, and only a run that has spent the whole of
// that boundary's window records the failure it would have recorded at once
// before.
//
// Two properties matter more than the retrying. Every wait is recorded durably
// before it is taken, so a process that dies mid-wait comes back to what it had
// spent rather than to a fresh window, and so an operator reading a run that
// succeeded can see how close it came. And exhausting the window escalates
// rather than going quiet: what the boundary would have produced is produced,
// with the attempts and the intervals named in front of it.

// recovering runs one boundary and asks it again while it goes on failing
// recoverably. Anything else — an answer, or a failure whose class says the next
// attempt earns the identical one — is returned exactly as the boundary produced
// it, which is what keeps this a wait around the existing behavior rather than a
// second opinion about it.
func (a *activeRun) recovering(ctx context.Context, boundary string, attempt func(context.Context) error) error {
	for {
		err := attempt(ctx)
		if err == nil || !recovery.Recoverable(err) {
			return err
		}
		retried, recordErr := a.recoverFrom(ctx, boundary, err)
		if recordErr != nil {
			return errors.Join(err, recordErr)
		}
		if !retried {
			return a.exhausted(boundary, err)
		}
	}
}

// recoveringValue is recovering around a boundary that answers with something.
// The value the last attempt produced is kept whichever way it went, because
// several of these report something worth having beside their failure — the
// harness commit a push made before the push itself failed, first among them.
func recoveringValue[T any](ctx context.Context, a *activeRun, boundary string, attempt func(context.Context) (T, error)) (T, error) {
	var value T
	err := a.recovering(ctx, boundary, func(ctx context.Context) error {
		var attemptErr error
		value, attemptErr = attempt(ctx)
		return attemptErr
	})
	return value, err
}

// recoveringTrackerWrite is recoveringValue around a tracker write that is not
// safe to make twice: a note appended, a claim, a close, a reopening. The tracker
// client already reads a write its bound killed back before it makes it again
// (internal/beads, writeItem), but a write it gave up on is one whose landing it
// could not establish, and asking for it again here as it stands is the note
// twice — the more so because a run's outcome notes are rendered afresh on every
// attempt, with the wait just taken among them, so the second copy would not
// even read the same.
//
// So every attempt after the first begins by asking whether the one before it
// landed, and one that did is answered with what the tracker now holds rather
// than made again. A read that cannot be made goes through the same window, so a
// store that answers later is asked both questions then. It is the rule a
// conversation's writes already follow (internal/chat, recoveringTrackerWrite),
// over the run's own record of its waits.
//
// A write still unanswered once the window is spent does not end the run: it is
// waited on for as long as the tracker takes (waitOnTracker).
func recoveringTrackerWrite[T any](ctx context.Context, a *activeRun, landed func(context.Context) (T, bool, error), attempt func(context.Context) (T, error)) (T, error) {
	attempted := false
	ask := func(ctx context.Context) (T, error) {
		if attempted {
			value, found, err := landed(ctx)
			if err != nil {
				var zero T
				return zero, fmt.Errorf("could not tell whether the tracker write that failed landed, so it was not made again: %w", err)
			}
			if found {
				return value, nil
			}
		}
		attempted = true
		return attempt(ctx)
	}
	value, err := recoveringValue(ctx, a, runstate.RetryTrackerWrite, ask)
	var spent runstate.StopError
	if err == nil || !errors.As(err, &spent) || spent.Class != runstate.StopRecoveryWindow || ctx.Err() != nil {
		return value, err
	}
	return waitOnTracker(ctx, a, err, ask)
}

// waitOnTracker is a run whose tracker write went unanswered through its whole
// window, waiting on the tracker rather than ending.
//
// The wait is recorded on the run before it is taken — the boundary, what it
// cost, and the tracker's last words — so every surface shows the run parked on
// the tracker rather than hung, and then the write is asked again, read back
// first as every attempt after the first is, once every longest interval of the
// backoff until the tracker answers. An answer lifts the park and the run goes
// on from the step that was waiting. Only the run ending, a person asking it to
// stop, or a tracker that answers with a refusal ends the wait.
//
// It waits here, in the process holding the run, rather than parking and
// exiting as a gate's unanswered read does. The writes this covers are a run's
// claim, made before it has a worktree to resume in, and the writes it finishes
// with, which read the outcome this process holds of the review and the
// promotion — neither is a point a later process could take the run up from
// its record alone. So a park on this boundary is never offered to a later
// process to resume (pausedForTracker), and a process that dies in it leaves an
// interrupted run for reconciliation to settle, as a run that died at either
// step always has.
func waitOnTracker[T any](ctx context.Context, a *activeRun, spent error, ask func(context.Context) (T, error)) (T, error) {
	var zero T
	paused, err := a.noteTrackerPause(runstate.RetryTrackerWrite, spent)
	if err != nil {
		return zero, errors.Join(spent, err)
	}
	for {
		if err := a.pipeline.sleep(ctx, recovery.MaxInterval); err != nil {
			return zero, spent
		}
		if err := a.stopRequested(); err != nil {
			return zero, err
		}
		value, err := ask(ctx)
		if err == nil {
			// The write is made, so what is left is the record saying so. A store that
			// cannot take that has the park cleared on this process all the same, and
			// the next save of the run carries it.
			_ = a.clearTrackerPause()
			return value, nil
		}
		if !recovery.Recoverable(err) || ctx.Err() != nil {
			return value, err
		}
		spent = err
		paused.Attempts++
		paused.WaitedSeconds += int64(recovery.MaxInterval / time.Second)
		paused.Failure = boundedFailureDetail(err.Error())
		a.state.TrackerPause = &paused
		a.state.UpdatedAt = a.pipeline.clock().Now()
		if saveErr := a.pipeline.Store.Save(a.state); saveErr != nil {
			return zero, errors.Join(err, fmt.Errorf("record the wait on the tracker: %w", saveErr))
		}
	}
}

// itemLanded is the landed question for a write to one item: the item read
// back, and whether it carries what the write made.
func itemLanded(tracker WorkTracker, id string, carries func(beads.WorkItem) bool) func(context.Context) (beads.WorkItem, bool, error) {
	return func(ctx context.Context) (beads.WorkItem, bool, error) {
		item, err := tracker.Show(ctx, id)
		if err != nil {
			return beads.WorkItem{}, false, err
		}
		return item, carries(item), nil
	}
}

// recordOutcomeOnce appends a note rendered by render under the rule above. The
// note is rendered on each attempt, so it carries every wait the run has taken,
// and the read-back looks for the one the failed attempt was appending.
func (a *activeRun) recordOutcomeOnce(ctx context.Context, render func() string) error {
	var note string
	_, err := recoveringTrackerWrite(ctx, a,
		itemLanded(a.pipeline.Tracker, a.state.WorkItemID, func(item beads.WorkItem) bool { return beads.NotesEndWith(item.Notes, note) }),
		func(ctx context.Context) (beads.WorkItem, error) {
			note = render()
			return a.pipeline.Tracker.RecordOutcome(ctx, a.state.WorkItemID, note)
		})
	return err
}

// createWorktree cuts the run's worktree, and waits out a tracker export that
// did not answer while the worktree was being prepared.
//
// The export is the one step of preparing a worktree that reaches the tracker,
// and under load it stalls as a tracker write does. A creation that failed there
// has made the worktree and given it nothing: no developer has been started, and
// a change an earlier run preserved for this item has not been lifted into it.
// So it is asked again on the run's own window, each wait recorded before it is
// taken, as a resumed creation. That adopts the worktree already made only while
// it is still unchanged at its base, prepares the export again, and gives the
// worktree its copy only once the export answered whole, so no developer is
// started on an export nobody confirmed. Asking again is safe because the export
// changes nothing (gitworktree.ErrExportsUnprepared), and it is not a new attempt
// at the work: the claim stays the one this run took, and no repair or re-run is
// spent repeating the step.
//
// A run that spends the window is stopped with what it was doing, what it
// leaves, and what picks it up again said in its failure. Every other creation
// failure is returned exactly as before.
func (a *activeRun) createWorktree(ctx context.Context, request gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	var made gitworktree.Worktree
	for {
		worktree, err := a.pipeline.Worktrees.Create(ctx, request)
		if err == nil {
			return worktree, nil
		}
		if worktree.Path != "" {
			made = worktree
		}
		if !errors.Is(err, gitworktree.ErrExportsUnprepared) || !exportUnanswered(err) {
			return made, err
		}
		// On the record before the wait, so a process that dies in it leaves a run
		// whose worktree reconciliation can find.
		if made.Path != "" {
			a.recordWorktree(made)
		}
		retried, recordErr := a.recoverFrom(ctx, runstate.RetryTrackerExport, err)
		if recordErr != nil {
			return made, errors.Join(err, recordErr)
		}
		if !retried {
			return made, a.exportStopped(made, err)
		}
		request.ResumeCreation = true
	}
}

// exportUnanswered reports an export failure that answered nothing about the
// store: a `bd export` killed at its bound or never completed, or one that
// never started because another export held the lock past the same bound.
func exportUnanswered(err error) bool {
	return recovery.Recoverable(err) || errors.Is(err, beads.ErrExportBusy)
}

// exportStopped is the failure a run stops with once the export has not
// answered through the whole window, or the run ended while it waited.
func (a *activeRun) exportStopped(made gitworktree.Worktree, cause error) error {
	attempts := a.state.RetryAttempts(runstate.RetryTrackerExport)
	waited := a.state.RetryWaited(runstate.RetryTrackerExport)
	left := "No worktree was left"
	if made.Path != "" {
		left = fmt.Sprintf("The worktree at %s on branch %s was made at its base and holds nothing of the work", made.Path, made.Branch)
	}
	if lift := a.state.Selection; lift != nil && lift.Lift != nil {
		left += fmt.Sprintf(", and the change run %s preserved on %s was not lifted and stays on that branch", lift.Lift.RunID, lift.Lift.Branch)
	}
	return fmt.Errorf("the run stopped while preparing its worktree, before any developer started: the tracker's export did not answer on %d wait(s) over %s. %s. Nothing about the work was judged, so nothing is charged to the item for this round. The claim is given back — by the re-run that started this run, which keeps its re-run, or otherwise by the harness's audit of claims — and the next run of the item prepares a worktree of its own: %w",
		attempts, waited.Round(time.Second), left, cause)
}

// readWorkItem is the same rule at the one boundary that has no run to record
// on: the tracker read a dispatch makes before it claims an item or adopts a run
// in flight, which is where what the item waits on and what the operator has
// directed are settled. A `bd` killed under load there turned a dispatch away
// outright, and for a resume that is a run left in flight that nothing picked up
// until somebody asked for it again.
//
// Nothing is recorded against a run, because there is none to record it on: no
// run has been reserved, and the run a resume is about belongs to whichever
// process holds its lease rather than to this one. What a run's record buys that
// matters here — a window a restarted process does not spend again — costs
// nothing to give up: a dispatch that dies mid-window claimed nothing and left
// nothing behind, so a fresh window buys one more attempt rather than a wait
// somebody has already paid for. The window and the intervals are the same, so
// the store gets asked the same way wherever a run meets it.
//
// What is recorded is that the wait is being taken, where a watch session started
// the dispatch: each wait goes onto the session's watch log before it is slept,
// with the boundary, the attempt it precedes, and the failure it waits out. Up to
// two hours of a held slot with nothing written anywhere read on every surface
// exactly as a hung process does, which is what yoyodyne-ifd.428.14 ended. A
// dispatch nobody's session started — `yoyo run` at a terminal — has somebody
// watching it already, and records nothing.
func (p Pipeline) readWorkItem(ctx context.Context, workItemID string) (beads.WorkItem, error) {
	var waited time.Duration
	for attempt := 1; ; attempt++ {
		item, err := p.Tracker.Show(ctx, workItemID)
		if err == nil || !recovery.Recoverable(err) {
			return item, err
		}
		delay := recovery.Interval(attempt)
		if waited+delay > recovery.Window {
			return item, fmt.Errorf("the tracker kept failing on something a later attempt could have survived, and %d retr(ies) over %s did not outlast it: %w",
				attempt-1, waited.Round(time.Second), err)
		}
		dispatchWaited(ctx, runstate.DispatchWait{
			WorkItemID:   workItemID,
			Boundary:     runstate.RetryDependencyRead,
			Attempt:      attempt,
			DelaySeconds: int64(delay / time.Second),
			At:           p.clock().Now(),
			Failure:      boundedFailureDetail(err.Error()),
		})
		if sleepErr := p.sleep(ctx, delay); sleepErr != nil {
			// The dispatch is over — cancelled, or out of time — so the read is left
			// as it failed rather than asked again under a context that has ended.
			return item, err
		}
		waited += delay
	}
}

// dispatchWaitsKey carries, on a dispatch's context, where the watch session that
// started it records a wait the dispatch takes before it has claimed anything.
type dispatchWaitsKey struct{}

// withDispatchWaits is a dispatch's context carrying where its waits are
// recorded. It travels on the context rather than on the pipeline because the
// session that starts a dispatch is the scheduler and the pipeline is built by
// whoever wired the start, and the context is the one thing both hand along.
func withDispatchWaits(ctx context.Context, record func(runstate.DispatchWait)) context.Context {
	return context.WithValue(ctx, dispatchWaitsKey{}, record)
}

// dispatchWaited records one pre-claim wait where the dispatch's session asked
// for them, and nowhere where it did not. Recording is the session's to do and
// never fails the dispatch: a wait that could not be written costs its
// visibility, not the work.
func dispatchWaited(ctx context.Context, wait runstate.DispatchWait) {
	if record, wired := ctx.Value(dispatchWaitsKey{}).(func(runstate.DispatchWait)); wired && record != nil {
		record(wait)
	}
}

// recoverProvider decides what a provider death does once the relaunch budget is
// spent. That budget bounds how much of the provider's weather one run absorbs,
// and it is the right bound for weather nobody can classify; a death that is
// plainly a dropped connection is not weather in that sense, and stopping on it
// is exactly what cost four runs their work. So the budget is spent first, and
// then this carries on past it for as long as the failure class stays clearly
// recoverable and the window has room.
//
// It reports false for everything else, which leaves the run blocking on the
// spent budget exactly as it always did — including for a death whose class this
// build does not recognize, which is the conservative half of the decision.
func (a *activeRun) recoverProvider(ctx context.Context, failure backend.TransientFailure) (bool, error) {
	if !recovery.RecoverableDetail(failure.Detail) {
		return false, nil
	}
	return a.recoverFrom(ctx, runstate.RetryProviderInvocation, errors.New(failure.Detail))
}

// recoverFrom records one recoverable failure and waits the interval its place
// in the backoff earns. It reports false when there is no wait left to take —
// the boundary's window is spent, or the run itself is ending — which is what
// hands the failure back to the caller to report the way it always reported it.
//
// The record is written before the wait, not after it. That is the same
// discipline every counter here follows and it is load-bearing for the same
// reason: a process that dies in the middle of a half-hour wait must come back
// to the window it had already spent rather than to a whole one, or a boundary
// that keeps failing across a restart would retry without bound.
func (a *activeRun) recoverFrom(ctx context.Context, boundary string, cause error) (bool, error) {
	attempt := a.state.RetryAttempts(boundary) + 1
	delay := recovery.Interval(attempt)
	if a.state.RetryWaited(boundary)+delay > recovery.Window {
		return false, nil
	}
	if err := a.recordRetry(boundary, attempt, delay, cause); err != nil {
		return false, err
	}
	if err := a.pipeline.sleep(ctx, delay); err != nil {
		// The run is over — cancelled, or out of time — so the boundary is left as
		// it failed rather than asked again under a context that has already ended.
		return false, nil
	}
	return true, nil
}

// recordRetry appends one waited-out failure to the run's record.
func (a *activeRun) recordRetry(boundary string, attempt int, delay time.Duration, cause error) error {
	retry := runstate.Retry{
		Boundary:     boundary,
		Attempt:      attempt,
		DelaySeconds: int64(delay / time.Second),
		At:           a.pipeline.clock().Now(),
		Failure:      boundedFailureDetail(cause.Error()),
	}
	a.state.Retries = append(a.state.Retries, retry)
	a.state.UpdatedAt = retry.At
	a.outcome.Retries = a.state.Retries
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the wait before asking again after %s failed: %w", boundary, err)
	}
	return nil
}

// exhausted is what a boundary that went on failing recoverably produces. It
// wraps the failure rather than replacing it, so everything downstream — an
// outstanding publication, a blocker on the item, a reported failure — reads as
// what it always read, with the attempts and the time in front of it. That is
// the escalation: a run handed to a person says the network was retried and for
// how long, rather than reporting the last reset as though it were the first.
func (a *activeRun) exhausted(boundary string, cause error) error {
	attempts := a.state.RetryAttempts(boundary)
	waited := a.state.RetryWaited(boundary)
	if attempts == 0 {
		// Nothing was waited at all, which is a run that ended under the boundary
		// rather than a window that ran out. Saying it was retried would be untrue.
		return cause
	}
	return runstate.StopError{Class: runstate.StopRecoveryWindow, Cause: fmt.Errorf("%s kept failing on something a later attempt could have survived, and %d retr(ies) over %s did not outlast it, so it is handed to a person rather than retried further: %w",
		boundary, attempts, waited.Round(time.Second), cause)}
}

// recovering is the same rule where a sweep meets the network rather than where
// a run does. A settlement finishes the publication its run could not, so it
// reaches the same forge over the same connection, and until yoyodyne-ifd.301 a
// single reset there was recorded as an outstanding publication at once — twice
// on yoyodyne-ifd.295, on the one step that was left.
//
// It is used at that step and at the settlement of an item whose change has
// landed, and no other. A sweep settles its runs one at a time under each one's
// lease, so a window waited out here holds up every run behind it; that is worth
// paying where the alternative is a leftover only a person can remove, or a
// landed change whose item stays open and is pulled again as ready work, and is
// not worth paying for a reading the next sweep takes again.
//
// It waits on the boundary's own window, which is durable on the run, so a sweep
// that takes up where a previous one left off spends what is left rather than a
// whole window again. The record is written before the wait for the reason the
// run's is, and the state the caller holds is advanced with it: this is the
// record the settlement goes on to save.
func (r Reconciler) recovering(ctx context.Context, state *runstate.State, boundary string, attempt func(context.Context) error) error {
	for {
		err := attempt(ctx)
		if err == nil || !recovery.Recoverable(err) {
			return err
		}
		waited := state.RetryAttempts(boundary)
		delay := recovery.Interval(waited + 1)
		if state.RetryWaited(boundary)+delay > recovery.Window {
			return reconcilerExhausted(*state, boundary, err)
		}
		at := r.clock().Now()
		state.Retries = append(state.Retries, runstate.Retry{
			Boundary:     boundary,
			Attempt:      waited + 1,
			DelaySeconds: int64(delay / time.Second),
			At:           at,
			Failure:      boundedFailureDetail(err.Error()),
		})
		state.UpdatedAt = at
		if saveErr := r.Store.Save(*state); saveErr != nil {
			return errors.Join(err, fmt.Errorf("record the wait before asking again after %s failed: %w", boundary, saveErr))
		}
		if sleepErr := r.sleep(ctx, delay); sleepErr != nil {
			// The sweep is over — cancelled, or out of time — so the boundary is left
			// as it failed rather than asked again under a context that has ended.
			return err
		}
	}
}

// reconcilerExhausted is activeRun.exhausted for a sweep: the same words, over
// the record the sweep is holding rather than the one a run owns.
func reconcilerExhausted(state runstate.State, boundary string, cause error) error {
	attempts := state.RetryAttempts(boundary)
	waited := state.RetryWaited(boundary)
	if attempts == 0 {
		return cause
	}
	return fmt.Errorf("%s kept failing on something a later attempt could have survived, and %d retr(ies) over %s did not outlast it, so it is handed to a person rather than retried further: %w",
		boundary, attempts, waited.Round(time.Second), cause)
}

// sleep waits out one interval, cut short by a cancelled context so a shutdown
// is never held up by a wait the backoff has grown to half an hour.
func (r Reconciler) sleep(ctx context.Context, duration time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// renderRetryNotes carries the recoverable failures a run waited out onto the
// work item, one line per boundary with the attempts, the intervals, and the
// last failure that was waited out.
//
// It is per boundary rather than per retry deliberately. Twenty lines saying a
// connection was reset is an outage said twenty times, and what a reader of the
// item needs is that a boundary was retried, how long for, and what it was
// failing on; the run's own record holds each one whole for whoever wants them.
func renderRetryNotes(outcome Outcome) []string {
	if len(outcome.Retries) == 0 {
		return nil
	}
	var order []string
	byBoundary := map[string][]runstate.Retry{}
	for _, retry := range outcome.Retries {
		if _, seen := byBoundary[retry.Boundary]; !seen {
			order = append(order, retry.Boundary)
		}
		byBoundary[retry.Boundary] = append(byBoundary[retry.Boundary], retry)
	}
	lines := make([]string, 0, len(order))
	for _, boundary := range order {
		retries := byBoundary[boundary]
		intervals := make([]string, 0, len(retries))
		var waited time.Duration
		for _, retry := range retries {
			intervals = append(intervals, retry.Delay().String())
			waited += retry.Delay()
		}
		lines = append(lines, fmt.Sprintf("Waited out a recoverable failure while %s: %d retr(ies) over %s, waiting %s; last failure: %s",
			boundary, len(retries), waited.Round(time.Second), strings.Join(intervals, ", "), retries[len(retries)-1].Failure))
	}
	return lines
}

// boundedFailureDetail keeps one recorded failure inside the bound the run state
// holds retries to, cut on a rune boundary so what is kept stays text.
func boundedFailureDetail(detail string) string {
	if len(detail) <= runstate.MaxRetryFailureBytes {
		return detail
	}
	cut := runstate.MaxRetryFailureBytes
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut]
}

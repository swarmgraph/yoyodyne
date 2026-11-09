package orchestrator

// An approved change waiting at its merge step for GitHub to be reachable again.
//
// The operator's rule is that a run is never ended for a flaky network: a
// dropped connection is waited out. A boundary's own recovery window
// (recovery.go) ends after two hours and is waited under the promotion lease,
// so at the merge step neither is enough for an outage. Where a step at the
// merge step cannot reach GitHub, the run waits for it instead:
//
//   - reading where the target branch stands on GitHub before landing
//     (settleRemoteTarget), and bringing the local target onto it;
//   - checking the target again and asking GitHub to merge the pull request,
//     for a change that lands through its pull request (publishIntegration).
//
// The step gives up the promotion lease as it returns — a long wait on the
// remote is never made holding it — and the run records the wait on its record
// (runstate.ForgeWait), which gives back its developer slot and lets every
// surface say what it is waiting for and when it next tries. It sleeps on the
// recovery package's paced intervals, which start at a second and grow to half
// an hour, and then makes the whole landing again from the top: the lease, the
// read of the target, the merge. Nothing ends the wait but GitHub answering, the
// operator asking the run to stop, or the process hosting it ending. Nothing is
// charged for it — no repair attempt, no review round — and nothing is put to
// the development manager.
//
// A refusal from GitHub about the change itself — a failing check, a conflict,
// a protected branch — is not a network failure, is not waited out here, and is
// handled as it always was. So is a merge request whose connection dropped after
// it was sent: GitHub may have received it, and that is settled by the existing
// short retry and, past it, by the dropped-merge handling, rather than by
// making the landing again from the top.

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// forgeAwayError is a step at the merge step that could not reach GitHub. It
// carries what the run was doing, in the words its landing uses, and what the
// step failed on.
type forgeAwayError struct {
	step  string
	cause error
}

func (e forgeAwayError) Error() string {
	return fmt.Sprintf("GitHub could not be reached while %s: %v", e.step, e.cause)
}

func (e forgeAwayError) Unwrap() error { return e.cause }

// forgeAwayOf reports the step that could not reach GitHub, where that is what
// ended a landing attempt. It reads the step's own failure and not any write the
// run attempted about it afterwards, for the reason integrationStopCauseOf does.
func forgeAwayOf(err error) (forgeAwayError, bool) {
	var away forgeAwayError
	if errors.As(stepCauseOf(err), &away) {
		return away, true
	}
	return forgeAwayError{}, false
}

// awayFromForge wraps a failed read of GitHub at the merge step as a step that
// could not reach it, where its class says so: any failure recovery.Recoverable
// recognises, which by the time it reaches here has either never reached GitHub
// or spent the boundary's own short window. Anything else is returned as it was.
func awayFromForge(step string, err error) error {
	if err == nil || !recovery.Recoverable(err) {
		return err
	}
	return forgeAwayError{step: step, cause: err}
}

// recoveringReachable is recovering for a boundary at the merge step: a failure
// that never reached GitHub is returned at once rather than waited out under the
// promotion lease, so the run can wait for GitHub with the lease given back. A
// connection that dropped mid-flight is still asked again on the boundary's own
// window, as every boundary is.
func (a *activeRun) recoveringReachable(ctx context.Context, boundary string, attempt func(context.Context) error) error {
	return a.recoveringUnless(ctx, boundary, recovery.Unreachable, attempt)
}

// awaitForge records that the run is waiting for GitHub, waits until the next
// try is due, and reports whether the landing may be made again. It returns the
// step's failure where the wait itself ended — the process hosting the run is
// ending — and the operator's stop where one was asked for, so the run ends on
// those exactly as it would have anywhere else.
//
// The wait is written before it is slept, for the reason every wait here is:
// what the record says is what every surface reads, and a run that says it is
// waiting has given its developer slot back.
func (a *activeRun) awaitForge(ctx context.Context, away forgeAwayError) error {
	p := a.pipeline
	now := p.clock().Now().UTC()
	wait := runstate.ForgeWait{Since: now}
	if a.state.ForgeWait != nil {
		wait = *a.state.ForgeWait
	}
	wait.Tries++
	wait.Step = away.step
	wait.Failure = boundedFailureDetail(singleLine(away.cause.Error(), runstate.MaxRetryFailureBytes))
	delay := recovery.Interval(wait.Tries)
	wait.NextTryAt = now.Add(delay)
	recorded := wait
	a.outcome.ForgeWaited = &recorded
	a.landingAt("waiting for GitHub to be reachable again, after it could not be reached while "+away.step, landingReadsRemote)
	// The wait is written as the store's narrow update rather than as this
	// copy of the record, together with the landing taken back off it where
	// unprepareLanding took one back, and the record as written is the run's
	// from here on.
	written, err := p.Store.Update(context.Background(), a.state.RunID, runstate.ForgeWaitRecorded{
		Wait:          wait,
		Unprepared:    a.state.Integration == nil,
		HarnessCommit: a.state.HarnessCommit,
		At:            now,
	})
	if err != nil {
		a.state.ForgeWait = nil
		return withFailedRecord(away, fmt.Errorf("record that the run is waiting for GitHub: %w", err))
	}
	a.state = written
	if err := p.sleep(ctx, delay); err != nil {
		a.state.ForgeWait = nil
		return away
	}
	if err := a.stopRequested(); err != nil {
		a.state.ForgeWait = nil
		return err
	}
	return nil
}

// endForgeWait takes a finished wait off the run's record. It is not saved on
// its own: the next save of the run carries it, which every way a landing
// attempt ends makes. The outcome keeps it, for the notes.
func (a *activeRun) endForgeWait() {
	a.state.ForgeWait = nil
}

// unprepareLanding takes a landing through the pull request back off the
// record, for a run that is going to make the landing again from the top: the
// landing moved no branch, and a run waiting with a promotion on its record
// would read as one whose change had landed. A commit the preparation made is
// kept on the record, as a refused promotion keeps its own, so the next
// preparation recognises it as the harness's.
func (a *activeRun) unprepareLanding() {
	if integration := a.outcome.Integration; integration != nil && integration.SourceCommit != "" && integration.SourceCommit != a.state.HarnessCommit {
		a.recordHarnessCommit(integration.SourceCommit)
	}
	a.outcome.Integration = nil
	a.state.Integration = nil
}

// renderForgeWaitNote is the line the item's notes carry about a wait for
// GitHub the run made and came through: how long it waited and over how many
// tries, so a run that landed late reads as one that waited out GitHub rather
// than as one that was merely slow.
func renderForgeWaitNote(outcome Outcome) string {
	wait := outcome.ForgeWaited
	if wait == nil {
		return ""
	}
	return fmt.Sprintf("Waited for GitHub to be reachable again: it could not be reached while %s; %s from %s before it answered. Nothing was charged to the work for the wait.",
		wait.Step, triesFor(wait.Tries), wait.Since.Local().Format("2006-01-02 15:04 MST"))
}

func triesFor(tries int) string {
	if tries == 1 {
		return "1 failed try"
	}
	return fmt.Sprintf("%d failed tries", tries)
}

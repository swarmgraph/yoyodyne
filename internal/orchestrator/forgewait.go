package orchestrator

// An approved change waiting at its merge step for GitHub to be reachable again.
//
// The operator's rule is that a run is never ended for a flaky network: a
// dropped connection is waited out. A boundary's own recovery window
// (recovery.go) ends after two hours and is waited under the promotion lease,
// so at the merge step neither is enough for an outage. Where a step at the
// merge step cannot reach GitHub, the run waits for it instead, in one of two
// ways depending on whether anything has landed yet.
//
// Before anything is promoted — reading where the target branch stands on
// GitHub and bringing the local target onto it (settleRemoteTarget) — the step
// returns forgeAwayError, which gives up the promotion lease as integrate
// returns, and promoteApproved waits and then makes the whole landing again
// from the top: the lease, the read, the promotion, the merge.
//
// Once the promotion is made — the local target moved, or the landing through
// the pull request prepared — the landing cannot be made again from the top, so
// the step that could not reach GitHub is asked again in place
// (untilGitHubAnswers): the check of the target and the merge request, the
// question whether the pull request merged, and the read that the target
// carries it (publishIntegration). The lease is given back for the wait and
// taken again before the step is asked again, so the fresh read of the target
// and the change to it are still made under the lease and no long wait is made
// holding it. Asking a merge again is safe however its connection failed,
// because the request names the head commit, so a merge GitHub already made is
// not made twice; and the check of the target is made again inside each try, so
// a target that moved during the wait is found as it always is.
//
// Either way the run records the wait (runstate.ForgeWait), which gives back its
// developer slot and lets every surface say what it is waiting for and when it
// next tries, and sleeps on the recovery package's paced intervals, which start
// at a second and grow to half an hour. Nothing ends the wait but GitHub
// answering, the operator asking the run to stop, or the process hosting it
// ending. Nothing is charged for it — no repair attempt, no review round — and
// nothing is put to the development manager.
//
// A refusal from GitHub about the change itself — a failing check, a conflict,
// a protected branch — is not a network failure, is not waited out here, and is
// handled as it always was.
//
// With execution.merge_queue on, the run never reaches any of this: it ends
// once its change is admitted to the queue and never contacts GitHub at its
// merge step. The queue's own passes land the change (mergequeuepromotion.go,
// mergequeuerecovery.go), and how they treat GitHub being away is theirs.

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
	// copy of the record, together with the landing as this process holds it,
	// and the record as written is the run's from here on.
	written, err := p.Store.Update(context.Background(), a.state.RunID, runstate.ForgeWaitRecorded{
		Wait:          wait,
		Integration:   a.state.Integration,
		HarnessCommit: a.state.HarnessCommit,
		At:            now,
	})
	if err != nil {
		a.state.ForgeWait = nil
		return withFailedRecord(away, fmt.Errorf("record that the run is waiting for GitHub: %w", err))
	}
	// This process holds the run, so its copy is the run's account of the
	// promotion under way, including what it has not saved yet — the pull
	// request's merge as it stands among it. Where nothing else wrote to the
	// record since that copy was read, the copy stays the run's with the wait and
	// the new revision on it. Where something did, the record as written is
	// taken, with this process's account of the promotion carried onto it.
	if written.Revision == a.state.Revision+1 {
		a.state.ForgeWait = written.ForgeWait
		a.state.Integration = written.Integration
		a.state.ReplayUnjudged = written.ReplayUnjudged
		a.state.HarnessCommit = written.HarnessCommit
		a.state.UpdatedAt = written.UpdatedAt
		a.state.Revision = written.Revision
	} else {
		held := a.state
		a.state = written
		a.state.PullRequest = held.PullRequest
		a.state.PublishFailure = held.PublishFailure
		a.state.StopClass = held.StopClass
	}
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

// untilGitHubAnswers asks one step of a promotion already made, and asks it
// again in place for as long as it fails in a way waits says is GitHub not
// being reachable. Each wait gives the promotion lease back and takes it again
// before the step is asked again (waitWithoutLease). What it returns is the
// step's own answer once GitHub gives one, which the caller handles as it always
// did. Where the wait itself ends — the process hosting the run is ending, or
// the wait could not be recorded or the lease taken again — the step's last
// failure is returned for the same handling; the operator asking the run to stop
// is returned as their stop, so the run is stopped rather than carrying on.
func (a *activeRun) untilGitHubAnswers(ctx context.Context, step string, waits func(error) bool, ask func(context.Context) error) error {
	// The landing step the caller noted is noted again after each wait, so a
	// stop after it names the step rather than the wait.
	noted := a.outcome.LandingStep
	for {
		a.outcome.LandingStep = noted
		err := ask(ctx)
		if err == nil || !waits(err) {
			a.endForgeWait()
			return err
		}
		if waitErr := a.waitWithoutLease(ctx, forgeAwayError{step: step, cause: err}); waitErr != nil {
			a.endForgeWait()
			var stopped operatorStop
			if errors.As(waitErr, &stopped) {
				return waitErr
			}
			return err
		}
	}
}

// stoppedWhileWaiting reports the operator's stop of a run that was waiting for
// GitHub in place (untilGitHubAnswers), which ends the promotion as their stop
// rather than being recorded as the step's failure.
func stoppedWhileWaiting(err error) bool {
	var stopped operatorStop
	return errors.As(err, &stopped)
}

// waitWithoutLease is awaitForge with the promotion lease given back for the
// wait and taken again once it is over. A run asked from outside integrate
// holds no lease here, and waits all the same.
func (a *activeRun) waitWithoutLease(ctx context.Context, away forgeAwayError) error {
	held := a.promotionLease != nil
	if held {
		_ = a.promotionLease.Release()
		a.promotionLease = nil
	}
	if err := a.awaitForge(ctx, away); err != nil {
		return err
	}
	if !held {
		return nil
	}
	a.landingAt("waiting for this run's turn to land on "+a.worktree.TargetBranch+" again, now that the wait for GitHub is over", landingAsksNothing)
	lease, err := a.pipeline.Store.LeasePromotion(ctx, a.worktree.TargetBranch)
	if err != nil {
		return fmt.Errorf("take this run's turn to land again after waiting for GitHub: %w", err)
	}
	a.promotionLease = lease
	return nil
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

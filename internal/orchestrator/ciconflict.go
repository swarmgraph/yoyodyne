package orchestrator

// A pull request that conflicts with its target while the run waits for the
// forge's checks on it (cichecks.go).
//
// GitHub starts no checks on a pull request it cannot merge, so the wait could
// only run out. Every look at a waiting run therefore asks GitHub whether the
// request conflicts, and a conflict decides the wait before anything the checks
// say. The run takes a developer slot like any continuation, brings the local
// target level with the remote's, and replays its change onto it
// (bringTargetIn). A change that replays cleanly is checked by the short local
// check, pushed, and waited on at its new head. One that does not is handed to
// the same run's developer (handCIWaitConflict) as the run's one reconciliation
// attempt, which is the one a conflict the merge queue hands back gets
// (mergequeuecatchup.go); a run that has used it stops for the development
// manager. Neither counts a repair attempt or a review round, and neither the
// full local checks nor a review runs on the head that conflicts.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CIConflictForge is a forge that says whether a pull request conflicts with
// its target. A forge without it never reports a conflict, and the wait goes on
// as it does for an answer not yet worked out.
type CIConflictForge interface {
	MergeWait(ctx context.Context, number int) (publish.MergeWait, error)
}

var _ CIConflictForge = publish.GitHub{}

// ciConflictRetry is how long a conflict that could not be acted on, or that
// the replay did not find, is left before the run takes a slot for it again.
const ciConflictRetry = 5 * time.Minute

// maxCIConflictSaysBytes bounds the sentence a wait carries about a conflict.
const maxCIConflictSaysBytes = 1 << 10

// ciWaitConflicts reports whether the forge says the waiting run's pull request
// conflicts with its target, at exactly the head waited on. An answer the forge
// has not worked out, could not give, or gave for another head is no conflict.
func ciWaitConflicts(ctx context.Context, forge CIForge, state runstate.State) bool {
	reader, ok := forge.(CIConflictForge)
	pr, wait := state.PullRequest, state.CIWait
	if !ok || pr == nil || wait == nil || pr.Number <= 0 || pr.HeadCommit != wait.Commit {
		return false
	}
	reading, err := reader.MergeWait(ctx, pr.Number)
	return err == nil && reading.Conflicts() && strings.EqualFold(reading.HeadCommit, wait.Commit)
}

// ciConflictSays is what a conflict reads as before what is done about it.
func ciConflictSays(state runstate.State) string {
	return fmt.Sprintf("GitHub reports that pull request #%d conflicts with %s", state.PullRequest.Number, state.TargetBranch)
}

// ciConflictDecided is what a pull makes of a conflict: whether it decides the
// wait, what it says, and whether the checks are to be read as usual instead.
func ciConflictDecided(state runstate.State, now time.Time) (decided bool, says string, ordinary bool) {
	tried := state.CIWait.Conflict
	if tried == nil || now.Sub(tried.At) >= ciConflictRetry || now.Before(tried.At) {
		return true, ciConflictSays(state) + ", so the run brings " + state.TargetBranch + " into its change", false
	}
	return false, tried.Says, tried.NothingToBringIn
}

// ciWaitConflict ends a wait whose change does not replay onto its target. It
// is an error so the repair loop answers it (handCIWaitConflict).
type ciWaitConflict struct {
	cause error
}

func (e ciWaitConflict) Error() string { return e.cause.Error() }
func (e ciWaitConflict) Unwrap() error { return e.cause }

// bringTargetIn answers a conflict found on the waiting run's pull request. It
// reports false, with the wait's record saying so, where the change is already
// on the target as it stands and there is nothing to bring in; the wait is then
// settled from the checks as usual.
//
// The end of the wait is saved with the replayed head, so no process finds the
// new head beside a wait for the old one.
func (a *activeRun) bringTargetIn(ctx context.Context, wait runstate.CIWait) (bool, error) {
	target := a.worktree.TargetBranch
	says := ciConflictSays(a.state)
	if err := a.levelTargetForWait(ctx); err != nil {
		if away, ok := forgeAwayOf(err); ok {
			wait.Decided = nil
			wait.Conflict = &runstate.CIWaitConflict{At: a.pipeline.clock().Now(),
				Says: oneline.Fold(fmt.Sprintf("%s, and %s could not be brought into the change because %s; it is tried again", says, target, away.Error()), maxCIConflictSaysBytes)}
			return true, a.parkAgain(wait, wait.Conflict.Says)
		}
		a.state.CIWait = nil
		if errors.Is(err, ErrDivergedTarget) {
			return true, err
		}
		return true, stoppedBy(runstate.StopIntegration, err)
	}
	rebase, err := a.replayOntoTarget(ctx)
	if err == nil && rebase.HeadCommit == wait.Commit {
		wait.Decided = nil
		wait.Conflict = &runstate.CIWaitConflict{At: a.pipeline.clock().Now(), NothingToBringIn: true,
			Says: fmt.Sprintf("%s, but the change is already on %s as it stands and nothing conflicts", says, target)}
		a.state.CIWait = &wait
		return false, nil
	}
	var done string
	switch {
	case errors.Is(err, gitworktree.ErrRebaseConflict) && a.state.ReconciliationUsed():
		done = "the change conflicts with it and this run already used its one attempt to bring its change up to date, so the development manager decides what happens next"
	case errors.Is(err, gitworktree.ErrRebaseConflict):
		done = "the change conflicts with it" + ciConflictPaths(err) + ", so it went back to its developer to settle, which counts no repair attempt"
	case err != nil:
		done = fmt.Sprintf("bringing %s into the change failed", target)
	default:
		done = fmt.Sprintf("%s at %s was brought into the change without a conflict, and the checks are waited for on the new head", target, shortCommit(rebase.BaseCommit))
	}
	a.state.CIWait = nil
	a.state.CIWaitEnd = &runstate.CIWaitEnd{Commit: wait.Commit, At: a.pipeline.clock().Now(), Conflict: true,
		Says: oneline.Fold(says+"; "+done, maxCIConflictSaysBytes)}
	if err == nil {
		// What is checked and reviewed next is owed to the target moving too.
		a.state.ConflictSpendingUnclear()
	}
	if recordErr := a.recordRebase(rebase); recordErr != nil {
		return true, stoppedBy(runstate.StopIntegration, withFailedRecord(err, recordErr))
	}
	switch {
	case errors.Is(err, gitworktree.ErrRebaseConflict):
		return true, ciWaitConflict{cause: err}
	case err != nil:
		return true, stoppedBy(runstate.StopIntegration, fmt.Errorf("bring %s into the change pull request #%d waits with: %w", target, a.state.PullRequest.Number, err))
	}
	return true, a.checkRound(ctx)
}

// replacingConflictedHead reports a run whose pull request still carries the
// head that was found to conflict, after the target was brought into the change.
// The push that ordinarily replaces that head comes after the short local check,
// so a developer's attempt that answers a failure of that check, in this process
// or one that takes the run up later, is published in its place instead.
// It is read from the record alone: the replayed head and how the wait ended are
// saved together, and a publication moves the request's head.
func (a *activeRun) replacingConflictedHead() bool {
	end, pr := a.state.CIWaitEnd, a.outcome.PullRequest
	return end != nil && end.Conflict && pr != nil && a.state.HarnessCommit != "" &&
		pr.HeadCommit == end.Commit && a.state.HarnessCommit != end.Commit
}

// levelTargetForWait brings the local target level with the remote's as a
// landing does, under the target's promotion lease and for no longer.
func (a *activeRun) levelTargetForWait(ctx context.Context) error {
	lease, err := a.pipeline.Store.LeasePromotion(ctx, a.worktree.TargetBranch)
	if err != nil {
		return fmt.Errorf("wait for a turn to bring %s level with the remote's: %w", a.worktree.TargetBranch, err)
	}
	defer func() { _ = lease.Release() }()
	return a.settleRemoteTarget(ctx)
}

// ciConflictPaths names the first conflicting files, for a sentence.
func ciConflictPaths(err error) string {
	var conflict *gitworktree.RebaseConflict
	if !errors.As(err, &conflict) || len(conflict.Paths) == 0 {
		return ""
	}
	const named = 5
	paths := conflict.Paths[:min(named, len(conflict.Paths))]
	says := " in " + strings.Join(paths, ", ")
	if more := len(conflict.Paths) - len(paths); more > 0 {
		says += fmt.Sprintf(" and %d more", more)
	}
	return says
}

// handCIWaitConflict hands a change that conflicts with its target, found while
// it waited for the forge's checks, to its developer as the run's one
// reconciliation attempt. The attempt and the run being at its developer are
// saved before the change is moved, so a process that stops anywhere after
// finds the move or the attempt still owed, and only once.
func (a *activeRun) handCIWaitConflict(ctx context.Context, conflict ciWaitConflict) error {
	a.recordReplayConflict(recordedReplayConflict(a.worktree, conflict.cause, runstate.PhaseChecking, a.pipeline.clock().Now().UTC()), conflict.cause)
	if a.state.ReconciliationUsed() {
		a.observeCheckEnded(ctx, conflict, budgetSpent)
		return a.blockOnCIWaitConflict(conflict)
	}
	a.observeCheckEnded(ctx, conflict, stillRepairable)
	return a.reconcile(ctx)
}

// blockOnCIWaitConflict stops a run whose change conflicts with its target
// after its one reconciliation attempt was used, as a second conflict in the
// merge queue does.
func (a *activeRun) blockOnCIWaitConflict(conflict ciWaitConflict) error {
	a.outcome.ReplayConflict = a.state.ReplayConflict
	a.syncLandingAttempts()
	blocked := fmt.Errorf("%s: %w", a.state.CIWaitEnd.Says, conflict.cause)
	notes := strings.Join([]string{
		"Yoyodyne stopped this item: " + a.state.CIWaitEnd.Says + ".",
		"Nothing was force-merged, reset, or settled automatically, no repair attempt was spent, and neither the full local checks nor a review was run on the conflicting change.",
		"Failure: " + blocked.Error(),
		"Run: " + a.outcome.RunID,
		"Branch: " + a.outcome.Branch,
		"Worktree: " + a.outcome.WorktreePath,
		"The branch, the worktree and the pull request are kept as they were. A repair decided by the development manager continues this run's developer session with the conflict to settle against the target.",
	}, "\n")
	if err := a.block(notes); err != nil {
		return stoppedBy(runstate.StopIntegrationBudget, withFailedRecord(blocked, fmt.Errorf("record the conflict as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopIntegrationBudget, blocked)
}

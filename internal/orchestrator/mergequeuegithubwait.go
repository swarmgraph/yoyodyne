package orchestrator

// The merge queue waiting out GitHub being unreachable in its own landing and
// recovery passes, as a run's merge step does (forgewait.go).
//
// The operator's rule is that a dropped connection is waited out: never charged
// to the work, and never a stop somebody has to clear. Every step of a queue
// pass that meets GitHub — pushing the candidate and reading its branch,
// opening the pull request, checking the remote target before the merge,
// asking for the merge and asking afterwards whether it was made, confirming
// the remote target carries the landing, bringing the local target onto it,
// and asking GitHub to withdraw a merge — wraps a failure that never reached
// GitHub, or reached it and dropped, as queueGitHubAway (gitHubAway). A push
// that ended with no answer from the remote is one whatever it printed. The
// promoter's ways in (Promote, Withdraw, and recover) turn that into a recorded
// wait rather than an error: the entry is neither handed back nor recorded as
// refused, the pass ends waiting, and what the step left on the landing record
// is what the next pass takes up — a push or merge asked for and not answered
// is observed before it is asked again, exactly as after a restart.
//
// The wait is kept beside the queue (runstate.MergeQueueGitHubWait) with the
// step, how many tries have failed since when, and when the next is due, on
// the recovery package's paced intervals, the ones a run's merge step waits on.
// A pass that finds the next try not yet due asks GitHub nothing and ends
// waiting with the same record, and the session's queue loop sleeps until the
// try is due (mergeQueuePasses.work), so a process started after a restart
// takes the wait up at the try it had reached rather than starting again. The
// record goes once the step is answered, whatever the answer, or once the entry
// it was for is no longer the one being landed.
//
// A refusal from GitHub about the change itself — a failing check, a conflict,
// a push refused for its content, a merge GitHub will not make — is an answer:
// it is not wrapped, and is handled as it always was.
//
// GitHub refusing the harness's login can look like no answer at all, because a
// push that fails without naming a refused ref is read as one that ended
// unanswered. So a wait that has failed mergeQueueGitHubWaitReportAfter tries
// in a row, about half an hour, files one warning for the development manager
// and says on status that something other than the network may be wrong; it
// goes on waiting, because a long outage is still an outage.

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueGitHubWaits keeps a queue's wait for GitHub. It is satisfied by
// *runstate.MergeQueueStore.
type MergeQueueGitHubWaits interface {
	GitHubWait(key runstate.MergeQueueKey) (runstate.MergeQueueGitHubWait, bool, error)
	RecordGitHubWait(ctx context.Context, key runstate.MergeQueueKey, wait runstate.MergeQueueGitHubWait) error
	ClearGitHubWait(ctx context.Context, key runstate.MergeQueueKey) error
}

// mergeQueueGitHubWaitReportAfter is how many tries in a row a queue's wait for
// GitHub may fail before the development manager is told. On the paced
// intervals that is about half an hour of failing.
const mergeQueueGitHubWaitReportAfter = 15

// queueGitHubAway is a step of a queue pass that could not reach GitHub. step
// says what the queue was doing, in words that follow "while".
type queueGitHubAway struct {
	step  string
	cause error
}

func (e queueGitHubAway) Error() string {
	return fmt.Sprintf("GitHub could not be reached while %s: %v", e.step, e.cause)
}

func (e queueGitHubAway) Unwrap() error { return e.cause }

// gitHubAway wraps a failed step as one that could not reach GitHub where its
// class says so (recovery.Recoverable), and returns anything else as it was.
func gitHubAway(step string, err error) error {
	if err == nil || !recovery.Recoverable(err) {
		return err
	}
	return queueGitHubAway{step: step, cause: err}
}

// gitHubAwayOK is gitHubAway for a caller that handles the other failures
// itself: it reports whether err was one that could not reach GitHub.
func gitHubAwayOK(step string, err error) (error, bool) {
	if err == nil || !recovery.Recoverable(err) {
		return err, false
	}
	return queueGitHubAway{step: step, cause: err}, true
}

// waitingOnGitHub reads the queue's wait for GitHub before a step is asked. A
// wait for another entry is stale and is removed. A wait whose next try is not
// due yet is the outcome — Waiting, with the wait — and reports true, so the
// caller asks GitHub nothing this pass.
func (q *queuePromotion) waitingOnGitHub(ctx context.Context) (bool, error) {
	waits := q.p.GitHubWaits
	if waits == nil {
		return false, nil
	}
	wait, found, err := waits.GitHubWait(q.key)
	if err != nil {
		return false, fmt.Errorf("read whether the merge queue for %s is waiting for GitHub: %w", q.key.TargetBranch, err)
	}
	if !found {
		return false, nil
	}
	if wait.EntryID != q.entry.EntryID {
		if err := waits.ClearGitHubWait(ctx, q.key); err != nil {
			return false, fmt.Errorf("clear the merge queue's wait for GitHub, which was for another entry: %w", err)
		}
		return false, nil
	}
	q.gitHubWait = &wait
	if !q.now().Before(wait.Wait.NextTryAt) {
		return false, nil
	}
	q.waitOn(wait)
	return true, nil
}

func (q *queuePromotion) waitOn(wait runstate.MergeQueueGitHubWait) {
	q.outcome.Waiting = wait.Says()
	q.outcome.GitHubWait = &wait
}

// settleGitHub is what a step's end does to the queue's wait for GitHub. A step
// that could not reach GitHub records the next try and ends the call waiting,
// returning nil; anything else ends the wait, if there was one, and is returned
// as it was.
func (q *queuePromotion) settleGitHub(ctx context.Context, err error) error {
	var away queueGitHubAway
	if !errors.As(err, &away) {
		if q.gitHubWait != nil && q.p.GitHubWaits != nil {
			q.gitHubWait = nil
			if clearErr := q.p.GitHubWaits.ClearGitHubWait(ctx, q.key); clearErr != nil && err == nil {
				return fmt.Errorf("clear the merge queue's wait for GitHub now that it answered: %w", clearErr)
			}
		}
		return err
	}
	now := q.now()
	wait := runstate.MergeQueueGitHubWait{EntryID: q.entry.EntryID, Wait: runstate.ForgeWait{Since: now}}
	if q.gitHubWait != nil {
		wait = *q.gitHubWait
	}
	wait.Wait.Tries++
	wait.Wait.Step = away.step
	wait.Wait.Failure = singleLine(away.cause.Error(), runstate.MaxRetryFailureBytes)
	wait.Wait.NextTryAt = now.Add(recovery.Interval(wait.Wait.Tries))
	if wait.ReportedAt == nil && wait.Wait.Tries >= mergeQueueGitHubWaitReportAfter && q.reportGitHubWait(wait) {
		wait.ReportedAt = &now
	}
	if q.p.GitHubWaits != nil {
		if recordErr := q.p.GitHubWaits.RecordGitHubWait(ctx, q.key, wait); recordErr != nil {
			return fmt.Errorf("record that the merge queue is waiting for GitHub (%v): %w", away, recordErr)
		}
	}
	q.gitHubWait = &wait
	// What the call had found before the step failed says nothing now: the
	// queue is waiting for GitHub, and that is all it is doing.
	q.outcome.Refusal, q.outcome.Unresolved, q.outcome.UnresolvedBy = "", "", nil
	q.waitOn(wait)
	return nil
}

// reportGitHubWait files the one warning a long wait for GitHub earns, for the
// development manager, and reports whether it was kept.
func (q *queuePromotion) reportGitHubWait(wait runstate.MergeQueueGitHubWait) bool {
	p := q.p.Pipeline
	if p.Reports == nil {
		return false
	}
	message := fmt.Sprintf("The merge queue for %s has not reached GitHub for %d tries in a row since %s, while %s (%s), and keeps waiting. A network outage usually ends sooner: if GitHub is answering otherwise, look at what that step printed, such as GitHub refusing the harness's login.",
		q.key.TargetBranch, wait.Wait.Tries, wait.Wait.Since.Local().Format("2006-01-02 15:04 MST"), wait.Wait.Step, oneline.Bound(wait.Wait.Failure, 400))
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning, For: domain.RoleDevelopmentManager,
		Message: oneline.Fold(message, report.MaxMessageBytes),
	}}, report.Attribution{
		Role: report.HarnessReporter, RunID: q.entry.RunID, WorkItemID: q.entry.WorkItemID, Build: p.Build,
		ProductID: p.Config.Product.ID, RepositoryID: string(p.Config.Product.RepositoryID),
	}, q.now())
	if err != nil {
		return false
	}
	kept := false
	for _, reported := range collected {
		if p.Reports.Append(reported) == nil {
			kept = true
		}
	}
	return kept
}

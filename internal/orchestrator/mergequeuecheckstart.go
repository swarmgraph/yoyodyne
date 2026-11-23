package orchestrator

// A required GitHub check that never starts, or that GitHub skips, gives the
// merge queue no verdict, and waiting on it only holds every entry behind it.
// So the queue asks GitHub for the check once more and keeps waiting, with the
// entry still at the head of the queue, and acts only once GitHub has
// answered or has left the check unstarted through one more wait.
//
// Where GitHub's required checks stand in for the candidate's full check
// (mergequeuequick.go), a check not started within checkStartLimit of the
// push, skipped, or cancelled is asked for again once (askOnForge); if it has
// still not passed within the wait limit, counted again from the request, the
// candidate's next generation runs the full local checks instead, charged to
// nothing.
//
// Where GitHub holds a merge the queue asked for (mergequeuepromotion.go,
// awaitForge), a required check GitHub has not started within checkStartLimit
// of the merge being asked for is asked for again once
// (answerUnstartedChecks), and triage.stuck_merge_age is counted again from
// the request. The driver withdraws the merge only once that second wait is
// over, and hands it back as a failure outside the change: no repair attempt
// is charged and the change is not marked at fault (outsideFailureLead).
//
// Each request is written down beside the queue before GitHub is asked
// (runstate's mergequeuereruns.go), with the candidate and why, so a process
// that stops after asking never asks again.
//
// A cancelled check on a held merge is answered by answerUnjudgedChecks
// (mergequeuecheckrerun.go), and a check that started and failed is unchanged
// by any of this.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// checkStartLimit is how long a required check may go unstarted before the
// queue asks GitHub for it again: long enough for GitHub to start a check on
// an ordinary busy afternoon, and well under the two hours a held merge may
// wait (triage.stuck_merge_age).
const checkStartLimit = 20 * time.Minute

// notStartedConclusion is how a request for a check GitHub never started is
// recorded, so it is told apart from a re-run of a check that ended.
const notStartedConclusion = "not started"

// outsideFailureLead opens the reason a merge withdrawn because GitHub never
// started its check is handed back with. The hand-back reads it
// (outsideFailure) to leave the run's passed checks standing and record no
// check failure against the change.
const outsideFailureLead = "a failure outside the change: "

// MergeQueueChecksAsker asks GitHub to run a required check again on a
// commit. It is satisfied by publish.GitHub.
type MergeQueueChecksAsker interface {
	AskChecksAgain(ctx context.Context, commit string, checkRun int64) (string, error)
}

// outsideFailure reports a hand-back reason for a merge withdrawn because
// GitHub never started its check.
func outsideFailure(reason string) bool {
	return strings.Contains(reason, outsideFailureLead)
}

// startLimit is checkStartLimit, or half of a shorter limit on the whole wait,
// so the request is always made with time left to answer it.
func startLimit(whole time.Duration) time.Duration {
	if whole > 0 && whole/2 < checkStartLimit {
		return whole / 2
	}
	return checkStartLimit
}

// askedOnCandidate is the latest request for each check on one candidate.
func askedOnCandidate(reruns []runstate.MergeQueueCheckRerun, candidate string) map[string]runstate.MergeQueueCheckRerun {
	asked := map[string]runstate.MergeQueueCheckRerun{}
	for _, rerun := range reruns {
		if rerun.Commit == candidate {
			asked[rerun.Check] = rerun
		}
	}
	return asked
}

// latestAsk is when the queue last asked GitHub for a check on the candidate,
// and zero where it never did.
func latestAsk(asked map[string]runstate.MergeQueueCheckRerun) time.Time {
	var latest time.Time
	for _, rerun := range asked {
		if rerun.At.After(latest) {
			latest = rerun.At
		}
	}
	return latest
}

// askOnForge records and makes one request to GitHub for each check that
// never started, was skipped, or was cancelled on the candidate. A check that
// ended names its job; checks GitHub never started name none, and one request
// for the commit's workflow runs answers them all. It returns what GitHub was
// asked, or what it would not do.
func (w MergeQueueWorker) askOnForge(ctx context.Context, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration, checks []runstate.CICheckEvidence) (said, refused string, err error) {
	jobs := map[string]int64{}
	if reading, err := w.Pipeline.CIForge.RequiredChecks(ctx, generation.Candidate, generation.Checks.ForgeChecks); err == nil && reading.Commit == generation.Candidate {
		for _, check := range reading.Checks {
			if check.Result != publish.CheckNotStarted {
				jobs[check.Name] = check.ID
			}
		}
	}
	pull := 0
	if generation.ForgePush != nil {
		pull = generation.ForgePush.PullRequest
	}
	var asked, notAsked []string
	unstartedAsked := false
	for _, check := range checks {
		job := jobs[check.Name]
		why := fmt.Sprintf("GitHub reported the required check %s %s on candidate %s", check.Name, check.Result, generation.Candidate)
		if check.Result == string(publish.CheckNotStarted) {
			why = fmt.Sprintf("GitHub had not started the required check %s on candidate %s within %s of the push", check.Name, generation.Candidate, startLimit(w.Pipeline.Config.Execution.CIChecks.WaitLimitInForce()))
		}
		rerun := runstate.MergeQueueCheckRerun{PullRequest: pull, Check: check.Name, CheckRun: job, Conclusion: check.Result, At: w.now(), Commit: generation.Candidate, Why: why}
		if err := w.Reruns.RecordCheckRerun(ctx, key, generation.EntryID, rerun); err != nil {
			return "", "", fmt.Errorf("record asking GitHub to run %s again on %s: %w", check.Name, generation.Candidate, err)
		}
		if job == 0 && unstartedAsked {
			asked = append(asked, check.Name)
			continue
		}
		answer, err := w.Asker.AskChecksAgain(ctx, generation.Candidate, job)
		if err != nil {
			notAsked = append(notAsked, fmt.Sprintf("%s (%v)", check.Name, err))
			continue
		}
		unstartedAsked = unstartedAsked || job == 0
		asked = append(asked, fmt.Sprintf("%s (%s)", check.Name, answer))
	}
	if len(notAsked) > 0 {
		refused = "GitHub would not run " + strings.Join(notAsked, "; ") + " again"
	}
	if len(asked) > 0 {
		said = fmt.Sprintf("GitHub had not run %s on candidate %s, so the queue asked it to run %s again and keeps the entry at the head of the queue meanwhile",
			strings.Join(names(checks), ", "), generation.Candidate, strings.Join(asked, ", "))
	}
	return said, refused, nil
}

func names(checks []runstate.CICheckEvidence) []string {
	out := make([]string, 0, len(checks))
	for _, check := range checks {
		out = append(out, check.Name)
	}
	return out
}

// answerUnstartedChecks asks GitHub once more for a required check it has not
// started on a merge it holds, once checkStartLimit has passed since the merge
// was asked for, and from then counts the limit on the held merge's wait again
// from the request. Once a request stands and GitHub has still not started the
// check, NeverStarted says so, and the driver withdraws the merge as a failure
// outside the change once that second wait is over. A merge whose checks
// failed or ended without judging the change is left to what already answers
// it.
func (q *queuePromotion) answerUnstartedChecks(ctx context.Context, reading runstate.MergeQueueForgeReading) error {
	if q.p.Asker == nil || q.p.Reruns == nil || q.outcome.ForgeHeldSince == nil ||
		q.outcome.ChecksFailed != "" || q.outcome.Unjudged != "" || q.outcome.RequiredFailed != "" {
		return nil
	}
	attempt := q.attempt()
	reruns, err := q.p.Reruns.CheckReruns(q.key, q.entry.EntryID)
	if err != nil {
		return fmt.Errorf("read the checks the queue asked GitHub for on entry %d: %w", q.entry.Order, err)
	}
	unstarted := strings.Join(reading.UnstartedChecks, ", ")
	for index := len(reruns) - 1; index >= 0; index-- {
		asked := reruns[index]
		if asked.PullRequest != attempt.PullRequest || asked.Commit != attempt.Candidate || asked.Conclusion != notStartedConclusion {
			continue
		}
		if since := asked.At; since.After(*q.outcome.ForgeHeldSince) {
			q.outcome.ForgeHeldSince = &since
		}
		if unstarted != "" {
			q.outcome.NeverStarted = fmt.Sprintf("GitHub never started the required check %s on pull request %d: the harness asked it to run again at %s, and it had still not started",
				unstarted, attempt.PullRequest, asked.At.Local().Format("2006-01-02 15:04 MST"))
			q.outcome.Waiting += "; " + q.outcome.NeverStarted
		}
		return nil
	}
	if unstarted == "" || q.now().Sub(*q.outcome.ForgeHeldSince) < startLimit(q.stuckAge()) {
		return nil
	}
	rerun := runstate.MergeQueueCheckRerun{
		PullRequest: attempt.PullRequest, Check: unstarted, Conclusion: notStartedConclusion, At: q.now(), Commit: attempt.Candidate,
		Why: fmt.Sprintf("GitHub had not started the required check %s on pull request %d within %s of the merge being asked for", unstarted, attempt.PullRequest, waitedFor(q.now().Sub(*q.outcome.ForgeHeldSince))),
	}
	if err := q.p.Reruns.RecordCheckRerun(ctx, q.key, q.entry.EntryID, rerun); err != nil {
		return fmt.Errorf("record asking GitHub to start %s on pull request %d: %w", unstarted, attempt.PullRequest, err)
	}
	at := rerun.At
	q.outcome.ForgeHeldSince = &at
	answer, err := q.p.Asker.AskChecksAgain(ctx, attempt.Candidate, 0)
	if err != nil {
		answer = fmt.Sprintf("GitHub would not run it again (%v)", err)
	}
	q.outcome.Waiting += fmt.Sprintf("; %s, so the harness asked for it again: %s; the merge stays at the head of the queue and waits the limit once more from now", rerun.Why, answer)
	return nil
}

// stuckAge is triage.stuck_merge_age as the promotion's pipeline holds it.
func (q *queuePromotion) stuckAge() time.Duration {
	if q.p.Pipeline == nil {
		return 0
	}
	return time.Duration(q.p.Pipeline.Config.Triage.StuckMergeAge)
}

// requiredCheckNames is the checks the project names as required on GitHub,
// which is how a required check GitHub reports nothing for is found.
func (q *queuePromotion) requiredCheckNames() []string {
	if q.p.Pipeline == nil {
		return nil
	}
	return q.p.Pipeline.Config.Execution.CIChecks.Required
}

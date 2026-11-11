package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueCandidateChecks reads the checks of a merge queue candidate's
// pull request and runs one of them again. It is satisfied by publish.GitHub.
type MergeQueueCandidateChecks interface {
	Checks(ctx context.Context, number int, base string) (publish.CheckReading, error)
	RerunCheck(ctx context.Context, checkRun int64) error
}

// MergeQueueCheckReruns is the record of the checks the queue ran again. It
// is satisfied by *runstate.MergeQueueStore.
type MergeQueueCheckReruns interface {
	CheckReruns(key runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueCheckRerun, error)
	RecordCheckRerun(ctx context.Context, key runstate.MergeQueueKey, entryID string, rerun runstate.MergeQueueCheckRerun) error
}

// unjudgedConclusions are how a check ends in the runner rather than on the
// change: cancelled (a job's own time limit cancels it), out of time, or never
// started. Such a check judged nothing, and a required one holds the merge
// for good, because the forge does not run it again by itself.
var unjudgedConclusions = map[string]bool{
	"cancelled":       true,
	"timed_out":       true,
	"startup_failure": true,
}

// answerUnjudgedChecks treats a check on the candidate's pull request that
// ended without judging the change as an answer rather than a wait. The first
// time a check ends that way the forge is asked to run it again, and the
// re-run is written down before it is asked for. A check that ends that way
// again after its re-run, or that the forge will not run again, leaves the
// forge nothing that will make the merge once no other check is still
// running, so the merge is marked for the driver to withdraw at once with that
// as its reason, rather than held until the stuck-merge limit. What it did is
// the promotion's Waiting, which the pass records for status and the
// dashboard.
//
// A promoter wired without check access, or a reading the forge would not
// give, leaves the merge waiting as it was.
func (q *queuePromotion) answerUnjudgedChecks(ctx context.Context) error {
	if q.p.Checks == nil || q.p.Reruns == nil {
		return nil
	}
	attempt := q.attempt()
	reading, err := q.p.Checks.Checks(ctx, attempt.PullRequest, q.key.TargetBranch)
	if err != nil {
		q.outcome.Waiting += fmt.Sprintf("; its checks could not be read now: %v", err)
		return nil
	}
	var unjudged []publish.FailedCheck
	for _, failing := range reading.Failing {
		if unjudgedConclusions[strings.ToLower(failing.Conclusion)] {
			unjudged = append(unjudged, failing)
		}
	}
	if len(unjudged) == 0 {
		return nil
	}
	reruns, err := q.p.Reruns.CheckReruns(q.key, q.entry.EntryID)
	if err != nil {
		return fmt.Errorf("read the checks the queue ran again for entry %d: %w", q.entry.Order, err)
	}
	var askedAgain, unanswered []string
	for _, check := range unjudged {
		ended := fmt.Sprintf("%s (%s)", check.Name, describeUnjudged(check.Conclusion))
		if ranAgain(reruns, attempt.PullRequest, check.Name) {
			unanswered = append(unanswered, ended+" again after the queue had it run again")
			continue
		}
		if check.ID <= 0 {
			unanswered = append(unanswered, ended+", and the forge names no job to run again")
			continue
		}
		rerun := runstate.MergeQueueCheckRerun{PullRequest: attempt.PullRequest, Check: check.Name, CheckRun: check.ID, Conclusion: check.Conclusion, At: q.now()}
		if err := q.p.Reruns.RecordCheckRerun(ctx, q.key, q.entry.EntryID, rerun); err != nil {
			return fmt.Errorf("record running %s again on pull request %d: %w", check.Name, attempt.PullRequest, err)
		}
		if err := q.p.Checks.RerunCheck(ctx, check.ID); err != nil {
			why := err.Error()
			if errors.Is(err, publish.ErrForgeAccessRefused) {
				why = "the forge would not let the harness's token run the job again"
			}
			unanswered = append(unanswered, fmt.Sprintf("%s, and the forge would not run it again: %s", ended, why))
			continue
		}
		askedAgain = append(askedAgain, ended)
	}
	var says []string
	if len(askedAgain) > 0 {
		says = append(says, fmt.Sprintf("on the candidate's pull request %d, %s ended without judging the change, so the queue asked the forge to run it again", attempt.PullRequest, strings.Join(askedAgain, " and ")))
	}
	if len(unanswered) > 0 {
		ended := fmt.Sprintf("on the candidate's pull request %d, %s", attempt.PullRequest, strings.Join(unanswered, "; "))
		says = append(says, ended)
		if len(askedAgain) == 0 && len(reading.Pending) == 0 {
			q.outcome.Unjudged = ended + "; nothing else is running there, so the forge will never make the merge"
		}
	}
	q.outcome.Waiting = fmt.Sprintf("the forge holds the merge of pull request %d queued; %s", attempt.PullRequest, strings.Join(says, "; "))
	return nil
}

// ranAgain reports a check the queue already had run again on this pull
// request.
func ranAgain(reruns []runstate.MergeQueueCheckRerun, pullRequest int, check string) bool {
	for _, rerun := range reruns {
		if rerun.PullRequest == pullRequest && rerun.Check == check {
			return true
		}
	}
	return false
}

// describeUnjudged says how a check ended in the runner, in ordinary words.
func describeUnjudged(conclusion string) string {
	switch strings.ToLower(conclusion) {
	case "cancelled":
		return "cancelled, as a job's own time limit cancels it"
	case "timed_out":
		return "out of time"
	case "startup_failure":
		return "never started"
	}
	return conclusion
}

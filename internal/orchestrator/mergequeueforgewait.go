package orchestrator

// What GitHub says a merge it holds for the queue is waiting on.
//
// A merge the queue asked GitHub for can sit unmade for the whole of
// triage.stuck_merge_age, and a withdrawal that said only how long it waited
// left nobody knowing why. So at every queue pass that finds GitHub holding
// the merge, awaitForge asks GitHub for its own account of the pull request —
// whether it can merge, its merge state, its reviews, where it stands in
// GitHub's merge queue, and how each required check stood — and keeps the
// latest reading on the entry with its time (runstate's
// mergequeueforgereading.go). The pass, the withdrawal, and yoyo status say
// what GitHub last said from it.
//
// A read of GitHub that fails — GitHub out of reach, a certificate that cannot
// be checked — is kept as the reading, saying why, and is not GitHub holding
// the merge: that pass does not count toward the limit, and the time from it
// until the next reading is added to the reading's UnreadFor, which is taken
// off the wait the limit is measured against.
//
// A required check that ended in failure, with no required check still
// running, is a merge GitHub will never make: the pass sets RequiredFailed and
// the driver withdraws the merge at once naming the check, rather than waiting
// out the limit. A required check that was cancelled, ran out of time, or
// never started judged nothing about the change, and is left to
// answerUnjudgedChecks (mergequeuecheckrerun.go), which has it run again once.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueMergeWaits reads GitHub's account of what a held merge is waiting
// on. It is satisfied by publish.GitHub.
type MergeQueueMergeWaits interface {
	MergeWait(ctx context.Context, number int) (publish.MergeWait, error)
}

// MergeQueueForgeReadings keeps the latest reading of that account per entry.
// It is satisfied by *runstate.MergeQueueStore.
type MergeQueueForgeReadings interface {
	ForgeReading(key runstate.MergeQueueKey, entryID string) (runstate.MergeQueueForgeReading, bool, error)
	RecordForgeReading(ctx context.Context, key runstate.MergeQueueKey, entryID string, reading runstate.MergeQueueForgeReading) error
}

// readMergeWait asks GitHub what the attempt's held merge is waiting on and
// keeps the answer, or the failure to get one, as the entry's latest reading.
// read is false where the promoter is wired to read nothing. An error is a
// reading that could not be kept, which fails the call as any record that
// would not save does.
func (q *queuePromotion) readMergeWait(ctx context.Context) (reading runstate.MergeQueueForgeReading, read bool, err error) {
	if q.p.Waits == nil || q.p.Readings == nil {
		return runstate.MergeQueueForgeReading{}, false, nil
	}
	attempt := q.attempt()
	prior, found, err := q.p.Readings.ForgeReading(q.key, q.entry.EntryID)
	if err != nil {
		return runstate.MergeQueueForgeReading{}, false, fmt.Errorf("read what GitHub last said about entry %d: %w", q.entry.Order, err)
	}
	now := q.now()
	reading = runstate.MergeQueueForgeReading{PullRequest: attempt.PullRequest, At: now}
	if found && prior.PullRequest == attempt.PullRequest {
		reading.UnreadFor = prior.UnreadFor
		if prior.Unread != "" && now.After(prior.At) {
			reading.UnreadFor += now.Sub(prior.At)
		}
	}
	wait, err := q.p.Waits.MergeWait(ctx, attempt.PullRequest)
	if err != nil {
		reading.Unread = err.Error()
	} else {
		reading.WaitingOn = wait.WaitingOn()
		reading.MergeState, reading.Mergeable = wait.MergeState, wait.Mergeable
		for _, check := range wait.FailedRequired() {
			reading.FailedChecks = append(reading.FailedChecks, check.Name)
		}
		reading.PendingChecks = wait.PendingRequired()
	}
	if err := q.p.Readings.RecordForgeReading(ctx, q.key, q.entry.EntryID, reading); err != nil {
		return runstate.MergeQueueForgeReading{}, false, fmt.Errorf("keep what GitHub said about pull request %d for entry %d: %w", attempt.PullRequest, q.entry.Order, err)
	}
	if reading.Unread == "" {
		q.outcome.RequiredFailed = requiredFailure(wait, attempt.PullRequest)
	}
	return reading, true, nil
}

// requiredFailure says why GitHub will never make a merge whose required check
// failed on the change, and is empty where it might still make it: no required
// check failed on the change, or a required check is still running.
func requiredFailure(wait publish.MergeWait, pullRequest int) string {
	if len(wait.PendingRequired()) > 0 {
		return ""
	}
	var failed []string
	for _, check := range wait.FailedRequired() {
		if unjudgedConclusions[strings.ToLower(check.Conclusion)] {
			continue
		}
		failed = append(failed, fmt.Sprintf("%q (%s)", check.Name, nonEmpty(check.Conclusion, "failure")))
	}
	if len(failed) == 0 {
		return ""
	}
	checks := "checks"
	if len(failed) == 1 {
		checks = "check"
	}
	return fmt.Sprintf("on the candidate's pull request %d, GitHub reports the required %s %s failed and no required check is still running, so GitHub will never make the merge",
		pullRequest, checks, strings.Join(failed, " and "))
}

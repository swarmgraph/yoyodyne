package runstate

// A merge queue waiting for GitHub to be reachable again.
//
// The queue's landing and recovery passes meet GitHub at every step that
// pushes a candidate, opens or merges its pull request, reads the target on the
// remote, or withdraws a merge. Where one of those steps cannot reach GitHub
// the queue waits for it rather than stopping or handing the entry back
// (internal/orchestrator's mergequeuegithubwait.go), and this is the record of
// that wait: kept beside the queue rather than on the entry's landing record,
// so a pass in a process started after a restart takes it up where it was, and
// so a build that does not know it reads past it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const mergeQueueGitHubWaitName = "github-wait.json"

// MergeQueueGitHubWait is a queue waiting for GitHub on behalf of one entry.
type MergeQueueGitHubWait struct {
	// EntryID is the entry whose step could not reach GitHub.
	EntryID string `json:"entry_id"`
	// Wait is the step, how many tries have failed since when, and when the
	// next is due, in the shape a run's own wait at its merge step takes.
	Wait ForgeWait `json:"wait"`
	// ReportedAt is when the development manager was told the wait has gone on
	// long enough that something other than the network may be the cause. It
	// is set once, so the report is filed once for one wait.
	ReportedAt *time.Time `json:"reported_at,omitempty"`
}

// Says is the wait in one line, for a person: that the merge queue is waiting
// for GitHub, what it was doing, and when it next tries, in local time with
// the zone named.
func (w MergeQueueGitHubWait) Says() string {
	says := "the merge queue is " + w.Wait.Says()
	if w.ReportedAt != nil {
		says += "; it has failed this way for longer than a network outage usually lasts, so if GitHub is answering otherwise something else may be wrong, such as GitHub refusing the harness's login, and the development manager has been told"
	}
	return says
}

func (w MergeQueueGitHubWait) validate() error {
	var problems []error
	if !mergeQueueEntryIDPattern.MatchString(w.EntryID) {
		problems = append(problems, fmt.Errorf("entry id %q is not a merge queue entry id", w.EntryID))
	}
	if err := w.Wait.Validate(); err != nil {
		problems = append(problems, fmt.Errorf("wait: %w", err))
	}
	return errors.Join(problems...)
}

// bounded keeps what the wait carries from GitHub's side within the bounds
// every recorded failure is held to.
func (w ForgeWait) bounded() ForgeWait {
	w.Step = BoundedMergeQueueText(strings.Join(strings.Fields(w.Step), " "))
	if len(w.Step) > MaxRecordedTextBytes {
		w.Step = w.Step[:MaxRecordedTextBytes]
	}
	w.Failure = strings.Join(strings.Fields(w.Failure), " ")
	if len(w.Failure) > MaxRetryFailureBytes {
		w.Failure = strings.ToValidUTF8(w.Failure[:MaxRetryFailureBytes], "")
	}
	w.Since, w.NextTryAt = w.Since.UTC(), w.NextTryAt.UTC()
	return w
}

// RecordGitHubWait writes the queue's wait for GitHub, replacing the one
// before it.
func (s *MergeQueueStore) RecordGitHubWait(ctx context.Context, key MergeQueueKey, wait MergeQueueGitHubWait) error {
	wait.Wait = wait.Wait.bounded()
	if wait.ReportedAt != nil {
		reported := wait.ReportedAt.UTC()
		wait.ReportedAt = &reported
	}
	if err := errors.Join(key.validate(), wait.validate()); err != nil {
		return fmt.Errorf("merge queue wait for GitHub: %w", err)
	}
	return s.writeSide(ctx, key, mergeQueueGitHubWaitName, wait)
}

// ClearGitHubWait removes the queue's wait for GitHub once GitHub has
// answered, or once the entry it was for is no longer the one being landed.
func (s *MergeQueueStore) ClearGitHubWait(ctx context.Context, key MergeQueueKey) error {
	return s.removeSide(ctx, key, mergeQueueGitHubWaitName)
}

// GitHubWait reports the queue's wait for GitHub, and false where it has none.
func (s *MergeQueueStore) GitHubWait(key MergeQueueKey) (MergeQueueGitHubWait, bool, error) {
	var wait MergeQueueGitHubWait
	found, err := s.readSide(key, mergeQueueGitHubWaitName, &wait)
	return wait, found, err
}

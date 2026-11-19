package runstate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// While a queue entry waits for GitHub to make a merge the queue asked for,
// each queue pass reads GitHub's own account of the pull request — whether it
// can merge, its merge state, each required check — and keeps the latest
// reading here, with its time (orchestrator's mergequeuepromotion.go,
// awaitForge). A withdrawal for waiting too long, and yoyo status, name what
// GitHub last said the merge was waiting on from it.
//
// A pass whose read of GitHub failed keeps that failure as the reading, and
// the time from a failed read to the next reading is added to UnreadFor: it is
// time nobody could ask GitHub, not time GitHub was seen holding the merge, so
// the limit on a held merge's wait leaves it out.
//
// The record is kept beside the queue's own, in a file of its own per entry,
// for the reason the check re-runs are (mergequeuereruns.go): a build that
// does not know it would refuse the whole landing record over a field it does
// not know.

// MergeQueueForgeReading is the latest reading of GitHub's account of a merge
// it holds for a queue entry's candidate pull request.
type MergeQueueForgeReading struct {
	PullRequest int       `json:"pull_request"`
	At          time.Time `json:"at"`
	// WaitingOn is what GitHub said the merge is waiting on, in plain words
	// with GitHub's own state words beside them. Empty where Unread is set.
	WaitingOn string `json:"waiting_on,omitempty"`
	// MergeState and Mergeable are GitHub's own words, kept as it gave them.
	MergeState string `json:"merge_state,omitempty"`
	Mergeable  string `json:"mergeable,omitempty"`
	// FailedChecks and PendingChecks name the required checks that ended
	// without passing and those that had not finished.
	FailedChecks  []string `json:"failed_checks,omitempty"`
	PendingChecks []string `json:"pending_checks,omitempty"`
	// Unread is why GitHub could not be asked at At, and empty where it was.
	Unread string `json:"unread,omitempty"`
	// UnreadFor is the time, in nanoseconds, this pull request's wait spent
	// between a failed read and the reading after it, summed over the wait.
	UnreadFor time.Duration `json:"unread_for,omitempty"`
}

// Says is the reading in words: what GitHub said the merge was waiting on, or
// that GitHub could not be asked and why, each with its time.
func (r MergeQueueForgeReading) Says() string {
	at := r.At.Local().Format("2006-01-02 15:04 MST")
	if r.Unread != "" {
		return fmt.Sprintf("GitHub could not be asked about pull request %d at %s: %s", r.PullRequest, at, r.Unread)
	}
	return fmt.Sprintf("at %s GitHub said the merge of pull request %d was waiting on: %s", at, r.PullRequest, r.WaitingOn)
}

func forgeReadingName(entryID string) (string, error) {
	if !mergeQueueEntryIDPattern.MatchString(entryID) {
		return "", fmt.Errorf("merge queue entry %q is not an entry identifier", entryID)
	}
	return "forge-reading-" + entryID + ".json", nil
}

// mergeQueueForgeReading is one entry's record.
type mergeQueueForgeReading struct {
	EntryID string                 `json:"entry_id"`
	Reading MergeQueueForgeReading `json:"reading"`
}

// ForgeReading is the latest reading of GitHub's account kept for an entry,
// and false where none was kept.
func (s *MergeQueueStore) ForgeReading(key MergeQueueKey, entryID string) (MergeQueueForgeReading, bool, error) {
	name, err := forgeReadingName(entryID)
	if err != nil {
		return MergeQueueForgeReading{}, false, err
	}
	var recorded mergeQueueForgeReading
	found, err := s.readSide(key, name, &recorded)
	if err != nil || !found {
		return MergeQueueForgeReading{}, false, err
	}
	if recorded.EntryID != entryID {
		return MergeQueueForgeReading{}, false, fmt.Errorf("the GitHub reading recorded for merge queue entry %s names entry %q", entryID, recorded.EntryID)
	}
	return recorded.Reading, true, nil
}

// RecordForgeReading replaces an entry's reading with a newer one.
func (s *MergeQueueStore) RecordForgeReading(ctx context.Context, key MergeQueueKey, entryID string, reading MergeQueueForgeReading) error {
	name, err := forgeReadingName(entryID)
	if err != nil {
		return err
	}
	if reading.PullRequest <= 0 || reading.At.IsZero() || (reading.Unread == "") == (strings.TrimSpace(reading.WaitingOn) == "") {
		return errors.New("a GitHub reading names the pull request, when it was read, and either what GitHub said or why it could not be asked")
	}
	if reading.UnreadFor < 0 {
		return errors.New("a GitHub reading cannot have spent negative time unread")
	}
	reading.At = reading.At.UTC()
	reading.WaitingOn = BoundedMergeQueueText(reading.WaitingOn)
	reading.Unread = BoundedMergeQueueText(reading.Unread)
	reading.FailedChecks = boundedNames(reading.FailedChecks)
	reading.PendingChecks = boundedNames(reading.PendingChecks)
	return s.writeSide(ctx, key, name, mergeQueueForgeReading{EntryID: entryID, Reading: reading})
}

// maxForgeReadingChecks bounds the check names one reading keeps.
const maxForgeReadingChecks = 32

func boundedNames(names []string) []string {
	if len(names) > maxForgeReadingChecks {
		names = names[:maxForgeReadingChecks]
	}
	var kept []string
	for _, name := range names {
		kept = append(kept, BoundedMergeQueueText(name))
	}
	return kept
}

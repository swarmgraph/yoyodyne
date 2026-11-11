package runstate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A check on a merge queue candidate's pull request that ended in the runner —
// cancelled, out of time, or never started — judged nothing about the change,
// and the forge holds the merge until it is run again. The queue asks the
// forge to run such a check again once, and keeps a record of each time it
// asked, so a second such ending is answered by withdrawing the merge rather
// than by asking again (orchestrator's mergequeuepromotion.go, awaitForge).
//
// The record is kept beside the queue's own, in a file of its own per entry,
// rather than on the landing record: a build that does not know it reads the
// landing record strictly before writing it, and would refuse the whole
// landing over a field it does not know.

// MaxMergeQueueCheckReruns bounds the re-runs one entry's record keeps. One
// re-run per check per candidate pull request is all the queue asks for, so
// this is many candidates' worth.
const MaxMergeQueueCheckReruns = 32

// MergeQueueCheckRerun is one check on a candidate's pull request the queue
// asked the forge to run again, and how it had ended.
type MergeQueueCheckRerun struct {
	PullRequest int       `json:"pull_request"`
	Check       string    `json:"check"`
	CheckRun    int64     `json:"check_run"`
	Conclusion  string    `json:"conclusion"`
	At          time.Time `json:"at"`
}

// mergeQueueCheckReruns is the record of one entry's re-runs.
type mergeQueueCheckReruns struct {
	EntryID string                 `json:"entry_id"`
	Reruns  []MergeQueueCheckRerun `json:"reruns"`
}

func checkRerunsName(entryID string) (string, error) {
	if !mergeQueueEntryIDPattern.MatchString(entryID) {
		return "", fmt.Errorf("merge queue entry %q is not an entry identifier", entryID)
	}
	return "check-reruns-" + entryID + ".json", nil
}

// CheckReruns is every re-run the queue asked for on an entry's candidates,
// oldest first; none where it never asked for one.
func (s *MergeQueueStore) CheckReruns(key MergeQueueKey, entryID string) ([]MergeQueueCheckRerun, error) {
	name, err := checkRerunsName(entryID)
	if err != nil {
		return nil, err
	}
	var recorded mergeQueueCheckReruns
	found, err := s.readSide(key, name, &recorded)
	if err != nil || !found {
		return nil, err
	}
	if recorded.EntryID != entryID {
		return nil, fmt.Errorf("the check re-runs recorded for merge queue entry %s name entry %q", entryID, recorded.EntryID)
	}
	return recorded.Reruns, nil
}

// RecordCheckRerun adds one re-run to an entry's record. It is written before
// the forge is asked, so a process that stops after asking never asks again.
func (s *MergeQueueStore) RecordCheckRerun(ctx context.Context, key MergeQueueKey, entryID string, rerun MergeQueueCheckRerun) error {
	name, err := checkRerunsName(entryID)
	if err != nil {
		return err
	}
	if rerun.PullRequest <= 0 || strings.TrimSpace(rerun.Check) == "" || rerun.At.IsZero() {
		return errors.New("a check re-run names the pull request, the check, and when it was asked for")
	}
	reruns, err := s.CheckReruns(key, entryID)
	if err != nil {
		return err
	}
	if len(reruns) >= MaxMergeQueueCheckReruns {
		return fmt.Errorf("merge queue entry %s already records %d check re-runs, the most one entry keeps", entryID, len(reruns))
	}
	rerun.At = rerun.At.UTC()
	rerun.Check = BoundedMergeQueueText(rerun.Check)
	reruns = append(reruns, rerun)
	return s.writeSide(ctx, key, name, mergeQueueCheckReruns{EntryID: entryID, Reruns: reruns})
}

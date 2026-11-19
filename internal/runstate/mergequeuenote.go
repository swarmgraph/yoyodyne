package runstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// RetryMergeQueueNote is the write that notes, on the work item, that the
// run's approved change was admitted to the merge queue. It is a boundary of
// its own rather than RetryTrackerWrite because what it writes is already true
// in the queue's own record: the change is safe in the queue whether or not the
// note lands, so a failure here never stops the run, and its waits are not
// counted against the window the run's other tracker writes share.
const RetryMergeQueueNote = "noting on the work item that the approved change was admitted to the merge queue"

// MergeQueueNoteOwed is that note, where the run could not write it: the run
// tried it with backoff for at least as long as one tracker call is allowed,
// then ended waiting on the queue as it would have, leaving the note here.
// Reconciliation writes it, reading the item first so it is never written
// twice, and clears this once it has (MergeQueueNoteRecorded).
type MergeQueueNoteOwed struct {
	// Note is the text owed, exactly as the run would have written it.
	Note string `json:"note"`
	// Since is when the run first tried to write it.
	Since time.Time `json:"since"`
	// Attempts and WaitedSeconds are how many times it has been tried and how
	// long was waited between those tries, the run's and reconciliation's
	// together.
	Attempts      int   `json:"attempts"`
	WaitedSeconds int64 `json:"waited_seconds"`
	// Failure is what the last try failed with.
	Failure string `json:"failure,omitempty"`
}

// OwedMergeQueueNote is the owed note as the record keeps it: the text cut to
// its bound, and the last failure cut to a retry's.
func OwedMergeQueueNote(note string, since time.Time, attempts int, waited time.Duration, failure string) MergeQueueNoteOwed {
	return MergeQueueNoteOwed{
		Note: boundRecordedText(note, MaxBlockerBytes, truncatedNote(MaxBlockerBytes)), Since: since,
		Attempts: attempts, WaitedSeconds: int64(waited / time.Second),
		Failure: boundRecordedText(failure, MaxRetryFailureBytes, ""),
	}
}

// Describe says in plain words what is owed and how hard it has been tried.
func (o MergeQueueNoteOwed) Describe() string {
	says := fmt.Sprintf("the note saying the approved change was admitted to the merge queue is not yet on the work item: it was tried %d time(s) since %s, waiting %s between tries, and reconciliation writes it",
		o.Attempts, o.Since.UTC().Format(time.RFC3339), (time.Duration(o.WaitedSeconds) * time.Second).String())
	if failure := strings.TrimSpace(o.Failure); failure != "" {
		says += "; the last try failed with: " + failure
	}
	return says
}

// Validate refuses an owed note with nothing to write or out of its bounds.
func (o MergeQueueNoteOwed) Validate() error {
	var problems []error
	if strings.TrimSpace(o.Note) == "" {
		problems = append(problems, errors.New("note is required"))
	}
	if len(o.Note) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("note is %d bytes, which exceeds the %d byte bound", len(o.Note), MaxBlockerBytes))
	}
	if o.Since.IsZero() {
		problems = append(problems, errors.New("since is required"))
	}
	if o.Attempts < 0 || o.WaitedSeconds < 0 {
		problems = append(problems, errors.New("attempts and waited_seconds cannot be negative"))
	}
	if len(o.Failure) > MaxRetryFailureBytes {
		problems = append(problems, fmt.Errorf("failure is %d bytes, which exceeds the %d byte bound", len(o.Failure), MaxRetryFailureBytes))
	}
	return errors.Join(problems...)
}

package runstate

// The wait of a change the merge queue handed back for a conflict: decision 1
// of docs/designs/approved-changes-that-no-longer-merge.md. The attempt to bring
// it up to date does not start while an entry the queue would take first, and
// has not finished with, changes any of the conflicting files. The wait lasts at
// most MaxConflictWait from the hand-back, and the run holds no developer slot
// while it lasts, because it has already ended waiting on the queue.
//
// The record is written before it decides anything, so a process that takes the
// queue over goes on from it. A record that cannot be read never refuses the run
// it is on: it reads as one that has ended, and the attempt starts.

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// MaxConflictWait is the longest the wait lasts, from the hand-back.
	MaxConflictWait = 2 * time.Hour
	// MaxConflictWaitAhead bounds how many entries one wait names.
	MaxConflictWaitAhead = 32
	// MaxConflictWaitTextBytes bounds each text a wait records.
	MaxConflictWaitTextBytes = 1 << 10
	// ConflictSlotAskHeld is how long a returned change's request for a developer
	// slot keeps a free one from fresh work, unless it is made again. A change
	// that something else has stopped asking for keeps no slot empty.
	ConflictSlotAskHeld = 5 * time.Minute
)

// ConflictWaitEntry is one entry waited for, and which of the conflicting files
// it changes.
type ConflictWaitEntry struct {
	EntryID    string   `json:"entry_id"`
	Order      uint64   `json:"order"`
	WorkItemID string   `json:"work_item_id"`
	Title      string   `json:"title"`
	Paths      []string `json:"paths"`
}

// ConflictWait is the wait of the change handed back as EntryID.
type ConflictWait struct {
	EntryID string    `json:"entry_id"`
	Since   time.Time `json:"since"`
	Until   time.Time `json:"until"`
	// Ahead are the entries waited for, and Omitted how many more there were. It
	// is empty on a hand-back nothing had to be waited for.
	Ahead   []ConflictWaitEntry `json:"ahead,omitempty"`
	Omitted int                 `json:"omitted,omitempty"`
	// EndedAt is when the wait ended and Ended why. A wait that ended is never
	// begun again for the same hand-back.
	EndedAt *time.Time `json:"ended_at,omitempty"`
	Ended   string     `json:"ended,omitempty"`
	// SlotAskedAt is when the change, its wait over, last found every developer
	// slot taken.
	SlotAskedAt *time.Time `json:"slot_asked_at,omitempty"`
	// Rejoined is the queue entry the change rejoined as, where it merged again
	// and no attempt was made.
	Rejoined string `json:"rejoined,omitempty"`
	// Unreadable is why the recorded wait could not be read.
	Unreadable string `json:"unreadable,omitempty"`
}

// UnmarshalJSON reads a wait, or records why it could not be read.
func (w *ConflictWait) UnmarshalJSON(encoded []byte) error {
	type recorded ConflictWait
	var read recorded
	err := json.Unmarshal(encoded, &read)
	if err == nil {
		err = ConflictWait(read).Validate()
	}
	if err != nil {
		*w = ConflictWait{Unreadable: boundRecordedText(err.Error(), MaxConflictWaitTextBytes, "…")}
		return nil
	}
	*w = ConflictWait(read)
	return nil
}

// Validate reports every contract violation in the record at once.
func (w ConflictWait) Validate() error {
	var problems []error
	if len(w.Ended) > MaxConflictWaitTextBytes || len(w.Unreadable) > MaxConflictWaitTextBytes {
		problems = append(problems, fmt.Errorf("ended and unreadable are at most %d bytes each", MaxConflictWaitTextBytes))
	}
	for _, ahead := range w.Ahead {
		if len(ahead.Title) > MaxConflictWaitTextBytes {
			problems = append(problems, fmt.Errorf("the title of %s is %d bytes, which exceeds the %d byte bound", ahead.EntryID, len(ahead.Title), MaxConflictWaitTextBytes))
		}
	}
	if w.Unreadable != "" {
		return errors.Join(problems...)
	}
	if !mergeQueueEntryIDPattern.MatchString(w.EntryID) {
		problems = append(problems, fmt.Errorf("entry_id %q is not a merge queue entry", w.EntryID))
	}
	if w.Since.IsZero() || w.Until.Before(w.Since) || w.Until.Sub(w.Since) > MaxConflictWait {
		problems = append(problems, fmt.Errorf("a wait ends within %s of the hand-back", MaxConflictWait))
	}
	if len(w.Ahead) > MaxConflictWaitAhead || w.Omitted < 0 {
		problems = append(problems, fmt.Errorf("%d entries and %d omitted are not what a wait keeps", len(w.Ahead), w.Omitted))
	}
	for _, ahead := range w.Ahead {
		if !mergeQueueEntryIDPattern.MatchString(ahead.EntryID) || len(ahead.Paths) == 0 || len(ahead.Paths) > MaxConflictedPaths {
			problems = append(problems, fmt.Errorf("the entry waited for %q does not name its entry and files", ahead.EntryID))
		}
	}
	if (w.EndedAt == nil) != (strings.TrimSpace(w.Ended) == "") {
		problems = append(problems, errors.New("a wait that ended says when and why"))
	}
	if w.EndedAt == nil && (w.SlotAskedAt != nil || w.Rejoined != "") {
		problems = append(problems, errors.New("a change whose wait has not ended asks for no slot and rejoins nothing"))
	}
	return errors.Join(problems...)
}

// Says is what is waited for and until when at the latest, and empty for a wait
// that has ended or could not be read.
func (w ConflictWait) Says() string {
	if w.EndedAt != nil || w.Unreadable != "" || len(w.Ahead) == 0 {
		return ""
	}
	var entries []string
	for _, ahead := range w.Ahead {
		entries = append(entries, fmt.Sprintf("%s (%s), entry %d, which changes %s", ahead.Title, ahead.WorkItemID, ahead.Order, strings.Join(ahead.Paths, ", ")))
	}
	if w.Omitted > 0 {
		entries = append(entries, fmt.Sprintf("and %d more", w.Omitted))
	}
	return fmt.Sprintf("bringing it up to date waits for changes ahead of it in the merge queue that touch its conflicting files: %s; the wait ends at %s at the latest",
		strings.Join(entries, "; "), w.Until.Local().Format("2006-01-02 15:04 MST"))
}

// ConflictWaitFor is the run's wait for one entry's hand-back, where it records
// one. A record that cannot be read answers for every entry, since which one it
// was for is not known.
func (s State) ConflictWaitFor(entryID string) (ConflictWait, bool) {
	if s.ConflictWait == nil || (s.ConflictWait.EntryID != entryID && s.ConflictWait.Unreadable == "") {
		return ConflictWait{}, false
	}
	return *s.ConflictWait, true
}

// ConflictWaitRecorded writes a wait on the run as stored. It is refused on a
// run that is not waiting on the merge queue.
type ConflictWaitRecorded struct {
	Wait ConflictWait
	At   time.Time
}

func (u ConflictWaitRecorded) apply(state *State) (bool, error) {
	if !state.AwaitingMergeQueue() {
		return false, fmt.Errorf("run %s is not waiting on the merge queue, so no wait for the entries ahead of it is recorded", state.RunID)
	}
	if err := u.Wait.Validate(); err != nil {
		return false, fmt.Errorf("the wait of run %s: %w", state.RunID, err)
	}
	wait := u.Wait
	wait.Ahead = slices.Clone(wait.Ahead)
	state.ConflictWait = &wait
	state.UpdatedAt = u.At
	return true, nil
}

// RejoinedMergeQueue records that a change handed back for a conflict merges
// again and is back in the queue at the same approved head. It names the new
// entry, keeps the hand-back among the run's conflicts, and uses no attempt; the
// run's spending is for what it was before, since nothing is spent on the
// conflict. It changes nothing on a run whose record has moved past the entry
// that was handed back.
type RejoinedMergeQueue struct {
	From     string
	Record   MergeQueueAdmissionRecord
	Handback ConflictHandback
	// Wait is the ended wait, naming the entry rejoined as.
	Wait ConflictWait
	At   time.Time
}

func (u RejoinedMergeQueue) apply(state *State) (bool, error) {
	if !state.AwaitingMergeQueue() || state.MergeQueue.EntryID != u.From {
		return false, nil
	}
	if u.Wait.EndedAt == nil || u.Wait.Rejoined != u.Record.EntryID {
		return false, fmt.Errorf("run %s rejoins the merge queue only with its wait ended, naming the entry", state.RunID)
	}
	if err := u.Wait.Validate(); err != nil {
		return false, fmt.Errorf("the wait of run %s: %w", state.RunID, err)
	}
	record, wait, spending := u.Record, u.Wait, state.ConflictSpending
	state.RecordConflictHandback(u.Handback)
	state.ConflictSpending = spending
	state.MergeQueue, state.ConflictWait = &record, &wait
	state.UpdatedAt = u.At
	return true, nil
}

// conflictSlotAsked is when a run still waiting on the queue last asked for a
// developer slot to bring its change up to date, and zero for any other.
func conflictSlotAsked(state State) time.Time {
	if !state.AwaitingMergeQueue() || state.ConflictWait == nil || state.ConflictWait.SlotAskedAt == nil || state.ConflictWait.Rejoined != "" {
		return time.Time{}
	}
	return state.ConflictWait.SlotAskedAt.UTC()
}

// ConflictSlotsAsked is every run whose returned change is owed the next free
// developer slot: its wait is over, and it found every slot taken within
// ConflictSlotAskHeld. A record that will not read is left out.
func (s *Store) ConflictSlotsAsked(now time.Time) ([]State, error) {
	since := now.Add(-ConflictSlotAskHeld)
	return s.indexed("owed a developer slot for a returned change",
		func(entry runIndexEntry) bool { return entry.SlotAskedAt.After(since) },
		func(unreadableRun) bool { return false },
		func(state State) bool { return conflictSlotAsked(state).After(since) })
}

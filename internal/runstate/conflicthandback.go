package runstate

// Each hand-back of a run's change because it no longer merges onto its target
// branch, and which hand-back the run's spending is being made for.
//
// A hand-back says where the conflict was met, the conflicting files, and how
// many lines conflict. It also becomes what the run's next calls to a provider
// are made for: the attempt to bring the change up to date, and every review
// after it, name it on their lines of the cost log (Spend.ConflictHandback),
// so what a conflict cost is read from those lines and nothing else.
//
// That holds until the run's developer is asked for something other than the
// conflict — a failing check, a reviewer's findings, or another move of the
// target. Whether the conflict caused what follows cannot be told, so those
// calls name the hand-back as unclear (SpendConflictUnclear) and are added to
// no conflict's cost.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// MaxConflictHandbacks bounds how many hand-backs one run keeps; the oldest
// are dropped first. It is far past what a run's attempts allow.
const MaxConflictHandbacks = 32

// MaxConflictLinesUnknownBytes bounds why a conflict's lines were not counted.
const MaxConflictLinesUnknownBytes = 1 << 10

// ConflictHandbackFrom is where the conflict was met.
type ConflictHandbackFrom string

const (
	// ConflictFromMergeQueue is the harness's merge queue, which could not merge
	// the approved head onto the target.
	ConflictFromMergeQueue ConflictHandbackFrom = "merge-queue"
	// ConflictFromLanding is the run's own landing, or bringing its waiting pull
	// request up to date, which could not replay the change onto the target.
	ConflictFromLanding ConflictHandbackFrom = "landing"
)

// ConflictHandback is one hand-back of a change for a conflict. ID is the merge
// queue entry's where the queue handed it back, since an entry is handed back
// once, and is otherwise made from the run and the hand-back's number in it.
type ConflictHandback struct {
	ID           string               `json:"id"`
	Number       int                  `json:"number"`
	From         ConflictHandbackFrom `json:"from"`
	At           time.Time            `json:"at"`
	TargetBranch string               `json:"target_branch"`
	TargetCommit string               `json:"target_commit,omitempty"`
	// Paths are the conflicting files, and Omitted how many more the bound
	// dropped. A conflict in something other than content can name none.
	Paths   []string `json:"paths,omitempty"`
	Omitted int      `json:"omitted,omitempty"`
	// Lines is how many lines of either side conflict. It is absent where that
	// is not known, and LinesUnknown then says why.
	Lines        *int   `json:"lines,omitempty"`
	LinesUnknown string `json:"lines_unknown,omitempty"`
}

// ConflictSpending is the hand-back the run's spending is being made for.
// Unclear says the run's developer has since been asked for something else, or
// was started on another run's kept change, so whether the conflict caused the
// spending cannot be told. Handback is empty only where no recorded hand-back
// can be named, which is always unclear.
type ConflictSpending struct {
	Handback string `json:"handback,omitempty"`
	Unclear  bool   `json:"unclear,omitempty"`
}

// BoundConflictPaths is the conflicting files a record keeps, and how many more
// there were.
func BoundConflictPaths(paths []string) ([]string, int) {
	kept := paths[:min(len(paths), MaxConflictedPaths)]
	return append([]string(nil), kept...), len(paths) - len(kept)
}

// RecordConflictHandback adds one hand-back, with its number and, where it has
// none, its id, and makes it what the run's spending is for. A hand-back the
// run already records, given again, changes nothing. It returns the id.
func (s *State) RecordConflictHandback(handback ConflictHandback) string {
	for _, earlier := range s.ConflictHandbacks {
		if handback.ID != "" && earlier.ID == handback.ID {
			return handback.ID
		}
	}
	handback.Number = 1
	if count := len(s.ConflictHandbacks); count > 0 {
		handback.Number = s.ConflictHandbacks[count-1].Number + 1
	}
	if handback.ID == "" {
		handback.ID = fmt.Sprintf("%s-conflict-%d", s.RunID, handback.Number)
	}
	handback.Paths = append([]string(nil), handback.Paths...)
	if handback.Lines == nil && strings.TrimSpace(handback.LinesUnknown) == "" {
		handback.LinesUnknown = "the lines were not counted"
	}
	handback.LinesUnknown = boundRecordedText(handback.LinesUnknown, MaxConflictLinesUnknownBytes, "…")
	kept := append(slices.Clone(s.ConflictHandbacks), handback)
	if extra := len(kept) - MaxConflictHandbacks; extra > 0 {
		kept = kept[extra:]
	}
	s.ConflictHandbacks = kept
	s.ConflictSpending = &ConflictSpending{Handback: handback.ID}
	return handback.ID
}

// ConflictSpendingUnclear records that what the run does from here may or may
// not be the conflict's doing. It changes nothing on a run no conflict was
// handed back to.
func (s *State) ConflictSpendingUnclear() {
	if s.ConflictSpending == nil || s.ConflictSpending.Unclear {
		return
	}
	s.ConflictSpending = &ConflictSpending{Handback: s.ConflictSpending.Handback, Unclear: true}
}

// SpendsForConflict is what the run's next line in the cost log says of
// conflicts: which of the recorded answers it is, and the hand-back it names.
func (s State) SpendsForConflict() (SpendConflict, string) {
	switch {
	case s.ConflictSpending == nil:
		return SpendConflictNone, ""
	case s.ConflictSpending.Unclear:
		return SpendConflictUnclear, s.ConflictSpending.Handback
	}
	return SpendConflictHandback, s.ConflictSpending.Handback
}

// lastConflictHandback is when the run was last handed back for a conflict, and
// zero where it never was.
func lastConflictHandback(state State) time.Time {
	if len(state.ConflictHandbacks) == 0 {
		return time.Time{}
	}
	return state.ConflictHandbacks[len(state.ConflictHandbacks)-1].At.UTC()
}

// ConflictHandbacksSince is every run handed back for a conflict at or after a
// moment, found through the run index. A record that will not read is left out
// rather than refusing the listing: it is figures, not a claim on work.
func (s *Store) ConflictHandbacksSince(since time.Time) ([]State, error) {
	return s.indexed("handed back for a conflict since "+since.UTC().Format(time.RFC3339),
		func(entry runIndexEntry) bool {
			return !entry.ConflictAt.IsZero() && !entry.ConflictAt.Before(since)
		},
		func(unreadableRun) bool { return false },
		func(state State) bool {
			at := lastConflictHandback(state)
			return !at.IsZero() && !at.Before(since)
		})
}

func (s State) validateConflictHandbacks() []error {
	var problems []error
	if len(s.ConflictHandbacks) > MaxConflictHandbacks {
		problems = append(problems, fmt.Errorf("conflict_handbacks holds %d entries, which exceeds the %d entry bound", len(s.ConflictHandbacks), MaxConflictHandbacks))
	}
	for index, handback := range s.ConflictHandbacks {
		if err := handback.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("conflict_handbacks[%d]: %w", index, err))
		}
	}
	if spending := s.ConflictSpending; spending != nil && strings.TrimSpace(spending.Handback) == "" && !spending.Unclear {
		problems = append(problems, errors.New("conflict_spending names the hand-back it is for"))
	}
	return problems
}

// Validate reports every contract violation in the record at once.
func (h ConflictHandback) Validate() error {
	var problems []error
	if strings.TrimSpace(h.ID) == "" || h.Number < 1 {
		problems = append(problems, errors.New("id and number are required"))
	}
	if h.From != ConflictFromMergeQueue && h.From != ConflictFromLanding {
		problems = append(problems, fmt.Errorf("from %q is not somewhere a conflict is met", h.From))
	}
	if h.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if strings.TrimSpace(h.TargetBranch) == "" {
		problems = append(problems, errors.New("target_branch is required"))
	}
	if h.TargetCommit != "" && !commitPattern.MatchString(h.TargetCommit) {
		problems = append(problems, errors.New("target_commit must be a full commit hash"))
	}
	if len(h.Paths) > MaxConflictedPaths || h.Omitted < 0 {
		problems = append(problems, fmt.Errorf("%d conflicting paths and %d omitted are not what a hand-back keeps", len(h.Paths), h.Omitted))
	}
	problems = append(problems, validConflictLines(h.Lines, h.LinesUnknown))
	if h.Lines == nil && strings.TrimSpace(h.LinesUnknown) == "" {
		problems = append(problems, errors.New("a conflict whose lines are not known says why"))
	}
	return errors.Join(problems...)
}

// validConflictLines reports a count of conflicting lines that disagrees with
// itself. Both absent is a record from before the lines were counted.
func validConflictLines(lines *int, unknown string) error {
	switch {
	case lines != nil && (*lines < 1 || unknown != ""):
		return errors.New("a known count of conflicting lines is at least one and does not also say why it is unknown")
	case len(unknown) > MaxConflictLinesUnknownBytes:
		return fmt.Errorf("lines_unknown is %d bytes, which exceeds the %d byte bound", len(unknown), MaxConflictLinesUnknownBytes)
	}
	return nil
}

// SpendConflict is what a line of the cost log says of merge conflicts.
type SpendConflict string

const (
	// SpendConflictNotRecorded is a line written before this was recorded, or
	// for an invocation that belongs to no run.
	SpendConflictNotRecorded SpendConflict = ""
	// SpendConflictNone is a run's invocation made for no conflict.
	SpendConflictNone SpendConflict = "none"
	// SpendConflictHandback is the attempt a hand-back for a conflict caused, or
	// a review after it.
	SpendConflictHandback SpendConflict = "handback"
	// SpendConflictUnclear is an invocation after a hand-back for a conflict
	// that something else also came before, so it cannot be told apart.
	SpendConflictUnclear SpendConflict = "unclear"
)

// conflictProblem reports a line whose account of conflicts disagrees with
// itself.
func (s Spend) conflictProblem() error {
	named := strings.TrimSpace(s.ConflictHandback) != ""
	switch s.Conflict {
	case SpendConflictNotRecorded, SpendConflictNone:
		if named {
			return errors.New("only a line made for or after a conflict names a hand-back")
		}
	case SpendConflictHandback:
		if !named {
			return errors.New("a line made for a conflict names the hand-back that caused it")
		}
	case SpendConflictUnclear:
	default:
		return fmt.Errorf("conflict %q is not one this build knows", s.Conflict)
	}
	if s.Conflict != SpendConflictNotRecorded && strings.TrimSpace(s.RunID) == "" {
		return errors.New("only a run's spend says whether it was made for a conflict")
	}
	return nil
}

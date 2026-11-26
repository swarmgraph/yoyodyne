package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// EpicCorrections is where a piece the development manager writes to correct a
// check of the whole epic is linked to that check, in the epic's progress. The
// joining code reads that link and never the item's words
// (docs/designs/epic-mode-second-part.md, "How a correcting piece names the check
// it came from"). It is satisfied by *epicprogress.Store, and a conversation
// without one refuses a creation naming a check.
type EpicCorrections interface {
	// MayCorrect is why no piece may be written for the check, and nil where a
	// correction is owed for it and none has been written.
	MayCorrect(epic, check string) error
	// Correcting writes the piece into the owed correction and answers the note of
	// fixed form to write on the piece afterwards.
	Correcting(epic, check, piece string) (note string, err error)
	// Noted writes that the note is on the piece.
	Noted(epic, check string) error
}

// epicCheckProblems refuses a creation naming a check that cannot be read as one.
func (a TrackerAction) epicCheckProblems() []error {
	check := strings.TrimSpace(a.EpicCheck)
	switch {
	case check == "":
		return nil
	case a.parent() == "":
		return []error{errors.New(`epic_check: a correcting piece names the epic as its "parent"`)}
	case strings.ContainsAny(check, " \t\r\n"):
		return []error{fmt.Errorf("epic_check: %q is not a check's identifier; name it exactly as status shows it", check)}
	}
	return nil
}

// mayCorrectCheck is why a creation naming a check is refused before anything is
// created, and empty where it may go ahead or names none.
func (s *Session) mayCorrectCheck(action TrackerAction) string {
	check := strings.TrimSpace(action.EpicCheck)
	switch {
	case check == "":
		return ""
	case s.state.Role != domain.RoleDevelopmentManager:
		return "only the development manager writes the piece that corrects a check of the whole epic"
	case s.options.EpicCorrections == nil:
		return "no record of the epics' progress is wired to this conversation, so no piece can be linked to a check"
	}
	if err := s.options.EpicCorrections.MayCorrect(action.parent(), check); err != nil {
		return "refused the correcting piece: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	return ""
}

// linkCorrection writes the created piece into the owed correction first and the
// note on the item second, and says how that went in the words reported for the
// creation. A stop between the two leaves the note to the joining code's next
// reading.
func (s *Session) linkCorrection(ctx context.Context, action TrackerAction, piece string) string {
	check := strings.TrimSpace(action.EpicCheck)
	if check == "" {
		return ""
	}
	epic := action.parent()
	note, err := s.options.EpicCorrections.Correcting(epic, check, piece)
	if err != nil {
		return fmt.Sprintf(", and it was NOT linked to the check %s of epic %s, so it does not join as a correction: %s", check, epic, singleLine(err.Error(), maxTrackerFailureBytes))
	}
	linked := fmt.Sprintf(", as the correction owed by the check %s of epic %s", check, epic)
	if _, err := s.options.Tracker.Update(ctx, piece, beads.WorkItemChange{AppendNotes: note}); err != nil {
		return linked + "; its note is written later, because writing it failed: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	if err := s.options.EpicCorrections.Noted(epic, check); err != nil {
		return linked + "; that its note is written could not be recorded, so it may be written again: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	return linked
}

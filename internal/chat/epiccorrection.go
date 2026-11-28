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

// EpicAnswers is, by report identifier, why a question about scope the harness
// filed needs nothing more, because a judgment made once more found its piece
// inside and it joined. It is satisfied by *epicprogress.Store.
type EpicAnswers interface {
	AnsweredByRereading() (map[string]string, error)
}

// epicAnswers reads them once for a turn. Where they cannot be read nothing is
// marked, and the problem is said once under the reports.
func (s *Session) epicAnswers() (map[string]string, string) {
	if s.options.EpicAnswers == nil {
		return nil, ""
	}
	answered, err := s.options.EpicAnswers.AnsweredByRereading()
	if err != nil {
		return nil, "Which questions about scope a judgment made once more has already answered could not be read, so none is marked: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	return answered, ""
}

// EpicCommentCorrections is the same link for a piece correcting feedback on a
// member's merged pull request, named by the comment's forge identifier
// (docs/designs/epic-mode-second-part.md, point 6 of "How a correcting piece
// names the check it came from"). It is satisfied by *epicprogress.Store, and a
// conversation whose EpicCorrections lacks it refuses a creation naming a
// comment.
type EpicCommentCorrections interface {
	MayCorrectComment(epic, comment string) error
	CorrectingComment(epic, comment, piece string) (note string, err error)
	NotedComment(epic, comment string) error
}

// epicCheckProblems refuses a creation naming a check or a review comment that
// cannot be read as one, or naming both.
func (a TrackerAction) epicCheckProblems() []error {
	var problems []error
	for _, named := range []struct{ field, value, what string }{
		{"epic_check", a.EpicCheck, "a check's identifier"},
		{"epic_comment", a.EpicComment, "a review comment's identifier"},
	} {
		value := strings.TrimSpace(named.value)
		switch {
		case value == "":
		case a.parent() == "":
			problems = append(problems, fmt.Errorf(`%s: a correcting piece names the epic as its "parent"`, named.field))
		case strings.ContainsAny(value, " \t\r\n"):
			problems = append(problems, fmt.Errorf("%s: %q is not %s; name it exactly as status shows it", named.field, value, named.what))
		}
	}
	if strings.TrimSpace(a.EpicCheck) != "" && strings.TrimSpace(a.EpicComment) != "" {
		problems = append(problems, errors.New("epic_check and epic_comment: a correcting piece corrects one check or one review comment, not both"))
	}
	return problems
}

// correctionLink is what one creation links its piece to: a check of the whole
// epic, or a review comment on a member's merged pull request.
type correctionLink struct {
	id, what string
	may      func(epic, id string) error
	link     func(epic, id, piece string) (string, error)
	noted    func(epic, id string) error
}

// correctionOf is the link a creation names, and why none can be made where it
// names one this conversation cannot link; empty id is a creation naming none.
func (s *Session) correctionOf(action TrackerAction) (correctionLink, string) {
	if check := strings.TrimSpace(action.EpicCheck); check != "" {
		if s.options.EpicCorrections == nil {
			return correctionLink{}, "no record of the epics' progress is wired to this conversation, so no piece can be linked to a check"
		}
		c := s.options.EpicCorrections
		return correctionLink{check, "the check " + check, c.MayCorrect, c.Correcting, c.Noted}, ""
	}
	comment := strings.TrimSpace(action.EpicComment)
	if comment == "" {
		return correctionLink{}, ""
	}
	c, can := s.options.EpicCorrections.(EpicCommentCorrections)
	if !can {
		return correctionLink{}, "no record of the epics' progress is wired to this conversation, so no piece can be linked to a review comment"
	}
	return correctionLink{comment, "the review comment " + comment, c.MayCorrectComment, c.CorrectingComment, c.NotedComment}, ""
}

// mayCorrectCheck is why a creation naming a check or a review comment is refused
// before anything is created, and empty where it may go ahead or names neither.
func (s *Session) mayCorrectCheck(action TrackerAction) string {
	if strings.TrimSpace(action.EpicCheck) == "" && strings.TrimSpace(action.EpicComment) == "" {
		return ""
	}
	if s.state.Role != domain.RoleDevelopmentManager {
		if strings.TrimSpace(action.EpicCheck) == "" {
			return "only the development manager writes the piece that corrects a review comment on a member of the epic"
		}
		return "only the development manager writes the piece that corrects a check of the whole epic"
	}
	link, refused := s.correctionOf(action)
	if refused != "" {
		return refused
	}
	if err := link.may(action.parent(), link.id); err != nil {
		return "refused the correcting piece: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	return ""
}

// linkCorrection writes the created piece into the owed correction first and the
// note on the item second, and says how that went in the words reported for the
// creation. A stop between the two leaves the note to the joining code's next
// reading.
func (s *Session) linkCorrection(ctx context.Context, action TrackerAction, piece string) string {
	link, refused := s.correctionOf(action)
	if link.id == "" || refused != "" {
		return ""
	}
	epic := action.parent()
	note, err := link.link(epic, link.id, piece)
	if err != nil {
		return fmt.Sprintf(", and it was NOT linked to %s of epic %s, so it does not join as a correction: %s", link.what, epic, singleLine(err.Error(), maxTrackerFailureBytes))
	}
	linked := fmt.Sprintf(", as the correction owed by %s of epic %s", link.what, epic)
	if _, err := s.options.Tracker.Update(ctx, piece, beads.WorkItemChange{AppendNotes: note}); err != nil {
		return linked + "; its note is written later, because writing it failed: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	if err := link.noted(epic, link.id); err != nil {
		return linked + "; that its note is written could not be recorded, so it may be written again: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	return linked
}

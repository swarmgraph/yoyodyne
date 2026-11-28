package report

// What a run's closing lines and the item's notes keep of a report the harness
// could not accept, and of one no pass will read (docs/designs/
// reports-nobody-reads.md, Decision 3).
//
// A report is refused only for what the harness cannot accept as written: a
// message past the length limit, a severity not on the list, or an addressee
// that is not a role. Nothing is refused for lack of a reader. A refusal still
// keeps the fact the run noticed: the message it gave is copied, cut to the
// report length limit with the cut said, into what the run prints when it ends
// and into the work item's notes, so the item's record holds it even though the
// pile does not.

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Refused is one entry of a report block the harness did not accept, with why
// and as much of what it said as a report may hold.
type Refused struct {
	// Index is the entry's place in its block, from zero.
	Index int `json:"index"`
	// Severity and For are as the entry wrote them, whether or not either is
	// one the contract accepts: what was wrong may be one of them.
	Severity Severity         `json:"severity,omitempty"`
	For      domain.AgentRole `json:"for,omitempty"`
	// Message is the message the entry gave, cut to MaxMessageBytes, and
	// CutBytes how many bytes past that were left out.
	Message  string `json:"message,omitempty"`
	CutBytes int    `json:"cut_bytes,omitempty"`
	// Why is every contract violation in the entry, in one line.
	Why string `json:"why"`
}

// refuse records one entry the contract did not accept.
func refuse(index int, entry Entry, problems []error) Refused {
	message, cut := cutMessage(strings.TrimSpace(entry.Message), MaxMessageBytes)
	why := make([]string, len(problems))
	for i, problem := range problems {
		why[i] = problem.Error()
	}
	return Refused{
		Index:    index,
		Severity: entry.Severity,
		For:      entry.For,
		Message:  message,
		CutBytes: cut,
		Why:      strings.Join(why, "; "),
	}
}

// cutMessage keeps at most limit bytes of a message, ending on a whole
// character, and says how many bytes it left out.
func cutMessage(message string, limit int) (string, int) {
	if len(message) <= limit {
		return message, 0
	}
	kept := message[:limit]
	for len(kept) > 0 && !utf8.ValidString(kept) {
		kept = kept[:len(kept)-1]
	}
	return kept, len(message) - len(kept)
}

// refusedError is the refusals of a block as one error, worded as a block
// refused whole was before entries were refused one at a time, for the callers
// that still refuse a block whole.
func refusedError(refused []Refused) error {
	problems := make([]error, len(refused))
	for i, entry := range refused {
		problems[i] = fmt.Errorf("reports[%d]: invalid report: %s", entry.Index, entry.Why)
	}
	return fmt.Errorf("invalid reports: %w", errors.Join(problems...))
}

// Says is the closing line for one refusal: what was wrong, and that the fact
// is kept.
func (r Refused) Says(role domain.AgentRole) string {
	return fmt.Sprintf("a %s report was refused: %s; what it said is kept in this run's summary and in the work item's notes", role, r.Why)
}

// Whole is the refused report as the run prints it when it ends and as the
// item's notes keep it: its severity and addressee as written, and its message,
// with the cut said where it was cut.
func (r Refused) Whole(role domain.AgentRole) string {
	severity := string(r.Severity)
	if severity == "" {
		severity = "no severity"
	}
	whom := "naming no role"
	if r.For != "" {
		whom = fmt.Sprintf("for %q", string(r.For))
	}
	message := r.Message
	if message == "" {
		message = "(no message)"
	}
	said := fmt.Sprintf("refused %s report from the %s, %s: %s", severity, role, whom, message)
	if r.CutBytes > 0 {
		said += fmt.Sprintf(" [cut: the last %d bytes were left out to keep it within the %d-byte report limit]", r.CutBytes, MaxMessageBytes)
	}
	return said
}

// Whole is a filed report as the run that filed it prints it when it ends,
// where no pass reads it: its severity, its addressee, its identifier, and its
// message whole.
func (r Report) Whole() string {
	return fmt.Sprintf("%s report %s, for the %s, which no pass reads here: %s",
		r.Severity, r.ID, addresseeName(namedAddressee(r)), r.Message)
}

// ClosingBlock is the block a run prints after its outcome and keeps in the
// item's notes, holding the reports it filed that reach no pass and then the
// messages of the reports that were refused, in that order. It is empty where
// there is neither.
func ClosingBlock(unread []Report, refused []RefusedBy) string {
	if len(unread) == 0 && len(refused) == 0 {
		return ""
	}
	var lines []string
	if len(unread) > 0 {
		lines = append(lines, "Reports this run filed that no pass reads here, the Lead Product Manager's included; nobody but a person will read them:")
		for _, reported := range unread {
			lines = append(lines, "- "+reported.Whole())
		}
	}
	if len(refused) > 0 {
		lines = append(lines, "Reports this run gave that were refused, kept here so what they said is not lost:")
		for _, entry := range refused {
			lines = append(lines, "- "+entry.Refused.Whole(entry.Role))
		}
	}
	return strings.Join(lines, "\n")
}

// RefusedBy is a refused report with the role whose reply carried it.
type RefusedBy struct {
	Role    domain.AgentRole `json:"role"`
	Refused Refused          `json:"refused"`
}

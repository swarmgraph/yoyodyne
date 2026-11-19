package intakerules

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Standing is what a query keeps between its runs about its rules document:
// whether a report about the document is already filed and not yet answered by
// the document becoming readable again. The query run keeps it with the rest of
// the query's own state; nothing here stores it.
type Standing struct {
	// Reported is the commit at which the document was last reported as
	// unreadable, and is empty while no such report stands.
	Reported string `json:"reported,omitempty"`
}

// Verdict is what one query run does with its rules as it read them.
type Verdict struct {
	// ReadersMayRun is false while the document cannot be read as rules. The
	// query's messages are then left recorded and unread, and nothing is marked
	// as decided, so they are read once the document is fixed and nothing is
	// lost.
	ReadersMayRun bool
	// Report is the one report to file for the Lead Product Manager, and nil
	// where none is due: the document is readable, or a report about it already
	// stands.
	Report *report.Entry
	// Standing is what the query keeps for its next run.
	Standing Standing
}

// maxReportedRefusals bounds how many rules a report lists by name, so a long
// document of broken rules still reads as one report.
const maxReportedRefusals = 5

// Judge says whether a query's readers may run on the rules it read, and
// whether a report is due. A document that cannot be read is reported once,
// when it is first found so, and not again on every run while it stays that
// way; once it reads again, a later break is reported afresh.
//
// The report holds what the document says and where it is, and nothing from
// any message, so it can be put in front of the Lead Product Manager, who owns
// the rules and can mend them.
func Judge(rules Rules, standing Standing) Verdict {
	if rules.Unreadable == "" {
		return Verdict{ReadersMayRun: true}
	}
	if standing.Reported != "" {
		return Verdict{Standing: standing}
	}
	entry := report.Entry{
		Severity: report.SeverityWarning,
		For:      domain.RoleProductManager,
		Message:  unreadableMessage(rules),
	}
	return Verdict{Report: &entry, Standing: Standing{Reported: rules.Commit}}
}

func unreadableMessage(rules Rules) string {
	var message strings.Builder
	fmt.Fprintf(&message, "The rules document %s, which intake query %s judges its messages by, cannot be read as rules as %s holds it at %s: %s. ",
		rules.Path, rules.Query, rules.Branch, short(rules.Commit), rules.Unreadable)
	if len(rules.Refused) > 0 {
		shown := rules.Refused
		if len(shown) > maxReportedRefusals {
			shown = shown[:maxReportedRefusals]
		}
		parts := make([]string, 0, len(shown))
		for _, refusal := range shown {
			parts = append(parts, refusal.String())
		}
		fmt.Fprintf(&message, "Refused: %s", strings.Join(parts, "; "))
		if more := len(rules.Refused) - len(shown); more > 0 {
			fmt.Fprintf(&message, "; and %d more", more)
		}
		message.WriteString(". ")
	}
	message.WriteString("No message from this query is read until the document on the target branch can be read again; the messages wait and none is lost.")
	text := message.String()
	if len(text) > report.MaxMessageBytes {
		text = clipBytes(text, report.MaxMessageBytes)
	}
	return text
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func clipBytes(text string, limit int) string {
	const tail = "..."
	cut := limit - len(tail)
	for cut > 0 && (cut >= len(text) || text[cut]&0xC0 == 0x80) {
		cut--
	}
	return text[:cut] + tail
}

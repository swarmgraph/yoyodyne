package readmodel

// A report waiting on a role, as the attention line lists it.
//
// A report is said to a role. Where the reporter named one, the report is that
// role's whatever its severity; where it named none, it is the Lead Product
// Manager's, because she handles the pile. Either way it is listed under that
// role and never under the operator: a critical report that reached the
// operator as something only a person could act on, when what it asked for was
// the development manager's re-run, is the misrouting this exists to end. The
// operator is named only where a handling of the report records that a person
// must act, and that is operatoraction.go's.
//
// Two kinds of unhandled report are listed. One addressed to a role is, so the
// role finds it on its own list. A critical one addressed to none is, so it
// stands out under the Lead Product Manager's head as well as reaching her pass
// at once. An unaddressed warning or note is left to the pile's count, which is
// how it has always waited for her.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// addressedReportEnds is what ends the entry, worded once for the line and the
// channel alike.
const addressedReportEnds = "a handling of the report records what became of it"

// AddressedReports is every unhandled report waiting on a role by name, oldest
// first: each one addressed to a role, and each critical one addressed to none.
// A product pass's failure is left out, as OperatorActions leaves it out: its
// own single entry carries it.
func AddressedReports(reports []report.Report, handlings []report.Handling) []report.Report {
	var waiting []report.Report
	for _, reported := range report.ByFiling(report.Unhandled(reports, handlings)) {
		if reported.PassFailureTask != "" {
			continue
		}
		if reported.For != "" || reported.Severity == report.SeverityCritical {
			waiting = append(waiting, reported)
		}
	}
	return waiting
}

// ReportsAddressedTo is the unhandled reports addressed to one role by name,
// oldest first, for that role's own pass. The Lead Product Manager's unaddressed
// reports are not among them: she is shown the whole pile.
func ReportsAddressedTo(role Mover, reports []report.Report, handlings []report.Handling) []report.Report {
	var addressed []report.Report
	for _, reported := range AddressedReports(reports, handlings) {
		if reported.For != "" && MoverOf(reported.Addressee()) == role {
			addressed = append(addressed, reported)
		}
	}
	return addressed
}

// addressedReportAttention is one report waiting on a role, as the attention
// line carries it.
func addressedReportAttention(reported report.Report) Attention {
	return resolved(Attention{
		Kind:       AttentionAddressedReport,
		ID:         reported.ID,
		WorkItemID: reported.WorkItemID,
		Report:     &reported,
	})
}

// readAddressedReports is the entries for the pile as one reading has it. A
// pile that could not be read lists none, and the pile's own problem says so.
func readAddressedReports(reports []report.Report, handlings []report.Handling, pileProblem string) []Attention {
	if pileProblem != "" {
		return nil
	}
	waiting := AddressedReports(reports, handlings)
	attention := make([]Attention, 0, len(waiting))
	for _, reported := range waiting {
		attention = append(attention, addressedReportAttention(reported))
	}
	return attention
}

// addressedReportSays is the report as the line names it: which report, how
// severe, who filed it and about what, and what it says.
func addressedReportSays(reported report.Report) string {
	reporter := "the " + reported.Role.Title()
	if reported.Role == report.HarnessReporter {
		reporter = "the harness itself"
	}
	what := fmt.Sprintf("%s, a %s report from %s", reported.ID, reported.Severity, reporter)
	if item := strings.TrimSpace(reported.WorkItemID); item != "" {
		what += " about " + item
	}
	return what + ": " + singleLine(reported.ExpandedMessage(), maxRefusalBytes)
}

// AddressedReportWhose is whose move a report waiting on a role is and what
// ends it: the role it is with, and that it is not the operator's. The line and
// the message that says the report in the channel both close on it, so the two
// name the same role.
func AddressedReportWhose(reported report.Report) string {
	mover := ownership.Resolve(ownership.KindAddressedReport, ownership.Record{Role: reported.Addressee()}).Owner
	if reported.For != "" {
		return fmt.Sprintf("%s — the report is addressed to the %s and is with that role, not the operator; %s",
			mover.Possessive(), reported.Addressee().Title(), addressedReportEnds)
	}
	return fmt.Sprintf("%s — the report names no role, so it is with the Lead Product Manager, who handles the reports, not the operator; %s",
		mover.Possessive(), addressedReportEnds)
}

package sweep

// What a pass decided about each report addressed to its role that the harness
// put in front of it.
//
// A report addressed to a role is put in front of that role's next pass, and
// until this existed a pass could read it, act on the work item it named, and
// end its pass without a word about the report: nothing recorded that it had
// been seen, and nothing could tell a report three passes had stepped over from
// one no pass had yet been shown. So a pass now says, for every such report,
// what it decided or that it left the report for now, and the harness records
// which reports each pass was shown (runstate.Sweep.Addressed). A report shown
// to a pass whose account says neither is a report that pass passed over, which
// is what `yoyo status` counts (readmodel.ReportsPassedOver).

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/report"
)

// MaxReportDecisions bounds how many reports one turn may account for. It is
// the most reports addressed to a role that the harness shows one pass, so a
// pass can always account for every report it was shown.
const (
	MaxReportDecisions     = 20
	MaxPassReportDecisions = MaxReportDecisions * MaxMergedTurns
	maxReportDecisionsText = "20"
)

// ReportDecision is what a pass did about one report addressed to its role:
// what it decided, or that it left the report for now and why. Exactly one of
// the two.
type ReportDecision struct {
	// Report is the report, by its identifier.
	Report string `json:"report"`
	// Decision is what the role decided about the report and did, in a sentence.
	Decision string `json:"decision,omitempty"`
	// Left is why the role left the report for now without deciding it.
	Left string `json:"left,omitempty"`
}

// Validate reports every contract violation in the entry at once.
func (d ReportDecision) Validate() error {
	var problems []error
	if !report.ValidID(d.Report) {
		problems = append(problems, fmt.Errorf("report %q is not a report's identifier", d.Report))
	}
	decision, left := strings.TrimSpace(d.Decision), strings.TrimSpace(d.Left)
	switch {
	case decision == "" && left == "":
		problems = append(problems, errors.New("a report carries what was decided about it or why it was left, and this carries neither"))
	case decision != "" && left != "":
		problems = append(problems, errors.New("a report was decided or left, not both"))
	}
	for field, text := range map[string]string{"decision": decision, "left": left} {
		if len(text) > maxLeftBytes {
			problems = append(problems, fmt.Errorf("%s is %d bytes, limit is %d", field, len(text), maxLeftBytes))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid report entry: %w", err)
	}
	return nil
}

// ReportDecided is what the account says the pass did about one report, and
// false where it says nothing. A later entry stands over an earlier one, as
// LeftReason reads the last.
func (r Result) ReportDecided(id string) (ReportDecision, bool) {
	id = strings.TrimSpace(id)
	var decided ReportDecision
	found := false
	for _, entry := range r.Reports {
		if strings.TrimSpace(entry.Report) == id {
			decided, found = entry, true
		}
	}
	return decided, found
}

// ReportsContract is what a pass is told about the "reports" field when the
// harness has put reports addressed to its role in front of it. It is a
// paragraph of its own, as the left contract is, because most passes are shown
// no such report.
func ReportsContract() string {
	return strings.Join([]string{
		`Your block also carries "reports": one entry for every report listed under "Reports addressed to you" or shown beside a work item, at most ` + maxReportDecisionsText + ` in one turn, each saying what you decided about it or that you left it for now and why:`,
		"",
		`"reports":[{"report":"the report's identifier","decision":"what you decided and did about it, in a sentence"},{"report":"the report's identifier","left":"why you did not decide it on this pass"}]`,
		"",
		"The harness records which reports each pass was shown and what its account said about each. A report your pass was shown and says nothing about is recorded as passed over, and once three of your passes have passed over one, `yoyo status` says so beside the report.",
	}, "\n")
}

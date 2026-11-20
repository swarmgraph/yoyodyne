package orchestrator

// A role's decision that needs the operator's hand, where no work item is
// admitted to give the harness the capability it lacks, is told to the Lead
// Product Manager as well as shown on his line.
//
// The ownership design says a hand names the item that ends the need for it,
// and where none is admitted the entry is also shown to her as a missing
// capability, which she admits a defect for
// (docs/designs/ownership-and-the-operator.md, "A role's decision that needs
// the operator's hand"). Without that, the entry stands on the operator's line
// with nobody told to admit the work that would take it off.
//
// It is told as the harness's own report, addressed to her, filed before each
// of her scheduled passes for every such entry not yet reported. The report
// pile is the record of what was said, as it is for the other reports the
// harness files: a report is filed under the entry's key, and an entry whose
// key a report already carries is not reported again, however many passes read
// it. An entry that later names an item to close it is not reported, and a
// report about it that she has not yet handled is recorded as needing nothing,
// naming the item, so it stops being put in front of her.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// missingCapabilityRun is what the harness's report about one entry is filed
// under, before the entry's key: it is how a later pass finds it was filed.
const missingCapabilityRun = "missing-capability@"

// HandEntries reads the entries on the operator's needs-a-human line, as `yoyo
// status` reads them, and what stopped the reading where something did.
type HandEntries func(ctx context.Context) ([]readmodel.Attention, string)

// MissingCapabilityReports is the pile the reports are filed in and read back
// from, and where the harness records one as needing nothing once an item
// closes its entry. It is satisfied by *runstate.ReportStore.
type MissingCapabilityReports interface {
	List() ([]report.Report, error)
	Handlings() ([]report.Handling, error)
	Append(reported report.Report) error
	Handle(handling report.Handling) error
}

// missingCapabilitiesWired reports a trigger that tells the Lead Product
// Manager about missing capabilities.
func (t Trigger) missingCapabilitiesWired() bool {
	return t.HandEntries != nil && t.CapabilityReports != nil
}

// reportMissingCapabilities files the harness's report to the Lead Product
// Manager for each entry needing the operator's hand that names no item to end
// it and has not been reported, and withdraws an unhandled report whose entry
// now names one. Nothing is done for any other role's pass. What stops it is
// returned for the pass's message to say, and the next pass tries again.
func (t Trigger) reportMissingCapabilities(ctx context.Context, role domain.AgentRole) string {
	if !t.missingCapabilitiesWired() || role != domain.RoleProductManager {
		return ""
	}
	entries, readProblem := t.HandEntries(ctx)
	var problems []string
	if readProblem != "" {
		problems = append(problems, "the needs-a-human line was read only in part: "+readProblem)
	}
	reports, err := t.CapabilityReports.List()
	if err != nil {
		// Without the reports already filed there is no saying which entries
		// were reported, and filing them all again every pass is what this must
		// not do.
		return joinMissingCapabilityProblems(append(problems, fmt.Sprintf("the reports already filed could not be read, so none was filed: %v", err)))
	}
	filed := map[string]report.Report{}
	for _, reported := range reports {
		if reported.Role == report.HarnessReporter && strings.HasPrefix(reported.RunID, missingCapabilityRun) {
			filed[strings.TrimPrefix(reported.RunID, missingCapabilityRun)] = reported
		}
	}
	now := t.now()
	var closed []readmodel.Attention
	for _, entry := range entries {
		key := entry.HandKey()
		if key == "" {
			continue
		}
		if !entry.LacksClosingItem() {
			if _, was := filed[key]; was {
				closed = append(closed, entry)
			}
			continue
		}
		if _, was := filed[key]; was {
			continue
		}
		if err := t.fileMissingCapability(entry, now); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(closed) > 0 {
		if err := t.withdrawMissingCapabilities(closed, filed, now); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return joinMissingCapabilityProblems(problems)
}

// fileMissingCapability files the harness's report about one entry.
func (t Trigger) fileMissingCapability(entry readmodel.Attention, now time.Time) error {
	attribution := t.Attribution
	attribution.Role = report.HarnessReporter
	attribution.Agent = ""
	attribution.RunID = missingCapabilityRun + entry.HandKey()
	attribution.WorkItemID = entry.WorkItemID
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleProductManager,
		Message:  missingCapabilityMessage(entry),
	}}, attribution, now)
	if err != nil {
		return fmt.Errorf("compose the report about the missing capability on %s: %w", entry.HandKey(), err)
	}
	for _, reported := range collected {
		if err := t.CapabilityReports.Append(reported); err != nil {
			return fmt.Errorf("file the report about the missing capability on %s: %w", entry.HandKey(), err)
		}
	}
	return nil
}

// missingCapabilityMessage is what the report says: the role that decided, the
// entry, and the capability the harness lacks, then what she is asked to do.
func missingCapabilityMessage(entry readmodel.Attention) string {
	hand := entry.Ownership.Hand
	return fmt.Sprintf("%s decision needs the operator's hand because the harness has no way yet of %s, and no work item is admitted to give it that capability; the entry on his line reads: %s. "+
		"Admit a defect for that capability, or record why none is needed; until it lands he types `%s`, the entry stays on his line, and this report is not filed again for it.",
		capitalize(entry.Mover.Possessive()), hand.Missing, strings.Join(strings.Fields(entry.What()), " "), hand.Types)
}

// withdrawMissingCapabilities records, as the harness, that each report about
// an entry that now names an item to close it needs nothing more, where nobody
// has handled the report yet.
func (t Trigger) withdrawMissingCapabilities(closed []readmodel.Attention, filed map[string]report.Report, now time.Time) error {
	handlings, err := t.CapabilityReports.Handlings()
	if err != nil {
		return fmt.Errorf("whether the reports about missing capabilities were handled could not be read, so none was withdrawn: %v", err)
	}
	handled := report.Handled(handlings)
	var problems []string
	for _, entry := range closed {
		reported := filed[entry.HandKey()]
		if _, done := handled[reported.ID]; done {
			continue
		}
		handling := report.Handling{
			SchemaVersion: report.HandlingSchemaVersion,
			ReportID:      reported.ID,
			Role:          report.HarnessReporter,
			RunID:         reported.RunID,
			ProductID:     reported.ProductID,
			RepositoryID:  reported.RepositoryID,
			Reason:        "needs nothing more: the entry now names " + entry.Ownership.Hand.ClosedBy + " as the work item that gives the harness the capability, so a defect for it is already admitted",
			RecordedAt:    now.UTC(),
		}
		if err := t.CapabilityReports.Handle(handling); err != nil {
			problems = append(problems, fmt.Sprintf("record that %s needs nothing more: %v", reported.ID, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("withdraw reports about missing capabilities: %s", strings.Join(problems, "; "))
	}
	return nil
}

// missingCapabilitySection is what a pass's message says where reporting
// missing capabilities could not be done in full, and nothing where it was: a
// pass told nothing when nothing could be read would conclude nothing was
// missing.
func missingCapabilitySection(problem string) string {
	if problem == "" {
		return ""
	}
	return "\n\n## Missing capabilities\n\nWhether every decision of a role that needs the operator's hand, with no work item admitted to end the need, has been reported to you could not be settled on this pass: " + problem
}

func joinMissingCapabilityProblems(problems []string) string {
	return strings.Join(problems, "; ")
}

// capitalize is text with its first letter in upper case.
func capitalize(text string) string {
	if text == "" {
		return ""
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// capabilityPile is the pile as a test holds it, taking the harness's reports
// and handlings as the report store does.
type capabilityPile struct{ memoryPile }

func (p *capabilityPile) Append(reported report.Report) error {
	p.reports = append(p.reports, reported)
	return nil
}

func (p *capabilityPile) Handle(handling report.Handling) error {
	if err := handling.Validate(); err != nil {
		return err
	}
	p.handlings = append(p.handlings, handling)
	return nil
}

// missingCapabilityReports is the harness's reports about missing
// capabilities in the pile, in filing order.
func (p *capabilityPile) missingCapabilityReports() []report.Report {
	var filed []report.Report
	for _, reported := range p.reports {
		if reported.Role == report.HarnessReporter && strings.HasPrefix(reported.RunID, missingCapabilityRun) {
			filed = append(filed, reported)
		}
	}
	return filed
}

// handEntry is an entry on the needs-a-human line: a decision the development
// manager escalated to the operator's hand for the gap named, with the item
// that closes it where one does.
func handEntry(run string, need ownership.HandNeed, closedBy string) readmodel.Attention {
	return readmodel.Attention{
		Kind:       readmodel.AttentionOperatorAction,
		ID:         "run:" + run,
		Mover:      readmodel.MoverDevelopmentManager,
		WorkItemID: "yoyodyne-ifd.9",
		Ownership: ownership.Answer{
			Owner: ownership.MoverDevelopmentManager,
			Hand: &ownership.Hand{Who: ownership.MoverOperator, Need: need, Missing: "crossing a cap past the development manager's own",
				Types: "yoyo triage override --cap 8 yoyodyne-ifd.9", ClosedBy: closedBy},
		},
		OperatorAction: &readmodel.OperatorAction{Key: "run:" + run, Subject: "yoyodyne-ifd.9", Needs: "cross the review-rounds cap to 8",
			FoundBy: "the development manager", RecordedIn: run, WorkItemID: "yoyodyne-ifd.9", Hand: need},
	}
}

// missingCapabilityTrigger is a trigger with the Lead Product Manager's
// report task, reading the entries a test sets.
func missingCapabilityTrigger(t *testing.T, entries *[]readmodel.Attention) (Trigger, *capabilityPile, *wokenRole) {
	t.Helper()
	store := sweepStore(t)
	pile := &capabilityPile{}
	role := &wokenRole{}
	trigger := Trigger{
		Tasks: reportTriageTask(), Claims: store, Reports: store, Roles: role, Clock: recurringClock{},
		Attribution: report.Attribution{ProductID: "example", RepositoryID: "example"},
		HandEntries: func(context.Context) ([]readmodel.Attention, string) {
			return *entries, ""
		},
		CapabilityReports: pile,
	}
	return trigger, pile, role
}

// A role's decision that needs the operator's hand with no item to end the
// need is put in front of the Lead Product Manager as a missing capability,
// naming the role that decided, the capability, and the entry; one whose hand
// names its closing item is not; and no other role's pass files anything.
func TestAHandWithNoClosingItemIsReportedToTheLeadProductManager(t *testing.T) {
	t.Parallel()

	entries := []readmodel.Attention{
		handEntry("run-open", ownership.HandCapCrossing, ""),
		handEntry("run-closed", ownership.HandOperationalConfiguration, "yoyodyne-ifd.434.8"),
	}
	trigger, pile, _ := missingCapabilityTrigger(t, &entries)

	if problem := trigger.reportMissingCapabilities(context.Background(), domain.RoleDevelopmentManager); problem != "" || len(pile.reports) != 0 {
		t.Fatalf("the development manager's pass filed %+v (problem %q), want nothing", pile.reports, problem)
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	filed := pile.missingCapabilityReports()
	if len(filed) != 1 {
		t.Fatalf("reports = %+v, want one, for the entry with no closing item", filed)
	}
	reported := filed[0]
	if reported.Addressee() != domain.RoleProductManager || reported.Severity != report.SeverityWarning || reported.WorkItemID != "yoyodyne-ifd.9" {
		t.Fatalf("report = %+v, want a warning for the Lead Product Manager about yoyodyne-ifd.9", reported)
	}
	if reported.RunID != missingCapabilityRun+entries[0].HandKey() {
		t.Fatalf("report run = %q, want it filed under the entry's key", reported.RunID)
	}
	for _, want := range []string{
		"The development manager's decision",
		"no way yet of crossing a cap past the development manager's own",
		"no work item is admitted",
		"cross the review-rounds cap to 8",
		"Admit a defect",
	} {
		if !strings.Contains(reported.Message, want) {
			t.Errorf("message = %q, want it to say %q", reported.Message, want)
		}
	}
	if strings.Contains(reported.Message, "434.8") {
		t.Errorf("message = %q, names the entry whose hand has a closing item", reported.Message)
	}
}

// The report is filed once per entry, not on every pass; and once the entry
// names an item to close it, nothing more is filed and the report she has not
// handled is recorded as needing nothing, naming the item, so it stops being
// put in front of her.
func TestAMissingCapabilityIsReportedOnceAndWithdrawnOnceAnItemClosesIt(t *testing.T) {
	t.Parallel()

	entries := []readmodel.Attention{handEntry("run-open", ownership.HandCapCrossing, "")}
	trigger, pile, _ := missingCapabilityTrigger(t, &entries)
	ctx := context.Background()

	for pass := 0; pass < 3; pass++ {
		if problem := trigger.reportMissingCapabilities(ctx, domain.RoleProductManager); problem != "" {
			t.Fatalf("pass %d: problem = %q", pass, problem)
		}
	}
	filed := pile.missingCapabilityReports()
	if len(filed) != 1 {
		t.Fatalf("reports after three passes = %+v, want exactly one", filed)
	}
	if unhandled := report.Unhandled(pile.reports, pile.handlings); len(unhandled) != 1 {
		t.Fatalf("unhandled = %+v, want the one report standing in front of her", unhandled)
	}

	// A second entry for the same gap is a different entry, reported on its own.
	entries = append(entries, handEntry("run-other", ownership.HandCapCrossing, ""))
	trigger.reportMissingCapabilities(ctx, domain.RoleProductManager)
	if filed = pile.missingCapabilityReports(); len(filed) != 2 {
		t.Fatalf("reports = %+v, want a second for the second entry", filed)
	}

	// An item is named as closing the first entry's gap.
	entries[0] = handEntry("run-open", ownership.HandCapCrossing, "yoyodyne-ifd.432.25.13")
	for pass := 0; pass < 2; pass++ {
		if problem := trigger.reportMissingCapabilities(ctx, domain.RoleProductManager); problem != "" {
			t.Fatalf("problem = %q", problem)
		}
	}
	if filed = pile.missingCapabilityReports(); len(filed) != 2 {
		t.Fatalf("reports = %+v, want nothing more filed once an item closes the entry", filed)
	}
	handled := report.Handled(pile.handlings)
	withdrawn, done := handled[filed[0].ID]
	if !done || withdrawn.Role != report.HarnessReporter || !strings.Contains(withdrawn.Reason, "yoyodyne-ifd.432.25.13") {
		t.Fatalf("handling of the first report = %+v (%v), want the harness recording it needs nothing, naming the item", withdrawn, done)
	}
	if len(pile.handlings) != 1 {
		t.Fatalf("handlings = %+v, want one: a handled report is not withdrawn again", pile.handlings)
	}
	if _, done := handled[filed[1].ID]; done {
		t.Fatalf("the second entry's report was withdrawn, though its entry still names no item")
	}
}

// A pile whose reports cannot be read files nothing, rather than every entry
// again, and the pass is told so.
func TestAMissingCapabilityIsNotFiledWhenThePileCannotBeRead(t *testing.T) {
	t.Parallel()

	entries := []readmodel.Attention{handEntry("run-open", ownership.HandCapCrossing, "")}
	trigger, _, _ := missingCapabilityTrigger(t, &entries)
	unreadable := &unreadableCapabilityPile{}
	trigger.CapabilityReports = unreadable
	problem := trigger.reportMissingCapabilities(context.Background(), domain.RoleProductManager)
	if !strings.Contains(problem, "none was filed") || unreadable.appended != 0 {
		t.Fatalf("problem = %q, appended %d, want nothing filed and the reason said", problem, unreadable.appended)
	}
	if section := missingCapabilitySection(problem); !strings.Contains(section, "## Missing capabilities") {
		t.Fatalf("section = %q, want the problem said under its own heading", section)
	}
}

type unreadableCapabilityPile struct{ appended int }

func (p *unreadableCapabilityPile) List() ([]report.Report, error) {
	return nil, errUnreadablePile
}
func (p *unreadableCapabilityPile) Handlings() ([]report.Handling, error) { return nil, nil }
func (p *unreadableCapabilityPile) Append(report.Report) error {
	p.appended++
	return nil
}
func (p *unreadableCapabilityPile) Handle(report.Handling) error { return nil }

var errUnreadablePile = errorString("the pile is unreadable")

type errorString string

func (e errorString) Error() string { return string(e) }

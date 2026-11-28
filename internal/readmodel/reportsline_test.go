package readmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// unreadPile is 2 reports for the development manager, one of them critical,
// and 1 naming no role.
func unreadPile() []report.Report {
	var filed []report.Report
	for i, role := range []domain.AgentRole{domain.RoleDevelopmentManager, domain.RoleDevelopmentManager, ""} {
		reported := filedReport(fmt.Sprintf("report-00000000000000000000000000000d0%d", i), report.SeverityWarning, moment.Add(-time.Duration(3-i)*time.Hour))
		reported.For = role
		filed = append(filed, reported)
	}
	filed[0].Severity = report.SeverityCritical
	return filed
}

// The standing carries the reports line, renders it after "reports: ", and
// its JSON carries each addressee's count and whether a pass reads its reports,
// which is what the dashboard shows.
func TestStatusCountsUnhandledReportsByAddresseeAndSaysWhichNoPassReads(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Reports = fakeReports{reports: unreadPile()}
	sources.ReportReaders = report.ReadersOf([]domain.AgentRole{domain.RoleProductManager})
	standing := ReadStanding(context.Background(), sources)

	want := "reports: 3 unhandled, 1 of them critical (development manager 2, no role named 1); no pass reads those for the development manager; read them with yoyo reports\n"
	if got := standing.RenderReports(); got != want {
		t.Fatalf("rendered = %q\nwant       %q", got, want)
	}

	encoded, err := json.Marshal(standing)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Reports struct {
			Addressees []struct {
				Role      string `json:"role"`
				Unhandled int    `json:"unhandled"`
				Critical  int    `json:"critical"`
				Read      bool   `json:"read"`
				Says      string `json:"says"`
			} `json:"addressees"`
		} `json:"reports"`
		ReportsSays string `json:"reports_says"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if "reports: "+decoded.ReportsSays+"\n" != want {
		t.Fatalf("reports_says = %q", decoded.ReportsSays)
	}
	got := decoded.Reports.Addressees
	if len(got) != 2 ||
		got[0].Role != string(domain.RoleDevelopmentManager) || got[0].Unhandled != 2 || got[0].Critical != 1 || got[0].Read ||
		got[1].Role != "" || got[1].Unhandled != 1 || !got[1].Read {
		t.Fatalf("addressees = %+v", got)
	}
	if got[0].Says != "development manager: 2 unhandled, 1 of them critical; no pass reads them" {
		t.Fatalf("says = %q", got[0].Says)
	}
}

// The status line and the opening line of `yoyo reports` are read off the same
// count, so for the same reports they say the same total and the same number
// critical.
func TestTheStatusLineAgreesWithTheReportsOpeningLine(t *testing.T) {
	t.Parallel()

	filed := unreadPile()
	handlings := []report.Handling{{ReportID: filed[1].ID}}
	sources := quietSources()
	sources.Reports = fakeReports{reports: filed, handlings: handlings}
	line := ReadStanding(context.Background(), sources).RenderReports()
	opening := report.SummarizeHandled(filed, report.Handled(handlings), moment).Describe()

	if !strings.HasPrefix(line, "reports: 2 unhandled, 1 of them critical (") {
		t.Fatalf("status line = %q", line)
	}
	if !strings.HasPrefix(opening, "2 of 3 collected report(s) are unhandled") || !strings.HasSuffix(opening, ", 1 of them critical") {
		t.Fatalf("reports opening line = %q", opening)
	}
	if !strings.Contains(line, "; no pass reads them;") {
		t.Fatalf("status line = %q, want it to say no pass reads them where none is configured", line)
	}
}

func TestStatusPrintsNoReportsLineWhenNothingStandsUnhandled(t *testing.T) {
	t.Parallel()

	filed := unreadPile()
	var handlings []report.Handling
	for _, reported := range filed {
		handlings = append(handlings, report.Handling{ReportID: reported.ID})
	}
	sources := quietSources()
	sources.Reports = fakeReports{reports: filed, handlings: handlings}
	standing := ReadStanding(context.Background(), sources)
	if got := standing.RenderReports(); got != "" || standing.ReportsSays != "" {
		t.Fatalf("rendered = %q, says = %q, want nothing", got, standing.ReportsSays)
	}
}

// reportsNeeds is the entries for the report pile on the line of things
// needing a person.
func reportsNeeds(standing Standing) []Attention {
	var found []Attention
	for _, attention := range standing.NeedsHuman {
		if attention.Kind == AttentionReports {
			found = append(found, attention)
		}
	}
	return found
}

// With no recurring pass at all, the Lead Product Manager's included, the line
// of things needing a person names the reports at once, how many and for
// whom, with the command that reads them, though the oldest is hours old.
func TestReportsNoPassReadsAreNamedAtOnceOnTheLineOfThingsNeedingAPerson(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Reports = fakeReports{reports: unreadPile()}
	sources.ReportReaders = report.ReadersOf(nil)
	needs := reportsNeeds(ReadStanding(context.Background(), sources))
	if len(needs) != 1 || !needs[0].UnreadReports {
		t.Fatalf("report entries on the line = %+v, want one for reports no pass reads", needs)
	}
	want := "3 unhandled report(s) that no pass reads here, the Lead Product Manager's included (development manager 2, no role named 1); read them with yoyo reports"
	if got := needs[0].What(); got != want {
		t.Fatalf("what = %q\nwant   %q", got, want)
	}
	if got := needs[0].Whose(); !strings.Contains(got, "no recurring pass reads these here") {
		t.Fatalf("whose = %q", got)
	}
}

// Where her pass runs, reports for a role no pass reads go to it, so nothing
// is put on the line before the week; and a reading made without the
// configuration says nothing either way.
func TestReportsHerPassReadsAreNotNamedAtOnce(t *testing.T) {
	t.Parallel()

	for name, readers := range map[string]report.Readers{
		"her pass runs":    report.ReadersOf([]domain.AgentRole{domain.RoleProductManager}),
		"no configuration": nil,
	} {
		sources := quietSources()
		sources.Reports = fakeReports{reports: unreadPile()}
		sources.ReportReaders = readers
		if needs := reportsNeeds(ReadStanding(context.Background(), sources)); len(needs) != 0 {
			t.Fatalf("%s: report entries on the line = %+v, want none", name, needs)
		}
	}
}

// Enabling a pass later takes the reports off the line, because the reader is
// worked out from the configuration and never written on the report: the same
// reports, read again with a pass for the development manager, name only the
// one naming no role.
func TestEnablingAPassLaterSendsWaitingReportsToIt(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Reports = fakeReports{reports: unreadPile()}
	sources.ReportReaders = report.ReadersOf([]domain.AgentRole{domain.RoleDevelopmentManager})
	needs := reportsNeeds(ReadStanding(context.Background(), sources))
	if len(needs) != 1 || !strings.HasPrefix(needs[0].What(), "1 unhandled report(s) that no pass reads here, the Lead Product Manager's included (no role named 1)") {
		t.Fatalf("report entries on the line = %+v", needs)
	}
}

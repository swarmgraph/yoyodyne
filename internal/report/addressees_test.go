package report

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// gameServerPile is the pile the design names: 27 reports for the development
// manager, 4 for the Lead Product Manager, and 3 naming no role, none handled.
func gameServerPile() []Report {
	var pile []Report
	add := func(count int, role domain.AgentRole) {
		for i := 0; i < count; i++ {
			reported := filed(len(pile)+1, SeverityWarning, filedAt.Add(time.Duration(len(pile))*time.Minute))
			reported.For = role
			pile = append(pile, reported)
		}
	}
	add(27, domain.RoleDevelopmentManager)
	add(4, domain.RoleProductManager)
	add(3, "")
	return pile
}

func TestTheStatusLineCountsEachAddresseeAndSaysNoPassReadsThem(t *testing.T) {
	pile := Summarize(gameServerPile(), nil, filedAt.Add(time.Hour)).ReadBy(nil)
	want := "34 unhandled (development manager 27, Lead Product Manager 4, no role named 3); no pass reads them; read them with yoyo reports"
	if got := pile.StatusLine(); got != want {
		t.Fatalf("status line = %q\nwant          %q", got, want)
	}
}

func TestTheStatusLineNamesOnlyTheAddresseesNoPassReads(t *testing.T) {
	pile := Summarize(gameServerPile(), nil, filedAt.Add(time.Hour)).ReadBy(ReadersOf([]domain.AgentRole{domain.RoleDevelopmentManager}))
	want := "34 unhandled (development manager 27, Lead Product Manager 4, no role named 3); no pass reads those for the Lead Product Manager or those naming no role; read them with yoyo reports"
	if got := pile.StatusLine(); got != want {
		t.Fatalf("status line = %q\nwant          %q", got, want)
	}
}

func TestTheStatusLineLeavesThePassClauseOutWhereEveryAddresseeIsRead(t *testing.T) {
	readers := ReadersOf([]domain.AgentRole{domain.RoleDevelopmentManager, domain.RoleProductManager})
	pile := Summarize(gameServerPile(), nil, filedAt.Add(time.Hour)).ReadBy(readers)
	got := pile.StatusLine()
	if strings.Contains(got, "no pass reads") {
		t.Fatalf("status line = %q, want no clause on passes", got)
	}
	if !strings.HasSuffix(got, "; read them with yoyo reports") {
		t.Fatalf("status line = %q, want it to name the command that reads them", got)
	}
}

func TestTheLeadProductManagerReadsReportsNamingNoRoleAndAProgramManagersReports(t *testing.T) {
	lead := ReadersOf([]domain.AgentRole{domain.RoleProductManager})
	for _, addressee := range []domain.AgentRole{"", domain.RoleProgramManager, domain.RoleProductManager} {
		if !lead.ReadsReports(addressee) {
			t.Errorf("the Lead Product Manager's pass does not read reports addressed to %q", addressee)
		}
	}
	for _, addressee := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager} {
		if lead.ReadsReports(addressee) {
			t.Errorf("the Lead Product Manager's pass reads reports addressed to %q, which its own role handles", addressee)
		}
	}
	if (ReadersOf([]domain.AgentRole{domain.RoleProgramManager})).ReadsReports(domain.RoleProgramManager) {
		t.Error("a program manager's own pass reads its reports; the Lead Product Manager handles them")
	}
}

func TestTheStatusLineCountsCriticalReportsAsTheReportsOpeningLineDoes(t *testing.T) {
	reports := gameServerPile()
	reports[0].Severity = SeverityCritical
	reports[30].Severity = SeverityCritical
	// Two handled, one of them critical, so neither line may count those.
	handlings := []Handling{{ReportID: reports[0].ID}, {ReportID: reports[1].ID}}
	pile := Summarize(reports, handlings, filedAt.Add(time.Hour)).ReadBy(nil)

	line, opening := pile.StatusLine(), pile.Describe()
	if !strings.HasPrefix(line, fmt.Sprintf("%d unhandled, %d of them critical (", pile.Unhandled, pile.Critical)) {
		t.Fatalf("status line = %q", line)
	}
	if !strings.HasPrefix(opening, fmt.Sprintf("%d of %d collected report(s) are unhandled", pile.Unhandled, pile.Collected)) ||
		!strings.Contains(opening, fmt.Sprintf(", %d of them critical", pile.Critical)) {
		t.Fatalf("opening line = %q", opening)
	}
	if pile.Unhandled != 32 || pile.Critical != 1 {
		t.Fatalf("unhandled %d, critical %d; want 32 and 1", pile.Unhandled, pile.Critical)
	}
	sum, critical := 0, 0
	for _, count := range pile.Addressees {
		sum += count.Unhandled
		critical += count.Critical
	}
	if sum != pile.Unhandled || critical != pile.Critical {
		t.Fatalf("addressees sum to %d unhandled and %d critical; the pile says %d and %d", sum, critical, pile.Unhandled, pile.Critical)
	}
	if !strings.Contains(line, "development manager 25, Lead Product Manager 4, no role named 3") {
		t.Fatalf("status line = %q", line)
	}
}

func TestAReportForARoleNoReportMayBeAddressedToIsCountedAsNamingNone(t *testing.T) {
	reported := filed(1, SeverityNote, filedAt)
	reported.For = domain.RoleDeveloper
	pile := Summarize([]Report{reported}, nil, filedAt).ReadBy(ReadersOf([]domain.AgentRole{domain.RoleProductManager}))
	if len(pile.Addressees) != 1 || pile.Addressees[0].Role != "" || !pile.Addressees[0].Read {
		t.Fatalf("addressees = %+v", pile.Addressees)
	}
	if got := pile.Addressees[0].Says; got != "no role named: 1 unhandled; a recurring pass reads them" {
		t.Fatalf("says = %q", got)
	}
}

func TestThereIsNoStatusLineWhenNothingStandsUnhandled(t *testing.T) {
	reports := piled(2, SeverityCritical)
	handlings := []Handling{{ReportID: reports[0].ID}, {ReportID: reports[1].ID}}
	if got := Summarize(reports, handlings, filedAt).ReadBy(nil).StatusLine(); got != "" {
		t.Fatalf("status line = %q, want none", got)
	}
	if got := Summarize(nil, nil, filedAt).StatusLine(); got != "" {
		t.Fatalf("status line on an empty pile = %q, want none", got)
	}
}

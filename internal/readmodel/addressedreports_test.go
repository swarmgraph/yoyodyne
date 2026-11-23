package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// architectPassShown is one completed pass of the architect's that was shown the
// reports given, with the account's entries on them.
func architectPassShown(started time.Time, shown []string, decided ...sweep.ReportDecision) runstate.Sweep {
	return runstate.Sweep{
		Task:      "needs-design",
		Role:      domain.RoleArchitect,
		StartedAt: started,
		EndedAt:   started.Add(time.Minute),
		Turns:     1,
		Addressed: shown,
		Result:    &sweep.Result{Status: sweep.StatusComplete, Summary: "worked the labelled items", Reports: decided},
	}
}

// A report addressed to the architect that three of her passes were shown and
// said nothing about is said so on status, with when the latest of them
// started in local time with the zone named. One that two passes passed over,
// and one a pass decided, are listed as before.
func TestStatusSaysAReportThreeOfItsRolesPassesPassedOver(t *testing.T) {
	t.Parallel()

	const (
		passedOver = "report-00000000000000000000000000000c01"
		twice      = "report-00000000000000000000000000000c02"
		decided    = "report-00000000000000000000000000000c03"
	)
	sources := quietSources()
	var filed []report.Report
	for _, id := range []string{passedOver, twice, decided} {
		reported := filedReport(id, report.SeverityWarning, moment.Add(-4*time.Hour))
		reported.Role = domain.RoleProductManager
		reported.For = domain.RoleArchitect
		reported.WorkItemID = "yoyodyne-ifd.435.59"
		filed = append(filed, reported)
	}
	sources.Reports = fakeReports{reports: filed}
	first, second, third := moment.Add(-3*time.Hour), moment.Add(-2*time.Hour), moment.Add(-time.Hour)
	sources.Sweeps = fakeSweeps{recorded: []runstate.Sweep{
		architectPassShown(first, []string{passedOver, twice, decided}),
		architectPassShown(second, []string{passedOver, twice, decided}, sweep.ReportDecision{Report: decided, Decision: "revised the design"}),
		architectPassShown(third, []string{passedOver, decided}),
		// A pass that asked her nothing passed over nothing.
		{Task: "needs-design", Role: domain.RoleArchitect, StartedAt: moment, Addressed: []string{twice}},
	}}

	byID := map[string]Attention{}
	for _, waiting := range ReadStanding(context.Background(), sources).NeedsHuman {
		if waiting.Kind == AttentionAddressedReport {
			byID[waiting.ID] = waiting
		}
	}
	entry, listed := byID[passedOver]
	if !listed || entry.PassedOver == nil || entry.PassedOver.Passes != 3 {
		t.Fatalf("entry = %+v, want it carrying three passes that passed it over", entry)
	}
	want := "3 of the architect's passes were shown it and recorded neither a decision about it nor that they left it, the latest starting " + third.Local().Format("2006-01-02 15:04 MST")
	if !strings.Contains(entry.What(), want) {
		t.Fatalf("what = %q, want it to say %q", entry.What(), want)
	}
	for _, id := range []string{twice, decided} {
		if other, listed := byID[id]; !listed || other.PassedOver != nil || strings.Contains(other.What(), "passes were shown it") {
			t.Fatalf("report %s = %+v, want it listed without saying it was passed over", id, other)
		}
	}
}

// Passes count against a report only where they were shown it and said
// nothing: a decision or a leaving on one pass is not counted, and a pass that
// gave no account at all is.
func TestReportsPassedOverCountsOnlyPassesThatSaidNothing(t *testing.T) {
	t.Parallel()

	const id = "report-00000000000000000000000000000c04"
	failed := architectPassShown(moment, []string{id})
	failed.Result = nil
	passed := ReportsPassedOver([]runstate.Sweep{
		architectPassShown(moment.Add(-2*time.Hour), []string{id}, sweep.ReportDecision{Report: id, Left: "waiting on the design review"}),
		architectPassShown(moment.Add(-time.Hour), []string{id}),
		failed,
	})
	if got := passed[id]; got.Passes != 2 || got.Role != domain.RoleArchitect || !got.Latest.Equal(moment) {
		t.Fatalf("ReportsPassedOver() = %+v, want two passes, the latest at %s", got, moment)
	}
}

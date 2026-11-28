package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// /reports shows a standing report once, with how often later runs repeated
// it and which run did so last.
func TestReportsListingShowsAStandingReportOnceWithItsRepeats(t *testing.T) {
	t.Parallel()
	standing := report.Report{
		SchemaVersion: report.SchemaVersion, ID: "report-0123456789abcdef0123456789abcdef", Role: "developer",
		RunID: "run-1111", ProductID: "yoyodyne", RepositoryID: "yoyodyne", Severity: report.SeverityWarning,
		Message: "a test server was left running", RecordedAt: fixedClock{}.Now(),
	}
	repeated := report.RepeatsOf([]report.Repeat{
		{ReportID: standing.ID, RunID: "run-2222", WorkItemID: "yoyodyne-one", RecordedAt: fixedClock{}.Now().Add(time.Hour)},
		{ReportID: standing.ID, RunID: "run-3333", WorkItemID: "yoyodyne-two", RecordedAt: fixedClock{}.Now().Add(2 * time.Hour)},
	})
	rendered := renderCollectedReports(console.NewTheme(func(string) string { return "" }, nil), []report.Report{standing}, map[string]report.Handling{}, repeated, nil, nil, fixedClock{}.Now())
	if strings.Count(rendered, standing.ID) != 1 || !strings.Contains(rendered, "repeated 2 times, the latest by run run-3333 on yoyodyne-two at ") {
		t.Fatalf("renderCollectedReports() = %q", rendered)
	}
}

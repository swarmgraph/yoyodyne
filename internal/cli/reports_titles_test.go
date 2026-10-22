package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// `yoyo reports` is where a program manager's digest is read without opening a
// conversation. Each item it names — in the report and in what became of it —
// is printed with its priority, labels, and title, one the tracker does not
// hold says so, and an unreadable tracker marks every item rather than leaving
// it bare.
func TestTheReportsCommandShowsEveryItemWithItsState(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	titles := readmodel.NewWorkItemTitles([]beads.WorkItem{
		{ID: "yoyodyne-ifd.434.9", Title: "Price a resumed session at what it moved by", Priority: 1, Labels: []string{"reliability"}},
		{ID: "yoyodyne-ifd.434.3", Title: "Say the provider's reset in local time", Priority: 2},
	})
	digest := report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          "program-manager",
		Agent:         "factory-pgm",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityNote,
		Message:       "digest: admitted 434.9; yoyodyne-ifd.999.1 is still open",
		RecordedAt:    at,
	}
	digest.References, _ = titles.References(digest.Message)
	reason := "yoyodyne-ifd.434.9 already covers it; admitted as 434.3"
	references, _ := titles.References(reason)
	handled := map[string]report.Handling{digest.ID: {
		ReportID: digest.ID, Role: "product-manager", RunID: "chat-1",
		Reason: reason, References: references, RecordedAt: at.Add(time.Hour),
	}}

	var out strings.Builder
	writeReports(&out, console.NewTheme(func(string) string { return "" }, nil), []report.Report{digest}, handled, nil, titles)
	printed := out.String()
	for _, want := range []string{
		"admitted yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session at what it moved by)",
		"yoyodyne-ifd.999.1 (not in the tracker)",
		"admitted as yoyodyne-ifd.434.3 (P2: Say the provider's reset in local time)",
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("writeReports() = %q, want it to carry %q", printed, want)
		}
	}

	var unreadable strings.Builder
	writeReports(&unreadable, console.NewTheme(func(string) string { return "" }, nil), []report.Report{digest}, nil, nil, readmodel.UnreadableWorkItemTitles(at, "yoyodyne"))
	if !strings.Contains(unreadable.String(), "yoyodyne-ifd.434.9 (tracker unreadable at ") {
		t.Errorf("writeReports(unreadable) = %q, want each item marked as unreadable", unreadable.String())
	}
}

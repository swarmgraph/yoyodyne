package chat

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// A program manager's digest is a report, and it names work the way the lane
// report the operator read on 2026-09-26 did: by short id. Recorded with its
// references, each is shown as the full id it named with the item's priority,
// labels, and title; one the tracker does not hold says so; and a short id in a
// report recorded before references were kept is left as written and counted.
func TestTheReportListingShowsEveryItemWithItsState(t *testing.T) {
	t.Parallel()

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
		Message:       "digest: admitted 434.9; objecting to 434.3; yoyodyne-ifd.999.1 is still open",
		RecordedAt:    fixedClock{}.Now(),
	}
	digest.References, _ = titles.References(digest.Message)
	digest.ReferencesKept = true
	older := digest
	older.ID, older.Message, older.References, older.ReferencesKept = "report-fedcba9876543210fedcba9876543210", "older: 434.9 is open", nil, false
	// Recorded against a listing in which its short id named no one item: it is
	// left as written, and not counted as written before references were kept.
	kept := older
	kept.ID, kept.Message, kept.ReferencesKept = "report-00112233445566778899aabbccddeeff", "kept: 434.9 was not yet admitted", true
	rendered := renderCollectedReports(console.NewTheme(func(string) string { return "" }, nil), []report.Report{digest, older, kept}, nil, nil, titles, fixedClock{}.Now())
	for _, want := range []string{
		"admitted yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session at what it moved by)",
		"objecting to yoyodyne-ifd.434.3 (P2: Say the provider's reset in local time)",
		"yoyodyne-ifd.999.1 (not in the tracker)",
		"older: 434.9 is open",
		"kept: 434.9 was not yet admitted",
		"1 short work item id above was recorded with no tracker listing to resolve it against",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("renderCollectedReports() = %q, want it to carry %q", rendered, want)
		}
	}
}

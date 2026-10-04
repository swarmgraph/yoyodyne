package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// A program manager's digest is a report, and it names work the way the lane
// report the operator read on 2026-09-26 did: by number. The listing shows each
// item beside its title, and an item the tracker does not hold as unknown.
func TestTheReportListingShowsEveryItemBesideItsTitle(t *testing.T) {
	t.Parallel()

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
	titles := readmodel.NewWorkItemTitles([]beads.WorkItem{
		{ID: "yoyodyne-ifd.434.9", Title: "Price a resumed session at what it moved by"},
		{ID: "yoyodyne-ifd.434.3", Title: "Say the provider's reset in local time"},
	})
	rendered := renderCollectedReports(console.NewTheme(func(string) string { return "" }, nil), []report.Report{digest}, nil, nil, titles, fixedClock{}.Now())
	for _, want := range []string{
		"(P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9)",
		"(P0) Say the provider's reset in local time (yoyodyne-ifd.434.3)",
		"title unavailable (yoyodyne-ifd.999.1)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("renderCollectedReports() = %q, want it to carry %q", rendered, want)
		}
	}
}

// A conversation uses the same bounded, fresh listing as the other surfaces.
type citationTracker struct {
	*fakeTracker
	deadline bool
}

func (f *citationTracker) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	_, f.deadline = ctx.Deadline()
	return f.fakeTracker.List(ctx, status)
}

func TestConversationRepliesExpandIdentifiersUsingABoundedCurrentListing(t *testing.T) {
	t.Parallel()
	tracker := &citationTracker{fakeTracker: &fakeTracker{all: []beads.WorkItem{{
		ID: "yoyodyne-ifd.12", Title: "Pause on a provider usage limit", Priority: 1, Labels: []string{"reliability"},
	}}}}
	session := &Session{options: Options{Tracker: tracker}}
	text := "Work on yoyodyne-ifd.12."
	if got := session.RenderReply(text); got != "Work on (P1, reliability) Pause on a provider usage limit (yoyodyne-ifd.12)." {
		t.Fatalf("RenderReply() = %q", got)
	}
	if !tracker.deadline {
		t.Fatal("the report/reply listing had no deadline")
	}
	tracker.all[0].Priority = 2
	tracker.all[0].Labels = nil
	if got := session.RenderReply(text); got != "Work on (P2) Pause on a provider usage limit (yoyodyne-ifd.12)." {
		t.Fatalf("next reply = %q, want current fields", got)
	}
	tracker.listErr = context.DeadlineExceeded
	if got := session.RenderReply(text); got != "Work on title unavailable (yoyodyne-ifd.12)." {
		t.Fatalf("unreadable tracker = %q", got)
	}
}

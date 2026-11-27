package readmodel

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
)

// A piece the tracker does not offer because the earlier piece it depends on is
// in progress is said that way: the earlier piece named with its state, and
// nobody asked to find out why. The queue cannot see the earlier piece, which
// somebody has claimed, and the listing's link carries no status; the standing
// has read the claimed work. A piece that depends on nothing unfinished still
// says the cause is unknown.
func TestAPieceWaitingOnAnEarlierPieceInProgressNamesIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	naming := epicscope.Naming{Epic: "yoyodyne-rp", Members: []string{"yoyodyne-rp.1", "yoyodyne-rp.2", "yoyodyne-rp.3"}, From: "terminal"}
	if _, err := operator.Start(naming, moment); err != nil {
		t.Fatal(err)
	}
	second := beads.WorkItem{ID: "yoyodyne-rp.2", Title: "Route the reports", Status: "open",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-rp.1", Type: beads.BlocksDependency}}}
	unexplained := beads.WorkItem{ID: "yoyodyne-rp.3", Title: "Say the totals", Status: "open"}
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{
			"open":        {second, unexplained},
			"in_progress": {{ID: "yoyodyne-rp.1", Title: "Read the reports", Status: "in_progress"}, {ID: "yoyodyne-rp", Title: "Reports", Status: "in_progress"}},
		},
	}}
	sources.Runs = runsOfItems{}
	sources.Epics = NewEpicRecords(root, "yoyodyne")
	rendered := ReadStanding(context.Background(), sources).RenderEpics()
	if want := "Route the reports (yoyodyne-rp.2) waits for Read the reports (yoyodyne-rp.1), which is in progress, and is offered once that is finished."; !strings.Contains(rendered, want) {
		t.Fatalf("rendered =\n%s\nwant it to say %q", rendered, want)
	}
	if strings.Contains(rendered, "Route the reports (yoyodyne-rp.2) is not offered by the tracker") {
		t.Fatalf("rendered =\n%s\nwant the waiting piece not sent to find out why", rendered)
	}
	if !strings.Contains(rendered, "Say the totals (yoyodyne-rp.3) is not offered by the tracker: find out why") {
		t.Fatalf("rendered =\n%s\nwant the piece that depends on nothing unfinished still said as unexplained", rendered)
	}
}

func TestStatusWordsSaysATrackerStatusForAPerson(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]string{"in_progress": "in progress", "open": "open", "blocked": "blocked", "": "not finished"} {
		if got := beads.StatusWords(status); got != want {
			t.Fatalf("StatusWords(%q) = %q, want %q", status, got, want)
		}
	}
}

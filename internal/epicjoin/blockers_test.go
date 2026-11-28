package epicjoin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// blocked is a member that waits on open work outside the epic, which holds a
// child of its own, beside closed work and another member it waits on too.
func (b *bench) blocked() {
	b.items = append(b.items,
		beads.WorkItem{ID: member, Title: "The member", Status: "open", Dependencies: []beads.Dependency{
			{ID: "yoyodyne-outside", Type: beads.BlocksDependency}, {ID: "yoyodyne-finished", Type: beads.BlocksDependency}, {ID: "yoyodyne-ep.2", Type: beads.BlocksDependency}}},
		beads.WorkItem{ID: "yoyodyne-ep.2", Title: "Another member", Status: "open"},
		beads.WorkItem{ID: "yoyodyne-outside", Title: "Outside work", Status: "open"},
		beads.WorkItem{ID: "yoyodyne-outside.1", Title: "Under it", Status: "open", Parent: "yoyodyne-outside"},
		beads.WorkItem{ID: "yoyodyne-finished", Title: "Finished", Status: "closed"})
}

// A link from before the rule leaves open work outside the epic able to block a
// member. One pass files one question to the Lead Product Manager for each such
// item, naming the epic, the member, the link and the add, after keeping its
// identifier in the epic's progress; a harness started again files nothing more,
// and nothing joins. Closed work and work inside the epic are asked about never.
func TestOpenWorkOutsideAnEpicThatCouldBlockAMemberIsAskedAboutOnce(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.blocked()
	reports := &fakeReports{}
	b.owes(b.asking(reports), true)
	b.must(b.asking(reports).Round(context.Background()))

	if len(reports.filed) != 2 {
		t.Fatalf("filed %d reports, want one for each open item outside: %+v", len(reports.filed), reports.filed)
	}
	for at, want := range []struct{ item, link string }{
		{"yoyodyne-outside", member + " waits on it"},
		{"yoyodyne-outside.1", "it is a child of yoyodyne-outside, which could block " + member},
	} {
		question := reports.filed[at]
		if question.For != domain.RoleProductManager || question.Role != report.HarnessReporter || question.WorkItemID != want.item || question.DuringEpic != epic ||
			!strings.HasPrefix(question.RunID, report.OutsideEpicBlockerRun) {
			t.Errorf("filed %+v", question)
		}
		for _, says := range []string{"outside epic " + epic, "could block its member " + member, want.link, "`yoyo epic add " + epic + " " + want.item + "`", "does not join on its own"} {
			if !strings.Contains(question.Message, says) {
				t.Errorf("the question about %s does not say %q: %s", want.item, says, question.Message)
			}
		}
		if kept, asked := b.entry().Blocker(want.item); !asked || kept.Report != question.ID || kept.Member != member {
			t.Errorf("kept %+v, want the report %s named for the member", kept, question.ID)
		}
	}
	if len(b.entry().Joined) != 0 || len(b.requests) != 0 {
		t.Fatalf("joined %+v and judged %d, want nothing", b.entry().Joined, len(b.requests))
	}

	// Started again: nothing owed, nothing filed.
	b.owes(b.asking(reports), false)
	b.must(b.asking(reports).Round(context.Background()))
	if len(reports.filed) != 2 {
		t.Fatalf("filed %d reports after a restart, want still two", len(reports.filed))
	}
}

// A stop after the identifier is kept and before the report is filed leaves the
// same report to be filed at the next pass, and only it.
func TestAQuestionAboutAnOutsideBlockerAStopKeptBackIsFiledUnderItsIdentifier(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.blocked()
	reports := &fakeReports{appendErr: errors.New("the disk is full")}
	if err := b.asking(reports).Round(context.Background()); err == nil {
		t.Fatal("a question that could not be filed is not an error")
	}
	kept, asked := b.entry().Blocker("yoyodyne-outside")
	if !asked || len(reports.filed) != 0 {
		t.Fatalf("kept %+v, filed %d", kept, len(reports.filed))
	}
	reports.appendErr = nil
	b.owes(b.asking(reports), true)
	b.must(b.asking(reports).Round(context.Background()))
	if len(reports.filed) != 2 || reports.filed[0].ID != kept.Report {
		t.Fatalf("filed %+v, want the kept report %s and the other once each", reports.filed, kept.Report)
	}
}

// A tracker that cannot be read files nothing and keeps nothing, and the next
// pass reads again.
func TestATrackerThatCannotBeReadFilesNoQuestionAboutOutsideBlockers(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.blocked()
	reports := &fakeReports{}
	joiner := b.asking(reports)
	joiner.Items = func(context.Context) ([]beads.WorkItem, error) { return nil, errors.New("the tracker is busy") }
	if _, err := joiner.blockersOwed(context.Background()); err == nil || !strings.Contains(err.Error(), "the tracker is busy") {
		t.Fatalf("owed error = %v, want the tracker's", err)
	}
	if err := joiner.askBlockers(context.Background()); err == nil {
		t.Fatal("a tracker that cannot be read is not an error")
	}
	if len(reports.filed) != 0 || len(b.entry().Blockers) != 0 {
		t.Fatalf("filed %d and kept %+v, want nothing", len(reports.filed), b.entry().Blockers)
	}
	b.must(b.asking(reports).Round(context.Background()))
	if len(reports.filed) != 2 {
		t.Fatalf("filed %d once the tracker reads, want two", len(reports.filed))
	}
}

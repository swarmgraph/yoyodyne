package orchestrator

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/designagreement"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func framedNotes(notes ...string) beads.WorkItem {
	item := beads.WorkItem{ID: "item-ruled", Status: "open"}
	for _, note := range notes {
		if item.FramedNotes != "" {
			item.FramedNotes += "\n"
		}
		item.FramedNotes += beads.FrameNote(note)
	}
	return item
}

const architectRuling = runstate.RulingNoteOpening + "design-x (Design X, document document-3.1) by the architect in conversation chat-a, after turn 3.\n\nThe design."

// Only the architect's own ruling, newest but for the harness's notes about her
// passes, is a ruling that can stand.
func TestARulingStandsOnlyWhereItIsTheNewestNote(t *testing.T) {
	t.Parallel()
	agreed := designagreement.Note("design-x", "document-3.1", time.Date(2026, 11, 10, 5, 0, 0, 0, time.UTC), time.Date(2026, 11, 10, 5, 1, 0, 0, time.UTC))
	for _, test := range []struct {
		name string
		item beads.WorkItem
		want bool
	}{
		{"ruling newest", framedNotes("Admitted.", architectRuling), true},
		{"pass notes after the ruling", framedNotes(architectRuling, "Passed over by the architect's recurring pass architect-pass#4 at 03:07 PST on November 10, 2026: …", agreed), true},
		{"a new note after the ruling", framedNotes(architectRuling, "Answer: take the second option."), false},
		{"another role's document", framedNotes(runstate.RulingNoteOpening + "brief (Brief, document document-9.1) by the Lead Product Manager in conversation chat-b, after turn 9."), false},
		{"no ruling", framedNotes("Admitted."), false},
		{"unframed notes", beads.WorkItem{ID: "item-ruled", Notes: architectRuling}, false},
	} {
		if _, got := RulingSnapshotOf(test.item, domain.RoleArchitect, ""); got != test.want {
			t.Errorf("%s: ruled = %v, want %v", test.name, got, test.want)
		}
	}
}

// The snapshot changes with anything about the item the architect would read
// again, and with the documents it cites.
func TestARulingSnapshotChangesWithTheItemAndItsDocuments(t *testing.T) {
	t.Parallel()
	item := framedNotes(architectRuling)
	before, _ := RulingSnapshotOf(item, domain.RoleArchitect, "cited-a")
	same, _ := RulingSnapshotOf(item, domain.RoleArchitect, "cited-a")
	if !(StandingRulings{item.ID: before}).Holds(same) {
		t.Fatal("an unchanged item's ruling does not stand")
	}
	changed := item
	changed.Status = "blocked"
	if after, _ := RulingSnapshotOf(changed, domain.RoleArchitect, "cited-a"); (StandingRulings{item.ID: before}).Holds(after) {
		t.Error("a change of status leaves the ruling standing")
	}
	moved, _ := RulingSnapshotOf(item, domain.RoleArchitect, "cited-b")
	if (StandingRulings{item.ID: before}).Holds(moved) {
		t.Error("a change to a cited document leaves the ruling standing")
	}
}

// A report naming the item, filed after the snapshot, puts it back.
func TestAReportNamingTheItemSinceItsSnapshotPutsItBack(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 10, 5, 0, 0, 0, time.UTC)
	ruling := runstate.RulingSnapshot{ID: "item-ruled", Digest: "d", At: at}
	for _, test := range []struct {
		name   string
		filed  report.Report
		before bool
	}{
		{"named in the message since", report.Report{Message: "The design for item-ruled contradicts the brief.", RecordedAt: at.Add(time.Minute)}, true},
		{"filed while working on it since", report.Report{WorkItemID: "item-ruled", Message: "x", RecordedAt: at.Add(time.Minute)}, true},
		{"named before the snapshot", report.Report{Message: "item-ruled", RecordedAt: at.Add(-time.Minute)}, false},
		{"a longer identifier", report.Report{Message: "item-ruled.2 is late", RecordedAt: at.Add(time.Minute)}, false},
	} {
		if got := reportedSince([]report.Report{test.filed}, ruling); got != test.before {
			t.Errorf("%s: reported since = %v, want %v", test.name, got, test.before)
		}
	}
}

func TestCitedDocumentsAreTheRepositoryFilesTheItemNames(t *testing.T) {
	t.Parallel()
	item := beads.WorkItem{
		Description: "Rests on docs/designs/b.md and docs/designs/a.md#section, and docs/designs/a.md again.",
		Design:      "Not docs/../etc/passwd.md.",
	}
	elsewhere := beads.WorkItem{Description: "Rests on architecture/rulings/c.md, not other/d.md."}
	if got := CitedDocuments(elsewhere, "./architecture/rulings/"); len(got) != 1 || got[0] != "architecture/rulings/c.md" {
		t.Fatalf("CitedDocuments() under a configured directory = %v", got)
	}
	got := CitedDocuments(item)
	if len(got) != 2 || got[0] != "docs/designs/a.md" || got[1] != "docs/designs/b.md" {
		t.Fatalf("CitedDocuments() = %v", got)
	}
}

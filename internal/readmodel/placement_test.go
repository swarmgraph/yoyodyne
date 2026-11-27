package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The standing names an item placed first among its priority in the words
// every listing of the queue uses, and says nothing new of an unplaced item.
func TestTheStandingSaysWhichItemsArePlacedFirst(t *testing.T) {
	t.Parallel()

	placement := &beads.Placement{By: domain.RoleProductManager, At: time.Date(2026, 10, 29, 13, 50, 0, 0, time.UTC), Priority: 0, Reason: "promised first by the operator"}
	placed := waitingWorkRef(backlog.Entry{ID: "yoyodyne-u6ei", Title: "The promised change", Placement: placement})
	plain := waitingWorkRef(backlog.Entry{ID: "yoyodyne-old", Title: "Older work"})
	if placed.PlacedFirst != placement.Line() || plain.PlacedFirst != "" {
		t.Fatalf("refs = %#v and %#v, want the placement line on the placed item alone", placed, plain)
	}
	standing := Standing{Admitted: 2, AdmittedItems: []WorkItemRef{plain, placed}, ObservedAt: time.Now()}
	rendered := standing.renderNotStartable()
	if !strings.Contains(rendered, "yoyodyne-u6ei") || !strings.Contains(rendered, placement.Line()) {
		t.Fatalf("rendered = %q, want the placed item named with its placement", rendered)
	}
	if strings.Contains(rendered, "yoyodyne-old") {
		t.Fatalf("rendered = %q, want nothing new about the unplaced item", rendered)
	}
	if empty := (Standing{Admitted: 1, AdmittedItems: []WorkItemRef{plain}}).renderNotStartable(); strings.Contains(empty, "placed first") {
		t.Fatalf("rendered = %q with nothing placed", empty)
	}
}

// A placed item the scheduler passed over for lower work says why on its
// placement line, at all times, and is not named a second time below it; a
// placed item with no kept reason says its placement alone.
func TestAPlacedItemPassedOverSaysWhyBesideItsPlacement(t *testing.T) {
	t.Parallel()

	placement := &beads.Placement{By: domain.RoleProductManager, At: time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC), Priority: 0, Reason: "the operator's deadline"}
	at := time.Date(2026, 11, 27, 21, 1, 0, 0, time.UTC)
	wait := &beads.SchedulingWait{Class: "already tried by this session", Reason: "already tried by this session, which does not start it again until the item changes or the session restarts: run run-1 failed",
		FirstPassedOver: at, ReasonSince: at}
	placed := waitingWorkRef(backlog.Entry{ID: "yoyodyne-9k1y.22", Title: "The deadline item", Placement: placement, SchedulingWait: wait})
	other := waitingWorkRef(backlog.Entry{ID: "yoyodyne-other", Title: "Other work", Placement: placement})
	standing := Standing{Admitted: 2, AdmittedItems: []WorkItemRef{placed, other}, SchedulingWaits: []WorkItemRef{placed}, ObservedAt: at}
	rendered := standing.renderNotStartable()
	line := "  The deadline item (yoyodyne-9k1y.22) — " + placement.Line() + "; not started: " + placed.WaitReason + "\n"
	if !strings.Contains(rendered, line) {
		t.Fatalf("rendered = %q, want the placement and the kept reason on one line:\n%s", rendered, line)
	}
	if strings.Count(rendered, "yoyodyne-9k1y.22") != 1 {
		t.Fatalf("rendered = %q, want the placed item named once", rendered)
	}
	if !strings.Contains(rendered, "  Other work (yoyodyne-other) — "+placement.Line()+"\n") {
		t.Fatalf("rendered = %q, want the placed item with no kept reason said with its placement alone", rendered)
	}
}

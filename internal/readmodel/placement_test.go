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

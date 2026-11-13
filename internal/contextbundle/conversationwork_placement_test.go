package contextbundle

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The work a recurring pass is handed says which items the Lead Product
// Manager placed first among their priority, in the words every listing of the
// queue uses, and its heading gives the order placements set. An unplaced item
// reads as it did before.
func TestConversationWorkSaysWhichItemsArePlacedFirst(t *testing.T) {
	t.Parallel()

	placement := &beads.Placement{By: domain.RoleProductManager, At: time.Date(2026, 10, 29, 13, 50, 0, 0, time.UTC), Priority: 0, Reason: "promised first by the operator"}
	section := ConversationWorkSection([]beads.WorkItem{
		{ID: "yoyodyne-u6ei", Title: "The promised design", Status: "open", Priority: 0, Placement: placement},
		{ID: "yoyodyne-old", Title: "Older design work", Status: "open", Priority: 0},
	}, "")
	if !strings.Contains(section, "- The promised design (yoyodyne-u6ei) [P0, open]\n    "+placement.Line()+"\n") {
		t.Fatalf("section = %q, want the placement line under the placed item", section)
	}
	if !strings.Contains(section, "- Older design work (yoyodyne-old) [P0, open]\n") || strings.Count(section, "placed first among priority") != 1 {
		t.Fatalf("section = %q, want the unplaced item to read as before", section)
	}
	if !strings.Contains(section, "the items the Lead Product Manager placed first") || strings.Contains(section, "then oldest admitted within each priority") {
		t.Fatalf("section = %q, want the heading to give the order placements set", section)
	}
}

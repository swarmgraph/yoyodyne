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
	section := ConversationWorkSection(domain.RoleArchitect, []beads.WorkItem{
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

// An item named for the role's pass says so under it in the queue, and the
// pass's lead section lists it; an item named for another role's pass says
// nothing here, and a queue with nothing named has no lead section.
func TestConversationWorkSaysWhichItemsWereNamedForThePass(t *testing.T) {
	t.Parallel()

	request := &beads.PassFirst{Role: domain.RoleDevelopmentManager, By: domain.RoleProductManager, At: time.Date(2026, 11, 27, 19, 13, 0, 0, time.UTC), Reason: "lift the design hold by Saturday"}
	elsewhere := &beads.PassFirst{Role: domain.RoleArchitect, By: domain.RoleProductManager, At: request.At, Reason: "rule on it"}
	items := []beads.WorkItem{
		{ID: "yoyodyne-9k1y.22", Title: "Delivered epics", Status: "open", Priority: 0, PassFirst: request},
		{ID: "yoyodyne-other", Title: "Other work", Status: "open", Priority: 0, PassFirst: elsewhere},
	}
	section := ConversationWorkSection(domain.RoleDevelopmentManager, items, "")
	if !strings.Contains(section, "- Delivered epics (yoyodyne-9k1y.22) [P0, open]\n    TAKE FIRST: "+request.Line()+"\n") || strings.Count(section, "TAKE FIRST") != 1 {
		t.Fatalf("section = %q, want the named line under the named item alone", section)
	}
	named := ConversationWorkNamed(domain.RoleDevelopmentManager, items)
	if !strings.HasPrefix(named, "## Take these first") || !strings.Contains(named, "- Delivered epics (yoyodyne-9k1y.22): "+request.Line()) || strings.Contains(named, "yoyodyne-other") {
		t.Fatalf("named = %q", named)
	}
	if got := ConversationWorkNamed(domain.RoleDevelopmentManager, items[1:]); got != "" {
		t.Fatalf("named = %q for a queue with nothing named for this role, want nothing", got)
	}
}

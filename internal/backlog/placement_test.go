package backlog

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Within one priority, placed items come first, earliest placement first, then
// the unplaced ones oldest admitted first; a placement never lifts an item above
// a higher priority, and one among another priority, or on a parked item, does
// not make it pullable or move it. The listing shows the placement line for a
// placed item and nothing new for an unplaced one.
func TestPlacedItemsLeadTheirPriorityAndSaySo(t *testing.T) {
	t.Parallel()

	admitted := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	placed := func(priority int, at time.Time) *beads.Placement {
		return &beads.Placement{By: domain.RoleProductManager, At: at, Priority: priority, Reason: "promised first by the operator"}
	}
	early, late := admitted.Add(48*time.Hour), admitted.Add(49*time.Hour)
	items := []beads.WorkItem{
		{ID: "yoyodyne-p1", Title: "Placed priority 1", Status: statusOpen, Priority: 1, CreatedAt: admitted, Placement: placed(1, early)},
		{ID: "yoyodyne-oldest", Title: "Oldest priority 0", Status: statusOpen, Priority: 0, CreatedAt: admitted},
		{ID: "yoyodyne-late", Title: "Placed later", Status: statusOpen, Priority: 0, CreatedAt: admitted.Add(2 * time.Hour), Placement: placed(0, late)},
		{ID: "yoyodyne-early", Title: "Placed earlier", Status: statusOpen, Priority: 0, CreatedAt: admitted.Add(3 * time.Hour), Placement: placed(0, early)},
		{ID: "yoyodyne-moved", Title: "Placed among priority 2", Status: statusOpen, Priority: 0, CreatedAt: admitted.Add(time.Hour), Placement: placed(2, early)},
		{ID: "yoyodyne-parked", Title: "Placed and parked", Status: statusOpen, Priority: 0, CreatedAt: admitted, Placement: placed(0, admitted), Parking: "deferred"},
	}
	var ready []string
	for _, item := range items {
		ready = append(ready, item.ID)
	}
	queue := Order(items, ready, ReadHolds(nil), nil)
	var order []string
	for _, entry := range queue.Entries {
		order = append(order, entry.ID)
	}
	want := "yoyodyne-parked,yoyodyne-early,yoyodyne-late,yoyodyne-oldest,yoyodyne-moved,yoyodyne-p1"
	if strings.Join(order, ",") != want {
		t.Fatalf("order = %v, want %s", order, want)
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-early" {
		t.Fatalf("Next() = %#v, %v, want the earliest placed item: a parked one stays held", next, ok)
	}
	for _, entry := range queue.Entries {
		if (entry.Placement != nil) != (entry.ID != "yoyodyne-oldest" && entry.ID != "yoyodyne-moved") {
			t.Fatalf("entry %s placement = %#v", entry.ID, entry.Placement)
		}
	}

	rendered := queue.Render()
	line := placed(0, early).Line()
	if !strings.Contains(rendered, "[yoyodyne-early] p0 Placed earlier\n    "+line+"\n") {
		t.Fatalf("rendered = %q, want the placement line under the placed item", rendered)
	}
	if !strings.Contains(rendered, "[yoyodyne-oldest] p0 Oldest priority 0\n  ") || strings.Count(rendered, "placed first among") != 4 {
		t.Fatalf("rendered = %q, want unplaced items to read as before", rendered)
	}
}

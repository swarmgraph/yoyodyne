package chat

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// The tracker view says who holds an item rather than printing the bare
// assignee, so a name a run's claim set is never read as a person's claim.
func TestTheTrackerViewSaysWhoHoldsAnItem(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		item beads.WorkItem
		want string
	}{
		{"a claim's name left behind", beads.WorkItem{ID: "yoyodyne-1", Status: "open", Assignee: "Mason Bryant", ClaimAssignee: true},
			"held by: nobody; the assignee Mason Bryant was left by a run's claim that has ended\n"},
		// This view reads no run records, so a name nothing marks is never said
		// to be set by hand: it may be one an earlier run's claim left.
		{"an unmarked name", beads.WorkItem{ID: "yoyodyne-1", Status: "open", Assignee: "Mason Bryant"},
			"held by: the assignee Mason Bryant, which no run's claim recorded setting\n"},
		{"a run's claim", beads.WorkItem{ID: "yoyodyne-1", Status: "in_progress", Assignee: "Mason Bryant", ClaimAssignee: true},
			"held by: a run the harness started; the tracker has the item claimed for it\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rendered := renderWorkItemHead(test.item, goal.Set{}, false)
			if !strings.Contains(rendered, test.want) || strings.Contains(rendered, "assignee: ") {
				t.Fatalf("rendered:\n%s\nwant it to say %q and print no bare assignee", rendered, test.want)
			}
		})
	}
	if rendered := renderWorkItemHead(beads.WorkItem{ID: "yoyodyne-1", Status: "open"}, goal.Set{}, false); strings.Contains(rendered, "held by") {
		t.Fatalf("an item nobody holds and nobody is assigned to says who holds it:\n%s", rendered)
	}
}

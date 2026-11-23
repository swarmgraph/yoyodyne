package cli

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// The review reads the goals in force and each item with the goal its
// attribution resolves to, by the goal's identifier.
func TestGoalReviewReadingResolvesEachItemsGoal(t *testing.T) {
	t.Parallel()

	goals := goal.Set{Goals: []goal.Goal{
		{Identity: "nearly-autonomous", Statement: "Run development nearly autonomously.", InForce: true},
		{Identity: "retired", Statement: "Something no longer intended.", InForce: false},
	}}
	items := []beads.WorkItem{
		{ID: "yoyodyne-1", Status: "open", Priority: 1, Notes: "Goal served: [nearly-autonomous] Run development nearly autonomously."},
		{ID: "yoyodyne-2", Status: "open", Labels: []string{"raised-by-operator"}},
	}
	reading := goalReviewReading(goals, items)
	const reference = "[nearly-autonomous] Run development nearly autonomously."
	if len(reading.Goals) != 1 || reading.Goals[0].Reference != reference {
		t.Fatalf("goals = %+v, want the one goal in force", reading.Goals)
	}
	if len(reading.Items) != 2 || reading.Items[0].Serves != reference || reading.Items[1].Serves != "" {
		t.Fatalf("items = %+v, want the first serving the goal and the second serving none", reading.Items)
	}
	if len(reading.Items[1].Labels) != 1 {
		t.Errorf("labels = %v, want the item's label carried", reading.Items[1].Labels)
	}
}

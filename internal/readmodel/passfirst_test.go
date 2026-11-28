package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An item the Lead Product Manager named for the development manager's next
// pass leads that pass's order even though a developer carries it and it sits
// behind the role's own work by priority, and a request to another role puts
// nothing in this one's order.
func TestAnItemNamedForARolesPassLeadsItsOrder(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 27, 19, 13, 0, 0, time.UTC)
	toDM := func(at time.Time) *beads.PassFirst {
		return &beads.PassFirst{Role: domain.RoleDevelopmentManager, By: domain.RoleProductManager, At: at, Reason: "deadline Saturday"}
	}
	items := []beads.WorkItem{
		{ID: "own-p0", Status: "open", Priority: 0, Executor: domain.ConversationWith(domain.RoleDevelopmentManager)},
		{ID: "named-later", Status: "open", Priority: 2, PassFirst: toDM(at.Add(time.Minute))},
		{ID: "named-earlier", Status: "blocked", Priority: 1, PassFirst: toDM(at), Parking: "parked for later"},
		{ID: "named-closed", Status: "closed", Priority: 0, PassFirst: toDM(at)},
		{ID: "named-architect", Status: "open", Priority: 0, PassFirst: &beads.PassFirst{Role: domain.RoleArchitect, By: domain.RoleProductManager, At: at, Reason: "x"}},
		{ID: "developer-work", Status: "open", Priority: 0},
	}
	var order []string
	for _, item := range ConversationWork(items, domain.RoleDevelopmentManager) {
		order = append(order, item.ID)
	}
	if got := strings.Join(order, ","); got != "named-earlier,named-later,own-p0" {
		t.Fatalf("order = %s, want the named items first, earliest named first, then the role's own work", got)
	}
}

// Status names an item a pass was handed first and did not take, with who
// named it and the role's reason, and names every other named item it left.
func TestStatusSaysANamedItemWasNotTakenWithTheReason(t *testing.T) {
	t.Parallel()
	recorded := []runstate.Sweep{answeredPass("dm-pass", passesAt,
		runstate.DeliveredWork{ID: "yoyodyne-9k1y.22", Priority: 0, NamedFirst: "named first by the Lead Product Manager: lift the hold", Reason: "the design has not landed"},
		runstate.DeliveredWork{ID: "yoyodyne-ifd.429.314", Priority: 0, NamedFirst: "named first by the Lead Product Manager: break it out", Reason: "waiting on the architect"},
		runstate.DeliveredWork{ID: "yoyodyne-ab2", Priority: 0, Taken: true},
	)}
	passes := ConversationPasses(recorded)
	if len(passes) != 1 {
		t.Fatalf("passes = %+v", passes)
	}
	said := passes[0].Says()
	for _, want := range []string{
		"first in its order: yoyodyne-9k1y.22 (P0), named first by the Lead Product Manager: lift the hold, NOT TAKEN — the design has not landed",
		"also yoyodyne-ifd.429.314 (P0), named first by the Lead Product Manager: break it out, NOT TAKEN — waiting on the architect",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("line = %q, want %q", said, want)
		}
	}
	if strings.Contains(said, "also yoyodyne-ab2") {
		t.Errorf("line = %q names an item that was neither named nor left", said)
	}
}

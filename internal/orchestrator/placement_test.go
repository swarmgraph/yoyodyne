package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// placedAt is a placement first among priority by the Lead Product Manager.
func placedAt(priority int, at time.Time) *beads.Placement {
	return &beads.Placement{By: domain.RoleProductManager, At: at, Priority: priority, Reason: "promised first by the operator"}
}

var (
	admittedEarly = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	admittedLate  = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	placedEarly   = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	placedLate    = time.Date(2026, 10, 28, 13, 0, 0, 0, time.UTC)
)

// The case the design was written for: on October 29 a freed slot took an
// older priority-0 item over the two the Lead Product Manager had promised
// would go first. With the newer item placed first among priority 0, the one
// free slot takes it; with nothing placed, the slot takes the older item, as it
// always did.
func TestAnItemPlacedFirstAmongPriorityZeroIsPulledBeforeOlderPriorityZeroWork(t *testing.T) {
	t.Parallel()

	for _, placed := range []bool{true, false} {
		older := beads.WorkItem{ID: "yoyodyne-ifd.433.41", Title: "Older priority 0 work", Status: "open", Priority: 0, CreatedAt: admittedEarly}
		promised := beads.WorkItem{ID: "yoyodyne-u6ei", Title: "The promised configuration change", Status: "open", Priority: 0, CreatedAt: admittedLate}
		want := older.ID
		if placed {
			promised.Placement = placedAt(0, placedEarly)
			want = promised.ID
		}
		harness := newScheduleHarness(older, promised)
		schedule, err := (Scheduler{Open: harness.open, Limit: 1}).Schedule(context.Background())
		if err != nil {
			t.Fatalf("Schedule() error = %v", err)
		}
		if got := strings.Join(runsStarted(schedule), ","); got != want {
			t.Fatalf("placed=%v: started = %v, want %s", placed, got, want)
		}
	}
}

// Among placed items the earlier placement goes first, so a later promise never
// overtakes an earlier one; and a placement never lifts an item above ready work
// of a higher priority.
func TestPlacedItemsGoEarliestPlacementFirstAndNeverAboveAHigherPriority(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-p1", Title: "Placed priority 1 work", Status: "open", Priority: 1, CreatedAt: admittedEarly, Placement: placedAt(1, placedEarly)},
		beads.WorkItem{ID: "yoyodyne-later", Title: "Placed later", Status: "open", Priority: 0, CreatedAt: admittedEarly, Placement: placedAt(0, placedLate)},
		beads.WorkItem{ID: "yoyodyne-plain", Title: "Unplaced priority 0", Status: "open", Priority: 0, CreatedAt: admittedEarly.Add(-time.Hour)},
		beads.WorkItem{ID: "yoyodyne-earlier", Title: "Placed earlier", Status: "open", Priority: 0, CreatedAt: admittedLate, Placement: placedAt(0, placedEarly)},
	)
	harness.capacity = 4
	schedule, err := (Scheduler{Open: harness.open, Limit: 4}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := "yoyodyne-earlier,yoyodyne-later,yoyodyne-plain,yoyodyne-p1"
	if got := strings.Join(runsStarted(schedule), ","); got != want {
		t.Fatalf("started = %v, want %s", got, want)
	}
}

// A placement made among one priority orders nothing once the item has moved
// to another: the promise was about its place among the old priority.
func TestAPlacementAmongAnotherPriorityOrdersNothing(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-older", Title: "Older priority 0 work", Status: "open", Priority: 0, CreatedAt: admittedEarly},
		beads.WorkItem{ID: "yoyodyne-moved", Title: "Placed among priority 1, now priority 0", Status: "open", Priority: 0, CreatedAt: admittedLate, Placement: placedAt(1, placedEarly)},
	)
	schedule, err := (Scheduler{Open: harness.open, Limit: 1}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := strings.Join(runsStarted(schedule), ","); got != "yoyodyne-older" {
		t.Fatalf("started = %v, want the older item: the placement was among another priority", got)
	}
}

// The operator's rule of October 25 stands above placement: work on a change a
// run already wrote and kept goes first, ahead of an item placed first among
// priority 0, and the placed item takes the next slot.
func TestWorkOnAWrittenChangeStillGoesAheadOfAPlacedItem(t *testing.T) {
	t.Parallel()

	for _, capacity := range []int{1, 2} {
		harness := newScheduleHarness(
			beads.WorkItem{ID: "yoyodyne-older", Title: "Older priority 0 work", Status: "open", Priority: 0, CreatedAt: admittedEarly},
			beads.WorkItem{ID: "yoyodyne-placed", Title: "Promised first", Status: "open", Priority: 0, CreatedAt: admittedLate, Placement: placedAt(0, placedEarly)},
			beads.WorkItem{ID: "yoyodyne-xdj", Title: "Confined writers survive replacement races", Status: "blocked", Priority: 2},
		)
		harness.capacity = capacity
		harness.ReadyItems["yoyodyne-xdj"] = false
		harness.stoppages = preservedStoppage("yoyodyne-xdj")
		fired := map[string]bool{}
		task := decidedTask("yoyodyne-xdj")
		task.Decision = runstate.TriageDecisionRepair
		harness.outstanding = outstandingUntilFired(fired, task)
		harness.carry = carriedWhenASlotIsFree(fired)

		schedule, err := (Scheduler{Open: harness.open, Limit: capacity}).Schedule(context.Background())
		if err != nil {
			t.Fatalf("Schedule() error = %v", err)
		}
		want := "yoyodyne-xdj"
		if capacity == 2 {
			want += ",yoyodyne-placed"
		}
		if got := strings.Join(runsStarted(schedule), ","); got != want {
			t.Fatalf("%d slot(s): started = %v, want %s: the repair of the written change first, then the placed item", capacity, got, want)
		}
	}
}

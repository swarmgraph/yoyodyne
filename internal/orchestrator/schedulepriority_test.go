package orchestrator

// A ready priority-0 item is started before any ready priority-1 item, whatever
// its parent's priority, whatever labels either carries, and whatever lower
// priority run shares a file with it; and work at one priority is still pulled
// in the order it was admitted.
//
// The case yoyodyne-j2u was admitted for: from October 2 to October 9 the
// priority-0 items yoyodyne-ifd.430.51 and yoyodyne-7mk were passed over at every
// pull as racing a priority-1 run over internal/readmodel/claims.go and
// internal/orchestrator/triage.go, while four priority-1 items that shared no
// file with it were started in their place. The run had been stopped for a
// redeploy and nothing was carrying it; the audit that would have ended it could
// not save the ending (TestARunStoppedForARedeployThatNobodyContinuedIsSettled).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// admittedAt is an admission time on the morning of the case, hours apart.
func admittedAt(hour int) time.Time {
	return time.Date(2026, 10, 9, hour, 0, 0, 0, time.UTC)
}

func TestATopPriorityItemTakesTheOneFreeSlotAheadOfSecondTierWork(t *testing.T) {
	t.Parallel()

	// The priority-1 item was admitted first, so nothing but priority puts the
	// priority-0 item ahead of it.
	second := beads.WorkItem{ID: "yoyodyne-second", Title: "Second-tier work", Status: "open", Priority: 1, CreatedAt: admittedAt(1)}
	for _, test := range []struct {
		name  string
		top   beads.WorkItem
		other []beads.WorkItem
		setup func(*scheduleHarness)
		// reason is what the started run's recorded selection has to say.
		reason string
	}{
		{
			name: "an item with no parent",
			top:  beads.WorkItem{ID: "yoyodyne-top", Title: "Main's build", Status: "open", Priority: 0, CreatedAt: admittedAt(5)},
		},
		{
			name: "an item whose parent is at a lower priority",
			top: beads.WorkItem{ID: "yoyodyne-heading.1", Title: "Main's build", Status: "open", Priority: 0,
				CreatedAt: admittedAt(5), Parent: "yoyodyne-heading"},
			other: []beads.WorkItem{{ID: "yoyodyne-heading", Title: "Scheduler and pipeline", Status: "open", Priority: 3, CreatedAt: admittedAt(0)}},
		},
		{
			name: "an item whose parent is at the top priority",
			top: beads.WorkItem{ID: "yoyodyne-urgent.1", Title: "Main's build", Status: "open", Priority: 0,
				CreatedAt: admittedAt(5), Parent: "yoyodyne-urgent"},
			other: []beads.WorkItem{{ID: "yoyodyne-urgent", Title: "Unblock the merges", Status: "open", Priority: 0, CreatedAt: admittedAt(0)}},
		},
		{
			// The only free slot prefers a label the priority-1 item carries and
			// the priority-0 item does not.
			name: "second-tier work carrying the label the free slot prefers",
			top:  beads.WorkItem{ID: "yoyodyne-top", Title: "Main's build", Status: "open", Priority: 0, CreatedAt: admittedAt(5), Labels: []string{"bug"}},
			setup: func(h *scheduleHarness) {
				h.slots = []domain.DeveloperSlot{{Prefer: []string{"reliability"}}}
				h.Items[0].Labels = []string{"reliability"}
			},
			reason: "this item does not carry it, and is taken ahead of that work because it is at priority 0",
		},
		{
			// A run over less urgent work shares a file with the priority-0 item,
			// and holds back a priority-1 item over the same file as before.
			name: "a run over less urgent work sharing a file with it",
			top: beads.WorkItem{ID: "yoyodyne-top", Title: "Main's build", Status: "open", Priority: 0, CreatedAt: admittedAt(5),
				Description: "conflict-surface: internal/readmodel/claims.go"},
			other: []beads.WorkItem{
				{ID: "yoyodyne-also-claims", Title: "Also the claims", Status: "open", Priority: 1, CreatedAt: admittedAt(0),
					Description: "conflict-surface: internal/readmodel/claims.go"},
				{ID: "yoyodyne-going", Title: "Already going", Status: "in_progress", Priority: 1,
					Description: "conflict-surface: internal/readmodel/claims.go"},
			},
			setup: func(h *scheduleHarness) {
				h.capacity = 2
				h.inFlight["yoyodyne-going"] = runstate.State{RunID: "run-going", WorkItemID: "yoyodyne-going", Status: runstate.StatusRunning}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// The tracker lists newest first.
			items := append([]beads.WorkItem{second, test.top}, test.other...)
			harness := newScheduleHarness(items...)
			if test.setup != nil {
				test.setup(harness)
			}

			schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if got := harness.pullOrder(); strings.Join(got, ",") != test.top.ID {
				t.Fatalf("pulled = %v, want the priority-0 item %s alone in the one free slot: %s", got, test.top.ID, schedule.Render())
			}
			reason := harness.selectionFor(test.top.ID).Reason
			if !strings.Contains(reason, "at priority 0") || strings.Contains(reason, "held back") {
				t.Fatalf("reason = %q, want it pulled at priority 0 with nothing ahead of it held back", reason)
			}
			if test.reason != "" && !strings.Contains(reason, test.reason) {
				t.Fatalf("reason = %q, want it to say %q", reason, test.reason)
			}
		})
	}
}

// The exception for a shared file is priority 0's alone. A priority-1 item over
// a file a run over priority-2 work holds is still held back behind that run,
// and the slot goes to work that shares nothing with it.
func TestSecondTierWorkStillWaitsBehindALessUrgentRunOverItsFile(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-second", Title: "Second-tier work", Status: "open", Priority: 1, CreatedAt: admittedAt(1),
			Description: "conflict-surface: internal/readmodel/claims.go"},
		beads.WorkItem{ID: "yoyodyne-third", Title: "Third-tier work", Status: "open", Priority: 2, CreatedAt: admittedAt(0)},
		beads.WorkItem{ID: "yoyodyne-going", Title: "Already going", Status: "in_progress", Priority: 2,
			Description: "conflict-surface: internal/readmodel/claims.go"},
	)
	harness.capacity = 2
	harness.inFlight["yoyodyne-going"] = runstate.State{RunID: "run-going", WorkItemID: "yoyodyne-going", Status: runstate.StatusRunning}

	schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := harness.pullOrder(); strings.Join(got, ",") != "yoyodyne-third" {
		t.Fatalf("pulled = %v, want the priority-1 item held behind run-going and the other item pulled: %s", got, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || !strings.Contains(schedule.Deferred[0].Reason, "run-going") {
		t.Fatalf("deferred = %#v, want the priority-1 item named as waiting on run-going", schedule.Deferred)
	}
}

// Within priority 1 the order is the order of admission, oldest first, however
// the tracker lists the items; and the priority-0 item admitted after all of
// them still goes first.
func TestItemsAtPriorityOneArePulledInTheOrderTheyWereAdmitted(t *testing.T) {
	t.Parallel()

	// Newest first, as the tracker lists them.
	harness := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-top", Title: "Main's build", Status: "open", Priority: 0, CreatedAt: admittedAt(9)},
		beads.WorkItem{ID: "yoyodyne-newest", Title: "Newest", Status: "open", Priority: 1, CreatedAt: admittedAt(8)},
		beads.WorkItem{ID: "yoyodyne-middle", Title: "Middle", Status: "open", Priority: 1, CreatedAt: admittedAt(4)},
		beads.WorkItem{ID: "yoyodyne-oldest", Title: "Oldest", Status: "open", Priority: 1, CreatedAt: admittedAt(2)},
	)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := "yoyodyne-top,yoyodyne-oldest,yoyodyne-middle,yoyodyne-newest"
	if got := harness.pullOrder(); strings.Join(got, ",") != want {
		t.Fatalf("pulled = %v, want %s: %s", got, want, schedule.Render())
	}
}

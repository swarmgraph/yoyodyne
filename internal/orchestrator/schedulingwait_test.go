package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func (h *scheduleHarness) RecordSchedulingWait(_ context.Context, id, class, reason string, at time.Time) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.Items {
		if h.Items[i].ID == id {
			h.Items[i].SchedulingWait, _ = beads.NextSchedulingWait(h.Items[i].SchedulingWait, class, reason, at)
			return h.Items[i], nil
		}
	}
	return beads.WorkItem{}, nil
}

func TestSchedulerKeepsTheReasonOnEarlierReadyWork(t *testing.T) {
	// Priority 0 is never walked past for a label, so the earlier item is at 1.
	h := newScheduleHarness(labelled("yoyodyne-earlier", 1), labelled("yoyodyne-later", 2, "reliability"))
	h.capacity = 1
	h.slots = []domain.DeveloperSlot{{Prefer: []string{"reliability"}}}
	h.Items[0].Title = "Earlier work"
	h.Items[1].Title = "Later reliability work"
	h.Items[0].Notes = "Existing notes"
	_, err := (Scheduler{Open: h.open, Limit: 1}).Schedule(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	wait := h.Items[0].SchedulingWait
	if wait == nil || !strings.Contains(wait.Reason, "Later reliability work (yoyodyne-later)") || wait.FirstPassedOver.IsZero() || wait.Class != string(runstate.PassedOverLeftForAnotherSlot) {
		t.Fatalf("wait = %+v", wait)
	}
	if h.Items[0].Notes != "Existing notes" {
		t.Fatal("notes changed")
	}
	h.mu.Unlock()
}

func TestDecidedRepairLeftUnstartedKeepsItsCurrentReason(t *testing.T) {
	h := newRerunHarness(t, continuableState())
	recordRepairDecision(t, h.runs, docketedItem)
	carrying := h.carryOutAt(2 * time.Minute)
	notes := newContinueHarness(t, continuableState()).tracker
	notes.Item.Notes = "Existing notes"
	carrying.Notes = notes
	task := theOneOutstanding(t, carrying)
	if task.Decision != runstate.TriageDecisionRepair {
		t.Fatalf("decision = %s", task.Decision)
	}
	notes.AlsoHolds = map[string]beads.WorkItem{"yoyodyne-later": {ID: "yoyodyne-later", Title: "Later reliability work"}}
	why := (outrankedCarryOut{priority: 2, ahead: []string{"yoyodyne-later"}, named: []string{"Later reliability work (yoyodyne-later)"}}).reason()
	_, err := carrying.RecordUnattempted(context.Background(), time.Minute, map[string]string{task.RunID: why})
	if err != nil {
		t.Fatal(err)
	}
	if notes.Item.SchedulingWait == nil || !strings.Contains(notes.Item.SchedulingWait.Reason, "Later reliability work (yoyodyne-later)") {
		t.Fatalf("wait = %+v", notes.Item.SchedulingWait)
	}
	first := notes.Item.SchedulingWait.FirstPassedOver
	carrying.Clock = laterClock{after: 3 * time.Minute}
	_, err = carrying.RecordUnattempted(context.Background(), time.Minute, map[string]string{task.RunID: "Intake hold is still in place"})
	if err != nil {
		t.Fatal(err)
	}
	if !notes.Item.SchedulingWait.FirstPassedOver.Equal(first) || notes.Item.SchedulingWait.Reason == why {
		t.Fatalf("replacement = %+v", notes.Item.SchedulingWait)
	}
	if notes.Item.Notes != "Existing notes" {
		t.Fatal("notes changed")
	}
}

func TestSchedulingWaitNamesAreExpandedOnceWithoutChangingRunIdentifiers(t *testing.T) {
	items := map[string]beads.WorkItem{"yoyodyne-task": {ID: "yoyodyne-task", Title: "Earlier work"}}
	reason := "run-yoyodyne-task holds yoyodyne-task"
	want := "run-yoyodyne-task holds Earlier work (yoyodyne-task)"
	if got := schedulingWaitReason(reason, items); got != want || schedulingWaitReason(got, items) != want {
		t.Fatalf("reason = %q", got)
	}
}

// unreadyWaitHarness is a queue whose first item the tracker does not offer:
// child is held back with the parent it was broken out of, which waits on
// blocker, and dependent waits on blocker itself. Only blocker is ready.
func unreadyWaitHarness(parentStatus string) *scheduleHarness {
	h := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-child", Title: "Automatic document confirmation", Status: "open", Priority: 0, Parent: "yoyodyne-parent"},
		beads.WorkItem{ID: "yoyodyne-dependent", Title: "Dependent work", Status: "open", Priority: 0,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-blocker", Type: beads.BlocksDependency}}},
		beads.WorkItem{ID: "yoyodyne-parent", Title: "Document confirmation", Status: parentStatus, Priority: 1,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-blocker", Type: beads.BlocksDependency, Status: "open"}}},
		beads.WorkItem{ID: "yoyodyne-blocker", Title: "Landing runs for documents", Status: "open", Priority: 2},
	)
	h.ReadyItems = map[string]bool{"yoyodyne-blocker": true}
	h.Items[0].Notes = "Existing notes"
	return h
}

func TestAnItemHeldBackThroughItsParentKeepsAReasonNamingTheParent(t *testing.T) {
	// The parent is being worked, so it has left the backlog and only the pull's
	// reading of the claimed work can name it; and the parent still in the
	// backlog, waiting, which the queue names itself.
	for _, parentStatus := range []string{"in_progress", "open"} {
		t.Run(parentStatus, func(t *testing.T) {
			h := unreadyWaitHarness(parentStatus)
			if _, err := (Scheduler{Open: h.open, Limit: 1}).Schedule(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			wait := h.Items[0].SchedulingWait
			if wait == nil || wait.Class != string(runstate.PassedOverWaitingOnOtherWork) ||
				!strings.Contains(wait.Reason, "blocked through its parent 'Document confirmation' (yoyodyne-parent)") ||
				!strings.Contains(wait.Reason, "'Landing runs for documents' (yoyodyne-blocker") {
				t.Fatalf("wait = %+v", wait)
			}
			if h.Items[0].Notes != "Existing notes" {
				t.Fatal("notes changed")
			}
		})
	}
}

func TestAnItemHeldByAnUnfinishedDependencyKeepsAReasonNamingIt(t *testing.T) {
	h := unreadyWaitHarness("in_progress")
	if _, err := (Scheduler{Open: h.open, Limit: 1}).Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	wait := h.Items[1].SchedulingWait
	if wait == nil || wait.Class != string(runstate.PassedOverWaitingOnOtherWork) ||
		wait.Reason != "waiting on Landing runs for documents (yoyodyne-blocker)" {
		t.Fatalf("wait = %+v", wait)
	}
	// What a slot reached is passed over exactly as before: the ready item is
	// started and carries no reason.
	if blocker := h.Items[3]; blocker.SchedulingWait != nil {
		t.Fatalf("started item wait = %+v", blocker.SchedulingWait)
	}
}

func TestAWaitOnOtherWorkIsClearedOnceNothingHoldsTheItem(t *testing.T) {
	at := time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC)
	waiting, _ := beads.NextSchedulingWait(nil, string(runstate.PassedOverWaitingOnOtherWork), "waiting on yoyodyne-blocker", at)
	sequenced, _ := beads.NextSchedulingWait(nil, string(runstate.PassedOverSequencedBehindWork), "would race run-1", at)
	entries := []backlog.Entry{
		{ID: "yoyodyne-released", Status: "open", Ready: true, SchedulingWait: waiting},
		{ID: "yoyodyne-parked", Status: "open", Parking: "parked by the development manager", SchedulingWait: waiting},
		{ID: "yoyodyne-raced", Status: "open", Ready: true, SchedulingWait: sequenced},
		{ID: "yoyodyne-waiting", Status: "open", WaitingOn: []string{"yoyodyne-blocker"}},
	}
	kept := map[string]string{}
	keepUnreadyWaits(entries, map[string]beads.WorkItem{}, func(id string, class runstate.PassedOverClass, reason string) {
		if class != runstate.PassedOverWaitingOnOtherWork {
			t.Fatalf("class = %s", class)
		}
		kept[id] = reason
	})
	want := map[string]string{"yoyodyne-released": "", "yoyodyne-parked": "", "yoyodyne-waiting": "waiting on yoyodyne-blocker"}
	if len(kept) != len(want) {
		t.Fatalf("kept = %#v", kept)
	}
	for id, reason := range want {
		if got, written := kept[id]; !written || got != reason {
			t.Fatalf("kept = %#v, want %#v", kept, want)
		}
	}
}

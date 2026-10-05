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

func (h *scheduleHarness) RecordSchedulingWait(_ context.Context, id, reason string, at time.Time) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.items {
		if h.items[i].ID == id {
			h.items[i].SchedulingWait, _ = beads.NextSchedulingWait(h.items[i].SchedulingWait, reason, at)
			return h.items[i], nil
		}
	}
	return beads.WorkItem{}, nil
}

func TestSchedulerKeepsTheReasonOnEarlierReadyWork(t *testing.T) {
	h := newScheduleHarness(labelled("yoyodyne-earlier", 0), labelled("yoyodyne-later", 1, "reliability"))
	h.capacity = 1
	h.slots = []domain.DeveloperSlot{{Prefer: []string{"reliability"}}}
	h.items[0].Title = "Earlier work"
	h.items[1].Title = "Later reliability work"
	h.items[0].Notes = "Existing notes"
	_, err := (Scheduler{Open: h.open, Limit: 1}).Schedule(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	wait := h.items[0].SchedulingWait
	if wait == nil || !strings.Contains(wait.Reason, "Later reliability work (yoyodyne-later)") || wait.FirstPassedOver.IsZero() {
		t.Fatalf("wait = %+v", wait)
	}
	if h.items[0].Notes != "Existing notes" {
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
	why := (outrankedCarryOut{priority: 2, ahead: []string{"yoyodyne-later"}}).reason()
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

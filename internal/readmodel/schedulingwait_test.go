package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestSharedStandingCarriesTheSchedulersCurrentReason(t *testing.T) {
	wait := &beads.SchedulingWait{Reason: "waiting behind Later work (yoyodyne-later)", FirstPassedOver: moment.Add(-2 * time.Hour), ReasonSince: moment.Add(-time.Hour)}
	item := beads.WorkItem{ID: "yoyodyne-earlier", Title: "Earlier work", Status: "open", SchedulingWait: wait}
	tracker := statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": {item}}, ready: []beads.WorkItem{item}}}
	standing := ReadStanding(context.Background(), Sources{Tracker: tracker, Runs: fakeRuns{}, Now: func() time.Time { return moment }})
	if len(standing.SchedulingWaits) != 1 || standing.SchedulingWaits[0].SchedulingWait != wait {
		t.Fatalf("waits = %+v", standing.SchedulingWaits)
	}
	if !strings.Contains(standing.Render(), wait.Reason) {
		t.Fatalf("terminal omitted reason: %s", standing.Render())
	}
	encoded, err := json.Marshal(standing)
	if err != nil || !strings.Contains(string(encoded), wait.Reason) || !strings.Contains(string(encoded), "first_passed_over") {
		t.Fatalf("dashboard reading = %s, %v", encoded, err)
	}
}

func TestSchedulingWaitProlongedUsesTheCurrentReasonAndConfiguredTime(t *testing.T) {
	wait := &beads.SchedulingWait{Reason: "waiting behind Later work (yoyodyne-later)", FirstPassedOver: moment.Add(-4 * time.Hour), ReasonSince: moment.Add(-90 * time.Minute)}
	item := beads.WorkItem{ID: "yoyodyne-earlier", Title: "Earlier work", Status: "open", SchedulingWait: wait}
	tracker := statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": {item}}, ready: []beads.WorkItem{item}}}
	for _, test := range []struct {
		after time.Duration
		want  bool
	}{{0, true}, {2 * time.Hour, false}, {time.Hour, true}} {
		standing := ReadStanding(context.Background(), Sources{Tracker: tracker, Runs: fakeRuns{}, SchedulingWaitProblemAfter: test.after, Now: func() time.Time { return moment }})
		if got := standing.SchedulingWaits[0].SchedulingWaitProlonged; got != test.want {
			t.Fatalf("after %s: prolonged = %t", test.after, got)
		}
	}
	// Past the time it is a problem, with whose move it is read off the class
	// the scheduler recorded, as the idle line reads it.
	wait.Class = string(runstate.PassedOverSequencedBehindWork)
	standing := ReadStanding(context.Background(), Sources{Tracker: tracker, Runs: fakeRuns{}, SchedulingWaitProblemAfter: time.Hour, Now: func() time.Time { return moment }})
	if len(standing.SchedulingWaitProblems) != 1 {
		t.Fatalf("problems = %+v", standing.SchedulingWaitProblems)
	}
	problem := standing.SchedulingWaitProblems[0]
	whose := passedOverMoves[runstate.PassedOverSequencedBehindWork]
	if problem.WorkItemID != item.ID || problem.SchedulingWaitWhose != whose || !strings.Contains(problem.WaitReason, "problem:") {
		t.Fatalf("problem = %+v", problem)
	}
	if rendered := standing.Render(); !strings.Contains(rendered, whose) || !strings.Contains(rendered, wait.Reason) {
		t.Fatalf("terminal omitted the problem or its owner: %s", rendered)
	}
	encoded, err := json.Marshal(standing)
	if err != nil || !strings.Contains(string(encoded), `"scheduling_wait_problems":[{`) {
		t.Fatalf("dashboard reading = %s, %v", encoded, err)
	}
	wait.ReasonSince = moment.Add(-time.Minute)
	if schedulingWaitProlonged(wait, moment, time.Hour) {
		t.Fatal("a changed reason retained the age of the earlier reason")
	}
}

func TestRunningWorkSuppressesAnOldSchedulingReason(t *testing.T) {
	wait := &beads.SchedulingWait{Reason: "obsolete waiting reason", FirstPassedOver: moment.Add(-time.Hour), ReasonSince: moment.Add(-time.Hour)}
	item := beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open", SchedulingWait: wait}
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": {item}}, ready: []beads.WorkItem{item}}}
	sources.Runs = fakeRuns{incomplete: []runstate.State{{RunID: "run-a", WorkItemID: item.ID, WorkItemTitle: item.Title, Status: runstate.StatusRunning, StartedAt: moment}}}
	standing := ReadStanding(context.Background(), sources)
	for _, ref := range append(standing.AdmittedItems, standing.StartableItems...) {
		if ref.SchedulingWait != nil || ref.WaitReason != "" || ref.SchedulingWaitProlonged {
			t.Fatalf("running work retains a wait: %+v", ref)
		}
	}
	encoded, _ := json.Marshal(standing)
	if strings.Contains(string(encoded), wait.Reason) {
		t.Fatalf("running reading includes old reason: %s", encoded)
	}
}

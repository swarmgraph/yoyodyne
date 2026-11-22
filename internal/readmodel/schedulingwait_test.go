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

func TestAWaitOnOtherWorkIsSaidOnceAndNeverAProblemByStanding(t *testing.T) {
	class := string(runstate.PassedOverWaitingOnOtherWork)
	held := beads.WorkItem{ID: "yoyodyne-waiting", Title: "Waiting work", Status: "open",
		Dependencies:   []beads.Dependency{{ID: "yoyodyne-blocker", Type: beads.BlocksDependency}},
		SchedulingWait: &beads.SchedulingWait{Class: class, Reason: "waiting on yoyodyne-blocker", FirstPassedOver: moment.Add(-9 * time.Hour), ReasonSince: moment.Add(-9 * time.Hour)}}
	blocker := beads.WorkItem{ID: "yoyodyne-blocker", Title: "Blocker", Status: "open"}
	// Released since the last pull: the next pull clears it, and nothing says it meanwhile.
	released := beads.WorkItem{ID: "yoyodyne-released", Title: "Released work", Status: "open",
		SchedulingWait: &beads.SchedulingWait{Class: class, Reason: "waiting on yoyodyne-gone", FirstPassedOver: moment.Add(-time.Hour), ReasonSince: moment.Add(-time.Hour)}}
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": {held, blocker, released}}, ready: []beads.WorkItem{blocker, released}}}
	standing := ReadStanding(context.Background(), sources)

	var refusal string
	for _, refused := range standing.NotStartable {
		if refused.WorkItemID == held.ID {
			refusal = refused.Reason
		}
	}
	if strings.Count(refusal, "waiting on yoyodyne-blocker") != 1 || !strings.Contains(refusal, "first passed over at") || strings.Contains(refusal, "problem") {
		t.Fatalf("refusal = %q", refusal)
	}
	if len(standing.SchedulingWaitProblems) != 0 {
		t.Fatalf("problems = %+v", standing.SchedulingWaitProblems)
	}
	for _, ref := range append(standing.AdmittedItems, standing.StartableItems...) {
		if ref.WorkItemID == released.ID && (ref.SchedulingWait != nil || ref.WaitReason != "") {
			t.Fatalf("a released item still says its old wait: %+v", ref)
		}
	}
	if rendered := standing.Render(); strings.Contains(rendered, "yoyodyne-gone") {
		t.Fatalf("terminal says a cleared wait: %s", rendered)
	}
}

// A ready priority 0 item no slot's labels cover, left for another developer
// slot, is shown on status with how long it has waited, counted from when it was
// first passed over, and that is said whether or not the wait has yet stood long
// enough to be called a problem.
func TestStatusSaysHowLongAnItemLeftForAnotherSlotHasWaited(t *testing.T) {
	reason := "left for another developer slot: Main's build is red (yoyodyne-urgent) is at priority 0, and no developer slot's labels cover it, so it is waiting for a developer slot with none of its own labelled work ready; developer slot 1 prefers the team-development label and took Team work (yoyodyne-team)"
	for _, test := range []struct {
		name    string
		since   time.Duration
		waited  string
		problem bool
	}{
		{name: "recent", since: 20 * time.Minute, waited: "it has waited 20 minutes"},
		{name: "standing", since: 5 * time.Hour, waited: "it has waited 5 hours", problem: true},
		{name: "days", since: 72 * time.Hour, waited: "it has waited 3 days", problem: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			wait := &beads.SchedulingWait{Class: string(runstate.PassedOverLeftForAnotherSlot), Reason: reason, FirstPassedOver: moment.Add(-test.since), ReasonSince: moment.Add(-test.since)}
			item := beads.WorkItem{ID: "yoyodyne-urgent", Title: "Main's build is red", Status: "open", Priority: 0, SchedulingWait: wait}
			tracker := statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": {item}}, ready: []beads.WorkItem{item}}}
			standing := ReadStanding(context.Background(), Sources{Tracker: tracker, Runs: fakeRuns{}, Now: func() time.Time { return moment }})
			if len(standing.SchedulingWaits) != 1 {
				t.Fatalf("waits = %+v", standing.SchedulingWaits)
			}
			said := standing.SchedulingWaits[0].WaitReason
			if !strings.Contains(said, reason) || !strings.Contains(said, test.waited) {
				t.Fatalf("wait reason = %q, want the reason and %q", said, test.waited)
			}
			if got := standing.SchedulingWaits[0].SchedulingWaitProlonged; got != test.problem {
				t.Fatalf("prolonged = %t, want %t", got, test.problem)
			}
			if rendered := standing.Render(); !strings.Contains(rendered, test.waited) {
				t.Fatalf("terminal omitted how long it waited: %s", rendered)
			}
		})
	}
	// A wait of any other class says nothing more.
	other := beads.SchedulingWait{Class: string(runstate.PassedOverSequencedBehindWork), Reason: "behind run-1", FirstPassedOver: moment.Add(-time.Hour), ReasonSince: moment.Add(-time.Hour)}
	if said := waitedFor(&other, moment); said != "" {
		t.Fatalf("waitedFor(other class) = %q, want nothing", said)
	}
}

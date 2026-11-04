package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// personMergesStub is the runs waiting for a person to merge, which a test can
// change between pulls as a forge check settling a merge would.
type personMergesStub struct {
	mu      sync.Mutex
	waiting []runstate.State
	err     error
}

func (p *personMergesStub) WaitingForPersonMerge() ([]runstate.State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]runstate.State(nil), p.waiting...), p.err
}

func (p *personMergesStub) merge(number int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.waiting[:0]
	for _, state := range p.waiting {
		if state.PersonMerge.Number != number {
			kept = append(kept, state)
		}
	}
	p.waiting = kept
}

// waitingForMerge is a run whose approved change waits on pull request number
// for a person to merge it.
func waitingForMerge(number int, workItemID, title string, since time.Time) runstate.State {
	return runstate.State{
		RunID:         fmt.Sprintf("run-%d", number),
		WorkItemID:    workItemID,
		WorkItemTitle: title,
		Status:        runstate.StatusSucceeded,
		PullRequest:   &runstate.PullRequest{Number: number, Branch: fmt.Sprintf("yoyodyne/%s", workItemID)},
		PersonMerge:   &runstate.PersonMerge{Number: number, Since: since},
	}
}

func waitingOnPullRequests(count int, since time.Time) *personMergesStub {
	stub := &personMergesStub{}
	for number := 1; number <= count; number++ {
		stub.waiting = append(stub.waiting, waitingForMerge(number, fmt.Sprintf("yoyodyne-w%d", number), fmt.Sprintf("Waiting change %d", number), since.Add(time.Duration(number)*time.Minute)))
	}
	return stub
}

// At the limit a pull chooses nothing from the queue and says why, naming
// every pull request by number and by its work item's title.
func TestAPullStartsNoNewWorkAtTheLimitOnPullRequestsWaitingForAPerson(t *testing.T) {
	t.Parallel()
	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.maxWaitingPullRequests = 2
	harness.personMerges = waitingOnPullRequests(2, harness.clock())

	held, err := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 0 || len(held.Started) != 0 {
		t.Fatalf("pulled %v and started %d, want nothing new started at the limit", order, len(held.Started))
	}
	if held.Stopped != ScheduleWaitingPullRequests || held.WaitingPullRequests == nil {
		t.Fatalf("stopped = %q, waiting %#v, want the drain stopped on the limit", held.Stopped, held.WaitingPullRequests)
	}
	rendered := held.Render()
	for _, want := range []string{
		"Not starting new work because 2 pull requests are waiting for a person to merge",
		"#1 for Waiting change 1 (yoyodyne-w1)",
		"#2 for Waiting change 2 (yoyodyne-w2)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("schedule does not say %q:\n%s", want, rendered)
		}
	}
}

// Under the limit, and with a limit of zero however many wait, new work is
// started as ever.
func TestNewWorkStartsUnderTheLimitAndWithNoLimit(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		limit   int
		waiting int
	}{
		{name: "under the limit", limit: 3, waiting: 2},
		{name: "no limit", limit: 0, waiting: 12},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newScheduleHarness(readyItems("yoyodyne-one")...)
			harness.maxWaitingPullRequests = test.limit
			harness.personMerges = waitingOnPullRequests(test.waiting, harness.clock())
			schedule, err := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if order := harness.pullOrder(); len(order) != 1 || schedule.Stopped == ScheduleWaitingPullRequests || schedule.WaitingPullRequests != nil {
				t.Fatalf("pulled %v, stopped %q, want the item started", order, schedule.Stopped)
			}
		})
	}
}

// A watching session waits at the limit, choosing nothing, and starts new
// work by itself at the first pull after a merge brings the count below it.
func TestAWatchingSessionResumesOnceAPullRequestWaitingForAPersonIsMerged(t *testing.T) {
	t.Parallel()
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.maxWaitingPullRequests = 2
	waits := waitingOnPullRequests(2, harness.clock())
	harness.personMerges = waits
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if order := h.pullOrder(); len(order) != 0 {
			return false
		}
		if sleeps == 2 {
			waits.merge(1)
		}
		return sleeps < 6
	}

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Polls < 2 {
		t.Fatalf("polls = %d, want the session to have waited at the limit before the merge", schedule.Polls)
	}
	if order := harness.pullOrder(); len(order) != 1 || order[0] != "yoyodyne-one" {
		t.Fatalf("pulled %v, want the item started once the merge brought the count below the limit", order)
	}
	if schedule.WaitingPullRequests != nil {
		t.Fatalf("waiting = %#v, want nothing recorded once the limit no longer stood", schedule.WaitingPullRequests)
	}
}

// A decided repair is not new work: it is carried out at the limit, while
// nothing is chosen from the queue beside it.
func TestADecidedRepairIsCarriedOutAtTheLimitOnWaitingPullRequests(t *testing.T) {
	t.Parallel()
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 3
	harness.maxWaitingPullRequests = 1
	harness.personMerges = waitingOnPullRequests(1, harness.clock())
	harness.outstanding = outstandingUntilAttempted(decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}

	schedule, err := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.carried) != 1 || harness.carried[0].WorkItemID != "yoyodyne-ifd.346" {
		t.Fatalf("carried = %#v, want the decided repair carried out at the limit", harness.carried)
	}
	if order := harness.pullOrder(); len(order) != 0 || schedule.Stopped != ScheduleWaitingPullRequests {
		t.Fatalf("pulled %v, stopped %q, want nothing chosen from the queue beside it", order, schedule.Stopped)
	}
}

// Waits that cannot be read stop nothing, and the pull says so.
func TestUnreadableWaitingPullRequestsHoldNothingBack(t *testing.T) {
	t.Parallel()
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.maxWaitingPullRequests = 1
	harness.personMerges = &personMergesStub{err: errors.New("the run index is locked")}
	schedule, err := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 1 || !strings.Contains(schedule.WaitingPullRequestsProblem, "the run index is locked") {
		t.Fatalf("pulled %v, problem %q, want the item started and the failed reading said", order, schedule.WaitingPullRequestsProblem)
	}
}

// Every pass of the Lead Product Manager's own recurring task is handed why no
// new work is starting while the limit stands, and nothing about it otherwise;
// a pass of another role's is handed nothing about it.
func TestTheLeadProductManagersSweepIsToldWhyNoNewWorkIsStarting(t *testing.T) {
	t.Parallel()
	said := "Not starting new work because 5 pull requests are waiting for a person to merge (the limit is 5): #1 for First (example-1)."
	for _, test := range []struct {
		name  string
		tasks map[string]config.RecurringTask
		says  string
		want  bool
	}{
		{name: "at the limit", tasks: productManagerSweep(), says: said, want: true},
		{name: "under it", tasks: productManagerSweep(), says: ""},
		{name: "another role", tasks: hourlyTask("sweep"), says: said},
	} {
		t.Run(test.name, func(t *testing.T) {
			role := &wokenRole{answers: []scriptedTurn{{result: complete("swept")}}}
			store := sweepStore(t)
			trigger := Trigger{Tasks: test.tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{},
				WaitingPullRequests: func() string { return test.says }}
			if _, err := trigger.Fire(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(role.messages) != 1 {
				t.Fatalf("messages = %d, want one pass", len(role.messages))
			}
			told := strings.Contains(role.messages[0], "## New work is not starting") && strings.Contains(role.messages[0], said)
			if told != test.want {
				t.Fatalf("told = %v, want %v:\n%s", told, test.want, role.messages[0])
			}
		})
	}
}

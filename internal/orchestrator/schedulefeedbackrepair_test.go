package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// feedbackRepairStub stands in for the development manager's decision to repair
// an item from feedback on a pull request: nothing is owed until decide is
// called, and the repair starts through the harness's own start, as the real one
// starts a new run of the item.
type feedbackRepairStub struct {
	harness *scheduleHarness
	item    string

	mu      sync.Mutex
	decided bool
	started bool
}

func (f *feedbackRepairStub) decide() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decided = true
}

func (f *feedbackRepairStub) Owed(context.Context) ([]FeedbackRepairTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.decided || f.started {
		return nil, nil
	}
	return []FeedbackRepairTask{{WorkItemID: f.item, RunID: "run-landed", PullRequest: 7, FeedbackID: "feedback-1"}}, nil
}

func (f *feedbackRepairStub) CarryTask(ctx context.Context, task FeedbackRepairTask) TeamFeedbackRepairCarried {
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
	carryOutBegun(ctx)
	outcome, err := f.harness.start(ctx, task.WorkItemID, runstate.Selection{By: runstate.SelectedByDevelopmentManager, Reason: "repair from feedback"})
	carried := TeamFeedbackRepairCarried{RunID: task.RunID, WorkItemID: task.WorkItemID, Started: true, NewRunID: outcome.RunID, Outcome: &outcome}
	if err != nil {
		carried.Failure = err.Error()
	}
	return carried
}

// Every slot is taken, a repair is decided from feedback on a pull request, and
// one slot is freed while fresh work stands ready: the repair takes it, ahead of
// the fresh work, as any decided repair of a change a run already wrote does.
func TestAFreedSlotTakesARepairDecidedFromFeedbackAheadOfFreshWork(t *testing.T) {
	t.Parallel()

	replay := newSlotReplay(
		labelled("yoyodyne-first", 0),
		labelled("yoyodyne-second", 0),
		labelled("yoyodyne-fresh", 0),
	)
	replay.releases["yoyodyne-landed"] = make(chan struct{})
	harness := replay.harness
	harness.capacity = 2
	repair := &feedbackRepairStub{harness: harness, item: "yoyodyne-landed"}
	harness.feedbackRepairs = repair

	go func() {
		// Both slots are full once two items have started; the repair is decided
		// then, before either frees.
		for len(harness.pullOrder()) < 2 {
			<-harness.started
		}
		repair.decide()
		replay.release(t, "yoyodyne-first", 2)
		replay.release(t, "yoyodyne-second", 3)
		replay.release(t, "yoyodyne-landed", 4)
		replay.release(t, "yoyodyne-fresh", 4)
	}()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	got := strings.Join(startedOrder(schedule), ",")
	if want := "yoyodyne-first,yoyodyne-second,yoyodyne-landed,yoyodyne-fresh"; got != want {
		t.Fatalf("start order = %s, want %s\n%s", got, want, schedule.Render())
	}
}

package orchestrator

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// standInGitHub is the forge as a pull reads it for the runs waiting on its
// checks: the required checks are still running, so nothing is continued.
type standInGitHub struct{ reads atomic.Int32 }

func (g *standInGitHub) Decided(context.Context, runstate.State) (bool, string, error) {
	g.reads.Add(1)
	return false, "the required checks are still running", nil
}

// waitingContinuations is what a pull reads to continue paused runs: nothing
// was asked to stop, and no item waits on other work.
type waitingContinuations struct{}

func (waitingContinuations) Show(_ context.Context, id string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Status: "in_progress"}, nil
}

func (waitingContinuations) StopRequested(string) (runstate.StopRequest, bool, error) {
	return runstate.StopRequest{}, false, nil
}

func waitingOnGitHub(workItemID, runID string, pushed time.Time) runstate.State {
	return runstate.State{
		RunID: runID, WorkItemID: workItemID, Status: runstate.StatusRunning,
		WorktreePath: "/worktrees/" + workItemID, Branch: "yoyodyne/" + workItemID, BaseCommit: "base",
		CIWait: &runstate.CIWait{Commit: "c0ffee", Branch: "yoyodyne/" + workItemID, PushedAt: pushed, Required: []string{"test", "race", "vet"}},
	}
}

// Both developer slots are free on record: every run in them waits on GitHub's
// checks, and such a run holds no slot. The development manager's decided
// recoveries go ahead of fresh work, and each is refused only after the tracker
// keeps it waiting for minutes. A pull with no slot left still fires one, so
// its refusal for a full harness is recorded; that start was given no slot, and
// until it was left out of the count it held one at every later pull, so the
// count read full for as long as decisions kept coming and ready priority 0
// work never started. The fresh item is started once the decisions that took
// the slots have waited past the grace, while the ones fired with no slot
// still wait; and a pull that counts every slot taken says what it counted.
func TestReadyWorkStartsBesideRunsWaitingOnGitHubWhileDecisionsWaitOnTheTracker(t *testing.T) {
	t.Parallel()

	fresh := beads.WorkItem{ID: "yoyodyne-ifd.430.81", Title: "Fix the architect's failing pass", Status: "open", Priority: 0}
	harness := newScheduleHarness(fresh)
	harness.capacity = 2
	harness.poll = 10 * time.Millisecond
	pushed := harness.now.Add(-30 * time.Minute)
	harness.inFlight["yoyodyne-ifd.429.193"] = waitingOnGitHub("yoyodyne-ifd.429.193", "run-3411062aafcac3446fdc0eb316a56567", pushed)
	harness.inFlight["yoyodyne-ifd.78.2"] = waitingOnGitHub("yoyodyne-ifd.78.2", "run-b7cfb454e01d4fdce9794a62155292cc", pushed)

	decisions := []CarryOutTask{
		decidedTaskOf("yoyodyne-ifd.309", "run-152784a8226c6b4d3601b858dd546acf"),
		decidedTaskOf("yoyodyne-ifd.207", "run-67ade78baafe115c2bcff58aa61728a6"),
		decidedTaskOf("yoyodyne-ifd.187", "run-cccccccccccccccccccccccccccccccc"),
		decidedTaskOf("yoyodyne-ifd.434.12", "run-dddddddddddddddddddddddddddddddd"),
	}
	for index := range decisions {
		decisions[index].DecidedAt = harness.now.Add(time.Duration(index) * time.Minute)
	}
	harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) { return decisions, nil }
	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		<-release
		return CarriedOut{WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision, Gate: runstate.TriageGateHarness,
			Problem: "refused by the work item's own state: the work item is closed"}, Outcome{}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var freshStarted atomic.Bool
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if id == fresh.ID {
			freshStarted.Store(true)
		}
		return h.complete(id), nil
	}
	// Every pull is a minute later than the one before, so the decisions given
	// a slot pass the grace a dispatch has within a few pulls.
	harness.onPull = func(h *scheduleHarness, pulls int) {
		h.mu.Lock()
		h.now = h.now.Add(time.Minute)
		h.mu.Unlock()
		if freshStarted.Load() || pulls > 12 {
			letGo()
			cancel()
		}
	}
	giveUp := time.AfterFunc(20*time.Second, func() { letGo(); cancel() })
	defer giveUp.Stop()

	forge := &standInGitHub{}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CIWaits = forge
		pull.Continuations = waitingContinuations{}
		return pull, err
	}
	sessions := &recordedSessions{}
	_, _ = Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions}.Schedule(ctx)

	if forge.reads.Load() == 0 {
		t.Fatal("no pull asked the stand-in GitHub about the runs waiting on its checks")
	}
	if !freshStarted.Load() {
		t.Fatalf("pulled %v, want the ready priority 0 item started into a slot the runs waiting on GitHub do not hold", harness.pullOrder())
	}
	var full *runstate.EmptySlots
	for _, note := range emptySlotNotes(sessions) {
		if note.Cleared && note.Ended == runstate.EmptySlotsEveryTaken {
			copied := note
			full = &copied
		}
	}
	if full == nil {
		t.Fatalf("empty slot notes = %+v, want a pull that counted every slot taken to say so", emptySlotNotes(sessions))
	}
	says := full.Says()
	if !strings.Contains(says, "every developer slot is counted taken, by yoyodyne-ifd.") {
		t.Fatalf("says %q, want what the slots were counted taken by named", says)
	}
	if strings.Contains(says, "yoyodyne-ifd.429.193") || strings.Contains(says, "yoyodyne-ifd.78.2") {
		t.Fatalf("says %q, want the runs waiting on GitHub never counted against a slot", says)
	}
}

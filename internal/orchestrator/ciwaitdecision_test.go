package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// passedGitHub is the forge as a pull reads it for a run whose required checks
// all passed on the commit it waits for.
type passedGitHub struct{}

func (passedGitHub) Decided(context.Context, runstate.State) (bool, string, error) {
	return true, ciAllPassed, nil
}

// recordingRuns is the scheduling harness's runs with the store's two readings
// a pull asks of them beyond the records: whether a run's worker is alive (a
// run named gone is one whose program ended), and a narrow update made under
// the run's write lock, applied here to the record the harness holds.
type recordingRuns struct {
	*scheduleHarness
	gone map[string]bool
}

func (r recordingRuns) HoldsDeveloperSlotNow(state runstate.State, _ time.Time) bool {
	return state.HoldsDeveloperSlot() && !r.gone[state.RunID]
}

func (r recordingRuns) Update(_ context.Context, runID string, update runstate.RunUpdate) (runstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, state := range r.inFlight {
		if state.RunID != runID {
			continue
		}
		next, _, err := runstate.ApplyUpdate(state, update)
		if err != nil {
			return state, err
		}
		r.inFlight[id] = next
		return next, nil
	}
	return runstate.State{}, fmt.Errorf("run %s is not recorded", runID)
}

func (r recordingRuns) record(workItemID string) runstate.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inFlight[workItemID]
}

// unpinnedInSlot records a developer slot on a run with no endpoint pair, so
// the store moves it to any free slot when it is taken up again while another
// run holds the one it recorded.
func unpinnedInSlot(state runstate.State, slot int, at time.Time) runstate.State {
	state.Routing = &runstate.RunRouting{
		Version: 2,
		Slot:    &runstate.RoutedSlot{Number: slot, Origin: runstate.RoutingClaimed, RecordedAt: at},
	}
	return state
}

// askedToContinue gives a run the development manager's outstanding request,
// made through run.recover, to continue it.
func askedToContinue(state runstate.State, requestID string, at time.Time) runstate.State {
	state.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: requestID, Action: runstate.RecoverContinue, Reason: "its checks passed on GitHub an hour ago", RequestedAt: at}
	return state
}

// GitHub's decision on the commit a run waits for is recorded on the run at
// the first pull that reads it, whether or not the run can be continued then.
// Here neither can, because every developer slot is busy: one run's program
// ended across a restart after it had taken its slot back, and the other waits
// as usual. Before, the decision was written only by the run itself once
// continued, so both records went on naming a wait on checks GitHub had
// finished hours before. The development manager's request to continue the
// first run is not started to be refused: it says on the request that it is
// continued at the first free slot, ahead of new work.
func TestGitHubsDecisionIsRecordedOnARunWaitingAcrossARestartWhileItsSlotIsBusy(t *testing.T) {
	t.Parallel()

	const (
		restarted = "yoyodyne-ifd.429.127.2"
		waiting   = "yoyodyne-ifd.432.25.14"
		holder    = "yoyodyne-ifd.428.181"
		busy      = "yoyodyne-ifd.431.13"
	)
	harness := newScheduleHarness()
	harness.capacity = 2
	begun := harness.now
	pushed := begun.Add(-3 * time.Hour)

	lost := inSlot(waitingOnGitHub(restarted, "run-e63fbad1", pushed), 2, pushed)
	lost.CIWait.WaitLimitSeconds = int64(time.Hour / time.Second)
	lost.CIWait.SlotTaken = true
	lost = askedToContinue(lost, "dm-1120-1427-429127", pushed.Add(time.Hour))
	harness.inFlight[restarted] = lost
	plain := unpinnedInSlot(waitingOnGitHub(waiting, "run-74eea4a9", pushed), 1, pushed)
	harness.inFlight[waiting] = plain
	harness.inFlight[holder] = inSlot(runstate.State{RunID: "run-a15c973e", WorkItemID: holder, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping}, 2, pushed)
	harness.inFlight[busy] = unpinnedInSlot(runstate.State{RunID: "run-e9a6de79", WorkItemID: busy, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping}, 1, pushed)

	runs := recordingRuns{scheduleHarness: harness, gone: map[string]bool{"run-e63fbad1": true}}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		pull.CIWaits = passedGitHub{}
		pull.Continuations = waitingContinuations{}
		return pull, err
	}
	// A watching session, for a few pulls: it is the one that re-adopts runs
	// with nobody behind them, which is where her request used to start the run
	// at every pull to be refused for want of its slot.
	harness.poll = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	harness.onPull = func(_ *scheduleHarness, pulls int) {
		if pulls > 4 {
			cancel()
		}
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return ctx.Err() == nil }
	schedule, _ := Scheduler{Open: open, Watching: true, Now: harness.clock, Sleep: harness.sleep, Sessions: &recordedSessions{}}.Schedule(ctx)

	if order := harness.pullOrder(); len(order) != 0 || len(schedule.Started) != 0 {
		t.Fatalf("starts = %v (%#v), want nothing started while no slot either run can take is free", order, schedule.Started)
	}
	for _, id := range []string{restarted, waiting} {
		record := runs.record(id)
		decided := record.CIWait.Decided
		if decided == nil || !decided.Passed || decided.Says != ciAllPassed || decided.At.Before(begun) || decided.At.After(harness.clock()) {
			t.Fatalf("%s's wait = %#v, want GitHub's pass recorded on it during the session", id, record.CIWait)
		}
		if !record.UpdatedAt.Equal(harness.inFlight[id].UpdatedAt) || record.CIWait.Commit != "c0ffee" {
			t.Fatalf("%s's record = %#v, want the decision recorded without moving its wait or its place in line", id, record)
		}
	}
	asked := runs.record(restarted).RecoveryContinuation
	if asked == nil || asked.TakenUpAt != nil || asked.Refused != "" {
		t.Fatalf("request = %#v, want it still outstanding", asked)
	}
	for _, says := range []string{"continued at the first pull with a developer slot free, ahead of any new work", ciAllPassed} {
		if !strings.Contains(asked.Waiting, says) {
			t.Fatalf("request waiting = %q, want it to say %q", asked.Waiting, says)
		}
	}
	if asked.WaitingSince == nil || asked.WaitingSince.Before(begun) {
		t.Fatalf("request waiting since = %v, want a time during the session", asked.WaitingSince)
	}
}

// A run whose wait on GitHub has ended is continued by the first developer
// slot to free, while the slot it recorded is still busy and before any fresh
// item is started. It keeps no endpoint pair, so the slot it recorded does not
// bind it. The development manager asked for it too, and it is started once,
// by the one continuation, which says it carries out her request.
func TestARunWhoseGitHubWaitEndedTakesTheFirstFreedSlotWhileItsOwnIsBusy(t *testing.T) {
	t.Parallel()

	const (
		id     = "yoyodyne-ifd.429.143"
		holder = "yoyodyne-ifd.428.181"
		ending = "yoyodyne-ifd.432.25.15"
	)
	fresh := beads.WorkItem{ID: "yoyodyne-ifd.431.13", Title: "warn about a stale build", Status: "open", Priority: 0}
	harness := newScheduleHarness(fresh)
	harness.capacity = 2
	harness.poll = 5 * time.Millisecond
	pushed := harness.now.Add(-2 * time.Hour)
	waiting := unpinnedInSlot(waitingOnGitHub(id, "run-0f9c7876", pushed), 2, pushed)
	waiting.CIWait.WaitLimitSeconds = int64(time.Hour / time.Second)
	waiting = askedToContinue(waiting, "dm-1120-1610-429143", pushed.Add(90*time.Minute))
	harness.inFlight[id] = waiting
	harness.inFlight[holder] = inSlot(runstate.State{RunID: "run-a15c973e", WorkItemID: holder, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping}, 2, pushed)
	harness.inFlight[ending] = unpinnedInSlot(runstate.State{RunID: "run-380d6f6a", WorkItemID: ending, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping}, 1, pushed)

	runs := recordingRuns{scheduleHarness: harness, gone: map[string]bool{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	holderBusyAtStart := false
	var waitedBeforeSlot string
	harness.run = func(h *scheduleHarness, workItemID string) (Outcome, error) {
		if workItemID == id {
			h.mu.Lock()
			_, held := h.inFlight[holder]
			h.mu.Unlock()
			mu.Lock()
			holderBusyAtStart = held
			mu.Unlock()
		}
		return h.complete(workItemID), nil
	}
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 3 {
			// What the pulls before this one wrote on her request, and then the
			// run in slot 1 finishes; slot 2 stays busy for the whole test.
			mu.Lock()
			if asked := runs.record(id).RecoveryContinuation; asked != nil {
				waitedBeforeSlot = asked.Waiting
			}
			mu.Unlock()
			h.mu.Lock()
			delete(h.inFlight, ending)
			h.mu.Unlock()
		}
		if slices.Contains(h.pullOrder(), fresh.ID) || pulls > 40 {
			cancel()
		}
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return ctx.Err() == nil }
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		pull.CIWaits = passedGitHub{}
		pull.Continuations = waitingContinuations{}
		return pull, err
	}
	schedule, _ := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: &recordedSessions{}}.Schedule(ctx)

	order := harness.pullOrder()
	continued, started := slices.Index(order, id), slices.Index(order, fresh.ID)
	if continued < 0 || (started >= 0 && started < continued) {
		t.Fatalf("starts = %v, want the run whose wait ended continued before the fresh item", order)
	}
	// Every start the scheduler made for the run counts, including one turned
	// away before the run went on: the re-adoption of runs with nobody behind
	// them used to start it at every pull, slot or no slot, and be refused.
	var attempts []Started
	for _, entry := range schedule.Started {
		if entry.WorkItemID == id {
			attempts = append(attempts, entry)
		}
	}
	if len(attempts) != 1 || attempts[0].Readoptions != 0 || attempts[0].Failure != "" || attempts[0].Declined != "" {
		t.Fatalf("starts for the run = %#v, want one continuation, although the development manager asked for it as well", attempts)
	}
	mu.Lock()
	defer mu.Unlock()
	if !holderBusyAtStart {
		t.Fatalf("the run was continued only after slot 2 freed, want it continued in the first slot to free while slot 2 was busy")
	}
	reason := harness.selectionFor(id).Reason
	for _, says := range []string{"run-0f9c7876", ciAllPassed, "dm-1120-1610-429143"} {
		if !strings.Contains(reason, says) {
			t.Fatalf("reason = %q, want it to say %q", reason, says)
		}
	}
	if !strings.Contains(waitedBeforeSlot, "continued at the first pull with a developer slot free, ahead of any new work") {
		t.Fatalf("request waiting while no slot was free = %q, want it to say it is continued at the first free slot", waitedBeforeSlot)
	}
}

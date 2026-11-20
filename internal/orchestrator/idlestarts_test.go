package orchestrator

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// harnessWithHistory is the schedule harness with the latest run of each item
// recorded, ended or not, as runstate.Store.Latest reads it.
type harnessWithHistory struct {
	*scheduleHarness
	latest map[string]runstate.State
}

func (h harnessWithHistory) Latest(workItemID string) (runstate.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if state, ok := h.inFlight[workItemID]; ok {
		return state, nil
	}
	if state, ok := h.latest[workItemID]; ok {
		return state, nil
	}
	return runstate.State{}, runstate.ErrNoRecordedRun
}

// Three developer slots. This session started three items, and none of them
// is working in a slot any more: one run waits on GitHub's checks, one run's
// approved change waits in the merge queue, and one run stopped with its
// change kept on its branch. Each run's process is still finishing — writing
// to a slow tracker — so the session has not collected any of the three. A
// fourth run, another process's, is working. The scheduler counted a slot
// taken for each of the three starts, which with the working run read as
// every slot taken, and started nothing while ready work waited. Only the
// working run holds a slot, so the next pull starts ready work into the other
// two, and into no more than two.
func TestReadyWorkStartsIntoSlotsNoRunIsWorkingIn(t *testing.T) {
	t.Parallel()

	const (
		checks  = "yoyodyne-ifd.429.187"
		queued  = "yoyodyne-ifd.429.236"
		stopped = "yoyodyne-ifd.429.232"
		working = "yoyodyne-ifd.429.87.1"
	)
	fresh := []string{"yoyodyne-ifd.432.25.10", "yoyodyne-jn5n", "yoyodyne-ifd.435.75"}
	items := readyItems(append([]string{checks, queued, stopped}, fresh...)...)
	harness := newScheduleHarness(items...)
	harness.capacity = 3
	harness.poll = 10 * time.Millisecond
	for _, id := range fresh {
		delete(harness.ReadyItems, id)
	}
	runs := harnessWithHistory{scheduleHarness: harness, latest: map[string]runstate.State{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	giveUp := time.AfterFunc(20*time.Second, cancel)
	defer giveUp.Stop()

	// Every run blocks until the session ends, as a run's process does while
	// it waits on the tracker after its work is over.
	harness.hostedRun = func(ctx context.Context, _ *scheduleHarness, id string) (Outcome, error) {
		<-ctx.Done()
		return Outcome{WorkItemID: id}, ctx.Err()
	}

	moved := false
	settledPulls := 0
	harness.onPull = func(h *scheduleHarness, _ int) {
		h.mu.Lock()
		defer h.mu.Unlock()
		started := h.order
		if !moved {
			if !slices.Contains(started, checks) || !slices.Contains(started, queued) || !slices.Contains(started, stopped) {
				return
			}
			moved = true
			now := h.now
			waiting := h.inFlight[checks]
			waiting.StartedAt = now
			waiting.WorktreePath, waiting.Branch, waiting.BaseCommit = "/worktrees/"+checks, "yoyodyne/"+checks, "base"
			waiting.CIWait = &runstate.CIWait{Commit: "c0ffee", Branch: waiting.Branch, PushedAt: now, Required: []string{"test", "race", "vet"}}
			h.inFlight[checks] = waiting

			admitted := h.inFlight[queued]
			delete(h.inFlight, queued)
			admitted.StartedAt = now
			admitted.Status, admitted.Phase = runstate.StatusSucceeded, runstate.PhaseCleaningUp
			admitted.MergeQueue = &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-351", Order: 351, TargetBranch: "main", AdmittedAt: now}
			runs.latest[queued] = admitted

			kept := h.inFlight[stopped]
			delete(h.inFlight, stopped)
			kept.StartedAt = now
			kept.Status, kept.Branch, kept.WorktreePath = runstate.StatusFailed, "yoyodyne/"+stopped, "/worktrees/"+stopped
			runs.latest[stopped] = kept

			h.inFlight[working] = runstate.State{RunID: "run-c940e4ee", WorkItemID: working, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: now}
			for _, id := range fresh {
				h.ReadyItems[id] = true
			}
			return
		}
		if slices.Contains(started, fresh[0]) && slices.Contains(started, fresh[1]) {
			// A few pulls more, so a third start, if the count allowed one, has
			// the chance to happen before the session ends.
			if settledPulls++; settledPulls > 3 {
				cancel()
			}
		}
	}

	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		return pull, err
	}
	_, err := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock}.Schedule(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("schedule: %v", err)
	}

	order := harness.pullOrder()
	if !moved {
		t.Fatalf("pulled %v, want the first three items started before their runs stopped working", order)
	}
	if !slices.Contains(order, fresh[0]) || !slices.Contains(order, fresh[1]) {
		t.Fatalf("pulled %v, want ready work started into the two slots no run is working in", order)
	}
	if slices.Contains(order, fresh[2]) {
		t.Fatalf("pulled %v, want no more than two started beside the one working run", order)
	}
}

// What the session knows says which of its own starts with no run holding a
// slot on record are working in none: one whose run is recorded waiting, and
// one whose own run is on record and over while its process is still going.
// A start still on its way to reserving, whose item's latest run is an earlier
// one, keeps its slot, and so does one whose goroutine has already handed its
// ending back, which the session settles at its next wait before the slot is
// given to anything else.
func TestIdleStartsLeavesOutOnlyStartsThatAreNotWorking(t *testing.T) {
	t.Parallel()

	began := time.Date(2026, 11, 19, 12, 23, 0, 0, time.UTC)
	occupied := map[string]runstate.State{}
	for _, id := range []string{"ended", "waiting", "queued", "continued", "reserving", "unread"} {
		occupied[id] = runstate.State{WorkItemID: id}
	}
	occupied["recorded"] = runstate.State{RunID: "run-recorded", WorkItemID: "recorded", Status: runstate.StatusRunning}
	mine := map[string]int{"ended": 0, "waiting": 1, "queued": 2, "continued": 3, "reserving": 4, "unread": 5, "recorded": 6}
	started := make([]Started, 7)
	for index := range started {
		started[index].began = began
	}
	waiting := map[string]runstate.State{
		"waiting": {RunID: "run-waiting", WorkItemID: "waiting", Status: runstate.StatusRunning, CIWait: &runstate.CIWait{Commit: "c0ffee"}},
		"ended":   {RunID: "run-ended", WorkItemID: "ended", Status: runstate.StatusRunning, CIWait: &runstate.CIWait{Commit: "c0ffee"}},
	}
	history := historyOf{
		"queued":    {RunID: "run-queued", WorkItemID: "queued", Status: runstate.StatusSucceeded, StartedAt: began.Add(time.Second), MergeQueue: &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-354"}},
		"continued": {RunID: "run-continued", WorkItemID: "continued", Status: runstate.StatusFailed, StartedAt: began.Add(-time.Hour)},
		"reserving": {RunID: "run-earlier", WorkItemID: "reserving", Status: runstate.StatusFailed, StartedAt: began.Add(-time.Hour)},
	}
	idle := idleStarts(occupied, waiting, mine, started, map[int]bool{0: true}, map[int]string{3: "run-continued"}, history)

	for _, id := range []string{"waiting", "queued", "continued"} {
		if !idle[id] {
			t.Errorf("idle = %v, want %s left out of the count", idle, id)
		}
	}
	for _, id := range []string{"ended", "reserving", "unread", "recorded"} {
		if idle[id] {
			t.Errorf("idle = %v, want %s still counted", idle, id)
		}
	}
}

type historyOf map[string]runstate.State

func (h historyOf) Latest(workItemID string) (runstate.State, error) {
	if state, ok := h[workItemID]; ok {
		return state, nil
	}
	return runstate.State{}, runstate.ErrNoRecordedRun
}

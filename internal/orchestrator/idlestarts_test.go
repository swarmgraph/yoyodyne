package orchestrator

import (
	"context"
	"errors"
	"slices"
	"strings"
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
// slot on record are working in none: one whose run is recorded waiting,
// whether or not its goroutine has handed its ending back yet, and one whose
// own run is on record and over while its process is still going. A start
// still on its way to reserving, whose item's latest run is an earlier one,
// keeps its slot, and so does one whose goroutine has handed back an ending
// with no wait recorded, or a probe the brake made, which the session settles
// at its next wait before the slot is given to anything else.
func TestIdleStartsLeavesOutOnlyStartsThatAreNotWorking(t *testing.T) {
	t.Parallel()

	began := time.Date(2026, 11, 19, 12, 23, 0, 0, time.UTC)
	occupied := map[string]runstate.State{}
	for _, id := range []string{"ended", "waiting", "queued", "continued", "reserving", "unread", "failed", "probe"} {
		occupied[id] = runstate.State{WorkItemID: id}
	}
	occupied["recorded"] = runstate.State{RunID: "run-recorded", WorkItemID: "recorded", Status: runstate.StatusRunning}
	mine := map[string]int{"ended": 0, "waiting": 1, "queued": 2, "continued": 3, "reserving": 4, "unread": 5, "recorded": 6, "failed": 7, "probe": 8}
	started := make([]Started, 9)
	for index := range started {
		started[index].began = began
	}
	started[8].Probe = true
	waiting := map[string]runstate.State{
		"waiting": {RunID: "run-waiting", WorkItemID: "waiting", Status: runstate.StatusRunning, CIWait: &runstate.CIWait{Commit: "c0ffee"}},
		"ended":   {RunID: "run-ended", WorkItemID: "ended", Status: runstate.StatusRunning, CIWait: &runstate.CIWait{Commit: "c0ffee"}},
		"probe":   {RunID: "run-probe", WorkItemID: "probe", Status: runstate.StatusRunning, CIWait: &runstate.CIWait{Commit: "c0ffee"}},
	}
	history := historyOf{
		"queued":    {RunID: "run-queued", WorkItemID: "queued", Status: runstate.StatusSucceeded, StartedAt: began.Add(time.Second), MergeQueue: &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-354"}},
		"continued": {RunID: "run-continued", WorkItemID: "continued", Status: runstate.StatusFailed, StartedAt: began.Add(-time.Hour)},
		"reserving": {RunID: "run-earlier", WorkItemID: "reserving", Status: runstate.StatusFailed, StartedAt: began.Add(-time.Hour)},
	}
	idle := idleStarts(occupied, waiting, mine, started, map[int]bool{0: true, 7: true, 8: true}, map[int]string{3: "run-continued"}, history)

	for _, id := range []string{"ended", "waiting", "queued", "continued"} {
		if !idle[id] {
			t.Errorf("idle = %v, want %s left out of the count", idle, id)
		}
	}
	for _, id := range []string{"failed", "probe", "reserving", "unread", "recorded"} {
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

// parkedRuns is the schedule harness with runs that parked on GitHub's checks
// kept in flight, as the pipeline records them: the harness drops a run's
// record when its goroutine returns, and a run that paused on the forge stays
// in flight with its wait recorded.
type parkedRuns struct {
	*scheduleHarness
	parked map[string]runstate.State
}

func (p parkedRuns) Incomplete() ([]runstate.State, error) {
	states, err := p.scheduleHarness.Incomplete()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, state := range p.parked {
		states = append(states, state)
	}
	return states, err
}

// Three developer slots, each taken by a run this session started. All three
// push their change and park on GitHub's checks, and each run's goroutine
// hands its paused ending back. The session collects one ending at each wait
// and none after a pull that started work, so at the next pull it has
// collected none of them. Each run is recorded waiting on GitHub's checks,
// which holds no developer slot by the rule the health check reads the slots
// by, so that next pull starts ready work into all three slots rather than
// counting a slot taken for each ending it has yet to collect.
func TestReadyWorkStartsIntoEverySlotRunsWaitingOnGitHubHeld(t *testing.T) {
	t.Parallel()

	parking := []string{"yoyodyne-ifd.429.147", "yoyodyne-ifd.432.25.13", "yoyodyne-ifd.435.52"}
	fresh := []string{"yoyodyne-ifd.428.175", "yoyodyne-ifd.432.25.10", "yoyodyne-ifd.435.75"}
	harness := newScheduleHarness(readyItems(append(append([]string(nil), parking...), fresh...)...)...)
	harness.capacity = 3
	harness.poll = 10 * time.Millisecond
	for _, id := range fresh {
		delete(harness.ReadyItems, id)
	}
	runs := parkedRuns{scheduleHarness: harness, parked: map[string]runstate.State{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	giveUp := time.AfterFunc(20*time.Second, cancel)
	defer giveUp.Stop()

	// Each of the first three runs records its wait on GitHub's checks and
	// returns paused, as a run does once it has pushed; ready work started
	// after them runs until the session ends.
	returned := make(chan string, len(parking))
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		if !slices.Contains(parking, id) {
			<-ctx.Done()
			return Outcome{WorkItemID: id}, ctx.Err()
		}
		h.mu.Lock()
		waiting := h.inFlight[id]
		waiting.WorktreePath, waiting.Branch, waiting.BaseCommit = "/worktrees/"+id, "yoyodyne/"+id, "base"
		waiting.CIWait = &runstate.CIWait{Commit: "c0ffee", Branch: waiting.Branch, PushedAt: h.now, Required: []string{"test", "race", "vet"}}
		runs.parked[id] = waiting
		h.mu.Unlock()
		returned <- id
		return Outcome{WorkItemID: id, RunID: waiting.RunID, Status: runstate.StatusRunning, Paused: true, PausedByCIWait: waiting.CIWait}, nil
	}

	// The first pull starts the three. The session goes straight on to the
	// next pull after a pull that started work, so that next pull waits here
	// until each run has parked and its goroutine has returned, and gives each
	// a moment to hand its ending back, before it reads the slots; the ready
	// work becomes ready then too.
	parkedAt := -1
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 0 {
			return
		}
		if parkedAt < 0 {
			for range parking {
				select {
				case <-returned:
				case <-ctx.Done():
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
			h.mu.Lock()
			for _, id := range fresh {
				h.ReadyItems[id] = true
			}
			h.mu.Unlock()
			parkedAt = pulls
			return
		}
		if pulls > parkedAt+3 {
			cancel()
		}
	}

	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		return pull, err
	}
	sessions := &recordedSessions{}
	_, err := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions}.Schedule(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("schedule: %v", err)
	}
	order := harness.pullOrder()
	if parkedAt != 1 {
		t.Fatalf("pulled %v, want the first pull to start the three items and the next to find them parked on GitHub's checks", order)
	}
	// Each pull that starts work says how many slots it filled of those it
	// found free. The first filled the three slots with the runs that then
	// parked; the next, the pull that found them waiting on GitHub's checks,
	// has to have found all three free and filled them.
	var filled []string
	for _, transition := range sessions.recorded() {
		if strings.HasPrefix(transition.reason, "filled ") {
			filled = append(filled, transition.reason)
		}
	}
	if len(filled) < 2 || !strings.HasPrefix(filled[0], "filled 3 of 3 free developer slots;") {
		t.Fatalf("filling lines = %q, want the first pull to fill all three slots", filled)
	}
	if !strings.HasPrefix(filled[1], "filled 3 of 3 free developer slots") {
		t.Fatalf("filling lines = %q, want the pull that found three runs waiting on GitHub's checks to fill all three slots", filled)
	}
	for _, id := range fresh {
		if !slices.Contains(order, id) {
			t.Fatalf("pulled %v, want ready work started into all three slots", order)
		}
	}
	if len(order) != len(parking)+len(fresh) {
		t.Fatalf("pulled %v, want no more than three started beside the runs waiting on GitHub's checks", order)
	}
}

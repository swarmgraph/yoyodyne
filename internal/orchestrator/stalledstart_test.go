package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A start of the session's own that the tracker keeps waiting, past the time a
// dispatch takes to reserve its run, stops holding a developer slot in the
// count fresh work is started against. The ready work behind it is started in
// the slot the records show free, and when the waiting start does reach its
// reservation the slot is already held, so it is refused there rather than
// run beside the fresh work: one slot is never given to two runs.
func TestFreshWorkStartsInASlotTheRecordsShowFreeWhileAStartWaitsOnASlowTracker(t *testing.T) {
	t.Parallel()

	elsewhere := beads.WorkItem{ID: "yoyodyne-elsewhere", Title: "Another process's run", Status: "in_progress", Priority: 1}
	stuck := beads.WorkItem{ID: "yoyodyne-stuck", Title: "Waits on the tracker", Status: "open", Priority: 0}
	fresh := beads.WorkItem{ID: "yoyodyne-fresh", Title: "Ready behind it", Status: "open", Priority: 2}
	harness := newScheduleHarness(elsewhere, stuck, fresh)
	harness.capacity = 2
	// One slot is held on record by a run another process has in flight.
	harness.inFlight[elsewhere.ID] = runstate.State{RunID: "run-" + elsewhere.ID, WorkItemID: elsewhere.ID, Status: runstate.StatusRunning}

	var (
		release       = make(chan struct{})
		releaseOnce   sync.Once
		stuckWaiting  = make(chan struct{})
		stuckDone     = make(chan struct{})
		freshBeside   bool
		recordedAtTop int
		stuckErr      error
		polls         int
		gaveUp        bool
		finished      bool
		stuckStarts   int
		stuckPending  bool
		startedTwice  bool
	)
	letStuckGo := func() { releaseOnce.Do(func() { close(release) }) }
	// The waiting start stands in for a dispatch whose read of the tracker has
	// not answered: it has no run on record until it reserves. When the tracker
	// answers it reserves under the store's rule, refused where the runs on
	// record already hold every slot (runstate.Store.Reserve).
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		switch id {
		case stuck.ID:
			h.mu.Lock()
			stuckStarts++
			if stuckStarts > 1 {
				// The item tried again once its first start was refused, which
				// the tracker now answers at once.
				startedTwice = startedTwice || stuckPending
				h.mu.Unlock()
				return h.complete(id), nil
			}
			stuckPending = true
			delete(h.inFlight, id)
			h.mu.Unlock()
			close(stuckWaiting)
			<-release
			defer close(stuckDone)
			h.mu.Lock()
			stuckPending = false
			holding := len(h.inFlight)
			if holding >= h.capacity {
				h.mu.Unlock()
				stuckErr = runstate.CapacityError{Limit: h.capacity, Active: holding}
				return Outcome{WorkItemID: id}, stuckErr
			}
			h.inFlight[id] = runstate.State{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusRunning}
			h.mu.Unlock()
			return h.complete(id), nil
		case fresh.ID:
			h.mu.Lock()
			freshBeside = !gaveUp
			recordedAtTop = len(h.inFlight)
			h.mu.Unlock()
			letStuckGo()
			<-stuckDone
		}
		return h.complete(id), nil
	}
	// Every wait moves the session's clock on by the interval it asked for, so
	// the waiting start passes its grace within a few polls. Past ten without
	// the fresh work started, the waiting start is let go so the test ends, and
	// the assertions below say what went wrong.
	tick := func() {
		harness.mu.Lock()
		defer harness.mu.Unlock()
		polls++
		if polls > 10 && !gaveUp {
			gaveUp = true
			letStuckGo()
		}
	}
	interval := func(wait time.Duration) <-chan time.Time {
		<-stuckWaiting
		tick()
		harness.mu.Lock()
		harness.now = harness.now.Add(wait)
		harness.mu.Unlock()
		elapsed := make(chan time.Time, 1)
		elapsed <- time.Time{}
		return elapsed
	}
	harness.onSleep = func(h *scheduleHarness, _ int) bool {
		tick()
		h.mu.Lock()
		defer h.mu.Unlock()
		finished = !stuckPending && len(h.order) >= 2 && h.running == 0 && stuckStarts > 1
		return !finished && polls < 20
	}

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Interval: interval, Now: harness.clock,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if startedTwice {
		t.Fatal("the waiting item was started a second time while its first start still waited, want one item never given two runs")
	}
	if order := harness.pullOrder(); len(order) < 2 || order[0] != stuck.ID || order[1] != fresh.ID {
		t.Fatalf("pulled %v, want the waiting start and then the fresh work beside it", order)
	}
	if !freshBeside {
		t.Fatal("the fresh work was started only once the waiting start was let go, want it started in the slot the records showed free while that start still waited")
	}
	if recordedAtTop > harness.capacity {
		t.Fatalf("%d runs were on record at once over %d slots, want never more than the slots", recordedAtTop, harness.capacity)
	}
	var capacity runstate.CapacityError
	if !errors.As(stuckErr, &capacity) {
		t.Fatalf("the waiting start reached its reservation with %v, want it refused because the slot was held", stuckErr)
	}
	declined := false
	for _, started := range schedule.Started {
		if started.WorkItemID == stuck.ID && strings.Contains(started.Declined, "the last free developer slot went to another run") {
			declined = true
		}
	}
	if !declined {
		t.Fatalf("started = %+v, want the waiting start recorded as declined for the slot the fresh work took", schedule.Started)
	}
}

// What a pull leaves out of the busy slots is the session's own starts with no
// run on record that have waited past the grace a dispatch is given, the
// oldest first and never more than there are slots. A start still inside the
// grace, a run on record, and an entry of another process are all counted.
func TestOnlyTheSessionsOwnStartsWaitingPastTheGraceAreLeftOutOfTheBusySlots(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 17, 21, 29, 0, 0, time.UTC)
	occupied := map[string]runstate.State{
		"yoyodyne-oldest":   {WorkItemID: "yoyodyne-oldest"},
		"yoyodyne-older":    {WorkItemID: "yoyodyne-older"},
		"yoyodyne-old":      {WorkItemID: "yoyodyne-old"},
		"yoyodyne-young":    {WorkItemID: "yoyodyne-young"},
		"yoyodyne-reserved": {WorkItemID: "yoyodyne-reserved", RunID: "run-reserved", Status: runstate.StatusRunning},
		"yoyodyne-other":    {WorkItemID: "yoyodyne-other", RunID: "run-other", Status: runstate.StatusRunning},
	}
	mine := map[string]int{"yoyodyne-oldest": 0, "yoyodyne-older": 1, "yoyodyne-old": 2, "yoyodyne-young": 3, "yoyodyne-reserved": 4}
	started := []Started{
		{WorkItemID: "yoyodyne-oldest", began: now.Add(-time.Hour)},
		{WorkItemID: "yoyodyne-older", began: now.Add(-30 * time.Minute)},
		{WorkItemID: "yoyodyne-old", began: now.Add(-countedGrace)},
		{WorkItemID: "yoyodyne-young", began: now.Add(-countedGrace + time.Second)},
		{WorkItemID: "yoyodyne-reserved", began: now.Add(-time.Hour)},
	}

	stalled := stalledStarts(occupied, mine, started, 5, now)
	if len(stalled) != 3 || !stalled["yoyodyne-oldest"] || !stalled["yoyodyne-older"] || !stalled["yoyodyne-old"] {
		t.Fatalf("stalled = %v, want the three starts past the grace with no run on record", stalled)
	}
	stalled = stalledStarts(occupied, mine, started, 2, now)
	if len(stalled) != 2 || !stalled["yoyodyne-oldest"] || !stalled["yoyodyne-older"] {
		t.Fatalf("stalled = %v over two slots, want the two oldest left out and the third counted again", stalled)
	}
	if stalled := stalledStarts(occupied, mine, started, 0, now); len(stalled) != 0 {
		t.Fatalf("stalled = %v with no slots, want none left out", stalled)
	}
}

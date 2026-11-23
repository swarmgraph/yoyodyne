package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// releasedRuns is the schedule harness with the store's reading of a run whose
// slot the harness freed: it holds no developer slot, as
// runstate.Store.HoldsDeveloperSlotNow reads it.
type releasedRuns struct{ *scheduleHarness }

func (releasedRuns) HoldsDeveloperSlotNow(state runstate.State, _ time.Time) bool {
	return state.HoldsDeveloperSlot() && !state.SlotReleased()
}

// Which of this session's own starts the run records free. A start counts as
// freed only where the run the session saw on record for it is the one now on
// record holding no slot, or where the harness freed its run's slot after the
// start was made. A start still on its way to reserving, one whose item has a
// newer run than the one the session saw, one whose ending the session has yet
// to collect, and one whose run is working all keep their slot.
func TestOwnStartsTheRecordsFreeAreOnlyThoseWhoseOwnRunHoldsNoSlot(t *testing.T) {
	t.Parallel()

	began := time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC)
	longAgo := began.Add(-3 * time.Hour)
	working := runstate.State{RunID: "run-working", WorkItemID: "working", Status: runstate.StatusRunning, StartedAt: longAgo}
	released := runstate.State{RunID: "run-released", WorkItemID: "released", Status: runstate.StatusRunning, StartedAt: longAgo,
		SlotRelease: &runstate.SlotRelease{At: began.Add(20 * time.Minute)}}
	releasedBefore := runstate.State{RunID: "run-released-before", WorkItemID: "released-before", Status: runstate.StatusRunning, StartedAt: longAgo,
		SlotRelease: &runstate.SlotRelease{At: began.Add(-time.Minute)}}
	counted := map[string]runstate.State{
		"working":         working,
		"released":        released,
		"released-before": releasedBefore,
	}
	for _, id := range []string{"queued", "checks", "failed", "reserving", "newer", "collected"} {
		counted[id] = runstate.State{WorkItemID: id}
	}
	ids := []string{"working", "released", "released-before", "queued", "checks", "failed", "reserving", "newer", "collected"}
	mine := map[string]int{}
	started := make([]Started, len(ids))
	for index, id := range ids {
		mine[id] = index
		started[index] = Started{WorkItemID: id, began: began, onRecord: "run-" + id}
	}
	started[mine["reserving"]].onRecord = ""
	history := historyOf{
		"queued": {RunID: "run-queued", WorkItemID: "queued", Status: runstate.StatusSucceeded, StartedAt: longAgo,
			MergeQueue: &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-499", Order: 499, TargetBranch: "main"}},
		"checks":    waitingOnGitHub("checks", "run-checks", began),
		"failed":    {RunID: "run-failed", WorkItemID: "failed", Status: runstate.StatusFailed, StartedAt: longAgo},
		"reserving": {RunID: "run-earlier", WorkItemID: "reserving", Status: runstate.StatusFailed, StartedAt: longAgo},
		"newer":     {RunID: "run-someone-else", WorkItemID: "newer", Status: runstate.StatusFailed, StartedAt: began.Add(time.Hour)},
		"collected": {RunID: "run-collected", WorkItemID: "collected", Status: runstate.StatusFailed, StartedAt: longAgo},
	}
	ended := map[int]bool{mine["collected"]: true}

	freed := ownStartsTheRecordsFree(releasedRuns{}, counted, mine, started, ended, history, began.Add(time.Hour))

	wants := map[string]string{
		"queued":   "merge queue",
		"checks":   "waiting on GitHub's checks",
		"failed":   "it has ended (failed)",
		"released": "freed its developer slot",
	}
	for id, says := range wants {
		why, ok := freed[id]
		if !ok {
			t.Errorf("freed = %v, want %s left out of the count", freed, id)
			continue
		}
		if !strings.Contains(why, says) {
			t.Errorf("why %s is free = %q, want it to say %q", id, why, says)
		}
	}
	for _, id := range []string{"working", "released-before", "reserving", "newer", "collected"} {
		if _, ok := freed[id]; ok {
			t.Errorf("freed = %v, want %s still counted", freed, id)
		}
	}
}

// One developer slot. The session's run took up an item's run that began
// hours before this start, as a decided recovery or a continuation does, and
// the session saw it on record holding the slot. Its approved change then
// enters the merge queue while its process is still finishing, so the session
// has not collected it. The run records say the slot is free; the scheduler
// starts ready work into it at its next pull, without a restart, and says in
// its log which run and which item it had counted wrongly.
func TestASlotIsGivenBackWhenTheSessionsOwnRunEntersTheMergeQueue(t *testing.T) {
	t.Parallel()

	const (
		approved = "yoyodyne-ifd.436.15"
		ready    = "yoyodyne-ifd.429.146.1"
	)
	harness := newScheduleHarness(readyItems(approved, ready)...)
	harness.capacity = 1
	harness.poll = 10 * time.Millisecond
	delete(harness.ReadyItems, ready)
	runs := harnessWithHistory{scheduleHarness: harness, latest: map[string]runstate.State{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	giveUp := time.AfterFunc(20*time.Second, cancel)
	defer giveUp.Stop()

	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		if id == approved {
			h.mu.Lock()
			taken := h.inFlight[id]
			taken.StartedAt = h.now.Add(-3 * time.Hour)
			h.inFlight[id] = taken
			h.mu.Unlock()
		}
		<-ctx.Done()
		return Outcome{WorkItemID: id}, ctx.Err()
	}
	startedAt, queuedAt := -1, -1
	harness.onPull = func(h *scheduleHarness, pulls int) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if !slices.Contains(h.order, approved) {
			return
		}
		if startedAt < 0 {
			startedAt = pulls
			return
		}
		// A pull after the start has seen the run on record, holding the slot.
		if queuedAt < 0 && pulls >= startedAt+2 {
			queuedAt = pulls
			admitted := h.inFlight[approved]
			delete(h.inFlight, approved)
			admitted.Status, admitted.Phase = runstate.StatusSucceeded, runstate.PhaseCleaningUp
			admitted.MergeQueue = &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-499", Order: 499, TargetBranch: "main", AdmittedAt: h.now}
			runs.latest[approved] = admitted
			h.ReadyItems[ready] = true
			return
		}
		if queuedAt >= 0 && (slices.Contains(h.order, ready) || pulls > queuedAt+5) {
			cancel()
		}
	}

	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		return pull, err
	}
	var log bytes.Buffer
	schedule, err := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Log: &log}.Schedule(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("schedule: %v", err)
	}
	order := harness.pullOrder()
	if queuedAt < 0 {
		t.Fatalf("pulled %v, want the approved item started and seen on record before its change entered the merge queue", order)
	}
	if !slices.Contains(order, ready) {
		t.Fatalf("pulled %v, want ready work started into the slot the run in the merge queue no longer holds", order)
	}
	said := log.String()
	for _, want := range []string{"run run-" + approved, approved, "its approved change is in the merge queue", "the run records say the slot is free"} {
		if !strings.Contains(said, want) {
			t.Fatalf("log = %q, want it to say %q", said, want)
		}
	}
	if strings.Count(said, "run run-"+approved) != 1 {
		t.Fatalf("log = %q, want the correction said once", said)
	}
	if len(schedule.SlotCountCorrections) != 1 {
		t.Fatalf("corrections = %q, want the one correction recorded on the schedule", schedule.SlotCountCorrections)
	}
}

// Three developer slots. A run waits on GitHub's checks past its limit, and
// GitHub has answered; a fresh item is ready. The scheduler continues the run
// through the ordinary start, where it takes its slot back, and starts the
// fresh item. More work becomes ready: the run is counted once, not once for
// its waiting record and again for its start, so exactly one more item starts,
// into the third slot. Then the run goes back to waiting on GitHub's checks
// while its process keeps going and the scheduler keeps running; at the next
// pull its slot goes to the work still waiting.
func TestARunContinuedAfterGitHubAnswersIsCountedOnceAndGivesItsSlotBackWhenItWaitsAgain(t *testing.T) {
	t.Parallel()

	const (
		waiting = "yoyodyne-ifd.429.257"
		fresh   = "yoyodyne-ifd.428.185"
	)
	later := []string{"yoyodyne-ifd.432.25.15", "yoyodyne-ifd.430.88"}
	harness := newScheduleHarness(readyItems(append([]string{fresh}, later...)...)...)
	harness.capacity = 3
	harness.poll = 10 * time.Millisecond
	for _, id := range later {
		delete(harness.ReadyItems, id)
	}
	pushed := harness.now.Add(-2 * time.Hour)
	parked := waitingOnGitHub(waiting, "run-ea0808b7", pushed)
	parked.CIWait.WaitLimitSeconds = int64(time.Hour / time.Second)
	harness.inFlight[waiting] = parked

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	giveUp := time.AfterFunc(20*time.Second, cancel)
	defer giveUp.Stop()

	var mu sync.Mutex
	tookSlot, waitsAgain := false, make(chan struct{})
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		if id == waiting {
			// The continuation takes the run's slot back, as
			// runstate.Store.ReclaimSlot does, and works in it.
			h.mu.Lock()
			state := h.inFlight[id]
			state.CIWait = nil
			h.inFlight[id] = state
			h.mu.Unlock()
			mu.Lock()
			tookSlot = true
			mu.Unlock()
			select {
			case <-waitsAgain:
			case <-ctx.Done():
				return Outcome{WorkItemID: id}, ctx.Err()
			}
			// It pushes again and waits on GitHub's checks, its process still
			// going.
			h.mu.Lock()
			state = h.inFlight[id]
			state.CIWait = &runstate.CIWait{Commit: "decaf", Branch: state.Branch, PushedAt: h.now, WaitLimitSeconds: int64(time.Hour / time.Second), Required: []string{"test"}}
			h.inFlight[id] = state
			h.mu.Unlock()
		}
		<-ctx.Done()
		return Outcome{WorkItemID: id}, ctx.Err()
	}

	readyAt, againAt, startedBeforeAgain := -1, -1, -1
	harness.onPull = func(h *scheduleHarness, pulls int) {
		mu.Lock()
		took := tookSlot
		mu.Unlock()
		h.mu.Lock()
		defer h.mu.Unlock()
		laterStarted := 0
		for _, id := range later {
			if slices.Contains(h.order, id) {
				laterStarted++
			}
		}
		switch {
		case readyAt < 0:
			if took && slices.Contains(h.order, fresh) {
				readyAt = pulls
				for _, id := range later {
					h.ReadyItems[id] = true
				}
			}
		case againAt < 0:
			// A few pulls with the run working, so a start the count wrongly
			// allowed has the chance to happen.
			if pulls >= readyAt+4 {
				againAt, startedBeforeAgain = pulls, laterStarted
				close(waitsAgain)
			}
		case laterStarted == len(later) || pulls > againAt+20:
			cancel()
		}
	}

	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.CIWaits = decidedGitHub{}
		pull.Continuations = waitingContinuations{}
		return pull, err
	}
	_, err := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: &recordedSessions{}}.Schedule(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("schedule: %v", err)
	}
	order := harness.pullOrder()
	if readyAt < 0 || againAt < 0 {
		t.Fatalf("pulled %v, want the waiting run continued and the fresh item started before more work became ready", order)
	}
	if count := countOf(order, waiting); count != 1 {
		t.Fatalf("pulled %v, want the waiting run continued once", order)
	}
	if startedBeforeAgain != 1 {
		t.Fatalf("pulled %v; %d of the later items started while the continued run worked, want exactly one, into the third slot", order, startedBeforeAgain)
	}
	for _, id := range later {
		if !slices.Contains(order, id) {
			t.Fatalf("pulled %v, want %s started into the slot the run gave back when it waited on GitHub's checks again", order, id)
		}
	}
}

func countOf(order []string, id string) int {
	count := 0
	for _, entry := range order {
		if entry == id {
			count++
		}
	}
	return count
}

// Which starts that took up a run already on record the run records free when
// no pull saw that run in flight. A start is freed once the run it took up has
// ended after the start was made, whether or not the session has collected it
// where the run succeeded. A run whose record ended before the start — a
// stopped run a decision is continuing, not yet live again — keeps its slot,
// and so does a run with no completion time, a handed-back start whose run
// failed, and a start whose item's latest run is another run.
func TestStartsThatTookUpARunAreFreedOnceThatRunEndsAfterTheStart(t *testing.T) {
	t.Parallel()

	began := time.Date(2026, 11, 22, 12, 29, 0, 0, time.UTC)
	before, after := began.Add(-2*time.Hour), began.Add(time.Minute)
	ended := func(id string, status runstate.Status, at *time.Time) runstate.State {
		return runstate.State{RunID: "run-" + id, WorkItemID: id, Status: status, StartedAt: before.Add(-time.Hour), CompletedAt: at}
	}
	queued := func(id string, at *time.Time) runstate.State {
		state := ended(id, runstate.StatusSucceeded, at)
		state.MergeQueue = &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-582", Order: 582, TargetBranch: "main"}
		return state
	}
	history := historyOf{
		"queued":           queued("queued", &after),
		"stopped":          ended("stopped", runstate.StatusFailed, &after),
		"stopped-before":   ended("stopped-before", runstate.StatusFailed, &before),
		"no-completion":    queued("no-completion", nil),
		"handed-landed":    queued("handed-landed", &after),
		"handed-failed":    ended("handed-failed", runstate.StatusFailed, &after),
		"another-run":      {RunID: "run-later", WorkItemID: "another-run", Status: runstate.StatusSucceeded, CompletedAt: &after},
		"fresh-start-only": queued("fresh-start-only", &after),
	}
	ids := []string{"queued", "stopped", "stopped-before", "no-completion", "handed-landed", "handed-failed", "another-run", "fresh-start-only"}
	counted := map[string]runstate.State{}
	mine := map[string]int{}
	started := make([]Started, len(ids))
	for index, id := range ids {
		counted[id] = runstate.State{WorkItemID: id}
		mine[id] = index
		started[index] = Started{WorkItemID: id, began: began, tookUp: "run-" + id}
	}
	started[mine["fresh-start-only"]].tookUp = ""
	handedBack := map[int]bool{mine["handed-landed"]: true, mine["handed-failed"]: true}

	freed := ownStartsTheRecordsFree(releasedRuns{}, counted, mine, started, handedBack, history, after)

	wants := map[string]string{
		"queued":        "merge queue",
		"stopped":       "it has ended (failed)",
		"handed-landed": "merge queue",
	}
	for id, says := range wants {
		why, ok := freed[id]
		if !ok {
			t.Errorf("freed = %v, want %s left out of the count", freed, id)
			continue
		}
		if !strings.Contains(why, says) {
			t.Errorf("why %s is free = %q, want it to say %q", id, why, says)
		}
	}
	for _, id := range []string{"stopped-before", "no-completion", "handed-failed", "another-run", "fresh-start-only"} {
		if _, ok := freed[id]; ok {
			t.Errorf("freed = %v, want %s still counted", freed, id)
		}
	}
}

// Two developer slots. One holds a run that keeps working. A run waits on
// GitHub's checks and GitHub has answered, so the scheduler takes it back up
// into the other slot. The run enters the merge queue within moments, before
// any pull sees it in flight again, and its process goes on writing to a slow
// tracker, so the session has not collected it. Ready work arrives: the run
// records say the second slot is free, and the next pull starts the ready work
// into it, saying which run it had counted the slot for.
func TestASlotIsPulledIntoOnceARunTakenBackUpAfterGitHubsChecksEntersTheMergeQueue(t *testing.T) {
	t.Parallel()

	const (
		busy    = "yoyodyne-ifd.435.85"
		waiting = "yoyodyne-fd6p"
		ready   = "yoyodyne-ifd.432.52"
	)
	harness := newScheduleHarness(readyItems(busy, ready)...)
	harness.capacity = 2
	harness.poll = 10 * time.Millisecond
	delete(harness.ReadyItems, ready)
	pushed := harness.now.Add(-time.Hour)
	harness.inFlight[waiting] = waitingOnGitHub(waiting, "run-23ad8109", pushed)
	runs := harnessWithHistory{scheduleHarness: harness, latest: map[string]runstate.State{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	giveUp := time.AfterFunc(20*time.Second, cancel)
	defer giveUp.Stop()

	var mu sync.Mutex
	queued, admittedNow := false, make(chan struct{})
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		if id == waiting {
			// It takes its slot back, passes review, and its approved change
			// enters the merge queue, all before the next pull reads the runs.
			h.mu.Lock()
			admitted := h.inFlight[id]
			delete(h.inFlight, id)
			completed := h.now
			admitted.CIWait = nil
			admitted.Status, admitted.Phase, admitted.CompletedAt = runstate.StatusSucceeded, runstate.PhaseCleaningUp, &completed
			admitted.MergeQueue = &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-582", Order: 582, TargetBranch: "main", AdmittedAt: completed}
			runs.latest[id] = admitted
			h.mu.Unlock()
			mu.Lock()
			queued = true
			mu.Unlock()
			close(admittedNow)
		}
		<-ctx.Done()
		return Outcome{WorkItemID: id}, ctx.Err()
	}
	readyAt := -1
	harness.onPull = func(h *scheduleHarness, pulls int) {
		// No pull after the run is taken back up reads the runs before its change
		// is in the merge queue, so no pull ever sees it in flight again.
		h.mu.Lock()
		takenUp := slices.Contains(h.order, waiting)
		h.mu.Unlock()
		if takenUp {
			select {
			case <-admittedNow:
			case <-ctx.Done():
			}
		}
		mu.Lock()
		admitted := queued
		mu.Unlock()
		h.mu.Lock()
		defer h.mu.Unlock()
		switch {
		case readyAt < 0:
			if admitted && slices.Contains(h.order, busy) {
				readyAt = pulls
				h.ReadyItems[ready] = true
			}
		case slices.Contains(h.order, ready) || pulls > readyAt+10:
			cancel()
		}
	}

	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		pull.CIWaits = decidedGitHub{}
		pull.Continuations = waitingContinuations{}
		return pull, err
	}
	var log bytes.Buffer
	_, err := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Log: &log, Sessions: &recordedSessions{}}.Schedule(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("schedule: %v", err)
	}
	order := harness.pullOrder()
	if readyAt < 0 {
		t.Fatalf("pulled %v, want the waiting run taken back up beside the busy one and its change in the merge queue", order)
	}
	if count := countOf(order, waiting); count != 1 {
		t.Fatalf("pulled %v, want the waiting run taken back up once", order)
	}
	if !slices.Contains(order, ready) {
		t.Fatalf("pulled %v, want ready work started into the slot the run in the merge queue no longer holds", order)
	}
	if said := log.String(); !strings.Contains(said, "run run-23ad8109 of "+waiting) || !strings.Contains(said, "its approved change is in the merge queue") {
		t.Fatalf("log = %q, want it to say which run it had counted the slot for and why the slot is free", said)
	}
}

// staleListing is the schedule harness with the list of runs in flight read
// before a run changed: listed is how that list shows each run, and the run's
// own record, read again, is in records.
type staleListing struct {
	*scheduleHarness
	listed  []runstate.State
	records map[string]runstate.State
}

func (s staleListing) Incomplete() ([]runstate.State, error) {
	states, err := s.scheduleHarness.Incomplete()
	return append(states, s.listed...), err
}

func (s staleListing) Latest(workItemID string) (runstate.State, error) {
	if state, ok := s.records[workItemID]; ok {
		return state, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if state, ok := s.inFlight[workItemID]; ok {
		return state, nil
	}
	return runstate.State{}, runstate.ErrNoRecordedRun
}

// One developer slot. Another process's run is listed as working in it, but
// its own record, read again, says it has gone back to waiting on GitHub's
// checks and holds no slot. The scheduler goes by the record: it starts the
// ready work into the slot, and says which run it had counted the slot for.
func TestARunWhoseRecordHoldsNoSlotIsNotCountedAgainstOne(t *testing.T) {
	t.Parallel()

	const (
		elsewhere = "yoyodyne-75sv"
		ready     = "yoyodyne-ifd.432.52"
	)
	harness := newScheduleHarness(readyItems(ready)...)
	harness.capacity = 1
	harness.poll = 10 * time.Millisecond
	working := runstate.State{RunID: "run-41da5a9c", WorkItemID: elsewhere, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: harness.now.Add(-time.Hour)}
	runs := staleListing{scheduleHarness: harness, listed: []runstate.State{working},
		records: map[string]runstate.State{elsewhere: waitingOnGitHub(elsewhere, working.RunID, harness.now.Add(-time.Minute))}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	giveUp := time.AfterFunc(20*time.Second, cancel)
	defer giveUp.Stop()
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		<-ctx.Done()
		return Outcome{WorkItemID: id}, ctx.Err()
	}
	harness.onPull = func(h *scheduleHarness, pulls int) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if slices.Contains(h.order, ready) || pulls > 10 {
			cancel()
		}
	}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		return pull, err
	}
	var log bytes.Buffer
	schedule, err := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Log: &log}.Schedule(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("schedule: %v", err)
	}
	if order := harness.pullOrder(); !slices.Contains(order, ready) {
		t.Fatalf("pulled %v, want the ready work started into the slot the waiting run's record says it does not hold", order)
	}
	said := log.String()
	for _, want := range []string{"run run-41da5a9c of " + elsewhere, "it is waiting on GitHub's checks", "the run records say the slot is free"} {
		if !strings.Contains(said, want) {
			t.Fatalf("log = %q, want it to say %q", said, want)
		}
	}
	if strings.Count(said, "run run-41da5a9c of") != 1 || len(schedule.SlotCountCorrections) != 1 {
		t.Fatalf("log = %q, corrections = %q, want the correction said once", said, schedule.SlotCountCorrections)
	}
}

// Two developer slots, both free on record: two runs wait on GitHub's checks,
// GitHub has answered on both, and ready work waits behind them. The pass that
// finds them gives both slots to the two runs, written work first, and says
// every slot is counted taken by them, naming each run, as the health reading
// of 18:49 PST on November 22 did. Taking a run back up is slow here: neither
// run takes its slot back on its record. While that is within the two minutes
// a start is given, the slots stay theirs and no ready work starts. Past them,
// the scheduler goes by the records, which say neither run holds a slot: the
// ready work starts into both slots, each run is taken back up once, and the
// log names each run whose slot it gave back.
func TestReadyWorkStartsWhenRunsTakenBackUpAfterGitHubsChecksHaveNotTakenTheirSlots(t *testing.T) {
	t.Parallel()

	const (
		first  = "yoyodyne-75sv"
		second = "yoyodyne-o4gg"
	)
	ready := []string{"yoyodyne-ifd.432.52", "yoyodyne-ifd.435.85"}
	harness := newScheduleHarness(readyItems(ready...)...)
	harness.capacity = 2
	harness.poll = 10 * time.Millisecond
	pushed := harness.now.Add(-time.Hour)
	harness.inFlight[first] = waitingOnGitHub(first, "run-41da5a9c", pushed)
	harness.inFlight[second] = waitingOnGitHub(second, "run-ad5523ed", pushed)
	runs := harnessWithHistory{scheduleHarness: harness, latest: map[string]runstate.State{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	giveUp := time.AfterFunc(20*time.Second, cancel)
	defer giveUp.Stop()
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		<-ctx.Done()
		return Outcome{WorkItemID: id}, ctx.Err()
	}
	takenUpAt := time.Time{}
	startedWithinGrace := false
	harness.onPull = func(h *scheduleHarness, pulls int) {
		h.mu.Lock()
		defer h.mu.Unlock()
		started := 0
		for _, id := range ready {
			if slices.Contains(h.order, id) {
				started++
			}
		}
		if takenUpAt.IsZero() && slices.Contains(h.order, first) && slices.Contains(h.order, second) {
			takenUpAt = h.now
		}
		if !takenUpAt.IsZero() && started > 0 && h.now.Sub(takenUpAt) < countedGrace {
			startedWithinGrace = true
		}
		if started == len(ready) || pulls > 30 {
			cancel()
			return
		}
		// Thirty seconds pass between pulls once the runs are being taken up.
		if !takenUpAt.IsZero() {
			h.now = h.now.Add(30 * time.Second)
		}
	}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		pull.CIWaits = decidedGitHub{}
		pull.Continuations = waitingContinuations{}
		return pull, err
	}
	sessions := &recordedSessions{}
	var log bytes.Buffer
	_, err := Scheduler{Open: open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, Log: &log}.Schedule(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("schedule: %v", err)
	}
	order := harness.pullOrder()
	for _, id := range []string{first, second} {
		if count := countOf(order, id); count != 1 {
			t.Fatalf("pulled %v, want %s taken back up once", order, id)
		}
	}
	if startedWithinGrace {
		t.Fatalf("pulled %v, want no ready work started into the slots while the runs are within two minutes of being taken up", order)
	}
	for _, id := range ready {
		if !slices.Contains(order, id) {
			t.Fatalf("pulled %v, want %s started into a slot neither waiting run has taken back on its record past the two minutes", order, id)
		}
	}
	said := log.String()
	for _, run := range []string{"run run-41da5a9c of " + first, "run run-ad5523ed of " + second} {
		if !strings.Contains(said, run) || !strings.Contains(said, "has not taken its developer slot back") {
			t.Fatalf("log = %q, want it to say it gave back the slot counted for %s", said, run)
		}
	}
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	named := false
	for _, transition := range sessions.transitions {
		if transition.emptySlots != nil && strings.Contains(transition.emptySlots.Says(), "by "+first+" (run run-41da5a9c), "+second+" (run run-ad5523ed)") {
			named = true
		}
	}
	if !named {
		t.Fatalf("no pass said every slot was counted taken by the two runs being taken back up, naming each run")
	}
}

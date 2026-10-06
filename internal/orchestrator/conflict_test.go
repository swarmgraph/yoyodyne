package orchestrator

// What the scheduler will not start beside what. The first two tests are the
// line yoyodyne-ifd.261 drew between a container epic and a decomposed one: the
// children of a heading run at once, and a child of an epic a run is already
// over waits for it. The rest are the halves that make it a scheduling rule
// rather than a brake — work that shares nothing still runs concurrently, and an
// item held back is held back for exactly as long as the run it would have raced
// lasts.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The over-sequencing yoyodyne-ifd.261 was admitted for, replayed: three items
// filed under one heading epic, and a machine with room for two. Under the rule
// this replaced, sharing a parent was itself a race, so the second and third
// waited on the first however unrelated their work was — and this backlog files
// everything under headings, which on the reading of 2026-09-22 put 29 of its
// 109 unfinished items behind one of them.
//
// The heading is in the queue as an item of its own, as a real one is. It is
// passed over, but as a container whose children carry its execution rather than
// as something racing them, and that distinction is the whole of what this
// asserts about it: the coverage check is what leaves a heading behind, and this
// guard has nothing to say about one.
func TestSchedulerRunsChildrenOfOneContainerEpicConcurrently(t *testing.T) {
	t.Parallel()

	items := append(
		[]beads.WorkItem{{ID: "yoyodyne-epic", Title: "Scheduler and pipeline", Status: "open", Priority: 2}},
		siblings("yoyodyne-epic", "yoyodyne-epic.1", "yoyodyne-epic.2", "yoyodyne-epic.3")...,
	)
	harness := newScheduleHarness(items...)
	harness.capacity = 2
	// Two of the three inside at once is the criterion, and it is asserted
	// against what actually overlapped rather than inferred from the order they
	// were started in.
	harness.developersMeet(2)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	pulled := harness.pullOrder()
	for _, id := range []string{"yoyodyne-epic.1", "yoyodyne-epic.2", "yoyodyne-epic.3"} {
		if !slices.Contains(pulled, id) {
			t.Fatalf("pulled = %v, want every child of the heading run: %s", pulled, schedule.Render())
		}
	}
	if harness.peak < 2 {
		t.Fatalf("peak concurrent runs = %d, want children of one heading run beside each other: %s",
			harness.peak, schedule.Render())
	}
	// The heading is the only thing left behind, and for the reason that is
	// actually true of it. It is ordinary work again once its last child closes,
	// which is why it is pulled too by the end of the drain.
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != "yoyodyne-epic" {
		t.Fatalf("deferred = %#v, want only the heading left behind: %s", schedule.Deferred, schedule.Render())
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "covered by") {
		t.Fatalf("deferred reason = %q, want the heading passed over as covered rather than as racing its own children",
			schedule.Deferred[0].Reason)
	}
	// And nothing that ran records having waited, because nothing did.
	for _, id := range []string{"yoyodyne-epic.1", "yoyodyne-epic.2", "yoyodyne-epic.3"} {
		if reason := harness.selectionFor(id).Reason; strings.Contains(reason, "held back") {
			t.Fatalf("reason for %s = %q, want a child of a heading to have waited for nothing", id, reason)
		}
	}
}

// The half yoyodyne-ifd.256 left open, and the shape it was left open on: an
// epic with a run already in flight over it, and the child the decomposition
// creates underneath it while that run works. The child carries part of the
// epic's own scope, so starting it beside the run is one change made twice —
// which is what yoyodyne-ifd.121 and yoyodyne-ifd.121.2 were.
//
// The parentage is stated only as a parent-child edge, with no parent field, as
// this project's own tracker export states it. A guard that read the field alone
// saw such a store as a backlog nothing was ever broken out of.
func TestSchedulerSequencesAChildBehindTheRunOverTheEpicItCameOutOf(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{
		ID: "yoyodyne-ifd.121", Title: "Docs architecture", Status: "in_progress", Priority: 1,
	}
	child := beads.WorkItem{
		ID: "yoyodyne-ifd.121.2", Title: "Execute the README split", Status: "open", Priority: 1,
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121.2", ID: epic.ID, Type: "parent-child"},
		},
	}
	if child.Parent != "" {
		t.Fatalf("the child's parent field = %q, want the edge to be the only statement of parentage", child.Parent)
	}
	harness := newScheduleHarness(epic, child)
	harness.capacity = 2
	harness.inFlight[epic.ID] = runstate.State{
		RunID: "run-" + epic.ID, WorkItemID: epic.ID, Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing started beside the run over the epic it was broken out of: %s",
			schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != child.ID {
		t.Fatalf("deferred = %#v, want the child named as sequenced behind the epic: %s",
			schedule.Deferred, schedule.Render())
	}
	// The run and the epic both, because an item cannot be checked against
	// `yoyo status`, which lists runs.
	if !strings.Contains(schedule.Deferred[0].Reason, "run-"+epic.ID) ||
		!strings.Contains(schedule.Deferred[0].Reason, epic.ID+" this item was broken out of") {
		t.Fatalf("deferred reason = %q, want the run in flight and the epic it is over both named",
			schedule.Deferred[0].Reason)
	}
}

// The other half of the rule, and the one it must not cost: items that share
// nothing still run at once. The developers are held in a rendezvous until both
// are inside, so "concurrently" is an observation rather than an inference.
func TestSchedulerStillRunsUnrelatedItemsConcurrently(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 2
	harness.developersMeet(2)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Deferred) != 0 {
		t.Fatalf("deferred = %#v, want nothing held back where nothing is shared", schedule.Deferred)
	}
	for _, started := range schedule.Started {
		if strings.Contains(started.Reason, "held back") {
			t.Fatalf("reason for %s = %q, want nothing said about sequencing where none happened",
				started.WorkItemID, started.Reason)
		}
	}
}

// Two items of no shared parentage that will plainly change the same file, and a
// third that will not. The two are sequenced and the third runs beside the first,
// so the slot the sequencing freed is spent rather than idled — and the run that
// took it records having been pulled ahead of the one that was held.
func TestSchedulerSequencesItemsOverASharedSurfaceAndPullsPastThem(t *testing.T) {
	t.Parallel()

	first := beads.WorkItem{
		ID: "yoyodyne-first", Title: "Widen the schedule reason", Status: "open", Priority: 1,
		Description: "conflict-surface: internal/orchestrator/schedule.go",
	}
	second := beads.WorkItem{
		ID: "yoyodyne-second", Title: "Bound the schedule reason", Status: "open", Priority: 1,
		Description: "conflict-surface: internal/orchestrator/schedule.go",
	}
	unrelated := beads.WorkItem{
		ID: "yoyodyne-unrelated", Title: "Something else entirely", Status: "open", Priority: 2,
		Description: "conflict-surface: internal/notify/voice.go",
	}
	harness := newScheduleHarness(first, second, unrelated)
	harness.capacity = 2
	// The first item and the unrelated one must be inside at once: that is the
	// slot the held-back item would otherwise have taken being spent on work that
	// races nothing.
	harness.developersMeet(2)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != second.ID {
		t.Fatalf("deferred = %#v, want the item over the same file held back: %s", schedule.Deferred, schedule.Render())
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "internal/orchestrator/schedule.go") {
		t.Fatalf("deferred reason = %q, want the surface the two share named", schedule.Deferred[0].Reason)
	}
	// The order was departed from, so the run that benefited says so.
	reason := harness.selectionFor(unrelated.ID).Reason
	if !strings.Contains(reason, second.ID) || !strings.Contains(reason, "held back at this pull") {
		t.Fatalf("reason for %s = %q, want the item it was pulled ahead of recorded", unrelated.ID, reason)
	}
	if pulled := harness.pullOrder(); len(pulled) != 3 {
		t.Fatalf("pulled = %v, want every item run once the conflict cleared: %s", pulled, schedule.Render())
	}
}

// An item that declares nothing is read for the files it plainly names, and only
// for those: a shared file sequences, and different files in one package do not.
// The narrowness is the point — a surface invented out of prose holds unrelated
// work back, which is the concurrency this is supposed to be protecting.
func TestSchedulerSequencesOnSurfacesInferredFromWhatAnItemSays(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		other    string
		deferred int
	}{
		{
			name:     "the same file named by both",
			other:    "Bound what internal/orchestrator/schedule.go records against the run state's own limit.",
			deferred: 1,
		},
		{
			name:     "different files in one package",
			other:    "Teach internal/orchestrator/publish.go to say which remote refused it.",
			deferred: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			first := beads.WorkItem{
				ID: "yoyodyne-first", Title: "Widen the recorded reason", Status: "open", Priority: 1,
				Description: "Put the ordering rationale into internal/orchestrator/schedule.go.",
			}
			second := beads.WorkItem{
				ID: "yoyodyne-second", Title: "Bound the recorded reason", Status: "open", Priority: 1,
				Description: test.other,
			}
			harness := newScheduleHarness(first, second)
			harness.capacity = 2

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Deferred) != test.deferred {
				t.Fatalf("deferred = %#v, want %d held back: %s", schedule.Deferred, test.deferred, schedule.Render())
			}
			if pulled := harness.pullOrder(); len(pulled) != 2 {
				t.Fatalf("pulled = %v, want both items run either way: %s", pulled, schedule.Render())
			}
		})
	}
}

// The case a reading of the queue alone would miss: the run it would race
// belongs to another process, over an item that has already left the backlog by
// being claimed. Nothing in the queue names it, and the item it would race is
// exactly the one whose integration is nearest.
func TestSchedulerSequencesBehindWorkAnotherProcessAlreadyHasInFlight(t *testing.T) {
	t.Parallel()

	claimed := beads.WorkItem{ID: "yoyodyne-epic", Title: "The whole of it", Status: "in_progress", Priority: 1}
	child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Second half", Status: "open", Priority: 1, Parent: "yoyodyne-epic"}
	harness := newScheduleHarness(claimed, child)
	harness.capacity = 2
	harness.inFlight[claimed.ID] = runstate.State{
		RunID: "run-" + claimed.ID, WorkItemID: claimed.ID, Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing started beside another process's run over the epic", schedule.Started)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != child.ID {
		t.Fatalf("deferred = %#v, want the child named as sequenced behind it: %s", schedule.Deferred, schedule.Render())
	}
	if !strings.Contains(schedule.Deferred[0].Reason, claimed.ID) {
		t.Fatalf("deferred reason = %q, want the item in flight named", schedule.Deferred[0].Reason)
	}
	// The run itself, and not only its item: an item cannot be checked against
	// `yoyo status`, which lists runs, and a reason that named one alone was read
	// on 2026-09-18 as the guard holding a slot on a run that had already failed.
	if !strings.Contains(schedule.Deferred[0].Reason, "run-"+claimed.ID) {
		t.Fatalf("deferred reason = %q, want the run in flight named", schedule.Deferred[0].Reason)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want a drain that found nothing startable to end", schedule.Stopped)
	}
}

// What the guard counts as in flight is runstate.Status.InFlight — pending or
// running — and nothing the listing it is handed says otherwise. The store's own
// listing already answers in those terms, so with the real store this rule is
// never the one that decides; what this proves is that the guard's reading is
// the status's own rather than the listing's, by handing it the runs the store
// never would: an epic whose last run ended in each terminal status, beside a
// ready child of it. None of them holds a developer slot or the epic, and the
// child is pulled with nothing said about a wait.
//
// The failed case is shaped like yoyodyne-ifd.272's run of 2026-09-16 — stopped
// at integration on a replay conflict, its pull request still open, a decision
// about it owed — because that is the run a stale schedule report named two days
// later as still in flight. That report, not the guard, was what misread the run
// (see passOver); this is the rule the report was mistaken for having broken.
func TestSchedulerDoesNotHoldAChildOnAnEpicWhoseRunHasEnded(t *testing.T) {
	t.Parallel()

	for _, status := range []runstate.Status{
		runstate.StatusFailed, runstate.StatusSucceeded, runstate.StatusCancelled, runstate.StatusTimedOut,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			ended := beads.WorkItem{
				ID: "yoyodyne-epic", Title: "A claimed-but-dead item is audited", Status: "in_progress", Priority: 1,
			}
			ready := beads.WorkItem{
				ID: "yoyodyne-epic.379", Title: "The guard reads run state", Status: "open", Priority: 0,
				Parent: "yoyodyne-epic",
			}
			harness := newScheduleHarness(ended, ready)
			harness.capacity = 2
			run := runstate.State{RunID: "run-2f6e6e0a", WorkItemID: ended.ID, Status: status, Phase: runstate.PhaseComplete}
			if status == runstate.StatusFailed {
				run.Phase = runstate.PhaseIntegrating
				run.Failure = "change cannot be replayed onto the moved integration target"
				run.PullRequest = &runstate.PullRequest{
					Remote: "origin", Branch: "yoyodyne/yoyodyne-epic-272/2f6e6e0a", Number: 511, State: "OPEN",
				}
			}
			harness.inFlight[ended.ID] = run

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ready.ID {
				t.Fatalf("started = %#v, want the ready child pulled beside a %s run over its epic: %s",
					schedule.Started, status, schedule.Render())
			}
			if len(schedule.Deferred) != 0 {
				t.Fatalf("deferred = %#v, want nothing held behind a run that has ended", schedule.Deferred)
			}
			if schedule.Occupied != 0 {
				t.Fatalf("occupied = %d, want a %s run to hold no developer slot", schedule.Occupied, status)
			}
			if reason := harness.selectionFor(ready.ID).Reason; strings.Contains(reason, "held back") {
				t.Fatalf("reason = %q, want the child to have waited for nothing", reason)
			}
		})
	}
}

// The other half of the same predicate: a run that is pending or running holds
// its epic whatever phase it is in. A run integrating is the case worth
// stating, because "failed/integrating" is how the 2026-09-16 run was described
// and the phase is not what decided anything — a running run at that phase is a
// promotion in progress, listed by `yoyo status`, and exactly the run a child of
// the epic would race.
func TestSchedulerHoldsAChildOnAnEpicWhoseRunIsInFlightWhateverItsPhase(t *testing.T) {
	t.Parallel()

	for _, run := range []runstate.State{
		{RunID: "run-pending", Status: runstate.StatusPending},
		{RunID: "run-integrating", Status: runstate.StatusRunning, Phase: runstate.PhaseIntegrating},
	} {
		t.Run(run.RunID, func(t *testing.T) {
			t.Parallel()

			claimed := beads.WorkItem{ID: "yoyodyne-epic", Title: "The whole of it", Status: "in_progress", Priority: 1}
			child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Second half", Status: "open", Priority: 1, Parent: "yoyodyne-epic"}
			harness := newScheduleHarness(claimed, child)
			harness.capacity = 2
			run.WorkItemID = claimed.ID
			harness.inFlight[claimed.ID] = run

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Started) != 0 {
				t.Fatalf("started = %#v, want nothing started beside a %s run over the epic", schedule.Started, run.Status)
			}
			if len(schedule.Deferred) != 1 || !strings.Contains(schedule.Deferred[0].Reason, run.RunID) {
				t.Fatalf("deferred = %#v, want the child held behind %s: %s", schedule.Deferred, run.RunID, schedule.Render())
			}
			if schedule.Occupied != 1 {
				t.Fatalf("occupied = %d, want the run in flight to hold a developer slot", schedule.Occupied)
			}
		})
	}
}

// What the schedule says about a held item is what the last pull found, not the
// first. A session that holds a child behind one run over its epic, and then
// behind the run that follows it, reports the second: the schedule is rendered
// when the session ends, and on 2026-09-18 one rendered with each held item's
// first reason named a run two days dead as what every one of them was still
// waiting on.
func TestWatchingReportsTheLastRunAHeldItemWaitedBehind(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(siblings("yoyodyne-epic", "yoyodyne-epic.1")...)
	harness.capacity = 2
	harness.admit(beads.WorkItem{ID: "yoyodyne-epic", Title: "The whole of it", Status: "in_progress", Priority: 1})
	harness.inFlight["yoyodyne-epic"] = runstate.State{
		RunID: "run-first", WorkItemID: "yoyodyne-epic", Status: runstate.StatusRunning,
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 2 {
			// The first run over the epic ends and the epic is run again — a
			// triage re-run, say — before this session's next pull.
			h.mu.Lock()
			h.inFlight["yoyodyne-epic"] = runstate.State{
				RunID: "run-second", WorkItemID: "yoyodyne-epic", Status: runstate.StatusRunning,
			}
			h.mu.Unlock()
		}
		return sleeps < 4
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want the child held for the whole session", schedule.Started)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != "yoyodyne-epic.1" {
		t.Fatalf("deferred = %#v, want the held child named once", schedule.Deferred)
	}
	reason := schedule.Deferred[0].Reason
	if !strings.Contains(reason, "run-second") || strings.Contains(reason, "run-first") {
		t.Fatalf("deferred reason = %q, want the run the last pull held it behind rather than the first", reason)
	}
}

// A held-back item is held back for as long as the run it would race lasts and
// no longer. What is remembered across a watching session is the fact of the
// wait, so the run that finally takes the item can account for it; nothing about
// the hold survives the run that caused it.
func TestWatchingPullsASequencedItemOnceTheRunItWouldRaceEnds(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(siblings("yoyodyne-epic", "yoyodyne-epic.1")...)
	harness.capacity = 2
	harness.inFlight["yoyodyne-epic"] = runstate.State{
		RunID: "run-outside", WorkItemID: "yoyodyne-epic", Status: runstate.StatusRunning,
	}
	// The other process's item is in the tracker as claimed work, which is what
	// says what it is going to change.
	harness.admit(beads.WorkItem{ID: "yoyodyne-epic", Title: "The whole of it", Status: "in_progress", Priority: 1})
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 2 {
			h.mu.Lock()
			delete(h.inFlight, "yoyodyne-epic")
			h.Items[1].Status = "closed"
			h.mu.Unlock()
		}
		return sleeps < 4
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-epic.1" {
		t.Fatalf("started = %#v, want the child pulled once the run it would race ended: %s",
			schedule.Started, schedule.Render())
	}
	// The deferral is a report rather than a decision, and an item held across
	// two polls is one line in it.
	if len(schedule.Deferred) != 1 {
		t.Fatalf("deferred = %#v, want the hold said once rather than once per poll", schedule.Deferred)
	}
	reason := harness.selectionFor("yoyodyne-epic.1").Reason
	if !strings.Contains(reason, "held back earlier in this session") || !strings.Contains(reason, "run-outside") {
		t.Fatalf("reason = %q, want the wait it came out of recorded with what caused it", reason)
	}
}

// siblings builds the ordinary filing: several open items at one priority, all
// hanging off one epic. They race nothing by sharing it — see the header — so
// this is the shape a test uses when it wants a parent stated and no hold from
// it.
func siblings(parent string, ids ...string) []beads.WorkItem {
	items := make([]beads.WorkItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, beads.WorkItem{ID: id, Title: id, Status: "open", Priority: 2, Parent: parent})
	}
	return items
}

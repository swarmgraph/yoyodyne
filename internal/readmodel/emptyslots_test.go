package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func emptySlotNote(session string, at time.Time, account runstate.EmptySlots) runstate.WatchTransition {
	account.At = at
	if account.Since.IsZero() && !account.Cleared {
		account.Since = at
	}
	return runstate.WatchTransition{SessionID: session, State: runstate.WatchWatching, At: at, EmptySlots: &account}
}

// The latest account stands until it is cleared or its session stops, and an
// account the reading contradicts — no slot free now, or no startable work
// where the scheduler could not read the queue — is not said.
func TestAnEmptySlotAccountStandsOnlyWhileItDescribesTheMachine(t *testing.T) {
	t.Parallel()

	rule := runstate.EmptySlots{Cause: ownership.EmptySlotRule, Reason: "a rule keeps yoyodyne-a out", Slots: []int{3}, Ready: 1,
		Passed: []runstate.EmptySlotItem{{WorkItemID: "yoyodyne-a", Why: "sequenced behind work in flight: it overlaps run-1"}}}
	sessions := []runstate.WatchTransition{emptySlotNote("watch-1", moment.Add(-time.Hour), rule)}
	read := func(transitions []runstate.WatchTransition, reading emptySlotsReading) *EmptySlots {
		empty, problem := readEmptySlots(Sources{Sessions: fakeSessions{transitions: transitions}}, reading, nil, moment)
		if problem != "" {
			t.Fatalf("readEmptySlots() problem = %q", problem)
		}
		return empty
	}
	empty := read(sessions, emptySlotsReading{free: 1, startable: 1})
	if empty == nil || empty.Owner.Owner != ownership.MoverHarness || empty.SessionID != "watch-1" {
		t.Fatalf("empty = %+v, want the rule standing as the harness's", empty)
	}
	if empty := read(sessions, emptySlotsReading{free: 0, startable: 1}); empty != nil {
		t.Fatalf("empty = %+v, want nothing said once every slot is taken", empty)
	}
	cleared := append(append([]runstate.WatchTransition(nil), sessions...), emptySlotNote("watch-1", moment, runstate.EmptySlots{Cleared: true}))
	if empty := read(cleared, emptySlotsReading{free: 1, startable: 1}); empty != nil {
		t.Fatalf("empty = %+v, want a cleared account to stand for nothing", empty)
	}
	stopped := append(append([]runstate.WatchTransition(nil), sessions...), runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchStopped, At: moment})
	if empty := read(stopped, emptySlotsReading{free: 1, startable: 1}); empty != nil {
		t.Fatalf("empty = %+v, want a stopped session's account to stand for nothing", empty)
	}
	unread := runstate.EmptySlots{Cause: ownership.EmptySlotHold, Reason: "new work is held", ReadyUnread: true, Slots: []int{2}}
	if empty := read([]runstate.WatchTransition{emptySlotNote("watch-1", moment, unread)}, emptySlotsReading{free: 1, startable: 0}); empty != nil {
		t.Fatalf("empty = %+v, want a hold over no startable work said as nothing here", empty)
	}
}

// A hold is whoever placed it: the operator's own is his under his own-hold
// reason, and the brake's is the development manager's.
func TestAnEmptySlotUnderAHoldIsWhoeverPlacedTheHold(t *testing.T) {
	t.Parallel()

	held := runstate.EmptySlots{Cause: ownership.EmptySlotHold, Reason: "new work is held", ReadyUnread: true, Slots: []int{2}}
	transitions := []runstate.WatchTransition{emptySlotNote("watch-1", moment, held)}
	operator := runstate.IntakeHold{HeldBy: runstate.IntakeHolderOperator}
	empty, _ := readEmptySlots(Sources{Sessions: fakeSessions{transitions: transitions}}, emptySlotsReading{free: 1, startable: 2, intake: operator, held: true}, nil, moment)
	if empty == nil || !empty.Owner.Owner.IsOperator() || empty.Owner.Reason != ownership.OperatorOwnHold {
		t.Fatalf("empty = %+v, want the operator's own hold his, under his own-hold reason", empty)
	}
	brake := runstate.IntakeHold{HeldBy: runstate.IntakeHolderBrake}
	empty, _ = readEmptySlots(Sources{Sessions: fakeSessions{transitions: transitions}}, emptySlotsReading{free: 1, startable: 2, intake: brake, held: true}, nil, moment)
	if empty == nil || empty.Owner.Owner != ownership.MoverDevelopmentManager {
		t.Fatalf("empty = %+v, want the brake's hold the development manager's", empty)
	}
}

// The running line says the empty slot as a problem, in its head and under it,
// with since when, who moves it, and each ready item passed over and why.
func TestTheRunningLineSaysAnEmptySlotAsAProblem(t *testing.T) {
	t.Parallel()

	since := moment.Add(-98 * time.Minute)
	standing := Standing{ObservedAt: moment, EmptySlots: &EmptySlots{
		EmptySlots: runstate.EmptySlots{Cause: ownership.EmptySlotNothingEligible, Reason: "no ready item could be started", Slots: []int{3}, Ready: 2,
			Passed: []runstate.EmptySlotItem{{WorkItemID: "yoyodyne-h0ou", Why: "the tree does not meet what it asks for"}}, Since: since, At: since},
		Owner: ownership.Resolve(ownership.KindEmptySlot, ownership.Record{EmptySlot: ownership.EmptySlotNothingEligible}),
	}}
	rendered := standing.renderRunning()
	for _, want := range []string{
		"Running: no run yet, and developer slot 3 empty while ready work waits:\n",
		"  problem: developer slot 3 left empty with 2 ready items waiting, because of no eligible item: no ready item could be started. This has stood since " + localMoment(since),
		"and it is the development manager's to move: ",
		"    passed over yoyodyne-h0ou: the tree does not meet what it asks for",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderRunning() = %q, want it to contain %q", rendered, want)
		}
	}
}

// A slot still counted as taken says, under the problem, which item it is
// counted for, named as every list the harness builds names an item.
func TestTheRunningLineNamesTheItemASlotIsStillCountedFor(t *testing.T) {
	t.Parallel()

	since := moment.Add(-30 * time.Minute)
	standing := Standing{ObservedAt: moment, EmptySlots: &EmptySlots{
		EmptySlots: runstate.EmptySlots{Cause: ownership.EmptySlotStillCounted, Reason: "the slot is still counted as taken for yoyodyne-done",
			Slots: []int{2}, ReadyUnread: true, CountedFor: []string{"yoyodyne-done"}, Since: since, At: since},
		Owner: ownership.Resolve(ownership.KindEmptySlot, ownership.Record{EmptySlot: ownership.EmptySlotStillCounted}),
	}}
	if rendered := standing.renderRunning(); !strings.Contains(rendered, "    the slot is still counted for yoyodyne-done\n") {
		t.Fatalf("renderRunning() = %q, want the item the slot is counted for named under the problem", rendered)
	}
	if named := standing.EmptySlotsCountedFor(); len(named) != 1 || named[0] != "yoyodyne-done" {
		t.Fatalf("EmptySlotsCountedFor() = %v, want the one item named", named)
	}
}

// A free slot beside ready work, with the only runs that were in it waiting on
// GitHub, is said by the reading itself once the scheduler's last word — that no
// slot stands empty — has stood for several polls, naming the runs waiting.
func TestForgeWaitSlotsNamesTheRunsWaitingOnGitHub(t *testing.T) {
	moment := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	cleared := []runstate.WatchTransition{emptySlotNote("watch-1", moment.Add(-10*time.Minute), runstate.EmptySlots{Cleared: true})}
	waiting := []RunningRun{
		{WorkItemID: "yoyodyne-ifd.429.193", Title: "Review threads", WaitingForCI: true},
		{WorkItemID: "yoyodyne-ifd.78.2", WaitingForCI: true},
		{WorkItemID: "yoyodyne-x", WaitingOn: "yoyodyne-y"},
	}
	sources := Sources{Sessions: fakeSessions{transitions: cleared}}
	reading := emptySlotsReading{free: 2, startable: 3}

	empty, problem := readEmptySlots(sources, reading, waiting, moment)
	if problem != "" || empty == nil {
		t.Fatalf("readEmptySlots() = %v, %q; want an account of runs waiting on GitHub", empty, problem)
	}
	if empty.Cause != ownership.EmptySlotWaitingOnForge {
		t.Fatalf("cause = %q", empty.Cause)
	}
	for _, want := range []string{"2 developer slots", "Review threads (yoyodyne-ifd.429.193)", "yoyodyne-ifd.78.2", "waiting on GitHub"} {
		if !strings.Contains(empty.Reason, want) {
			t.Errorf("reason %q does not say %q", empty.Reason, want)
		}
	}
	if strings.Contains(empty.Reason, "yoyodyne-x") {
		t.Errorf("reason %q names a run that is not waiting on GitHub", empty.Reason)
	}

	// Within the polls the scheduler is given, nothing is said.
	if early, _ := readEmptySlots(sources, reading, waiting, moment.Add(-8*time.Minute)); early != nil {
		t.Errorf("a slot the next poll fills was reported: %v", early.Says())
	}
	// No ready work, no free slot, or no run waiting on GitHub: nothing is said.
	for name, none := range map[string]*EmptySlots{
		"no ready work": first(readEmptySlots(sources, emptySlotsReading{free: 2, startable: 0}, waiting, moment)),
		"no free slot":  first(readEmptySlots(sources, emptySlotsReading{free: 0, startable: 3}, waiting, moment)),
		"no waiting":    first(readEmptySlots(sources, reading, waiting[2:], moment)),
	} {
		if none != nil {
			t.Errorf("%s: reported %v", name, none.Says())
		}
	}
}

func first(empty *EmptySlots, _ string) *EmptySlots { return empty }

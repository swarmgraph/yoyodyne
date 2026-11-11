package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// emptySlotNotes is every account of empty slots a session wrote, in order.
func emptySlotNotes(sessions *recordedSessions) []runstate.EmptySlots {
	var notes []runstate.EmptySlots
	for _, transition := range sessions.recorded() {
		if transition.emptySlots != nil {
			notes = append(notes, *transition.emptySlots)
		}
	}
	return notes
}

func operatorHold(reason string) *runstate.IntakeHold {
	return &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Date(2026, 11, 9, 18, 0, 0, 0, time.UTC),
		HeldBy:        runstate.IntakeHolderOperator,
		Reason:        reason,
	}
}

// A hold on new work leaves every free slot empty, and the session says so
// once, naming who placed the hold, however many polls it waits over it.
func TestAHoldOnNewWorkIsRecordedAsWhyTheSlotsStandEmpty(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 2
	harness.held = operatorHold("checking the overnight runs")
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }
	sessions := &recordedSessions{}

	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	notes := emptySlotNotes(sessions)
	if len(notes) != 1 {
		t.Fatalf("empty slot notes = %+v, want one note however many polls the hold stood over", notes)
	}
	note := notes[0]
	if note.Cause != ownership.EmptySlotHold || !strings.Contains(note.Reason, "the operator placed it — checking the overnight runs") {
		t.Fatalf("note = %+v, want the hold named with who placed it and why", note)
	}
	if len(note.Slots) != 2 || note.Slots[0] != 1 || note.Slots[1] != 2 {
		t.Fatalf("note slots = %v, want both free slots named", note.Slots)
	}
	if note.Since.IsZero() || note.Since != note.At {
		t.Fatalf("note = %+v, want since when on the first account", note)
	}
}

// Work that overlaps a run in flight is kept out by a rule, and the account
// names the item kept out and the run it overlaps.
func TestARuleKeepingOverlappingWorkOutIsRecordedWithTheItemItKeptOut(t *testing.T) {
	t.Parallel()

	claimed := beads.WorkItem{ID: "yoyodyne-epic", Title: "The whole of it", Status: "in_progress", Priority: 1}
	child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Second half", Status: "open", Priority: 1, Parent: "yoyodyne-epic"}
	harness := newScheduleHarness(claimed, child)
	harness.capacity = 2
	harness.inFlight[claimed.ID] = runstate.State{RunID: "run-" + claimed.ID, WorkItemID: claimed.ID, Status: runstate.StatusRunning}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	sessions := &recordedSessions{}

	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	notes := emptySlotNotes(sessions)
	if len(notes) != 1 {
		t.Fatalf("empty slot notes = %+v, want one account of the free slot", notes)
	}
	note := notes[0]
	if note.Cause != ownership.EmptySlotRule || !strings.Contains(note.Reason, child.ID) || !strings.Contains(note.Reason, "run-"+claimed.ID) {
		t.Fatalf("note = %+v, want the rule naming the item it kept out and the run it overlaps", note)
	}
	if note.Ready != 1 || len(note.Passed) != 1 || note.Passed[0].WorkItemID != child.ID || note.Passed[0].Why == "" {
		t.Fatalf("note = %+v, want the item passed over named with why", note)
	}
}

// A ready item the tree does not serve is passed over for something about the
// item, so the slot stands empty for want of an eligible item, and the
// account says why the first one was passed over.
func TestNoEligibleItemIsRecordedWithWhyTheReadyItemsWerePassedOver(t *testing.T) {
	t.Parallel()

	accounts := walkAccount([]int{3}, queueOf("yoyodyne-a", "yoyodyne-b"), []readmodel.PassedOverItem{
		{ID: "yoyodyne-a", Class: runstate.PassedOverPrerequisiteUnmet},
		{ID: "yoyodyne-b", Class: runstate.PassedOverPrerequisiteUnmet},
	}, map[string]string{"yoyodyne-a": "it asks for internal/missing.go, which the tree does not have"}, "", "")
	if accounts == nil || accounts.Cause != ownership.EmptySlotNothingEligible {
		t.Fatalf("account = %+v, want no eligible item", accounts)
	}
	if accounts.Ready != 2 || len(accounts.Passed) != 2 || !strings.Contains(accounts.Reason, "internal/missing.go") {
		t.Fatalf("account = %+v, want both items passed over and the first's reason said", accounts)
	}
	if len(accounts.Slots) != 1 || accounts.Slots[0] != 3 {
		t.Fatalf("account slots = %v, want slot 3", accounts.Slots)
	}
	// Work no slot could take is no reason at all: the slot is not left empty
	// while ready work waits.
	if account := walkAccount([]int{3}, backlog.Queue{Entries: []backlog.Entry{{ID: "yoyodyne-parked"}}}, []readmodel.PassedOverItem{
		{ID: "yoyodyne-parked", Class: runstate.PassedOverParked},
	}, nil, "", ""); account != nil {
		t.Fatalf("account = %+v, want none over a queue with no ready work", account)
	}
	// Ready work already in flight is not waiting.
	if account := walkAccount([]int{3}, queueOf("yoyodyne-going"), []readmodel.PassedOverItem{
		{ID: "yoyodyne-going", Class: runstate.PassedOverAlreadyInFlight},
	}, nil, "", ""); account != nil {
		t.Fatalf("account = %+v, want none where the only ready item is in flight", account)
	}
}

// A slot still counted as taken for a start of this session that has no run
// on record is named with the item it is counted for, once the start has had
// the time a dispatch takes to reserve its run.
func TestASlotStillCountedForAFinishedStartIsRecordedWithWhatItIsCountedFor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 9, 20, 17, 0, 0, time.UTC)
	occupied := map[string]runstate.State{
		"yoyodyne-done":    {WorkItemID: "yoyodyne-done"},
		"yoyodyne-running": {WorkItemID: "yoyodyne-running", RunID: "run-1", Status: runstate.StatusRunning},
	}
	mine := map[string]int{"yoyodyne-done": 0, "yoyodyne-running": 1}
	started := []Started{{WorkItemID: "yoyodyne-done", Slot: 2, began: now.Add(-30 * time.Minute)}, {WorkItemID: "yoyodyne-running", Slot: 1, began: now.Add(-time.Hour)}}

	account := countedAccount(occupied, nil, mine, started, now)
	if account == nil || account.Cause != ownership.EmptySlotStillCounted {
		t.Fatalf("account = %+v, want the slot still counted", account)
	}
	if !strings.Contains(account.Reason, "yoyodyne-done") || len(account.Slots) != 1 || account.Slots[0] != 2 {
		t.Fatalf("account = %+v, want slot 2 named as counted for yoyodyne-done", account)
	}
	started[0].began = now.Add(-30 * time.Second)
	if account := countedAccount(occupied, nil, mine, started, now); account != nil {
		t.Fatalf("account = %+v, want a start still reserving its run left alone", account)
	}
}

// A wait nobody named a reason for is recorded as not known, never as nothing,
// and a wait with every slot taken records nothing.
func TestAnEmptySlotNobodyNamedIsRecordedAsNotKnown(t *testing.T) {
	t.Parallel()

	slots := newSlotAccount()
	slots.counted(1, []int{2})
	account := slots.orElse(runstate.WatchIdle, "something nobody classified", false)
	if account == nil || account.Cause != ownership.EmptySlotNotKnown || strings.TrimSpace(account.Reason) == "" {
		t.Fatalf("account = %+v, want a reason recorded as not known", account)
	}
	if !strings.Contains(runstate.EmptySlots{Cause: account.Cause, Reason: account.Reason}.Says(), "could not name") {
		t.Fatalf("says = %q, want the reason said as not known", account.Says())
	}
	full := newSlotAccount()
	full.counted(0, nil)
	if account := full.orElse(runstate.WatchIdle, "", false); account != nil {
		t.Fatalf("account = %+v, want nothing where no slot is free", account)
	}
	if account := walkAccount([]int{1}, queueOf("yoyodyne-x"), nil, nil, "", ""); account == nil || account.Cause != ownership.EmptySlotNotKnown {
		t.Fatalf("account = %+v, want ready work the walk never reached recorded as not known", account)
	}
}

// The session writes an account only when its reason changes, keeps since when
// over the same reason, and ends it with one cleared note.
func TestTheSessionRecordsAnEmptySlotAgainOnlyWhenTheReasonChanges(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 11, 9, 18, 17, 0, 0, time.UTC)
	sessions := &recordedSessions{}
	session := &watchSession{to: sessions, now: func() time.Time { return clock }, schedule: &Schedule{}}
	rule := &runstate.EmptySlots{Cause: ownership.EmptySlotRule, Reason: "a rule keeps yoyodyne-a out", Slots: []int{3}, Ready: 1}

	session.emptySlots(rule)
	clock = clock.Add(time.Minute)
	session.emptySlots(rule)
	clock = clock.Add(time.Minute)
	moved := *rule
	moved.Slots = []int{2, 3}
	session.emptySlots(&moved)
	clock = clock.Add(time.Minute)
	session.emptySlots(nil)
	session.emptySlots(nil)

	notes := emptySlotNotes(sessions)
	if len(notes) != 3 {
		t.Fatalf("notes = %+v, want the first account, the account over another slot, and one clearing", notes)
	}
	if !notes[1].Since.Equal(notes[0].Since) || notes[1].At.Equal(notes[0].At) {
		t.Fatalf("notes = %+v, want the same reason to keep since when", notes)
	}
	if !notes[2].Cleared {
		t.Fatalf("last note = %+v, want the account cleared", notes[2])
	}
}

// queueOf is a queue of ready entries.
func queueOf(ids ...string) backlog.Queue {
	queue := backlog.Queue{}
	for _, id := range ids {
		queue.Entries = append(queue.Entries, backlog.Entry{ID: id, Ready: true})
	}
	return queue
}

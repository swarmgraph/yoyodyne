package runstate

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// An account of empty slots is a note on the log, read back whole and not
// where the session got to; one that names no reason, or a cleared one that
// still names one, is refused, so an empty slot is never recorded blank.
func TestAnEmptySlotAccountIsANoteReadBackWhole(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	idle := testWatchTransition(testWatchSessionID, WatchIdle, "nothing further pullable")
	note := testWatchTransition(testWatchSessionID, WatchWatching, "developer slot 3 left empty")
	note.At = idle.At.Add(time.Minute)
	note.EmptySlots = &EmptySlots{
		Cause: ownership.EmptySlotRule, Reason: "a rule keeps yoyodyne-a out", Slots: []int{3}, Ready: 2,
		Passed: []EmptySlotItem{{WorkItemID: "yoyodyne-a", Why: "sequenced behind work in flight"}},
		Since:  note.At, At: note.At,
	}
	for _, transition := range []WatchTransition{idle, note} {
		if err := store.Record(transition); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	recorded, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || recorded[1].EmptySlots == nil || !recorded[1].EmptySlots.SameReason(*note.EmptySlots) {
		t.Fatalf("List() = %#v, want the account read back whole", recorded)
	}
	if latest, watched, err := store.Latest(); err != nil || !watched || latest.State != WatchIdle {
		t.Fatalf("Latest() = %#v (watched %v, error %v), want the idle poll rather than the note", latest, watched, err)
	}

	for name, broken := range map[string]func(*WatchTransition){
		"on an idle entry":           func(t *WatchTransition) { t.State = WatchIdle },
		"with no cause":              func(t *WatchTransition) { t.EmptySlots.Cause = "" },
		"with no reason":             func(t *WatchTransition) { t.EmptySlots.Reason = " " },
		"with no since":              func(t *WatchTransition) { t.EmptySlots.Since = time.Time{} },
		"with an item and no why":    func(t *WatchTransition) { t.EmptySlots.Passed[0].Why = "" },
		"cleared and still a reason": func(t *WatchTransition) { t.EmptySlots.Cleared = true },
		"naming slot zero":           func(t *WatchTransition) { t.EmptySlots.Slots = []int{0} },
	} {
		refused := note
		account := *note.EmptySlots
		account.Passed = append([]EmptySlotItem(nil), account.Passed...)
		refused.EmptySlots = &account
		broken(&refused)
		if err := store.Record(refused); err == nil {
			t.Errorf("Record() of an empty slot account %s was accepted", name)
		}
	}
	cleared := note
	cleared.EmptySlots = &EmptySlots{Cleared: true, At: note.At}
	if err := store.Record(cleared); err != nil {
		t.Fatalf("Record() of a cleared account error = %v", err)
	}
}

// An account naming a child work item, whose id carries the dots of its
// number, or a run, which a document landing is counted under, is recorded
// like any other, and one naming something that is neither is refused. The
// check once held work item ids to the shape of a configured name, so every
// account naming a slot counted for such an item was refused whole and the log said nothing while the slot stood
// empty. A cleared account says which step ended it, and where every slot was
// counted taken, what holds them.
func TestAnEmptySlotAccountNamingAChildItemIsRecorded(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	counted := testWatchTransition(testWatchSessionID, WatchWatching, "1 developer slot left empty")
	counted.EmptySlots = &EmptySlots{
		Cause: ownership.EmptySlotStillCounted, Reason: "the slot is still counted as taken for yoyodyne-ifd.309", ReadyUnread: true, FreeSlots: 1,
		CountedFor: []string{"yoyodyne-ifd.309", "run-6768ca4fa272988b78495592bf66c31e"},
		Passed:     []EmptySlotItem{{WorkItemID: "yoyodyne-ifd.428.126.4.19", Why: "sequenced behind work in flight"}},
		Since:      counted.At, At: counted.At,
	}
	full := testWatchTransition(testWatchSessionID, WatchWatching, "no developer slot is left empty while ready work waits")
	full.At = counted.At.Add(time.Minute)
	full.EmptySlots = &EmptySlots{
		Cleared: true, Ended: EmptySlotsEveryTaken, Taken: 2,
		TakenBy: []EmptySlotHolder{{WorkItemID: "yoyodyne-ifd.428.99.27", RunID: "run-6768ca4fa272988b78495592bf66c31e"}, {WorkItemID: "yoyodyne-ifd.309"}},
		Since:   full.At, At: full.At,
	}
	for _, transition := range []WatchTransition{counted, full} {
		if err := store.Record(transition); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	recorded, err := store.List()
	if err != nil || len(recorded) != 2 || !recorded[1].EmptySlots.SameReason(*full.EmptySlots) {
		t.Fatalf("List() = %#v, %v; want both accounts read back whole", recorded, err)
	}

	for name, wrong := range map[string]EmptySlots{
		"an ending on a standing account":  {Cause: ownership.EmptySlotRule, Reason: "a rule", Ended: EmptySlotsFilled},
		"holders without every slot taken": {Cleared: true, Ended: EmptySlotsFilled, Taken: 1, TakenBy: []EmptySlotHolder{{WorkItemID: "yoyodyne-a"}}},
		"an ending nobody names":           {Cleared: true, Ended: "somewhere"},
		"counted for no item or run":       {Cause: ownership.EmptySlotStillCounted, Reason: "counted", CountedFor: []string{"../yoyodyne ifd"}},
	} {
		wrong.At, wrong.Since = full.At, full.At
		if err := wrong.validate(); err == nil {
			t.Errorf("%s: validate() accepted %+v", name, wrong)
		}
	}
}

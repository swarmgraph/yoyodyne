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

package runstate

import (
	"testing"
	"time"
)

// Giving the slot back puts the wait back so the run holds no slot, marks a
// GitHub-checks wait as not holding it, and changes nothing on a run that
// already holds none or has ended.
func TestSlotGivenBackPutsTheWaitBackAndChangesNothingTwice(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 27, 18, 26, 0, 0, time.UTC)
	taken := State{RunID: "run-a", Status: StatusRunning}
	if !taken.HoldsDeveloperSlot() {
		t.Fatal("the taken-up run holds no slot, so the test proves nothing")
	}
	wait := &CIWait{Commit: "9afda54e", Branch: "b", PushedAt: at, Required: []string{"test"}, SlotTaken: true}
	given, changed, err := ApplyUpdate(taken, SlotGivenBack{CIWait: wait, At: at})
	if err != nil || !changed {
		t.Fatalf("ApplyUpdate() changed = %t, error = %v; want the wait put back", changed, err)
	}
	if given.HoldsDeveloperSlot() || given.CIWait == nil || given.CIWait.SlotTaken || !given.UpdatedAt.Equal(at) {
		t.Fatalf("after giving the slot back: %#v; want a checks wait holding no slot", given.CIWait)
	}
	if wait.SlotTaken != true {
		t.Fatal("the update changed the wait it was handed")
	}
	if _, changed, err := ApplyUpdate(given, SlotGivenBack{EpicWait: &EpicWait{Since: at}, At: at}); err != nil || changed {
		t.Fatalf("giving back a slot already given back: changed = %t, error = %v; want nothing changed", changed, err)
	}
	ended := taken
	ended.Status = StatusFailed
	if _, changed, err := ApplyUpdate(ended, SlotGivenBack{EpicWait: &EpicWait{Since: at}, At: at}); err != nil || changed {
		t.Fatalf("giving back the slot of an ended run: changed = %t, error = %v; want nothing changed", changed, err)
	}
	if _, _, err := ApplyUpdate(taken, SlotGivenBack{At: at}); err == nil {
		t.Fatal("an update naming no wait was accepted")
	}
}

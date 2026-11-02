package runstate

import (
	"os"
	"testing"
	"time"
)

// The hold stands from when it is placed until it is lifted or reaches its
// own end, whichever comes first; lifting none is not an error, and a record
// another product wrote is refused rather than read as no hold.
func TestATrackerSquashHoldStandsUntilLiftedOrItsEnd(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := NewTrackerSquashHoldStore(root, "calc")
	if err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)
	if _, standing, err := store.Standing(since); err != nil || standing {
		t.Fatalf("Standing() before any hold = %t, %v, want none", standing, err)
	}
	if err := store.Place(TrackerSquashHold{Since: since, Until: since.Add(19 * time.Minute), Reason: "listing open items took 33 seconds"}); err != nil {
		t.Fatal(err)
	}
	if hold, standing, err := store.Standing(since.Add(18 * time.Minute)); err != nil || !standing || hold.Reason != "listing open items took 33 seconds" {
		t.Fatalf("Standing() inside the hold = %+v, %t, %v, want it standing", hold, standing, err)
	}
	if _, standing, err := store.Standing(since.Add(19 * time.Minute)); err != nil || standing {
		t.Fatalf("Standing() at its end = %t, %v, want it ended on its own", standing, err)
	}
	if err := store.Lift(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Fatalf("the hold's file after Lift() = %v, want it gone", err)
	}
	if err := store.Lift(); err != nil {
		t.Fatalf("Lift() with no hold = %v, want no error", err)
	}
	if err := store.Place(TrackerSquashHold{Since: since, Until: since}); err == nil {
		t.Fatal("Place() of a hold that ends as it starts succeeded, want it refused")
	}

	other, err := NewTrackerSquashHoldStore(root, "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Place(TrackerSquashHold{Since: since, Until: since.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(other.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Standing(since); err == nil {
		t.Fatal("Standing() of another product's hold succeeded, want it refused")
	}
	var unwired *TrackerSquashHoldStore
	if _, standing, err := unwired.Standing(since); err != nil || standing {
		t.Fatalf("Standing() with no store = %t, %v, want no hold", standing, err)
	}
}

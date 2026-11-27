package runstate

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// LaunchHolds names the hold files still on disk for one run's launches and
// nothing of another run's.
func TestLaunchHoldsListsOnlyTheRunsOwnHolds(t *testing.T) {
	store, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.root, 0o700); err != nil {
		t.Fatal(err)
	}
	const ours, theirs = "run-0123456789abcdef0123456789abcdef", "run-fedcba9876543210fedcba9876543210"
	for _, name := range []string{ours + ".attempt-1.hold", ours + ".attempt-2.hold", theirs + ".attempt-1.hold", ours + ".json"} {
		if err := os.WriteFile(filepath.Join(store.root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	holds, err := store.LaunchHolds(ours)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(store.root, ours+".attempt-1.hold"), filepath.Join(store.root, ours+".attempt-2.hold")}
	slices.Sort(holds)
	if !slices.Equal(holds, want) {
		t.Fatalf("LaunchHolds() = %v, want %v", holds, want)
	}
	if _, err := store.LaunchHolds("../elsewhere"); err == nil {
		t.Fatal("LaunchHolds() accepted a run id that is not one")
	}
}

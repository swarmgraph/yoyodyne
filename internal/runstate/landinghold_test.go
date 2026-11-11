package runstate

import (
	"os"
	"strings"
	"testing"
	"time"
)

func newLandingHoldTestStore(t *testing.T) *LandingHoldStore {
	t.Helper()
	store, err := NewLandingHoldStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A hold stands from when it is placed until it is released or reaches its own
// end, whichever comes first, and releasing none is not an error.
func TestLandingHoldStandsUntilReleasedOrItsEnd(t *testing.T) {
	t.Parallel()
	store := newLandingHoldTestStore(t)
	now := time.Date(2026, 11, 9, 20, 2, 0, 0, time.UTC)
	if _, held, err := store.Standing("staging", now); err != nil || held {
		t.Fatalf("Standing() before any hold = %t, %v, want none", held, err)
	}
	placed, err := store.Place("staging", "  cutting\nrelease ", 2*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if placed.Reason != "cutting release" || !placed.Until.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("Place() = %+v, want a folded reason and an end two hours out", placed)
	}
	if hold, held, err := store.Standing("staging", now.Add(time.Hour)); err != nil || !held || hold.TargetBranch != "staging" {
		t.Fatalf("Standing() inside the hold = %+v, %t, %v, want it held", hold, held, err)
	}
	if !strings.Contains(placed.Says(), "yoyo landing release staging") {
		t.Fatalf("Says() = %q, want it to name the release command", placed.Says())
	}
	if hold, held, err := store.Standing("staging", now.Add(2*time.Hour)); err != nil || held || hold.Reason == "" {
		t.Fatalf("Standing() at its end = %+v, %t, %v, want it expired and reported", hold, held, err)
	}
	if hold, found, err := store.Release("staging"); err != nil || !found || hold.Reason != "cutting release" {
		t.Fatalf("Release() = %+v, %t, %v, want the hold reported", hold, found, err)
	}
	if _, err := os.Stat(store.Path("staging")); !os.IsNotExist(err) {
		t.Fatalf("the hold's file after Release() = %v, want it gone", err)
	}
	if _, found, err := store.Release("staging"); err != nil || found {
		t.Fatalf("Release() of none = %t, %v, want no record and no error", found, err)
	}
	var unwired *LandingHoldStore
	if _, held, err := unwired.Standing("staging", now); err != nil || held {
		t.Fatalf("Standing() with no store = %t, %v, want none", held, err)
	}
}

func TestLandingHoldPlaceRefusals(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 20, 2, 0, 0, time.UTC)
	tests := []struct {
		name, branch, reason string
		duration             time.Duration
		want                 string // empty means accepted
	}{
		{"zero", "staging", "r", 0, "a landing hold needs a duration: say how long it lasts with --for, up to 4 hours, such as --for 30m"},
		{"four hours", "staging", "r", 4 * time.Hour, ""},
		{"over four hours", "staging", "r", 4*time.Hour + time.Second, "--for 4h0m1s asks for longer"},
		{"negative", "staging", "r", -time.Minute, "a landing hold's duration must be more than zero, and -1m0s is not"},
		{"empty reason", "staging", " \n ", time.Hour, "a landing hold needs a reason, so whoever reads the status line knows why landing stopped"},
		{"bad branch", "refs/heads/staging", "r", time.Hour, "not a local branch name"},
		{"head", "HEAD", "r", time.Hour, "not a local branch name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := newLandingHoldTestStore(t).Place(test.branch, test.reason, test.duration, now)
			if test.want == "" {
				if err != nil {
					t.Fatalf("Place() = %v, want it accepted", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Place() = %v, want it to say %q", err, test.want)
			}
		})
	}
}

func TestLandingHoldListIsStandingHoldsSortedByBranch(t *testing.T) {
	t.Parallel()
	store := newLandingHoldTestStore(t)
	now := time.Date(2026, 11, 9, 20, 2, 0, 0, time.UTC)
	if holds, err := store.List(now); err != nil || len(holds) != 0 {
		t.Fatalf("List() before any hold = %v, %v, want none", holds, err)
	}
	for _, p := range []struct {
		branch string
		d      time.Duration
	}{{"staging", time.Hour}, {"main", 2 * time.Hour}, {"short", time.Minute}} {
		if _, err := store.Place(p.branch, "r", p.d, now); err != nil {
			t.Fatal(err)
		}
	}
	holds, err := store.List(now.Add(30 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 2 || holds[0].TargetBranch != "main" || holds[1].TargetBranch != "staging" {
		t.Fatalf("List() = %+v, want main then staging with the expired one skipped", holds)
	}
	if _, err := os.Stat(store.Path("short")); err != nil {
		t.Fatalf("the expired hold's file = %v, want it left in place", err)
	}
}

// Branches that differ only by a separator keep separate holds.
func TestLandingHoldsOnSimilarBranchNamesDoNotCollide(t *testing.T) {
	t.Parallel()
	store := newLandingHoldTestStore(t)
	now := time.Date(2026, 11, 9, 20, 2, 0, 0, time.UTC)
	if _, err := store.Place("release/1.2", "slash", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, held, err := store.Standing("release-1.2", now); err != nil || held {
		t.Fatalf("Standing(release-1.2) = %t, %v, want none", held, err)
	}
	if _, err := store.Place("release-1.2", "dash", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if hold, _, err := store.Standing("release/1.2", now); err != nil || hold.Reason != "slash" {
		t.Fatalf("Standing(release/1.2) = %+v, %v, want its own hold", hold, err)
	}
	if holds, err := store.List(now); err != nil || len(holds) != 2 {
		t.Fatalf("List() = %+v, %v, want both", holds, err)
	}
}

func TestLandingHoldThatCannotBeReadIsAnError(t *testing.T) {
	t.Parallel()
	store := newLandingHoldTestStore(t)
	now := time.Date(2026, 11, 9, 20, 2, 0, 0, time.UTC)
	if _, err := store.Place("staging", "r", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path("staging"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Standing("staging", now); err == nil {
		t.Fatal("Standing() of a corrupt record succeeded, want an error")
	}
	if _, err := store.List(now); err == nil {
		t.Fatal("List() with a corrupt record succeeded, want an error")
	}
	if _, found, err := store.Release("staging"); err != nil || !found {
		t.Fatalf("Release() of a corrupt record = %t, %v, want it cleared", found, err)
	}

	other, err := NewLandingHoldStore(store.root[:strings.LastIndex(store.root, "/")], "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Place("staging", "r", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(other.Path("staging"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path("staging"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Standing("staging", now); err == nil {
		t.Fatal("Standing() of another product's hold succeeded, want an error")
	}
}

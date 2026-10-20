package runstate

import (
	"context"
	"testing"
	"time"
)

func TestCadenceAdoptionPreservesFiringsAndSurvivesRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fired := time.Date(2026, 10, 2, 8, 18, 18, 0, time.FixedZone("PDT", -7*60*60))
	adopted := time.Date(2026, 10, 2, 16, 15, 0, 0, fired.Location())
	for _, test := range []struct {
		name      string
		old, next time.Duration
		due       time.Time
	}{
		{"shortening", 24 * time.Hour, time.Hour, adopted},
		{"lengthening", time.Hour, 24 * time.Hour, fired.Add(24 * time.Hour)},
		{"unchanged", time.Hour, time.Hour, fired.Add(time.Hour)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newSweepStore(t)
			initial, err := store.Claim(ctx, "architect-pass", test.old, fired)
			if err != nil {
				t.Fatal(err)
			}
			claim, found, err := store.Adopt(ctx, "architect-pass", test.next, adopted)
			if err != nil || !found {
				t.Fatalf("Adopt: %v, %v", found, err)
			}
			if !claim.FiredAt.Equal(initial.FiredAt) || !claim.UpdatedAt.Equal(initial.UpdatedAt) || claim.Firings != initial.Firings || claim.Settled() {
				t.Fatalf("adoption changed the firing: %+v", claim)
			}
			if !claim.NextDue(test.next).Equal(test.due) {
				t.Fatalf("due %s, want %s", claim.NextDue(test.next), test.due)
			}
			// A different store is a restarted process reading the same state.
			restarted := &SweepStore{root: store.root, state: store.state, productID: store.productID}
			later, _, err := restarted.Adopt(ctx, "architect-pass", test.next, adopted.Add(2*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if !later.CadenceAt.Equal(claim.CadenceAt) || !later.NextDue(test.next).Equal(test.due) {
				t.Fatalf("restart reset the cadence: %+v", later)
			}
		})
	}
}

func TestLegacyCadenceDoesNotInventAdoptionTiming(t *testing.T) {
	t.Parallel()
	store := newSweepStore(t)
	now := time.Date(2026, 10, 2, 16, 15, 0, 0, time.UTC)
	legacy := SweepClaim{SchemaVersion: SweepSchemaVersion, ProductID: "example", Task: "architect-pass", FiredAt: now.Add(-8 * time.Hour), UpdatedAt: now.Add(-7 * time.Hour), Firings: 1, Problem: "old failure"}
	if err := store.save(legacy.Task, legacy); err != nil {
		t.Fatal(err)
	}
	claim, _, err := store.Adopt(context.Background(), legacy.Task, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if !claim.CadenceUncertain || !claim.NextDue(time.Hour).Equal(now) || claim.Problem != legacy.Problem || !claim.Settled() {
		t.Fatalf("legacy adoption: %+v", claim)
	}
	if _, err := store.Claim(context.Background(), legacy.Task, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	claim, _, err = store.Find(legacy.Task)
	if err != nil || claim.CadenceUncertain {
		t.Fatalf("an actual firing should establish the cadence: %+v, %v", claim, err)
	}
}

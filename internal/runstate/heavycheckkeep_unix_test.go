//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// keptAsk is an ask that reads whether the merge queue still needs a heavy
// check from needed, which a test flips as an entry joins and leaves the queue.
func keptAsk(run string, limit int, priority domain.HeavyCheckPriority, needed *atomic.Bool) HeavyCheckAsk {
	ask := heavyAsk(run, "make race", limit, priority)
	ask.LandingNeeds = func() map[string]bool {
		if needed.Load() {
			return map[string]bool{"make race": true}
		}
		return nil
	}
	return ask
}

// With the queue holding an entry that needs the check, ordinary checks stop one
// place short, so a landing check that arrives while the others are held by
// ordinary checks starts at once instead of waiting for one of them to finish.
func TestALandingCheckStartsAtOnceWhenOrdinaryChecksHoldEveryOtherPlace(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)
	var needed atomic.Bool
	needed.Store(true)

	first := acquireSoon(t, store, keptAsk("run-a", 3, domain.HeavyCheckOrdinary, &needed))
	second := acquireSoon(t, store, keptAsk("run-b", 3, domain.HeavyCheckForPreservedWork, &needed))
	third, thirdWaiting, thirdFailed := acquireLater(store, keptAsk("run-c", 3, domain.HeavyCheckOrdinary, &needed))
	expectWaiting(t, thirdWaiting, thirdFailed)
	expectStillWaiting(t, third, thirdFailed)

	landing := acquireSoon(t, store, keptAsk("run-d", 3, domain.HeavyCheckForLanding, &needed))
	if landing.Permit.RunID != "run-d" || landing.Waited > 5*time.Second {
		t.Fatalf("landing = %s after %s, want run-d without waiting for an ordinary check", landing.Permit.RunID, landing.Waited)
	}
	// The ordinary check still waits: the place is the landing check's while it runs.
	expectStillWaiting(t, third, thirdFailed)
	for _, grant := range []*HeavyCheckGrant{first, second, landing} {
		if _, err := grant.Finish("not started"); err != nil {
			t.Fatal(err)
		}
	}
	needed.Store(false)
	final := expectGranted(t, third, thirdFailed)
	_, _ = final.Finish("not started")
}

// An ordinary check waits for the kept place even while it is free, and takes it
// the moment no entry needs the check, with nothing cleared by hand.
func TestAnOrdinaryCheckTakesTheKeptPlaceOnceNoEntryNeedsIt(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)
	var needed atomic.Bool
	needed.Store(true)

	holder := acquireSoon(t, store, keptAsk("run-a", 2, domain.HeavyCheckOrdinary, &needed))
	ordinary, waiting, failed := acquireLater(store, keptAsk("run-b", 2, domain.HeavyCheckOrdinary, &needed))
	expectWaiting(t, waiting, failed)
	expectStillWaiting(t, ordinary, failed)
	if record := recordOf(t, store); len(record.Holding("make race")) != 1 {
		t.Fatalf("holding %d places, want the running check left alone and the second place free", len(record.Holding("make race")))
	}
	needed.Store(false)
	got := expectGranted(t, ordinary, failed)
	_, _ = got.Finish("not started")
	_, _ = holder.Finish("not started")
}

// Where the check has one place, nothing is kept: an ordinary check takes it
// although the queue holds an entry that needs the check.
func TestNothingIsKeptWhereTheCheckHasOnePlace(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)
	var needed atomic.Bool
	needed.Store(true)
	got := acquireSoon(t, store, keptAsk("run-a", 1, domain.HeavyCheckOrdinary, &needed))
	_, _ = got.Finish("not started")
}

// With no entry needing the check, ordinary checks use every place.
func TestOrdinaryChecksUseEveryPlaceWhenTheQueueNeedsNone(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)
	var needed atomic.Bool
	first := acquireSoon(t, store, keptAsk("run-a", 2, domain.HeavyCheckOrdinary, &needed))
	second := acquireSoon(t, store, keptAsk("run-b", 2, domain.HeavyCheckOrdinary, &needed))
	_, _ = first.Finish("not started")
	_, _ = second.Finish("not started")
}

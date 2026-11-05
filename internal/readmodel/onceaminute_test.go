package readmodel

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// failingTracker is a changingTracker whose open and ready listings fail
// while failing is set, as a tracker store held past its limit does.
type failingTracker struct {
	*changingTracker
	mu      sync.Mutex
	failing bool
}

var errStoreHeld = errors.New("bd list timed out after 30s waiting for the store")

func (f *failingTracker) fail(failing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = failing
}

func (f *failingTracker) failed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failing
}

func (f *failingTracker) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	if f.failed() {
		return nil, errStoreHeld
	}
	return f.changingTracker.List(ctx, status)
}

func (f *failingTracker) Ready(ctx context.Context) ([]beads.WorkItem, error) {
	if f.failed() {
		return nil, errStoreHeld
	}
	return f.changingTracker.Ready(ctx)
}

func (f *failingTracker) ListUnfinished(ctx context.Context) ([]beads.WorkItem, error) {
	if f.failed() {
		return nil, errStoreHeld
	}
	return f.changingTracker.ListUnfinished(ctx)
}

// madeSince is the tracker commands made since before.
func madeSince(before, after map[string]int) map[string]int {
	made := map[string]int{}
	for kind, n := range after {
		if n != before[kind] {
			made[kind] = n - before[kind]
		}
	}
	return made
}

// Six dashboard refreshes inside one minute, ten seconds apart as the page
// polls, ask the tracker for the open work once and for what is ready once,
// and so do the six after the minute is up.
func TestRefreshesInsideOneMinuteListTheOpenAndReadyWorkOnce(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	tracker := newChangingTracker(
		beads.WorkItem{ID: "yoyodyne-1", Title: "Queued", Status: "open"},
		beads.WorkItem{ID: "yoyodyne-2", Title: "Landed", Status: "closed"},
	)
	readings := &clockedReadings{now: now}

	minute := func(first map[string]int) {
		t.Helper()
		before := tracker.counts()
		for refresh := range 6 {
			if refresh > 0 {
				clock = clock.Add(10 * time.Second)
			}
			dashboardRefresh(t, tracker, readings, now)
		}
		if made := madeSince(before, tracker.counts()); !reflect.DeepEqual(made, first) {
			t.Fatalf("six refreshes inside one minute asked the tracker %v; want %v", made, first)
		}
	}
	// The first minute lists every item once, closed work included, which is
	// the open listing and the closed items together.
	minute(map[string]int{"every": 1, "ready": 1})
	clock = clock.Add(sharedReadingFresh)
	minute(map[string]int{"unfinished": 1, "ready": 1})
}

// The same holds with the store the product keeps its shared reading in,
// which every page, surface, and pass on the machine asks: refreshes made
// together list the open and ready work once between them.
func TestRefreshesSharingTheStoreListTheOpenAndReadyWorkOnce(t *testing.T) {
	t.Parallel()
	store, err := runstate.NewTrackerReadingStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	tracker := newChangingTracker(beads.WorkItem{ID: "yoyodyne-1", Title: "Queued", Status: "open"})
	var surfaces sync.WaitGroup
	for range 4 {
		surfaces.Go(func() {
			sources := quietSources()
			sources.Tracker, sources.TrackerReadings, sources.Now = tracker, store, time.Now
			for range 3 {
				ReadStanding(context.Background(), sources)
			}
		})
	}
	surfaces.Wait()
	if counts := tracker.counts(); !reflect.DeepEqual(counts, map[string]int{"every": 1, "ready": 1}) {
		t.Fatalf("twelve standings from four surfaces asked the tracker %v; want one listing and one readiness query", counts)
	}
}

// A listing that fails keeps the last good one: the standing and the titles
// are read from what the tracker last answered, and the standing says when
// that was and what the failed listing said, until a listing succeeds again.
func TestAFailedListingKeepsTheLastGoodListingAndItsTime(t *testing.T) {
	t.Parallel()
	good := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	clock := good
	now := func() time.Time { return clock }
	tracker := &failingTracker{changingTracker: newChangingTracker(
		beads.WorkItem{ID: "yoyodyne-1", Title: "Queued", Status: "open"},
		beads.WorkItem{ID: "yoyodyne-2", Title: "Landed", Status: "closed"},
	)}
	readings := &clockedReadings{now: now}
	standing := func() Standing {
		t.Helper()
		sources := quietSources()
		sources.Tracker, sources.TrackerReadings, sources.Now = tracker, readings, now
		return ReadStanding(context.Background(), sources)
	}

	first := standing()
	if first.TrackerListing == nil || !first.TrackerListing.TakenAt.Equal(good) || first.TrackerListing.Failure != "" {
		t.Fatalf("the first standing's listing is %+v; want one taken at %s that did not fail", first.TrackerListing, good)
	}

	tracker.fail(true)
	failedAt := good.Add(sharedReadingFresh + time.Second)
	clock = failedAt
	held := standing()
	listing := held.TrackerListing
	if listing == nil || !listing.TakenAt.Equal(good) {
		t.Fatalf("the standing after a failed listing gives its listing as %+v; want the last good one, taken at %s", listing, good)
	}
	if !strings.Contains(listing.Failure, errStoreHeld.Error()) || listing.FailedAt == nil || !listing.FailedAt.Equal(failedAt) {
		t.Fatalf("the standing after a failed listing says %q at %v; want the tracker's own words at %s", listing.Failure, listing.FailedAt, failedAt)
	}
	if held.Startable != first.Startable || held.Admitted != first.Admitted || held.NotStartableProblem != first.NotStartableProblem {
		t.Fatalf("the standing after a failed listing has %d startable and %d admitted (%q); want the last good listing's %d and %d (%q)",
			held.Startable, held.Admitted, held.NotStartableProblem, first.Startable, first.Admitted, first.NotStartableProblem)
	}
	if title, found := held.Titles.Title("yoyodyne-1"); !found || title != "Queued" {
		t.Fatalf("the standing after a failed listing names yoyodyne-1 %q (found = %v); want the last good listing's title", title, found)
	}

	// A second failure a minute later still carries the first good listing's
	// time, not the time of the failure before it.
	clock = clock.Add(sharedReadingFresh + time.Second)
	if listing := standing().TrackerListing; listing == nil || !listing.TakenAt.Equal(good) || listing.Failure == "" {
		t.Fatalf("the standing after a second failed listing gives its listing as %+v; want the one taken at %s", listing, good)
	}

	tracker.fail(false)
	clock = clock.Add(sharedReadingFresh + time.Second)
	if listing := standing().TrackerListing; listing == nil || !listing.TakenAt.Equal(clock) || listing.Failure != "" {
		t.Fatalf("the standing after the tracker answers again gives its listing as %+v; want a new one taken at %s", listing, clock)
	}
}

// A listing that fails with nothing good ever kept is a failure as it always
// was: there is no listing to show in its place.
func TestAFailedFirstListingHasNothingToKeep(t *testing.T) {
	t.Parallel()
	now := func() time.Time { return time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC) }
	tracker := &failingTracker{changingTracker: newChangingTracker(beads.WorkItem{ID: "yoyodyne-1", Status: "open"}), failing: true}
	sources := quietSources()
	sources.Tracker, sources.TrackerReadings, sources.Now = tracker, &clockedReadings{now: now}, now
	standing := ReadStanding(context.Background(), sources)
	if standing.TrackerListing != nil {
		t.Fatalf("a standing whose first listing failed gives a listing %+v; want none", standing.TrackerListing)
	}
	if !strings.Contains(standing.NotStartableProblem, errStoreHeld.Error()) {
		t.Fatalf("a standing whose first listing failed says %q; want the tracker's failure", standing.NotStartableProblem)
	}
}

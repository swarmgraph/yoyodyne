package readmodel

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// changingTracker is a tracker the test changes between refreshes, counting
// each kind of command it is asked: "every" is a listing of every item, closed
// work included, which is the one a refresh must not make after the first.
type changingTracker struct {
	mu      sync.Mutex
	items   []beads.WorkItem
	changed map[string]time.Time
	calls   map[string]int
}

func newChangingTracker(items ...beads.WorkItem) *changingTracker {
	return &changingTracker{items: items, changed: map[string]time.Time{}, calls: map[string]int{}}
}

func (c *changingTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if status == "" {
		c.calls["every"]++
		return slices.Clone(c.items), nil
	}
	c.calls["status"]++
	return c.matching(func(item beads.WorkItem) bool { return item.Status == status }), nil
}

func (c *changingTracker) Ready(context.Context) ([]beads.WorkItem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls["ready"]++
	return c.matching(func(item beads.WorkItem) bool { return item.Status == "open" }), nil
}

func (c *changingTracker) ListUnfinished(context.Context) ([]beads.WorkItem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls["unfinished"]++
	return c.matching(func(item beads.WorkItem) bool { return item.Status != "closed" }), nil
}

func (c *changingTracker) ListClosedSince(_ context.Context, since time.Time) ([]beads.WorkItem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls["closed since"]++
	return c.matching(func(item beads.WorkItem) bool {
		return item.Status == "closed" && c.changed[item.ID].After(since)
	}), nil
}

func (c *changingTracker) matching(keep func(beads.WorkItem) bool) []beads.WorkItem {
	var items []beads.WorkItem
	for _, item := range c.items {
		if keep(item) {
			items = append(items, item)
		}
	}
	return items
}

// set gives one item a status, as a write to the tracker at a time would.
func (c *changingTracker) set(id, status string, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index := range c.items {
		if c.items[index].ID == id {
			c.items[index].Status = status
			c.changed[id] = at
		}
	}
}

func (c *changingTracker) counts() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := map[string]int{}
	for kind, n := range c.calls {
		counts[kind] = n
	}
	return counts
}

// clockedReadings keeps the shared reading as runstate.TrackerReadingStore
// does — handed over while fresh, and otherwise made again with the kept one
// handed to whatever makes it — on the test's clock rather than the wall's.
type clockedReadings struct {
	mu   sync.Mutex
	now  func() time.Time
	kept *runstate.TrackerReading
}

func (c *clockedReadings) Share(_ context.Context, fresh time.Duration, read func(*runstate.TrackerReading) (runstate.TrackerReading, bool)) (runstate.TrackerReading, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	asked := c.now()
	if c.kept != nil && !c.kept.StartedAt.Before(asked.Add(-fresh)) {
		return *c.kept, nil
	}
	var previous *runstate.TrackerReading
	if c.kept != nil {
		kept := *c.kept
		previous = &kept
	}
	reading, keep := read(previous)
	reading.StartedAt = asked
	if keep {
		c.kept = &reading
	}
	return reading, nil
}

func (c *clockedReadings) every() []beads.WorkItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kept == nil {
		return nil
	}
	return slices.Clone(c.kept.Every)
}

// dashboardRefresh is what one refresh of the dashboard reads from the
// tracker: the standing, the titles its throughput is named with, and the
// delivery measures.
func dashboardRefresh(t *testing.T, tracker *changingTracker, readings TrackerReadings, now func() time.Time) (*WorkItemTitles, Delivery) {
	t.Helper()
	sources := quietSources()
	sources.Tracker, sources.TrackerReadings, sources.Now = tracker, readings, now
	ReadStanding(context.Background(), sources)
	titles, err := ReadWorkItemTitles(context.Background(), sources)
	if err != nil {
		t.Fatalf("titles: %v", err)
	}
	delivery := ReadDelivery(context.Background(), DeliverySources{Tracker: tracker, TrackerReadings: readings, Now: now})
	return titles, delivery
}

// One dashboard refresh after the first lists the unfinished work and asks
// what is ready, and nothing else: the closed items are carried from the
// reading before. Something closing since is listed in the same refresh as the
// closed items changed since, not as every item; and every item is listed again
// only once the last whole listing is an hour old.
func TestADashboardRefreshListsEveryClosedItemOnlyOnTheFirst(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 10, 24, 19, 13, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	tracker := newChangingTracker(
		beads.WorkItem{ID: "yoyodyne-1", Title: "Queued", Status: "open"},
		beads.WorkItem{ID: "yoyodyne-2", Title: "Being worked", Status: "in_progress"},
		beads.WorkItem{ID: "yoyodyne-3", Title: "Landed", Status: "closed"},
		beads.WorkItem{ID: "yoyodyne-4", Title: "Landed too", Status: "closed"},
	)
	readings := &clockedReadings{now: now}

	refresh := func() map[string]int {
		t.Helper()
		before := tracker.counts()
		dashboardRefresh(t, tracker, readings, now)
		after := tracker.counts()
		made := map[string]int{}
		for kind, n := range after {
			if n != before[kind] {
				made[kind] = n - before[kind]
			}
		}
		return made
	}

	if made := refresh(); !reflect.DeepEqual(made, map[string]int{"every": 1, "ready": 1}) {
		t.Fatalf("the first refresh asked the tracker %v; want one listing of every item and one readiness query", made)
	}
	for round := range 5 {
		clock = clock.Add(sharedReadingFresh + time.Second)
		if made := refresh(); !reflect.DeepEqual(made, map[string]int{"unfinished": 1, "ready": 1}) {
			t.Fatalf("refresh %d asked the tracker %v; want only the unfinished work and the readiness query", round+2, made)
		}
	}

	tracker.set("yoyodyne-1", "closed", clock)
	clock = clock.Add(sharedReadingFresh + time.Second)
	if made := refresh(); !reflect.DeepEqual(made, map[string]int{"unfinished": 1, "closed since": 1, "ready": 1}) {
		t.Fatalf("the refresh after an item closed asked the tracker %v; want the closed items changed since beside the unfinished work", made)
	}
	if item, found := itemIn(readings.every(), "yoyodyne-1"); !found || item.Status != "closed" {
		t.Fatalf("the reading after yoyodyne-1 closed gives it as %#v (found = %v), want it closed", item, found)
	}

	// A later change to work already closed is seen within closedRefresh.
	clock = clock.Add(closedRefresh)
	if made := refresh(); !reflect.DeepEqual(made, map[string]int{"unfinished": 1, "closed since": 1, "ready": 1}) {
		t.Fatalf("the refresh %s on asked the tracker %v; want the closed items changed since beside the unfinished work", closedRefresh, made)
	}

	clock = clock.Add(wholeReadingEvery)
	if made := refresh(); !reflect.DeepEqual(made, map[string]int{"every": 1, "ready": 1}) {
		t.Fatalf("the refresh an hour on asked the tracker %v; want the hourly listing of every item", made)
	}
}

// An item reopened since is listed once, as the unfinished listing gives it,
// and not also as the closed copy carried from before; one closed and listed
// in between is carried closed.
func TestACarriedReadingFollowsItemsReopenedAndClosed(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 10, 24, 19, 13, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	tracker := newChangingTracker(
		beads.WorkItem{ID: "yoyodyne-1", Status: "open"},
		beads.WorkItem{ID: "yoyodyne-2", Status: "closed"},
		beads.WorkItem{ID: "yoyodyne-3", Status: "closed"},
	)
	readings := &clockedReadings{now: now}
	dashboardRefresh(t, tracker, readings, now)

	tracker.set("yoyodyne-2", "open", clock)
	clock = clock.Add(sharedReadingFresh + time.Second)
	dashboardRefresh(t, tracker, readings, now)
	var ids []string
	for _, item := range readings.every() {
		ids = append(ids, item.ID+" "+item.Status)
	}
	slices.Sort(ids)
	if want := []string{"yoyodyne-1 open", "yoyodyne-2 open", "yoyodyne-3 closed"}; !slices.Equal(ids, want) {
		t.Fatalf("the reading after yoyodyne-2 reopened holds %v, want %v", ids, want)
	}
	if counts := tracker.counts(); counts["every"] != 1 {
		t.Fatalf("the tracker was asked %v; want one listing of every item", counts)
	}
}

// The delivery measures and the titles the dashboard names landed work with
// are the same read from a reading that carried its closed items forward as
// from a listing of every item.
func TestClosedWorkFiguresAreUnchangedByCarryingClosedItems(t *testing.T) {
	t.Parallel()
	fixture := deliveryFixture()
	whole, err := fixture.Tracker.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for index := range whole {
		// The merged changes' items are closed; the bugs filed against them
		// are still open.
		whole[index].Status = "closed"
		if whole[index].IssueType == "bug" && whole[index].ID != "yoyodyne-ifd.b" {
			whole[index].Status = "open"
		}
	}
	fixture.Tracker = fakeDeliveryTracker{items: whole}
	listed := ReadDelivery(context.Background(), fixture)
	listedTitles := NewWorkItemTitles(whole)

	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tracker := newChangingTracker(whole...)
	readings := &clockedReadings{now: func() time.Time { return clock }}
	carriedSources := fixture
	carriedSources.Tracker, carriedSources.TrackerReadings = tracker, readings
	ReadDelivery(context.Background(), carriedSources)
	clock = clock.Add(sharedReadingFresh + time.Second)
	carried := ReadDelivery(context.Background(), carriedSources)
	titles, err := ReadWorkItemTitles(context.Background(), Sources{Tracker: tracker, TrackerReadings: readings, Now: fixture.Now})
	if err != nil {
		t.Fatal(err)
	}

	if counts := tracker.counts(); counts["every"] != 1 || counts["unfinished"] != 1 {
		t.Fatalf("the tracker was asked %v; want the second reading to have carried the closed items", counts)
	}
	if !reflect.DeepEqual(carried, listed) {
		t.Fatalf("delivery from the carried reading =\n%+v\nfrom every item =\n%+v", carried, listed)
	}
	for _, item := range whole {
		got, gotKnown := titles.Title(item.ID)
		want, wantKnown := listedTitles.Title(item.ID)
		if got != want || gotKnown != wantKnown {
			t.Fatalf("title of %s = %q (%v) from the carried reading, %q (%v) from every item", item.ID, got, gotKnown, want, wantKnown)
		}
	}
}

func itemIn(items []beads.WorkItem, id string) (beads.WorkItem, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return beads.WorkItem{}, false
}

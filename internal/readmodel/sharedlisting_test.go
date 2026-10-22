package readmodel

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// countingTracker is a tracker that answers as bd does and counts what it is
// asked. Where gate is set, the whole listing and the readiness query each wait
// until the other has started, so a reading that asks for them one after the
// other never gets an answer. Where hold is set, the whole listing also waits
// until the test closes it, so a caller can be made to meet a listing that is
// still being read.
type countingTracker struct {
	every []beads.WorkItem
	ready []beads.WorkItem
	fail  error
	gate  bool
	hold  chan struct{}

	mu       sync.Mutex
	listings []string
	readies  int
	listing  chan struct{}
	readying chan struct{}
}

func newCountingTracker(every, ready []beads.WorkItem) *countingTracker {
	return &countingTracker{every: every, ready: ready, listing: make(chan struct{}), readying: make(chan struct{})}
}

func (c *countingTracker) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	c.mu.Lock()
	c.listings = append(c.listings, status)
	c.mu.Unlock()
	if c.gate {
		close(c.listing)
		select {
		case <-c.readying:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.hold != nil {
		select {
		case <-c.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.fail != nil {
		return nil, c.fail
	}
	var matching []beads.WorkItem
	for _, item := range c.every {
		if status == "" || item.Status == status {
			matching = append(matching, item)
		}
	}
	return matching, nil
}

func (c *countingTracker) Ready(ctx context.Context) ([]beads.WorkItem, error) {
	c.mu.Lock()
	c.readies++
	c.mu.Unlock()
	if c.gate {
		close(c.readying)
		select {
		case <-c.listing:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.fail != nil {
		return nil, c.fail
	}
	return c.ready, nil
}

func sharedListingItems() ([]beads.WorkItem, []beads.WorkItem) {
	open := beads.WorkItem{ID: "yoyodyne-ifd.1", Title: "Open work", Status: "open"}
	blocked := beads.WorkItem{ID: "yoyodyne-ifd.2", Title: "Blocked work", Status: "blocked"}
	claimed := beads.WorkItem{ID: "yoyodyne-ifd.3", Title: "Claimed work", Status: "in_progress"}
	closed := beads.WorkItem{ID: "yoyodyne-ifd.4", Title: "Closed work", Status: "closed"}
	return []beads.WorkItem{open, blocked, claimed, closed}, []beads.WorkItem{open}
}

// A standing asks the tracker for one listing of every item and one readiness
// query, however many lines read the admitted work, and reads the open, the
// blocked, the claimed, and the titles out of that one listing exactly as the
// narrower listings would have answered them.
func TestAStandingAsksTheTrackerForOneListingAndOneReadiness(t *testing.T) {
	t.Parallel()
	every, ready := sharedListingItems()
	tracker := newCountingTracker(every, ready)
	sources := quietSources()
	sources.Tracker = tracker

	standing := ReadStanding(context.Background(), sources)

	if len(tracker.listings) != 1 || tracker.listings[0] != "" || tracker.readies != 1 {
		t.Fatalf("listings = %q, readiness queries = %d; want one listing of every item and one readiness query", tracker.listings, tracker.readies)
	}
	if standing.NotStartableProblem != "" {
		t.Fatalf("not startable problem = %q", standing.NotStartableProblem)
	}
	admitted := make([]string, 0, len(standing.AdmittedItems))
	for _, item := range standing.AdmittedItems {
		admitted = append(admitted, item.WorkItemID)
	}
	if standing.Admitted != 2 || strings.Join(admitted, ",") != "yoyodyne-ifd.1,yoyodyne-ifd.2" {
		t.Fatalf("admitted = %d %q; want the open and the blocked item and neither the claimed nor the closed one", standing.Admitted, admitted)
	}
	for _, item := range every {
		if title, known := standing.Titles.Title(item.ID); !known || title != item.Title {
			t.Errorf("title of %s = %q, %v; want %q from the same listing", item.ID, title, known, item.Title)
		}
	}
}

// The two tracker commands a standing makes are under way together: a tracker
// whose listing does not answer until its readiness query has been asked, and
// the other way round, still answers a standing well inside the bound each
// command is given. Asked one after the other, the first would wait out its
// bound and the admitted work would be unreadable.
func TestAStandingsTwoTrackerCommandsRunTogether(t *testing.T) {
	t.Parallel()
	every, ready := sharedListingItems()
	tracker := newCountingTracker(every, ready)
	tracker.gate = true
	sources := quietSources()
	sources.Tracker = tracker
	sources.TrackerTimeout = 10 * time.Second

	started := time.Now()
	standing := ReadStanding(context.Background(), sources)
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the standing took %s; want its two tracker commands answered together", took)
	}
	if standing.NotStartableProblem != "" || standing.Admitted != 2 {
		t.Fatalf("admitted = %d, problem = %q; want the queue read from the two commands", standing.Admitted, standing.NotStartableProblem)
	}
}

// A whole listing that fails costs every line read from it, each saying why,
// and is not asked again for each of them.
func TestAFailedWholeListingIsSaidOnceAndAskedOnce(t *testing.T) {
	t.Parallel()
	every, ready := sharedListingItems()
	tracker := newCountingTracker(every, ready)
	tracker.fail = errors.New("bd list did not answer within its 30s bound")
	sources := quietSources()
	sources.Tracker = tracker

	standing := ReadStanding(context.Background(), sources)

	if len(tracker.listings) != 1 {
		t.Fatalf("listings = %q; want the failed listing asked once", tracker.listings)
	}
	if !strings.Contains(standing.NotStartableProblem, "the admitted work could not be read") || !strings.Contains(standing.NotStartableProblem, "did not answer within its 30s bound") {
		t.Fatalf("not startable problem = %q; want the failure said", standing.NotStartableProblem)
	}
	if standing.Titles == nil || standing.Titles.Readable() {
		t.Fatalf("titles = %+v; want the unreadable reading from a listing that failed, so items are marked rather than bare", standing.Titles)
	}
}

// A caller whose own context ends while the shared listing is still being read
// stops waiting at once, and a later caller is handed the listing's answer.
func TestAWaiterOnTheSharedListingStopsWithItsOwnContext(t *testing.T) {
	t.Parallel()
	every, ready := sharedListingItems()
	tracker := newCountingTracker(every, ready)
	tracker.gate = true
	tracker.hold = make(chan struct{})
	sources, shared := Sources{Tracker: tracker}.sharing()
	// The shared tracker starts the listing and the readiness query together,
	// so the gate between them opens at once; the hold is what keeps the
	// listing under way until the cancelled waiter has given up on it.
	waiting, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sources.Tracker.List(waiting, "open"); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled waiter got %v; want its own context's end", err)
	}
	close(tracker.hold)
	items, err := shared.List(context.Background(), "open")
	if err != nil || len(items) != 1 || items[0].ID != "yoyodyne-ifd.1" {
		t.Fatalf("open = %+v, %v; want the open item from the listing already under way", items, err)
	}
	if len(tracker.listings) != 1 {
		t.Fatalf("listings = %q; want one", tracker.listings)
	}
}

// slowReadyTracker fails its listing at once and answers its readiness query
// only after a pause, recording when that query ended.
type slowReadyTracker struct {
	mu    sync.Mutex
	ended bool
}

func (s *slowReadyTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return nil, errors.New("bd list failed")
}

func (s *slowReadyTracker) Ready(context.Context) ([]beads.WorkItem, error) {
	time.Sleep(50 * time.Millisecond)
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	return nil, nil
}

// A standing that stops early on a failed listing still waits for the
// readiness query it started beside it, so nothing it started is running — or
// recording how the tracker answered — once it has returned.
func TestAStandingLeavesNoTrackerCommandRunningWhenItReturns(t *testing.T) {
	t.Parallel()
	tracker := &slowReadyTracker{}
	sources := quietSources()
	sources.Tracker = tracker

	ReadStanding(context.Background(), sources)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.ended {
		t.Fatal("the standing returned with its readiness query still running")
	}
}

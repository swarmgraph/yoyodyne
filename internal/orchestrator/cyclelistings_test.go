package orchestrator

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// storeLock stands in for bd's store: every command, a listing or a
// write, takes the one lock the whole store has and holds it while it works,
// as every bd process does. It records each listing against the pull that made
// it, so a test can count what one scheduling cycle asked the tracker for.
type storeLock struct {
	harness *scheduleHarness
	// lock is the store's exclusive lock: one slot, waited for in turn.
	lock chan struct{}
	hold time.Duration

	mu       sync.Mutex
	listings map[int][]string
	// fail is told each listing's command and decides whether it fails, which is
	// a listing that ran out its attempts behind other callers.
	fail func(command string) error
}

func newStoreLock(harness *scheduleHarness, hold time.Duration) *storeLock {
	return &storeLock{harness: harness, lock: make(chan struct{}, 1), hold: hold, listings: map[int][]string{}}
}

// take waits for the store's lock, or for the caller's limit, and holds it for
// as long as one command works.
func (s *storeLock) take(ctx context.Context) (func(), error) {
	select {
	case s.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	time.Sleep(s.hold)
	return func() { <-s.lock }, nil
}

func (s *storeLock) listing(ctx context.Context, command string) (func(), error) {
	s.harness.mu.Lock()
	pull := s.harness.pulls
	s.harness.mu.Unlock()
	s.mu.Lock()
	s.listings[pull] = append(s.listings[pull], command)
	fail := s.fail
	s.mu.Unlock()
	release, err := s.take(ctx)
	if err != nil {
		return nil, err
	}
	if fail != nil {
		if err := fail(command); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

func (s *storeLock) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	release, err := s.listing(ctx, "list "+status)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.harness.List(ctx, status)
}

func (s *storeLock) Ready(ctx context.Context) ([]beads.WorkItem, error) {
	release, err := s.listing(ctx, "ready")
	if err != nil {
		return nil, err
	}
	defer release()
	return s.harness.Ready(ctx)
}

// write is a tracker write made beside the scheduler, under its own limit.
func (s *storeLock) write(ctx context.Context) error {
	release, err := s.take(ctx)
	if err != nil {
		return err
	}
	release()
	return nil
}

func (s *storeLock) madeBy() map[int][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	made := make(map[int][]string, len(s.listings))
	for pull, listings := range s.listings {
		made[pull] = append([]string(nil), listings...)
	}
	return made
}

// severalStatusesStore is the store reached through a tracker that lists
// several statuses in one command, as beads.Client does.
type severalStatusesStore struct{ *storeLock }

func (s severalStatusesStore) ListStatuses(ctx context.Context, statuses []string) ([]beads.WorkItem, error) {
	release, err := s.listing(ctx, "list "+strings.Join(statuses, ","))
	if err != nil {
		return nil, err
	}
	defer release()
	every, err := s.harness.List(ctx, "")
	if err != nil {
		return nil, err
	}
	var matching []beads.WorkItem
	for _, item := range every {
		if slices.Contains(statuses, item.Status) {
			matching = append(matching, item)
		}
	}
	return matching, nil
}

// cycleListingWork is a queue with an item of each status a pull reads and one
// it must not: nothing is ready, so every poll reads the tracker and starts
// nothing, and the session goes straight on to its next poll.
func cycleListingWork() []beads.WorkItem {
	return []beads.WorkItem{
		{ID: "yoyodyne-open", Title: "open", Status: "open", Priority: 2},
		{ID: "yoyodyne-blocked", Title: "blocked", Status: "blocked", Priority: 2},
		{ID: "yoyodyne-claimed", Title: "claimed", Status: "in_progress", Priority: 2},
		{ID: "yoyodyne-closed", Title: "closed", Status: "closed", Priority: 2},
	}
}

// TestRepeatedSchedulingCyclesLetATrackerWriteThroughWithinItsLimit runs a
// watch through poll after poll while tracker writes are made beside it against
// the same store lock, and holds each poll to its listings: one command for
// every status it reads where the tracker can list several at once, else one
// per status, and one readiness query. Both the claim audit and the queue read
// the claimed items, and the early ranking and the queue read the queue, so a
// pull that asked for each itself would make more.
func TestRepeatedSchedulingCyclesLetATrackerWriteThroughWithinItsLimit(t *testing.T) {
	const polls = 12
	const hold = 2 * time.Millisecond
	// The write's own limit, as a tracker call has one. It is many commands long,
	// so a loaded machine does not fail it, and far shorter than the session,
	// so a pull that kept the store for the whole of its cycle would.
	const writeLimit = time.Second

	for _, tc := range []struct {
		name    string
		tracker func(*storeLock) ScheduleTracker
		want    []string
	}{
		{
			name:    "several statuses in one command",
			tracker: func(s *storeLock) ScheduleTracker { return severalStatusesStore{s} },
			want:    []string{"list open,blocked,in_progress", "ready"},
		},
		{
			name:    "one status at a time",
			tracker: func(s *storeLock) ScheduleTracker { return s },
			want:    []string{"list blocked", "list in_progress", "list open", "ready"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness := newScheduleHarness(cycleListingWork()...)
			for id := range harness.ReadyItems {
				harness.ReadyItems[id] = false
			}
			var audited [][]string
			harness.audit = func(h *scheduleHarness, claimed []beads.WorkItem) (ClaimSweep, error) {
				var ids []string
				for _, item := range claimed {
					ids = append(ids, item.ID)
				}
				audited = append(audited, ids)
				return ClaimSweep{}, nil
			}
			harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < polls }
			store := newStoreLock(harness, hold)
			tracker := tc.tracker(store)

			stop := make(chan struct{})
			var writes int
			var slowest time.Duration
			var writeErrs []error
			var writer sync.WaitGroup
			writer.Add(1)
			go func() {
				defer writer.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					ctx, cancel := context.WithTimeout(context.Background(), writeLimit)
					started := time.Now()
					err := store.write(ctx)
					cancel()
					writes++
					slowest = max(slowest, time.Since(started))
					if err != nil {
						writeErrs = append(writeErrs, err)
					}
				}
			}()

			schedule, err := Scheduler{
				Open: func(ctx context.Context) (Pull, error) {
					pull, err := harness.open(ctx)
					pull.Tracker = tracker
					return pull, err
				},
				Watching: true, Sleep: harness.sleep, Sessions: &recordedSessions{},
			}.Schedule(context.Background())
			close(stop)
			writer.Wait()
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if schedule.Polls != polls || !schedule.BacklogRead || schedule.Admitted != 2 {
				t.Fatalf("schedule = %d poll(s), backlog read %v, %d admitted; want %d polls each reading the open and the blocked item",
					schedule.Polls, schedule.BacklogRead, schedule.Admitted, polls)
			}

			if writes == 0 {
				t.Fatal("no tracker write was made beside the scheduler")
			}
			if len(writeErrs) > 0 {
				t.Fatalf("%d of %d tracker writes beside the scheduler did not finish inside their %s limit (slowest %s): %v",
					len(writeErrs), writes, writeLimit, slowest, writeErrs[0])
			}

			made := store.madeBy()
			if len(made) != polls {
				t.Fatalf("listings were made in %d pull(s), want every one of the %d: %v", len(made), polls, made)
			}
			for pull, listings := range made {
				got := slices.Clone(listings)
				slices.Sort(got)
				if !slices.Equal(got, tc.want) {
					t.Fatalf("pull %d asked the tracker for %v, want each listing it needs once: %v", pull, listings, tc.want)
				}
			}
			// The claimed items the audit was handed came out of the shared
			// listing, and are exactly the claimed ones.
			for _, ids := range audited {
				if !slices.Equal(ids, []string{"yoyodyne-claimed"}) {
					t.Fatalf("the claim audit was handed %v, want only the claimed item", ids)
				}
			}
			if len(audited) != polls {
				t.Fatalf("the claim audit ran %d time(s), want once a poll over %d polls", len(audited), polls)
			}
		})
	}
}

// A listing that failed is the answer for the rest of its pull: the queue is
// not read again behind the audit's failure, and the session reads again with
// a new pull after its wait.
func TestAFailedListingIsNotAskedAgainInTheSamePull(t *testing.T) {
	harness := newScheduleHarness(cycleListingWork()...)
	harness.audit = func(*scheduleHarness, []beads.WorkItem) (ClaimSweep, error) { return ClaimSweep{}, nil }
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }
	store := newStoreLock(harness, 0)
	failed := false
	store.fail = func(command string) error {
		if strings.HasPrefix(command, "list ") && !failed {
			failed = true
			return errors.New("bd list did not answer within its 30s bound on any of 3 attempts")
		}
		return nil
	}
	tracker := severalStatusesStore{store}

	schedule, err := Scheduler{
		Open: func(ctx context.Context) (Pull, error) {
			pull, err := harness.open(ctx)
			pull.Tracker = tracker
			return pull, err
		},
		Watching: true, Sleep: harness.sleep, Sessions: &recordedSessions{},
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-open" {
		t.Fatalf("started = %#v, want the open item pulled once the tracker answered", schedule.Started)
	}
	made := store.madeBy()
	if got := made[1]; !slices.Equal(got, []string{"list open,blocked,in_progress"}) {
		t.Fatalf("the pull whose listing failed asked the tracker for %v, want the one listing and nothing after it", got)
	}
	for pull, listings := range made {
		if pull > 1 && len(listings) > 2 {
			t.Fatalf("pull %d asked the tracker for %v, want at most one listing and one readiness query", pull, listings)
		}
	}
}

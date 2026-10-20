package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func newTrackerReadingStore(t *testing.T) *TrackerReadingStore {
	t.Helper()
	store, err := NewTrackerReadingStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A reading begun within the freshness asked for is handed over as it was
// kept, failures and all; an older one is made again.
func TestASharedTrackerReadingIsHandedOverWhileFresh(t *testing.T) {
	t.Parallel()
	store := newTrackerReadingStore(t)
	clock := time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return clock }
	reads := 0
	read := func() (TrackerReading, bool) {
		reads++
		return TrackerReading{
			Every:        []beads.WorkItem{{ID: "yoyodyne-1", Status: "open"}},
			ReadyFailure: "bd ready did not answer within its 30s bound",
		}, true
	}

	first, err := store.Share(context.Background(), 10*time.Second, read)
	if err != nil || reads != 1 || !first.StartedAt.Equal(clock) {
		t.Fatalf("first reading = %+v, %v after %d read(s); want one made now", first, err, reads)
	}
	clock = clock.Add(9 * time.Second)
	again, err := store.Share(context.Background(), 10*time.Second, read)
	if err != nil || reads != 1 {
		t.Fatalf("reading 9s later = %v after %d read(s); want the kept one", err, reads)
	}
	if len(again.Every) != 1 || again.Every[0].ID != "yoyodyne-1" || again.ReadyFailure != first.ReadyFailure {
		t.Fatalf("kept reading = %+v; want what was read, the failure included", again)
	}
	clock = clock.Add(2 * time.Second)
	if _, err := store.Share(context.Background(), 10*time.Second, read); err != nil || reads != 2 {
		t.Fatalf("reading 11s after the first = %v after %d read(s); want a new one", err, reads)
	}
}

// A reading its own caller abandoned is handed to that caller and kept for
// nobody else.
func TestAnAbandonedTrackerReadingIsNotKept(t *testing.T) {
	t.Parallel()
	store := newTrackerReadingStore(t)
	reads := 0
	read := func(keep bool) func() (TrackerReading, bool) {
		return func() (TrackerReading, bool) {
			reads++
			return TrackerReading{EveryFailure: "context canceled"}, keep
		}
	}
	if reading, err := store.Share(context.Background(), time.Minute, read(false)); err != nil || reading.EveryFailure != "context canceled" {
		t.Fatalf("abandoned reading = %+v, %v; want it handed to its caller", reading, err)
	}
	if _, err := store.Share(context.Background(), time.Minute, read(true)); err != nil || reads != 2 {
		t.Fatalf("reading after an abandoned one = %v after %d read(s); want a new one", err, reads)
	}
}

// A caller that arrives while a reading is being made waits for it and is
// handed it, rather than reading the tracker behind it: the reading began no
// earlier than the caller asked, so it holds every write finished before the
// ask even where no age at all is allowed.
func TestACallerQueuedBehindAReadingIsHandedIt(t *testing.T) {
	t.Parallel()
	store := newTrackerReadingStore(t)
	// Both callers ask at the moment the reading begins.
	clock := time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return clock }
	var mu sync.Mutex
	reads := 0
	making, release := make(chan struct{}), make(chan struct{})
	read := func() (TrackerReading, bool) {
		mu.Lock()
		reads++
		first := reads == 1
		mu.Unlock()
		if first {
			close(making)
			<-release
		}
		return TrackerReading{Every: []beads.WorkItem{{ID: "yoyodyne-1"}}}, true
	}
	done := make(chan error, 2)
	go func() {
		_, err := store.Share(context.Background(), 0, read)
		done <- err
	}()
	<-making
	go func() {
		reading, err := store.Share(context.Background(), 0, read)
		if err == nil && (len(reading.Every) != 1 || reading.Every[0].ID != "yoyodyne-1") {
			err = errors.New("the queued caller was not handed the reading")
		}
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if reads != 1 {
		t.Fatalf("reads = %d; want the queued caller handed the reading it waited for", reads)
	}
}

// A caller whose wait for the lock ends is told so and reads nothing.
func TestAWaitForTheSharedReadingEndsWithItsContext(t *testing.T) {
	t.Parallel()
	store := newTrackerReadingStore(t)
	holding, release, held := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(held)
		_, _ = store.Share(context.Background(), 0, func() (TrackerReading, bool) {
			close(holding)
			<-release
			return TrackerReading{}, true
		})
	}()
	<-holding
	// The holder keeps its reading once it is let go, so the test waits for it
	// to finish writing before its directory is removed.
	defer func() {
		close(release)
		<-held
	}()
	waiting, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	read := false
	_, err := store.Share(waiting, time.Minute, func() (TrackerReading, bool) { read = true; return TrackerReading{}, true })
	if err == nil || read {
		t.Fatalf("a wait that ended = %v, read = %v; want it refused without reading", err, read)
	}
}

// What a kept reading carries is every field a listing decoded, so a status
// reading answered from it says what one answered from the tracker says.
func TestASharedTrackerReadingKeepsEveryFieldOfAnItem(t *testing.T) {
	t.Parallel()
	store := newTrackerReadingStore(t)
	at := time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	item := beads.WorkItem{
		RelevantGoals:      []string{"v1-goals#1"},
		SchedulingWait:     &beads.SchedulingWait{Class: "capacity", Reason: "no slot", FirstPassedOver: at, ReasonSince: at},
		ID:                 "yoyodyne-1",
		Title:              "Title",
		Description:        "Description",
		Design:             "Design",
		AcceptanceCriteria: "Criteria",
		Notes:              "Goal served: something",
		Status:             "open",
		Priority:           1,
		IssueType:          "task",
		Assignee:           "someone",
		Parent:             "yoyodyne",
		Dependencies:       []beads.Dependency{{IssueID: "yoyodyne-1", ID: "yoyodyne-2", Type: "blocks", Status: "open", Title: "Other", CreatedAt: at}},
		CreatedAt:          at,
		ClosedAt:           at.Add(time.Hour),
		CloseReason:        "landed",
		Cost:               &beads.Cost{TotalUSD: 1.5, Runs: 2, UnknownRuns: 1},
		GoalWitness:        goal.Witness{Recorded: true, Statement: "something"},
		Labels:             []string{"reliability"},
		Landing:            "document@2026-10-19",
	}
	if _, err := store.Share(context.Background(), time.Minute, func() (TrackerReading, bool) {
		return TrackerReading{Every: []beads.WorkItem{item}, Ready: []beads.WorkItem{item}}, true
	}); err != nil {
		t.Fatal(err)
	}
	kept, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(item)
	for _, got := range [][]beads.WorkItem{kept.Every, kept.Ready} {
		if len(got) != 1 {
			t.Fatalf("kept = %+v", got)
		}
		encoded, _ := json.Marshal(got[0])
		if string(encoded) != string(want) {
			t.Fatalf("kept item = %s\nwant %s", encoded, want)
		}
	}
}

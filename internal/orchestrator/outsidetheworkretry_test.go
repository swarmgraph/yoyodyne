package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// settingsCheckTimedOut is the failure the pre-start settings check reports when
// Claude Code does not answer, marked the way requireLaunchSettings marks it.
func settingsCheckTimedOut() error {
	return fmt.Errorf("check what the developer's provider applies: %w",
		refusedByEnvironment("whether the developer's provider applies its sandbox and guard could not be checked",
			errors.New("ask Claude Code which settings it applies: the process timed_out")))
}

// busySession is a watch session whose one developer slot never sits idle long
// enough for a poll interval to pass with nothing finishing: every pull admits a
// fresh item that finishes at once, and the clock moves twenty seconds a pull.
// first is the item at the head of the queue, whose first start fails with
// failure; every later start of anything succeeds.
func busySession(t *testing.T, failure error) (*scheduleHarness, *recordedSessions) {
	t.Helper()
	items := readyItems("yoyodyne-first")
	items[0].Priority = 0
	harness := newScheduleHarness(items...)
	harness.poll = time.Minute
	failed := false
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		fail := id == "yoyodyne-first" && !failed
		if fail {
			failed = true
		}
		h.mu.Unlock()
		if fail {
			return Outcome{}, failure
		}
		return h.complete(id), nil
	}
	harness.onPull = func(h *scheduleHarness, pulls int) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.now = h.now.Add(20 * time.Second)
		if pulls < 12 {
			id := fmt.Sprintf("yoyodyne-busy%02d", pulls)
			h.Items = append(h.Items, readyItems(id)...)
			h.ReadyItems[id] = true
		}
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	return harness, &recordedSessions{}
}

func startsOf(harness *scheduleHarness, id string) int {
	starts := 0
	for _, started := range harness.pullOrder() {
		if started == id {
			starts++
		}
	}
	return starts
}

// A start the settings check refused because the check timed out is tried again
// once a poll interval has passed, even in a session busy enough that no poll
// interval ever passes with nothing finishing. Before, such a session held the
// item out until it restarted.
func TestAStartRefusedOutsideTheWorkIsTriedAgainInABusySession(t *testing.T) {
	t.Parallel()

	harness, sessions := busySession(t, settingsCheckTimedOut())
	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}.
		Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := startsOf(harness, "yoyodyne-first"); starts != 2 {
		t.Fatalf("yoyodyne-first was started %d time(s) (%v), want it tried again once a poll interval had passed", starts, harness.pullOrder())
	}
	if schedule.BlockedInARow != 0 || schedule.Braked != nil {
		t.Fatalf("blocked in a row = %d, braked = %#v, want a refusal outside the work to count toward neither", schedule.BlockedInARow, schedule.Braked)
	}
}

// A start that failed on the item itself is still held until the item changes,
// however long the busy session goes on, so it is not retried in a loop.
func TestAStartThatFailedOnTheItemIsNotTriedAgainInABusySession(t *testing.T) {
	t.Parallel()

	harness, sessions := busySession(t, errors.New("validate work item: its design document is missing"))
	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}).
		Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := startsOf(harness, "yoyodyne-first"); starts != 1 {
		t.Fatalf("yoyodyne-first was started %d time(s) (%v), want it held until the item changes", starts, harness.pullOrder())
	}
	idle, found := lastEntered(sessions, runstate.WatchIdle)
	if !found {
		t.Fatal("no idle transition was recorded")
	}
	if !strings.Contains(idle.reason, "yoyodyne-first — this session tried it and the dispatch failed before any run was recorded; held until the item changes") {
		t.Fatalf("idle reason = %q, want the item's own failure named as held until the item changes", idle.reason)
	}
}

// While an item waits out a refusal outside the work, the status says so, and
// says it is tried again, rather than reading as a failure held against it.
func TestStatusSaysAStartRefusedOutsideTheWorkIsNotHeldAgainstTheItem(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-first")...)
	harness.run = func(*scheduleHarness, string) (Outcome, error) { return Outcome{}, settingsCheckTimedOut() }
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	sessions := &recordedSessions{}
	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}).
		Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	waiting, found := lastEntered(sessions, runstate.WatchBlocked)
	if !found {
		t.Fatal("no blocked transition was recorded")
	}
	groups := waiting.passedOver.Groups
	if len(groups) != 1 || groups[0].Class != runstate.PassedOverAlreadyTried || len(groups[0].Reasons) != 1 {
		t.Fatalf("passed over = %+v, want the one item passed over with a reason", waiting.passedOver)
	}
	reason := groups[0].Reasons[0]
	for _, want := range []string{
		"a check outside the work refused the start before any run was recorded, so nothing is held against the item and it is tried again once a poll interval has passed",
		"check what the developer's provider applies",
	} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason = %q, want it to say %q", reason, want)
		}
	}
	if strings.Contains(reason, "until the item changes") {
		t.Fatalf("reason = %q, want nothing saying the item is held until it changes", reason)
	}
}

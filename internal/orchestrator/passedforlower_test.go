package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An item this session already tried is passed over for lower work at every
// later pull, and the reason it is passed over is kept on the item, so status
// can say it whether or not a slot then stands empty. Two ways a session comes
// to have tried it: a run that ended without finishing the item, and a start a
// check before the run refused.
func TestAnItemAlreadyTriedKeepsWhyLowerWorkWentAheadOfIt(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		outcome Outcome
		failure string
		want    string
	}{
		"a run that ended":          {Outcome{RunID: "run-first", Status: runstate.StatusFailed}, "its checks failed", "run run-first failed: its checks failed"},
		"a refused pre-start check": {Outcome{}, "the item names a condition nobody has granted", "the dispatch failed before any run was recorded"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			items := readyItems("yoyodyne-first", "yoyodyne-second")
			items[0].Priority = 0
			harness := newScheduleHarness(items...)
			harness.capacity = 1
			harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
				h.mu.Lock()
				delete(h.inFlight, id)
				h.mu.Unlock()
				if id == "yoyodyne-first" {
					return test.outcome, errors.New(test.failure)
				}
				return Outcome{RunID: "run-" + id, Status: runstate.StatusSucceeded}, nil
			}
			harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
				h.mu.Lock()
				defer h.mu.Unlock()
				_, second := h.selections["yoyodyne-second"]
				return !second && sleeps < 200
			}
			if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}).Schedule(context.Background()); err != nil {
				t.Fatal(err)
			}
			harness.mu.Lock()
			defer harness.mu.Unlock()
			if _, second := harness.selections["yoyodyne-second"]; !second {
				t.Fatalf("selections = %v; want the lower item started", harness.selections)
			}
			wait := harness.Items[0].SchedulingWait
			if wait == nil || wait.Class != string(runstate.PassedOverAlreadyTried) ||
				!strings.Contains(wait.Reason, "already tried by this session") || !strings.Contains(wait.Reason, test.want) || !strings.Contains(wait.Reason, test.failure) {
				t.Fatalf("wait = %+v; want the session's attempt kept as why the item was passed over, saying %q", wait, test.want)
			}
		})
	}
}

// What a session kept about its own attempts is cleared by the next session,
// which holds no such attempt, and an epic refusal is cleared once the epics
// no longer refuse the item — whether or not a slot reaches the item.
func TestClearLapsedWaitsClearsOnlyCausesThatHaveGone(t *testing.T) {
	t.Parallel()
	entry := func(id string, class runstate.PassedOverClass) backlog.Entry {
		return backlog.Entry{ID: id, Ready: true, SchedulingWait: &beads.SchedulingWait{Class: string(class), Reason: "kept"}}
	}
	entries := []backlog.Entry{
		entry("yoyodyne-tried-before", runstate.PassedOverAlreadyTried),
		entry("yoyodyne-tried-now", runstate.PassedOverAlreadyTried),
		entry("yoyodyne-added", runstate.PassedOverOutsideNamedEpics),
		entry("yoyodyne-outside", runstate.PassedOverOutsideNamedEpics),
		entry("yoyodyne-sequenced", runstate.PassedOverSequencedBehindWork),
		{ID: "yoyodyne-nothing", Ready: true},
	}
	cleared := map[string]bool{}
	clearLapsedWaits(entries,
		func(id string) bool { return id == "yoyodyne-tried-now" },
		func(id string) bool { return id == "yoyodyne-outside" },
		func(id string, _ runstate.PassedOverClass, reason string) {
			if reason != "" {
				t.Fatalf("kept %q on %s; want only clearing", reason, id)
			}
			cleared[id] = true
		})
	if len(cleared) != 2 || !cleared["yoyodyne-tried-before"] || !cleared["yoyodyne-added"] {
		t.Fatalf("cleared = %v; want the attempt no longer held and the item the epics now take", cleared)
	}
}

// Only an epic refusal ahead of the last item the pull started is kept: one
// behind it was not passed over for anything lower.
func TestKeepPassedForLowerKeepsOnlyWhatLowerWorkWentAheadOf(t *testing.T) {
	t.Parallel()
	entries := []backlog.Entry{{ID: "yoyodyne-a"}, {ID: "yoyodyne-b"}, {ID: "yoyodyne-c"}, {ID: "yoyodyne-d"}}
	refused := map[string]string{"yoyodyne-a": "outside a", "yoyodyne-d": "outside d"}
	kept := map[string]string{}
	keep := func(id string, class runstate.PassedOverClass, reason string) {
		if class != runstate.PassedOverOutsideNamedEpics {
			t.Fatalf("class = %q", class)
		}
		kept[id] = reason
	}
	keepPassedForLower(entries, map[string]bool{"yoyodyne-c": true}, refused, keep)
	if len(kept) != 1 || kept["yoyodyne-a"] != "outside a" {
		t.Fatalf("kept = %v; want only the refusal ahead of the started item", kept)
	}
	kept = map[string]string{}
	keepPassedForLower(entries, nil, refused, keep)
	if len(kept) != 0 {
		t.Fatalf("kept = %v; want nothing kept where the pull started nothing", kept)
	}
}

// The kept reason says what keeps the item out: the session's memory of the
// attempt for one held against the item, and the refusal's own words, which
// already say when it is tried again, for a start a check outside the work
// refused or a usage limit holds.
func TestTriedReasonSaysWhatKeepsTheItemOut(t *testing.T) {
	t.Parallel()
	held := triedReason(runstate.PassedOverAlreadyTried, attempt{reason: "run run-1 failed"})
	if !strings.HasPrefix(held, "already tried by this session") || !strings.HasSuffix(held, "run run-1 failed") {
		t.Fatalf("held = %q; want the session's attempt said with its reason", held)
	}
	retried := triedReason(runstate.PassedOverAlreadyTried, attempt{reason: "tried again once a poll interval has passed", retryAtPoll: true})
	if retried != "tried again once a poll interval has passed" {
		t.Fatalf("retried = %q; want the refusal's own words, which say it is tried again", retried)
	}
	window := triedReason(runstate.PassedOverWaitingOnUsageWindow, attempt{reason: "until 14:00 PST"})
	if window != "until 14:00 PST" {
		t.Fatalf("window = %q; want the usage limit's own words", window)
	}
}

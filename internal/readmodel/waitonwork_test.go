package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A stoppage the development manager decided to wait on named work for is not
// one awaiting her decision: she has decided, and what moves next is that work
// landing. `yoyo status` counts it apart for as long as the work is unfinished,
// and as awaiting her decision again once it is closed (yoyodyne-ifd.428.84).
func TestAStoppageWaitingOnNamedWorkIsCountedApartFromOnesAwaitingADecision(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	waiting := heldFor("run-0123456789abcdef0123456789abcdef", "the run stopped", decidedStanding{waitsOn: "yoyodyne-recovery"}, stopped)
	if waiting.WaitsOn != "yoyodyne-recovery" || waiting.Decided || !strings.Contains(waiting.Reason, "wait on yoyodyne-recovery") {
		t.Fatalf("hold = %#v, want it naming the work the wait is on", waiting)
	}
	undecided := heldFor("run-fedcba9876543210fedcba9876543210", "the run stopped", decidedStanding{}, stopped)

	holds := backlog.ReadHolds(map[string]backlog.Hold{"yoyodyne-waited": waiting, "yoyodyne-undecided": undecided})
	queue := backlog.Order([]beads.WorkItem{
		{ID: "yoyodyne-waited", Title: "waited", Status: "blocked"},
		{ID: "yoyodyne-undecided", Title: "undecided", Status: "blocked"},
		{ID: "yoyodyne-recovery", Title: "the recovery", Status: "open"},
	}, []string{"yoyodyne-recovery"}, holds, nil)

	count := func(unfinished map[string]bool) heldWork {
		work := heldWork{unfinished: unfinished}
		for _, entry := range queue.Entries {
			work.count(entry)
		}
		return work
	}
	open := count(map[string]bool{"yoyodyne-recovery": true})
	if open.awaitingWork != 1 || open.awaitingDecision != 1 {
		t.Fatalf("while the work is open: awaiting work %d, decision %d; want 1 and 1", open.awaitingWork, open.awaitingDecision)
	}
	closed := count(map[string]bool{})
	if closed.awaitingWork != 0 || closed.awaitingDecision != 2 {
		t.Fatalf("once the work is closed: awaiting work %d, decision %d; want 0 and 2", closed.awaitingWork, closed.awaitingDecision)
	}

	split := Standing{AwaitingDecision: 1, AwaitingWork: 1}.heldSplit()
	if !strings.Contains(split, "1 awaits the development manager's decision") ||
		!strings.Contains(split, "1 awaits admitted work the development manager decided to wait for") {
		t.Fatalf("heldSplit() = %q, want the two counted apart", split)
	}
}

// trackerOf answers for the work items it holds, and fails for every other.
type trackerOf map[string]beads.WorkItem

func (t trackerOf) Show(_ context.Context, id string) (beads.WorkItem, error) {
	item, found := t[id]
	if !found {
		return beads.WorkItem{}, errors.New("bd show " + id + ": database is locked")
	}
	return item, nil
}

// A wait on named work ends when that work closes, and nothing has to record
// it ending: the hold asks the tracker as it is read. The yoyodyne-ifd.435.20
// shape is the one tested, a wait whose work closed two weeks before the hold
// was read; a wait whose work cannot be asked about says so rather than that it
// still waits, and a wait on open work reads as it always did.
func TestAWaitOnNamedWorkEndsWhenTheTrackerHoldsItClosed(t *testing.T) {
	t.Parallel()

	const item, waitsOn = "yoyodyne-ifd.435.20", "yoyodyne-ifd.435.20.1"
	stoppedAt := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 10, 17, 18, 30, 0, 0, time.UTC)
	stopped := preservedRun("run-a55ad7d7", item, stoppedAt)
	record := recordedDecisions{item: {WorkItemID: item, Decisions: []runstate.TriageDecision{
		{Decision: runstate.TriageDecisionWait, RunID: stopped.RunID, DecidedAt: stoppedAt.Add(time.Hour), WaitsOn: waitsOn},
	}}}
	hold := func(t *testing.T, waited WaitedOn) (string, backlog.Holds) {
		t.Helper()
		held, err := HeldForAPerson(context.Background(), fakeStoppages{runs: []runstate.State{stopped}}, record, nil, waited)
		if err != nil {
			t.Fatalf("HeldForAPerson() error = %v", err)
		}
		return heldReason(t, held, item), held
	}
	counted := func(held backlog.Holds) heldWork {
		queue := backlog.Order([]beads.WorkItem{{ID: item, Title: "the loopback sandbox", Status: "blocked"}}, nil, held, nil)
		// The status reading counts a wait as waiting while its work is among the
		// unfinished; the work is passed as unfinished here so the count turns on
		// what the hold says alone.
		work := heldWork{unfinished: map[string]bool{waitsOn: true}}
		for _, entry := range queue.Entries {
			work.count(entry)
		}
		return work
	}

	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		reason, held := hold(t, trackerOf{waitsOn: {ID: waitsOn, Status: "closed", ClosedAt: closedAt}})
		for _, want := range []string{waitsOn + ", the work the development manager was waiting on, closed " + localMoment(closedAt),
			"that wait is over and the stoppage needs her decision", awaitingDecisionClause} {
			if !strings.Contains(reason, want) {
				t.Fatalf("held for %q, want it to say %q", reason, want)
			}
		}
		if strings.Contains(reason, "decided to wait on") {
			t.Fatalf("held for %q, want it no longer saying she decided to wait", reason)
		}
		if held.Decided(item) {
			t.Fatalf("held for %q as the harness's move, want it hers", reason)
		}
		if work := counted(held); work.awaitingDecision != 1 || work.awaitingWork != 0 {
			t.Fatalf("awaiting decision %d, work %d; want the stoppage counted as owing her a decision", work.awaitingDecision, work.awaitingWork)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()
		reason, _ := hold(t, trackerOf{})
		for _, want := range []string{"whether " + waitsOn + " is closed could not be read", "database is locked", "cannot say whether that wait still stands"} {
			if !strings.Contains(reason, want) {
				t.Fatalf("held for %q, want it to say %q", reason, want)
			}
		}
		if strings.Contains(reason, "once it is closed the stoppage is back on her docket") {
			t.Fatalf("held for %q, want it not saying the stoppage still waits", reason)
		}
	})
	t.Run("not wired", func(t *testing.T) {
		t.Parallel()
		if reason, _ := hold(t, nil); !strings.Contains(reason, "nothing was wired to read the tracker") {
			t.Fatalf("held for %q, want it saying nothing could ask", reason)
		}
	})
	t.Run("open", func(t *testing.T) {
		t.Parallel()
		reason, held := hold(t, trackerOf{waitsOn: {ID: waitsOn, Status: "in_progress"}})
		if !strings.HasSuffix(reason, "; "+waitStands(waitsOn)) {
			t.Fatalf("held for %q, want it closing on the wait as before", reason)
		}
		if work := counted(held); work.awaitingWork != 1 || work.awaitingDecision != 0 {
			t.Fatalf("awaiting decision %d, work %d; want the stoppage counted as waiting", work.awaitingDecision, work.awaitingWork)
		}
	})
}

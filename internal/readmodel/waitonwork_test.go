package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
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

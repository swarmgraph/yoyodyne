package backlog

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
)

func TestOrderIsPriorityFirstAndHoldsWhatTheProductManagerDidNotDecide(t *testing.T) {
	t.Parallel()

	// The tracker's own order is deliberately not the backlog's: two items at the
	// same priority with no admission time arrive in the order bd listed them and
	// stay in it, because nothing has decided between them.
	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.26", Title: "See and stop what is pulled", Status: statusOpen, Priority: 2},
		{ID: "yoyodyne-ifd.3", Title: "The scheduler that runs it", Status: statusOpen, Priority: 0},
		{ID: "yoyodyne-ifd.25", Title: "Approval moves up to the goals", Status: statusOpen, Priority: 2},
		{ID: "yoyodyne-ifd.4", Title: "The development manager that pulls", Status: statusOpen, Priority: 1},
	}, []string{"yoyodyne-ifd.26", "yoyodyne-ifd.3", "yoyodyne-ifd.25", "yoyodyne-ifd.4"}, ReadHolds(nil), nil)

	var order []string
	for _, entry := range queue.Entries {
		order = append(order, entry.ID)
	}
	want := []string{"yoyodyne-ifd.3", "yoyodyne-ifd.4", "yoyodyne-ifd.26", "yoyodyne-ifd.25"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
	// Positions are the backlog's own numbering, counting from one.
	for i, entry := range queue.Entries {
		if entry.Position != i+1 {
			t.Fatalf("entry %s is at position %d, want %d", entry.ID, entry.Position, i+1)
		}
	}
	next, ok := queue.Next()
	if !ok || next.ID != "yoyodyne-ifd.3" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
}

func TestEqualPriorityIsTakenOldestAdmittedFirst(t *testing.T) {
	t.Parallel()

	// bd lists newest first. Taken as the order, that put every priority-0 item
	// admitted after the red-check misfiling fix (yoyodyne-c02) ahead of it, on
	// 2026-09-29 and 30, for as long as priority-0 work kept arriving. An item
	// the tracker gave no admission time goes after the ones it did.
	at := func(clock string) time.Time {
		parsed, err := time.Parse(time.RFC3339, clock)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	listed := []beads.WorkItem{
		{ID: "yoyodyne-ifd.432.25.1", Status: statusOpen, Priority: 0, CreatedAt: at("2026-09-30T15:52:02Z")},
		{ID: "yoyodyne-ifd.271", Status: statusOpen, Priority: 3, CreatedAt: at("2026-09-03T21:18:06Z")},
		{ID: "yoyodyne-unknown", Status: statusOpen, Priority: 0},
		{ID: "yoyodyne-ifd.428.58", Status: statusOpen, Priority: 0, CreatedAt: at("2026-09-30T01:57:08Z")},
		{ID: "yoyodyne-c02", Status: statusOpen, Priority: 0, CreatedAt: at("2026-09-29T15:04:46Z")},
	}
	var ready []string
	for _, item := range listed {
		ready = append(ready, item.ID)
	}
	queue := Order(listed, ready, ReadHolds(nil), nil)

	var order []string
	for _, entry := range queue.Entries {
		order = append(order, entry.ID)
	}
	want := []string{"yoyodyne-c02", "yoyodyne-ifd.428.58", "yoyodyne-ifd.432.25.1", "yoyodyne-unknown", "yoyodyne-ifd.271"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-c02" {
		t.Fatalf("Next() = %#v, %v, want the oldest priority-0 item", next, ok)
	}
}

func TestOnlyAdmittedUnfinishedWorkIsInTheBacklog(t *testing.T) {
	t.Parallel()

	// Work somebody is already doing has been pulled, and closed work has left
	// the backlog whether it was finished or retired. Neither is still queued.
	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-1", Title: "Being worked on", Status: "in_progress", Priority: 0},
		{ID: "yoyodyne-2", Title: "Finished", Status: "closed", Priority: 0},
		{ID: "yoyodyne-3", Title: "Admitted and waiting", Status: statusOpen, Priority: 3},
		{
			ID: "yoyodyne-4", Title: "Admitted and blocked", Status: statusBlocked, Priority: 1,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-3", Type: beads.BlocksDependency, Status: statusOpen}},
		},
		// A tracker that reports a claimed or closed item as ready cannot put it
		// back in the queue: what has left the backlog has left it.
	}, []string{"yoyodyne-1", "yoyodyne-2", "yoyodyne-3", "yoyodyne-4"}, ReadHolds(nil), nil)

	if len(queue.Entries) != 2 {
		t.Fatalf("entries = %#v", queue.Entries)
	}
	// A blocked item keeps its place in the order: it is admitted work that is
	// not finished, and hiding it would understate what is queued. What holds it
	// back is the unfinished work it waits for rather than its status.
	if queue.Entries[0].ID != "yoyodyne-4" || queue.Entries[0].Ready {
		t.Fatalf("blocked entry = %#v", queue.Entries[0])
	}
	if !queue.Entries[1].Ready {
		t.Fatalf("open entry = %#v", queue.Entries[1])
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-3" {
		t.Fatalf("Next() = %#v, %v; the blocked item ahead of it is not pullable", next, ok)
	}
	if queue.Ready() != 1 {
		t.Fatalf("Ready() = %d, want 1", queue.Ready())
	}
}

// The 2026-09-04 idle morning, at the grain the queue is read at: an item whose
// status was set to blocked when its work stopped, whose every blocker has since
// closed, and which nothing is holding. Nothing rewrites that status when the
// gates clear, so trusting it hid two-thirds of the backlog — two p0 items among
// it — while the line sat idle. What decides here is the dependencies.
func TestABlockedStatusWithEveryBlockerClosedDoesNotHideTheWork(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{
			ID: "yoyodyne-ifd.192", Title: "Blocked months ago, blocker long closed",
			Status: statusBlocked, Priority: 0,
			// The blocker is closed, so it is not in the backlog any more. The
			// dependency to it is still listed, exactly as Beads lists it.
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.18", Type: beads.BlocksDependency}},
		},
		{ID: "yoyodyne-ifd.4", Title: "Open and pullable", Status: statusOpen, Priority: 1},
		// The tracker offers only the open one, because its ready list is computed
		// from the same status field. That is exactly why it is not asked about the
		// blocked one.
	}, []string{"yoyodyne-ifd.4"}, ReadHolds(nil), nil)

	released := queue.Entries[0]
	if !released.Ready || len(released.WaitingOn) != 0 || released.Awaiting != "" {
		t.Fatalf("an item whose every blocker closed was still hidden by its status: %#v", released)
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-ifd.192" {
		t.Fatalf("Next() = %#v, %v; the p0 item is what the order says comes first", next, ok)
	}
	if queue.Ready() != 2 {
		t.Fatalf("Ready() = %d, want both items", queue.Ready())
	}
}

// The other half, and the one that must never be got wrong: a stoppage somebody
// has to decide about is not a dependency block. It has no unfinished blocker to
// clear, so a queue that read readiness off the dependencies alone would pull it
// and start a fresh run on top of a change that is still preserved. The hold is
// what stops that, and nothing here lifts one.
func TestAGovernanceHoldIsNotReleasedByADependencyThatCleared(t *testing.T) {
	t.Parallel()

	const stoppage = "run run-5035c832 stopped on it and its change is preserved"
	queue := Order([]beads.WorkItem{
		{
			ID: "yoyodyne-ifd.153", Title: "Guard the notes writer", Status: statusBlocked, Priority: 1,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.18", Type: beads.BlocksDependency}},
		},
		{ID: "yoyodyne-ifd.4", Title: "Open and pullable", Status: statusOpen, Priority: 2},
	}, []string{"yoyodyne-ifd.4"}, ReadHolds(map[string]Hold{"yoyodyne-ifd.153": {Reason: stoppage}}), nil)

	held := queue.Entries[0]
	if held.Ready || held.Awaiting != stoppage {
		t.Fatalf("a held stoppage was released by a cleared dependency: %#v", held)
	}
	// It keeps its place, and what it says is the stoppage rather than a wait:
	// reading it as a wait would send somebody looking for a blocker to release
	// when what it needs is a decision.
	if held.Position != 1 {
		t.Fatalf("the held entry moved: %#v", held)
	}
	rendered := queue.Render()
	if !strings.Contains(rendered, stoppage) {
		t.Fatalf("rendered backlog = %q, want it to name the stoppage", rendered)
	}
	if strings.Contains(rendered, "waiting on") {
		t.Fatalf("a stoppage was reported as a wait: %q", rendered)
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-ifd.4" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
}

// Holds nobody could read are not holds of none. A reader that cannot tell a
// dependency block from a stoppage must release neither, because releasing a
// stoppage starts a run over work that is still there and holding a releasable
// item costs one pull.
func TestHoldsThatCouldNotBeReadHoldEveryBlockedItem(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.192", Title: "Blocked, nothing waiting", Status: statusBlocked, Priority: 0},
		{ID: "yoyodyne-ifd.4", Title: "Open and pullable", Status: statusOpen, Priority: 1},
	}, []string{"yoyodyne-ifd.4"}, Holds{}, nil)

	unread := queue.Entries[0]
	if unread.Ready || unread.Hold() != unreadHold {
		t.Fatalf("an unread hold released a blocked item: %#v", unread)
	}
	// The gap is a fact about the reading rather than about the item, so it is not
	// carried as something somebody has to go and decide about.
	if unread.Awaiting != "" {
		t.Fatalf("a failed reading was reported against the item: %#v", unread)
	}
	// The open item is untouched: what could not be read is what is holding the
	// blocked work, and the tracker still answers for everything else.
	if !queue.Entries[1].Ready || queue.Entries[1].Awaiting != "" {
		t.Fatalf("an unread hold reached open work: %#v", queue.Entries[1])
	}
	if !strings.Contains(queue.Render(), "could not be read") {
		t.Fatalf("rendered backlog = %q, want it to say the reading failed", queue.Render())
	}
}

func TestAnItemWaitingOnUnfinishedWorkKeepsItsPlaceButIsNotPulled(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{
			ID: "yoyodyne-ifd.4", Title: "The development manager that pulls",
			Status: statusOpen, Priority: 0,
			Dependencies: []beads.Dependency{
				{ID: "yoyodyne-ifd.18", Type: beads.BlocksDependency},
				// A dependency whose item has left the backlog is finished work,
				// and a Beads listing says that nowhere on the dependency itself:
				// it reads exactly the same after the blocker is closed. What says
				// so is that nothing in the queue is that item any more.
				{ID: "yoyodyne-ifd.2", Type: beads.BlocksDependency},
				// Only the blocking relation makes an item wait; a parent does not.
				{ID: "yoyodyne-ifd.1", Type: "parent-child"},
			},
		},
		{ID: "yoyodyne-ifd.18", Title: "Give the product manager the backlog", Status: statusOpen, Priority: 1},
		// The tracker holds the first one back and offers the second, which is
		// what it does when a blocker is unfinished.
	}, []string{"yoyodyne-ifd.18"}, ReadHolds(nil), nil)

	waiting := queue.Entries[0]
	if waiting.Ready || len(waiting.WaitingOn) != 1 || waiting.WaitingOn[0] != "yoyodyne-ifd.18" {
		t.Fatalf("waiting entry = %#v", waiting)
	}
	// The order is unchanged by readiness: what is pulled next skips the entry,
	// it does not move it.
	next, ok := queue.Next()
	if !ok || next.ID != "yoyodyne-ifd.18" || next.Position != 2 {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
}

// A Beads listing records that a dependency exists and says nothing about
// whether it is finished: the entry reads identically after the blocker is
// closed. So a dependency whose item has left the backlog names nobody's wait,
// and an item is not held back for a blocker that is already done.
func TestADependencyOnFinishedWorkHoldsNothingBack(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{{
		ID: "yoyodyne-ifd.4", Title: "The development manager that pulls",
		Status: statusOpen, Priority: 0,
		// The blocker is closed, so it is not in the backlog any more. The
		// dependency to it is still listed, exactly as Beads lists it.
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.18", Type: beads.BlocksDependency}},
	}}, []string{"yoyodyne-ifd.4"}, ReadHolds(nil), nil)

	entry := queue.Entries[0]
	if !entry.Ready || len(entry.WaitingOn) != 0 {
		t.Fatalf("an item whose blocker is finished was held back: %#v", entry)
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-ifd.4" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
	if strings.Contains(queue.Render(), "waiting on") {
		t.Fatalf("a finished blocker was named as a wait: %q", queue.Render())
	}
}

// The failure this exists for, at the grain the queue is read at: an item whose
// execution is a conversation with a role, sitting at the top of the order with
// the tracker reporting it as perfectly pullable — which it is, for anything but
// a developer run. It keeps its place and is never what comes next, and the line
// that says so says it will not become pullable rather than reading like a wait.
func TestWorkAConversationCarriesIsNeverTheNextThingToPull(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{
			ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: statusOpen, Priority: 0,
			Executor: domain.WorkItemExecutorConversation,
		},
		{ID: "yoyodyne-ifd.144", Title: "Mark the conversation items", Status: statusOpen, Priority: 1},
	}, []string{"yoyodyne-ifd.138", "yoyodyne-ifd.144"}, ReadHolds(nil), nil)

	held := queue.Entries[0]
	if held.Ready {
		t.Fatalf("work no run can carry was reported ready: %#v", held)
	}
	// It is passed over rather than reordered: where the product manager put it is
	// still where it is.
	if held.Position != 1 || held.Executor != domain.WorkItemExecutorConversation {
		t.Fatalf("the entry moved or lost its marker: %#v", held)
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-ifd.144" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
	if queue.Ready() != 1 {
		t.Fatalf("ready = %d, want the conversation item out of the count", queue.Ready())
	}
	rendered := queue.Render()
	if !strings.Contains(rendered, `its executor is "conversation" rather than a developer run`) {
		t.Fatalf("rendered backlog = %q", rendered)
	}
	// The three other holds are waits, and reading this one as one would send
	// somebody looking for a blocker to release.
	if strings.Contains(rendered, "waiting on") {
		t.Fatalf("an executor was reported as a wait: %q", rendered)
	}
}

// The failure this exists for: work deferred by a decision, sitting at the
// bottom of the order with the tracker reporting it as perfectly pullable —
// which it is, as far as dependencies go. Parking it takes it out of the ready
// count entirely, so a queue that drains past everything above it still has
// nothing to pull.
func TestParkedWorkIsNeverPullableHoweverFarTheQueueDrains(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.188", Title: "Parked work is unschedulable", Status: statusOpen, Priority: 1},
		{
			ID: "yoyodyne-ifd.6", Title: "The thin Codex backend", Status: statusOpen, Priority: 4,
			Parking: "off the critical path by the scope decision; released when a second backend is wanted",
		},
	}, []string{"yoyodyne-ifd.188", "yoyodyne-ifd.6"}, ReadHolds(nil), nil)

	parked := queue.Entries[1]
	if parked.Ready {
		t.Fatalf("parked work was reported ready: %#v", parked)
	}
	// It is passed over rather than taken out of the order: where the product
	// manager put it is still where it is, and the reason travels with it.
	if parked.Position != 2 || !parked.Parking.Parked() {
		t.Fatalf("the entry moved or lost its parking: %#v", parked)
	}
	if queue.Ready() != 1 || queue.Parked() != 1 {
		t.Fatalf("ready = %d, parked = %d; want the parked item counted apart from the pullable one", queue.Ready(), queue.Parked())
	}

	// The case that cost the money: everything above it is gone, and the drained
	// queue still has nothing to pull rather than reaching the parked item.
	drained := Order([]beads.WorkItem{{
		ID: "yoyodyne-ifd.6", Title: "The thin Codex backend", Status: statusOpen, Priority: 4,
		Parking: "off the critical path by the scope decision",
	}}, []string{"yoyodyne-ifd.6"}, ReadHolds(nil), nil)
	if next, ok := drained.Next(); ok {
		t.Fatalf("a drained queue selected parked work: %#v", next)
	}

	rendered := queue.Render()
	for _, required := range []string{
		"backlog (2 admitted, 1 ready to pull, 1 parked):",
		"2. [yoyodyne-ifd.6] p4 parked The thin Codex backend",
		"parked, so no pull selects it however far the queue drains: off the critical path by the scope decision",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered backlog = %q, want it to contain %q", rendered, required)
		}
	}
	// Parking is not a wait, and reading it as one would send somebody looking for
	// a blocker to release rather than for the decision to revisit.
	if strings.Contains(rendered, "waiting on") {
		t.Fatalf("a parking was reported as a wait: %q", rendered)
	}
}

// Parking and the executor are separate axes, and an item can carry both. What
// it is told is the hold that would still stand after the other is lifted:
// releasing the parking on work no run can carry would change nothing.
func TestParkedWorkThatNoRunCouldCarryIsToldTheHoldThatWouldStillStand(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{{
		ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: statusOpen, Priority: 0,
		Executor: domain.ConversationWith(domain.RoleArchitect),
		Parking:  "waiting on the operator's answer about the goals",
	}}, []string{"yoyodyne-ifd.138"}, ReadHolds(nil), nil)

	entry := queue.Entries[0]
	if entry.Ready {
		t.Fatalf("work that is both parked and conversation-executed was reported ready: %#v", entry)
	}
	rendered := queue.Render()
	if !strings.Contains(rendered, `its executor is "conversation:architect" rather than a developer run`) {
		t.Fatalf("rendered backlog = %q", rendered)
	}
	// It is still marked as parked in the listing, because it is, and the count
	// the operator reads has to include it.
	if !strings.Contains(rendered, "p0 parked Promote the brief") || queue.Parked() != 1 {
		t.Fatalf("rendered backlog = %q, parked = %d", rendered, queue.Parked())
	}
}

// Readiness is the tracker's answer rather than one inferred from a listing.
// A listing that carries no dependencies looks exactly like work with none, so
// an item the tracker does not offer is held back whatever its listing shows —
// otherwise a blocked item would be named as the next thing to pull on any
// listing that leaves dependencies out.
func TestAnItemTheTrackerDoesNotOfferIsNotPulledHoweverCleanItsListingLooks(t *testing.T) {
	t.Parallel()

	// Neither item lists a dependency, which is what a listing without dependency
	// data looks like from here. The tracker offers only the second.
	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.3", Title: "Blocked, but the listing does not say so", Status: statusOpen, Priority: 0},
		{ID: "yoyodyne-ifd.4", Title: "Actually pullable", Status: statusOpen, Priority: 1},
	}, []string{"yoyodyne-ifd.4"}, ReadHolds(nil), nil)

	held := queue.Entries[0]
	if held.Ready {
		t.Fatalf("an item the tracker does not offer was reported ready: %#v", held)
	}
	next, ok := queue.Next()
	if !ok || next.ID != "yoyodyne-ifd.4" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
	// It says the tracker is what is holding it rather than inventing a blocker
	// nobody reported.
	if !strings.Contains(queue.Render(), "the tracker does not report it as ready to pull") {
		t.Fatalf("rendered backlog = %q", queue.Render())
	}

	// The degenerate case is the one that matters most: a readiness answer that
	// arrives empty makes the whole queue unready rather than making all of it
	// pullable.
	none := Order([]beads.WorkItem{{ID: "yoyodyne-ifd.3", Title: "Admitted", Status: statusOpen}}, nil, ReadHolds(nil), nil)
	if _, ok := none.Next(); ok || none.Ready() != 0 {
		t.Fatalf("an empty ready set produced %#v", none.Entries)
	}
}

func TestRenderSaysTheOrderWhatIsHeldBackAndWhatIsNext(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.3", Title: "The scheduler that runs it", Status: statusOpen, Priority: 0,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.4", Type: beads.BlocksDependency, Status: statusOpen}}},
		{ID: "yoyodyne-ifd.4", Title: "The development manager that pulls", Status: statusOpen, Priority: 1},
		{ID: "yoyodyne-ifd.9", Title: "A run that failed and was blocked", Status: statusBlocked, Priority: 2},
		// The tracker offers the one item nothing is holding: the first waits for
		// unfinished work and the third stopped on a change nobody has decided about.
	}, []string{"yoyodyne-ifd.4"}, ReadHolds(map[string]Hold{
		"yoyodyne-ifd.9": {Reason: "run run-f4fbf60a stopped on it and its change is preserved"},
	}), nil)

	rendered := queue.Render()
	for _, required := range []string{
		"backlog (3 admitted, 1 ready to pull):",
		"1. [yoyodyne-ifd.3] p0 The scheduler that runs it",
		"waiting on yoyodyne-ifd.4",
		"2. [yoyodyne-ifd.4] p1 The development manager that pulls",
		"3. [yoyodyne-ifd.9] p2 A run that failed and was blocked",
		"run run-f4fbf60a stopped on it and its change is preserved",
		"next to be pulled: yoyodyne-ifd.4",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered backlog = %q, want it to contain %q", rendered, required)
		}
	}

	// An empty backlog says so. A blank space is not an answer.
	if empty := (Queue{}).Render(); !strings.Contains(empty, "nothing is admitted") {
		t.Fatalf("empty backlog = %q", empty)
	}

	// A backlog where everything waits says that rather than naming nothing at
	// all, because "nothing is ready" and "nothing is queued" call for opposite
	// responses from an operator.
	stalled := Order([]beads.WorkItem{{ID: "yoyodyne-1", Title: "Blocked", Status: statusBlocked, Priority: 0}}, nil,
		ReadHolds(map[string]Hold{"yoyodyne-1": {Reason: "its stoppage is in front of the development manager"}}), nil)
	if !strings.Contains(stalled.Render(), "nothing is ready to be pulled") {
		t.Fatalf("stalled backlog = %q", stalled.Render())
	}
}

func TestARenderedBacklogIsCutWhileItsCountsStayExact(t *testing.T) {
	t.Parallel()

	items := make([]beads.WorkItem, 0, maxRenderedEntries+5)
	ready := make([]string, 0, maxRenderedEntries+5)
	for i := 0; i < maxRenderedEntries+5; i++ {
		id := "yoyodyne-" + strconv.Itoa(i)
		items = append(items, beads.WorkItem{
			ID:     id,
			Title:  strings.Repeat("a very long title ", 40),
			Status: statusOpen,
		})
		ready = append(ready, id)
	}
	rendered := Order(items, ready, ReadHolds(nil), nil).Render()

	if !strings.Contains(rendered, "backlog ("+strconv.Itoa(len(items))+" admitted") {
		t.Fatalf("the count was cut with the list: %q", rendered)
	}
	if !strings.Contains(rendered, "5 further admitted item(s) are not listed here.") {
		t.Fatalf("the cut was not declared: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if len(line) > maxRenderedTitleBytes*2 {
			t.Fatalf("a tracker title escaped its line: %q", line)
		}
	}
}

// Held work carries which of the two waits it is in, and the queue counts them
// apart. Both are unpullable and neither is a wait for anything, and that is
// where the resemblance ends: one is the development manager's to settle and the
// other is the harness's to carry out, so a count that added them together sent
// an operator to chase decisions that had all been made.
func TestHeldEntriesCountApartByWhoseMoveTheyAreWaitingOn(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.150", Title: "Decided and not carried out", Status: statusBlocked, Priority: 1},
		{ID: "yoyodyne-ifd.151", Title: "Nobody has decided", Status: statusBlocked, Priority: 1},
		{ID: "yoyodyne-ifd.4", Title: "Open and pullable", Status: statusOpen, Priority: 2},
	}, []string{"yoyodyne-ifd.4"}, ReadHolds(map[string]Hold{
		"yoyodyne-ifd.150": {Reason: "run run-a stopped on it; the decision is recorded", Decided: true},
		"yoyodyne-ifd.151": {Reason: "run run-b stopped on it; nobody has decided"},
	}), nil)

	if queue.AwaitingCarryOut() != 1 || queue.AwaitingDecision() != 1 {
		t.Fatalf("the queue counts %d awaiting carry-out and %d awaiting a decision, want one of each",
			queue.AwaitingCarryOut(), queue.AwaitingDecision())
	}
	if decided := queue.Entries[0]; !decided.AwaitingCarryOut || decided.Ready {
		t.Fatalf("a decided stoppage reads as %#v, want it held and named as the harness's", decided)
	}
	if undecided := queue.Entries[1]; undecided.AwaitingCarryOut || undecided.Ready {
		t.Fatalf("an undecided stoppage reads as %#v, want it held and named as hers", undecided)
	}
	// A decision recorded about an item nothing is holding says nothing about why
	// it is not being pulled, so it is never on either count.
	if queue.Entries[2].AwaitingCarryOut {
		t.Fatalf("a pullable item was counted as held: %#v", queue.Entries[2])
	}
}

// An item labelled needs-design is admitted work in the order and never the
// next thing to pull, whatever the tracker says about it; without the label the
// same item is the next thing to pull.
func TestANeedsDesignItemIsNeverReadyUntilTheLabelIsRemoved(t *testing.T) {
	t.Parallel()

	labelled := beads.WorkItem{ID: "item", Title: "Item", Status: statusOpen, Labels: []string{"reliability", domain.NeedsDesignLabel}}
	queue := Order([]beads.WorkItem{labelled}, []string{labelled.ID}, ReadHolds(nil), nil)
	entry := queue.Entries[0]
	if entry.Ready || !entry.NeedsDesign {
		t.Fatalf("entry = %#v, want a labelled item unready and marked", entry)
	}
	if _, ok := queue.Next(); ok {
		t.Fatal("the queue offered an item labelled needs-design")
	}
	if kind := entry.HoldKind(); kind != HeldForDesign {
		t.Fatalf("kind = %q, want %q", kind, HeldForDesign)
	}
	if rendered := queue.Render(); !strings.Contains(rendered, "p0 "+domain.NeedsDesignLabel+" Item") || !strings.Contains(rendered, "architect's design") {
		t.Fatalf("rendered = %q, want the label on the item's line and the reason beneath it", rendered)
	}

	labelled.Labels = []string{"reliability"}
	queue = Order([]beads.WorkItem{labelled}, []string{labelled.ID}, ReadHolds(nil), nil)
	if next, ok := queue.Next(); !ok || next.ID != labelled.ID {
		t.Fatalf("Next() = %#v, %v, want the item once its label is removed", next, ok)
	}
}

// The kind of a hold and the sentence that explains it come from one reading,
// so a surface counting piles by kind counts the piles the sentences describe:
// the executor answers first, then the parking, then the hold, then the wait,
// and the tracker not offering an item is the one kind nothing here explains.
func TestHoldKindNamesThePileTheHoldSentenceDescribes(t *testing.T) {
	t.Parallel()

	for _, held := range []struct {
		entry Entry
		kind  HoldKind
		says  string
	}{
		{Entry{Executor: domain.ConversationWith(domain.RoleArchitect), Parking: "parked"}, HeldByConversation, "its executor is"},
		{Entry{Parking: "the design is being reworked", Awaiting: "a decision"}, HeldParked, "parked, so no pull selects it"},
		{Entry{Parking: "the design is being reworked", NeedsDesign: true}, HeldParked, "parked, so no pull selects it"},
		{Entry{NeedsDesign: true, Awaiting: "run run-b stopped on it", WaitingOn: []string{"x"}}, HeldForDesign, "labelled needs-design"},
		{Entry{Awaiting: "run run-b stopped on it", WaitingOn: []string{"x"}}, HeldForAPerson, "run run-b stopped on it"},
		{Entry{ID: "yoyodyne-ifd.209.7", HumanGates: humangate.Read(humangate.DeclareMarker + " soak-reviewed — the operator has judged the soak"), WaitingOn: []string{"x"}}, HeldForAGate, "waiting on a person"},
		{Entry{WaitingOn: []string{"yoyodyne-ifd.1", "yoyodyne-ifd.2"}}, HeldWaitingOn, "waiting on yoyodyne-ifd.1, yoyodyne-ifd.2"},
		{Entry{Status: statusBlocked}, HeldUnread, unreadHold},
		{Entry{Status: statusOpen}, HeldUnread, "the tracker does not report it as ready to pull"},
	} {
		if kind := held.entry.HoldKind(); kind != held.kind {
			t.Errorf("%+v: kind %q, want %q", held.entry, kind, held.kind)
		}
		if says := held.entry.Hold(); !strings.Contains(says, held.says) {
			t.Errorf("%+v: says %q, want it to contain %q", held.entry, says, held.says)
		}
	}
}

// Awaits is read off the same derivation as HoldKind, so an entry counts as
// held for a person exactly when its kind says so. The queue itself makes four
// other refusals, and each is tried here beside a hold: the executor and the
// parking win over the hold, and the hold wins over the wait and over blocked
// work whose holds could not be read. A directive pause and the pass-level
// stall are not the queue's to make, so no entry here can carry them.
func TestAwaitsIsReadOffTheHoldKind(t *testing.T) {
	t.Parallel()

	for _, tried := range []struct {
		name     string
		entry    Entry
		kind     HoldKind
		held     bool
		carryOut bool
	}{
		{"a conversation carries it", Entry{Executor: domain.ConversationWith(domain.RoleArchitect), Awaiting: "run run-a stopped on it", AwaitingCarryOut: true}, HeldByConversation, false, false},
		{"parked", Entry{Parking: "later", Awaiting: "run run-a stopped on it", AwaitingCarryOut: true}, HeldParked, false, false},
		{"held and waiting on other work", Entry{Awaiting: "run run-a stopped on it", WaitingOn: []string{"yoyodyne-ifd.1"}}, HeldForAPerson, true, false},
		{"held, decided, and blocked", Entry{Status: statusBlocked, Awaiting: "run run-a stopped on it; the decision is recorded", AwaitingCarryOut: true}, HeldForAPerson, true, true},
		{"held alone", Entry{Awaiting: "run run-a stopped on it"}, HeldForAPerson, true, false},
		{"gated", Entry{ID: "yoyodyne-ifd.209.7", HumanGates: humangate.Read(humangate.DeclareMarker + " soak-reviewed — the operator has judged the soak")}, HeldForAGate, false, false},
		{"waiting alone", Entry{WaitingOn: []string{"yoyodyne-ifd.1"}}, HeldWaitingOn, false, false},
		{"blocked and unread", Entry{Status: statusBlocked, AwaitingCarryOut: true}, HeldUnread, false, false},
		{"pullable", Entry{Status: statusOpen, Ready: true}, HeldUnread, false, false},
	} {
		if kind := tried.entry.HoldKind(); kind != tried.kind {
			t.Errorf("%s: kind %q, want %q", tried.name, kind, tried.kind)
		}
		held, carryOut := tried.entry.Awaits()
		if held != tried.held || carryOut != tried.carryOut {
			t.Errorf("%s: Awaits() = %v, %v, want %v, %v", tried.name, held, carryOut, tried.held, tried.carryOut)
		}
		if held != (tried.entry.HoldKind() == HeldForAPerson) {
			t.Errorf("%s: Awaits() says held %v but the kind is %q", tried.name, held, tried.entry.HoldKind())
		}
	}
}

// The regression this machinery exists for, as it actually happened on
// 2026-09-04.
//
// yoyodyne-ifd.209.6 was the parity soak, and its done condition reserved the
// operator's own reading of that soak before the default flipped.
// yoyodyne-ifd.209.7 was the flip. The only encoding available for "a person has
// to judge this first" was an item-closed dependency, so the reservation went
// into 209.6's text and 209.7 was made to depend on 209.6 — and when the run
// that finished the soak closed 209.6, the tracker reported 209.7 ready and the
// flip jumped the step the operator had kept for themselves.
//
// The first half below is that jump, reproduced: nothing about the dependency
// encoding holds 209.7 once 209.6 has closed, and nothing in this package could
// tell that from work that was genuinely finished. The second half is the gate
// doing what the dependency could not.
func TestAnItemClosureNeverPassesAStepReservedForAPerson(t *testing.T) {
	t.Parallel()

	// The soak has closed and has left the backlog, exactly as it did.
	flip := beads.WorkItem{
		ID: "yoyodyne-ifd.209.7", Title: "Declarative becomes the default",
		Status: statusOpen, Priority: 1,
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.209.6", Type: beads.BlocksDependency, Status: "closed"}},
	}

	jumped := Order([]beads.WorkItem{flip}, []string{flip.ID}, ReadHolds(nil), nil)
	if !jumped.Entries[0].Ready {
		t.Fatal("the dependency-only encoding is being reproduced here, and it must read as ready; " +
			"if it does not, this test no longer describes the failure it guards against")
	}

	// The same flip, with the operator's step declared as a gate on the work it
	// holds rather than as a clause in an item somebody closes.
	flip.Description = "Flip the default once the soak has been judged.\n\n" +
		humangate.DeclareMarker + " soak-reviewed — the operator has read a week of soak runs and is content to flip\n"

	held := Order([]beads.WorkItem{flip}, []string{flip.ID}, ReadHolds(nil), nil)
	entry := held.Entries[0]
	if entry.Ready {
		t.Fatal("the flip was pullable with the operator's step still untaken, which is the whole of the regression")
	}
	if len(entry.HumanGates.Gates) != 1 || entry.HumanGates.Gates[0].Name != "soak-reviewed" {
		t.Fatalf("gates = %#v", entry.HumanGates)
	}
	// The queue says it is a person rather than a wait, and says so where the
	// order is read.
	if hold := entry.Hold(); !strings.Contains(hold, "waiting on a person") || !strings.Contains(hold, "closing an item") {
		t.Fatalf("hold = %q", hold)
	}
	if _, ok := held.Next(); ok {
		t.Fatal("Next() offered the gated item")
	}
	if held.Gated() != 1 {
		t.Fatalf("Gated() = %d", held.Gated())
	}
	if !strings.Contains(held.Render(), "1 waiting on a person") {
		t.Fatalf("rendered backlog = %q", held.Render())
	}

	// And once a person has recorded the act, the same item is pullable. The gate
	// stops work; it does not end it.
	passed := Order([]beads.WorkItem{flip}, []string{flip.ID}, ReadHolds(nil), map[string][]string{"yoyodyne-ifd.209.7": {"soak-reviewed"}})
	if !passed.Entries[0].Ready {
		t.Fatalf("the flip is still held after the act was recorded: %#v", passed.Entries[0])
	}
	if passed.Entries[0].HumanGates.Holds() {
		t.Fatalf("a gate somebody passed is still reported as outstanding: %#v", passed.Entries[0].HumanGates)
	}
}

// A gate outranks a wait on other work, because the other work will finish on
// its own and the gate will not. An item told about its blocker instead would
// have somebody watching for a moment that changes nothing.
func TestAGateIsNamedAheadOfWorkThatWillClearOnItsOwn(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.209.6", Title: "The soak", Status: statusOpen, Priority: 0},
		{
			ID: "yoyodyne-ifd.209.7", Title: "The flip", Status: statusOpen, Priority: 1,
			Description:  humangate.DeclareMarker + " soak-reviewed — the operator has judged the soak\n",
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.209.6", Type: beads.BlocksDependency}},
		},
	}, []string{"yoyodyne-ifd.209.6"}, ReadHolds(nil), nil)

	flip := queue.Entries[1]
	if !strings.Contains(flip.Hold(), "waiting on a person") {
		t.Fatalf("hold = %q, want the gate named ahead of the blocker", flip.Hold())
	}
	// A parked item is still told about its parking: that is a decision somebody
	// already took, and offering them a gate to record would be offering an act
	// that changes nothing.
	parked := Order([]beads.WorkItem{{
		ID: "yoyodyne-ifd.209.7", Title: "The flip", Status: statusOpen, Priority: 1,
		Description: humangate.DeclareMarker + " soak-reviewed — the operator has judged the soak\n",
		Parking:     domain.WorkItemParking("waiting for the release to cut"),
	}}, []string{"yoyodyne-ifd.209.7"}, ReadHolds(nil), nil)
	if !strings.Contains(parked.Entries[0].Hold(), "parked") {
		t.Fatalf("hold = %q", parked.Entries[0].Hold())
	}
}

// A gate declaration nothing could read holds the item exactly as a gate does.
//
// This is the same regression arriving through the parser instead of through the
// tracker. An author who writes `human-gate:` with a mistyped name, or with
// nothing said about the act, meant to reserve a step for a person; a reader
// that dropped what it could not parse would turn that intention into an item
// with no gate at all, and the work would be pulled past a step nobody was ever
// asked to take. So the typo stops the work, and says what is wrong with it.
func TestADeclarationNothingCouldReadHoldsTheItemToo(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		declaration string
		want        string
	}{
		{name: "a name nothing could record", declaration: humangate.DeclareMarker + " Soak Reviewed — the operator has judged the soak\n", want: "not a gate name"},
		{name: "nothing said about the act", declaration: humangate.DeclareMarker + " soak-reviewed\n", want: "does not say what the person has to do"},
		{name: "a marker with nothing after it", declaration: humangate.DeclareMarker + "\n", want: "has no name"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			item := beads.WorkItem{
				ID: "yoyodyne-ifd.209.7", Title: "Declarative becomes the default",
				Status: statusOpen, Priority: 1, Description: testCase.declaration,
			}
			queue := Order([]beads.WorkItem{item}, []string{item.ID}, ReadHolds(nil), nil)
			entry := queue.Entries[0]
			if entry.Ready {
				t.Fatalf("a declaration nothing could read left the item pullable: %#v", entry)
			}
			if len(entry.HumanGates.Unreadable) != 1 {
				t.Fatalf("unreadable = %#v", entry.HumanGates)
			}
			hold := entry.Hold()
			if !strings.Contains(hold, testCase.want) {
				t.Fatalf("hold = %q, want it to mention %q", hold, testCase.want)
			}
			// It says what actually clears it, which is not a command: there is no
			// name for anybody to have recorded an act against.
			if !strings.Contains(hold, "correcting the line") {
				t.Fatalf("hold = %q, want it to say the author has to fix the declaration", hold)
			}
			// And no recorded act passes it, however many are on the record.
			recorded := Order([]beads.WorkItem{item}, []string{item.ID}, ReadHolds(nil), map[string][]string{"yoyodyne-ifd.209.7": {"soak-reviewed", "soak"}})
			if recorded.Entries[0].Ready {
				t.Fatalf("a recorded act passed a declaration nothing could read: %#v", recorded.Entries[0])
			}
			if recorded.Gated() != 1 {
				t.Fatalf("Gated() = %d, want the unreadable declaration counted as waiting on a person", recorded.Gated())
			}
		})
	}
}

// A gate name recurs, and an act recorded against one item passes that item's
// step and nobody else's.
//
// `release-signed` describes a step taken once per release. If the recorded name
// alone decided it, the second release would be pullable on the strength of the
// first release's signature — the same "gate reads satisfied with the step
// untaken" failure this whole mechanism exists to end, arriving through the
// namespace instead of through the tracker.
func TestAnActPassesTheGateOnItsOwnItemAndNoOther(t *testing.T) {
	t.Parallel()

	items := []beads.WorkItem{
		{
			ID: "yoyodyne-ifd.300", Title: "Cut v0.3.0", Status: statusOpen, Priority: 0,
			Description: humangate.DeclareMarker + " release-signed — the operator has signed this release off\n",
		},
		{
			ID: "yoyodyne-ifd.400", Title: "Cut v0.4.0", Status: statusOpen, Priority: 1,
			Description: humangate.DeclareMarker + " release-signed — the operator has signed this release off\n",
		},
	}
	// The first release's sign-off is on the record, and nothing else is.
	queue := Order(items, []string{"yoyodyne-ifd.300", "yoyodyne-ifd.400"}, ReadHolds(nil), map[string][]string{"yoyodyne-ifd.300": {"release-signed"}})

	if !queue.Entries[0].Ready {
		t.Fatalf("the signed release is still held: %#v", queue.Entries[0])
	}
	later := queue.Entries[1]
	if later.Ready {
		t.Fatal("the next release was pullable on the strength of the last release's signature")
	}
	if len(later.HumanGates.Gates) != 1 || later.HumanGates.Gates[0].Name != "release-signed" {
		t.Fatalf("gates = %#v", later.HumanGates)
	}
	// And the line it prints names the item to record against, so the operator is
	// not sent to type a command that would be refused as already passed.
	if hold := later.Hold(); !strings.Contains(hold, "--for yoyodyne-ifd.400") {
		t.Fatalf("hold = %q", hold)
	}
	if queue.Gated() != 1 {
		t.Fatalf("Gated() = %d", queue.Gated())
	}
}

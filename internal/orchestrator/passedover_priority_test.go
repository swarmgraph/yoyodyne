package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The case yoyodyne-ifd.428.58 was admitted for, as the tracker held it when a
// slot freed at 13:42 PDT on 2026-09-29: two priority-0 items ready, the red-check
// misfiling fix (yoyodyne-c02) admitted first, and the development manager's
// re-run of the concurrent tracker access item (yoyodyne-ifd.271) at priority 3
// outstanding. The tracker lists newest first, which is the order the fixture
// gives them in.
func stuckPriorityZeroItems() []beads.WorkItem {
	at := func(clock string) time.Time {
		parsed, err := time.Parse(time.RFC3339, clock)
		if err != nil {
			panic(err)
		}
		return parsed
	}
	return []beads.WorkItem{
		{ID: "yoyodyne-8ff", Title: "The re-arms are refused", Status: "open", Priority: 0, CreatedAt: at("2026-09-29T19:04:50Z")},
		{ID: "yoyodyne-c02", Title: "A red check is filed against main only when main fails it", Status: "open", Priority: 0, CreatedAt: at("2026-09-29T15:04:46Z")},
		{ID: "yoyodyne-ifd.271", Title: "Concurrent tracker access is exercised live", Status: "blocked", Priority: 3, CreatedAt: at("2026-09-03T21:18:06Z")},
	}
}

// carriedWhenASlotIsFree fires a decision as a run where a developer slot is free
// and refuses it on developer capacity where one is not, as the action does: a
// pull with no slot free still attempts a decision, so that the refusal is
// recorded on the item. The record offers it again until it fires.
func carriedWhenASlotIsFree(fired map[string]bool) func(*scheduleHarness, CarryOutTask) (CarriedOut, Outcome, error) {
	return func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		h.mu.Lock()
		full := h.running >= h.capacity
		if !full {
			fired[task.WorkItemID] = true
		}
		h.mu.Unlock()
		if full {
			return CarriedOut{
				WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
				Gate: runstate.TriageGateCapacity, Waiting: true,
				Problem: "the \"rerun\" the development manager decided is waiting on " + runstate.TriageGateCapacity,
			}, Outcome{}, nil
		}
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
}

// A ready priority-0 item is started ahead of a lower-priority re-run and ahead of
// a priority-0 item admitted after it. Before yoyodyne-ifd.428.58 the re-run took
// the one free slot before the queue was read, and once the queue was read the
// newest priority-0 item was taken first.
func TestAReadyPriorityZeroItemIsSelectedAheadOfALowerPriorityDecisionAndNewerWork(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(stuckPriorityZeroItems()...)
	harness.ReadyItems["yoyodyne-ifd.271"] = false
	fired := map[string]bool{}
	harness.outstanding = outstandingUntilFired(fired, decidedTask("yoyodyne-ifd.271"))
	harness.carry = carriedWhenASlotIsFree(fired)

	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	var order []string
	for _, started := range schedule.Started {
		// An attempt the capacity gate refused is on the schedule too, so the
		// refusal has somewhere to be recorded; it started nothing.
		if started.Declined == "" {
			order = append(order, started.WorkItemID)
		}
	}
	want := []string{"yoyodyne-c02", "yoyodyne-8ff", "yoyodyne-ifd.271"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("started = %v, want %v: the oldest priority-0 item first, and the priority-3 re-run once nothing ready outranks it", order, want)
	}
	if !fired["yoyodyne-ifd.271"] {
		t.Fatalf("carried = %#v, want the re-run fired once the priority-0 work had its slot", harness.carried)
	}
	// The pull that held the re-run back said why, naming the work that outranked
	// it, where the development manager reads what became of her decision.
	var said string
	for _, passed := range harness.passedOver {
		if why, named := passed[docketedRunID]; named {
			said = why
			break
		}
	}
	for _, want := range []string{"priority 3", "yoyodyne-c02", "yoyodyne-8ff", "Lead Product Manager's order"} {
		if !strings.Contains(said, want) {
			t.Fatalf("what the pull said of the re-run = %q, want it to name %q", said, want)
		}
	}
}

// A decision held back for work that outranks it is not held back by work that
// cannot start: the slot the queue leaves empty is the decision's, and lower-
// priority work the walk reaches after it waits behind it as it always did.
func TestADecisionOutrankedOnlyByWorkThatCannotStartTakesTheSlot(t *testing.T) {
	t.Parallel()

	items := append(stuckPriorityZeroItems(), beads.WorkItem{ID: "yoyodyne-ifd.900", Title: "Later work", Status: "open", Priority: 4})
	harness := newScheduleHarness(items...)
	harness.ReadyItems["yoyodyne-ifd.271"] = false
	for _, id := range []string{"yoyodyne-c02", "yoyodyne-8ff"} {
		harness.pausing[id] = []directive.Directive{{
			ID:         "directive-1",
			Kind:       directive.KindArtifact,
			Text:       "the red-check rule is being rewritten",
			Unresolved: "whose failure a red check is",
		}}
	}
	fired := map[string]bool{}
	harness.outstanding = outstandingUntilFired(fired, decidedTask("yoyodyne-ifd.271"))
	harness.carry = carriedWhenASlotIsFree(fired)

	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	var order []string
	for _, started := range schedule.Started {
		// An attempt the capacity gate refused is on the schedule too, so the
		// refusal has somewhere to be recorded; it started nothing.
		if started.Declined == "" {
			order = append(order, started.WorkItemID)
		}
	}
	want := []string{"yoyodyne-ifd.271", "yoyodyne-ifd.900"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("started = %v, want %v: the re-run in the slot the paused priority-0 work left, ahead of the priority-4 item", order, want)
	}
}

// A priority-0 item passed over for lower-priority work on a sentence it states
// about itself is passed over naming that sentence and the field it was read
// out of: on the schedule's own record, and on the entry the development
// manager's docket holds. The factory-flow program manager's first suspect for
// the red-check misfiling fix (yoyodyne-c02) was this reading, and the only way
// to confirm or rule it out was a reason that quoted what it read.
func TestAPriorityZeroItemPassedOverOnItsProseNamesTheSentenceItRead(t *testing.T) {
	t.Parallel()

	const sentence = "It does not start before the red-check rule (yoyodyne-m5p) lands"
	items := []beads.WorkItem{
		{
			ID: "yoyodyne-c02", Title: "A red check is filed against main only when main fails it", Status: "open", Priority: 0,
			Description: "The harness filed pull request 907's red check as main's failure. " + sentence + ".",
		},
		{ID: "yoyodyne-ifd.271", Title: "Concurrent tracker access is exercised live", Status: "open", Priority: 3},
	}
	harness := newScheduleHarness(items...)
	harness.tree = citingTree{}
	docket, _ := unreadyDocketFor(t, harness)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-ifd.271" {
		t.Fatalf("started = %#v, want the priority-3 item started and the priority-0 one held on its sentence", schedule.Started)
	}
	var reason string
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID == "yoyodyne-c02" {
			reason = deferred.Reason
		}
	}
	for _, want := range []string{sentence, "its description says of it"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("passed-over reason = %q, want it to contain %q", reason, want)
		}
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatalf("read the docket: %v", err)
	}
	if len(entries) != 1 || entries[0].Unready == nil || len(entries[0].Unready.Prerequisites) == 0 {
		t.Fatalf("docket = %+v, want the one unready entry for the priority-0 item", entries)
	}
	read := entries[0].Unready.Prerequisites[0]
	if !strings.Contains(read.Missing, sentence) || !strings.Contains(read.Missing, "its description") {
		t.Fatalf("docket prerequisite = %+v, want the sentence quoted and its field named where the development manager reads it", read)
	}
	if !strings.Contains(entries[0].Render(), sentence) {
		t.Fatalf("rendered docket entry does not quote the sentence:\n%s", entries[0].Render())
	}
}

// The case yoyodyne-ifd.428.81 was admitted for: two developer slots, a deep
// queue of ready work at priorities 0 and 1, and one decided re-run on a
// priority-2 item whose stopped run's branch is still there. The decision takes
// the first free slot ahead of all of it, and says what it was put ahead of.
func TestADecisionAboutPreservedWorkTakesTheNextFreeSlotAheadOfHigherPriorityWork(t *testing.T) {
	t.Parallel()

	var items []beads.WorkItem
	for index := 0; index < 6; index++ {
		items = append(items,
			beads.WorkItem{ID: fmt.Sprintf("yoyodyne-p0-%d", index), Title: "Fresh priority-0 work", Status: "open", Priority: 0},
			beads.WorkItem{ID: fmt.Sprintf("yoyodyne-p1-%d", index), Title: "Fresh priority-1 work", Status: "open", Priority: 1})
	}
	items = append(items, beads.WorkItem{ID: "yoyodyne-ifd.363", Title: "Neutral operator wording", Status: "blocked", Priority: 2})
	harness := newScheduleHarness(items...)
	harness.capacity = 2
	harness.ReadyItems["yoyodyne-ifd.363"] = false
	harness.stoppages = haltedWork{runs: []runstate.State{{
		RunID:        docketedRunID,
		WorkItemID:   "yoyodyne-ifd.363",
		Status:       runstate.StatusFailed,
		Branch:       "yoyodyne/yoyodyne-ifd-363/01234567",
		WorktreePath: "/state/worktrees/yoyodyne-ifd-363-01234567",
		Blocker:      "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}}}
	fired := map[string]bool{}
	harness.outstanding = outstandingUntilFired(fired, decidedTask("yoyodyne-ifd.363"))
	harness.carry = carriedWhenASlotIsFree(fired)

	schedule, err := (Scheduler{Open: harness.open, Limit: 2}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	var order []string
	for _, started := range schedule.Started {
		if started.Declined == "" {
			order = append(order, started.WorkItemID)
		}
	}
	if len(order) == 0 || order[0] != "yoyodyne-ifd.363" {
		t.Fatalf("started = %v, want the decided re-run of preserved work first, ahead of every ready priority-0 and priority-1 item", order)
	}
	if len(harness.carried) == 0 || !harness.carried[0].Preserved {
		t.Fatalf("carried = %#v, want the decision carried out marked as preserved work", harness.carried)
	}
	// What it went ahead of is named: on the task the action records the run's
	// reason from, and on the pass's own account of the start.
	ahead := strings.Join(harness.carried[0].AheadOf, ",")
	for _, want := range []string{"Fresh priority-0 work (yoyodyne-p0-0)", "Fresh priority-1 work (yoyodyne-p1-0)"} {
		if !strings.Contains(ahead, want) {
			t.Fatalf("ahead of = %q, want it to name %s", ahead, want)
		}
	}
	reason := carryingOutReason(harness.carried[0])
	for _, want := range []string{"went ahead of 12 items of higher priority", "yoyodyne-p0-0", "still there"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("pass reason = %q, want it to contain %q", reason, want)
		}
	}
}

// The same decision about a stopped run whose branch and worktree are gone keeps
// the order it had: work that outranks its item goes first.
func TestADecisionAboutWorkNoLongerPreservedStillWaitsBehindHigherPriorityWork(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(stuckPriorityZeroItems()...)
	harness.ReadyItems["yoyodyne-ifd.271"] = false
	harness.stoppages = haltedWork{runs: []runstate.State{{
		RunID: docketedRunID, WorkItemID: "yoyodyne-ifd.271", Status: runstate.StatusFailed,
		Branch: "yoyodyne/yoyodyne-ifd-271/01234567", BranchRemoved: true,
	}}}
	fired := map[string]bool{}
	harness.outstanding = outstandingUntilFired(fired, decidedTask("yoyodyne-ifd.271"))
	harness.carry = carriedWhenASlotIsFree(fired)

	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	var order []string
	for _, started := range schedule.Started {
		if started.Declined == "" {
			order = append(order, started.WorkItemID)
		}
	}
	if want := "yoyodyne-c02,yoyodyne-8ff,yoyodyne-ifd.271"; strings.Join(order, ",") != want {
		t.Fatalf("started = %v, want %s", order, want)
	}
}

// What a fired decision records about the work it went ahead of, and what a
// decision about preserved work that found no slot says clears it.
func TestTheSentencesAPreservedDecisionCarries(t *testing.T) {
	t.Parallel()

	task := decidedTask("yoyodyne-ifd.363")
	if aheadOfQueue(task) != "" || nextSlot(task) != "" {
		t.Fatalf("a decision not marked preserved carries no sentence")
	}
	task.Preserved = true
	if !strings.Contains(nextSlot(task), "it is next") {
		t.Fatalf("nextSlot = %q, want it to say the decision is next", nextSlot(task))
	}
	task.AheadOf = []string{"yoyodyne-a", "yoyodyne-b"}
	ahead := aheadOfQueue(task)
	if !strings.Contains(ahead, "2 items") || !strings.Contains(ahead, "yoyodyne-a, yoyodyne-b") {
		t.Fatalf("aheadOfQueue = %q, want both items named", ahead)
	}
	long := strings.Repeat("reasoning ", runstate.MaxSelectionReasonBytes)
	got := withAheadOf(long, ahead)
	if len(got) > runstate.MaxSelectionReasonBytes || !strings.HasSuffix(got, ahead) {
		t.Fatalf("withAheadOf kept %d bytes, want the bound kept and the sentence whole at the end", len(got))
	}
}

func TestCheckStageContinuationsTakeAFreeSlotAheadOfEqualOrLowerPriorityFreshWork(t *testing.T) {
	t.Parallel()
	for _, decision := range []string{DecisionContinueChecks, runstate.TriageDecisionRepair} {
		for _, freshPriority := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("%s-%d", decision, freshPriority), func(t *testing.T) {
				h := newScheduleHarness(
					beads.WorkItem{ID: "yoyodyne-continuation", Title: "Continue the stopped checks", Status: "blocked", Priority: 1},
					beads.WorkItem{ID: "yoyodyne-fresh", Title: "Fresh work", Status: "open", Priority: freshPriority},
				)
				h.ReadyItems["yoyodyne-continuation"] = false
				fired := map[string]bool{}
				task := decidedTask("yoyodyne-continuation")
				task.Decision = decision
				h.outstanding = outstandingUntilFired(fired, task)
				h.carry = carriedWhenASlotIsFree(fired)
				schedule, err := (Scheduler{Open: h.open}).Schedule(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var order []string
				for _, started := range schedule.Started {
					if started.Declined == "" {
						order = append(order, started.WorkItemID)
					}
				}
				want := "yoyodyne-continuation,yoyodyne-fresh"
				if freshPriority < 1 {
					want = "yoyodyne-fresh,yoyodyne-continuation"
				}
				if strings.Join(order, ",") != want {
					t.Fatalf("order = %v, want %s", order, want)
				}
			})
		}
	}
}

package landingtests

// Closure follows the kind of landing a run claimed, not the fact that it
// landed. The case these tests exist for is a real one: a run answered a work
// item it could not do yet with a diagnosis that said so in bold, the diagnosis
// integrated because it was good evidence, and the item closed against it —
// because closure mechanically followed integration and read nothing of what
// had landed. Both kinds are driven through the whole pipeline here rather than
// asserted over a hand-built state, because a test that set the claim itself
// would pass just as happily if nothing ever read one off a developer's reply.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The diagnosis this whole item comes from, replayed: a developer that finds the
// work is not doable yet lands the evidence for that and says so. The change is
// reviewed and integrated exactly as any other change is, and the item it was
// claimed for goes back to the backlog parked, with the diagnosis as the parking
// reason.
func TestAnHonestNotDoableYetLandingIntegratesAndReParksItsItem(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker()
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
	provider.DeveloperFinalText = "The conversion is not doable yet; this change lands the diagnosis.\n\n" +
		testharness.LandingBlock(`{"outcome":"evidence","why":"the management-conversion design has not landed, so the anchor stays open"}`)
	pipeline, store := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The evidence landed. That half is unchanged: an honest landing is worth
	// keeping and is promoted like anything else that passes the gate.
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("an evidence landing did not integrate: %#v", outcome)
	}
	if outcome.WorkItemClosed {
		t.Fatal("the run closed an item its own landing said it does not discharge")
	}
	if tracker.Closed {
		t.Fatalf("the tracker closed the item; calls = %v", tracker.Calls)
	}
	if !tracker.Reopened {
		t.Fatalf("the item was left claimed by a run that has ended; calls = %v", tracker.Calls)
	}
	if tracker.Item.Status != "open" {
		t.Errorf("item status = %q, want open", tracker.Item.Status)
	}
	// The reason has to be on the item, or the item afterwards reads as work
	// somebody walked away from.
	if !strings.Contains(tracker.ReopenReason, "the anchor stays open") {
		t.Errorf("the item does not carry the developer's account: %q", tracker.ReopenReason)
	}
	if !strings.Contains(tracker.ReopenReason, outcome.RunID) {
		t.Errorf("the item does not name the run whose evidence landed: %q", tracker.ReopenReason)
	}
	// And the parking is what keeps the item out of the next pull. Without it the
	// item is back in the queue as ordinary open work, the run that made it is
	// recorded as succeeded so no brake counts it, and the next selection buys
	// another run of the same diagnosis.
	if !tracker.Item.Parking.Parked() {
		t.Fatalf("the item went back to the backlog unparked; calls = %v", tracker.Calls)
	}
	if !strings.Contains(tracker.Item.Parking.Reason(), "the anchor stays open") {
		t.Errorf("the parking reason does not name what would release the item: %q", tracker.Item.Parking)
	}
	if len(tracker.Blockers) > 0 {
		t.Errorf("a landing that named no impediment made the item wait on one: %v", tracker.Blockers)
	}
	// The notes an operator reads must not open by describing a completed item.
	if !strings.Contains(tracker.Notes, "the item is parked") {
		t.Errorf("the recorded outcome reads as a discharged item: %q", tracker.Notes)
	}
	if outcome.Landing != landing.OutcomeEvidence {
		t.Errorf("outcome landing = %q, want %q", outcome.Landing, landing.OutcomeEvidence)
	}
	// And the claim is durable, because the closure is not always made by the
	// process that read it.
	recorded := testharness.OnlyRecordedRun(t, store)
	if recorded.Outcome != runstate.OutcomeSucceeded {
		t.Errorf("run outcome = %q, want %q: landing evidence is not a failed run", recorded.Outcome, runstate.OutcomeSucceeded)
	}
}

// yoyodyne-ifd.284 replayed exactly as it happened, with the one thing it was
// missing: the developer writes no block at all, so the claim in force is the
// default that closes the item, and the reviewer reads the change for what it is
// and approves it as evidence. The change is promoted as any approved change is,
// and the item stays open — parked, in the reviewer's own words, which is the
// sentence it was already writing into its summary when the item closed against
// its own diagnosis.
func TestAnApprovalOfEvidenceLeavesTheItemOpenInTheReviewersWords(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker()
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveEvidenceVerdict)
	provider.DeveloperFinalText = "the machinery this item is written against has not landed; this change is the diagnosis"
	pipeline, store := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The approval is an approval: the evidence landed and was promoted, and the
	// run succeeded.
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("an approval of evidence did not integrate: %#v", outcome)
	}
	if outcome.ReviewDecision != review.DecisionApprove || outcome.ReviewApproves != review.ApprovesEvidence {
		t.Fatalf("the run did not record what the approval approved: decision = %q, approves = %q",
			outcome.ReviewDecision, outcome.ReviewApproves)
	}
	// And it closes nothing. The developer claimed the ordinary landing, which is
	// the claim that used to decide this on its own.
	if outcome.Landing != "" {
		t.Fatalf("the developer claimed %q; this replay is the run that claimed nothing", outcome.Landing)
	}
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("the item closed against a change its reviewer called evidence; calls = %v", tracker.Calls)
	}
	if !tracker.Reopened || tracker.Item.Status != "open" {
		t.Fatalf("the item was left claimed by a run that has ended; calls = %v", tracker.Calls)
	}
	// Parked, because the marker is the developer's channel and a reviewer has
	// none: an item returned bare is the one the next pull selects.
	if !tracker.Item.Parking.Parked() {
		t.Fatalf("the item went back to the backlog unparked; calls = %v", tracker.Calls)
	}
	// In the reviewer's words. Nobody else wrote an account of this decision.
	for _, where := range []string{tracker.Item.Parking.Reason(), tracker.ReopenReason, tracker.Notes} {
		if !strings.Contains(where, "the design it needs has not landed") {
			t.Errorf("the item does not carry the reviewer's account: %q", where)
		}
	}
	if !strings.Contains(tracker.Notes, "approved the change as evidence") {
		t.Errorf("the notes do not say which reader withheld the closure: %q", tracker.Notes)
	}
	// The record is durable, because the closure is not always made by the process
	// that read the verdict.
	recorded := testharness.OnlyRecordedRun(t, store)
	if recorded.Outcome != runstate.OutcomeSucceeded {
		t.Errorf("run outcome = %q, want %q: approving evidence is not a failed run", recorded.Outcome, runstate.OutcomeSucceeded)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReviewApproves != runstate.ApprovesEvidence || state.Discharges() {
		t.Errorf("the durable record does not withhold the closure: approves = %q, discharges = %t",
			state.ReviewApproves, state.Discharges())
	}
}

// An approval that never said what it approves is asked for once more rather
// than settled from the answer nobody gave. The change is built, checked, and
// waiting on one invocation, and the default it would fall back to is the
// closing one.
func TestAnApprovalThatSaysNothingIsAskedForAgain(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker()
	unstated := `{"decision":"approve","summary":"the change is fine"}`
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, unstated, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the re-asked review did not settle the run: %#v, closed = %t", outcome, tracker.Closed)
	}
	// The re-ask is a second review rather than a second developer attempt: the
	// change said nothing wrong, the reviewer did.
	if outcome.RepairAttempts != 0 {
		t.Errorf("repair attempts = %d, want the change handed back to nobody", outcome.RepairAttempts)
	}
	if outcome.ReviewApproves != review.ApprovesImplementation {
		t.Errorf("the run recorded approves = %q, want the kind the second verdict stated", outcome.ReviewApproves)
	}
}

// The one alternative to the parking. A developer that can name the impediment
// as another work item asks for the item to be left open waiting on it, which is
// a marker selection honours and which releases itself when the impediment
// closes — where the parking waits for a person.
func TestALandingThatNamesItsImpedimentLeavesTheItemOpenWaitingOnIt(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker().Holds("yoyodyne-impediment")
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
	provider.DeveloperFinalText = "the diagnosis\n\n" +
		testharness.LandingBlock(`{"outcome":"evidence","why":"the conversion needs yoyodyne-impediment to land first","blocked_by":"yoyodyne-impediment"}`)
	pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("a landing that does not discharge closed its item; calls = %v", tracker.Calls)
	}
	if tracker.Item.Parking.Parked() {
		t.Errorf("an item that names what it waits on was parked as well: %q", tracker.Item.Parking)
	}
	if len(tracker.Blockers) != 1 || tracker.Blockers[0] != "yoyodyne-impediment" {
		t.Fatalf("the item was not made to wait on the impediment it named: %v", tracker.Blockers)
	}
	// The dependency is recorded before the status. Between an item being made
	// open and being made to wait there is a window a watch session polling the
	// queue pulls it in, which is the bare openness this marker replaces.
	blocker := slices.Index(tracker.Calls, "blocker")
	reopen := slices.Index(tracker.Calls, "reopen")
	if blocker < 0 || reopen < 0 || blocker > reopen {
		t.Errorf("the item was returned to the backlog before it waited on anything: %v", tracker.Calls)
	}
	if outcome.LandingBlockedBy != "yoyodyne-impediment" {
		t.Errorf("outcome landing marker = %q, want the item it named", outcome.LandingBlockedBy)
	}
	if !strings.Contains(tracker.Notes, "yoyodyne-impediment") {
		t.Errorf("the item's notes do not say what it is waiting for: %q", tracker.Notes)
	}
}

// The marker is developer-written text, and the shape check it passes proves only
// that it is identifier-shaped. Two different things go wrong with a name and
// both end here. A marker the tracker would refuse a dependency for fails the
// settlement of a run whose change is already integrated, leaving the item
// claimed with nothing watching it. A marker the tracker would accept and that
// holds nothing back is worse, because it looks like it worked: the item goes
// back unparked behind a satisfied dependency and the next pull selects it, which
// is the loop this whole item exists to close. Each of them takes the parking,
// which holds the item back without needing anything to be true of the tracker.
func TestAMarkerTheHarnessCannotUseTakesTheParkingInstead(t *testing.T) {
	t.Parallel()

	for _, unusable := range []struct {
		name    string
		marker  func(tracker *orchestratortest.Tracker) string
		saysWhy string
	}{
		{
			// Nothing waits on itself, and the tracker refuses the dependency as a
			// cycle.
			name:    "a marker naming the item it was claimed on",
			marker:  func(tracker *orchestratortest.Tracker) string { return tracker.Item.ID },
			saysWhy: "nothing waits on itself",
		},
		{
			// The developer cannot open a work item, so a marker naming one that was
			// never admitted is the ordinary way this goes wrong.
			name:    "a marker naming work the tracker does not have",
			marker:  func(*orchestratortest.Tracker) string { return "yoyodyne-never-admitted" },
			saysWhy: "the tracker did not confirm that item",
		},
		{
			// The dependency would be written and would hold nothing: every gate in
			// the harness reads a closed blocker as no blocker, so the item would sit
			// in the queue unparked behind it.
			name: "a marker naming work that is already closed",
			marker: func(tracker *orchestratortest.Tracker) string {
				tracker.HoldsItem(beads.WorkItem{ID: "yoyodyne-already-done", Title: "Done", Status: "closed"})
				return "yoyodyne-already-done"
			},
			saysWhy: "already closed",
		},
		{
			// A follow-on item that depends on the item it follows makes this shape
			// ordinary rather than exotic, and the tracker refuses the second edge as
			// a cycle.
			name: "a marker naming work that already waits on this item",
			marker: func(tracker *orchestratortest.Tracker) string {
				tracker.HoldsItem(beads.WorkItem{
					ID: "yoyodyne-follows-this", Title: "Follow-on", Status: "open",
					Dependencies: []beads.Dependency{{ID: tracker.Item.ID, Type: "blocks"}},
				})
				return "yoyodyne-follows-this"
			},
			saysWhy: "already waits on this item",
		},
	} {
		t.Run(unusable.name, func(t *testing.T) {
			t.Parallel()

			tracker := testharness.NewOutcomeTracker()
			provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
			provider.DeveloperFinalText = "the diagnosis\n\n" +
				testharness.LandingBlock(`{"outcome":"evidence","why":"not doable yet","blocked_by":"`+unusable.marker(tracker)+`"}`)
			pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			// The run finishes. A marker the harness cannot use must not cost a run
			// whose change has already been promoted.
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
				t.Fatalf("an unusable marker cost the run its integration: %#v", outcome)
			}
			if len(tracker.Blockers) > 0 {
				t.Fatalf("the item was made to wait on a marker nothing could use: %v", tracker.Blockers)
			}
			if !tracker.Item.Parking.Parked() {
				t.Fatalf("a marker that named nothing usable left the item unparked; calls = %v", tracker.Calls)
			}
			if outcome.LandingBlockedBy != "" {
				t.Errorf("outcome landing marker = %q, want none", outcome.LandingBlockedBy)
			}
			// The request is recorded rather than dropped: an item parked with no
			// trace of it reads afterwards as a developer that asked for nothing.
			if !strings.Contains(outcome.LandingImpedimentProblem, unusable.saysWhy) {
				t.Errorf("the outcome does not say why the marker was not used: %q", outcome.LandingImpedimentProblem)
			}
			if !strings.Contains(tracker.Item.Parking.Reason(), unusable.saysWhy) {
				t.Errorf("the parking reason does not say why the item is parked rather than waiting: %q", tracker.Item.Parking)
			}
			if !strings.Contains(tracker.Notes, unusable.saysWhy) {
				t.Errorf("the item's notes do not say why the marker was not used: %q", tracker.Notes)
			}
		})
	}
}

// The refusal the resolution above cannot see. The marker names work the tracker
// has, that is open, and that does not already wait on this item, and the tracker
// refuses the dependency anyway — a cycle further round the graph than the one
// read that resolution makes. The item is parked, and the whole record has to say
// so: the disposition every surface prints is derived from the run's own landing
// fields, and the outcome notes an operator reads are written from them before the
// item's status is settled at all.
func TestADependencyRefusedAtTheSettlementSaysParkedOnEverySurface(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker().Holds("yoyodyne-impediment")
	tracker.BlockerErr = errors.New("bd: adding this dependency would create a cycle")
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
	provider.DeveloperFinalText = "the diagnosis\n\n" +
		testharness.LandingBlock(`{"outcome":"evidence","why":"the conversion needs yoyodyne-impediment to land first","blocked_by":"yoyodyne-impediment"}`)
	pipeline, store := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	// The run finishes. A dependency the tracker will not write must not cost a run
	// whose change has already been promoted.
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("a refused dependency cost the run its integration: %#v", outcome)
	}
	// Where the item actually went.
	if !tracker.Item.Parking.Parked() {
		t.Fatalf("the item went back unparked behind a dependency the tracker refused; calls = %v", tracker.Calls)
	}
	// And what every surface says about it. `yoyo status` and the conversation both
	// derive the disposition from these two fields, so a run that kept the marker
	// tells them the item stays open waiting on work it is not waiting on.
	if outcome.LandingBlockedBy != "" {
		t.Errorf("outcome landing marker = %q, want none: the tracker refused the dependency", outcome.LandingBlockedBy)
	}
	if !strings.Contains(outcome.LandingImpedimentProblem, "would not make this item wait") {
		t.Errorf("the outcome does not say why the item was parked instead: %q", outcome.LandingImpedimentProblem)
	}
	if got := outcome.UndischargedDisposition(); !strings.Contains(got, "parked") {
		t.Errorf("UndischargedDisposition() = %q, want the parking the item actually got", got)
	}
	// The notes are recorded before the item's status is settled, so this is what
	// asserts the arrangement happens ahead of them rather than after.
	if strings.Contains(tracker.Notes, "stays open waiting on") {
		t.Errorf("the recorded outcome names a disposition the item did not get: %q", tracker.Notes)
	}
	if !strings.Contains(tracker.Notes, "the item is parked") {
		t.Errorf("the recorded outcome does not say the item was parked: %q", tracker.Notes)
	}
	// And the durable record, because the run that made the claim is not always the
	// process that describes it afterwards.
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.LandingBlockedBy != "" {
		t.Errorf("the run's record still carries the marker the tracker refused: %q", recorded.LandingBlockedBy)
	}
	if !strings.Contains(recorded.LandingImpedimentProblem, "would not make this item wait") {
		t.Errorf("the run's record does not say why the item was parked: %q", recorded.LandingImpedimentProblem)
	}
}

// The whole point of the marker, replayed through the derivation selection
// actually reads: an item a run did not discharge is offered by no pull until
// somebody releases it, or until the impediment it names closes. Before this it
// went back as ordinary open work, and the next pull bought another run and
// another diagnosis of the same impediment.
func TestSelectionNeverPicksAnUndischargedItemUntilItIsReleased(t *testing.T) {
	t.Parallel()

	// pulls is how many readings of the queue the item has to survive. One would
	// prove it; a handful is what a watch session actually makes while nothing
	// about the item changes.
	const pulls = 3

	t.Run("the parking default", func(t *testing.T) {
		t.Parallel()

		tracker := testharness.NewOutcomeTracker()
		provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
		provider.DeveloperFinalText = "the diagnosis\n\n" +
			testharness.LandingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`)
		pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})
		if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		// The tracker reports the item as ready, because it is open and nothing
		// blocks it — which is exactly why the parking has to be the thing that
		// holds it back. Selection is asked about the axis the tracker does not know.
		settled := tracker.Item
		for pull := 1; pull <= pulls; pull++ {
			queue := backlog.Order([]beads.WorkItem{settled}, []string{settled.ID}, backlog.ReadHolds(nil), nil)
			if next, ok := queue.Next(); ok {
				t.Fatalf("pull %d selected the item its own landing parked: %s", pull, next.ID)
			}
			if queue.Parked() != 1 {
				t.Fatalf("pull %d does not report the item as parked: %+v", pull, queue.Entries)
			}
		}
		// And it is a parking rather than a disappearance: releasing it is one act,
		// and the item is pulled the next time the queue is read.
		released := settled
		released.Parking = ""
		queue := backlog.Order([]beads.WorkItem{released}, []string{released.ID}, backlog.ReadHolds(nil), nil)
		if next, ok := queue.Next(); !ok || next.ID != released.ID {
			t.Fatalf("the released item was not pulled: %+v", queue.Entries)
		}
	})

	t.Run("the impediment a landing named", func(t *testing.T) {
		t.Parallel()

		tracker := testharness.NewOutcomeTracker().Holds("yoyodyne-impediment")
		provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
		provider.DeveloperFinalText = "the diagnosis\n\n" +
			testharness.LandingBlock(`{"outcome":"evidence","why":"it needs yoyodyne-impediment first","blocked_by":"yoyodyne-impediment"}`)
		pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})
		if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		// This branch is asked of the harness's own dependency gate rather than of
		// the queue. An open item's readiness is the tracker's answer and the queue
		// only relays it, so a test that assembled that answer itself would be
		// asserting its own bookkeeping; the gate every run passes through on the way
		// in is this repository's code and reads the same relation.
		calls := len(tracker.Calls)
		for pull := 1; pull <= pulls; pull++ {
			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("pull %d error = %v", pull, err)
			}
			if !outcome.Paused || outcome.PausedByDependency == nil {
				t.Fatalf("pull %d started work on an item waiting for its impediment: %#v", pull, outcome)
			}
			if !slices.Contains(outcome.PausedByDependency.Blockers, "yoyodyne-impediment") {
				t.Errorf("pull %d does not name what the item waits for: %v", pull, outcome.PausedByDependency.Blockers)
			}
		}
		// Nothing was claimed and no developer ran, which is what picking it zero
		// times has to mean: a refusal that still spent a run is the cost this exists
		// to avoid.
		if extra := tracker.Calls[calls:]; len(extra) > 0 {
			t.Errorf("the refused pulls still wrote to the tracker: %v", extra)
		}
		// The release nobody has to remember to make: the impediment closes and the
		// same gate lets the work through, which is what the marker buys over the
		// parking.
		for index := range tracker.Item.Dependencies {
			tracker.Item.Dependencies[index].Status = "closed"
		}
		// A fresh pipeline over the same tracker, because a run identifier is spent
		// once and this fixture's is fixed. What is being asked is the gate, and the
		// gate decides from the item rather than from the run store.
		released, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})
		outcome, err := released.Run(context.Background(), tracker.Item.ID)
		if err != nil {
			t.Fatalf("Run() after the impediment closed error = %v", err)
		}
		if outcome.Paused {
			t.Fatalf("the item was still held back after its impediment closed: %#v", outcome)
		}
		if !tracker.Claimed {
			t.Error("the released item was never claimed, so nothing actually picked it up")
		}
	})
}

// The other kind, and the one nearly every run is. A developer that says nothing
// about its landing has discharged the item, and so has one that says so.
func TestADischargingLandingClosesItsItem(t *testing.T) {
	t.Parallel()

	for _, claimed := range []struct {
		name  string
		reply string
	}{
		{name: "a reply that claims nothing", reply: "implemented the work item"},
		{
			name: "a reply that claims the discharge",
			reply: "implemented the work item\n\n" +
				testharness.LandingBlock(`{"outcome":"discharged","why":"the acceptance criteria are met and the suite covers them"}`),
		},
	} {
		t.Run(claimed.name, func(t *testing.T) {
			t.Parallel()

			tracker := testharness.NewOutcomeTracker()
			provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
			provider.DeveloperFinalText = claimed.reply
			pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !outcome.WorkItemClosed || !tracker.Closed || tracker.Item.Status != "closed" {
				t.Fatalf("a discharging landing did not close its item: outcome = %#v, calls = %v", outcome, tracker.Calls)
			}
			if tracker.Reopened {
				t.Errorf("a discharging landing put its item back in the backlog; calls = %v", tracker.Calls)
			}
		})
	}
}

// A claim that arrived and could not be read is not a claim that was never made.
// The developer wrote a block, which means it was trying to say something about
// whether the item closes, and the safe reading of an unreadable one is the
// recoverable direction: an item left open is something a person can settle, and
// a false closure is the thing nobody sees.
func TestAnUnreadableLandingClaimWithholdsTheClosure(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker()
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
	provider.DeveloperFinalText = "worked on it\n\n" + testharness.LandingBlock(`{"outcome":"partly","why":"some of it"}`)
	pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil {
		t.Fatalf("an unreadable claim cost the run its integration: %#v", outcome)
	}
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("the item closed on a claim nobody could read; calls = %v", tracker.Calls)
	}
	if outcome.LandingProblem == "" {
		t.Fatal("an unreadable claim left no trace on the outcome")
	}
	if !tracker.Reopened || !strings.Contains(tracker.ReopenReason, "could not be read") {
		t.Errorf("the item does not say why it was not closed: %q", tracker.ReopenReason)
	}
	// The prose the developer wrote is the run's evidence, and a refused claim
	// must not take it.
	if !strings.Contains(outcome.Summary, "worked on it") {
		t.Errorf("a refused claim cost the run its summary: %q", outcome.Summary)
	}
}

// A repair round that finishes the work must not be closed against the previous
// attempt's evidence claim. The claim describes the change as it now stands.
func TestALaterAttemptReplacesTheLandingTheEarlierOneClaimed(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker()
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.RepairVerdict, testharness.ApproveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{
		"not doable yet\n\n" + testharness.LandingBlock(`{"outcome":"evidence","why":"the dependency has not landed"}`),
		"the reviewer was right, and it was doable after all",
	}
	pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Landing != "" || outcome.LandingReason != "" {
		t.Errorf("the second attempt inherited the first one's claim: %q / %q", outcome.Landing, outcome.LandingReason)
	}
	if !outcome.WorkItemClosed || tracker.Reopened {
		t.Fatalf("the finished work was not closed: outcome = %#v, calls = %v", outcome, tracker.Calls)
	}
}

// The reviewer is the only reader that sees the claim beside the change, which
// is what lets a diagnosis be judged as a diagnosis rather than as a missing
// implementation. It reaches the reviewer as untrusted evidence, because the
// developer wrote it.
func TestTheReviewerIsShownWhichLandingWasClaimed(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker()
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
	provider.DeveloperFinalText = "the diagnosis\n\n" +
		testharness.LandingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`)
	var reviewerPrompt string
	underlying := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			reviewerPrompt = request.Prompt
		}
		return underlying(request)
	}
	pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(reviewerPrompt, "Claimed landing outcome") {
		t.Fatal("the reviewer was not shown the claim at all")
	}
	if !strings.Contains(reviewerPrompt, "does not discharge the work item") {
		t.Errorf("the reviewer was not told which landing was claimed: %q", reviewerPrompt)
	}
	if !strings.Contains(reviewerPrompt, "the design this needs has not landed") {
		t.Errorf("the reviewer was not told why: %q", reviewerPrompt)
	}
	// The claim is the developer's, so it belongs under the untrusted heading
	// rather than beside the invariants the harness supplied.
	claimAt := strings.Index(reviewerPrompt, "Claimed landing outcome")
	untrustedAt := strings.Index(reviewerPrompt, "# Untrusted review evidence")
	if untrustedAt < 0 || claimAt < untrustedAt {
		t.Error("the claim was presented outside the untrusted evidence the developer produced")
	}
}

// The claim reaches two readers that want opposite things from it. A reviewer is
// being asked to decide, so it is told what turns on the claim; a work item's
// notes are what somebody reads once every decision has been taken, and "ask for
// changes" there is an instruction addressed to nobody who can take it. So the
// direction is added where the reviewer is prompted and never travels to the
// item, on either landing.
func TestTheReviewersDirectionNeverReachesTheWorkItem(t *testing.T) {
	t.Parallel()

	for _, landed := range []struct {
		name string
		// reply is what the developer said, and directed is whether a reviewer
		// looking at it should be told what approving it does — which is the
		// question only a landing that closes the item raises.
		reply    string
		directed bool
	}{
		{name: "the default nobody writes", reply: "implemented the work item", directed: true},
		{
			name: "an evidence claim",
			reply: "the diagnosis\n\n" +
				testharness.LandingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`),
			directed: false,
		},
	} {
		t.Run(landed.name, func(t *testing.T) {
			t.Parallel()

			tracker := testharness.NewOutcomeTracker()
			provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
			provider.DeveloperFinalText = landed.reply
			var reviewerPrompt string
			underlying := provider.Respond
			provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
				if request.Role == domain.RoleReviewer {
					reviewerPrompt = request.Prompt
				}
				return underlying(request)
			}
			pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})

			if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			// Everything the run leaves on the item, read together: the outcome notes,
			// the reason it was closed with, the reason it went back to the backlog,
			// and the parking it sits under.
			onTheItem := strings.Join([]string{
				tracker.Notes, tracker.CloseReason, tracker.ReopenReason, tracker.Item.Parking.Reason(),
			}, "\n")
			for _, directed := range []string{"Judge the change against that claim", "Approve it as evidence instead"} {
				if strings.Contains(onTheItem, directed) {
					t.Errorf("the item carries a direction written for the reviewer (%q): %q", directed, onTheItem)
				}
			}
			// The item still says which landing it was settled by, or the split would
			// have been made by dropping the claim rather than by placing it.
			if !strings.Contains(onTheItem, "Landing claim") && !strings.Contains(onTheItem, "integrated") {
				t.Errorf("the item says nothing about how it was settled: %q", onTheItem)
			}
			if got := strings.Contains(reviewerPrompt, "Approve it as evidence instead"); got != landed.directed {
				t.Errorf("the reviewer was told what approving the claim does = %v, want %v: %q", got, landed.directed, reviewerPrompt)
			}
		})
	}
}

// yoyodyne-ifd.284 replayed. A developer files a diagnosis saying the machinery
// its item needs has not landed, and writes no landing block — so the ordinary
// discharging landing is in force and the item closes on a document saying in its
// own words that the work was not done. That is what happened, after the claim
// channel shipped, because the claim nobody writes was shown to nobody: the
// reviewer approved a diagnosis without ever being told that approving it closed
// the item.
//
// The default is a claim like any other now, and the reviewer is shown it. A
// diagnosis offered under it is a change that is not the work, and the reviewer
// has two answers for that: approve it as evidence, which the test above this one
// is, or send it back — after which the repair round is where the developer
// claims what it actually landed. This is that second route, and it leaves the
// item parked rather than closed just as the first does.
func TestADiagnosisWithNoClaimIsSentBackRatherThanClosingItsItem(t *testing.T) {
	t.Parallel()

	tracker := testharness.NewOutcomeTracker()
	provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.RepairVerdict, testharness.ApproveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{
		"the recurring-task machinery this item needs is on a branch that has not landed; this change is the diagnosis",
		"the diagnosis stands\n\n" +
			testharness.LandingBlock(`{"outcome":"evidence","why":"the machinery this item is written against has not landed"}`),
	}
	var reviewerPrompts []string
	underlying := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			reviewerPrompts = append(reviewerPrompts, request.Prompt)
		}
		return underlying(request)
	}
	pipeline, _ := testharness.NewAutomaticPipeline(t, testharness.PipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The first review is the one that mattered: the change in front of it was a
	// diagnosis, and it has to be told that approving it closes the item.
	if len(reviewerPrompts) == 0 {
		t.Fatal("the change was never reviewed")
	}
	if !strings.Contains(reviewerPrompts[0], "claimed no landing outcome") {
		t.Errorf("the reviewer was not told the default claim was in force: %q", reviewerPrompts[0])
	}
	if !strings.Contains(reviewerPrompts[0], "Approve it as evidence instead") {
		t.Errorf("the reviewer was not told what approving the default does: %q", reviewerPrompts[0])
	}
	// And the item is where the evidence says it belongs, not closed against it.
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("the item closed against a diagnosis; calls = %v", tracker.Calls)
	}
	if !tracker.Reopened || !tracker.Item.Parking.Parked() {
		t.Fatalf("the item was not put back parked; calls = %v", tracker.Calls)
	}
	if !strings.Contains(tracker.Item.Parking.Reason(), "has not landed") {
		t.Errorf("the parking reason does not name what would release the item: %q", tracker.Item.Parking)
	}
}

// A sweep settling a run somebody killed decides the same way the run would
// have. It has to: a run whose merge the forge only queued is settled by a later
// process entirely, and a sweep reading the promotion alone is exactly the
// closure this record exists to stop.
func TestReconciliationSettlesAnInterruptedRunByTheLandingItClaimed(t *testing.T) {
	t.Parallel()

	for _, settled := range []struct {
		name       string
		reply      string
		wantClosed bool
		wantParked bool
		wantWaits  string
	}{
		{
			name:       "a landing that discharges the item",
			reply:      "implemented the work item",
			wantClosed: true,
		},
		{
			name: "a landing that does not",
			reply: "the diagnosis\n\n" +
				testharness.LandingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`),
			wantParked: true,
		},
		{
			// The sweep decides where the item goes as well as whether it closes: a
			// sweep that read only the promotion would park an item whose own landing
			// asked for the dependency, and one that read only the outcome would put
			// it back bare.
			name: "a landing that named the impediment it waits on",
			reply: "the diagnosis\n\n" +
				testharness.LandingBlock(`{"outcome":"evidence","why":"it needs yoyodyne-impediment first","blocked_by":"yoyodyne-impediment"}`),
			wantWaits: "yoyodyne-impediment",
		},
	} {
		t.Run(settled.name, func(t *testing.T) {
			t.Parallel()

			repository, worktreeRoot, store := testharness.RestartableFixture(t)
			tracker := (&orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}).
				Holds("yoyodyne-impediment")
			provider := orchestratortest.RoleBackend(testharness.WriteFeature, testharness.ApproveVerdict)
			provider.DeveloperFinalText = settled.reply
			// Killed after the promotion and before the item was settled, which is
			// the boundary reconciliation exists for.
			halting := &testharness.HaltingStore{StateStore: store, At: runstate.PhaseCompleting}
			pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
			if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
				t.Fatal("interrupted Run() error = nil")
			}
			if tracker.Closed || tracker.Reopened {
				t.Fatalf("the interrupted run settled the item itself; calls = %v", tracker.Calls)
			}

			results := testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
			if len(results) != 1 {
				t.Fatalf("reconciled %d runs, want the one this test made", len(results))
			}
			if tracker.Closed != settled.wantClosed {
				t.Errorf("closed = %t, want %t; calls = %v", tracker.Closed, settled.wantClosed, tracker.Calls)
			}
			if tracker.Reopened == settled.wantClosed {
				t.Errorf("reopened = %t, want %t; calls = %v", tracker.Reopened, !settled.wantClosed, tracker.Calls)
			}
			if tracker.Item.Parking.Parked() != settled.wantParked {
				t.Errorf("parked = %q, want parked = %t", tracker.Item.Parking, settled.wantParked)
			}
			if waits := strings.Join(tracker.Blockers, ","); waits != settled.wantWaits {
				t.Errorf("the item waits on %q, want %q", waits, settled.wantWaits)
			}
			// The words the sweep leaves say which way it went. The note is written
			// before the item is settled and the detail is what the operator is
			// shown, so a "closed" on either of them for an item the sweep put back
			// is the one wrong word on exactly the record a reopened item's reader
			// turns to first.
			said, unsaid := "the item is being closed", "put back in the backlog"
			if !settled.wantClosed {
				said, unsaid = unsaid, said
			}
			if !strings.Contains(tracker.Notes, said) || strings.Contains(tracker.Notes, unsaid) {
				t.Errorf("the reconciled note says %q and not %q; notes = %q", said, unsaid, tracker.Notes)
			}
			said, unsaid = "closed", "put back in the backlog undischarged"
			if !settled.wantClosed {
				said, unsaid = unsaid, said
			}
			if detail := results[0].Detail; !strings.Contains(detail, said) || strings.Contains(detail, unsaid) {
				t.Errorf("Detail = %q, want it to say %q and not %q", detail, said, unsaid)
			}
			// Whichever way it went, settling it twice settles it once: the second
			// sweep finds the item already in the state this run's landing calls for.
			calls := len(tracker.Calls)
			testharness.ReconcileSweep(t, repository, worktreeRoot, store, tracker)
			if extra := tracker.Calls[calls:]; testharness.CountCalls(extra, "complete")+testharness.CountCalls(extra, "reopen")+testharness.CountCalls(extra, "blocker") > 0 {
				t.Errorf("a second sweep settled the item again: %v", extra)
			}
		})
	}
}

// A claim with no account of itself is refused by the durable schema, because
// the account is the half of the record anybody reads afterwards: an item left
// open for a reason nobody wrote down is the false closure's quieter cousin.
func TestTheDurableSchemaRefusesALandingClaimWithNoReason(t *testing.T) {
	t.Parallel()

	now := execution.RealClock{}.Now()
	state := runstate.State{
		SchemaVersion:  runstate.StateSchemaVersion,
		RunID:          "run-abcdef0123456789abcdef0123456789",
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		WorkItemID:     "yoyodyne-task",
		Backend:        "claude-code",
		Status:         runstate.StatusRunning,
		StartedAt:      now,
		UpdatedAt:      now,
		LandingOutcome: runstate.LandingEvidence,
	}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "landing_reason") {
		t.Fatalf("Validate() error = %v, want a refusal naming the missing reason", err)
	}
	state.LandingReason = "the design this needs has not landed"
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() refused a complete claim: %v", err)
	}
	// The marker only means anything on a landing that leaves the item open, and
	// the settlement reads it: a record carrying one against a closure would have
	// the item closed and waiting at once.
	state.LandingBlockedBy = "yoyodyne-impediment"
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() refused a marker on the landing it belongs to: %v", err)
	}
	state.LandingOutcome = runstate.LandingDischarged
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "landing_blocked_by") {
		t.Fatalf("Validate() error = %v, want a refusal naming the marker on a discharge", err)
	}
	// The marker the schema stores is one the harness resolved, so the two ways it
	// could be unresolvable are refused rather than stored and acted on: nothing
	// waits on itself, and a marker and the reason a marker could not be used are
	// two different settlements to record at once.
	state.LandingOutcome = runstate.LandingEvidence
	state.LandingBlockedBy = state.WorkItemID
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "cannot name the work item it was claimed on") {
		t.Fatalf("Validate() error = %v, want a refusal of a marker naming its own item", err)
	}
	state.LandingBlockedBy = "yoyodyne-impediment"
	state.LandingImpedimentProblem = "the tracker did not confirm that item"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "cannot both be recorded") {
		t.Fatalf("Validate() error = %v, want a refusal of a marker recorded beside its own problem", err)
	}
	state.LandingBlockedBy = ""
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() refused a landing parked because its marker was unusable: %v", err)
	}
}

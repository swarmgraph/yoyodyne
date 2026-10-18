package orchestrator

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
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// landingBlock is the claim as a developer's reply carries it.
func landingBlock(payload string) string {
	return landing.Fence + "\n" + payload + "\n```\n"
}

// The settlement is re-runnable, which is not a nicety: the reopen is retried
// where the tracker was busy, and a sweep re-runs the whole settlement of a run
// whose reopen never landed. A second run of it must not add the dependency
// again, because the tracker refuses a duplicate and the settlement it would fail
// is that of a run whose change is already integrated.
func TestSettlingAnUndischargedItemTwiceMakesItWaitOnce(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "in_progress"}}
	state := runstate.State{
		RunID:            "run-abcdef0123456789abcdef0123456789",
		WorkItemID:       tracker.Item.ID,
		TargetBranch:     "main",
		LandingOutcome:   runstate.LandingEvidence,
		LandingReason:    "it needs yoyodyne-impediment first",
		LandingBlockedBy: "yoyodyne-impediment",
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := settleUndischarged(context.Background(), tracker, state); err != nil {
			t.Fatalf("settleUndischarged() attempt %d error = %v", attempt, err)
		}
	}
	if len(tracker.Blockers) != 1 {
		t.Fatalf("the item was made to wait %d times: %v", len(tracker.Blockers), tracker.Blockers)
	}
	if tracker.Item.Parking.Parked() {
		t.Errorf("an item waiting on its impediment was parked as well: %q", tracker.Item.Parking)
	}
}

// Every refusal the harness can see is decided when the claim is read, and one it
// cannot is a cycle further round the graph than the one read it makes. So a
// dependency the tracker refuses anyway takes the parking rather than failing the
// settlement: the change is already promoted, the run cannot be retried into a
// better state, and failing here leaves the item claimed with nothing watching
// it — which is the outcome this settlement exists to prevent.
func TestADependencyTheTrackerRefusesParksTheItemRatherThanFailingTheSettlement(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "in_progress"}}
	tracker.BlockerErr = errors.New("bd: adding this dependency would create a cycle")
	state := runstate.State{
		RunID:            "run-abcdef0123456789abcdef0123456789",
		WorkItemID:       tracker.Item.ID,
		TargetBranch:     "main",
		LandingOutcome:   runstate.LandingEvidence,
		LandingReason:    "it needs yoyodyne-impediment first",
		LandingBlockedBy: "yoyodyne-impediment",
	}
	settled, err := settleUndischarged(context.Background(), tracker, state)
	if err != nil {
		t.Fatalf("settleUndischarged() error = %v, want the parking fallback rather than a failure", err)
	}
	if tracker.Item.Status != "open" {
		t.Fatalf("the item was left claimed by a run that has ended: status = %q", tracker.Item.Status)
	}
	if !tracker.Item.Parking.Parked() {
		t.Fatalf("the item went back unparked behind a dependency that was refused; calls = %v", tracker.Calls)
	}
	// The settlement answers with the run's landing fields as they now stand. A
	// caller that recorded the state it went in with would say the item stays open
	// waiting on work the item was in fact parked for.
	if settled.LandingBlockedBy != "" {
		t.Errorf("the settled state still carries the marker the tracker refused: %q", settled.LandingBlockedBy)
	}
	if !strings.Contains(settled.LandingImpedimentProblem, "would not make this item wait") {
		t.Errorf("the settled state does not say why the item was parked instead: %q", settled.LandingImpedimentProblem)
	}
	// What the tracker would not do is on the item, because that is where somebody
	// reads it.
	if !strings.Contains(tracker.Item.Parking.Reason(), "would not make this item wait") {
		t.Errorf("the parking reason does not say the dependency was refused: %q", tracker.Item.Parking)
	}
	if !strings.Contains(tracker.Notes, "would not make this item wait") {
		t.Errorf("the item's notes do not say the dependency was refused: %q", tracker.Notes)
	}
}

// Deciding where an undischarged item goes reads the item, and a tracker that
// could not be read is not the run's failure: a run whose change is promoted
// leaves the settlement to ask again under the recovery it always had. So the
// read is reported rather than swallowed — a decision made from an item nobody
// saw would write a dependency the item may already carry and lift a parking
// somebody placed.
func TestDecidingWhereAnUndischargedItemGoesReportsATrackerItCouldNotRead(t *testing.T) {
	t.Parallel()

	// A tracker that holds some other work, so the item this run served is one it
	// cannot answer for.
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-other", Title: "Another", Status: "open"}}
	state := runstate.State{
		RunID:            "run-abcdef0123456789abcdef0123456789",
		WorkItemID:       "yoyodyne-task",
		TargetBranch:     "main",
		LandingOutcome:   runstate.LandingEvidence,
		LandingReason:    "it needs yoyodyne-impediment first",
		LandingBlockedBy: "yoyodyne-impediment",
	}
	if _, _, err := arrangeUndischarged(context.Background(), tracker, state); err == nil {
		t.Fatal("arrangeUndischarged() returned no error for an item it could not read")
	}
	if len(tracker.Blockers) > 0 {
		t.Errorf("a dependency was written for an item that was never read: %v", tracker.Blockers)
	}
	// The parking needs nothing of the read but the reason it supersedes, so it
	// settles anyway: a sentence lost there must not cost the settlement of a
	// change that is already promoted.
	parked := state
	parked.LandingBlockedBy = ""
	if _, err := settleUndischarged(context.Background(), tracker, parked); err != nil {
		t.Fatalf("settleUndischarged() error = %v, want the parking to settle without the read", err)
	}
}

// A settlement adds a dependency; it never lifts a parking. An item an operator
// parked and then named for a run anyway comes back still parked, because the
// operator's decision is not this run's to retire on the way past — and once the
// impediment closes, the parking is still what keeps the item out of the queue.
func TestSettlingAnItemLeftWaitingKeepsTheParkingItAlreadyCarried(t *testing.T) {
	t.Parallel()

	operatorParked := domain.WorkItemParking("deferred by the scope decision until the quarter turns")
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID: "yoyodyne-task", Title: "Task", Status: "in_progress", Parking: operatorParked,
	}}
	state := runstate.State{
		RunID:            "run-abcdef0123456789abcdef0123456789",
		WorkItemID:       tracker.Item.ID,
		TargetBranch:     "main",
		LandingOutcome:   runstate.LandingEvidence,
		LandingReason:    "it needs yoyodyne-impediment first",
		LandingBlockedBy: "yoyodyne-impediment",
	}
	if _, err := settleUndischarged(context.Background(), tracker, state); err != nil {
		t.Fatalf("settleUndischarged() error = %v", err)
	}
	if tracker.Item.Parking != operatorParked {
		t.Errorf("the settlement rewrote a parking somebody else placed: %q", tracker.Item.Parking)
	}
	if len(tracker.Blockers) != 1 {
		t.Errorf("the item was not made to wait on its impediment: %v", tracker.Blockers)
	}
}

// The parking default does replace the reason — both are parkings, and the run's
// names what would release the item now — so the one it superseded goes into the
// notes. A decision somebody took has to stay readable off the item; the parking
// field holds one value, and without this the only copy of theirs is whatever the
// tracker keeps of its own history.
func TestTheParkingDefaultRecordsTheReasonItSuperseded(t *testing.T) {
	t.Parallel()

	operatorParked := domain.WorkItemParking("deferred by the scope decision until the quarter turns")
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID: "yoyodyne-task", Title: "Task", Status: "in_progress", Parking: operatorParked,
	}}
	state := runstate.State{
		RunID:          "run-abcdef0123456789abcdef0123456789",
		WorkItemID:     tracker.Item.ID,
		TargetBranch:   "main",
		LandingOutcome: runstate.LandingEvidence,
		LandingReason:  "the design this needs has not landed",
	}
	if _, err := settleUndischarged(context.Background(), tracker, state); err != nil {
		t.Fatalf("settleUndischarged() error = %v", err)
	}
	if !strings.Contains(tracker.Item.Parking.Reason(), "the design this needs has not landed") {
		t.Errorf("the run's account did not become the parking reason: %q", tracker.Item.Parking)
	}
	if !strings.Contains(tracker.Notes, operatorParked.Reason()) {
		t.Errorf("the superseded parking reason was lost: %q", tracker.Notes)
	}
	// An item nobody had parked has nothing to supersede, and a note saying so
	// would be a sentence about a decision nobody took.
	unparked := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "in_progress"}}
	if _, err := settleUndischarged(context.Background(), unparked, state); err != nil {
		t.Fatalf("settleUndischarged() error = %v", err)
	}
	if strings.Contains(unparked.Notes, "replaces the parking reason") {
		t.Errorf("the notes claim a parking reason that never existed: %q", unparked.Notes)
	}
}

// A developer's account may run to the whole of what a claim can carry, and the
// parking reason has to stay one value the tracker will hold on one line. A
// reason the tracker refuses fails the settlement of a run whose change is
// already integrated, and the account is on the item as notes in full either way,
// so cutting it here costs a reader nothing.
func TestTheParkingReasonStaysInsideWhatTheTrackerHolds(t *testing.T) {
	t.Parallel()

	state := runstate.State{
		RunID:          "run-abcdef0123456789abcdef0123456789",
		WorkItemID:     "yoyodyne-task",
		LandingOutcome: runstate.LandingEvidence,
		LandingReason:  strings.Repeat("the design this needs has not landed\n", 200),
	}
	parking := undischargedParking(state)
	if !parking.Parked() {
		t.Fatal("a landing that parks produced no parking reason")
	}
	// The tracker's own two rules, asserted here because this is what writes one.
	if len(parking.Reason()) > domain.MaxWorkItemParkingBytes {
		t.Errorf("parking reason is %d bytes, past the %d the tracker holds", len(parking.Reason()), domain.MaxWorkItemParkingBytes)
	}
	if strings.ContainsAny(parking.Reason(), "\r\n") {
		t.Errorf("parking reason spans lines: %q", parking.Reason())
	}
	if !strings.Contains(parking.Reason(), state.RunID) {
		t.Errorf("parking reason does not name the run that parked the item: %q", parking.Reason())
	}
	// A landing that named its impediment is held back by that instead, so parking
	// it as well would take an item out of reach for a wait that releases itself.
	waiting := state
	waiting.LandingBlockedBy = "yoyodyne-impediment"
	if got := undischargedParking(waiting); got.Parked() {
		t.Errorf("a landing that named its impediment was parked as well: %q", got)
	}
}

// The derivation the closure reads is one derivation, so a run cannot be closed
// by the process that made it and left open by the sweep that settles it.
func TestTheClosureDerivationReadsTheSameRecordEverywhere(t *testing.T) {
	t.Parallel()

	for _, recorded := range []struct {
		name       string
		state      runstate.State
		discharges bool
		parks      bool
	}{
		{name: "a run that claimed nothing", state: runstate.State{}, discharges: true},
		{
			name:       "a run recorded before the channel existed",
			state:      runstate.State{Status: runstate.StatusSucceeded},
			discharges: true,
		},
		{
			name:       "a claimed discharge",
			state:      runstate.State{LandingOutcome: runstate.LandingDischarged, LandingReason: "done"},
			discharges: true,
		},
		{
			name:       "a claimed evidence landing",
			state:      runstate.State{LandingOutcome: runstate.LandingEvidence, LandingReason: "not yet"},
			discharges: false,
			parks:      true,
		},
		{
			name: "a claimed evidence landing that named its impediment",
			state: runstate.State{
				WorkItemID:       "yoyodyne-task",
				LandingOutcome:   runstate.LandingEvidence,
				LandingReason:    "not yet",
				LandingBlockedBy: "yoyodyne-impediment",
			},
			discharges: false,
		},
		{
			// The tracker refuses a self-dependency as a cycle, so a marker naming
			// the item it was claimed on holds nothing back. It takes the parking,
			// which is the disposition that does.
			name: "a claimed evidence landing that named its own item",
			state: runstate.State{
				WorkItemID:       "yoyodyne-task",
				LandingOutcome:   runstate.LandingEvidence,
				LandingReason:    "not yet",
				LandingBlockedBy: "yoyodyne-task",
			},
			discharges: false,
			parks:      true,
		},
		{
			name:       "a claim that could not be read",
			state:      runstate.State{LandingProblem: "decode landing claim: unexpected trailing content"},
			discharges: false,
			parks:      true,
		},
		{
			// The reviewer's half of the same question. The developer claimed the
			// ordinary landing — which is the claim nobody writes — and the reviewer
			// approved the change as evidence rather than as the work, which is the
			// yoyodyne-ifd.284 shape and has to leave the item exactly where an
			// evidence landing does.
			name: "an approval of evidence over the default claim",
			state: runstate.State{
				ReviewDecision: runstate.ReviewApprove,
				ReviewApproves: runstate.ApprovesEvidence,
				ReviewSummary:  "a sound diagnosis rather than the work",
			},
			discharges: false,
			parks:      true,
		},
		{
			name: "an approval of the implementation",
			state: runstate.State{
				ReviewDecision: runstate.ReviewApprove,
				ReviewApproves: runstate.ApprovesImplementation,
			},
			discharges: true,
		},
	} {
		t.Run(recorded.name, func(t *testing.T) {
			t.Parallel()

			if got := recorded.state.Discharges(); got != recorded.discharges {
				t.Fatalf("Discharges() = %t, want %t", got, recorded.discharges)
			}
			// Where the item is left is the other half of the same derivation, and it
			// is read by both settlement sites for the same reason: an item parked by
			// the run and reopened bare by the sweep is an item selection picks up
			// again the moment the sweep runs.
			if got := recorded.state.Parks(); got != recorded.parks {
				t.Fatalf("Parks() = %t, want %t", got, recorded.parks)
			}
			// An item already in the state the run calls for is settled, which
			// is what makes settling one twice settle it once.
			for status, want := range map[string]bool{"closed": true, "open": !recorded.discharges, "in_progress": false} {
				if got := itemSettled(recorded.state, status); got != want {
					t.Errorf("itemSettled(%q) = %t, want %t", status, got, want)
				}
			}
		})
	}
}

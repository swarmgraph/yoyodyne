package orchestrator

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// awaitedRepair is an item's triage record with a repair decided about the
// docketed run and not carried out yet: the grant reserves rounds the item has
// not spent, and the run records no continuation since the decision.
func awaitedRepair() runstate.TriageCounters {
	return runstate.TriageCounters{
		WorkItemID:      docketedItem,
		RepairGrants:    1,
		GrantedRounds:   2,
		CommittedRounds: 5,
		ReviewRounds:    3,
		Decisions: []runstate.TriageDecision{{
			Decision:     runstate.TriageDecisionRepair,
			RunID:        docketedRunID,
			Reason:       "move the kept change onto current main and check and review it again",
			DecidedBy:    "Development manager",
			Conversation: "chat-0123456789abcdef",
			Turn:         4,
			DecidedAt:    docketedNow.Add(-2 * time.Hour),
			Rounds:       2,
		}},
	}
}

// A run whose decided repair is waiting for a developer slot is not put to the
// development manager again as an unpublished change: her repair already
// answers the whole run.
func TestAPublicationIsNotDocketedWhileItsRunsDecidedRepairWaits(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	recorded := &recordedDecisions{counters: map[string]runstate.TriageCounters{docketedItem: awaitedRepair()}}
	docketer := docketerDeciding([]runstate.State{publishedState(3 * time.Hour)}, docket, recorded, recorded)
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 || len(built.Entries) != 0 || len(docket.entries) != 0 {
		t.Fatalf("build = %#v, docket = %#v; want the pull request left off while the repair waits", built, docket.entries)
	}

	// Once the repair has been carried out — the grant spent — the decision no
	// longer stands in the way, and a publication still stuck is docketed as any
	// other.
	carried := awaitedRepair()
	carried.ReviewRounds = carried.CommittedRounds
	recorded.counters[docketedItem] = carried
	built, err = docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if built.Added != 1 || len(built.Entries) != 1 || built.Entries[0].Class != triage.ClassPublication {
		t.Fatalf("build = %#v, want the stuck publication docketed once nothing is waiting to be carried out", built)
	}
}

// A publication entry docketed before the repair was decided, or whose wait has
// since lapsed, is not listed while the repair waits either: it is no question
// for her, and the harness still owes the run its repair.
func TestAnOpenPublicationEntryIsNotListedWhileItsRunsDecidedRepairWaits(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	docket.entries = append(docket.entries, triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.PublicationKey(docketedRunID, 42),
		Class:         triage.ClassPublication,
		ProductID:     "yoyodyne",
		RunID:         docketedRunID,
		WorkItemID:    docketedItem,
		RecordedAt:    docketedNow.Add(-time.Hour),
		Publication:   &triage.Publication{Number: 42, State: "OPEN", ApprovedAt: docketedNow.Add(-3 * time.Hour)},
	})
	// The wait she recorded on it has lapsed, which would put it back in front
	// of her.
	docket.waitOn(triage.PublicationKey(docketedRunID, 42), docketedNow.Add(-50*time.Minute), docketedNow.Add(-10*time.Minute))
	recorded := &recordedDecisions{counters: map[string]runstate.TriageCounters{docketedItem: awaitedRepair()}}
	built, err := docketerDeciding([]runstate.State{publishedState(3 * time.Hour)}, docket, recorded, recorded).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 0 {
		t.Fatalf("entries = %#v, want nothing listed while the repair waits for a slot", built.Entries)
	}

	// A repair the harness tried to carry out and was refused is hers again, and
	// the entry is listed.
	refused := awaitedRepair()
	refused.CarryOuts = []runstate.TriageCarryOut{{
		RunID:     docketedRunID,
		Decision:  runstate.TriageDecisionRepair,
		DecidedAt: refused.Decisions[0].DecidedAt,
		Gate:      runstate.TriageGateHarness,
		Refusal:   "the run's worktree was retired",
		Clears:    "a re-run instead",
		Attempts:  1,
		RefusedAt: docketedNow.Add(-5 * time.Minute),
	}}
	recorded.counters[docketedItem] = refused
	built, err = docketerDeciding([]runstate.State{publishedState(3 * time.Hour)}, docket, recorded, recorded).Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if len(built.Entries) != 1 || built.Entries[0].Class != triage.ClassPublication {
		t.Fatalf("entries = %#v, want the publication listed once the repair is refused", built.Entries)
	}
}

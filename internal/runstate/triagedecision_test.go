package runstate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The second stoppage the tests below decide about, where one is not enough.
const secondDecidedRunID = "run-8888777766665555444433332222aaaa"

// The whole point of the record: a spend and the decision that authorized it are
// one write, so an item's record can never say a budget went without saying what
// was decided, by whom, and about which stoppage.
func TestARerunSpendCarriesTheDecisionThatAuthorizedIt(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	decision := triageDecided(TriageDecisionRerun, decidedRunID)
	counters, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.311", decision, time.Now(), TriageCaps{
		ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2,
	})
	if err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	if counters.Reruns != 1 {
		t.Fatalf("reruns = %d, want the decision's spend", counters.Reruns)
	}
	// Read back from the record rather than from what was returned, because the
	// record is what a carry-out reads.
	reread, err := store.Counters("yoyodyne-ifd.311")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	recorded, found := reread.DecisionOf(decidedRunID)
	if !found {
		t.Fatalf("counters = %#v, want the decision standing about the stoppage it was made about", reread)
	}
	if recorded.Decision != TriageDecisionRerun || recorded.Reason != decision.Reason {
		t.Fatalf("decision = %#v, want the re-run and the reasoning it was recorded with", recorded)
	}
	if recorded.DecidedBy != decision.DecidedBy || recorded.Conversation != decision.Conversation || recorded.Turn != decision.Turn {
		t.Fatalf("decision = %#v, want the role and the turn it was recorded on", recorded)
	}
	if recorded.DecidedAt.IsZero() {
		t.Fatalf("decision = %#v, want the moment it was recorded", recorded)
	}
	// The citation is what an attribution built from this points at, so it has to
	// name all three.
	for _, want := range []string{decision.DecidedBy, decision.Conversation, "after turn 3"} {
		if !strings.Contains(recorded.Cite(), want) {
			t.Fatalf("citation %q is missing %q", recorded.Cite(), want)
		}
	}
}

// A decision names its own stoppage, so a decision about one run says nothing
// about another — which is the whole reason a spent counter cannot stand in for
// one. An item that stopped twice carries a decision about each.
func TestDecisionsStandPerStoppageAndTheLatestOneAboutARunSupersedes(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 8, RepairGrants: 2, Reruns: 2, MergeRearms: 2}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.311", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	second := triageDecided(TriageDecisionRerun, secondDecidedRunID)
	second.Reason = "the second stoppage was a backend death rather than anything about the change"
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.311", second, time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	counters, err := store.Counters("yoyodyne-ifd.311")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Decisions) != 2 {
		t.Fatalf("decisions = %#v, want one standing about each stoppage", counters.Decisions)
	}
	if decided, _ := counters.DecisionOf(secondDecidedRunID); decided.Reason != second.Reason {
		t.Fatalf("decision = %#v, want the one recorded about that stoppage", decided)
	}

	// The development manager came back to the first stoppage and decided to wait
	// instead. That is one decision about one run rather than two, so it replaces
	// what was standing rather than leaving a carry-out to choose.
	waited := triageDecided(TriageDecisionWait, decidedRunID)
	waited.Reason = "the forge still has the merge, so nothing is to be done about this one yet"
	if _, err := store.RecordDecision(context.Background(), "yoyodyne-ifd.311", waited, time.Now()); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	counters, err = store.Counters("yoyodyne-ifd.311")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Decisions) != 2 {
		t.Fatalf("decisions = %#v, want the later decision in place of the earlier one", counters.Decisions)
	}
	standing, _ := counters.DecisionOf(decidedRunID)
	if standing.Decision != TriageDecisionWait {
		t.Fatalf("decision = %#v, want the decision that holds now", standing)
	}
	// Superseding a decision does not give a budget back: what was spent was
	// spent, and the counters say so.
	if counters.Reruns != 2 {
		t.Fatalf("reruns = %d, want both spends still recorded", counters.Reruns)
	}
}

// A decision that buys another attempt is recorded by the operation that spends
// its budget, and by nothing else. Recording one on its own would be a decision
// the guards never saw.
func TestADecisionThatSpendsIsRefusedByTheOperationThatRecordsNoSpend(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	for _, decision := range []string{TriageDecisionRepair, TriageDecisionRerun, TriageDecisionRearm} {
		if _, err := store.RecordDecision(context.Background(), "yoyodyne-ifd.311", triageDecided(decision, decidedRunID), time.Now()); err == nil {
			t.Fatalf("RecordDecision() recorded a %q, which spends a budget it never asked for", decision)
		}
	}
	counters, err := store.Counters("yoyodyne-ifd.311")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Decisions) != 0 || counters.Passes() != 0 {
		t.Fatalf("counters = %#v, want nothing recorded and nothing spent", counters)
	}
}

// The word and the budget have to agree. An operation that took whatever
// decision it was handed would let a wait pay for a re-run, which is the
// attribution the record exists to make impossible.
func TestASpendIsRefusedWhenTheDecisionIsNotTheOneItSpends(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.311", triageDecided(TriageDecisionWait, decidedRunID), time.Now(), caps); err == nil {
		t.Fatal("RecordRerun() spent a re-run on a decision to wait")
	}
	if _, err := store.GrantRepair(context.Background(), "yoyodyne-ifd.311", triageDecided(TriageDecisionRerun, decidedRunID), 2, time.Now(), caps); err == nil {
		t.Fatal("GrantRepair() granted a repair on a decision to re-run")
	}
	counters, err := store.Counters("yoyodyne-ifd.311")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.Passes() != 0 || len(counters.Decisions) != 0 {
		t.Fatalf("counters = %#v, want nothing spent and nothing recorded", counters)
	}
}

// Every field of a decision is required, because a decision nobody is named for
// and one that names no stoppage are exactly the prose this replaces. A spend
// asked for with one is refused before anything is written.
func TestAnIncompleteDecisionSpendsNothing(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}
	for name, spoil := range map[string]func(*TriageDecision){
		"no reasoning":    func(d *TriageDecision) { d.Reason = "" },
		"nobody deciding": func(d *TriageDecision) { d.DecidedBy = "" },
		"no conversation": func(d *TriageDecision) { d.Conversation = "" },
		"no stoppage":     func(d *TriageDecision) { d.RunID = "" },
		"not a run":       func(d *TriageDecision) { d.RunID = "the last one" },
	} {
		decision := triageDecided(TriageDecisionRerun, decidedRunID)
		spoil(&decision)
		if _, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.311", decision, time.Now(), caps); err == nil {
			t.Fatalf("RecordRerun() with %s recorded a decision anyway", name)
		}
	}
	counters, err := store.Counters("yoyodyne-ifd.311")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.Reruns != 0 {
		t.Fatalf("reruns = %d, want nothing spent on a decision the record would not hold", counters.Reruns)
	}
}

// A cap refuses the decision and the spend together, because they are one write.
// A record left carrying a decision the guard refused would be a decision the
// carry-out then acted on.
func TestADecisionRefusedByACapIsNotRecorded(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	caps := TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 0, MergeRearms: 2}
	_, err := store.RecordRerun(context.Background(), "yoyodyne-ifd.311", triageDecided(TriageDecisionRerun, decidedRunID), time.Now(), caps)
	if !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("RecordRerun() error = %v, want the cap refusing", err)
	}
	counters, err := store.Counters("yoyodyne-ifd.311")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Decisions) != 0 {
		t.Fatalf("decisions = %#v, want none recorded past a cap", counters.Decisions)
	}
}

// The record is read back by another process, so it has to survive the file it
// is written to.
func TestARecordedDecisionSurvivesBeingReadBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewTriageStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	if _, err := store.RecordDecision(context.Background(), "yoyodyne-ifd.311",
		triageDecided(TriageDecisionEscalate, decidedRunID), time.Now()); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	reopened, err := NewTriageStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	counters, err := reopened.Counters("yoyodyne-ifd.311")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	decided, found := counters.DecisionOf(decidedRunID)
	if !found || decided.Decision != TriageDecisionEscalate {
		t.Fatalf("counters = %#v, want the escalation another process wrote", counters)
	}
}

// Two decisions standing about one stoppage describe a record the only thing
// that writes them could not have written, and a carry-out reading it would have
// to choose between them.
func TestARecordWithTwoDecisionsAboutOneStoppageIsRefused(t *testing.T) {
	t.Parallel()

	decision := triageDecided(TriageDecisionRerun, decidedRunID)
	decision.DecidedAt = time.Now().UTC()
	counters := TriageCounters{
		SchemaVersion: TriageCountersSchemaVersion,
		ProductID:     "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.311",
		Reruns:        2,
		Decisions:     []TriageDecision{decision, decision},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := counters.Validate(); err == nil {
		t.Fatal("Validate() accepted two decisions about one stoppage")
	}
}

// A stop names the item that supersedes the run it stops, and it is the only
// decision that may: what supersedes a run is an answer to why the run should not
// go on, which no decision about a stoppage is asking. It spends nothing and is
// recorded by the operation that records no spend.
func TestOnlyAStopNamesTheItemThatSupersedesItsRun(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	stop := triageDecided(TriageDecisionStop, decidedRunID)
	stop.SupersededBy = "yoyodyne-ifd.398"
	if stop.Spends() {
		t.Fatal("a stop spends a budget, and it buys no attempt at anything")
	}
	counters, err := store.RecordDecision(context.Background(), "yoyodyne-ifd.428.34", stop, time.Now())
	if err != nil {
		t.Fatalf("RecordDecision(stop) error = %v", err)
	}
	recorded, found := counters.DecisionOf(decidedRunID)
	if !found || recorded.SupersededBy != "yoyodyne-ifd.398" {
		t.Fatalf("decision = %#v, want the stop and what supersedes it", recorded)
	}
	if !strings.Contains(recorded.Describe(), "in flight") || !strings.Contains(recorded.Describe(), "superseded by yoyodyne-ifd.398") {
		t.Fatalf("Describe() = %q", recorded.Describe())
	}
	if counters.AwaitingCarryOut(decidedRunID) {
		t.Fatal("a stop reads as a decision the harness has still to carry out")
	}

	wait := triageDecided(TriageDecisionWait, secondDecidedRunID)
	wait.SupersededBy = "yoyodyne-ifd.398"
	if _, err := store.RecordDecision(context.Background(), "yoyodyne-ifd.428.34", wait, time.Now()); err == nil ||
		!strings.Contains(err.Error(), `only a "stop" decision names the item that supersedes a run`) {
		t.Fatalf("RecordDecision(wait naming a superseding item) error = %v", err)
	}
}

// A repair handed back to its run is carried out, however few of the rounds it
// granted were judged. The supervisor's periodic pass (yoyodyne-ifd.413) was
// granted two rounds on 2026-09-30, continued at once, approved in one, and
// stopped at its promotion — and with two rounds committed and one judged, every
// reading said the harness had still to carry the repair out (yoyodyne-8ff).
func TestARepairContinuedSinceItsDecisionIsCarriedOut(t *testing.T) {
	t.Parallel()

	decided := time.Date(2026, 9, 30, 20, 32, 55, 0, time.UTC)
	repair := triageDecided(TriageDecisionRepair, decidedRunID)
	repair.DecidedAt = decided
	counters := TriageCounters{RepairGrants: 1, GrantedRounds: 2, CommittedRounds: 2, ReviewRounds: 1, Decisions: []TriageDecision{repair}}
	if !counters.AwaitingCarryOut(decidedRunID) {
		t.Fatal("the ledger alone stopped reading an unspent grant as outstanding")
	}
	run := State{RunID: decidedRunID}
	if !counters.AwaitingCarryOutOf(run) {
		t.Fatal("a repair no continuation carries reads as carried out")
	}
	for name, continuation := range map[string]RepairContinuation{
		"before the decision":     {GrantedAttempts: 2, ContinuedAt: decided.Add(-time.Hour)},
		"the harness's own stall": {ContinuedAt: decided.Add(2 * time.Minute), Stall: true, ByHarness: true},
	} {
		run.RepairContinuations = []RepairContinuation{continuation}
		if !counters.AwaitingCarryOutOf(run) {
			t.Fatalf("a continuation %s carried the repair out", name)
		}
	}
	for _, returned := range []bool{false, true} {
		run.RepairContinuations = []RepairContinuation{{GrantedAttempts: 2, ContinuedAt: decided.Add(2 * time.Minute), Returned: returned}}
		if counters.AwaitingCarryOutOf(run) || !counters.StandingOf(run).CarriedOut || counters.StandingOf(run).Decided {
			t.Fatalf("a carried-out repair still decides a later stop (returned %t): %#v", returned, counters.StandingOf(run))
		}
	}
}

// A decision the harness tried and was refused is not one it still has to carry
// out: the refusal is the development manager's to answer. A finding about an
// earlier decision, one waiting on a switch the operator holds, and one no pass
// reached leave the decision the harness's (yoyodyne-8ff).
func TestARefusedCarryOutIsNotAwaitingTheHarness(t *testing.T) {
	t.Parallel()

	decided := time.Date(2026, 9, 28, 15, 43, 9, 0, time.UTC)
	rearm := triageDecided(TriageDecisionRearm, decidedRunID)
	rearm.DecidedAt = decided
	refusal := TriageCarryOut{RunID: decidedRunID, Decision: TriageDecisionRearm, Gate: TriageGateHarness,
		Refusal: "recorded no promotion", Attempts: 1, RefusedAt: decided.Add(18 * time.Hour)}
	counters := TriageCounters{MergeRearms: 1, Decisions: []TriageDecision{rearm}}
	if !counters.AwaitingCarryOut(decidedRunID) {
		t.Fatal("a re-arm nothing has attempted reads as not the harness's")
	}
	for name, finding := range map[string]func(TriageCarryOut) TriageCarryOut{
		"older than the decision": func(f TriageCarryOut) TriageCarryOut { f.RefusedAt = decided.Add(-time.Hour); return f },
		"waiting on a switch":     func(f TriageCarryOut) TriageCarryOut { f.Waiting = true; return f },
		"unattempted":             func(f TriageCarryOut) TriageCarryOut { f.Unattempted = true; return f },
		"about another decision":  func(f TriageCarryOut) TriageCarryOut { f.Decision = TriageDecisionRerun; return f },
	} {
		counters.CarryOuts = []TriageCarryOut{finding(refusal)}
		if _, refused := counters.RefusedCarryOut(decidedRunID); refused || !counters.AwaitingCarryOut(decidedRunID) {
			t.Fatalf("a finding %s took the decision off the harness", name)
		}
	}
	counters.CarryOuts = []TriageCarryOut{refusal}
	if _, refused := counters.RefusedCarryOut(decidedRunID); !refused || counters.AwaitingCarryOut(decidedRunID) || !counters.Standing(decidedRunID).Refused {
		t.Fatal("a refused re-arm still reads as the harness's to carry out")
	}
}

package runstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func TestAPendingCarryOutNoteSurvivesAFailedDeliveryAndAClearedFinding(t *testing.T) {
	t.Parallel()
	store := newTriageStore(t)
	ctx := context.Background()
	refusal := carryOutRefused(decidedRunID, TriageGatePreservedWork)
	refusal.Cause = triage.CarryOutWorktreeGone
	counters, err := store.RecordCarryOutRefusal(ctx, "yoyodyne-ifd.346", refusal, time.Now())
	if err != nil || len(counters.PendingCarryOutNotes) != 1 {
		t.Fatalf("refusal = %+v, %v; want an atomically queued note", counters, err)
	}
	note := counters.PendingCarryOutNotes[0]
	trackerErr := errors.New("the tracker is temporarily unavailable")
	if err := store.DeliverCarryOutNotes(ctx, counters.WorkItemID, time.Now(), func(context.Context, string) error {
		return trackerErr
	}); !errors.Is(err, trackerErr) {
		t.Fatalf("failed delivery = %v, want %v", err, trackerErr)
	}
	if _, err := store.ClearCarryOut(ctx, counters.WorkItemID, decidedRunID, time.Now()); err != nil {
		t.Fatal(err)
	}
	read, err := store.Counters(counters.WorkItemID)
	if err != nil || len(read.CarryOuts) != 0 || len(read.PendingCarryOutNotes) != 1 || read.PendingCarryOutNotes[0] != note {
		t.Fatalf("cleared finding = %+v, %v; want the exact pending note preserved", read, err)
	}
	deliveries := 0
	for pull := 0; pull < 2; pull++ {
		if err := store.DeliverCarryOutNotes(ctx, counters.WorkItemID, time.Now(), func(_ context.Context, delivered string) error {
			deliveries++
			if delivered != note {
				t.Fatalf("delivered note = %q, want %q", delivered, note)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if deliveries != 1 {
		t.Fatalf("note delivered %d times, want once", deliveries)
	}
}

// carryOutRefused is one finding as the record requires it: the stoppage, what
// was being carried out, which gate stopped it, and what would clear it.
func carryOutRefused(runID, gate string) TriageCarryOut {
	return TriageCarryOut{
		RunID:    runID,
		Decision: TriageDecisionRerun,
		Gate:     gate,
		Refusal:  "the gate's own account of why it refused this carry-out",
		Clears:   "what somebody would have to do about it",
	}
}

// The whole point of the record: a decision the harness tried to fire and a gate
// stopped has to be readable afterwards, because a decision that quietly fails to
// fire is indistinguishable from one nothing has reached yet — which is what
// thirty-three items looked like for days.
func TestARefusedCarryOutIsReadableAgainstTheStoppageItWasAbout(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	refusal := carryOutRefused(decidedRunID, TriageGateIntakeHold)
	refusal.Waiting = true
	if _, err := store.RecordCarryOutRefusal(context.Background(), "yoyodyne-ifd.346", refusal, time.Now()); err != nil {
		t.Fatalf("RecordCarryOutRefusal() error = %v", err)
	}
	// Read back from the record rather than from what was returned, because the
	// record is what the docket joins.
	counters, err := store.Counters("yoyodyne-ifd.346")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	recorded, found := counters.CarryOutOf(decidedRunID)
	if !found {
		t.Fatalf("counters = %#v, want the finding standing about the stoppage it was recorded against", counters)
	}
	if recorded.Gate != TriageGateIntakeHold || !recorded.Waiting {
		t.Fatalf("finding = %#v, want the gate that stopped it and that it clears on its own", recorded)
	}
	if recorded.Attempts != 1 || recorded.RefusedAt.IsZero() {
		t.Fatalf("finding = %#v, want the first attempt and the moment it was refused", recorded)
	}
	// A stoppage nobody attempted has none, which is what makes the presence of one
	// mean something.
	if _, found := counters.CarryOutOf(secondDecidedRunID); found {
		t.Fatalf("counters = %#v, want no finding about a stoppage nothing attempted", counters)
	}
}

// A gate that has been shut for a week and one shut for a minute are different
// facts about the same decision, so the attempts accumulate rather than the
// record reading as a first try on every pass.
func TestRepeatedRefusalsSupersedeAndCountTheAttempts(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	if _, err := store.RecordCarryOutRefusal(ctx, "yoyodyne-ifd.346", carryOutRefused(decidedRunID, TriageGateCapacity), time.Now()); err != nil {
		t.Fatalf("RecordCarryOutRefusal() error = %v", err)
	}
	second := carryOutRefused(decidedRunID, TriageGateWorkItem)
	counters, err := store.RecordCarryOutRefusal(ctx, "yoyodyne-ifd.346", second, time.Now())
	if err != nil {
		t.Fatalf("RecordCarryOutRefusal() error = %v", err)
	}
	if len(counters.CarryOuts) != 1 {
		t.Fatalf("carry-outs = %#v, want one standing per stoppage rather than one per attempt", counters.CarryOuts)
	}
	recorded, _ := counters.CarryOutOf(decidedRunID)
	if recorded.Gate != TriageGateWorkItem {
		t.Fatalf("finding = %#v, want the gate that stopped the latest attempt", recorded)
	}
	if recorded.Attempts != 2 {
		t.Fatalf("attempts = %d, want the count to carry forward across supersession", recorded.Attempts)
	}
}

// A finding standing over a decision that has since been carried out is the worst
// kind: it reads exactly like the condition the record exists to report.
func TestACarriedOutDecisionClearsTheFindingAndLeavesTheOthers(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	if _, err := store.RecordCarryOutRefusal(ctx, "yoyodyne-ifd.346", carryOutRefused(decidedRunID, TriageGateCapacity), time.Now()); err != nil {
		t.Fatalf("RecordCarryOutRefusal() error = %v", err)
	}
	if _, err := store.RecordCarryOutRefusal(ctx, "yoyodyne-ifd.346", carryOutRefused(secondDecidedRunID, TriageGateBudget), time.Now()); err != nil {
		t.Fatalf("RecordCarryOutRefusal() error = %v", err)
	}
	counters, err := store.ClearCarryOut(ctx, "yoyodyne-ifd.346", decidedRunID, time.Now())
	if err != nil {
		t.Fatalf("ClearCarryOut() error = %v", err)
	}
	if _, found := counters.CarryOutOf(decidedRunID); found {
		t.Fatalf("counters = %#v, want the finding about the carried-out stoppage gone", counters)
	}
	// The other stoppage's finding is about a different decision and a different
	// gate, and this one firing says nothing about it.
	if _, found := counters.CarryOutOf(secondDecidedRunID); !found {
		t.Fatalf("counters = %#v, want the other stoppage's finding left where it is", counters)
	}
	// A stoppage with nothing recorded is the ordinary case — the first carry-out
	// of an item usually fires — so clearing it is not an error.
	if _, err := store.ClearCarryOut(ctx, "yoyodyne-ifd.346", decidedRunID, time.Now()); err != nil {
		t.Fatalf("ClearCarryOut() on a stoppage with no finding error = %v", err)
	}
}

// The pacing is what keeps one refused decision from spending every pass's single
// carry-out, and the exception to it is what keeps a decision from waiting a
// quarter of an hour after the switch it was waiting on was already opened.
func TestOnlyAGateThatNeedsSomethingChangedIsPaced(t *testing.T) {
	t.Parallel()

	refusedAt := time.Now().UTC()
	needsAPerson := TriageCarryOut{RefusedAt: refusedAt}
	if !needsAPerson.Cooling(refusedAt.Add(TriageCarryOutRetryDelay - time.Minute)) {
		t.Fatalf("a refusal inside the retry delay is not cooling, so the pass would spend its one carry-out on it every poll")
	}
	if needsAPerson.Cooling(refusedAt.Add(TriageCarryOutRetryDelay + time.Minute)) {
		t.Fatalf("a refusal past the retry delay is still cooling, so the decision would never be carried out")
	}
	clearsItself := TriageCarryOut{RefusedAt: refusedAt, Waiting: true}
	if clearsItself.Cooling(refusedAt.Add(time.Second)) {
		t.Fatalf("a gate that clears on its own is paced, so a lifted hold would be noticed %s late", TriageCarryOutRetryDelay)
	}
}

// A finding nobody can act on is the silence this record exists to end, worded
// rather than absent. Each half of that is a contract violation.
func TestAFindingWithoutAGateOrARemedyIsRefused(t *testing.T) {
	t.Parallel()

	for name, refusal := range map[string]TriageCarryOut{
		"no gate":         {RunID: decidedRunID, Decision: TriageDecisionRerun, Refusal: "why", Clears: "how", Attempts: 1, RefusedAt: time.Now()},
		"unknown gate":    {RunID: decidedRunID, Decision: TriageDecisionRerun, Gate: "something else", Refusal: "why", Clears: "how", Attempts: 1, RefusedAt: time.Now()},
		"no refusal":      {RunID: decidedRunID, Decision: TriageDecisionRerun, Gate: TriageGateBudget, Clears: "how", Attempts: 1, RefusedAt: time.Now()},
		"no remedy":       {RunID: decidedRunID, Decision: TriageDecisionRerun, Gate: TriageGateBudget, Refusal: "why", Attempts: 1, RefusedAt: time.Now()},
		"no stoppage":     {Decision: TriageDecisionRerun, Gate: TriageGateBudget, Refusal: "why", Clears: "how", Attempts: 1, RefusedAt: time.Now()},
		"no decision":     {RunID: decidedRunID, Gate: TriageGateBudget, Refusal: "why", Clears: "how", Attempts: 1, RefusedAt: time.Now()},
		"no attempt":      {RunID: decidedRunID, Decision: TriageDecisionRerun, Gate: TriageGateBudget, Refusal: "why", Clears: "how", RefusedAt: time.Now()},
		"unrecorded when": {RunID: decidedRunID, Decision: TriageDecisionRerun, Gate: TriageGateBudget, Refusal: "why", Clears: "how", Attempts: 1},
	} {
		if err := refusal.Validate(); err == nil {
			t.Fatalf("%s: Validate() = nil, want a refusal", name)
		}
	}
}

// The finding is written where a reader acts on it, so what it says has to name
// the gate and the remedy rather than only that something went wrong.
func TestAFindingDescribesTheGateAndTheRemedy(t *testing.T) {
	t.Parallel()

	refusal := carryOutRefused(decidedRunID, TriageGatePreservedWork)
	refusal.Attempts = 3
	refusal.RefusedAt = time.Now().UTC()
	described := refusal.Describe()
	for _, want := range []string{TriageGatePreservedWork, decidedRunID, refusal.Refusal, refusal.Clears, "3 attempt"} {
		if !strings.Contains(described, want) {
			t.Fatalf("described %q is missing %q", described, want)
		}
	}
}

func TestAPermanentRefusalBlocksOnlyTheDecisionThatWasAttempted(t *testing.T) {
	t.Parallel()
	decided := time.Now()
	refusal := TriageCarryOut{Decision: TriageDecisionRepair, DecidedAt: decided,
		Cause: triage.CarryOutWorktreeGone, RefusedAt: decided.Add(time.Minute)}
	later := decided.Add(7 * 24 * time.Hour)
	if !refusal.BlocksDecision(TriageDecisionRepair, decided, later) {
		t.Fatal("a permanent refusal cooled into another attempt")
	}
	if refusal.BlocksDecision(TriageDecisionRerun, decided, later) ||
		refusal.BlocksDecision(TriageDecisionRepair, decided.Add(time.Second), later) {
		t.Fatal("a refusal about an earlier decision blocked a new decision")
	}
	refusal.Cause = ""
	if !refusal.BlocksDecision(TriageDecisionRepair, decided, decided.Add(2*time.Minute)) ||
		refusal.BlocksDecision(TriageDecisionRepair, decided, later) {
		t.Fatal("a temporary gate lost its paced retry")
	}
}

// A carry-out record written before the intake hold's gate was reworded still
// loads, and reads under the wording the vocabulary has now, so the docket
// never prints the earlier one.
func TestACarryOutRecordedUnderTheEarlierIntakeHoldWordingStillReads(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	refusal := carryOutRefused(decidedRunID, TriageGateIntakeHold)
	refusal.Waiting = true
	if _, err := store.RecordCarryOutRefusal(context.Background(), "yoyodyne-ifd.346", refusal, time.Now()); err != nil {
		t.Fatalf("RecordCarryOutRefusal() error = %v", err)
	}
	path := store.path("yoyodyne-ifd.346")
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	current, _ := json.Marshal(TriageGateIntakeHold)
	earlier, _ := json.Marshal(triageGateIntakeHoldAsRecorded)
	if !bytes.Contains(written, current) {
		t.Fatalf("record %s does not carry the gate %s", written, current)
	}
	if err := os.WriteFile(path, bytes.Replace(written, current, earlier, 1), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	counters, err := store.Counters("yoyodyne-ifd.346")
	if err != nil {
		t.Fatalf("Counters() error = %v, want the earlier wording read rather than refused", err)
	}
	recorded, found := counters.CarryOutOf(decidedRunID)
	if !found || recorded.Gate != TriageGateIntakeHold {
		t.Fatalf("finding = %#v, want the gate read as %q", recorded, TriageGateIntakeHold)
	}
}

package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// spentOnRunID is the fresh run a re-run claim was spent on in these tests.
const spentOnRunID = "run-fedcba9876543210fedcba9876543210"

// freshRunThatDiedPreparing is a re-run's fresh run that was reserved and then
// died while its worktree was being prepared, with nothing classifying why: the
// shape that spends a claim the harness cannot prove bought nothing.
func freshRunThatDiedPreparing() runstate.State {
	started := docketedNow.Add(-10 * time.Minute)
	completed := started.Add(time.Minute)
	return runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         spentOnRunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    docketedItem,
		WorkItemTitle: docketedTitle,
		Backend:       "claude-code",
		Status:        runstate.StatusFailed,
		StartedAt:     started,
		UpdatedAt:     completed,
		CompletedAt:   &completed,
		Failure:       "prepare the worktree: bd export timed out",
	}
}

// The case this exists for, end to end: a re-run claim spent on a fresh run
// that died preparing its worktree is not given back by the harness, the fresh
// run's docket entry names the claim and what spending it cost, the development
// manager decides to give it back, and the harness carries that out — so the
// claim's record says who gave it back and why, and her re-run decision is
// carried out again. Nothing under the state root is edited by hand.
func TestASpentClaimIsDocketedAndGivenBackOnTheDevelopmentManagersDecision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	harness := newRerunHarness(t, stoppedState())
	fresh := freshRunThatDiedPreparing()
	harness.outcome = Outcome{RunID: fresh.RunID, WorkItemID: docketedItem, Status: runstate.StatusFailed}
	harness.failure = errors.New(fresh.Failure)
	if _, err := harness.rerunner().Rerun(ctx, rerunRequest()); err == nil {
		t.Fatal("Rerun() reported no failure for a fresh run that died")
	}
	if err := harness.runs.Create(fresh); err != nil {
		t.Fatalf("Create() of the fresh run error = %v", err)
	}
	key := triage.Key(triage.ClassStoppedRun, docketedRunID)
	spent, found, err := harness.reruns.Find(key)
	if err != nil || !found || spent.RunID != fresh.RunID || !spent.Spent() {
		t.Fatalf("claim = %#v, %v, %v, want it spent on the fresh run", spent, found, err)
	}
	if err := harness.reruns.Withdraw(ctx, key); err == nil {
		t.Fatal("the harness gave back by itself a claim whose fresh run it cannot prove bought nothing")
	}

	// Docketed: the fresh run's entry carries the claim, the item, and what
	// spending it cost, and says what she can decide.
	docketer := docketerOverStore(harness.docket, harness.runs, config.Config{Triage: docketedTriage})
	docketer.Clock = docketClock{}
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var entry *triage.Entry
	for index := range built.Entries {
		if built.Entries[index].RunID == fresh.RunID {
			entry = &built.Entries[index]
		}
	}
	if entry == nil || entry.SpentClaim == nil || entry.SpentClaim.PriorRunID != docketedRunID || entry.WorkItemID != docketedItem {
		t.Fatalf("docket = %#v, want the fresh run's entry naming the claim it spent", built.Entries)
	}
	rendered := entry.Render()
	for _, want := range []string{docketedRunID, "What it cost", "1 of 1 re-run(s)", "give-back-claim", "leave-claim-spent"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("entry does not say %q:\n%s", want, rendered)
		}
	}

	// Her decision, recorded on the claim as her conversation records it.
	if _, err := harness.reruns.DecideClaim(ctx, entry.SpentClaim.DocketKey, runstate.RerunClaimDecision{
		Decision:     runstate.RerunClaimGiveBack,
		Reason:       "the fresh run died preparing its worktree and bought nothing",
		DecidedBy:    "development manager",
		Conversation: decidedIn,
		Turn:         decidedOnTurn,
	}); err != nil {
		t.Fatalf("DecideClaim() error = %v", err)
	}

	// Carried out by the harness on its pull.
	notes := &recordedNotes{}
	carrying := harness.carryOut()
	carrying.ClaimGiver = &ClaimGiver{Claims: harness.reruns, Runs: harness.runs, Notes: notes, Clock: docketClock{}}
	carried, err := carrying.CarryClaimGiveBacks(ctx)
	if err != nil || len(carried) != 1 || !carried[0].Carried || carried[0].Decision != DecisionGiveBackClaim {
		t.Fatalf("CarryClaimGiveBacks() = %#v, %v, want the one give-back carried out", carried, err)
	}
	given, _, err := harness.reruns.Find(key)
	if err != nil || given.GivenBack == nil || given.GivenBack.By != "the harness's watching session" ||
		!strings.Contains(given.GivenBack.Decision.Reason, "bought nothing") || given.GivenBack.RunID != fresh.RunID {
		t.Fatalf("claim = %#v, %v, want who gave it back, on whose decision, and why", given.GivenBack, err)
	}
	if len(notes.notes) != 1 || !strings.Contains(notes.notes[0], "Re-run claim given back") {
		t.Fatalf("item notes = %#v, want the give-back said on the item", notes.notes)
	}
	if again, err := carrying.CarryClaimGiveBacks(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second CarryClaimGiveBacks() = %#v, %v, want nothing left to carry out", again, err)
	}

	// The re-run she decided before is outstanding again, and carried out.
	task := theOneOutstanding(t, carrying)
	if task.Decision != runstate.TriageDecisionRerun || task.RunID != docketedRunID {
		t.Fatalf("outstanding = %#v, want the re-run of the stopped run offered again", task)
	}
	harness.failure = nil
	harness.outcome = Outcome{RunID: "run-abababababababababababababababab", WorkItemID: docketedItem, Status: runstate.StatusSucceeded}
	if _, err := harness.rerunner().Rerun(ctx, rerunRequest()); err != nil {
		t.Fatalf("Rerun() after the give-back error = %v, want the same decision carried out", err)
	}
	if len(harness.started) != 2 {
		t.Fatalf("starts = %d, want the run the claim was spent on and the one after the give-back", len(harness.started))
	}
	reclaimed, _, err := harness.reruns.Find(key)
	if err != nil || !reclaimed.Spent() || len(reclaimed.EarlierGivenBack) != 1 {
		t.Fatalf("claim = %#v, %v, want it taken again with the give-back kept", reclaimed, err)
	}
}

// Leaving the claim spent is carried out as nothing given back, and the claim
// stays spent.
func TestAClaimLeftSpentIsNotGivenBack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	harness := newRerunHarness(t, stoppedState())
	fresh := freshRunThatDiedPreparing()
	harness.outcome = Outcome{RunID: fresh.RunID, WorkItemID: docketedItem, Status: runstate.StatusFailed}
	harness.failure = errors.New(fresh.Failure)
	_, _ = harness.rerunner().Rerun(ctx, rerunRequest())
	if err := harness.runs.Create(fresh); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	key := triage.Key(triage.ClassStoppedRun, docketedRunID)
	if _, err := harness.reruns.DecideClaim(ctx, key, runstate.RerunClaimDecision{
		Decision: runstate.RerunClaimLeaveSpent, Reason: "the fresh run reached the item; it was spent on something",
		DecidedBy: "development manager", Conversation: decidedIn, Turn: decidedOnTurn,
	}); err != nil {
		t.Fatalf("DecideClaim() error = %v", err)
	}
	giver := ClaimGiver{Claims: harness.reruns, Runs: harness.runs}
	if results, err := giver.GiveBackDecided(ctx, "the harness's watching session"); err != nil || len(results) != 0 {
		t.Fatalf("GiveBackDecided() = %#v, %v, want nothing given back", results, err)
	}
	if _, err := giver.GiveBack(ctx, fresh.RunID, "a person with yoyo triage give-back"); err == nil || !strings.Contains(err.Error(), runstate.RerunClaimLeaveSpent) {
		t.Fatalf("GiveBack() error = %v, want it refused naming the decision standing", err)
	}
	if claimed, _ := harness.reruns.Claimed(docketedItem); len(claimed) != 1 {
		t.Fatalf("claimed = %d, want the claim still spent", len(claimed))
	}
}

// A claim whose fresh run is still going is not given back, whatever was
// decided: the run may yet buy something.
func TestAClaimIsNotGivenBackWhileItsRunIsGoing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	harness := newRerunHarness(t, stoppedState())
	fresh := freshRunThatDiedPreparing()
	harness.outcome = Outcome{RunID: fresh.RunID, WorkItemID: docketedItem, Status: runstate.StatusFailed}
	harness.failure = errors.New(fresh.Failure)
	_, _ = harness.rerunner().Rerun(ctx, rerunRequest())
	fresh.Status = runstate.StatusRunning
	fresh.CompletedAt = nil
	fresh.Failure = ""
	if err := harness.runs.Create(fresh); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	key := triage.Key(triage.ClassStoppedRun, docketedRunID)
	if _, err := harness.reruns.DecideClaim(ctx, key, runstate.RerunClaimDecision{
		Decision: runstate.RerunClaimGiveBack, Reason: "it bought nothing",
		DecidedBy: "development manager", Conversation: decidedIn, Turn: decidedOnTurn,
	}); err != nil {
		t.Fatalf("DecideClaim() error = %v", err)
	}
	giver := ClaimGiver{Claims: harness.reruns, Runs: harness.runs}
	if _, err := giver.GiveBack(ctx, fresh.RunID, "someone"); err == nil || !strings.Contains(err.Error(), "rather than ended") {
		t.Fatalf("GiveBack() error = %v, want it refused while the run is going", err)
	}
	if claimed, _ := harness.reruns.Claimed(docketedItem); len(claimed) != 1 {
		t.Fatalf("claimed = %d, want the claim still spent", len(claimed))
	}
}

// recordedNotes is a tracker that keeps every note written onto an item.
type recordedNotes struct{ notes []string }

func (n *recordedNotes) RecordOutcome(_ context.Context, id, note string) (beads.WorkItem, error) {
	n.notes = append(n.notes, note)
	return beads.WorkItem{ID: id}, nil
}

// A claim the harness took for a run that never started, and then could not save
// giving back, names no fresh run. It is put in front of the development manager
// on the entry of the stopped run it was taken for, which her re-run decision had
// closed, and a give-back she decides by that run is carried out.
func TestAClaimSpentOnARunThatNeverStartedIsDocketedOnTheStoppedRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	harness := newRerunHarness(t, stoppedState())
	key := triage.Key(triage.ClassStoppedRun, docketedRunID)
	harness.docket.close(key, runstate.TriageDecisionRerun, docketedNow)
	harness.failure = errors.New("reserve developer run: the checkout has uncommitted changes")
	harness.outcome = Outcome{}
	rerunner := harness.rerunner()
	rerunner.Reruns = unwithdrawableReruns{RerunRecords: harness.reruns}
	if _, err := rerunner.Rerun(ctx, rerunRequest()); err == nil || !strings.Contains(err.Error(), "could not be given back") {
		t.Fatalf("Rerun() error = %v, want the refusal saying the claim could not be given back", err)
	}
	claim, found, err := harness.reruns.Find(key)
	if err != nil || !found || !claim.Spent() || claim.RunID != "" {
		t.Fatalf("claim = %#v, %v, %v, want it spent and naming no fresh run", claim, found, err)
	}

	docketer := docketerOverStore(harness.docket, harness.runs, config.Config{Triage: docketedTriage})
	docketer.Clock = docketClock{}
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var entry *triage.Entry
	for index := range built.Entries {
		if built.Entries[index].RunID == docketedRunID {
			entry = &built.Entries[index]
		}
	}
	if entry == nil || entry.SpentClaim == nil || !entry.SpentClaim.NoFreshRun {
		t.Fatalf("docket = %#v, want the stopped run's entry back on the list naming the claim", built.Entries)
	}
	rendered := entry.Render()
	for _, want := range []string{"Re-run claim spent on no run", "1 of 1 re-run(s)", `"give-back-claim" on run ` + docketedRunID, "Next mover: you"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("entry does not say %q:\n%s", want, rendered)
		}
	}

	if _, err := harness.reruns.DecideClaim(ctx, key, runstate.RerunClaimDecision{
		Decision: runstate.RerunClaimGiveBack, Reason: "the run it was claimed for never started",
		DecidedBy: "development manager", Conversation: decidedIn, Turn: decidedOnTurn,
	}); err != nil {
		t.Fatalf("DecideClaim() error = %v", err)
	}
	giver := ClaimGiver{Claims: harness.reruns, Runs: harness.runs, Clock: docketClock{}}
	result, err := giver.GiveBack(ctx, docketedRunID, "a person with yoyo triage give-back")
	if err != nil || !result.GivenBack || result.Claim.GivenBack == nil || result.Claim.GivenBack.RunID != "" {
		t.Fatalf("GiveBack() = %#v, %v, want the claim named by its stopped run given back", result, err)
	}
	built, err = docketer.Build()
	if err != nil {
		t.Fatalf("Build() after the give-back error = %v", err)
	}
	for _, listed := range built.Entries {
		if listed.RunID == docketedRunID {
			t.Fatalf("entry still listed after the give-back:\n%s", listed.Render())
		}
	}
	harness.failure = nil
	harness.outcome = Outcome{RunID: "run-abababababababababababababababab", WorkItemID: docketedItem, Status: runstate.StatusSucceeded}
	if _, err := harness.rerunner().Rerun(ctx, rerunRequest()); err != nil {
		t.Fatalf("Rerun() after the give-back error = %v, want the same decision carried out", err)
	}
}

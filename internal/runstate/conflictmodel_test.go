package runstate

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func conflictTrialRun(id string, arm ConflictModelArm) State {
	return State{RunID: id, WorkItemID: "item-" + id, ConflictModel: &ConflictModel{Handback: "h-" + id, Arm: arm, Reason: "eligible"}}
}

func TestEligibleConflictAttemptsAlternateTheTwoModelsForTheTrialsAttempts(t *testing.T) {
	t.Parallel()

	const target = 4
	var runs []State
	want := []ConflictModelArm{ConflictModelCheaper, ConflictModelStronger, ConflictModelCheaper, ConflictModelStronger, ConflictModelSetting}
	for index, arm := range want {
		trial := ReadConflictModelTrial(runs, target)
		if trial.NextArm != arm || trial.Attempts != min(index, target) {
			t.Fatalf("before attempt %d the trial reads %+v, want next side %s", index+1, trial, arm)
		}
		runs = append(runs, conflictTrialRun(string(rune('a'+index)), arm))
		// A run the setting did not reach takes no place in the trial.
		runs = append(runs, State{RunID: "unrelated"})
	}
	if trial := ReadConflictModelTrial(runs, target); trial.NextArm != ConflictModelSetting || trial.Attempts != target {
		t.Fatalf("after the trial's attempts it reads %+v, want every attempt on the setting's kind", trial)
	}
}

func TestADefectAfterLandingOnACheaperResolutionStopsTheConflictTrialAtOnce(t *testing.T) {
	t.Parallel()

	stronger := conflictTrialRun("b", ConflictModelStronger)
	stronger.ConflictModel.DefectAfterLanding = &LandedDefect{Note: "on the stronger side"}
	runs := []State{conflictTrialRun("a", ConflictModelCheaper), stronger}
	if trial := ReadConflictModelTrial(runs, 20); trial.StoppedBy != "" || trial.NextArm != ConflictModelCheaper {
		t.Fatalf("a defect on the stronger side stopped the trial: %+v", trial)
	}
	cheaper := conflictTrialRun("c", ConflictModelCheaper)
	cheaper.ConflictModel.DefectAfterLanding = &LandedDefect{Note: "lost a branch of the merge"}
	runs = append(runs, cheaper)
	trial := ReadConflictModelTrial(runs, 20)
	if trial.NextArm != ConflictModelStopped || trial.StoppedBy != "c" || trial.StopNote != "lost a branch of the merge" {
		t.Fatalf("trial = %+v, want it stopped by run c", trial)
	}
}

func TestTheConflictTrialsComparisonCountsEachSide(t *testing.T) {
	t.Parallel()

	landed := func(state State) State { state.Integration = &Integration{}; return state }
	a := landed(conflictTrialRun("a", ConflictModelCheaper))
	a.ConflictModel.FirstReview = "approve"
	b := conflictTrialRun("b", ConflictModelCheaper)
	b.ConflictModel.ChecksFailed = true
	c := landed(conflictTrialRun("c", ConflictModelStronger))
	c.ConflictModel.FirstReview = "request_changes"
	d := landed(conflictTrialRun("d", ConflictModelCheaper))
	d.ConflictModel.FirstReview = "request_changes"
	d.ConflictModel.DefectAfterLanding = &LandedDefect{Note: "broken"}
	outside := landed(conflictTrialRun("e", ConflictModelSetting))
	figures := SummarizeConflictModelTrial([]State{a, b, c, d, outside}, map[string]float64{"a": 1, "b": 2, "c": 4, "e": 100}, 20)

	cheaper, stronger := figures.Cheaper, figures.Stronger
	if figures.Attempts != 4 || figures.StoppedBy != "d" {
		t.Fatalf("figures = %+v, want four attempts on the two sides and the stop by d", figures)
	}
	if cheaper.Attempts != 3 || cheaper.Landed != 2 || cheaper.CostUSD != 3 || cheaper.CostPerLandedUSD != 1.5 || cheaper.Unpriced != 1 ||
		cheaper.ChecksFailed != 1 || cheaper.Reviewed != 2 || cheaper.Refused != 1 || cheaper.DefectsAfterLanding != 1 {
		t.Fatalf("cheaper side = %+v", cheaper)
	}
	if stronger.Attempts != 1 || stronger.Landed != 1 || stronger.CostPerLandedUSD != 4 || stronger.Reviewed != 1 || stronger.Refused != 1 {
		t.Fatalf("stronger side = %+v", stronger)
	}
}

func TestRecordingAResolutionDefectIsRefusedOffTheTrialAndBeforeLanding(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	if _, _, err := ApplyUpdate(State{RunID: "x"}, RecordResolutionDefect{Note: "broken", At: at}); !errors.Is(err, ErrNoConflictModelAttempt) {
		t.Fatalf("a run the setting never reached: error = %v, want ErrNoConflictModelAttempt", err)
	}
	if _, _, err := ApplyUpdate(conflictTrialRun("a", ConflictModelCheaper), RecordResolutionDefect{Note: "broken", At: at}); err == nil || !strings.Contains(err.Error(), "records no landing") {
		t.Fatalf("a run that never landed: error = %v, want the missing landing named", err)
	}
	landed := conflictTrialRun("a", ConflictModelCheaper)
	landed.Integration = &Integration{}
	if _, _, err := ApplyUpdate(landed, RecordResolutionDefect{Note: "  ", At: at}); err == nil {
		t.Fatal("a note that says nothing was recorded")
	}
	recorded, changed, err := ApplyUpdate(landed, RecordResolutionDefect{Note: "lost a branch", At: at})
	if err != nil || !changed || recorded.ConflictModel.DefectAfterLanding == nil || recorded.ConflictModel.DefectAfterLanding.Note != "lost a branch" {
		t.Fatalf("ApplyUpdate() = %+v, %v, %v; want the defect recorded", recorded.ConflictModel, changed, err)
	}
	if landed.ConflictModel.DefectAfterLanding != nil {
		t.Fatal("recording the defect wrote through to the run it was given")
	}
	if _, changed, err := ApplyUpdate(recorded, RecordResolutionDefect{Note: "lost a branch", At: at}); err != nil || changed {
		t.Fatalf("recording the same defect again = %v, %v; want nothing changed", changed, err)
	}
}

func TestTheModelAChosenAttemptAsksForHoldsOnlyWhileThatAttemptIsOutstanding(t *testing.T) {
	t.Parallel()

	state := State{
		ReplayConflict: &ReplayConflict{TargetBranch: "main", Phase: PhaseIntegrating, Reconciliation: true, Handback: "h-1"},
		ConflictModel:  &ConflictModel{Handback: "h-1", Arm: ConflictModelCheaper, Model: "sonnet"},
	}
	if model, chosen := state.ConflictModelAsked(); !chosen || model != "sonnet" {
		t.Fatalf("ConflictModelAsked() = %q, %v; want sonnet for the outstanding attempt", model, chosen)
	}
	later := state
	later.ReplayConflict = &ReplayConflict{TargetBranch: "main", Phase: PhaseIntegrating, Reconciliation: true, Handback: "h-2"}
	if _, chosen := later.ConflictModelAsked(); chosen {
		t.Fatal("a later attempt at another conflict asked for the first attempt's model")
	}
	answered := state
	answered.ReplayConflict = nil
	if _, chosen := answered.ConflictModelAsked(); chosen {
		t.Fatal("the run asked for the attempt's model once the conflict was answered")
	}
	stopped := state
	stopped.ConflictModel = &ConflictModel{Handback: "h-1", Arm: ConflictModelStopped}
	if _, chosen := stopped.ConflictModelAsked(); chosen {
		t.Fatal("an attempt after the trial stopped asked for a model of its own")
	}
}

func TestTheRunIndexFindsTheRunsTheConflictModelReached(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	reached := testState(t, StatusSucceeded)
	reached.ConflictModel = &ConflictModel{Handback: "h-1", Arm: ConflictModelCheaper, Model: "sonnet", Reason: "eligible"}
	for _, state := range []State{reached, testState(t, StatusSucceeded)} {
		if err := store.Create(state); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := store.ConflictModelTrialRuns()
	if err != nil || len(runs) != 1 || runs[0].RunID != reached.RunID {
		t.Fatalf("ConflictModelTrialRuns() = %v, %v; want the one run the setting reached", runs, err)
	}
}

// A resolution that rejoined the merge queue with no review of its own has the
// queue's review of its candidate as its first review: an approval known from
// the landing, a refusal from the hand-back. Nothing overwrites a review the
// resolution already had, or one after failed checks or a later conflict.
func TestTheQueuesReviewOfTheCandidateIsTheFirstReviewOfAnUnreviewedResolution(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	resolving := func() State {
		state := conflictTrialRun("a", ConflictModelCheaper)
		state.Reconciliation = &Reconciliation{TargetBranch: "main", Handback: "h-a", Attempt: 1}
		return state
	}
	landed, changed, err := ApplyUpdate(resolving(), IntegrationRecorded{Integration: Integration{TargetBranch: "main"}, At: at})
	if err != nil || !changed || landed.ConflictModel.FirstReview != string(ReviewApprove) {
		t.Fatalf("landing = %+v, %v, %v; want the candidate's approval noted", landed.ConflictModel, changed, err)
	}

	refused := resolving()
	if !refused.NoteConflictModelQueueReview("repair") || refused.ConflictModel.FirstReview != "repair" {
		t.Fatalf("hand-back = %+v, want the candidate's refusal noted", refused.ConflictModel)
	}
	if refused.NoteConflictModelQueueReview(string(ReviewApprove)) || refused.ConflictModel.FirstReview != "repair" {
		t.Fatalf("a later review overwrote the first: %+v", refused.ConflictModel)
	}

	failed := resolving()
	failed.ConflictModel.ChecksFailed = true
	if failed.NoteConflictModelQueueReview(string(ReviewApprove)) {
		t.Fatal("a resolution that failed its checks had a later review counted as its first")
	}
	later := resolving()
	later.Reconciliation.Handback = "h-b"
	if later.NoteConflictModelQueueReview(string(ReviewApprove)) {
		t.Fatal("a review after a later attempt at a conflict was counted for the first")
	}
}

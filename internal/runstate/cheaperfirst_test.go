package runstate

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func trialRun(id string, arm CheaperFirstArm) State {
	return State{RunID: id, WorkItemID: "item-" + id, CheaperFirst: &CheaperFirst{Arm: arm, Reason: "eligible"}}
}

func TestTheTrialAlternatesStartingCheaperAndStartingStronger(t *testing.T) {
	t.Parallel()

	var runs []State
	want := []CheaperFirstArm{CheaperFirstCheaper, CheaperFirstStronger, CheaperFirstCheaper, CheaperFirstStronger}
	for index, arm := range want {
		trial := ReadCheaperFirstTrial(runs)
		if trial.NextArm != arm || trial.Runs != index {
			t.Fatalf("before run %d the trial reads %+v, want next arm %s after %d runs", index+1, trial, arm, index)
		}
		runs = append(runs, trialRun(string(rune('a'+index)), arm))
		// A run that was not eligible, or started after a stop, takes no place.
		runs = append(runs, State{RunID: "unrelated"})
	}
}

func TestADefectAfterLandingOnACheaperRunStopsTheTrialAtOnce(t *testing.T) {
	t.Parallel()

	stronger := trialRun("b", CheaperFirstStronger)
	stronger.CheaperFirst.DefectAfterLanding = &LandedDefect{Note: "a defect on the stronger arm"}
	runs := []State{trialRun("a", CheaperFirstCheaper), stronger}
	if trial := ReadCheaperFirstTrial(runs); trial.StoppedBy != "" {
		t.Fatalf("a defect on a run that started stronger stopped the trial: %+v", trial)
	}

	cheaper := trialRun("c", CheaperFirstCheaper)
	cheaper.CheaperFirst.DefectAfterLanding = &LandedDefect{Note: "broke the status line"}
	runs = append(runs, cheaper)
	trial := ReadCheaperFirstTrial(runs)
	if trial.NextArm != CheaperFirstStopped || trial.StoppedBy != "c" || trial.StopNote != "broke the status line" {
		t.Fatalf("trial = %+v, want it stopped by run c", trial)
	}
}

func TestRecordingADefectAfterLandingIsRefusedOutsideTheTrialAndBeforeLanding(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	landed := trialRun("a", CheaperFirstCheaper)
	landed.Integration = &Integration{}

	if _, _, err := ApplyUpdate(State{RunID: "x"}, RecordDefectAfterLanding{Note: "broken", At: at}); !errors.Is(err, ErrNotInCheaperFirstTrial) {
		t.Fatalf("a run outside the trial: error = %v, want ErrNotInCheaperFirstTrial", err)
	}
	if _, _, err := ApplyUpdate(trialRun("a", CheaperFirstCheaper), RecordDefectAfterLanding{Note: "broken", At: at}); err == nil || !strings.Contains(err.Error(), "records no landing") {
		t.Fatalf("a run that never landed: error = %v, want a refusal naming the missing landing", err)
	}
	if _, _, err := ApplyUpdate(landed, RecordDefectAfterLanding{Note: "  ", At: at}); err == nil {
		t.Fatal("a defect that says nothing was recorded")
	}
	next, changed, err := ApplyUpdate(landed, RecordDefectAfterLanding{Note: "broke the status line", At: at})
	if err != nil || !changed || next.CheaperFirst.DefectAfterLanding == nil || next.CheaperFirst.DefectAfterLanding.Note != "broke the status line" {
		t.Fatalf("ApplyUpdate() = %+v, %v, %v; want the defect recorded", next.CheaperFirst, changed, err)
	}
	if landed.CheaperFirst.DefectAfterLanding != nil {
		t.Fatal("the update wrote through the copy it was handed")
	}
	if _, changed, err := ApplyUpdate(next, RecordDefectAfterLanding{Note: "broke the status line", At: at}); err != nil || changed {
		t.Fatalf("recording the same defect again: changed = %v, error = %v; want nothing changed", changed, err)
	}
}

func TestTheTrialsComparisonCountsEachArm(t *testing.T) {
	t.Parallel()

	cheapLanded := trialRun("a", CheaperFirstCheaper)
	cheapLanded.Integration = &Integration{}
	cheapLanded.RepairAttempts = 2
	cheapLanded.CheaperFirst.FirstReview = "repair"
	cheapLanded.CheaperFirst.Move = &ModelMove{From: "sonnet", To: "opus"}
	strongLanded := trialRun("b", CheaperFirstStronger)
	strongLanded.Integration = &Integration{}
	strongLanded.CheaperFirst.FirstReview = "approve"
	cheapFailed := trialRun("c", CheaperFirstCheaper)
	cheapFailed.CheaperFirst.FirstReview = "approve"

	figures := SummarizeCheaperFirstTrial([]State{cheapLanded, strongLanded, cheapFailed, trialRun("d", CheaperFirstStopped)},
		map[string]float64{"a": 1.5, "b": 2.5}, 40)
	cheaper, stronger := figures.Cheaper, figures.Stronger
	if figures.Runs != 3 || figures.Target != 40 {
		t.Fatalf("figures = %+v, want 3 runs of a 40-run trial", figures)
	}
	if cheaper.Runs != 2 || cheaper.Merged != 1 || cheaper.CostUSD != 1.5 || cheaper.Unpriced != 1 || cheaper.CostPerMergedUSD != 1.5 ||
		cheaper.Reviewed != 2 || cheaper.FirstReviewApproved != 1 || cheaper.RepairRounds != 2 || cheaper.Moves != 1 {
		t.Fatalf("cheaper arm = %+v", cheaper)
	}
	if stronger.Runs != 1 || stronger.Merged != 1 || stronger.CostPerMergedUSD != 2.5 || stronger.FirstReviewApproved != 1 {
		t.Fatalf("stronger arm = %+v", stronger)
	}
}

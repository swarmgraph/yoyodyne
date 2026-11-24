package runstate

import (
	"fmt"
	"strings"
	"testing"
)

func TestASecondReviewIsKeptBoundedAndSaysWhetherItAgrees(t *testing.T) {
	t.Parallel()

	agree := ReviewComparison{DecidingDecision: "approve", CheaperDecision: "approve"}
	cheaperLooser := ReviewComparison{DecidingDecision: "repair", CheaperDecision: "approve"}
	cheaperStricter := ReviewComparison{DecidingDecision: "approve", CheaperDecision: "repair"}
	noVerdict := ReviewComparison{DecidingDecision: "approve", Problem: "the second review failed"}
	if !agree.Agree() || agree.CheaperApprovedWhatStrongerRefused() {
		t.Fatal("two approvals do not agree")
	}
	if cheaperLooser.Agree() || !cheaperLooser.CheaperApprovedWhatStrongerRefused() {
		t.Fatal("a cheaper approval of a refused change is not counted")
	}
	if cheaperStricter.Agree() || cheaperStricter.CheaperApprovedWhatStrongerRefused() {
		t.Fatal("a cheaper refusal of an approved change is counted as a cheaper approval")
	}
	if noVerdict.Compared() || noVerdict.Agree() {
		t.Fatal("a second review with no verdict is compared")
	}

	var state State
	for index := 0; index < MaxReviewComparisons+3; index++ {
		state.RecordReviewComparison(ReviewComparison{DecidingDecision: "approve", CheaperDecision: fmt.Sprint(index), CheaperSummary: strings.Repeat("x", MaxReviewSummaryBytes+10)})
	}
	if len(state.ReviewComparisons) != MaxReviewComparisons || state.ReviewComparisons[0].CheaperDecision != "3" {
		t.Fatalf("kept %d second reviews starting at %q, want the latest %d", len(state.ReviewComparisons), state.ReviewComparisons[0].CheaperDecision, MaxReviewComparisons)
	}
	if len(state.ReviewComparisons[0].CheaperSummary) > MaxReviewSummaryBytes {
		t.Fatal("the second reviewer's summary is not cut to the review summary's bound")
	}
}

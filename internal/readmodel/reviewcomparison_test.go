package readmodel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type comparisonRuns struct {
	Runs
	states []runstate.State
	err    error
}

func (r comparisonRuns) ReviewComparisonRuns() ([]runstate.State, error) { return r.states, r.err }

func comparedDocumentRun(runID, document string, comparisons ...runstate.ReviewComparison) runstate.State {
	return runstate.State{RunID: runID, WorkItemID: "item-" + runID, Document: &runstate.DocumentPublication{Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: document}}}, ReviewComparisons: comparisons}
}

func TestTheReviewComparisonListsBothVerdictsAndCountsAgreementsAndCheaperApprovals(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	pair := func(minutes int, stronger, cheaper string) runstate.ReviewComparison {
		return runstate.ReviewComparison{At: at.Add(time.Duration(minutes) * time.Minute), DecidingModel: "opus", DecidingDecision: stronger, CheaperModel: "sonnet", CheaperDecision: cheaper}
	}
	failed := runstate.ReviewComparison{At: at.Add(5 * time.Minute), DecidingModel: "opus", DecidingDecision: "approve", CheaperModel: "sonnet", Problem: "the second review failed: provider gone"}
	runs := comparisonRuns{states: []runstate.State{
		comparedDocumentRun("run-a", "design-a", pair(0, "approve", "approve")),
		comparedDocumentRun("run-b", "design-b", pair(1, "repair", "approve"), pair(2, "approve", "repair")),
		comparedDocumentRun("run-c", "design-c", failed),
	}}
	listing := ReadReviewComparison(Sources{Runs: runs, CompareDocumentReviews: true})
	if listing.Problem != "" {
		t.Fatal(listing.Problem)
	}
	if listing.Publications != 2 || listing.Compared != 3 || listing.Agreements != 1 || listing.CheaperApprovedRefused != 1 || listing.NoSecondVerdict != 1 || len(listing.Entries) != 4 {
		t.Fatalf("listing = %+v", listing)
	}
	if !listing.Entries[0].Agree || listing.Entries[1].Agree || listing.Entries[1].RunID != "run-b" {
		t.Fatalf("entries are not in review order with their agreement: %+v", listing.Entries)
	}
	rendered := listing.Render()
	for _, want := range []string{
		"Document publications reviewed a second time on the cheaper model: on. 2 of at least 30 publications reviewed both ways so far.",
		"3 pair(s) of verdicts: 1 agree, 2 disagree; the cheaper reviewer approved what the stronger refused 1 time(s).",
		"1 second review(s) reached no verdict",
		"design-b (item-run-b, run run-b): stronger opus repair, cheaper sonnet approve: disagree",
		"design-a (item-run-a, run run-a): stronger opus approve, cheaper sonnet approve: agree",
		"cheaper sonnet no verdict: not compared (the second review failed: provider gone)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the listing does not say %q:\n%s", want, rendered)
		}
	}
}

func TestTheReviewComparisonSaysWhenItIsOffOrUnreadable(t *testing.T) {
	t.Parallel()

	empty := ReadReviewComparison(Sources{Runs: comparisonRuns{}})
	if !strings.Contains(empty.Render(), "cheaper model: off. 0 of at least 30") || empty.Problem != "" {
		t.Fatalf("an empty listing = %q", empty.Render())
	}
	broken := ReadReviewComparison(Sources{Runs: comparisonRuns{err: errors.New("disk gone")}})
	if !strings.Contains(broken.Problem, "disk gone") {
		t.Fatalf("an unreadable listing = %+v", broken)
	}
}

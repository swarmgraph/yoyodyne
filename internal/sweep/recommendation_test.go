package sweep

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
)

const (
	proposalA = "amendment-0123456789abcdef0123456789abcdef"
	proposalB = "amendment-fedcba9876543210fedcba9876543210"
)

// An owning role's account carries what it recommends on the proposals it was
// put, and the block decodes with them exactly as it decodes without.
func TestAnAccountCarriesRecommendations(t *testing.T) {
	t.Parallel()

	reply := "I read both proposals against the design.\n\n" +
		"```yoyodyne-sweep\n" +
		`{"status":"complete","summary":"two argued","recommendations":[` +
		`{"proposal":"` + proposalA + `","verdict":"approve","reason":"the design does say both orderings hold"},` +
		`{"proposal":"` + proposalB + `","verdict":"merge","reason":"asks for the same clarification","into":"` + proposalA + `"}]}` +
		"\n```\n"
	_, result, _, err := Extract(reply)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result == nil || len(result.Recommendations) != 2 {
		t.Fatalf("result = %+v, want both recommendations", result)
	}
	if result.Recommendations[1].Verdict != RecommendMerge || result.Recommendations[1].Into != proposalA {
		t.Errorf("merge = %+v, want it to name the proposal it folds into", result.Recommendations[1])
	}
}

// The recommendation is an argument, so one with no reason is refused; a merge
// that names nothing to merge into, or names itself, is refused; and a verdict
// outside the three is refused. Each is what an operator would otherwise have
// to guess at.
func TestARecommendationIsHeldToItsShape(t *testing.T) {
	t.Parallel()

	for name, recommendation := range map[string]Recommendation{
		"no reason":         {Proposal: proposalA, Verdict: RecommendApprove},
		"not a proposal id": {Proposal: "yoyodyne-ifd.300", Verdict: RecommendDecline, Reason: "no"},
		"unknown verdict":   {Proposal: proposalA, Verdict: "defer", Reason: "later"},
		"merge into nothing": {
			Proposal: proposalA, Verdict: RecommendMerge, Reason: "the same change",
		},
		"merge into itself": {
			Proposal: proposalA, Verdict: RecommendMerge, Reason: "the same change", Into: proposalA,
		},
		"into on an approve": {
			Proposal: proposalA, Verdict: RecommendApprove, Reason: "yes", Into: proposalB,
		},
	} {
		if err := recommendation.Validate(); err == nil {
			t.Errorf("%s: %+v validated", name, recommendation)
		}
	}
	good := Recommendation{Proposal: proposalA, Verdict: RecommendMerge, Reason: "the same change", Into: proposalB}
	if err := good.Validate(); err != nil {
		t.Errorf("a well-formed merge was refused: %v", err)
	}
}

// One turn may recommend on at most as many proposals as the harness puts to
// it, and the contract says the same number.
func TestATurnIsBoundedToWhatItWasPut(t *testing.T) {
	t.Parallel()

	over := Result{Status: StatusComplete, Summary: "too many"}
	for i := 0; i <= MaxRecommendations; i++ {
		over.Recommendations = append(over.Recommendations, Recommendation{Proposal: proposalA, Verdict: RecommendDecline, Reason: "no"})
	}
	if err := over.validateTurn(); err == nil {
		t.Error("a turn over the recommendation bound validated")
	}
	if err := over.Validate(); err != nil {
		t.Errorf("a pass at the turn bound plus one is inside the pass bound and was refused: %v", err)
	}
	contract := RecommendationContract()
	for _, want := range []string{maxRecommendationsText, string(RecommendApprove), string(RecommendDecline), string(RecommendMerge), "yoyo amendment approve", "not a decision", `"amendment" action`} {
		if !strings.Contains(contract, want) {
			t.Errorf("the recommendation contract does not state %q:\n%s", want, contract)
		}
	}
	if maxRecommendationsText != "10" || MaxRecommendations != 10 {
		t.Errorf("the contract says %s recommendations and the code enforces %d", maxRecommendationsText, MaxRecommendations)
	}
}

// A recommendation on a real proposal is never refused over its id: the shape
// checked is the amendment package's own.
func TestTheProposalIDShapeIsTheAmendmentPackages(t *testing.T) {
	t.Parallel()

	id, err := amendment.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	recommendation := Recommendation{Proposal: id, Verdict: RecommendApprove, Reason: "right"}
	if err := recommendation.Validate(); err != nil {
		t.Errorf("a recommendation on a freshly minted proposal id %q is refused: %v", id, err)
	}
}

// Recommendations accumulate across the turns of one pass and are bounded at
// the pass cap, with the summary saying what was cut, exactly as findings are.
func TestRecommendationsMergeAcrossTurns(t *testing.T) {
	t.Parallel()

	merged := Result{Status: StatusMore, Summary: "first", Recommendations: []Recommendation{{Proposal: proposalA, Verdict: RecommendApprove, Reason: "yes"}}}
	merged = merged.Merge(Result{Status: StatusComplete, Summary: "second", Recommendations: []Recommendation{{Proposal: proposalB, Verdict: RecommendDecline, Reason: "no"}}})
	if len(merged.Recommendations) != 2 {
		t.Fatalf("merged = %+v, want both turns' recommendations", merged.Recommendations)
	}
	full := Result{Status: StatusComplete, Summary: "full"}
	for i := 0; i < MaxPassRecommendations; i++ {
		full.Recommendations = append(full.Recommendations, Recommendation{Proposal: proposalA, Verdict: RecommendDecline, Reason: "no"})
	}
	over := full.Merge(Result{Status: StatusComplete, Summary: "over", Recommendations: []Recommendation{{Proposal: proposalB, Verdict: RecommendApprove, Reason: "yes"}}})
	if len(over.Recommendations) != MaxPassRecommendations || !strings.Contains(over.Summary, "1 recommendation(s)") {
		t.Errorf("merged = %d recommendations, summary %q; want the bound held and the cut said", len(over.Recommendations), over.Summary)
	}
	if err := over.Validate(); err != nil {
		t.Fatalf("a merge held at its own bound does not validate: %v", err)
	}
}

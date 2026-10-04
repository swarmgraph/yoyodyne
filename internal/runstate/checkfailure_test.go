package runstate

import (
	"strings"
	"testing"
)

func TestAForgeCheckFailureRequiresAFullCommitID(t *testing.T) {
	t.Parallel()
	failure := CheckFailure{Command: "build", ForgeHeadCommit: "short-head"}
	if err := failure.Validate(); err == nil || !strings.Contains(err.Error(), "forge_head_commit must be a full commit id") {
		t.Fatalf("Validate() = %v, want the invalid forge revision refused", err)
	}
	failure.ForgeHeadCommit = strings.Repeat("a", 40)
	if err := failure.Validate(); err != nil {
		t.Fatal(err)
	}
	failure.ForgeHeadCommit = ""
	if err := failure.Validate(); err != nil {
		t.Fatalf("a local check was refused for having no forge revision: %v", err)
	}
}

func TestAForgeFailureKeepsLocalCleanupHistoryWithoutCurrentIntegrationCredit(t *testing.T) {
	t.Parallel()
	state := integratedState(t, PhaseComplete)
	state.WorktreeRemoved, state.BranchRemoved = true, true
	state.PullRequest = &PullRequest{
		Remote: "origin", Branch: state.Branch, Number: 84,
		URL: "https://example.test/pull/84", HeadCommit: state.Integration.SourceCommit,
	}
	state.MergeDrop = &MergeDrop{At: state.UpdatedAt, Reason: "the change failed its forge build"}
	state.CheckFailure = &CheckFailure{Command: "build", ForgeHeadCommit: state.Integration.SourceCommit, LocalPromotion: state.Integration}
	state.Integration, state.ChecksPassed = nil, nil
	state.ReviewDecision, state.ReviewSessionID, state.ReviewModel = "", "", ""
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	if state.Outstanding() || state.AwaitingForge() {
		t.Fatal("historical local cleanup granted current publication credit")
	}
	for _, test := range []struct {
		name   string
		change func(*State)
	}{
		{name: "unrecorded cleanup", change: func(state *State) { state.CheckFailure.LocalPromotion = nil }},
		{name: "different revision", change: func(state *State) { state.CheckFailure.LocalPromotion.SourceCommit = strings.Repeat("c", 40) }},
		{name: "no local promotion", change: func(state *State) { state.CheckFailure.LocalPromotion.ThroughPullRequest = true }},
		{name: "no dropped publication", change: func(state *State) { state.MergeDrop = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := state
			failure, promotion := *state.CheckFailure, *state.CheckFailure.LocalPromotion
			failure.LocalPromotion = &promotion
			changed.CheckFailure = &failure
			test.change(&changed)
			if err := changed.Validate(); err == nil {
				t.Fatal("cleanup without the matching local promotion and dropped publication was accepted")
			}
		})
	}
}

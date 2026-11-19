package testharness

import (
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The decision a development manager records when the ground moved under a
// correct change, and the guidance it leaves on the item for the developer that
// runs it again.
const (
	RerunReasoning = "the change is correct and its base moved under it: main took a rename this branch predates, so nothing here needs repairing and it needs running again from the start"
	// PriorRunID is the stopped run in the end-to-end test, which has to be a
	// different run to the one the test pipeline reserves for the fresh attempt.
	PriorRunID    = "run-99998888777766665555444433332222"
	RerunGuidance = "Triaged: to be run again from the start. The preserved branch yoyodyne/task/abc holds the finished rename; cherry-pick its second commit rather than writing it again."
	// DecidedRunID is the stoppage a decision stands about where the test is not
	// about which stoppage it was. The sequences that are about that name their
	// own run.
	DecidedRunID = "run-11112222333344445555666677778888"
	// DecidedIn and DecidedOnTurn are where the development manager recorded the
	// decision. A re-run cites them, so they are what a test asserting the
	// attribution looks for.
	DecidedIn     = "chat-0123456789abcdef"
	DecidedOnTurn = 3
)

// TriageDecided is one decision of the development manager's as the durable
// record holds it: the word, the stoppage it settles, the reasoning it was made
// on, and where it was recorded.
func TriageDecided(decision, runID string) runstate.TriageDecision {
	return runstate.TriageDecision{
		Decision:     decision,
		RunID:        runID,
		Reason:       RerunReasoning,
		DecidedBy:    "development manager",
		Conversation: DecidedIn,
		Turn:         DecidedOnTurn,
	}
}

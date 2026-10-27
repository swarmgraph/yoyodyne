package orchestrator

import (
	"os"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestTheTrackerReadFixtureMatchesTheStoppedRunWriter(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile("../chat/testdata/review-stop.notes")
	if err != nil {
		t.Fatal(err)
	}
	outcome := Outcome{
		Blocked:          true,
		RunID:            "run-review",
		Failure:          "replay conflicted with the target branch",
		Phase:            runstate.PhaseIntegrating,
		Changes:          gitworktree.ChangeSummary{Status: "M feature.go", DiffStat: "LONG_DIFF_OUTPUT"},
		ReviewBaseCommit: "base-revision",
		ReviewHeadCommit: "reviewed-revision",
		ReviewDecision:   review.DecisionApprove,
		ReviewApproves:   review.ApprovesEvidence,
		ReviewSummary:    "The change is a sound diagnosis.\nIt does not implement the item.",
		ReviewFindings: []review.Finding{{
			Severity: review.SeverityMinor, Disposition: review.DispositionOutOfScope,
			Message: "The follow-up repair is still needed.\nKeep the item open.",
		}},
	}
	if got, want := renderFailureNotes(outcome), strings.TrimSuffix(string(fixture), "\n"); got != want {
		t.Fatalf("tracker read fixture differs from the actual stop writer:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

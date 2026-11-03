package orchestrator

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
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

// The chat package's tracker read is pinned against this note too: its failure
// and its review summary each carry a line that begins with a field's name.
func TestTheLabelShapedTrackerReadFixtureMatchesTheStoppedRunWriter(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile("../chat/testdata/label-shaped-stop.notes")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	outcome := Outcome{
		Blocked: true,
		RunID:   "run-label",
		Failure: "verification failed after 2 of 2 permitted attempt(s):\nCheck: make generate (passed=false, exit=1, 2s of 30m0s)\nThe generated schema is older than its source; regenerate it and run the checks again.",
		Phase:   runstate.PhaseChecking,
		Changes: gitworktree.ChangeSummary{Status: "M feature.go", DiffStat: "LONG_DIFF_OUTPUT"},
		Checks: []checks.Result{{
			Command: "make test", Passed: true, Timeout: 30 * time.Minute,
			Process: execution.ProcessResult{StartedAt: started, FinishedAt: started.Add(5 * time.Second)},
		}},
		ReviewBaseCommit: "base-revision",
		ReviewHeadCommit: "reviewed-revision",
		ReviewDecision:   review.DecisionRepair,
		ReviewSummary:    "The change regenerates the schema.\n\nReason: the generator is still pinned to the old version, so the next run undoes it.\nPin it as well.",
		ReviewFindings: []review.Finding{{
			Severity: review.SeverityMajor,
			Message:  "Pin the generator.",
		}},
	}
	if got, want := renderFailureNotes(outcome), strings.TrimSuffix(string(fixture), "\n"); got != want {
		t.Fatalf("tracker read fixture differs from the actual stop writer:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// renderOutcomeNotes writes "Repair attempts" after the base commit, and the
// chat package's tracker read is pinned against what it writes for a raise.
func TestTheRaisedTrackerReadFixtureMatchesTheOutcomeWriter(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile("../chat/testdata/raised-stop.notes")
	if err != nil {
		t.Fatal(err)
	}
	outcome := Outcome{
		RunID:            "run-raised",
		Branch:           "branch-raised",
		WorktreePath:     "worktree-raised",
		BaseCommit:       "base-raised",
		RepairAttempts:   2,
		ReviewBaseCommit: "base-raised",
		ReviewHeadCommit: "tip-raised",
		ReviewDecision:   review.DecisionEscalate,
		ReviewSummary:    "The item asks for two things that contradict each other.\nBranch: the design names one, the criteria another.",
	}
	if got, want := renderOutcomeNotes(outcome), strings.TrimSuffix(string(fixture), "\n"); got != want {
		t.Fatalf("tracker read fixture differs from the actual outcome writer:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// renderReconciledIntegrationNotes writes where the change was integrated
// before the review, and the chat package's tracker read is pinned against it.
func TestTheReconciledTrackerReadFixtureMatchesTheReconcileWriter(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile("../chat/testdata/reconciled-integration.notes")
	if err != nil {
		t.Fatal(err)
	}
	state := runstate.State{
		RunID:  "run-reconciled",
		Phase:  runstate.PhaseIntegrating,
		Branch: "branch-reconciled",
		Integration: &runstate.Integration{
			TargetBranch: "main", SourceCommit: "source-reconciled", PreviousTargetCommit: "previous-reconciled",
		},
		ReviewSessionID:  "review-session",
		ReviewBaseCommit: "base-reconciled",
		ReviewHeadCommit: "tip-reconciled",
		ReviewDecision:   "approve",
	}
	if got, want := renderReconciledIntegrationNotes(state, false), strings.TrimSuffix(string(fixture), "\n"); got != want {
		t.Fatalf("tracker read fixture differs from the actual reconcile writer:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// reviewThenBrokenCheck is a check that passes until the developer's change
// carries broken.txt, which the developer below writes in answer to the review.
const reviewThenBrokenCheck = `test ! -f broken.txt || { echo "broken.txt must not exist" >&2; exit 3; }`

// answerReviewWithBrokenCheck is a developer whose first attempt passes the
// checks and is sent back by the reviewer, and whose answer to the review fails
// a check. Every later attempt fixes the check.
func answerReviewWithBrokenCheck() func(backend.RunRequest) error {
	attempts := 0
	return func(request backend.RunRequest) error {
		attempts++
		switch attempts {
		case 1:
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
		case 2:
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "broken.txt"), []byte("answering the review\n"), 0o600)
		default:
			return os.Remove(filepath.Join(request.WorkingDirectory, "broken.txt"))
		}
	}
}

// requireCheckAndReview fails unless a developer prompt carries both the
// failing check and the findings of the review it came after, review first.
func requireCheckAndReview(t *testing.T, label, prompt string) {
	t.Helper()
	for _, want := range []string{
		"Command: " + reviewThenBrokenCheck,
		"Exit code: 3",
		"broken.txt must not exist",
		"## The review this check failed after",
		"First an independent reviewer examined an earlier version of your change",
		"Then the change that answered them failed the check above",
		"Reviewer summary: the change misses the acceptance criteria",
		`"message": "add the missing file"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("%s is missing %q:\n%s", label, want, prompt)
		}
	}
}

// TestCheckFailingAfterAReviewKeepsTheReviewersFindings drives a review that
// sends findings back followed by a check that fails on the answer to them. The
// run record keeps both, with the review recorded as the one the check came
// after, and both reach the developer: on the next attempt in the same process,
// on the attempt a restarted process reissues, and in a fresh session's
// briefing.
func TestCheckFailingAfterAReviewKeepsTheReviewersFindings(t *testing.T) {
	t.Parallel()

	t.Run("next attempt", func(t *testing.T) {
		t.Parallel()
		repository, worktreeRoot, store := restartableFixture(t)
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
		provider := orchestratortest.RoleBackend(answerReviewWithBrokenCheck(), repairVerdict)
		pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{reviewThenBrokenCheck}), provider)
		pipeline.Config.Execution.RepairAttemptsBeforeReplan = 3
		if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
			t.Fatal("Run() error = nil, want the reviewer's standing repair to end the run")
		}
		developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
		if len(developerRequests) < 3 {
			t.Fatalf("developer invocations = %d, want the attempt after the failing check", len(developerRequests))
		}
		requireCheckAndReview(t, "the attempt after the failing check", developerRequests[2].Prompt)
	})

	t.Run("restarted and fresh session", func(t *testing.T) {
		t.Parallel()
		repository, worktreeRoot, store := restartableFixture(t)
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
		// The process dies once the attempt answering the failing check is
		// recorded and before it is issued, so what the next developer is told
		// comes from the durable record alone.
		interrupted := &interruptedStore{StateStore: store, atAttempt: 2, allowSaves: 1}
		first := orchestratortest.RoleBackend(answerReviewWithBrokenCheck(), repairVerdict)
		firstPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{reviewThenBrokenCheck}), first)
		firstOutcome, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
		if err == nil || !interrupted.stopped {
			t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.stopped)
		}
		state, err := store.Load(firstOutcome.RunID)
		if err != nil {
			t.Fatalf("Load() interrupted state error = %v", err)
		}
		if state.Phase != runstate.PhaseDeveloping || state.RepairAttempts != 2 {
			t.Fatalf("interrupted state = phase %q at attempt %d, want the developing phase at attempt 2", state.Phase, state.RepairAttempts)
		}
		failure := state.CheckFailure
		if failure == nil || failure.ExitCode != 3 || !strings.Contains(failure.Output, "broken.txt must not exist") {
			t.Fatalf("record lost the failing check: %#v", failure)
		}
		// The review is on the record beside the failure, as the one it came after.
		before := failure.ReviewBefore
		if before == nil || len(before.Findings) != 1 || before.Findings[0].Message != "add the missing file" {
			t.Fatalf("record lost the reviewer's findings when the check failed: %#v", before)
		}
		if before.Decision != runstate.ReviewRepair || before.Summary != "the change misses the acceptance criteria" || before.ReviewedCommit == "" {
			t.Fatalf("review before the check = %#v, want the repair verdict and the commit it read", before)
		}
		// Its verdict is no longer the record's verdict on the change: the change
		// it judged is not the one that now fails.
		if state.ReviewDecision != "" || len(state.ReviewFindingDetails) != 0 {
			t.Fatalf("record still holds the review as a verdict on the failing change: decision %q, findings %#v", state.ReviewDecision, state.ReviewFindingDetails)
		}

		// A developer started fresh on the run is briefed from the same record.
		freshState := state
		freshState.ProviderSessionID = ""
		repair, err := resumedDeveloperPrompt(freshState, "", "", "# Assigned work item\n", scratchForTest, []string{reviewThenBrokenCheck}, protectedpath.Protect(firstPipeline.Config), 3)
		if err != nil {
			t.Fatalf("resumedDeveloperPrompt() error = %v", err)
		}
		requireCheckAndReview(t, "a fresh session's briefing", freshSessionRepairPrompt(repair, "", "# Assigned work item\n", freshState))

		// A restarted process reissues the recorded attempt with both, and the run
		// goes on to finish.
		second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
			return os.Remove(filepath.Join(request.WorkingDirectory, "broken.txt"))
		}, approveVerdict)
		resumed := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{reviewThenBrokenCheck}), second)
		if _, err := resumed.Run(context.Background(), tracker.Item.ID); err != nil {
			t.Fatalf("resumed Run() error = %v", err)
		}
		developerRequests := second.RequestsForRole(domain.RoleDeveloper)
		if len(developerRequests) != 1 {
			t.Fatalf("resumed developer invocations = %d, want the one recorded attempt reissued", len(developerRequests))
		}
		requireCheckAndReview(t, "the reissued attempt", developerRequests[0].Prompt)
		// Once the checks pass the reviewer has judged the answer, so the review
		// the failure kept goes with it.
		finished, err := store.Load(firstOutcome.RunID)
		if err != nil {
			t.Fatalf("Load() finished state error = %v", err)
		}
		if finished.CheckFailure != nil {
			t.Fatalf("finished run kept the repaired check failure: %#v", finished.CheckFailure)
		}
	})
}

// TestASecondCheckFailureKeepsTheReviewTheFirstKept is two failing checks in a
// row after a review: the attempt between them reached no reviewer, so the
// findings are as outstanding at the second failure as they were at the first.
func TestASecondCheckFailureKeepsTheReviewTheFirstKept(t *testing.T) {
	a := activeRun{state: runstate.State{
		ReviewDecision:       runstate.ReviewRepair,
		ReviewSummary:        "the change misses the acceptance criteria",
		ReviewFindings:       1,
		ReviewFindingDetails: []runstate.Finding{{Severity: "blocker", Message: "add the missing file"}},
	}}
	a.recordCheckFailure(checkResultExiting("make test", 2))
	a.recordCheckFailure(checkResultExiting("make test", 1))
	before := a.state.CheckFailure.ReviewBefore
	if before == nil || len(before.Findings) != 1 || before.Findings[0].Message != "add the missing file" {
		t.Fatalf("second failure lost the review the first kept: %#v", a.state.CheckFailure)
	}
	if a.state.CheckFailure.ExitCode != 1 {
		t.Fatalf("record holds exit %d, want the second failure's", a.state.CheckFailure.ExitCode)
	}
	if err := a.state.CheckFailure.Validate(); err != nil {
		t.Fatalf("recorded failure does not validate: %v", err)
	}

	// A check failing with no review before it records none.
	none := activeRun{state: runstate.State{}}
	none.recordCheckFailure(checkResultExiting("make test", 2))
	if none.state.CheckFailure.ReviewBefore != nil {
		t.Fatalf("failure with no review before it recorded one: %#v", none.state.CheckFailure.ReviewBefore)
	}
}

func checkResultExiting(command string, exitCode int) checks.Result {
	return checks.Result{Command: command, Process: execution.ProcessResult{ExitCode: exitCode, Stdout: "FAIL"}}
}

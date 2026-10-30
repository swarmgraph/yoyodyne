package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// humanApprovalPublishing is a publishing pipeline whose integration a person
// approves: the shared fixture's default policy, with a remote and a forge.
func humanApprovalPublishing(t *testing.T, provider backend.Backend, commands []string) (Pipeline, *runstate.Store, *orchestratortest.Tracker, *orchestratortest.Forge, string) {
	t.Helper()
	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	pipeline, store := newPipeline(t, repository, tracker, provider, commands)
	pipeline = publishing(pipeline, forge)
	if pipeline.Config.Approvals.Integration != domain.ApprovalHuman {
		t.Fatalf("integration = %q, want the human policy this test is about", pipeline.Config.Approvals.Integration)
	}
	return pipeline, store, tracker, forge, remote
}

// requireHandedOverUnmerged holds a finished human-approval run to what it must
// leave: succeeded, nothing integrated or merged, the item neither closed nor
// blocked, the worktree and branch standing, and exactly one pull request,
// opened for the approved change and still open.
func requireHandedOverUnmerged(t *testing.T, outcome Outcome, store *runstate.Store, tracker *orchestratortest.Tracker, forge *orchestratortest.Forge, remote string) runstate.State {
	t.Helper()
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusSucceeded {
		t.Fatalf("status = %q (%s), want the approved change handed over", state.Status, state.Failure)
	}
	if state.Integration != nil || outcome.Integration != nil {
		t.Fatalf("integration = %#v, want nothing promoted under a person's approval", state.Integration)
	}
	if merges := forge.MergeRequests(); len(merges) != 0 {
		t.Fatalf("merges = %#v, want the pull request left for a person to merge", merges)
	}
	if record := tracker.Record(); record.Closed || record.Blocked {
		t.Fatalf("item closed=%t blocked=%t, want it left for the person who merges", record.Closed, record.Blocked)
	}
	if state.WorktreeRemoved || state.BranchRemoved {
		t.Fatalf("worktree removed=%t branch removed=%t, want both standing", state.WorktreeRemoved, state.BranchRemoved)
	}
	if opened := forge.OpenedRequests(); len(opened) != 1 {
		t.Fatalf("opened = %#v, want one pull request opened once, after the approval", opened)
	}
	if state.PullRequest == nil || state.PullRequest.Merged || !strings.EqualFold(state.PullRequest.State, "open") {
		t.Fatalf("pull request = %#v, want it recorded open and unmerged", state.PullRequest)
	}
	if state.PullRequest.HeadCommit != state.HarnessCommit {
		t.Fatalf("published head = %q, want the approved commit %q", state.PullRequest.HeadCommit, state.HarnessCommit)
	}
	if pushed := strings.TrimSpace(gitOutput(t, remote, "rev-parse", "refs/heads/"+state.Branch)); pushed != state.HarnessCommit {
		t.Fatalf("remote branch at %q, want the approved commit %q", pushed, state.HarnessCommit)
	}
	if state.WorkflowDivergence != "" {
		t.Fatalf("divergence = %q, want the run to walk the human-approval definition", state.WorkflowDivergence)
	}
	return state
}

// TestAHumanApprovalRunReturnsAFailingCheckToTheDeveloperAndOpensItsPullRequestOnlyOnceApproved
// is the human-approval run that fails its checks: the failure goes back to the
// same developer as a repair round, the repaired change is reviewed, and only
// then is anything pushed or a pull request opened, which is left open.
func TestAHumanApprovalRunReturnsAFailingCheckToTheDeveloperAndOpensItsPullRequestOnlyOnceApproved(t *testing.T) {
	t.Parallel()

	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return baselineImplements(request)
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "repaired.txt"), []byte("repaired\n"), 0o600)
	}, approveVerdict)
	pipeline, store, tracker, forge, remote := humanApprovalPublishing(t, provider, []string{"test -f repaired.txt"})
	reviewedBeforeOpening := false
	forge.OnEnsure = func() {
		reviewedBeforeOpening = len(provider.RequestsForRole(domain.RoleReviewer)) > 0
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state := requireHandedOverUnmerged(t, outcome, store, tracker, forge, remote)
	if developed := len(provider.RequestsForRole(domain.RoleDeveloper)); developed != 2 || state.RepairAttempts != 1 {
		t.Fatalf("developer invocations = %d, repair attempts = %d; want the failing check handed back once", developed, state.RepairAttempts)
	}
	if reviewed := len(provider.RequestsForRole(domain.RoleReviewer)); reviewed != 1 {
		t.Fatalf("reviews = %d, want the repaired change reviewed once", reviewed)
	}
	if !reviewedBeforeOpening {
		t.Fatal("the pull request was opened before the reviewer approved the change")
	}
	if state.CheckFailure != nil {
		t.Fatalf("check failure = %#v, want it cleared once the repair passed", state.CheckFailure)
	}
}

// TestAHumanApprovalRunReturnsReviewFindingsToTheDeveloper is the human-approval
// run whose first review asks for changes: the findings go back to the same
// developer on the same repair budget, the next attempt is reviewed again, and
// the approved change is handed over with its pull request open and unmerged.
func TestAHumanApprovalRunReturnsReviewFindingsToTheDeveloper(t *testing.T) {
	t.Parallel()

	provider := orchestratortest.RoleBackend(baselineImplements, repairVerdict, approveVerdict)
	pipeline, store, tracker, forge, remote := humanApprovalPublishing(t, provider, []string{"test -f feature.txt"})
	opensAtReview := []int{}
	forge.OnEnsure = func() {
		opensAtReview = append(opensAtReview, len(provider.RequestsForRole(domain.RoleReviewer)))
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state := requireHandedOverUnmerged(t, outcome, store, tracker, forge, remote)
	if developed := len(provider.RequestsForRole(domain.RoleDeveloper)); developed != 2 || state.RepairAttempts != 1 {
		t.Fatalf("developer invocations = %d, repair attempts = %d; want the findings handed back once", developed, state.RepairAttempts)
	}
	if reviewed := len(provider.RequestsForRole(domain.RoleReviewer)); reviewed != 2 {
		t.Fatalf("reviews = %d, want the repaired change reviewed again", reviewed)
	}
	if len(opensAtReview) != 1 || opensAtReview[0] != 2 {
		t.Fatalf("pull request opened after %v review(s), want once, after the approving second review", opensAtReview)
	}
}

// TestAHumanApprovalRunThatNeverPassesReviewBlocksWithNothingPublished holds the
// budget: a change no verdict approves spends the same repair budget it spends
// under automatic integration, ends blocked, and never reaches the forge.
func TestAHumanApprovalRunThatNeverPassesReviewBlocksWithNothingPublished(t *testing.T) {
	t.Parallel()

	provider := orchestratortest.RoleBackend(baselineImplements, repairVerdict)
	pipeline, store, tracker, forge, remote := humanApprovalPublishing(t, provider, []string{"test -f feature.txt"})

	outcome, _ := pipeline.Run(context.Background(), tracker.Item.ID)
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !tracker.Record().Blocked || state.Status == runstate.StatusSucceeded {
		t.Fatalf("status = %q blocked = %t, want the run blocked on its unresolved findings", state.Status, tracker.Record().Blocked)
	}
	limit := state.RepairBudget(pipeline.Config.Execution.RepairAttemptsBeforeReplan)
	if state.RepairAttempts != limit || len(provider.RequestsForRole(domain.RoleDeveloper)) != limit+1 {
		t.Fatalf("repair attempts = %d over %d developer invocations, want the budget of %d spent", state.RepairAttempts, len(provider.RequestsForRole(domain.RoleDeveloper)), limit)
	}
	if opened := forge.OpenedRequests(); len(opened) != 0 || state.PullRequest != nil {
		t.Fatalf("opened = %#v, pull request = %#v; want nothing published for an unapproved change", opened, state.PullRequest)
	}
	if listed := strings.TrimSpace(gitOutput(t, remote, "branch", "--list", state.Branch)); listed != "" {
		t.Fatalf("remote carries %q, want the unapproved branch never pushed", listed)
	}
}

// TestAPullRequestLeftForAPersonSaysTheHarnessDoesNotMergeIt holds the body a
// person merges from to what the harness does under their approval: it never
// claims the harness will merge the request.
func TestAPullRequestLeftForAPersonSaysTheHarnessDoesNotMergeIt(t *testing.T) {
	t.Parallel()

	outcome := Outcome{RunID: "run-1", WorkItemID: "yoyodyne-task", Branch: "run", BaseCommit: "1a2b3c4"}
	item := beads.WorkItem{ID: "yoyodyne-task", Title: "Task"}
	human := pullRequestBody(item, outcome, "main", false)
	if strings.Contains(human, "The harness merges this request") || !strings.Contains(human, "does not merge this request") {
		t.Errorf("body under a person's approval says the wrong thing about who merges it:\n%s", human)
	}
	if automatic := pullRequestBody(item, outcome, "main", true); !strings.Contains(automatic, "The harness merges this request") {
		t.Errorf("body under automatic integration no longer says the harness merges it:\n%s", automatic)
	}
}

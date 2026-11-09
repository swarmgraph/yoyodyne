package publicationtests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A change that landed on the target waits for the team's merge: checks that
// find the landing commit missing from the team's branch change nothing, and
// the first that finds it there closes the item naming the commit and the
// branch, and records when and where it was found.
func TestAChangeTheTeamMergesClosesItsItemAtTheCheckThatFindsIt(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	testharness.AddBareRemote(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	base := testharness.GitLine(t, repository, "rev-parse", "main")
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	landing := testharness.GitLine(t, repository, "rev-parse", "main")
	if landing == base || outcome.Integration == nil {
		t.Fatalf("main = %s (was %s), integration = %v; want the change landed on main", landing, base, outcome.Integration)
	}

	// The run as the pipeline leaves it under execution.upstream_branch: landed,
	// succeeded, and waiting, with the item still claimed.
	run := testharness.LoadRun(t, store, outcome.RunID)
	run.TeamMerge = &runstate.TeamMerge{TargetBranch: "main", UpstreamBranch: "team", LandingCommit: landing, Since: run.UpdatedAt}
	if err := store.Save(run); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	tracker.Closed, tracker.CloseReason, tracker.Item.Status = false, "", "in_progress"
	tracker.Calls, tracker.Notes, tracker.Item.Notes = nil, "", ""
	testharness.RunPipelineGit(t, repository, "push", "origin", base+":refs/heads/team")

	check := func() []orchestrator.TeamMergeSettlement {
		t.Helper()
		settled, err := orchestrator.Reconciler{
			Tracker: tracker, Worktrees: testharness.NewObserver(t, repository, worktreeRoot), Store: store,
		}.SettleTeamMerges(context.Background())
		if err != nil {
			t.Fatalf("SettleTeamMerges() error = %v", err)
		}
		return settled
	}

	// Not on the team's branch yet: nothing changes, however often it is read.
	for range 2 {
		settled := check()
		if len(settled) != 1 || settled[0].Found || settled[0].Settled || settled[0].Failure != "" {
			t.Fatalf("check = %#v, want the commit not found and nothing settled", settled)
		}
	}
	if tracker.Closed || len(tracker.Calls) != 0 || !testharness.LoadRun(t, store, run.RunID).WaitingForTeamMerge() {
		t.Fatalf("closed = %t, calls = %v; want the item untouched and the run still waiting", tracker.Closed, tracker.Calls)
	}

	// The team merges: its branch now carries the landing commit.
	testharness.RunPipelineGit(t, repository, "push", "origin", landing+":refs/heads/team")
	settled := check()
	if len(settled) != 1 || !settled[0].Found || !settled[0].Settled || settled[0].Failure != "" || settled[0].FoundOn != landing {
		t.Fatalf("check = %#v, want it found on team at %s and settled", settled, landing)
	}
	if !tracker.Closed || !strings.Contains(tracker.CloseReason, landing) || !strings.Contains(tracker.CloseReason, "team") {
		t.Fatalf("closed = %t, reason = %q; want the item closed naming the commit and the branch", tracker.Closed, tracker.CloseReason)
	}
	if !strings.Contains(tracker.Notes, "The team merged this item into team: "+landing+", the commit it landed on main as") || !strings.Contains(tracker.Notes, "team was at "+landing) {
		t.Fatalf("notes = %q, want the finding and the tip it was read at", tracker.Notes)
	}
	after := testharness.LoadRun(t, store, run.RunID)
	if after.WaitingForTeamMerge() || after.TeamMerge == nil || !after.TeamMerge.Found() || after.TeamMerge.FoundOn != landing {
		t.Fatalf("wait = %#v, want it ended with FoundAt and FoundOn recorded", after.TeamMerge)
	}

	calls := len(tracker.Calls)
	if again := check(); len(again) != 0 {
		t.Fatalf("second check = %#v, want nothing left waiting", again)
	}
	if len(tracker.Calls) != calls {
		t.Fatalf("the second check made %v on the work item", tracker.Calls[calls:])
	}
}

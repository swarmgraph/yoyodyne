package landingtests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These hold that every way an approved change can stop while landing ends the
// notes it writes on its item with the step it stopped at and what the forge
// said there — on the blocker the development manager is handed and on the
// failure note recorded after it — so nobody reading the item has to find the
// reason inside the review and the protection line, and a stop the forge had no
// part in says so rather than reading as one whose answer was lost.

// assertLandingStop holds that the blocker, the run's record of it, and the
// failure note each end on the landing's stop line, and that the line says
// everything in want.
func assertLandingStop(t *testing.T, tracker *orchestratortest.Tracker, store *runstate.Store, runID string, want ...string) {
	t.Helper()
	if !tracker.Blocked {
		t.Fatal("the stopped landing was not handed over as a blocker")
	}
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(tracker.NoteRecords) == 0 {
		t.Fatal("the stopped landing recorded no note on its item")
	}
	for name, notes := range map[string]string{
		"blocker":          tracker.BlockReason,
		"recorded blocker": state.Blocker,
		"failure note":     tracker.NoteRecords[len(tracker.NoteRecords)-1],
	} {
		last := testharness.LastLine(notes)
		if !strings.HasPrefix(last, "Landing stopped at: ") {
			t.Errorf("%s does not end on the step the landing stopped at; it ends:\n%s", name, last)
			continue
		}
		for _, phrase := range want {
			if !strings.Contains(last, phrase) {
				t.Errorf("%s's last line does not say %q:\n%s", name, phrase, last)
			}
		}
	}
}

// The shape all three stops of October 11 had: the change was approved on a
// protected main, another change merged into the same file first, and the
// replay conflicted with no repair left. Nothing was asked of the forge, and the
// note says so last instead of ending on the protection line.
func TestALandingStoppedByAReplayConflictSaysTheForgeWasNeverAsked(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, TargetProtection: publish.BranchProtection{Protected: true, By: "branch protection"}}
	forge.OnEnsure = func() { mergeConflictingRemoteChange(t, remote, "feature.txt") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("this run's answer\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "cannot be replayed onto the moved target branch") {
		t.Fatalf("Run() error = %v, want the replay conflict", err)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("merges = %v, want the forge never asked", forge.Merges)
	}
	assertLandingStop(t, tracker, store, outcome.RunID,
		"replaying the change onto main",
		"never asked to merge pull request #1",
		"the failure above is this repository's")
	if !strings.Contains(tracker.BlockReason, "main is protected on the forge") {
		t.Errorf("blocker lost the protection line:\n%s", tracker.BlockReason)
	}
}

// The forge refusing the merge is the stop the forge did speak for, and its own
// words are on the last line.
func TestALandingTheForgeRefusedEndsOnWhatTheForgeSaid(t *testing.T) {
	t.Parallel()

	run := testharness.NewProtectedRun(t, &orchestratortest.Forge{
		TargetProtection: publish.BranchProtection{Protected: true, By: "branch protection"},
		MergeErr: publish.MergeRefused{
			Number: 1,
			Method: publish.MergeCommit,
			Status: "BLOCKED",
			Reason: "At least 1 approving review is required by reviewers with write access",
		},
	})
	outcome, err := run.Pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped on the refused merge", outcome)
	}
	assertLandingStop(t, run.Tracker, run.Store, outcome.RunID,
		"asking the forge to merge pull request #1 into main",
		"What the forge said: ",
		"approving review is required")
}

// A merge the forge was asked for and never confirmed is a stop at the
// confirmation, with the forge's answer to the question.
func TestALandingWhoseMergeWasNeverConfirmedEndsOnTheConfirmation(t *testing.T) {
	t.Parallel()

	run := testharness.NewProtectedRun(t, &orchestratortest.Forge{
		TargetProtection: publish.BranchProtection{Protected: true, By: "branch protection"},
		StateErr:         errors.New("gh: Could not resolve to a PullRequest with the number of 1"),
	})
	outcome, err := run.Pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped on the unconfirmed merge", outcome)
	}
	assertLandingStop(t, run.Tracker, run.Store, outcome.RunID,
		"confirming the forge merged pull request #1",
		"Could not resolve to a PullRequest")
}

// A remote target rewritten under the run stops the landing before anything is
// promoted, at the check of where the remote stands, and says what was found.
func TestALandingStoppedByADivergedRemoteEndsOnTheRemoteCheck(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, TargetProtection: publish.BranchProtection{Protected: true, By: "ruleset"}}
	forge.OnEnsure = func() { testharness.RewriteRemoteTarget(t, remote, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped on the diverged remote", outcome)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("merges = %v, want the forge never asked", forge.Merges)
	}
	assertLandingStop(t, tracker, store, outcome.RunID,
		"checking where origin's main stands before landing",
		"What the remote showed: ",
		"The forge was never asked to merge pull request #1.")
}

// assertFailureNoteEndsOnLanding holds that the note a stopped landing records
// as it ends finishes on the step it stopped at, for the stops that hand over
// no blocker of their own.
func assertFailureNoteEndsOnLanding(t *testing.T, tracker *orchestratortest.Tracker, want ...string) {
	t.Helper()
	if len(tracker.NoteRecords) == 0 {
		t.Fatal("the stopped landing recorded no note on its item")
	}
	last := testharness.LastLine(tracker.NoteRecords[len(tracker.NoteRecords)-1])
	if !strings.HasPrefix(last, "Landing stopped at: ") {
		t.Fatalf("failure note does not end on the step the landing stopped at; it ends:\n%s", last)
	}
	for _, phrase := range want {
		if !strings.Contains(last, phrase) {
			t.Errorf("failure note's last line does not say %q:\n%s", phrase, last)
		}
	}
}

// landingWorktrees is the real worktree manager with one landing step made to
// fail the way a person would meet it.
type landingWorktrees struct {
	orchestrator.WorktreeManager
	republishErr error
	catchupErr   error
	wrongHead    bool
}

func (w *landingWorktrees) RepublishBranch(ctx context.Context, worktree gitworktree.Worktree, previousCommit string) (gitworktree.Publication, error) {
	if w.republishErr != nil {
		return gitworktree.Publication{}, w.republishErr
	}
	return w.WorktreeManager.RepublishBranch(ctx, worktree, previousCommit)
}

func (w *landingWorktrees) CatchUpTarget(ctx context.Context, targetBranch string) (gitworktree.Catchup, error) {
	if w.catchupErr != nil {
		return gitworktree.Catchup{}, w.catchupErr
	}
	return w.WorktreeManager.CatchUpTarget(ctx, targetBranch)
}

// PrepareLanding prepares the real landing and, where asked, reports it as a
// commit other than the one the pull request carries, which is what a worktree
// written to after the last attempt was published produces.
func (w *landingWorktrees) PrepareLanding(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Integration, error) {
	integration, err := w.WorktreeManager.PrepareLanding(ctx, worktree, message)
	if err == nil && w.wrongHead {
		integration.SourceCommit = otherCommit(integration.SourceCommit)
		integration.TargetCommit = integration.SourceCommit
	}
	return integration, err
}

// otherCommit is a commit name that is not commit's, of the same shape.
func otherCommit(commit string) string {
	last := "0"
	if strings.HasSuffix(commit, "0") {
		last = "1"
	}
	return commit[:len(commit)-1] + last
}

// leaseRefusingStore is the run store with another promotion holding the
// target branch for the whole wait.
type leaseRefusingStore struct {
	orchestrator.StateStore
}

func (leaseRefusingStore) LeasePromotion(_ context.Context, targetBranch string) (*runstate.Lease, error) {
	return nil, fmt.Errorf("wait to promote into %s: another promotion held the lease for the whole 15m0s wait", targetBranch)
}

// protectedLandingRun is a publishing run against a protected main whose worktree
// manager can be made to fail one landing step.
func protectedLandingRun(t *testing.T, forge *orchestratortest.Forge, worktrees *landingWorktrees) (orchestrator.Pipeline, *orchestratortest.Tracker, string) {
	t.Helper()
	repository, remote := testharness.PublishedRepository(t)
	forge.Remote = remote
	forge.TargetProtection = publish.BranchProtection{Protected: true, By: "branch protection"}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	worktrees.WorktreeManager = pipeline.Worktrees
	pipeline.Worktrees = worktrees
	return pipeline, tracker, remote
}

// Another promotion holding main for the whole wait stops the landing before
// anything is asked of anyone.
func TestALandingThatNeverGotItsTurnSaysSo(t *testing.T) {
	t.Parallel()

	forge := &orchestratortest.Forge{}
	pipeline, tracker, _ := protectedLandingRun(t, forge, &landingWorktrees{})
	pipeline.Store = leaseRefusingStore{StateStore: pipeline.Store}

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !strings.Contains(err.Error(), "held the lease") {
		t.Fatalf("Run() error = %v, want the refused turn", err)
	}
	assertFailureNoteEndsOnLanding(t, tracker,
		"waiting for this run's turn to land on main",
		"never asked to merge pull request #1")
}

// A replayed change the remote will not take onto its pull request stops the
// landing at the push, in the remote's words.
func TestALandingWhoseReplayedChangeTheRemoteRefusedSaysWhatTheRemoteShowed(t *testing.T) {
	t.Parallel()

	forge := &orchestratortest.Forge{}
	worktrees := &landingWorktrees{republishErr: errors.New("git push exited 1: ! [rejected] yoyodyne/yoyodyne-task/01234567 (stale info)")}
	pipeline, tracker, remote := protectedLandingRun(t, forge, worktrees)
	forge.OnEnsure = func() { testharness.DriftRemoteTarget(t, remote, "main") }

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !strings.Contains(err.Error(), "stale info") {
		t.Fatalf("Run() error = %v, want the refused push", err)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("merges = %v, want the forge never asked", forge.Merges)
	}
	assertFailureNoteEndsOnLanding(t, tracker,
		"publishing the replayed change to pull request #1",
		"What the remote showed: ",
		"stale info",
		"The forge was never asked to merge pull request #1.")
}

// A pull request that does not carry the approved commit is never merged, and
// the landing says the refusal was this repository's own.
func TestALandingWhosePullRequestCarriesAnotherCommitSaysTheForgeWasNeverAsked(t *testing.T) {
	t.Parallel()

	forge := &orchestratortest.Forge{}
	pipeline, tracker, _ := protectedLandingRun(t, forge, &landingWorktrees{wrongHead: true})
	store := pipeline.Store.(*runstate.Store)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped on the pull request carrying another commit", outcome)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("merges = %v, want the forge never asked", forge.Merges)
	}
	assertLandingStop(t, tracker, store, outcome.RunID,
		"checking that pull request #1 carries the approved commit",
		"never asked to merge pull request #1",
		"the failure above is this repository's")
}

// A remote that moved just before the merge was asked for, and that the local
// main then could not be brought onto, stops the landing at the catch-up.
func TestALandingThatCouldNotCatchUpBeforeTheMergeSaysWhatTheRemoteShowed(t *testing.T) {
	t.Parallel()

	forge := &orchestratortest.Forge{MergeResets: 1}
	worktrees := &landingWorktrees{catchupErr: errors.New("fatal: refusing to fetch into branch 'refs/heads/main' checked out at the primary checkout")}
	pipeline, tracker, remote := protectedLandingRun(t, forge, worktrees)
	forge.AfterMergeReset = func() { testharness.DriftRemoteTarget(t, remote, "main") }
	pipeline = testharness.Waiting(pipeline, &testharness.PausingClock{Instant: testharness.BaseTime}, 6*time.Hour, 6*time.Hour)

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !strings.Contains(err.Error(), "refusing to fetch") {
		t.Fatalf("Run() error = %v, want the refused catch-up", err)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("merges = %v, want the dropped attempt the only time the forge was asked", forge.Merges)
	}
	assertFailureNoteEndsOnLanding(t, tracker,
		"bringing the local main onto origin's, which moved before the forge was asked to merge",
		"What the remote showed: ",
		"refusing to fetch")
}

// A run that stops anywhere but its landing says nothing about landing.
func TestAStopBeforeLandingWritesNoLandingLine(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 1"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() succeeded over a failing check")
	}
	if strings.Contains(tracker.Notes+tracker.BlockReason, "Landing stopped at") {
		t.Fatalf("a run stopped at its checks says it stopped landing:\n%s\n%s", tracker.Notes, tracker.BlockReason)
	}
}

// mergeConflictingRemoteChange is another pull request merging into the remote
// target a change to the same file this run changes, which is what moved main
// under the three landings that stopped on October 11.
func mergeConflictingRemoteChange(t *testing.T, remote, file string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "other-pull-request")
	testharness.RunPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	testharness.RunPipelineGit(t, clone, "config", "user.name", "Someone Else")
	testharness.RunPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	testharness.DisablePipelineMaintenance(t, clone)
	if err := os.WriteFile(filepath.Join(clone, file), []byte("another change's answer\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	testharness.RunPipelineGit(t, clone, "add", ".")
	testharness.RunPipelineGit(t, clone, "commit", "-m", "another pull request")
	testharness.RunPipelineGit(t, clone, "push", "origin", "HEAD:refs/heads/main")
}

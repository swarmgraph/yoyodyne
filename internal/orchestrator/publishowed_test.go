package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// refusingPushWorktrees answers its first branch pushes with the
// errors it was given and publishes for real after that. reviewsAtPush is how
// many reviewer invocations had been made when each push was asked for, which
// is what says whether a push came before or after the review.
type refusingPushWorktrees struct {
	WorktreeManager
	refusals      []error
	provider      *orchestratortest.Backend
	reviewsAtPush []int
}

func (w *refusingPushWorktrees) PublishBranch(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Publication, error) {
	w.reviewsAtPush = append(w.reviewsAtPush, len(w.provider.RequestsForRole(domain.RoleReviewer)))
	if len(w.reviewsAtPush) <= len(w.refusals) {
		return gitworktree.Publication{}, w.refusals[len(w.reviewsAtPush)-1]
	}
	return w.WorktreeManager.PublishBranch(ctx, worktree, message)
}

func identityRefusal() error {
	return fmt.Errorf("%w: git push exited 255: No user exists for uid 501", gitworktree.ErrRemoteAuthRefused)
}

// identityStoppedRun runs the pipeline until its first push is refused for want
// of a GitHub identity and returns the stopped record.
func identityStoppedRun(t *testing.T, refusals ...error) (Pipeline, *runstate.Store, *orchestratortest.Backend, *refusingPushWorktrees, *orchestratortest.Tracker, runstate.State) {
	t.Helper()
	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pushes := &refusingPushWorktrees{WorktreeManager: pipeline.Worktrees, refusals: refusals, provider: provider}
	pipeline.Worktrees = pushes
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatalf("Run() error = nil, want the refused push to stop the run")
	}
	stopped, loadErr := store.Load(outcome.RunID)
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	return pipeline, store, provider, pushes, tracker, stopped
}

// takenUp is the stopped run as the pass that takes it up leaves it: live at its
// checks, owing the push.
func takenUp(stopped runstate.State) runstate.State {
	live := stopped
	live.IdentityTakeUps = []runstate.IdentityTakeUp{{Reason: "GitHub answers again", TakenUpAt: stopped.UpdatedAt, SupersededFailure: stopped.Failure}}
	live.PublishOwed = true
	live.Failure = ""
	live.Status = runstate.StatusRunning
	live.Phase = runstate.PhaseChecking
	live.CompletedAt = nil
	live.StopClass = ""
	return live
}

func TestARunStoppedByGitHubIdentityIsRecordedAsSuch(t *testing.T) {
	t.Parallel()

	_, _, _, pushes, _, stopped := identityStoppedRun(t, identityRefusal())
	if stopped.Status != runstate.StatusFailed || stopped.StopClass != runstate.StopPublish {
		t.Fatalf("stopped run = %s, class %s; want failed at publish", stopped.Status, stopped.StopClass)
	}
	if stopped.IdentityStop == nil || !strings.Contains(stopped.IdentityStop.Detail, "No user exists for uid 501") || stopped.IdentityStop.RecordedAt.IsZero() {
		t.Fatalf("identity stop = %#v, want the refusal recorded", stopped.IdentityStop)
	}
	if stopped.PublishOwed {
		t.Fatal("a run that has not been taken up owes nothing yet")
	}
	if len(pushes.reviewsAtPush) != 1 {
		t.Fatalf("pushes = %v, want the one refused", pushes.reviewsAtPush)
	}
}

func TestAPushThatFailedForAnotherReasonRecordsNoIdentityStop(t *testing.T) {
	t.Parallel()

	_, _, _, _, _, stopped := identityStoppedRun(t, errors.New("push rejected: non-fast-forward"))
	if stopped.Status != runstate.StatusFailed || stopped.IdentityStop != nil {
		t.Fatalf("stopped run = %s with identity stop %#v, want a failure with none", stopped.Status, stopped.IdentityStop)
	}
}

func TestAnOwedPushIsMadeAfterTheChecksAndBeforeTheReview(t *testing.T) {
	t.Parallel()

	pipeline, store, provider, pushes, tracker, stopped := identityStoppedRun(t, identityRefusal())
	live := takenUp(stopped)
	if !continuedAtCheckStage(live) {
		t.Fatalf("taken-up run %#v is not adoptable at its checks", live)
	}
	if err := store.ReserveLive(context.Background(), live, 4); err != nil {
		t.Fatalf("ReserveLive() error = %v", err)
	}
	outcome, err := pipeline.Continue(context.Background(), tracker.Item.ID, live.RunID)
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if len(pushes.reviewsAtPush) != 2 || pushes.reviewsAtPush[1] != 0 {
		t.Fatalf("reviews made when each push was asked for = %v, want the owed push before any review", pushes.reviewsAtPush)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) == 0 {
		t.Fatal("the reviewer was never invoked after the owed push")
	}
	final, err := store.Load(live.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.PublishOwed || final.IdentityStop != nil || final.PullRequest == nil {
		t.Fatalf("final run owes %t, identity stop %#v, pull request %#v; want the push made and the stop cleared", final.PublishOwed, final.IdentityStop, final.PullRequest)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome status = %s, want succeeded", outcome.Status)
	}
}

func TestAnOwedPushRefusedAgainStopsTheRunBeforeTheReview(t *testing.T) {
	t.Parallel()

	pipeline, store, provider, pushes, tracker, stopped := identityStoppedRun(t, identityRefusal(), identityRefusal())
	live := takenUp(stopped)
	if err := store.ReserveLive(context.Background(), live, 4); err != nil {
		t.Fatalf("ReserveLive() error = %v", err)
	}
	if _, err := pipeline.Continue(context.Background(), tracker.Item.ID, live.RunID); err == nil {
		t.Fatal("Continue() error = nil, want the refused push to stop the run")
	}
	if len(pushes.reviewsAtPush) != 2 {
		t.Fatalf("pushes = %v, want the original and the owed one", pushes.reviewsAtPush)
	}
	if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 0 {
		t.Fatalf("reviewer invocations = %d, want none before the push was made", len(reviews))
	}
	final, err := store.Load(live.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.Status != runstate.StatusFailed || final.StopClass != runstate.StopPublish || final.IdentityStop == nil || !final.PublishOwed {
		t.Fatalf("final run = %s, class %s, identity stop %#v, owed %t; want failed at publish, stopped on identity, still owing the push",
			final.Status, final.StopClass, final.IdentityStop, final.PublishOwed)
	}
}

func TestARunOwingAPushIsAdoptedAtItsChecksOnlyWhenTakenUp(t *testing.T) {
	t.Parallel()

	state := continuableState()
	state.Status, state.Phase, state.RepairAttempts = runstate.StatusRunning, runstate.PhaseChecking, 0
	state.PublishOwed = true
	if continuedAtCheckStage(state) {
		t.Fatal("a run owing a push with no take-up recorded was adopted")
	}
	state.IdentityTakeUps = []runstate.IdentityTakeUp{{Reason: "GitHub answers", TakenUpAt: state.UpdatedAt}}
	if !continuedAtCheckStage(state) {
		t.Fatal("a taken-up run owing a push was not adopted at its checks")
	}
	state.PublishOwed = false
	if continuedAtCheckStage(state) {
		t.Fatal("a take-up alone adopted a run that owes nothing")
	}
}

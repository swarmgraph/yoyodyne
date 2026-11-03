package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive bringing a pull request a person is to merge up to date when
// other contributors' work moves its target (yoyodyne-ifd.429.101). Each case is
// a whole run, under a merge setting that leaves the merge to a person, that
// ended waiting on its pull request; the remote target then moves, and the
// forge check is run with the run it makes live hosted, as `yoyo reconcile`
// hosts it.

type personMergeUpdateFixture struct {
	repository   string
	remote       string
	worktreeRoot string
	store        *runstate.Store
	tracker      *orchestratortest.Tracker
	provider     *orchestratortest.Backend
	forge        *orchestratortest.CheckedForge
	pipeline     Pipeline
	run          runstate.State
}

func newPersonMergeUpdateFixture(t *testing.T) personMergeUpdateFixture {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	remote := addBareRemote(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The developer writes the change, and on every later attempt — a conflict
	// handed back — settles the file to the change's own content again.
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	forge := &orchestratortest.CheckedForge{Forge: &orchestratortest.Forge{Remote: remote}}
	pipeline := publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider), forge)
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	run, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !run.WaitingForPersonMerge() {
		t.Fatalf("run = %#v, want it waiting for a person to merge its pull request", run.PersonMerge)
	}
	// The forge reports the request's head behind its target from here on; the
	// remote is moved by each test itself.
	forge.Reading = publish.CheckReading{HeadCommit: run.PullRequest.HeadCommit, BehindBy: 1}
	return personMergeUpdateFixture{
		repository: repository, remote: remote, worktreeRoot: worktreeRoot, store: store,
		tracker: tracker, provider: provider, forge: forge, pipeline: pipeline, run: run,
	}
}

// reconciler is the forge check `yoyo reconcile` runs: the forge's checks
// read, a free developer slot, and the run it makes live hosted through the
// same pipeline.
func (f personMergeUpdateFixture) reconciler(t *testing.T) Reconciler {
	t.Helper()
	return Reconciler{
		Tracker:   f.tracker,
		Worktrees: newObserver(t, f.repository, f.worktreeRoot),
		Store:     f.store,
		Publisher: f.forge,
		Checks:    f.forge,
		Capacity:  1,
		HostsRuns: true,
		Continue:  f.pipeline.Continue,
	}
}

// check runs one forge check and hosts what it made live.
func (f personMergeUpdateFixture) check(t *testing.T) (PersonMergeSettlement, []UpdateContinuation) {
	t.Helper()
	reconciler := f.reconciler(t)
	settled, err := reconciler.SettlePersonMerges(context.Background())
	if err != nil {
		t.Fatalf("SettlePersonMerges() error = %v", err)
	}
	if len(settled) != 1 {
		t.Fatalf("check = %#v, want the one waiting pull request read", settled)
	}
	updates, err := reconciler.ContinueUpdates(context.Background())
	if err != nil {
		t.Fatalf("ContinueUpdates() error = %v", err)
	}
	return settled[0], updates
}

func (f personMergeUpdateFixture) load(t *testing.T) runstate.State {
	t.Helper()
	run, err := f.store.Load(f.run.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return run
}

func (f personMergeUpdateFixture) reviews() int {
	count := 0
	for _, request := range f.provider.RequestsMade() {
		if request.Role == domain.RoleReviewer {
			count++
		}
	}
	return count
}

// moveTargetOnFeature lands another contributor's version of the change's own
// file on the remote target, so the change conflicts when it is replayed.
func moveTargetOnFeature(t *testing.T, remote, content string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "contributor")
	runPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	runPipelineGit(t, clone, "config", "user.name", "Someone Else")
	runPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	disablePipelineMaintenance(t, clone)
	if err := os.WriteFile(filepath.Join(clone, "feature.txt"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, clone, "add", ".")
	runPipelineGit(t, clone, "commit", "-m", "someone else's version of the feature")
	runPipelineGit(t, clone, "push", "origin", "HEAD:refs/heads/main")
}

// A target another contributor moved is caught up with cleanly: the merge
// authority is withdrawn first, the change is replayed onto the new target,
// checked and reviewed again, and pushed to the same pull request, which waits
// for the person again.
func TestAWaitingPullRequestTheTargetMovedPastIsReplayedRecheckedReviewedAndPushed(t *testing.T) {
	t.Parallel()
	fixture := newPersonMergeUpdateFixture(t)
	driftRemoteTarget(t, fixture.remote, "main")
	remoteMain := publishedCommit(t, fixture.remote, "main")
	reviews := fixture.reviews()

	settled, updates := fixture.check(t)
	if !settled.Updating || settled.BehindBy != 1 || settled.Failure != "" || settled.Kept != "" {
		t.Fatalf("check = %#v, want the run put back to bring its pull request up to date", settled)
	}
	if len(fixture.forge.Withdrawn) != 1 || fixture.forge.Withdrawn[0] != fixture.run.PullRequest.Number {
		t.Fatalf("withdrawn = %v, want the request's merge authority withdrawn before its head is rewritten", fixture.forge.Withdrawn)
	}
	if len(updates) != 1 || !updates[0].Continued || updates[0].Failure != "" {
		t.Fatalf("updates = %#v, want the run hosted through its update", updates)
	}

	after := fixture.load(t)
	if !after.WaitingForPersonMerge() || after.PersonMerge.Number != fixture.run.PullRequest.Number {
		t.Fatalf("run = %s/%s, wait %#v; want it waiting for the person again on the same pull request", after.Status, after.Phase, after.PersonMerge)
	}
	pushed := publishedCommit(t, fixture.remote, fixture.run.Branch)
	if pushed == fixture.run.PullRequest.HeadCommit || after.PullRequest.HeadCommit != pushed {
		t.Fatalf("published head = %s (was %s, record says %s), want the replayed head pushed and recorded", pushed, fixture.run.PullRequest.HeadCommit, after.PullRequest.HeadCommit)
	}
	if parent := gitLine(t, fixture.remote, "rev-parse", pushed+"^"); parent != remoteMain {
		t.Fatalf("the pushed head sits on %s, want it on the moved target %s", parent, remoteMain)
	}
	if fixture.reviews() != reviews+1 || after.ReviewHeadCommit != pushed {
		t.Fatalf("reviews = %d (was %d), reviewed head %s; want the replayed head reviewed again before it was pushed", fixture.reviews(), reviews, after.ReviewHeadCommit)
	}
	if after.UpToDateConflicts != 0 || after.RepairAttempts != fixture.run.RepairAttempts {
		t.Fatalf("conflicts %d, repairs %d; a clean replay spends neither", after.UpToDateConflicts, after.RepairAttempts)
	}
	if fixture.tracker.Closed || fixture.tracker.Blocked {
		t.Fatalf("tracker = %v, want the item still claimed and waiting", fixture.tracker.Calls)
	}
	if !strings.Contains(fixture.tracker.Notes, fmt.Sprintf("bringing pull request #%d up to date", fixture.run.PullRequest.Number)) {
		t.Errorf("the item was not told its pull request is being brought up to date:\n%s", fixture.tracker.Notes)
	}

	// The target has not moved again, so the next check leaves the request alone.
	fixture.forge.Reading.BehindBy = 0
	withdrawn := len(fixture.forge.Withdrawn)
	if settled, updates := fixture.check(t); settled.Updating || len(updates) != 0 || len(fixture.forge.Withdrawn) != withdrawn {
		t.Fatalf("second check = %#v / %#v, want a request level with its target left as it stands", settled, updates)
	}
}

// A request the forge is already merging is left alone: when the check finds
// it, nothing is withdrawn or replayed; and when a person starts the merge
// while the update runs, the replayed head is not pushed over it.
func TestAMergeAPersonHasInProgressIsNeverPushedOver(t *testing.T) {
	t.Parallel()
	t.Run("found by the check", func(t *testing.T) {
		t.Parallel()
		fixture := newPersonMergeUpdateFixture(t)
		driftRemoteTarget(t, fixture.remote, "main")
		fixture.forge.Queued = true

		settled, updates := fixture.check(t)
		if settled.Updating || !strings.Contains(settled.Kept, "in progress") || len(updates) != 0 {
			t.Fatalf("check = %#v / %#v, want the request left alone while it is being merged", settled, updates)
		}
		if len(fixture.forge.Withdrawn) != 0 || !fixture.forge.HoldsQueuedMerge() {
			t.Fatalf("withdrawn = %v, want the person's merge untouched", fixture.forge.Withdrawn)
		}
		if after := fixture.load(t); !after.WaitingForPersonMerge() || publishedCommit(t, fixture.remote, fixture.run.Branch) != fixture.run.PullRequest.HeadCommit {
			t.Fatalf("run = %s, want it still waiting with its head as published", after.Status)
		}
	})
	t.Run("started during the update", func(t *testing.T) {
		t.Parallel()
		fixture := newPersonMergeUpdateFixture(t)
		driftRemoteTarget(t, fixture.remote, "main")
		reconciler := fixture.reconciler(t)
		settled, err := reconciler.SettlePersonMerges(context.Background())
		if err != nil || len(settled) != 1 || !settled[0].Updating {
			t.Fatalf("SettlePersonMerges() = %#v, %v; want the run put back", settled, err)
		}
		// A person starts the merge after the authority was withdrawn and before
		// the replayed head is pushed.
		fixture.forge.Queued = true
		updates, err := reconciler.ContinueUpdates(context.Background())
		if err != nil || len(updates) != 1 || updates[0].Failure != "" {
			t.Fatalf("ContinueUpdates() = %#v, %v", updates, err)
		}
		if pushed := publishedCommit(t, fixture.remote, fixture.run.Branch); pushed != fixture.run.PullRequest.HeadCommit {
			t.Fatalf("published head = %s, want the head the person is merging (%s) left as it was", pushed, fixture.run.PullRequest.HeadCommit)
		}
		after := fixture.load(t)
		if !after.WaitingForPersonMerge() || after.PullRequest.HeadCommit != fixture.run.PullRequest.HeadCommit {
			t.Fatalf("run = %s, head %s; want it waiting for the person again on the head it published", after.Status, after.PullRequest.HeadCommit)
		}
		if !strings.Contains(fixture.tracker.Notes, "did not push the update") {
			t.Errorf("the item was not told why the update was not pushed:\n%s", fixture.tracker.Notes)
		}
	})
}

// A replay that conflicts is a round for the developer with the conflicting
// paths named, counted against its own limit of three and never against the
// repair attempts. The conflict after the third round sends the run to the
// development manager with the change kept.
func TestConflictsBringingAWaitingPullRequestUpToDateAreRoundsUnderTheirOwnLimit(t *testing.T) {
	t.Parallel()
	fixture := newPersonMergeUpdateFixture(t)
	repairs := fixture.run.RepairAttempts

	for round := 1; round <= runstate.MaxUpToDateConflicts; round++ {
		moveTargetOnFeature(t, fixture.remote, fmt.Sprintf("someone else's version %d\n", round))
		developers := fixture.provider.DeveloperAttempts
		settled, updates := fixture.check(t)
		if !settled.Updating || len(updates) != 1 || updates[0].Failure != "" {
			t.Fatalf("round %d: check = %#v / %#v, want the run updated", round, settled, updates)
		}
		after := fixture.load(t)
		if after.UpToDateConflicts != round || after.RepairAttempts != repairs {
			t.Fatalf("round %d: conflicts %d, repairs %d (was %d); want the round counted on its own limit", round, after.UpToDateConflicts, after.RepairAttempts, repairs)
		}
		if fixture.provider.DeveloperAttempts != developers+1 {
			t.Fatalf("round %d: the developer was invoked %d times, want once to settle the conflict", round, fixture.provider.DeveloperAttempts-developers)
		}
		requests := fixture.provider.RequestsMade()
		var prompt string
		for _, request := range requests {
			if request.Role == domain.RoleDeveloper {
				prompt = request.Prompt
			}
		}
		if !strings.Contains(prompt, "- feature.txt") || !strings.Contains(prompt, fmt.Sprintf("conflict round %d of %d", round, runstate.MaxUpToDateConflicts)) {
			t.Fatalf("round %d: the developer was not handed the conflicting path and the round:\n%s", round, prompt)
		}
		if !after.WaitingForPersonMerge() {
			t.Fatalf("round %d: run = %s/%s, want the settled change pushed and waiting again", round, after.Status, after.Phase)
		}
		if pushed := publishedCommit(t, fixture.remote, fixture.run.Branch); after.PullRequest.HeadCommit != pushed {
			t.Fatalf("round %d: published head %s, record %s", round, pushed, after.PullRequest.HeadCommit)
		}
	}

	moveTargetOnFeature(t, fixture.remote, "someone else's version 4\n")
	developers := fixture.provider.DeveloperAttempts
	published := publishedCommit(t, fixture.remote, fixture.run.Branch)
	settled, updates := fixture.check(t)
	if !settled.Updating || len(updates) != 1 {
		t.Fatalf("last check = %#v / %#v, want the run updated", settled, updates)
	}
	after := fixture.load(t)
	if fixture.provider.DeveloperAttempts != developers {
		t.Fatalf("the developer was invoked again past the limit")
	}
	if after.Status.InFlight() || after.WaitingForPersonMerge() || after.StopClass != runstate.StopIntegrationBudget {
		t.Fatalf("run = %s, stop %q; want it stopped for the development manager", after.Status, after.StopClass)
	}
	if !fixture.tracker.Blocked || !strings.Contains(fixture.tracker.Notes, "conflict rounds") || !strings.Contains(fixture.tracker.Notes, "development manager") {
		t.Fatalf("blocked = %t, notes:\n%s\nwant the item blocked for the development manager", fixture.tracker.Blocked, fixture.tracker.Notes)
	}
	if after.ReplayConflict == nil || len(after.ReplayConflict.Paths) != 1 || after.ReplayConflict.Paths[0] != "feature.txt" {
		t.Fatalf("conflict = %#v, want it kept on the run naming feature.txt", after.ReplayConflict)
	}
	if after.WorktreeRemoved || after.BranchRemoved || publishedCommit(t, fixture.remote, fixture.run.Branch) != published {
		t.Fatalf("worktree removed %t, branch removed %t; want the change kept and the pull request untouched", after.WorktreeRemoved, after.BranchRemoved)
	}
}

// A request behind its target that the harness cannot bring up to date, for a
// reason no later check clears, is told on the item once and recorded on the
// wait for the development manager, and nothing is withdrawn or replayed. The
// wait goes on, so a later check still settles a merge.
func TestAWaitingPullRequestThatCannotBeBroughtUpToDateGoesToTheDevelopmentManagerOnce(t *testing.T) {
	t.Parallel()
	fixture := newPersonMergeUpdateFixture(t)
	gone := fixture.load(t)
	gone.ProviderSessionID = ""
	if err := fixture.store.Save(gone); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	driftRemoteTarget(t, fixture.remote, "main")

	settled, updates := fixture.check(t)
	if settled.Updating || settled.Failure != "" || !strings.Contains(settled.Kept, "its record names no developer session") || !strings.Contains(settled.Kept, "development manager") || len(updates) != 0 {
		t.Fatalf("check = %#v / %#v, want the request left as it stands and given to the development manager", settled, updates)
	}
	if len(fixture.forge.Withdrawn) != 0 {
		t.Fatalf("withdrawn = %v, want nothing withdrawn for a request the harness will not update", fixture.forge.Withdrawn)
	}
	after := fixture.load(t)
	if !after.WaitingForPersonMerge() || !strings.Contains(after.PersonMerge.UpdateRefused, "no developer session") {
		t.Fatalf("wait = %#v, want it still waiting with the reason recorded", after.PersonMerge)
	}
	const told = "cannot bring this item's waiting pull request up to date"
	if strings.Count(fixture.tracker.Notes, told) != 1 || !strings.Contains(fixture.tracker.Notes, "development manager decides") {
		t.Fatalf("item notes:\n%s\nwant one note naming the development manager", fixture.tracker.Notes)
	}

	if settled, _ := fixture.check(t); !strings.Contains(settled.Kept, "development manager") || strings.Count(fixture.tracker.Notes, told) != 1 {
		t.Fatalf("second check = %#v, notes told %d times; want the same answer and no second note", settled, strings.Count(fixture.tracker.Notes, told))
	}
}

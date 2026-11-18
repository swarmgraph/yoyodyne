package orchestrator

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
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// gitHubUnreachable is what git printed when the machine could not reach GitHub
// at all, in its own words.
func gitHubUnreachable(what string) error {
	return fmt.Errorf("%s failed with exit code 128: ssh: connect to host ssh.github.com port 443: Undefined error: 0\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.", what)
}

// unreachableTargetReads cannot reach GitHub for the first reads of where the
// remote target stands, and reads it normally afterwards.
type unreachableTargetReads struct {
	WorktreeManager
	failures int
	reads    int
}

func (w *unreachableTargetReads) VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error {
	w.reads++
	if w.reads <= w.failures {
		return gitHubUnreachable("resolve " + integration.TargetBranch + " on origin")
	}
	return w.WorktreeManager.VerifyRemoteTarget(ctx, integration)
}

// unreachableMerges cannot reach GitHub for the first merge requests, and
// merges normally afterwards.
type unreachableMerges struct {
	*orchestratortest.Forge
	failures int
	asked    int
}

func (f *unreachableMerges) Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error) {
	f.asked++
	if f.asked <= f.failures {
		return publish.MergeResult{}, gitHubUnreachable(fmt.Sprintf("gh pr merge %d", request.Number))
	}
	return f.Forge.Merge(ctx, request)
}

// unreachableMergeStates cannot reach GitHub for the first questions whether the
// pull request merged, after GitHub has accepted the merge.
type unreachableMergeStates struct {
	*orchestratortest.Forge
	failures int
	asked    int
}

func (f *unreachableMergeStates) State(ctx context.Context, branch string) (publish.PullRequest, error) {
	f.asked++
	if f.asked <= f.failures {
		return publish.PullRequest{}, gitHubUnreachable("gh pr view " + branch)
	}
	return f.Forge.State(ctx, branch)
}

// unreachableConfirmations cannot reach GitHub for the first reads that the
// target carries the merge.
type unreachableConfirmations struct {
	WorktreeManager
	failures int
	reads    int
}

func (w *unreachableConfirmations) ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration, mergeCommit string) (string, error) {
	w.reads++
	if w.reads <= w.failures {
		return "", gitHubUnreachable("fetch " + integration.TargetBranch + " from origin")
	}
	return w.WorktreeManager.ConfirmRemoteTarget(ctx, integration, mergeCommit)
}

// forgeWaitFixture is an approved change landing through its pull request onto
// a protected target, on a hand-driven clock that records, at every wait,
// what the run's record said while it waited.
type forgeWaitFixture struct {
	pipeline Pipeline
	store    *runstate.Store
	tracker  *orchestratortest.Tracker
	forge    *orchestratortest.Forge
	clock    *pausingClock
	// during is the run's record as it stood at each wait, and leaseFree whether
	// the target's promotion lease could be taken by another run then.
	during    []runstate.State
	leaseFree []bool
}

func newForgeWaitFixture(t *testing.T, protected bool, publisher func(*orchestratortest.Forge) PullRequests) *forgeWaitFixture {
	t.Helper()
	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	if protected {
		forge.TargetProtection = publish.BranchProtection{Protected: true, By: "branch protection"}
	}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, publisher(forge), []string{"exit 0"})
	fixture := &forgeWaitFixture{pipeline: pipeline, store: store, tracker: tracker, forge: forge, clock: &pausingClock{now: baseTime}}
	fixture.clock.onSleep = func() {
		incomplete, err := store.Incomplete()
		if err != nil {
			t.Errorf("Incomplete() error = %v", err)
			return
		}
		for _, state := range incomplete {
			if state.ForgeWait != nil {
				fixture.during = append(fixture.during, state)
				// No long wait on GitHub is made holding the promotion lease: another
				// run can take it while this one waits.
				leaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				lease, err := store.LeasePromotion(leaseCtx, "main")
				cancel()
				fixture.leaseFree = append(fixture.leaseFree, err == nil)
				if err == nil {
					_ = lease.Release()
				}
			}
		}
	}
	fixture.pipeline = waiting(pipeline, fixture.clock, 6*time.Hour, 6*time.Hour)
	return fixture
}

// assertWaitedOutAndLanded holds a run that met an unreachable GitHub at its
// merge step to what the operator's rule asks: it waited, longer than the
// two-hour window any one boundary is given, holding no developer slot and
// saying so, then carried on and landed, charged nothing and put to nobody.
func (f *forgeWaitFixture) assertWaitedOutAndLanded(t *testing.T, outcome Outcome, err error, failures int, step string, promoted bool) {
	t.Helper()
	if err != nil {
		t.Fatalf("Run() error = %v, want GitHub waited out and the change landed", err)
	}
	if outcome.PullRequest == nil || !outcome.PullRequest.Merged || outcome.PublishFailure != "" {
		t.Fatalf("pull request = %#v, publish failure = %q, want the approved request merged", outcome.PullRequest, outcome.PublishFailure)
	}
	if len(f.forge.Merges) != 1 {
		t.Fatalf("merges = %#v, want exactly one", f.forge.Merges)
	}
	if outcome.Blocked || f.tracker.Blocked || outcome.RepairAttempts != 0 || outcome.IntegrationStop != nil {
		t.Fatalf("blocked = %t (tracker %t), repairs = %d, integration stop = %#v; want the wait charged nothing and put to nobody",
			outcome.Blocked, f.tracker.Blocked, outcome.RepairAttempts, outcome.IntegrationStop)
	}
	// It waited past the window a single boundary is given, on the paced
	// intervals, rather than ending when the window ran out.
	if got := f.clock.waited(); got <= recovery.Window {
		t.Fatalf("waited %s, want the wait to outlast the %s window a boundary is given", got, recovery.Window)
	}
	if len(f.clock.slept) != failures {
		t.Fatalf("waits = %d (%v), want one per failed try, %d", len(f.clock.slept), f.clock.slept, failures)
	}
	for index, slept := range f.clock.slept {
		if want := recovery.Interval(index + 1); slept != want {
			t.Fatalf("wait %d = %s, want the paced interval %s", index+1, slept, want)
		}
	}
	// While it waited, its record said so in plain words, said when it next
	// tries, and held no developer slot.
	if len(f.during) != failures {
		t.Fatalf("records seen waiting = %d, want %d", len(f.during), failures)
	}
	for index, state := range f.during {
		wait := state.ForgeWait
		if wait.Tries != index+1 || !strings.Contains(wait.Step, step) || !strings.Contains(wait.Failure, "ssh: connect to host") {
			t.Fatalf("wait %d recorded %#v, want try %d at %q with ssh's words", index+1, wait, index+1, step)
		}
		if state.HoldsDeveloperSlot() {
			t.Fatalf("wait %d: the run holds a developer slot while it waits for GitHub", index+1)
		}
		// A wait before the promotion records none; a wait after it keeps the
		// promotion on the record, because it cannot be made again.
		if (state.Integration != nil) != promoted {
			t.Fatalf("wait %d: the run records promotion %#v, want one recorded = %t", index+1, state.Integration, promoted)
		}
		if !f.leaseFree[index] {
			t.Fatalf("wait %d: the promotion lease was held while the run waited for GitHub", index+1)
		}
		says := wait.Says()
		if !strings.HasPrefix(says, "waiting for GitHub to be reachable again") || !strings.Contains(says, "next try at ") {
			t.Fatalf("wait %d says %q, want it to say it waits for GitHub and when it next tries", index+1, says)
		}
	}
	state, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ForgeWait != nil || state.Status != runstate.StatusSucceeded {
		t.Fatalf("run ended %s with wait %#v, want it succeeded with the wait cleared", state.Status, state.ForgeWait)
	}
	if outcome.ForgeWaited == nil || outcome.ForgeWaited.Tries != failures {
		t.Fatalf("outcome wait = %#v, want the %d tries kept for the notes", outcome.ForgeWaited, failures)
	}
	if !strings.Contains(f.tracker.Notes, "Waited for GitHub to be reachable again") {
		t.Fatalf("the work item does not say GitHub was waited for:\n%s", f.tracker.Notes)
	}
}

// The read of where GitHub's main stands, which two approved changes were ended
// on: GitHub could not be reached, the run waited until it could, and then
// landed from that step.
func TestAnUnreachableGitHubAtTheTargetReadIsWaitedOutAndTheRunLands(t *testing.T) {
	t.Parallel()

	const failures = 20
	var reads *unreachableTargetReads
	fixture := newForgeWaitFixture(t, true, func(forge *orchestratortest.Forge) PullRequests { return forge })
	reads = &unreachableTargetReads{WorktreeManager: fixture.pipeline.Worktrees, failures: failures}
	fixture.pipeline.Worktrees = reads

	outcome, err := fixture.pipeline.Run(context.Background(), "yoyodyne-task")
	fixture.assertWaitedOutAndLanded(t, outcome, err, failures, "reading where origin's main stands before landing", false)
	// The read was asked again after every wait, and once more before the merge.
	if reads.reads != failures+2 {
		t.Fatalf("target reads = %d, want %d", reads.reads, failures+2)
	}
}

// The merge request itself, on a target landed through its pull request and on
// one promoted locally first: GitHub could not be reached, so the run waited
// with the promotion lease given back and asked again in place once it could.
func TestAnUnreachableGitHubAtTheMergeRequestIsWaitedOutAndTheRunLands(t *testing.T) {
	t.Parallel()

	for _, landing := range []struct {
		name      string
		protected bool
	}{
		{name: "through the pull request", protected: true},
		{name: "after a local promotion", protected: false},
	} {
		t.Run(landing.name, func(t *testing.T) {
			t.Parallel()
			const failures = 20
			var merges *unreachableMerges
			fixture := newForgeWaitFixture(t, landing.protected, func(forge *orchestratortest.Forge) PullRequests {
				merges = &unreachableMerges{Forge: forge, failures: failures}
				return merges
			})

			outcome, err := fixture.pipeline.Run(context.Background(), "yoyodyne-task")
			fixture.assertWaitedOutAndLanded(t, outcome, err, failures, "asking GitHub to merge pull request #1 into main", true)
			if merges.asked != failures+1 {
				t.Fatalf("merge requests = %d, want %d", merges.asked, failures+1)
			}
		})
	}
}

// After GitHub accepted the merge, the question whether the pull request merged
// and the read that the target carries it: GitHub could not be reached, so the
// run waited and asked that question again, never the merge.
func TestAnUnreachableGitHubAfterTheMergeIsWaitedOutAndTheRunLands(t *testing.T) {
	t.Parallel()

	t.Run("asking whether the pull request merged", func(t *testing.T) {
		t.Parallel()
		const failures = 20
		var states *unreachableMergeStates
		fixture := newForgeWaitFixture(t, true, func(forge *orchestratortest.Forge) PullRequests {
			states = &unreachableMergeStates{Forge: forge, failures: failures}
			return states
		})
		outcome, err := fixture.pipeline.Run(context.Background(), "yoyodyne-task")
		fixture.assertWaitedOutAndLanded(t, outcome, err, failures, "asking GitHub whether pull request #1 merged", true)
		if states.asked <= failures {
			t.Fatalf("questions asked = %d, want more than the %d that failed", states.asked, failures)
		}
	})
	t.Run("reading that the target carries it", func(t *testing.T) {
		t.Parallel()
		const failures = 20
		fixture := newForgeWaitFixture(t, true, func(forge *orchestratortest.Forge) PullRequests { return forge })
		confirmations := &unreachableConfirmations{WorktreeManager: fixture.pipeline.Worktrees, failures: failures}
		fixture.pipeline.Worktrees = confirmations
		outcome, err := fixture.pipeline.Run(context.Background(), "yoyodyne-task")
		fixture.assertWaitedOutAndLanded(t, outcome, err, failures, "checking that origin's main carries pull request #1", true)
		if confirmations.reads != failures+1 {
			t.Fatalf("confirmation reads = %d, want %d", confirmations.reads, failures+1)
		}
	})
}

// A refusal from GitHub about the change itself is an answer, not GitHub being
// away: it is handled as it always was and never waited out.
func TestARefusalAboutTheChangeIsNotWaitedOut(t *testing.T) {
	t.Parallel()

	fixture := newForgeWaitFixture(t, true, func(forge *orchestratortest.Forge) PullRequests {
		forge.MergeErr = errors.New("GraphQL: Pull request is not mergeable: the merge commit cannot be cleanly created (mergePullRequest)")
		return forge
	})

	outcome, err := fixture.pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatal("Run() error = nil, want the refused merge to stop the run as it always has")
	}
	if len(fixture.clock.slept) != 0 || len(fixture.during) != 0 || outcome.ForgeWaited != nil {
		t.Fatalf("waits = %v, records seen waiting = %d, outcome wait = %#v; want a refusal never waited out",
			fixture.clock.slept, len(fixture.during), outcome.ForgeWaited)
	}
	if !strings.Contains(outcome.PublishFailure, "not mergeable") || !outcome.Blocked {
		t.Fatalf("publish failure = %q, blocked = %t; want the refusal handed on as before", outcome.PublishFailure, outcome.Blocked)
	}
	state, err := fixture.store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ForgeWait != nil {
		t.Fatalf("run recorded a wait for GitHub over a refusal: %#v", state.ForgeWait)
	}
}

// unreachableQueueReads cannot reach GitHub for the first reads of how the
// target is protected, as merge queue admission asks them.
type unreachableQueueReads struct {
	switchableForgeQueue
	failures int
	reads    int
	// clock stamps the answer, as GitHub's answer is stamped by the clock the
	// harness reads it with.
	clock *pausingClock
}

func (f *unreachableQueueReads) QueueCapabilities(ctx context.Context, branch string) (publish.QueueCapabilities, error) {
	f.reads++
	if f.reads <= f.failures {
		return publish.QueueCapabilities{}, gitHubUnreachable("gh api repos/o/r/rules/branches/" + branch)
	}
	observed, err := f.switchableForgeQueue.QueueCapabilities(ctx, branch)
	observed.ObservedAt = f.clock.Now()
	return observed, err
}

// With the merge queue switched on, admission reads how the target is
// protected from GitHub. A GitHub that cannot be reached for that read is
// waited for, past the two-hour window, and the change is then admitted:
// nothing is held, blocked, or reported, and the run holds no slot meanwhile.
func TestAnUnreachableGitHubAtMergeQueueAdmissionIsWaitedOutAndTheChangeAdmitted(t *testing.T) {
	t.Parallel()

	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	reports := &fakeReports{}
	r := newHeldQueueRun(t, queue, reports)
	r.required = false
	const failures = 20
	reads := &unreachableQueueReads{switchableForgeQueue: switchableForgeQueue{Forge: r.forge, required: &r.required, unread: &r.unread}, failures: failures}
	r.pipeline.Publisher = reads
	clock := &pausingClock{now: time.Now()}
	reads.clock = clock
	var during []runstate.State
	clock.onSleep = func() {
		incomplete, err := r.runs.Incomplete()
		if err != nil {
			t.Errorf("Incomplete() error = %v", err)
			return
		}
		for _, state := range incomplete {
			if state.ForgeWait != nil {
				during = append(during, state)
			}
		}
	}
	r.pipeline = waiting(r.pipeline, clock, 6*time.Hour, 6*time.Hour)

	outcome, err := r.pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v, want GitHub waited out and the change admitted", err)
	}
	if outcome.MergeQueue == nil || r.tracker.Blocked || len(reports.appended) != 0 || outcome.IntegrationStop != nil {
		t.Fatalf("queue entry = %#v, blocked = %t, reports = %#v, stop = %#v; want the change admitted with nothing held or reported",
			outcome.MergeQueue, r.tracker.Blocked, reports.appended, outcome.IntegrationStop)
	}
	if reads.reads != failures+1 {
		t.Fatalf("protection reads = %d, want %d", reads.reads, failures+1)
	}
	if got := clock.waited(); got <= recovery.Window {
		t.Fatalf("waited %s, want the wait to outlast the %s window", got, recovery.Window)
	}
	if len(during) != failures {
		t.Fatalf("records seen waiting = %d, want %d", len(during), failures)
	}
	for index, state := range during {
		if state.HoldsDeveloperSlot() || !strings.Contains(state.ForgeWait.Step, "admit the change to the merge queue") {
			t.Fatalf("wait %d: %#v, holds slot %t; want a wait at admission holding no slot", index+1, state.ForgeWait, state.HoldsDeveloperSlot())
		}
	}
	if entries, err := queue.Entries(mainQueueKey); err != nil || len(entries) != 1 {
		t.Fatalf("entries = %#v, %v; want the change admitted", entries, err)
	}
	if _, found, err := queue.Refusal(mainQueueKey); err != nil || found {
		t.Fatalf("Refusal() found = %t, %v; want no condition recorded", found, err)
	}
}

package twofactory

// Checks 03, 04 and 05: how a factory's runs treat the target branch the other
// factory moves, and how many pull requests a person may be left holding. Each
// drives the harness's own pipeline, forge check and scheduler on factory A's
// home and clone, with the shared remote standing in for what factory B (or
// any other contributor) pushes to.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// runID is the identifier a pipeline gives its nth run: the harness's run
// identifiers are "run-" and thirty-two hexadecimal digits.
func runID(n int) string { return fmt.Sprintf("run-%032x", n) }

// prepareClone gives a factory's clone what the pipeline needs of a checkout:
// an identity to commit as and no background maintenance racing the run's Git.
func prepareClone(t *testing.T, f *factoryHome) {
	t.Helper()
	git(t, f.clone, "config", "user.name", "Yoyodyne Validation")
	git(t, f.clone, "config", "user.email", "yoyodyne@example.invalid")
	git(t, f.clone, "config", "maintenance.auto", "false")
	git(t, f.clone, "config", "gc.auto", "0")
	t.Cleanup(func() { testharness.RemoveLinkedPipelineWorktrees(t, f.clone) })
}

// pushFromB is another factory landing work on the shared remote's main: B
// catches up with the remote, commits the file with the content given, and
// pushes. It returns the commit now at the remote's main.
func pushFromB(t *testing.T, w *world, file, content string) string {
	t.Helper()
	git(t, w.b.clone, "fetch", "--quiet", "origin")
	git(t, w.b.clone, "reset", "--quiet", "--hard", "origin/main")
	writeFile(t, filepath.Join(w.b.clone, file), content)
	git(t, w.b.clone, "add", file)
	git(t, w.b.clone, "commit", "--quiet", "-m", "factory B changes "+file)
	git(t, w.b.clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return git(t, w.b.clone, "rev-parse", "HEAD")
}

// remoteMain is where the shared remote's main stands.
func remoteMain(t *testing.T, w *world) string {
	t.Helper()
	return git(t, w.remote, "rev-parse", "refs/heads/main")
}

// factoryAStore is the run store of factory A's home.
func factoryAStore(t *testing.T, w *world) *runstate.Store {
	t.Helper()
	store, err := runstate.NewStore(w.a.stateRoot(), product)
	if err != nil {
		t.Fatalf("open factory A's run store: %v", err)
	}
	return store
}

// factoryAPipeline is the harness's pipeline over factory A's clone, worktree
// root and run store, reading its directives and holds from A's home. The
// developer writes the file named by write, and the reviewer approves.
func factoryAPipeline(t *testing.T, w *world, store *runstate.Store, tracker orchestrator.WorkTracker, write func() string) (orchestrator.Pipeline, *orchestratortest.Backend) {
	t.Helper()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, write()), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, w.a.clone, w.a.worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	// The runs belong to the product both factories work, not to the harness's
	// own test product.
	pipeline.Config.Product.ID = domain.ProductID(product)
	pipeline.Config.Product.RepositoryID = product
	directives, err := runstate.NewDirectiveStore(w.a.stateRoot(), product)
	if err != nil {
		t.Fatalf("open factory A's directive store: %v", err)
	}
	holds, err := runstate.NewOperatorHoldStore(w.a.stateRoot())
	if err != nil {
		t.Fatalf("open factory A's operator hold store: %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(w.a.stateRoot(), product)
	if err != nil {
		t.Fatalf("open factory A's intake hold store: %v", err)
	}
	pipeline.Directives, pipeline.Holds, pipeline.Intake = directives, holds, intake
	return pipeline, provider
}

// Check 03. Another factory's merge lands on the shared remote while factory A
// is idle: A's next run is cut from the target it fetched just before, and its
// record says so. With the remote then unreachable a run still starts, from
// the target the last fetch left, and its record says the fetch failed and how
// old that target is.
func checkStartingTarget(t *testing.T, w *world) {
	prepareClone(t, w.a)
	store := factoryAStore(t, w)
	item := func(id string) *orchestratortest.Tracker {
		return &orchestratortest.Tracker{Item: beads.WorkItem{ID: id, Title: "Work " + id, Status: "open"}}
	}
	first := item("shared-first")
	file := "first.txt"
	pipeline, _ := factoryAPipeline(t, w, store, first, func() string { return file })
	pipeline.NewRunID = func() (string, error) { return runID(1), nil }

	// Factory B's merge reaches the remote; A's clone has not fetched it.
	before := git(t, w.a.clone, "rev-parse", "refs/heads/main")
	theirs := pushFromB(t, w, "from-b.txt", "merged by factory B\n")
	if local := git(t, w.a.clone, "rev-parse", "refs/heads/main"); local != before || before == theirs {
		t.Fatalf("setup: factory A's main is %s and the remote's is %s; expected A not to have fetched B's commit yet", local, theirs)
	}

	outcome, err := pipeline.Run(context.Background(), first.Item.ID)
	if err != nil {
		t.Fatalf("factory A's first run failed: %v", err)
	}
	run, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("load factory A's first run: %v", err)
	}
	started := run.StartingTarget
	if started == nil {
		t.Fatalf("expected the first run to record the target it started from; its record has none")
	}
	if !started.Fetched || started.Failure != "" {
		t.Errorf("expected the first run to record main as fetched from the remote; it recorded fetched=%t, failure=%q", started.Fetched, started.Failure)
	}
	if started.Commit != theirs || started.RemoteCommit != theirs {
		t.Errorf("expected the first run to start from the remote's new main %s (the commit factory B pushed); it recorded commit %s, remote commit %s", theirs, started.Commit, started.RemoteCommit)
	}
	if outcome.BaseCommit != theirs {
		t.Errorf("expected the first run's worktree to be cut from %s; it was cut from %s", theirs, outcome.BaseCommit)
	}
	if !strings.Contains(first.Notes, "started from main at "+theirs+", fetched from origin for this run") {
		t.Errorf("expected the item's notes to say the run started from main at %s fetched from origin; the notes say:\n%s", theirs, first.Notes)
	}

	// The fetch the first run made, made again three hours earlier by the
	// clock the record keeps, so the age a later failed fetch reports is one
	// this check can predict.
	fetchedAt := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	fetcher, ok := pipeline.Worktrees.(orchestrator.TargetFetcher)
	if !ok {
		t.Fatalf("expected the pipeline's worktrees to fetch targets; they do not")
	}
	if fetch := fetcher.FetchTarget(context.Background(), "main", fetchedAt); !fetch.Fetched {
		t.Fatalf("setup: the backdated fetch of main from the reachable remote failed: %s", fetch.Failure)
	}

	// B pushes again and the remote then becomes unreachable for A.
	pushFromB(t, w, "later-from-b.txt", "never seen by factory A\n")
	lastFetched := git(t, w.a.clone, "rev-parse", "refs/heads/main")
	git(t, w.a.clone, "remote", "set-url", "origin", filepath.Join(w.root, "missing.git"))

	second := item("shared-second")
	file = "second.txt"
	pipeline.Tracker = second
	pipeline.NewRunID = func() (string, error) { return runID(2), nil }
	outcome, err = pipeline.Run(context.Background(), second.Item.ID)
	if err != nil {
		t.Fatalf("expected factory A's second run to start although the remote is unreachable; it failed: %v", err)
	}
	run, err = store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("load factory A's second run: %v", err)
	}
	started = run.StartingTarget
	if started == nil {
		t.Fatalf("expected the second run to record the target it started from; its record has none")
	}
	if started.Fetched || started.Failure == "" {
		t.Errorf("expected the second run to record that fetching main failed; it recorded fetched=%t, failure=%q", started.Fetched, started.Failure)
	}
	if started.Commit != lastFetched || outcome.BaseCommit != lastFetched {
		t.Errorf("expected the second run to start from the target the last fetch left, %s; it recorded commit %s and was cut from %s", lastFetched, started.Commit, outcome.BaseCommit)
	}
	if !started.LastFetchedAt.Equal(fetchedAt) {
		t.Errorf("expected the record to say main was last fetched at %s; it says %s", fetchedAt, started.LastFetchedAt)
	}
	if age, known := started.Age(); !known || age < 3*time.Hour || age > 4*time.Hour {
		t.Errorf("expected the record to say the target was about three hours old; it says age %s (known %t)", age, known)
	}
	if !strings.Contains(second.Notes, "the target the last fetch left, because fetching it from origin failed") ||
		!strings.Contains(second.Notes, "it was last fetched 3 hours") {
		t.Errorf("expected the item's notes to say the fetch failed and the target was last fetched 3 hours before; the notes say:\n%s", second.Notes)
	}
}

// updateFixture is a run of factory A that ended with its approved change
// published and waiting for a person to merge, under a merge setting that
// leaves the merge to a person, and the forge check that brings it up to date.
type updateFixture struct {
	w        *world
	store    *runstate.Store
	tracker  *orchestratortest.Tracker
	provider *orchestratortest.Backend
	forge    *orchestratortest.CheckedForge
	pipeline orchestrator.Pipeline
	run      runstate.State
}

func newUpdateFixture(t *testing.T, w *world) updateFixture {
	t.Helper()
	prepareClone(t, w.a)
	store := factoryAStore(t, w)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "shared-task", Title: "Task", Status: "open"}}
	pipeline, provider := factoryAPipeline(t, w, store, tracker, func() string { return "feature.txt" })
	forge := &orchestratortest.CheckedForge{Forge: &orchestratortest.Forge{Remote: w.remote}}
	pipeline = testharness.Publishing(pipeline, forge)
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	pipeline.NewRunID = func() (string, error) { return runID(1), nil }

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("factory A's run failed: %v", err)
	}
	run, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("load factory A's run: %v", err)
	}
	if !run.WaitingForPersonMerge() {
		t.Fatalf("expected factory A's run to end waiting for a person to merge its pull request; it ended %s/%s", run.Status, run.Phase)
	}
	// From here the forge says the request's head is one commit behind its
	// target; the target itself is moved by factory B in the check.
	forge.Reading = publish.CheckReading{HeadCommit: run.PullRequest.HeadCommit, BehindBy: 1}
	return updateFixture{w: w, store: store, tracker: tracker, provider: provider, forge: forge, pipeline: pipeline, run: run}
}

// check is one forge check of factory A's waiting request, with the run it
// puts back at its update hosted through the same pipeline.
func (f updateFixture) check(t *testing.T) (orchestrator.PersonMergeSettlement, []orchestrator.UpdateContinuation) {
	t.Helper()
	reconciler := orchestrator.Reconciler{
		Tracker:   f.tracker,
		Worktrees: testharness.NewObserver(t, f.w.a.clone, f.w.a.worktreeRoot),
		Store:     f.store,
		Publisher: f.forge,
		Checks:    f.forge,
		Capacity:  1,
		HostsRuns: true,
		Continue:  f.pipeline.Continue,
	}
	settled, err := reconciler.SettlePersonMerges(context.Background())
	if err != nil {
		t.Fatalf("the forge check failed: %v", err)
	}
	if len(settled) != 1 {
		t.Fatalf("expected the forge check to read the one waiting pull request; it read %d", len(settled))
	}
	updates, err := reconciler.ContinueUpdates(context.Background())
	if err != nil {
		t.Fatalf("hosting the update failed: %v", err)
	}
	return settled[0], updates
}

func (f updateFixture) load(t *testing.T) runstate.State {
	t.Helper()
	run, err := f.store.Load(f.run.RunID)
	if err != nil {
		t.Fatalf("load factory A's run: %v", err)
	}
	return run
}

// lastDeveloperPrompt is what the developer was last asked.
func lastDeveloperPrompt(provider *orchestratortest.Backend) string {
	var prompt string
	for _, request := range provider.RequestsMade() {
		if request.Role == domain.RoleDeveloper {
			prompt = request.Prompt
		}
	}
	return prompt
}

// Check 04. Factory B lands a change on the shared main that conflicts with the
// change factory A's pull request waits with. Each conflict while the request
// is brought up to date is a round for the developer naming the conflicting
// paths, counted against the request's own limit of three and never against
// the item's repair attempts. The conflict after the third round sends the run
// to the development manager with the change kept.
func checkUpdateConflict(t *testing.T, w *world) {
	f := newUpdateFixture(t, w)
	repairs := f.run.RepairAttempts

	for round := 1; round <= runstate.MaxUpToDateConflicts; round++ {
		pushFromB(t, w, "feature.txt", fmt.Sprintf("factory B's version %d\n", round))
		developers := f.provider.DeveloperAttempts
		settled, updates := f.check(t)
		if !settled.Updating || len(updates) != 1 || updates[0].Failure != "" {
			t.Fatalf("round %d: expected the run to be put back to bring its pull request up to date and hosted; the check says %#v and the updates %#v", round, settled, updates)
		}
		after := f.load(t)
		if after.UpToDateConflicts != round {
			t.Errorf("round %d: expected %d conflict rounds counted on the request's own limit; the run counts %d", round, round, after.UpToDateConflicts)
		}
		if after.RepairAttempts != repairs {
			t.Errorf("round %d: expected the item's repair attempts to stay at %d; they are %d", round, repairs, after.RepairAttempts)
		}
		if got := f.provider.DeveloperAttempts - developers; got != 1 {
			t.Errorf("round %d: expected the developer to be asked once to settle the conflict; it was asked %d times", round, got)
		}
		prompt := lastDeveloperPrompt(f.provider)
		if !strings.Contains(prompt, "- feature.txt") {
			t.Errorf("round %d: expected the developer to be told the conflicting path feature.txt; it was told:\n%s", round, prompt)
		}
		if want := fmt.Sprintf("conflict round %d of %d", round, runstate.MaxUpToDateConflicts); !strings.Contains(prompt, want) {
			t.Errorf("round %d: expected the developer to be told %q; it was told:\n%s", round, want, prompt)
		}
		if !after.WaitingForPersonMerge() {
			t.Fatalf("round %d: expected the settled change to be pushed and the run to wait for a person again; it is %s/%s", round, after.Status, after.Phase)
		}
	}

	pushFromB(t, w, "feature.txt", "factory B's version 4\n")
	developers := f.provider.DeveloperAttempts
	published := testharness.PublishedCommit(t, w.remote, f.run.Branch)
	settled, updates := f.check(t)
	if !settled.Updating || len(updates) != 1 {
		t.Fatalf("after the third round: expected the run to be put back to bring its pull request up to date and hosted; the check says %#v and the updates %#v", settled, updates)
	}
	after := f.load(t)
	if f.provider.DeveloperAttempts != developers {
		t.Errorf("expected the developer not to be asked again once the %d rounds were spent; it was asked %d more times", runstate.MaxUpToDateConflicts, f.provider.DeveloperAttempts-developers)
	}
	if after.Status.InFlight() || after.WaitingForPersonMerge() || after.StopClass != runstate.StopIntegrationBudget {
		t.Errorf("expected the run to stop for the development manager (stop class %q); it is %s with stop class %q", runstate.StopIntegrationBudget, after.Status, after.StopClass)
	}
	if !f.tracker.Blocked || !strings.Contains(f.tracker.Notes, "conflict rounds") || !strings.Contains(f.tracker.Notes, "development manager") {
		t.Errorf("expected the item to be blocked with notes naming the conflict rounds and the development manager; blocked=%t, notes:\n%s", f.tracker.Blocked, f.tracker.Notes)
	}
	if after.ReplayConflict == nil || len(after.ReplayConflict.Paths) != 1 || after.ReplayConflict.Paths[0] != "feature.txt" {
		t.Errorf("expected the conflict to be kept on the run naming feature.txt; it is %#v", after.ReplayConflict)
	}
	if after.RepairAttempts != repairs {
		t.Errorf("expected the item's repair attempts to stay at %d through the whole conflict; they are %d", repairs, after.RepairAttempts)
	}
	if after.WorktreeRemoved || after.BranchRemoved {
		t.Errorf("expected the change to be kept; worktree removed=%t, branch removed=%t", after.WorktreeRemoved, after.BranchRemoved)
	}
	if now := testharness.PublishedCommit(t, w.remote, f.run.Branch); now != published {
		t.Errorf("expected the pull request's head to stay at %s; it moved to %s", published, now)
	}
}

// startRecorder is the scheduler's way of starting a run: it records the work
// items it was asked to start and starts nothing.
type startRecorder struct {
	mu      sync.Mutex
	started []string
}

func (s *startRecorder) start(_ context.Context, workItemID string, _ runstate.Selection) (orchestrator.Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = append(s.started, workItemID)
	return orchestrator.Outcome{WorkItemID: workItemID, Status: runstate.StatusSucceeded}, nil
}

func (s *startRecorder) items() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.started...)
}

// readyEnvironment is a checkout nothing is wrong with.
type readyEnvironment struct{}

func (readyEnvironment) ValidateReady(context.Context) error { return nil }

// Check 05. Factory A has two approved changes waiting for a person to merge,
// and execution.max_waiting_pull_requests is two. The scheduler then starts no
// new developer work and the status line says why, naming each pull request by
// number and work item; with the limit above the count, work starts again.
func checkWaitingLimit(t *testing.T, w *world) {
	prepareClone(t, w.a)
	store := factoryAStore(t, w)
	for number, id := range []string{"shared-one", "shared-two"} {
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: id, Title: "Change " + id, Status: "open"}}
		pipeline, _ := factoryAPipeline(t, w, store, tracker, func() string { return id + ".txt" })
		pipeline = testharness.Publishing(pipeline, &orchestratortest.Forge{Remote: w.remote, Number: number + 1})
		pipeline.Config.Approvals.Integration = domain.ApprovalHuman
		pipeline.NewRunID = func() (string, error) { return runID(number + 1), nil }
		if _, err := pipeline.Run(context.Background(), id); err != nil {
			t.Fatalf("factory A's run for %s failed: %v", id, err)
		}
	}
	waiting, err := store.WaitingForPersonMerge()
	if err != nil || len(waiting) != 2 {
		t.Fatalf("setup: expected two runs waiting for a person to merge; found %d (error %v)", len(waiting), err)
	}

	intake, err := runstate.NewIntakeHoldStore(w.a.stateRoot(), product)
	if err != nil {
		t.Fatalf("open factory A's intake hold store: %v", err)
	}
	directives, err := runstate.NewDirectiveStore(w.a.stateRoot(), product)
	if err != nil {
		t.Fatalf("open factory A's directive store: %v", err)
	}
	queue := &orchestratortest.ScheduleTracker{
		Items:      []beads.WorkItem{{ID: "shared-three", Title: "Third change", Status: "open", Priority: 2}},
		ReadyItems: map[string]bool{"shared-three": true},
	}
	schedule := func(limit int) (orchestrator.Schedule, []string) {
		starts := &startRecorder{}
		scheduler := orchestrator.Scheduler{
			Open: func(context.Context) (orchestrator.Pull, error) {
				return orchestrator.Pull{
					Tracker: queue, Runs: store, Gates: store, Intake: intake, Directives: directives,
					Environment: readyEnvironment{}, Capacity: 1, Start: starts.start,
					MaxWaitingPullRequests: limit, PersonMerges: store,
				}, nil
			},
			Sleep: func(context.Context, time.Duration) bool { return false },
		}
		result, err := scheduler.Schedule(context.Background())
		if err != nil {
			t.Fatalf("the scheduler failed at a limit of %d: %v", limit, err)
		}
		return result, starts.items()
	}
	status := func(limit int) readmodel.Standing {
		return readmodel.ReadStanding(context.Background(), readmodel.Sources{
			Runs: store, Capacity: 1, MaxWaitingPullRequests: limit, Now: time.Now,
		})
	}

	held, started := schedule(2)
	if len(started) != 0 || len(held.Started) != 0 {
		t.Errorf("expected the scheduler to start no new work with 2 pull requests waiting at a limit of 2; it started %v", started)
	}
	if held.Stopped != orchestrator.ScheduleWaitingPullRequests || held.WaitingPullRequests == nil {
		t.Errorf("expected the scheduler to stop on the waiting pull request limit; it stopped %q with waiting %#v", held.Stopped, held.WaitingPullRequests)
	}
	line := status(2)
	if line.WaitingPullRequests == nil {
		t.Fatalf("expected the status line to read the waiting pull requests at the limit; it read none")
	}
	said := line.RenderBanner()
	for _, want := range []string{
		"Not starting new work because 2 pull requests are waiting for a person to merge",
		"#1 for Change shared-one (shared-one)",
		"#2 for Change shared-two (shared-two)",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("expected the status line to say %q; it says:\n%s", want, said)
		}
	}

	// A limit of three is not reached by two waiting requests, so the third
	// item is started and the status line says nothing about waiting.
	below, started := schedule(3)
	if len(started) != 1 || started[0] != "shared-three" || below.WaitingPullRequests != nil {
		t.Errorf("expected the scheduler to start shared-three below the limit; it started %v (waiting %#v)", started, below.WaitingPullRequests)
	}
	if said := status(3).RenderBanner(); strings.Contains(said, "waiting for a person to merge") {
		t.Errorf("expected the status line to say nothing about waiting pull requests below the limit; it says:\n%s", said)
	}
}

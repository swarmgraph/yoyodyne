package testharness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	PipelineRunID = "run-0123456789abcdef0123456789abcdef"
	// The ordinary approval: the reviewer approves the change as the work the item
	// asked for, which is what closes the item.
	ApproveVerdict = `{"decision":"approve","approves":"implementation","summary":"the change matches the acceptance criteria"}`
	// The other approval. The change is promoted exactly as the one above is and
	// discharges nothing, because what it landed is not the work the item asked
	// for.
	ApproveEvidenceVerdict = `{"decision":"approve","approves":"evidence","summary":"this is a sound diagnosis rather than the conversion the item asked for; the design it needs has not landed"}`
	RepairVerdict          = `{"decision":"repair","summary":"the change misses the acceptance criteria","findings":[{"severity":"blocker","message":"add the missing file","location":{"file":"feature.txt","line":1}}]}`
	// A repair whose whole residue is one finding the reviewer disposed of as out
	// of scope: the work goes back to the developer, and the item is charged no
	// round for it.
	OutOfScopeVerdict = `{"decision":"repair","summary":"the change is right; one note beside it","findings":[{"severity":"minor","disposition":"out_of_scope","message":"rename this variable","location":{"file":"feature.txt","line":1}}]}`
	// The same residue without the disposition. Minor is a severity, and a
	// severity does not decide the budget, so this is charged like any repair.
	MinorVerdict = `{"decision":"repair","summary":"the change is right; one small note","findings":[{"severity":"minor","message":"rename this variable","location":{"file":"feature.txt","line":1}}]}`
	// Every configured agent declares a selector, and the run records both it
	// and the model the provider reported serving.
	TestDeveloperModel = "opus"
	TestReviewerModel  = "opus"
	DeveloperResolved  = orchestratortest.DeveloperResolved
	ReviewerResolved   = orchestratortest.ReviewerResolved
	// ScratchForTest stands in for the per-run scratch directory the harness cuts
	// and names in the contract, where a test builds a prompt directly rather than
	// running a pipeline that would cut a real one.
	ScratchForTest = "/scratch/run-0123456789abcdef0123456789abcdef"
)

func NewPipeline(t *testing.T, repository string, tracker orchestrator.WorkTracker, provider backend.Backend, commands []string) (orchestrator.Pipeline, *runstate.Store) {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	return NewSharedPipeline(t, repository, filepath.Join(t.TempDir(), "worktrees"), store, tracker, provider, commands), store
}

// TestGitBudget is the budget every worktree manager this package's tests build
// gives one local Git command, in place of the manager's load-scaled default.
//
// The default is sized for a run, and under a full suite it has been reached by
// Git that was working: a replay in
// TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn killed at
// the idle thirty seconds with the one-minute load at 11 to 13 on sixteen
// cores, which the scaling reads as an idle machine; a one-file checkout in
// TestPipelineMergesIntoATargetThatRefusesDirectPushes killed at 31 seconds with
// the load near 50; `git status` in
// TestPipelineBlocksWhenTheIntegrationRetryBudgetIsSpent and the replay in
// TestPipelineBlocksOnAReplayConflictWithoutResolvingIt killed the same way. Each
// passed in seconds on its own. A lagging one-minute average is not what a
// suite's own race binaries do to a Git command started beside them, so no
// scaling of it is a figure these tests can rely on.
//
// These tests are about what a run does with what Git answered, never about how
// long Git took to answer, so the only thing their budget has to catch is a Git
// command that has hung — and any figure catches that. Ten minutes sits under
// the Makefile's TEST_TIMEOUT, so a hung command is still ended by the runner
// and reported as the command it was rather than by the binary's own timeout.
// The production budget is held to its own behaviour in internal/gitworktree,
// which is where it is the thing under test.
const TestGitBudget = 10 * time.Minute

// NewSharedPipeline builds a pipeline over an explicit worktree root and run
// state store, so two pipelines can be built over the same durable artifacts:
// that is what a restarted or a concurrent process sees.
func NewSharedPipeline(t *testing.T, repository, worktreeRoot string, store orchestrator.StateStore, tracker orchestrator.WorkTracker, provider backend.Backend, commands []string) orchestrator.Pipeline {
	t.Helper()
	processRunner := execution.OSProcessRunner{}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                processRunner,
		RepositoryRoot:        repository,
		WorktreeRoot:          worktreeRoot,
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
		// What `yoyo run` configures, so a pipeline built here refreshes and
		// refuses exactly what a real one does. A repository that carries no such
		// file is unaffected: there is nothing to copy across and nothing to hold.
		CurrentExports: []string{".beads/issues.jsonl"},
		Timeout:        TestGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	cfg := config.Config{
		Version: config.CurrentVersion,
		Product: config.Product{
			ID: "yoyodyne", RepositoryID: "yoyodyne", Repository: repository,
			Specifications: config.DefaultSpecifications,
			Invariants:     config.DefaultInvariants,
			Designs:        config.DefaultDesigns,
			Decisions:      config.DefaultDecisions,
		},
		Execution: config.Execution{
			MaxConcurrentDevelopers:                1,
			RepairAttemptsBeforeReplan:             2,
			IntegrationRetriesBeforeReconciliation: 2,
			WorktreeRoot:                           "auto",
			Remote:                                 "origin",
			UsageLimitUnknownResetPause:            config.Duration(30 * time.Minute),
			ServerOverloadPause:                    config.Duration(90 * time.Second),
			CheckTimeout:                           config.Duration(30 * time.Minute),
			CheckStageTimeout:                      config.Duration(30 * time.Minute),
			LandingCheckTimeout:                    config.Duration(2 * time.Hour),
			WorkPoll:                               config.Duration(60 * time.Second),
			RedeployDrainLimit:                     config.Duration(15 * time.Minute),
			BlockedRunsBeforeIntakeHold:            3,
			// What a loaded configuration fills in, and what every run this suite
			// drives therefore executes: the built-in definition, compiled and
			// stepped beside the run. A test about the legacy path turns it off
			// itself, which is what a project rolling back writes.
			DeclarativeDelivery: true,
		},
		// The triage thresholds a loaded configuration would have filled in.
		// Nothing here drives them; they are stated because a hand-built
		// configuration is validated exactly like a loaded one.
		Triage: config.Triage{
			StuckMergeAge:       config.Duration(2 * time.Hour),
			ReviewRoundsCap:     4,
			RepairGrantAttempts: 2,
		},
		Exchange: config.Exchange{MaxRounds: 10},
		// And the conversation threshold, for the same reason: no run here holds
		// a conversation, and a hand-built configuration is still validated.
		Conversation: config.Conversation{RefreshAfterLandings: config.DefaultRefreshAfterLandings},
		Approvals: config.Approvals{
			Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, Designs: domain.ApprovalAutomatic,
			WorkItems: domain.ApprovalHuman, Integration: domain.ApprovalHuman, Publishing: domain.ApprovalHuman,
		},
		// The parts of the product a loaded configuration would have filled in.
		// Nothing here starts one; they are stated for the reason the triage
		// thresholds are.
		Services: config.DefaultServices(),
		Checks:   commands,
		Agents: map[string]config.AgentConfig{
			"developer": {Role: domain.RoleDeveloper, Backend: domain.BackendClaudeCode, Model: TestDeveloperModel, Instances: 1},
		},
	}
	pipeline := orchestrator.Pipeline{
		Tracker: tracker, Worktrees: worktrees, Store: store, Backend: provider,
		Checks: checks.Runner{Process: processRunner}, NewRunID: func() (string, error) { return PipelineRunID, nil },
		// Every pipeline reads what the operator has directed before it commits to
		// work, so every test pipeline has somewhere to read it from. A store
		// nobody recorded anything in is a product nobody has directed, which is
		// what all but the directive tests are about.
		Directives: NewDirectiveStore(t),
		// And every pipeline reads whether the operator has paused everything
		// before it spends, so every test pipeline has somewhere to read that from
		// too. A store nobody paused is a harness that is running, which is what all
		// but the hold tests are about.
		Holds: NewOperatorHoldStore(t),
		// And every pipeline reads whether the operator has held what the harness
		// chooses for itself before it starts anything new. A store nobody held is a
		// harness that may choose work, which is what all but the intake tests are
		// about.
		Intake:     NewIntakeHoldStore(t),
		Repository: repository, Config: cfg,
	}
	// And every pipeline records the instance it is executing the definition
	// through, because `declarative_delivery` is the default above and a fixture
	// with nowhere to record one would be running the legacy path while its
	// configuration said otherwise. A test driving an interrupted process hands
	// in a wrapper around its store and wires the store underneath it itself,
	// which is what a dead process leaves: the instance on disk is whatever it
	// had already written.
	if instances, ok := store.(*runstate.Store); ok {
		pipeline.Instances = instances
	}
	return pipeline
}

// NewOperatorHoldStore builds the durable record of the operator's hold. A test
// that drives a pause keeps its own reference and hands the same store to the
// pipeline, because a hold recorded anywhere else is one no run sees.
func NewOperatorHoldStore(t *testing.T) *runstate.OperatorHoldStore {
	t.Helper()
	store, err := runstate.NewOperatorHoldStore(t.TempDir())
	if err != nil {
		t.Fatalf("runstate.NewOperatorHoldStore() error = %v", err)
	}
	return store
}

// NewIntakeHoldStore builds the durable record of the operator's hold on what
// the harness starts by itself. A test that drives one keeps its own reference
// and hands the same store to the pipeline, because a hold recorded anywhere
// else is one no run sees.
func NewIntakeHoldStore(t *testing.T) *runstate.IntakeHoldStore {
	t.Helper()
	store, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	return store
}

// NewDirectiveStore builds the durable directive record a pipeline reads. A test
// that drives a directive keeps its own reference and hands the same store to
// the pipeline, because a directive recorded anywhere else is one no run sees.
func NewDirectiveStore(t *testing.T) *runstate.DirectiveStore {
	t.Helper()
	store, err := runstate.NewDirectiveStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDirectiveStore() error = %v", err)
	}
	return store
}

func NewAutomaticPipeline(t *testing.T, repository string, tracker orchestrator.WorkTracker, provider backend.Backend, commands []string) (orchestrator.Pipeline, *runstate.Store) {
	t.Helper()
	pipeline, store := NewPipeline(t, repository, tracker, provider, commands)
	return Automatic(pipeline, provider), store
}

// Automatic turns a pipeline into one that reviews and integrates on its own.
func Automatic(pipeline orchestrator.Pipeline, provider backend.Backend) orchestrator.Pipeline {
	pipeline.Config.Approvals.Integration = domain.ApprovalAutomatic
	pipeline.Config.Agents["reviewer"] = config.AgentConfig{Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: TestReviewerModel, Instances: 1}
	pipeline.Reviewer = review.Reviewer{Backend: provider, Model: TestReviewerModel}
	return pipeline
}

// InterruptedStore stops accepting writes once a run reaches the given repair
// attempt, after letting allowSaves of them through. What is left on disk is
// what an interrupted process leaves behind: a non-terminal run recorded at the
// last step it managed to write down. Varying allowSaves is what chooses the
// step the process died on.
//
// reached overrides which step that is, for a test interested in a budget other
// than the repair one. It is the only part of the shape that differs between
// them: what a killed process leaves behind is the same story whichever count it
// was in the middle of.
type InterruptedStore struct {
	orchestrator.StateStore
	AtAttempt  int
	Reached    func(runstate.State) bool
	AllowSaves int
	Saved      int
	Stopped    bool
}

func (s *InterruptedStore) Save(state runstate.State) error {
	if s.ReachedStep(state) {
		if s.Saved >= s.AllowSaves {
			s.Stopped = true
			return errors.New("state store is unavailable")
		}
		s.Saved++
	}
	return s.StateStore.Save(state)
}

func (s *InterruptedStore) ReachedStep(state runstate.State) bool {
	if s.Reached != nil {
		return s.Reached(state)
	}
	return state.RepairAttempts >= s.AtAttempt
}

// RestartableFixture returns a repository, worktree root, and run state store
// that outlive any one pipeline, so a second pipeline over them sees exactly
// what a restarted process would.
func RestartableFixture(t *testing.T) (string, string, *runstate.Store) {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	return PipelineRepository(t), filepath.Join(t.TempDir(), "worktrees"), store
}

func PipelineRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(filepath.Join(repository, "docs"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "design.md"), []byte("design content\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	RunPipelineGit(t, repository, "init", "-b", "main")
	RunPipelineGit(t, repository, "config", "user.name", "Yoyodyne Test")
	RunPipelineGit(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	DisablePipelineMaintenance(t, repository)
	// Registered after the first TempDir call above and therefore run before
	// TempDir's removal, so the repository is idle by the time Go deletes it.
	t.Cleanup(func() { RemoveLinkedPipelineWorktrees(t, repository) })
	RunPipelineGit(t, repository, "add", ".")
	RunPipelineGit(t, repository, "commit", "-m", "initial")
	CreationLoopIfAsked(t, repository)
	return repository
}

// CreationLoopVariable turns the loop below on for every repository a pipeline
// test builds, rather than only for the two tests that always run under it.
const CreationLoopVariable = "YOYODYNE_TEST_CREATION_LOOP"

// CreationLoopIfAsked starts the loop for every pipeline test when
// YOYODYNE_TEST_CREATION_LOOP is set. Most pipeline tests are about something
// else and would only pay for it, so they get it on request:
//
//	YOYODYNE_TEST_CREATION_LOOP=1 go test ./internal/orchestrator \
//	  -run 'TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn|TestTwoRunsPromotingIntoOneTargetBranchSerializeAndBothLand' \
//	  -count=20 -race
func CreationLoopIfAsked(t *testing.T, repository string) {
	t.Helper()
	if os.Getenv(CreationLoopVariable) == "" {
		return
	}
	StartCreationLoop(t, repository)
}

// StartCreationLoop runs `git worktree add` and `git worktree remove` against
// this test's own repository for as long as the test lasts.
//
// It exists for a question a single pass of a test cannot answer. The runs a
// concurrent-runs test hosts write one repository's worktree bookkeeping, and a
// command that walks that bookkeeping while another run is registering used to
// fail outright — a rebase was seen doing it with `failed to read
// .git/worktrees/<other>/commondir`. Whether that still happens is a question
// about a window measured in milliseconds, so the way to ask it is to widen the
// window: register and unregister worktrees continuously underneath the runs
// and see whether anything fails.
//
// The two tests that were seen failing that way call this directly, so the
// condition they have to survive is judged by `make test` and `make race` like
// any other, rather than being something somebody has to remember to turn on.
//
// The loop is Git run directly rather than through the worktree manager, so it
// takes no registry lease — which is the point. A run's own Git is leased and
// therefore cannot meet a half-written entry; this is the neighbour that is not,
// and what has to survive it is the re-run underneath every Git command.
//
// It bounds itself twice over. Its own deadline stops it whatever becomes of
// the test, and the test's cleanup closes its channel and waits for it, so a
// test that fails early never leaves a loop running against a repository Go is
// about to delete.
func StartCreationLoop(t *testing.T, repository string) {
	t.Helper()
	// One entry is held for the whole loop, because Git deletes worktrees/
	// itself when its last entry goes and a walk crossing that dies on the
	// directory rather than on an entry — a different race from the one this is
	// here to produce.
	held := filepath.Join(t.TempDir(), "creation-loop-held")
	RunPipelineGit(t, repository, "worktree", "add", "--quiet", "--detach", held, "HEAD")

	base := filepath.Join(t.TempDir(), "creation-loop")
	stop := make(chan struct{})
	stopped := make(chan int)
	go func() {
		cycles := 0
		deadline := time.Now().Add(10 * time.Minute)
		for time.Now().Before(deadline) {
			select {
			case <-stop:
				stopped <- cycles
				return
			default:
			}
			path := fmt.Sprintf("%s-%d", base, cycles)
			// Its own failures are not the subject: a raw add crossing another
			// raw add has nothing covering it, which is exactly why the runs
			// beside it do.
			if _, err := AttemptPipelineGit(repository, "worktree", "add", "--quiet", "--detach", path, "HEAD"); err == nil {
				_, _ = AttemptPipelineGit(repository, "worktree", "remove", "--force", path)
			}
			cycles++
		}
		stopped <- cycles
	}()
	t.Cleanup(func() {
		close(stop)
		t.Logf("creation loop ran %d add/remove cycles against %s", <-stopped, repository)
	})
}

// DisablePipelineMaintenance stops Git from handing this repository to a
// process that outlives the command which started it. Writing commands
// otherwise start "git maintenance run --auto --detach", which daemonizes into
// its own session and is still working inside .git when a test's TempDir
// cleanup deletes that directory. Nothing under test needs maintenance, so none
// is started.
func DisablePipelineMaintenance(t *testing.T, repository string) {
	t.Helper()
	RunPipelineGit(t, repository, "config", "maintenance.auto", "false")
	RunPipelineGit(t, repository, "config", "gc.auto", "0")
}

// RemoveLinkedPipelineWorktrees makes a test responsible for the worktrees its
// run created. TempDir deletes directories and unregisters nothing, so a
// registration inside .git outlives the checkout it names.
func RemoveLinkedPipelineWorktrees(t *testing.T, repository string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repository, ".git")); err != nil {
		return
	}
	for _, path := range LinkedPipelineWorktreePaths(t, repository) {
		// A worktree a run already removed is gone from the listing; one whose
		// directory is missing can only be pruned, which the sweep below does.
		if output, err := AttemptPipelineGit(repository, "worktree", "remove", "--force", path); err != nil {
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("cleanup could not remove worktree %s: %v: %s", path, err, output)
			}
		}
	}
	if output, err := AttemptPipelineGit(repository, "worktree", "prune"); err != nil {
		t.Errorf("cleanup could not prune worktree registrations: %v: %s", err, output)
	}
	if remaining := LinkedPipelineWorktreePaths(t, repository); len(remaining) > 0 {
		t.Errorf("cleanup left worktree registrations behind: %v", remaining)
	}
}

// LinkedPipelineWorktreePaths names every worktree registered against
// repository apart from the primary checkout, which is the repository itself.
func LinkedPipelineWorktreePaths(t *testing.T, repository string) []string {
	t.Helper()
	listing, err := AttemptPipelineGit(repository, "worktree", "list", "--porcelain")
	if err != nil {
		t.Errorf("cleanup could not list worktrees: %v: %s", err, listing)
		return nil
	}
	var paths []string
	for _, line := range strings.Split(listing, "\n") {
		path, found := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !found || SameWorktreePath(path, repository) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

// SameWorktreePath compares two checkout paths the way the worktree manager
// does, tolerating symlinked ancestors: Git reports the resolved path, which on
// macOS is not the temporary directory the test was handed.
func SameWorktreePath(left, right string) bool {
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	resolvedLeft, err := filepath.EvalSymlinks(left)
	if err != nil {
		return false
	}
	resolvedRight, err := filepath.EvalSymlinks(right)
	if err != nil {
		return false
	}
	return resolvedLeft == resolvedRight
}

func RunPipelineGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	if output, err := AttemptPipelineGit(repository, args...); err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
}

// AttemptPipelineGit runs a Git command whose failure is the caller's to
// interpret, rather than a test failure at the point of the call.
func AttemptPipelineGit(repository string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func GitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v error = %v", args, err)
	}
	return string(output)
}

func GitLine(t *testing.T, repository string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(GitOutput(t, repository, args...))
}

func WritePipelineFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", relative, err)
	}
}

// InterruptingStore fails saves at a chosen phase, either once (an interrupted
// step that recovers) or for good (persistence lost at that boundary).
type InterruptingStore struct {
	orchestrator.StateStore
	FailPhase  runstate.Phase
	FailAlways bool
	Failed     bool
}

func (s *InterruptingStore) Save(state runstate.State) error {
	if state.Phase == s.FailPhase && (s.FailAlways || !s.Failed) {
		s.Failed = true
		return errors.New("state store is unavailable")
	}
	return s.StateStore.Save(state)
}

// HookedWorktrees lets a test observe or replace the destructive cleanup step
// while the rest of the manager stays real.
type HookedWorktrees struct {
	orchestrator.WorktreeManager
	BeforeCleanup func() error
	// Cleanup replaces the real removal entirely, which is how a partial
	// cleanup (one artifact gone, one left) is exercised end to end.
	Cleanup func(gitworktree.CleanupRequest) (gitworktree.Cleanup, error)
}

func (w *HookedWorktrees) CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
	if w.BeforeCleanup != nil {
		if err := w.BeforeCleanup(); err != nil {
			return gitworktree.Cleanup{}, err
		}
	}
	if w.Cleanup != nil {
		return w.Cleanup(request)
	}
	return w.WorktreeManager.CleanupIntegrated(ctx, request)
}

// AutomaticFixture is the standard approved-and-integrated setup: a developer
// that writes one file, a passing check, and an approving reviewer.
func AutomaticFixture(t *testing.T) (string, *orchestratortest.Tracker, *orchestratortest.Backend, orchestrator.Pipeline, *runstate.Store) {
	t.Helper()
	repository := PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, ApproveVerdict)
	pipeline, store := NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	return repository, tracker, provider, pipeline, store
}

func CountCalls(calls []string, name string) int {
	matching := 0
	for _, call := range calls {
		if call == name {
			matching++
		}
	}
	return matching
}

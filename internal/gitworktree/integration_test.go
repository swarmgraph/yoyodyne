package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestManagerIntegratePromotesCheckedWorkAndPermitsCleanup(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, "doomed.txt", "delete me\n")
	runGit(t, repository, "add", "doomed.txt")
	runGit(t, repository, "commit", "-m", "add doomed file")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-integrate",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.TargetBranch != "main" {
		t.Fatalf("recorded target = %q", worktree.TargetBranch)
	}
	writeFile(t, worktree.Path, "README.txt", "test\nedited\n")
	writeFile(t, worktree.Path, filepath.Join("sub", "new.txt"), "brand new\n")
	if err := os.Remove(filepath.Join(worktree.Path, "doomed.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if integration.PreviousTargetCommit != worktree.BaseCommit {
		t.Errorf("previous target = %q, want base %q", integration.PreviousTargetCommit, worktree.BaseCommit)
	}
	if integration.SourceCommit == worktree.BaseCommit || integration.SourceCommit != integration.TargetCommit {
		t.Errorf("integration commits = %#v", integration)
	}
	if integration.Branch != worktree.Branch || integration.TargetBranch != "main" {
		t.Errorf("integration refs = %#v", integration)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != integration.TargetCommit {
		t.Errorf("main = %q, want %q", head, integration.TargetCommit)
	}

	// A fast-forward of the branch the primary checkout is on must leave that
	// checkout consistent with the branch it just moved.
	if content := readFile(t, repository, "README.txt"); content != "test\nedited\n" {
		t.Errorf("primary README.txt = %q", content)
	}
	if content := readFile(t, repository, filepath.Join("sub", "new.txt")); content != "brand new\n" {
		t.Errorf("primary sub/new.txt = %q", content)
	}
	if _, err := os.Stat(filepath.Join(repository, "doomed.txt")); !os.IsNotExist(err) {
		t.Errorf("deleted file survived integration: %v", err)
	}
	if status := gitOutput(t, repository, "status", "--porcelain=v1", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Errorf("primary checkout is dirty after integration: %q", status)
	}
	if author := gitLine(t, repository, "log", "-1", "--format=%an <%ae>", integration.TargetCommit); author != harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+">" {
		t.Errorf("integration commit author = %q, want the harness identity", author)
	}
	if subject := gitLine(t, repository, "log", "-1", "--format=%s", integration.TargetCommit); !strings.Contains(subject, "yoyodyne-integrate") {
		t.Errorf("default commit subject = %q", subject)
	}

	cleanup, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	})
	if err != nil {
		t.Fatalf("CleanupIntegrated() after integration error = %v", err)
	}
	if !cleanup.Complete() {
		t.Fatalf("CleanupIntegrated() = %#v", cleanup)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Errorf("integrated worktree still exists: %v", err)
	}
	if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) != "" {
		t.Errorf("integrated branch still exists: %q", branches)
	}
}

// Observe is what reconciliation reads instead of trusting durable state. It
// has to answer through the whole life of a run's artifacts, including after
// they are gone, and it must only call work integrated once a commit past the
// base is actually contained in the target.
func TestManagerObserveReportsArtifactsThroughIntegrationAndRemoval(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-observe",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// A fresh worktree carries uncommitted work and nothing integrated. The
	// base commit is in the target by construction, which must never on its own
	// read as a promotion.
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	fresh, err := manager.Observe(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Observe() fresh error = %v", err)
	}
	if !fresh.WorktreeRegistered || !fresh.WorktreePresent || !fresh.WorktreeDirty {
		t.Fatalf("fresh observation = %#v", fresh)
	}
	if fresh.WorktreeBranch != worktree.Branch || fresh.WorktreeHead != worktree.BaseCommit {
		t.Fatalf("fresh observation = %#v, want the recorded branch at its base", fresh)
	}
	if !fresh.BranchExists || fresh.BranchCommit != worktree.BaseCommit || fresh.BranchIntegrated {
		t.Fatalf("fresh observation = %#v, want nothing integrated", fresh)
	}
	if !fresh.TargetExists || fresh.TargetCommit != worktree.BaseCommit {
		t.Fatalf("fresh observation = %#v, want the target at the base", fresh)
	}

	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	integrated, err := manager.Observe(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Observe() integrated error = %v", err)
	}
	if !integrated.BranchIntegrated || integrated.BranchCommit != integration.SourceCommit {
		t.Fatalf("integrated observation = %#v, want the promoted commit", integrated)
	}
	if integrated.WorktreeDirty || integrated.WorktreeHead != integration.SourceCommit {
		t.Fatalf("integrated observation = %#v, want a clean worktree at the harness commit", integrated)
	}

	if _, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	}); err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	// Artifacts that are already gone are an observation, not a failure: this
	// is exactly what reconciliation asks about a run nobody finished.
	gone, err := manager.Observe(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Observe() after cleanup error = %v", err)
	}
	if gone.WorktreeRegistered || gone.WorktreePresent || gone.BranchExists || gone.BranchIntegrated {
		t.Fatalf("observation after cleanup = %#v, want every run artifact absent", gone)
	}
	if !gone.TargetExists || gone.TargetCommit != integration.TargetCommit {
		t.Fatalf("observation after cleanup = %#v, want the target still carrying the work", gone)
	}
}

// A branch that moved on without reaching the target is not integrated. Saying
// otherwise would let reconciliation close an item and delete a change that is
// nowhere in the target's history.
func TestManagerObserveDoesNotCallUnpromotedWorkIntegrated(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-unpromoted",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	runGit(t, worktree.Path, "add", "feature.txt")
	runGit(t, worktree.Path, "commit", "-m", "committed but never promoted")

	observation, err := manager.Observe(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if observation.BranchCommit == worktree.BaseCommit || observation.BranchIntegrated {
		t.Fatalf("observation = %#v, want a moved branch that is not integrated", observation)
	}
	if observation.TargetCommit != worktree.BaseCommit {
		t.Fatalf("observation = %#v, want the target still at the base", observation)
	}
}

func TestManagerCleanupIsResumableAcrossItsDestructiveSteps(t *testing.T) {
	t.Parallel()

	newIntegratedWorktree := func(t *testing.T) (string, *Manager, Worktree, Integration) {
		t.Helper()
		repository := newRepository(t)
		manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
		worktree, err := manager.Create(context.Background(), CreateRequest{
			RunID:        testRunID,
			WorkItemID:   "yoyodyne-resume",
			BaseRef:      "HEAD",
			TargetBranch: "main",
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		writeFile(t, worktree.Path, "feature.txt", "implemented\n")
		integration, err := manager.Integrate(context.Background(), worktree, "")
		if err != nil {
			t.Fatalf("Integrate() error = %v", err)
		}
		return repository, manager, worktree, integration
	}

	t.Run("resumes when only the branch is left", func(t *testing.T) {
		t.Parallel()
		repository, manager, worktree, integration := newIntegratedWorktree(t)
		request := CleanupRequest{Worktree: worktree, TargetBranch: worktree.TargetBranch, SourceCommit: integration.SourceCommit}

		// Interrupt cleanup exactly between its two destructive steps.
		runGit(t, repository, "worktree", "remove", worktree.Path)
		if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) == "" {
			t.Fatal("test setup did not leave the branch behind")
		}

		cleanup, err := manager.CleanupIntegrated(context.Background(), request)
		if err != nil {
			t.Fatalf("CleanupIntegrated() retry error = %v", err)
		}
		if !cleanup.Complete() {
			t.Fatalf("retry = %#v, want both artifacts removed", cleanup)
		}
		if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) != "" {
			t.Fatalf("retry did not delete the integrated branch: %q", branches)
		}
	})

	t.Run("resumes the exact registration that outlived its directory", func(t *testing.T) {
		t.Parallel()
		repository := newRepository(t)
		worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
		runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
		manager, err := New(Options{Runner: runner, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		worktree, err := manager.Create(context.Background(), CreateRequest{
			RunID:        testRunID,
			WorkItemID:   "yoyodyne-resume",
			BaseRef:      "HEAD",
			TargetBranch: "main",
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		writeFile(t, worktree.Path, "feature.txt", "implemented\n")
		integration, err := manager.Integrate(context.Background(), worktree, "")
		if err != nil {
			t.Fatalf("Integrate() error = %v", err)
		}

		// An unrelated run's worktree is left stale on purpose: recovering this
		// run must not touch anyone else's registration.
		foreign, err := manager.Create(context.Background(), CreateRequest{
			RunID:      "run-fedcba9876543210fedcba9876543210",
			WorkItemID: "yoyodyne-foreign",
			BaseRef:    "HEAD",
		})
		if err != nil {
			t.Fatalf("Create() foreign error = %v", err)
		}
		if err := os.RemoveAll(foreign.Path); err != nil {
			t.Fatalf("RemoveAll() foreign error = %v", err)
		}

		// A removal interrupted partway leaves a registration with no directory.
		if err := os.RemoveAll(worktree.Path); err != nil {
			t.Fatalf("RemoveAll() error = %v", err)
		}
		cleanup, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
			Worktree:     worktree,
			TargetBranch: worktree.TargetBranch,
			SourceCommit: integration.SourceCommit,
		})
		if err != nil {
			t.Fatalf("CleanupIntegrated() retry error = %v", err)
		}
		if !cleanup.Complete() {
			t.Fatalf("retry = %#v", cleanup)
		}
		if registered, _, err := manager.registeredWorktree(context.Background(), worktree.Path); err != nil || registered {
			t.Fatalf("stale registration survived: registered = %t, err = %v", registered, err)
		}
		if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) != "" {
			t.Fatalf("retry did not delete the integrated branch: %q", branches)
		}

		// Recovery is targeted: no repository-wide pruning, and the unrelated
		// stale registration and its branch are untouched.
		for _, command := range runner.commands {
			if len(command) >= 2 && command[len(command)-2] == "worktree" && command[len(command)-1] == "prune" {
				t.Fatalf("cleanup pruned repository-wide registrations: %#v", command)
			}
		}
		foreignRegistered, _, err := manager.registeredWorktree(context.Background(), foreign.Path)
		if err != nil {
			t.Fatalf("registeredWorktree() foreign error = %v", err)
		}
		if !foreignRegistered {
			t.Fatal("cleanup removed an unrelated stale worktree registration")
		}
		if branches := gitOutput(t, repository, "branch", "--list", foreign.Branch); strings.TrimSpace(branches) == "" {
			t.Fatal("cleanup deleted an unrelated branch")
		}
	})

	t.Run("is a no-op once both artifacts are gone", func(t *testing.T) {
		t.Parallel()
		_, manager, worktree, integration := newIntegratedWorktree(t)
		request := CleanupRequest{Worktree: worktree, TargetBranch: worktree.TargetBranch, SourceCommit: integration.SourceCommit}
		if _, err := manager.CleanupIntegrated(context.Background(), request); err != nil {
			t.Fatalf("CleanupIntegrated() error = %v", err)
		}

		cleanup, err := manager.CleanupIntegrated(context.Background(), request)
		if err != nil {
			t.Fatalf("CleanupIntegrated() repeat error = %v", err)
		}
		if !cleanup.Complete() {
			t.Fatalf("repeat = %#v, want both artifacts reported absent", cleanup)
		}
	})
}

// The one place cleanup could destroy work nothing else records: a worktree that
// gained uncommitted changes after the commit that authorized its removal. The
// removal is refused and the directory is left exactly as it is, which is what
// makes "preservation survives cleanup" true for the integrated path — the
// retirement sweep is what moves such a checkout, and it records the tree on a
// ref before it does.
func TestManagerCleanupRefusesAWorktreeHoldingUncommittedWork(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-dirty",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	// Somebody's half-finished work, arriving after the promotion this cleanup is
	// authorized by. No commit, no branch, and no ref holds a copy of it.
	writeFile(t, worktree.Path, "unsaved.txt", "half-finished\n")

	cleanup, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	})
	if err == nil || !strings.Contains(err.Error(), "dirty worktree") {
		t.Fatalf("CleanupIntegrated() dirty worktree error = %v", err)
	}
	if cleanup.WorktreeRemoved {
		t.Fatalf("cleanup = %#v, want nothing reported removed", cleanup)
	}
	if content := readFile(t, worktree.Path, "unsaved.txt"); content != "half-finished\n" {
		t.Fatalf("unsaved.txt = %q, want the uncommitted work exactly as it was", content)
	}
}

func TestManagerCleanupRefusesUnprovenOrForeignArtifacts(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-unproven",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	request := CleanupRequest{Worktree: worktree, TargetBranch: worktree.TargetBranch, SourceCommit: integration.SourceCommit}

	// A commit that is not in the target proves nothing, so nothing is removed.
	unproven := request
	unproven.SourceCommit = strings.Repeat("a", 40)
	if _, err := manager.CleanupIntegrated(context.Background(), unproven); err == nil || !strings.Contains(err.Error(), "not contained in") {
		t.Fatalf("CleanupIntegrated() unproven commit error = %v", err)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("unproven cleanup removed the worktree: %v", err)
	}

	// The base commit is in the target by construction, so accepting it would
	// make the containment proof vacuous for a worktree that never integrated.
	vacuous := request
	vacuous.SourceCommit = worktree.BaseCommit
	if _, err := manager.CleanupIntegrated(context.Background(), vacuous); err == nil || !strings.Contains(err.Error(), "proves no integration") {
		t.Fatalf("CleanupIntegrated() base commit error = %v", err)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("vacuous cleanup removed the worktree: %v", err)
	}

	// A target the worktree was never aimed at cannot authorize its removal,
	// even when the integrated commit is genuinely contained in that branch.
	runGit(t, repository, "branch", "elsewhere", worktree.TargetBranch)
	foreign := request
	foreign.TargetBranch = "elsewhere"
	if _, err := manager.CleanupIntegrated(context.Background(), foreign); err == nil || !strings.Contains(err.Error(), "does not match the worktree's recorded target") {
		t.Fatalf("CleanupIntegrated() foreign target error = %v", err)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("foreign target cleanup removed the worktree: %v", err)
	}

	// A branch that moved off the integrated commit is not the branch we
	// integrated, so it survives even though the worktree is removed.
	runGit(t, repository, "worktree", "remove", worktree.Path)
	runGit(t, repository, "branch", "-f", worktree.Branch, worktree.BaseCommit)
	cleanup, err := manager.CleanupIntegrated(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "want the integrated commit") {
		t.Fatalf("CleanupIntegrated() moved branch error = %v", err)
	}
	if !cleanup.WorktreeRemoved || cleanup.BranchRemoved {
		t.Fatalf("partial cleanup = %#v, want the worktree absent and the branch kept", cleanup)
	}
	if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) == "" {
		t.Fatal("a branch that moved off the integrated commit was deleted")
	}
}

func TestManagerIntegrateAdvancesTargetThatIsNotCheckedOut(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runGit(t, repository, "branch", "release")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	mainCommit := gitLine(t, repository, "rev-parse", "refs/heads/main")
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-release",
		BaseRef:      "release",
		TargetBranch: "release",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "release.txt", "released\n")

	integration, err := manager.Integrate(context.Background(), worktree, "yoyodyne: integrate release work")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if release := gitLine(t, repository, "rev-parse", "refs/heads/release"); release != integration.SourceCommit {
		t.Errorf("release = %q, want %q", release, integration.SourceCommit)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != mainCommit {
		t.Errorf("main moved during integration of release: %q", head)
	}
	if subject := gitLine(t, repository, "log", "-1", "--format=%s", integration.SourceCommit); subject != "yoyodyne: integrate release work" {
		t.Errorf("commit subject = %q", subject)
	}
	// The primary checkout is on another branch and must be untouched by an
	// integration that only advanced a ref.
	if _, err := os.Stat(filepath.Join(repository, "release.txt")); !os.IsNotExist(err) {
		t.Errorf("integration wrote into the primary checkout: %v", err)
	}

	// Cleanup must be as independent of the checked-out branch as integration
	// was: deletion is decided against the recorded commit, not against HEAD.
	cleanup, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	})
	if err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	if !cleanup.Complete() {
		t.Fatalf("CleanupIntegrated() = %#v", cleanup)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Errorf("integrated worktree still exists: %v", err)
	}
	if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) != "" {
		t.Errorf("integrated branch still exists: %q", branches)
	}
	if release := gitLine(t, repository, "rev-parse", "refs/heads/release"); release != integration.SourceCommit {
		t.Errorf("release moved during cleanup: %q", release)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != mainCommit {
		t.Errorf("main moved during cleanup: %q", head)
	}
}

// postMutationFaultRunner fails one command, but only once an earlier command
// has already run. It is how a verification step is broken while the
// destructive step it verifies genuinely succeeds.
type postMutationFaultRunner struct {
	delegate execution.ProcessRunner
	arm      func(args []string) bool
	fail     func(args []string) bool
	armed    bool
	failed   bool
}

func (r *postMutationFaultRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.armed && !r.failed && r.fail(command.Args) {
		r.failed = true
		return execution.ProcessResult{}, errors.New("git process runner is unavailable")
	}
	result, err := r.delegate.Run(ctx, command, observer)
	if !r.armed && r.arm(command.Args) {
		r.armed = true
	}
	return result, err
}

func hasArgs(args []string, wanted ...string) bool {
	for index := 0; index+len(wanted) <= len(args); index++ {
		if reflect.DeepEqual(args[index:index+len(wanted)], wanted) {
			return true
		}
	}
	return false
}

func TestManagerCleanupReportsRemovalsWhoseVerificationCouldNotRun(t *testing.T) {
	t.Parallel()

	// A destructive Git command that succeeded is a fact. When the command that
	// would confirm it cannot run, the removal must still be reported, or a
	// caller will send an operator after an artifact that no longer exists.
	newIntegrated := func(t *testing.T, runner *postMutationFaultRunner) (string, *Manager, Worktree, Integration) {
		t.Helper()
		repository := newRepository(t)
		runner.delegate = execution.OSProcessRunner{}
		manager, err := New(Options{Runner: runner, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: testGitBudget})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		worktree, err := manager.Create(context.Background(), CreateRequest{
			RunID:        testRunID,
			WorkItemID:   "yoyodyne-verify",
			BaseRef:      "HEAD",
			TargetBranch: "main",
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		writeFile(t, worktree.Path, "feature.txt", "implemented\n")
		integration, err := manager.Integrate(context.Background(), worktree, "")
		if err != nil {
			t.Fatalf("Integrate() error = %v", err)
		}
		return repository, manager, worktree, integration
	}

	t.Run("worktree removal verification fails", func(t *testing.T) {
		t.Parallel()
		runner := &postMutationFaultRunner{
			arm:  func(args []string) bool { return hasArgs(args, "worktree", "remove") },
			fail: func(args []string) bool { return hasArgs(args, "worktree", "list") },
		}
		repository, manager, worktree, integration := newIntegrated(t, runner)

		cleanup, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
			Worktree:     worktree,
			TargetBranch: worktree.TargetBranch,
			SourceCommit: integration.SourceCommit,
		})
		if err == nil || !strings.Contains(err.Error(), "verify removal of worktree") {
			t.Fatalf("CleanupIntegrated() error = %v, want a verification failure", err)
		}
		if !runner.failed {
			t.Fatal("the verification fault was never injected")
		}
		// The removal really happened, so the result must say so.
		if !cleanup.WorktreeRemoved {
			t.Fatalf("cleanup = %#v, want the completed removal preserved", cleanup)
		}
		if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
			t.Fatalf("worktree still exists on disk: %v", err)
		}
		if registrations := gitOutput(t, repository, "worktree", "list", "--porcelain"); strings.Contains(registrations, worktree.Path) {
			t.Fatalf("worktree is still registered: %q", registrations)
		}
		// Branch deletion is not attempted after a failed verification, so the
		// branch is honestly reported as still present.
		if cleanup.BranchRemoved {
			t.Fatalf("cleanup = %#v, want the untouched branch reported as present", cleanup)
		}
	})

	t.Run("branch deletion verification fails", func(t *testing.T) {
		t.Parallel()
		runner := &postMutationFaultRunner{
			arm:  func(args []string) bool { return hasArgs(args, "update-ref", "-d") },
			fail: func(args []string) bool { return hasArgs(args, "show-ref") },
		}
		repository, manager, worktree, integration := newIntegrated(t, runner)

		cleanup, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
			Worktree:     worktree,
			TargetBranch: worktree.TargetBranch,
			SourceCommit: integration.SourceCommit,
		})
		if err == nil || !strings.Contains(err.Error(), "verify deletion of branch") {
			t.Fatalf("CleanupIntegrated() error = %v, want a verification failure", err)
		}
		if !runner.failed {
			t.Fatal("the verification fault was never injected")
		}
		if !cleanup.WorktreeRemoved || !cleanup.BranchRemoved {
			t.Fatalf("cleanup = %#v, want both completed removals preserved", cleanup)
		}
		if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) != "" {
			t.Fatalf("branch still exists: %q", branches)
		}
		if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
			t.Fatalf("worktree still exists on disk: %v", err)
		}
	})

	t.Run("verification that observes a surviving artifact still reports it", func(t *testing.T) {
		t.Parallel()
		// The flag is only preserved when verification cannot run. An
		// observation that the artifact survived must still clear it.
		repository := newRepository(t)
		manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
		worktree, err := manager.Create(context.Background(), CreateRequest{
			RunID:        testRunID,
			WorkItemID:   "yoyodyne-survivor",
			BaseRef:      "HEAD",
			TargetBranch: "main",
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		writeFile(t, worktree.Path, "feature.txt", "implemented\n")
		integration, err := manager.Integrate(context.Background(), worktree, "")
		if err != nil {
			t.Fatalf("Integrate() error = %v", err)
		}
		runGit(t, repository, "worktree", "remove", worktree.Path)
		// A branch re-created at the integrated commit and checked out elsewhere
		// survives cleanup, and cleanup says so.
		reused := filepath.Join(t.TempDir(), "reused")
		runGit(t, repository, "worktree", "add", reused, worktree.Branch)

		cleanup, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
			Worktree:     worktree,
			TargetBranch: worktree.TargetBranch,
			SourceCommit: integration.SourceCommit,
		})
		if err == nil {
			t.Fatal("CleanupIntegrated() error = nil, want a refusal")
		}
		if cleanup.BranchRemoved {
			t.Fatalf("cleanup = %#v, want the surviving branch reported as present", cleanup)
		}
		if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) == "" {
			t.Fatal("a surviving branch was deleted")
		}
	})
}

func TestManagerCleanupRefusesABranchStillCheckedOut(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-checkedout",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}

	// Deleting a ref cannot see checkouts, so a branch that is still checked out
	// somewhere must be refused rather than deleted out from under it.
	cleanup, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	})
	if err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	if !cleanup.Complete() {
		t.Fatalf("CleanupIntegrated() = %#v", cleanup)
	}

	// Re-create the branch and check it out elsewhere to prove the refusal.
	runGit(t, repository, "branch", worktree.Branch, integration.SourceCommit)
	reused := filepath.Join(t.TempDir(), "reused")
	runGit(t, repository, "worktree", "add", reused, worktree.Branch)

	retry, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	})
	if err == nil || !strings.Contains(err.Error(), "still checked out") {
		t.Fatalf("CleanupIntegrated() checked-out branch error = %v", err)
	}
	if retry.BranchRemoved {
		t.Fatalf("cleanup deleted a branch that is still checked out: %#v", retry)
	}
	if branches := gitOutput(t, repository, "branch", "--list", worktree.Branch); strings.TrimSpace(branches) == "" {
		t.Fatal("a checked-out branch was deleted")
	}
}

func TestManagerIntegrateRefusesEmptyAndAgentCommittedWork(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, ".gitignore", "ignored.txt\n")
	runGit(t, repository, "add", ".gitignore")
	runGit(t, repository, "commit", "-m", "ignore build output")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-empty",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")

	if _, err := manager.Integrate(context.Background(), worktree, ""); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("Integrate() empty error = %v, want ErrNoChanges", err)
	}
	// An ignored-only worktree has produced nothing integratable either.
	writeFile(t, worktree.Path, "ignored.txt", "not a reviewable change\n")
	if _, err := manager.Integrate(context.Background(), worktree, ""); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("Integrate() ignored-only error = %v, want ErrNoChanges", err)
	}

	writeFile(t, worktree.Path, "new.txt", "new\n")
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("Integrate() developer commit error = %v", err)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != base {
		t.Fatalf("main moved during a refused integration: %q", head)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, "new.txt")); err != nil {
		t.Fatalf("refused worktree was not preserved: %v", err)
	}
}

func TestManagerIntegrateRefusesTargetDriftAndDirtyPrimaryWithoutCommitting(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-drift",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")

	// A dirty primary checkout is refused before anything is committed.
	writeFile(t, repository, "unrelated.txt", "someone else's edit\n")
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "primary checkout is not ready") {
		t.Fatalf("Integrate() dirty primary error = %v", err)
	}
	assertUncommittedWork(t, worktree, "work.txt")

	// The same change is refused once the target has moved on.
	runGit(t, repository, "add", "unrelated.txt")
	runGit(t, repository, "commit", "-m", "concurrent target change")
	drifted := gitLine(t, repository, "rev-parse", "refs/heads/main")
	_, err = manager.Integrate(context.Background(), worktree, "")
	if !errors.Is(err, ErrTargetDrift) {
		t.Fatalf("Integrate() drift error = %v, want ErrTargetDrift", err)
	}
	if !strings.Contains(err.Error(), drifted) || !strings.Contains(err.Error(), worktree.BaseCommit) {
		t.Errorf("drift error does not report both commits: %v", err)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != drifted {
		t.Errorf("main = %q, want the drifted commit %q", head, drifted)
	}
	assertUncommittedWork(t, worktree, "work.txt")
}

func TestManagerIntegrateRefusesInvalidOwnershipAndTargets(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-invalid",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")

	tests := []struct {
		name    string
		mutate  func(Worktree) Worktree
		message string
	}{
		{"no target", func(w Worktree) Worktree { w.TargetBranch = ""; return w }, "no recorded target branch"},
		{"qualified ref", func(w Worktree) Worktree { w.TargetBranch = "refs/heads/main"; return w }, "must be a local branch name"},
		{"HEAD", func(w Worktree) Worktree { w.TargetBranch = "HEAD"; return w }, "must be a local branch name"},
		{"traversal", func(w Worktree) Worktree { w.TargetBranch = "../evil"; return w }, "invalid target branch"},
		{"own branch", func(w Worktree) Worktree { w.TargetBranch = w.Branch; return w }, "must differ from the worktree branch"},
		{"missing branch", func(w Worktree) Worktree { w.TargetBranch = "absent"; return w }, "resolve branch absent"},
		{"tampered path", func(w Worktree) Worktree { w.Path = repository; return w }, "owned path"},
		{"tampered branch", func(w Worktree) Worktree { w.Branch = "yoyodyne/other/01234567"; return w }, "owned branch"},
		{"tampered base", func(w Worktree) Worktree { w.BaseCommit = "not-a-commit"; return w }, "base commit is invalid"},
	}
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := manager.Integrate(context.Background(), test.mutate(worktree), "")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Integrate() error = %v, want %q", err, test.message)
			}
			if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != base {
				t.Fatalf("main moved during a refused integration: %q", head)
			}
		})
	}
	assertUncommittedWork(t, worktree, "work.txt")
}

func TestManagerIntegrateRefusesTargetCheckedOutElsewhere(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runGit(t, repository, "branch", "release")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-elsewhere",
		BaseRef:      "release",
		TargetBranch: "release",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")
	foreign := filepath.Join(t.TempDir(), "foreign")
	runGit(t, repository, "worktree", "add", foreign, "release")

	release := gitLine(t, repository, "rev-parse", "refs/heads/release")
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "checked out in another worktree") {
		t.Fatalf("Integrate() foreign checkout error = %v", err)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/release"); head != release {
		t.Errorf("release moved into a foreign checkout: %q", head)
	}
	assertUncommittedWork(t, worktree, "work.txt")
}

func TestManagerIntegratePreservesCommittedWorktreeWhenCommitOrUpdateFails(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	failing := &commandFailureRunner{delegate: execution.OSProcessRunner{}, failOn: "commit"}
	manager, err := New(Options{Runner: failing, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-failure",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")

	failing.armed = true
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "injected commit failure") {
		t.Fatalf("Integrate() commit failure error = %v", err)
	}
	failing.armed = false
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != base {
		t.Fatalf("main moved after a failed commit: %q", head)
	}
	if content := readFile(t, worktree.Path, "work.txt"); content != "developer work\n" {
		t.Fatalf("worktree work was lost after a failed commit: %q", content)
	}
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head != worktree.BaseCommit {
		t.Fatalf("worktree HEAD = %q after a failed commit, want the base", head)
	}
}

func TestManagerIntegrateRefusesNonFastForwardAndKeepsHarnessCommit(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	drifting := &driftingRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{Runner: drifting, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-race",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")

	// The target is moved after the drift check but before the update, so only
	// the fast-forward-only update itself can catch it.
	drifting.beforeUpdate = func() {
		writeFile(t, repository, "concurrent.txt", "racing change\n")
		runGit(t, repository, "add", "concurrent.txt")
		runGit(t, repository, "commit", "-m", "racing target change")
	}
	_, err = manager.Integrate(context.Background(), worktree, "")
	if !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("Integrate() race error = %v, want ErrNotFastForward", err)
	}
	racing := gitLine(t, repository, "rev-parse", "refs/heads/main")
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head == worktree.BaseCommit || head == racing {
		t.Fatalf("harness commit was not preserved on the worktree branch: %q", head)
	}
	if content := readFile(t, repository, "concurrent.txt"); content != "racing change\n" {
		t.Fatalf("racing target change was overwritten: %q", content)
	}
	if status := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Fatalf("preserved worktree is dirty: %q", status)
	}
}

func TestManagerIntegrateUsesExactCommitAndDisablesAmbientCustomization(t *testing.T) {
	repository := newRepository(t)
	drifting := &driftingRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{Runner: drifting, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-exact",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "approved work\n")

	// These normally override user.name/user.email. Harness commits must ignore
	// them and must not execute repository-controlled lifecycle hooks.
	t.Setenv("GIT_AUTHOR_NAME", "Ambient Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "ambient-author@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Ambient Committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "ambient-committer@example.com")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	t.Setenv("YOYODYNE_TEST_HOOK_MARKER", marker)
	for _, name := range []string{"post-commit", "post-merge", "reference-transaction"} {
		hook := filepath.Join(repository, ".git", "hooks", name)
		if err := os.WriteFile(hook, []byte("#!/bin/sh\n: > \"$YOYODYNE_TEST_HOOK_MARKER\"\n"), 0o700); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	var sourceCommit string
	var movedBranchCommit string
	drifting.beforeUpdate = func() {
		sourceCommit = gitLine(t, worktree.Path, "rev-parse", "HEAD")
		movedBranchCommit = gitLine(t, repository, "commit-tree", worktree.BaseCommit+"^{tree}", "-p", worktree.BaseCommit, "-m", "concurrent source ref move")
		runGit(t, repository, "-c", "core.hooksPath="+os.DevNull, "update-ref", "refs/heads/"+worktree.Branch, movedBranchCommit, sourceCommit)
	}

	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if integration.SourceCommit != sourceCommit || integration.TargetCommit != sourceCommit {
		t.Fatalf("integration commits = %#v, want exact source %s", integration, sourceCommit)
	}
	if main := gitLine(t, repository, "rev-parse", "refs/heads/main"); main != sourceCommit || main == movedBranchCommit {
		t.Fatalf("main = %q, want exact source %q and not moved branch %q", main, sourceCommit, movedBranchCommit)
	}
	if content := readFile(t, repository, "work.txt"); content != "approved work\n" {
		t.Fatalf("primary work.txt = %q", content)
	}
	if author := gitLine(t, repository, "log", "-1", "--format=%an <%ae>", sourceCommit); author != harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+">" {
		t.Errorf("integration author = %q", author)
	}
	if committer := gitLine(t, repository, "log", "-1", "--format=%cn <%ce>", sourceCommit); committer != harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+">" {
		t.Errorf("integration committer = %q", committer)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("repository hook ran during integration: %v", err)
	}
}

func TestManagerCreateRejectsTargetThatIsNotTheBase(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runGit(t, repository, "branch", "release")
	writeFile(t, repository, "later.txt", "later\n")
	runGit(t, repository, "add", "later.txt")
	runGit(t, repository, "commit", "-m", "move main past release")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	if _, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-mismatch",
		BaseRef:      "HEAD",
		TargetBranch: "release",
	}); err == nil || !strings.Contains(err.Error(), "not the base commit") {
		t.Fatalf("Create() mismatched target error = %v", err)
	}
	if _, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-badtarget",
		BaseRef:      "HEAD",
		TargetBranch: "refs/heads/main",
	}); err == nil || !strings.Contains(err.Error(), "must be a local branch name") {
		t.Fatalf("Create() invalid target error = %v", err)
	}
}

func TestManagerCurrentBranchNamesTheIntegrationTargetAndRefusesDetachedHead(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	branch, err := manager.CurrentBranch(context.Background())
	if err != nil {
		t.Fatalf("CurrentBranch() error = %v", err)
	}
	if branch != "main" {
		t.Fatalf("CurrentBranch() = %q, want main", branch)
	}

	// A detached HEAD names no branch, so there is nothing to integrate into.
	runGit(t, repository, "checkout", "--detach", "HEAD")
	if _, err := manager.CurrentBranch(context.Background()); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("CurrentBranch() detached error = %v", err)
	}
}

// assertUncommittedWork proves a refused integration left the developer's work
// exactly where it was: uncommitted, in place, and still on the recorded base.
func assertUncommittedWork(t *testing.T, worktree Worktree, relative string) {
	t.Helper()
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head != worktree.BaseCommit {
		t.Fatalf("worktree HEAD = %q, want the recorded base %q", head, worktree.BaseCommit)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, relative)); err != nil {
		t.Fatalf("preserved worktree file %s: %v", relative, err)
	}
}

func readFile(t *testing.T, root, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", relative, err)
	}
	return string(content)
}

func gitLine(t *testing.T, repository string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gitOutput(t, repository, args...))
}

// commandFailureRunner injects a failure into one Git subcommand once armed.
type commandFailureRunner struct {
	delegate execution.ProcessRunner
	failOn   string
	armed    bool
}

func (r *commandFailureRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.armed && hasArgument(command.Args, r.failOn) {
		return execution.ProcessResult{}, errors.New("injected " + r.failOn + " failure")
	}
	return r.delegate.Run(ctx, command, observer)
}

func hasArgument(arguments []string, want string) bool {
	for _, argument := range arguments {
		if argument == want {
			return true
		}
	}
	return false
}

// driftingRunner moves the integration target between the harness commit and
// the fast-forward, exercising the update's own safety rather than the earlier
// drift check.
type driftingRunner struct {
	delegate     execution.ProcessRunner
	beforeUpdate func()
}

func (r *driftingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.beforeUpdate != nil && (containsArguments(command.Args, "merge", "--ff-only") || hasArgument(command.Args, "update-ref")) {
		drift := r.beforeUpdate
		r.beforeUpdate = nil
		drift()
	}
	return r.delegate.Run(ctx, command, observer)
}

// Two runs promoting into one target branch is the case concurrency makes
// ordinary and an operator moving the target already makes real. Exactly one of
// them may promote, the other must lose without losing its work, and the loser
// must then be able to replay onto what the target became and promote after all.
// Both promotion paths are driven, because they fail closed by different
// mechanisms: a fast-forward-only merge in the primary checkout, and a
// compare-and-swap on the ref when the target is checked out nowhere.
func TestManagerIntegrateAdmitsOnePromotionAndReplaysTheLoser(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		// parked leaves the primary checkout on another branch, so the target is
		// advanced by a bare compare-and-swap rather than by a merge.
		parked bool
	}{
		{name: "target checked out in the primary repository"},
		{name: "target checked out nowhere", parked: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := newRepository(t)
			if testCase.parked {
				runGit(t, repository, "checkout", "-b", "parking")
			}
			worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
			held := &heldRunner{delegate: execution.OSProcessRunner{}, holding: make(chan struct{}), release: make(chan struct{})}
			loserManager, err := New(Options{Runner: held, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			winnerManager := newManager(t, repository, worktreeRoot)
			base := gitLine(t, repository, "rev-parse", "refs/heads/main")

			loser, err := loserManager.Create(context.Background(), CreateRequest{
				RunID: testRunID, WorkItemID: "yoyodyne-loser", BaseRef: "main", TargetBranch: "main",
			})
			if err != nil {
				t.Fatalf("Create() loser error = %v", err)
			}
			winner, err := winnerManager.Create(context.Background(), CreateRequest{
				RunID: testRunID, WorkItemID: "yoyodyne-winner", BaseRef: "main", TargetBranch: "main",
			})
			if err != nil {
				t.Fatalf("Create() winner error = %v", err)
			}
			writeFile(t, loser.Path, "loser.txt", "loser work\n")
			writeFile(t, winner.Path, "winner.txt", "winner work\n")

			// The loser is held after it has read where the target is and before it
			// advances anything, which is exactly the window two concurrent
			// promotions overlap in.
			held.holdAfter = func(arguments []string) bool {
				return hasArgument(arguments, "rev-parse") && hasArgument(arguments, "refs/heads/main^{commit}")
			}
			type promotion struct {
				integration Integration
				err         error
			}
			lost := make(chan promotion, 1)
			go func() {
				integration, err := loserManager.Integrate(context.Background(), loser, "")
				lost <- promotion{integration: integration, err: err}
			}()
			<-held.holding
			promoted, err := winnerManager.Integrate(context.Background(), winner, "")
			if err != nil {
				t.Fatalf("Integrate() winner error = %v", err)
			}
			close(held.release)
			refused := <-lost

			if !errors.Is(refused.err, ErrNotFastForward) {
				t.Fatalf("Integrate() loser error = %v, want ErrNotFastForward", refused.err)
			}
			if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != promoted.TargetCommit {
				t.Fatalf("main = %q after both promotions, want the one that won (%q)", head, promoted.TargetCommit)
			}
			// The refusal still reports the commit the harness made, or the losing
			// run would be left holding a HEAD nothing recorded.
			if refused.integration.SourceCommit == "" || refused.integration.TargetCommit != "" {
				t.Fatalf("refused promotion = %#v", refused.integration)
			}
			if head := gitLine(t, loser.Path, "rev-parse", "HEAD"); head != refused.integration.SourceCommit {
				t.Fatalf("loser HEAD = %q, want the reported commit %q", head, refused.integration.SourceCommit)
			}

			// Replaying the loser onto what the target became is what lets it try
			// again. Only its own branch moves.
			loser.HarnessCommit = refused.integration.SourceCommit
			replay, err := loserManager.RebaseOntoTarget(context.Background(), loser, "")
			if err != nil {
				t.Fatalf("RebaseOntoTarget() error = %v", err)
			}
			if !replay.Moved() || replay.BaseCommit != promoted.TargetCommit {
				t.Fatalf("RebaseOntoTarget() = %#v, want the change replayed onto %q", replay, promoted.TargetCommit)
			}
			if replay.HeadCommit == replay.PreviousHeadCommit || replay.PreviousHeadCommit != refused.integration.SourceCommit {
				t.Fatalf("replayed commits = %#v", replay)
			}
			if identity := gitLine(t, loser.Path, "log", "-1", "--format=%ae %ce", replay.HeadCommit); identity != harnessCommitAuthorEmail+" "+harnessCommitAuthorEmail {
				t.Fatalf("replayed commit identity = %q, want the harness identity", identity)
			}

			loser.BaseCommit = replay.BaseCommit
			loser.HarnessCommit = replay.HeadCommit
			retried, err := loserManager.Integrate(context.Background(), loser, "")
			if err != nil {
				t.Fatalf("Integrate() retry error = %v", err)
			}
			if retried.PreviousTargetCommit != promoted.TargetCommit {
				t.Fatalf("retried promotion = %#v, want it made from %q", retried, promoted.TargetCommit)
			}
			if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != retried.TargetCommit {
				t.Fatalf("main = %q after the retry, want %q", head, retried.TargetCommit)
			}

			// Nothing was lost and nothing was forced: main is the base, then the
			// commit that won the race, then the replayed one, in a straight line.
			history := strings.Fields(gitOutput(t, repository, "rev-list", "refs/heads/main"))
			if len(history) != 3 || history[0] != retried.TargetCommit || history[1] != promoted.TargetCommit || history[2] != base {
				t.Fatalf("main history = %v, want %q, %q, %q", history, retried.TargetCommit, promoted.TargetCommit, base)
			}
			listing := gitOutput(t, repository, "ls-tree", "--name-only", "-r", "refs/heads/main")
			for _, name := range []string{"winner.txt", "loser.txt"} {
				if !strings.Contains(listing, name) {
					t.Fatalf("main is missing %q: %q", name, listing)
				}
			}
		})
	}
}

// A replay that conflicts is the one integration outcome the harness must not
// resolve. It refuses, abandons the replay, and leaves the branch and the
// worktree exactly as they were, so the conflict reaches whoever owns it with
// both sides intact.
func TestManagerRebaseOntoTargetRefusesAConflictAndKeepsTheChange(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID: testRunID, WorkItemID: "yoyodyne-conflict", BaseRef: "main", TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "this run's answer\n")

	// The target moves onto the same line this change rewrote, which is what
	// makes the replay a decision rather than a mechanical replay.
	writeFile(t, repository, "README.txt", "somebody else's answer\n")
	runGit(t, repository, "add", "README.txt")
	runGit(t, repository, "commit", "-m", "conflicting target change")

	replay, err := manager.RebaseOntoTarget(context.Background(), worktree, "")
	if !errors.Is(err, ErrRebaseConflict) {
		t.Fatalf("RebaseOntoTarget() error = %v, want ErrRebaseConflict", err)
	}
	// The commit made of the developer's work is reported even though the replay
	// was refused: it is the HEAD every later inspection has to accept.
	if replay.HeadCommit == "" || replay.BaseCommit != worktree.BaseCommit || replay.Moved() {
		t.Fatalf("refused replay = %#v", replay)
	}
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head != replay.HeadCommit {
		t.Fatalf("worktree HEAD = %q, want the reported commit %q", head, replay.HeadCommit)
	}
	if branch := gitLine(t, worktree.Path, "rev-parse", "--abbrev-ref", "HEAD"); branch != worktree.Branch {
		t.Fatalf("worktree is not on its branch after the refusal: %q", branch)
	}
	if status := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Fatalf("worktree is dirty after the refusal: %q", status)
	}
	if content := readFile(t, worktree.Path, "README.txt"); content != "this run's answer\n" {
		t.Fatalf("the change was not preserved: %q", content)
	}
	if content := readFile(t, repository, "README.txt"); content != "somebody else's answer\n" {
		t.Fatalf("the target change was not preserved: %q", content)
	}
	// The refusal names what it stopped on and where the target had got to,
	// which is what the developer asked to reconcile it is handed.
	var conflict *RebaseConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("RebaseOntoTarget() error = %T, want the conflict's detail", err)
	}
	if target := gitLine(t, repository, "rev-parse", "refs/heads/main"); conflict.TargetCommit != target {
		t.Fatalf("conflict target commit = %q, want main at %q", conflict.TargetCommit, target)
	}
	if len(conflict.Paths) != 1 || conflict.Paths[0] != "README.txt" {
		t.Fatalf("conflict paths = %v, want README.txt", conflict.Paths)
	}
	worktree.HarnessCommit = replay.HeadCommit
	if _, err := manager.Integrate(context.Background(), worktree, ""); !errors.Is(err, ErrTargetDrift) {
		t.Fatalf("Integrate() after a refused replay error = %v, want ErrTargetDrift", err)
	}
}

// yoyodyne-ifd.132: a refused replay is answered by moving the change onto the
// target anyway, with the disagreement left in the worktree as Git's markers
// for the developer who wrote the change. Nothing is chosen: both answers are in
// the file, the target keeps every commit it had, and no Git operation is left
// in progress — only an ordinary dirty worktree on top of the target.
func TestManagerReplayForRepairLeavesTheConflictOnTopOfTheTarget(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID: testRunID, WorkItemID: "yoyodyne-conflict", BaseRef: "main", TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "this run's answer\n")
	writeFile(t, worktree.Path, "added.txt", "a file only this change adds\n")
	writeFile(t, repository, "README.txt", "somebody else's answer\n")
	runGit(t, repository, "add", "README.txt")
	runGit(t, repository, "commit", "-m", "conflicting target change")
	target := gitLine(t, repository, "rev-parse", "refs/heads/main")

	refused, err := manager.RebaseOntoTarget(context.Background(), worktree, "")
	if !errors.Is(err, ErrRebaseConflict) {
		t.Fatalf("RebaseOntoTarget() error = %v, want ErrRebaseConflict", err)
	}
	worktree.HarnessCommit = refused.HeadCommit

	moved, err := manager.ReplayForRepair(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("ReplayForRepair() error = %v", err)
	}
	if moved.BaseCommit != target || moved.HeadCommit != target || !moved.Moved() {
		t.Fatalf("moved = %#v, want the change based on the target at %q", moved, target)
	}
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head != target {
		t.Fatalf("worktree HEAD = %q, want the target %q", head, target)
	}
	if branch := gitLine(t, worktree.Path, "rev-parse", "--abbrev-ref", "HEAD"); branch != worktree.Branch {
		t.Fatalf("worktree is not on its branch after the move: %q", branch)
	}
	content := readFile(t, worktree.Path, "README.txt")
	for _, want := range []string{"<<<<<<<", "this run's answer", "somebody else's answer", ">>>>>>>"} {
		if !strings.Contains(content, want) {
			t.Fatalf("README.txt = %q, want both answers between conflict markers", content)
		}
	}
	// What merged cleanly is there too, and nothing is staged: the resolution is
	// the developer's to make, and no replay or apply is left in progress.
	if added := readFile(t, worktree.Path, "added.txt"); added != "a file only this change adds\n" {
		t.Fatalf("added.txt = %q, want the change's clean half applied", added)
	}
	if staged := gitOutput(t, worktree.Path, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "" {
		t.Fatalf("staged after the move = %q, want nothing staged", staged)
	}
	if unmerged := gitOutput(t, worktree.Path, "ls-files", "--unmerged"); strings.TrimSpace(unmerged) != "" {
		t.Fatalf("index still records a conflict: %q", unmerged)
	}
	for _, state := range []string{"CHERRY_PICK_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		location := gitLine(t, worktree.Path, "rev-parse", "--git-path", state)
		if !filepath.IsAbs(location) {
			location = filepath.Join(worktree.Path, location)
		}
		if _, err := os.Stat(location); err == nil {
			t.Fatalf("%s is left in the worktree's Git directory", state)
		}
	}
	if theirs := gitLine(t, repository, "show", "main:README.txt"); theirs != "somebody else's answer" {
		t.Fatalf("main README.txt = %q, want the target left exactly as it was", theirs)
	}

	// Once reconciled, the change is an ordinary commit above the target and
	// promotes by fast-forward.
	writeFile(t, worktree.Path, "README.txt", "both answers, reconciled\n")
	worktree.BaseCommit = moved.BaseCommit
	worktree.HarnessCommit = ""
	promoted, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() after reconciling error = %v", err)
	}
	if promoted.PreviousTargetCommit != target {
		t.Fatalf("promotion = %#v, want it made from %q", promoted, target)
	}
	if integrated := gitLine(t, repository, "show", "main:README.txt"); integrated != "both answers, reconciled" {
		t.Fatalf("main README.txt = %q, want the reconciliation", integrated)
	}
}

// A process that dies after the move put the worktree on the target and before
// the caller recorded it leaves a HEAD the ownership check refuses, with the
// recorded state still naming the change's own commit. Asking for the move again
// with that record recognises the state, puts the worktree back on the recorded
// change, and makes the move again — rather than stranding the run for a person.
// Both interrupted shapes are covered: the apply finished, and the apply never
// began after the reset onto the target.
func TestManagerReplayForRepairResumesAMoveNothingRecorded(t *testing.T) {
	t.Parallel()

	for _, interrupted := range []struct {
		name  string
		leave func(t *testing.T, manager *Manager, worktree Worktree, target string)
	}{
		{
			name: "after the apply",
			leave: func(t *testing.T, manager *Manager, worktree Worktree, target string) {
				if _, err := manager.ReplayForRepair(context.Background(), worktree, ""); err != nil {
					t.Fatalf("ReplayForRepair() error = %v", err)
				}
			},
		},
		{
			name: "between the reset and the apply",
			leave: func(t *testing.T, _ *Manager, worktree Worktree, target string) {
				runGit(t, worktree.Path, "reset", "--hard", "--quiet", target)
			},
		},
	} {
		t.Run(interrupted.name, func(t *testing.T) {
			t.Parallel()
			repository := newRepository(t)
			manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
			worktree, err := manager.Create(context.Background(), CreateRequest{
				RunID: testRunID, WorkItemID: "yoyodyne-conflict", BaseRef: "main", TargetBranch: "main",
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			writeFile(t, worktree.Path, "README.txt", "this run's answer\n")
			writeFile(t, repository, "README.txt", "somebody else's answer\n")
			runGit(t, repository, "add", "README.txt")
			runGit(t, repository, "commit", "-m", "conflicting target change")
			target := gitLine(t, repository, "rev-parse", "refs/heads/main")
			refused, err := manager.RebaseOntoTarget(context.Background(), worktree, "")
			if !errors.Is(err, ErrRebaseConflict) {
				t.Fatalf("RebaseOntoTarget() error = %v, want ErrRebaseConflict", err)
			}
			// The record the caller holds: the change's commit on the old base.
			worktree.HarnessCommit = refused.HeadCommit
			interrupted.leave(t, manager, worktree, target)

			moved, err := manager.ReplayForRepair(context.Background(), worktree, "")
			if err != nil {
				t.Fatalf("ReplayForRepair() over an unrecorded move error = %v", err)
			}
			if moved.BaseCommit != target || moved.HeadCommit != target {
				t.Fatalf("moved = %#v, want the change based on the target at %q", moved, target)
			}
			content := readFile(t, worktree.Path, "README.txt")
			if strings.Count(content, "<<<<<<<") != 1 || !strings.Contains(content, "this run's answer") || !strings.Contains(content, "somebody else's answer") {
				t.Fatalf("README.txt = %q, want the conflict applied once over the target", content)
			}
		})
	}
}

// A replay onto a target that never moved is a no-op rather than a failure: a
// promotion can lose to a held lock rather than to a commit, and that one is
// simply worth trying again.
func TestManagerRebaseOntoTargetIsANoOpWhenTheTargetHasNotMoved(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID: testRunID, WorkItemID: "yoyodyne-still", BaseRef: "main", TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")

	replay, err := manager.RebaseOntoTarget(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("RebaseOntoTarget() error = %v", err)
	}
	if replay.Moved() || replay.BaseCommit != worktree.BaseCommit || replay.HeadCommit == worktree.BaseCommit {
		t.Fatalf("RebaseOntoTarget() = %#v", replay)
	}
	worktree.HarnessCommit = replay.HeadCommit
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() after a no-op replay error = %v", err)
	}
	if integration.SourceCommit != replay.HeadCommit {
		t.Fatalf("promotion = %#v, want the commit the replay reported (%q)", integration, replay.HeadCommit)
	}
}

// heldRunner runs Git normally and holds the first command matching holdAfter
// open until the test releases it, so a second promotion can complete while the
// first is genuinely in flight.
type heldRunner struct {
	delegate  execution.ProcessRunner
	holdAfter func(arguments []string) bool
	holding   chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (r *heldRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	result, err := r.delegate.Run(ctx, command, observer)
	if r.holdAfter != nil && r.holdAfter(command.Args) {
		r.once.Do(func() {
			close(r.holding)
			<-r.release
		})
	}
	return result, err
}

// A rebase Git refuses outright is not a conflict. It exits exactly as a
// conflicted one does, and calling it a conflict would send somebody to settle
// a disagreement that does not exist — on a worktree where nothing was applied
// and nothing needs settling.
func TestManagerRebaseOntoTargetReportsARefusalThatIsNotAConflict(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	refusing := &refusingRebaseRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{Runner: refusing, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID: testRunID, WorkItemID: "yoyodyne-refused", BaseRef: "main", TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")
	writeFile(t, repository, "concurrent.txt", "somebody else's work\n")
	runGit(t, repository, "add", "concurrent.txt")
	runGit(t, repository, "commit", "-m", "concurrent target change")

	refusing.armed = true
	replay, err := manager.RebaseOntoTarget(context.Background(), worktree, "")
	if err == nil || !strings.Contains(err.Error(), "invalid upstream") {
		t.Fatalf("RebaseOntoTarget() error = %v, want the refusal Git reported", err)
	}
	if errors.Is(err, ErrRebaseConflict) {
		t.Fatalf("a rebase that never started was reported as a conflict: %v", err)
	}
	// The commit the harness made before the refusal is still reported, and the
	// worktree is exactly where that commit left it.
	if replay.HeadCommit == "" || replay.Moved() {
		t.Fatalf("refused replay = %#v", replay)
	}
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head != replay.HeadCommit {
		t.Fatalf("worktree HEAD = %q, want the reported commit %q", head, replay.HeadCommit)
	}
	if status := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Fatalf("worktree is dirty after the refusal: %q", status)
	}
	if state := gitLine(t, worktree.Path, "rev-parse", "--git-path", "rebase-merge"); dirExists(state) {
		t.Fatalf("a replay state directory survived a rebase that never started: %q", state)
	}
}

// refusingRebaseRunner makes `git rebase` refuse the way an unusable revision
// does: a non-zero exit with nothing half-applied behind it.
type refusingRebaseRunner struct {
	delegate execution.ProcessRunner
	armed    bool
}

func (r *refusingRebaseRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.armed && hasArgument(command.Args, "rebase") && !hasArgument(command.Args, "--abort") {
		return execution.ProcessResult{
			Status:   execution.ProcessFailed,
			ExitCode: 128,
			Stderr:   "fatal: invalid upstream\n",
		}, nil
	}
	return r.delegate.Run(ctx, command, observer)
}

// A rebase the harness killed part-way is not a conflict, however it was
// killed. It leaves the state directory a conflicted one does, and reading that
// as a conflict sent an operator to settle one that did not exist. The replay is
// abandoned, the worktree is put back on its branch, and the stop is reported as
// the harness's rather than the change's.
func TestManagerRebaseOntoTargetReportsAKilledReplayAsAStopNotAConflict(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		status  execution.ProcessStatus
		caller  bool
		stopped string
	}{
		{name: "timed out", status: execution.ProcessTimedOut, stopped: "timed out"},
		{name: "stalled", status: execution.ProcessStalled, stopped: "stalled"},
		{name: "cancelled by the caller", caller: true, stopped: "was cancelled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repository := newRepository(t)
			killing := &killingRebaseRunner{
				delegate: execution.OSProcessRunner{},
				marker:   filepath.Join(t.TempDir(), "picked"),
				status:   tt.status,
			}
			manager, err := New(Options{Runner: killing, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: testGitBudget})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			worktree, err := manager.Create(context.Background(), CreateRequest{
				RunID: testRunID, WorkItemID: "yoyodyne-killed", BaseRef: "main", TargetBranch: "main",
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			writeFile(t, worktree.Path, "work.txt", "developer work\n")
			writeFile(t, repository, "concurrent.txt", "somebody else's work\n")
			runGit(t, repository, "add", "concurrent.txt")
			runGit(t, repository, "commit", "-m", "concurrent target change")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.caller {
				killing.cancelCaller = cancel
			}
			killing.armed = true
			replay, err := manager.RebaseOntoTarget(ctx, worktree, "")
			if !killing.killed {
				t.Fatalf("the rebase was never killed part-way, so this test proved nothing: %v", err)
			}
			if !errors.Is(err, ErrReplayKilled) {
				t.Fatalf("RebaseOntoTarget() error = %v, want ErrReplayKilled", err)
			}
			if errors.Is(err, ErrRebaseConflict) {
				t.Fatalf("a killed replay was reported as a conflict: %v", err)
			}
			if !strings.Contains(err.Error(), tt.stopped) {
				t.Fatalf("RebaseOntoTarget() error = %v, want it to say the replay %s", err, tt.stopped)
			}
			if replay.HeadCommit == "" || replay.Moved() {
				t.Fatalf("killed replay = %#v", replay)
			}
			if state := gitLine(t, worktree.Path, "rev-parse", "--git-path", "rebase-merge"); dirExists(filepath.Join(worktree.Path, state)) || dirExists(state) {
				t.Fatalf("the killed replay's state directory survived: %q", state)
			}
			if branch := gitLine(t, worktree.Path, "rev-parse", "--abbrev-ref", "HEAD"); branch != worktree.Branch {
				t.Fatalf("worktree is not on its branch after the killed replay: %q", branch)
			}
			if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head != replay.HeadCommit {
				t.Fatalf("worktree HEAD = %q, want the reported commit %q", head, replay.HeadCommit)
			}
			if status := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); strings.TrimSpace(status) != "" {
				t.Fatalf("worktree is dirty after the killed replay: %q", status)
			}

			// Nothing about the change stopped it, so the same replay asked again
			// goes through.
			worktree.HarnessCommit = replay.HeadCommit
			retried, err := manager.RebaseOntoTarget(context.Background(), worktree, "")
			if err != nil {
				t.Fatalf("RebaseOntoTarget() after a killed replay error = %v", err)
			}
			if !retried.Moved() {
				t.Fatalf("RebaseOntoTarget() after a killed replay = %#v, want the change replayed", retried)
			}
		})
	}
}

// killingRebaseRunner kills the first `git rebase` part-way through: after it
// has applied the change and left its state directory, and while it is still
// running. The rebase is made to hold there by an --exec step that sleeps and
// marks the moment; the sleep ends by itself, so nothing outlives the
// test however it ends. The kill is the runner's own process context being
// cancelled, or the caller's when cancelCaller is set, and status is how a
// runner-side kill is reported: the process dies the same way whichever budget
// ended it.
type killingRebaseRunner struct {
	delegate     execution.ProcessRunner
	marker       string
	status       execution.ProcessStatus
	cancelCaller context.CancelFunc
	armed        bool
	killed       bool
}

func (r *killingRebaseRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if !r.armed || !hasArgument(command.Args, "rebase") || hasArgument(command.Args, "--abort") {
		return r.delegate.Run(ctx, command, observer)
	}
	r.armed = false
	arguments := make([]string, 0, len(command.Args)+2)
	for _, argument := range command.Args {
		arguments = append(arguments, argument)
		if argument == "rebase" {
			// The sleep is started before the mark is made, so the kill never lands
			// while the shell is still forking it.
			arguments = append(arguments, "--exec", "sleep 30 & touch '"+r.marker+"'; wait")
		}
	}
	command.Args = arguments
	running, stop := context.WithCancel(ctx)
	defer stop()
	kill := stop
	if r.cancelCaller != nil {
		kill = r.cancelCaller
	}
	go func() {
		for {
			if _, err := os.Stat(r.marker); err == nil {
				kill()
				return
			}
			select {
			case <-running.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	result, err := r.delegate.Run(running, command, observer)
	if err != nil {
		return result, err
	}
	if _, statErr := os.Stat(r.marker); statErr == nil && result.Status == execution.ProcessCancelled {
		r.killed = true
		if r.cancelCaller == nil {
			result.Status = r.status
		}
	}
	return result, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Survives is the narrow question the hold derivation asks of every stopped
// run: is the branch there, is the checkout there. It answers from the
// repository and the disk rather than from anything recorded, through the whole
// life of the artifacts — both there, the checkout retired and the branch
// standing, both gone — and it proves ownership first, so it never answers for
// a directory or a branch that is not the run's.
func TestManagerSurvivesReportsWhatIsActuallyThere(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-survives",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	both, err := manager.Survives(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Survives() fresh error = %v", err)
	}
	if !both.BranchExists || !both.WorktreePresent || !both.Any() {
		t.Fatalf("fresh survival = %#v, want the branch and the checkout both there", both)
	}

	// The checkout retired and the branch left standing, which is what the
	// convergence sweep leaves of a stopped run past the tail: the work is still
	// somebody's to pick up, on the branch.
	if _, err := manager.RemovePreservedWorktree(context.Background(), worktree, CaptureUncommittedWork); err != nil {
		t.Fatalf("RemovePreservedWorktree() error = %v", err)
	}
	branchOnly, err := manager.Survives(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Survives() after the checkout went error = %v", err)
	}
	if !branchOnly.BranchExists || branchOnly.WorktreePresent || !branchOnly.Any() {
		t.Fatalf("survival after the checkout went = %#v, want the branch alone", branchOnly)
	}

	if _, err := manager.RemoveMergedBranch(context.Background(), worktree.Branch, worktree.TargetBranch); err != nil {
		t.Fatalf("RemoveMergedBranch() error = %v", err)
	}
	gone, err := manager.Survives(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Survives() after the branch went error = %v", err)
	}
	if gone.Any() {
		t.Fatalf("survival after both went = %#v, want nothing", gone)
	}

	// A record whose identifiers do not derive the path is refused rather than
	// answered, because an answer about some other run's directory is worse than
	// none.
	foreign := worktree
	foreign.Branch = "yoyodyne/somebody-else/" + testRunID
	if _, err := manager.Survives(context.Background(), foreign); err == nil {
		t.Fatal("Survives() answered for a branch the run does not own")
	}
}

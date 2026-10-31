package gitworktree

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

const testRunID = "run-0123456789abcdef0123456789abcdef"

func TestManagerCreateInspectPreserveAndCleanup(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	manager := newManager(t, repository, worktreeRoot)
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1.2", BaseRef: "HEAD", TargetBranch: "main"}

	worktree, err := manager.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.Branch != "yoyodyne/yoyodyne-1-2/01234567" {
		t.Fatalf("branch = %q", worktree.Branch)
	}
	if !commitPattern.MatchString(worktree.BaseCommit) {
		t.Fatalf("base commit = %q", worktree.BaseCommit)
	}
	inspection, err := manager.Inspect(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !inspection.Registered || inspection.Dirty || inspection.Branch != worktree.Branch {
		t.Fatalf("Inspect() = %#v", inspection)
	}

	// Cleanup only ever acts on a worktree that really integrated, so this
	// promotes one first rather than passing the base commit off as an
	// integrated one: the base is in the target by construction and would make
	// the containment proof vacuous.
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}

	dirtyPath := filepath.Join(worktree.Path, "failure.txt")
	if err := os.WriteFile(dirtyPath, []byte("preserve me"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cleanupRequest := CleanupRequest{Worktree: worktree, TargetBranch: "main", SourceCommit: integration.SourceCommit}
	if _, err := manager.CleanupIntegrated(context.Background(), cleanupRequest); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("CleanupIntegrated() dirty error = %v", err)
	}
	if _, err := os.Stat(dirtyPath); err != nil {
		t.Fatalf("dirty worktree was not preserved: %v", err)
	}
	if err := os.Remove(dirtyPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	cleanup, err := manager.CleanupIntegrated(context.Background(), cleanupRequest)
	if err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	if !cleanup.Complete() {
		t.Fatalf("CleanupIntegrated() = %#v, want both artifacts removed", cleanup)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestManagerCreatesWorktreeFromResolvedBaseCommit(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-base", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// The run branch is what the worktree is cut on, so the commit it starts
	// from is the commit the worktree starts from.
	var branchBase string
	for _, command := range runner.commands {
		for index := 0; index+2 < len(command); index++ {
			if command[index] == "branch" && command[index+1] == worktree.Branch {
				branchBase = command[len(command)-1]
			}
		}
	}
	if branchBase != worktree.BaseCommit {
		t.Fatalf("git branch base = %q, want resolved commit %q", branchBase, worktree.BaseCommit)
	}
}

// A developer's first act is to execute the project's checks in the worktree it
// was handed, so a worktree that is still being filled when the run starts is a
// probe that judges half a tree and reports a broken toolchain. Create is what
// stops that: it returns only once every file the base commit carries is on disk
// with the content it was committed with, and a run is invoked after it returns.
// A developer on 2026-09-01 read the worktree's uniform creation-time timestamps
// as a checkout still in flight, which is what this pins as not being one.
func TestCreateReturnsOnlyAWorktreeThatIsWhole(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	committed := map[string]string{
		"README.txt":             "test\n",
		"one.txt":                "the first file\n",
		"nested/two.txt":         "the second file\n",
		"nested/deeper/three.go": "package deeper\n\nfunc Three() int { return 3 }\n",
	}
	for relative, content := range committed {
		if relative == "README.txt" {
			continue
		}
		writeFile(t, repository, relative, content)
	}
	runGit(t, repository, "add", "--all")
	runGit(t, repository, "commit", "-m", "content to materialize")

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-whole", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	for relative, content := range committed {
		found, err := os.ReadFile(filepath.Join(worktree.Path, relative))
		if err != nil {
			t.Fatalf("Create() returned before %s was written: %v", relative, err)
		}
		if string(found) != content {
			t.Fatalf("%s = %q, want the committed %q", relative, found, content)
		}
	}
	if status := gitOutput(t, worktree.Path, "status", "--porcelain", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Fatalf("created worktree is not quiescent: %s", status)
	}
}

func TestManagerReturnsCreatedIdentityWhenPostCreateInspectionFails(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &postCreateFailureRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-partial", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "verify created worktree") {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.Path == "" || worktree.Branch == "" || worktree.BaseCommit == "" {
		t.Fatalf("Create() discarded created identity: %#v", worktree)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("created worktree is not recoverable at %s: %v", worktree.Path, err)
	}
}

func TestManagerRejectsReuseAndDirtyPrimaryRepository(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1", BaseRef: "HEAD"}
	if _, err := manager.Create(context.Background(), request); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), request); err == nil || (!strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "uncommitted")) {
		t.Fatalf("second Create() error = %v", err)
	}

	repository = newRepository(t)
	manager = newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	if err := os.WriteFile(filepath.Join(repository, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), request); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("Create() dirty error = %v", err)
	}
	var dirty PrimaryDirtyError
	if err := manager.ValidateReady(context.Background()); !errors.As(err, &dirty) || !errors.Is(err, ErrPrimaryNotReady) {
		t.Fatalf("ValidateReady() error = %v, want the dirty paths and the environmental sentinel", err)
	}
	if len(dirty.Paths) != 1 || dirty.Paths[0] != "dirty.txt" {
		t.Fatalf("dirty paths = %v, want dirty.txt", dirty.Paths)
	}
}

// Development is parallel, so several runs can be given a worktree at the same
// instant — which is this repository's own configuration. Git's bookkeeping
// under .git/worktrees has no lock of its own: an add registers the new entry
// and then fills it in, so two adds at that instant can have one read the
// other's half-written entry and exit outright, losing a run to nothing but
// timing. Creation queues now, and every run that asked for a worktree gets one.
//
// The overlap is what this asserts rather than only the eight successes. Git's
// own window is a few microseconds wide and a machine that happens not to hit it
// would pass this test with nothing serializing anything at all, which is
// precisely the reassurance that let the race reach production in the first
// place.
func TestManagerCreatesConcurrentWorktreesWithoutLosingAny(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &creationWitness{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const concurrent = 8
	// Every creation is released at once rather than one happening to be finished
	// before the next starts, because the window this is about is only open while
	// an add is in flight.
	start := make(chan struct{})
	worktrees := make([]Worktree, concurrent)
	failures := make([]error, concurrent)
	var creating sync.WaitGroup
	for index := range concurrent {
		creating.Add(1)
		go func() {
			defer creating.Done()
			<-start
			worktrees[index], failures[index] = manager.Create(context.Background(), CreateRequest{
				RunID:      fmt.Sprintf("run-%08x%s", index, strings.Repeat("0", 24)),
				WorkItemID: fmt.Sprintf("yoyodyne-concurrent-%d", index),
				BaseRef:    "HEAD",
			})
		}()
	}
	close(start)
	creating.Wait()

	registered := gitOutput(t, repository, "worktree", "list", "--porcelain")
	for index := range concurrent {
		if failures[index] != nil {
			t.Fatalf("Create(%d) error = %v", index, failures[index])
		}
		if !strings.Contains(registered, worktrees[index].Path) {
			t.Fatalf("worktree %d at %s is not registered:\n%s", index, worktrees[index].Path, registered)
		}
	}
	adds, overlaps := runner.observed()
	if adds != concurrent {
		t.Fatalf("git worktree add ran %d time(s), want one per creation", adds)
	}
	if overlaps != 0 {
		t.Fatalf("%d worktree creation(s) started while another was still in flight", overlaps)
	}
}

// The `-c` options above fence the Git commands this package composes and
// nothing else. Every worktree the harness cuts shares the repository's common
// Git directory, so a Git command an agent or a project's build tooling runs
// inside one hands the same repository to the same automatic maintenance — and a
// prune reaching a registration `git worktree add` has not finished writing
// deletes it out from under the add, losing the run to nothing but timing.
//
// So the neighbour here is a Git command nobody in this package composed,
// launched the way the harness launches an agent and a check, doing the writes
// that Git follows with maintenance, in a repository whose own config asks for
// it — while eight creations are in flight. What holds it off is the fence in
// the launched process's environment, which is asserted directly as well: the
// prune's window is microseconds wide, so a machine that happens not to hit it
// would otherwise pass this test with nothing fencing anything at all.
func TestConcurrentCreationSurvivesAGitCommandTheHarnessDidNotCompose(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	// Restored before the cleanup newRepository registered, which runs Git
	// commands of its own outside the fence.
	t.Cleanup(func() { disableBackgroundMaintenance(t, repository) })
	runGit(t, repository, "config", "gc.auto", "1")
	runGit(t, repository, "config", "maintenance.auto", "true")
	// The worktree-prune task prunes registrations without gc, and a newer Git
	// runs it under "maintenance run --auto" by default; it is switched on here
	// so the test asks for it on every Git that has it.
	runGit(t, repository, "config", "maintenance.worktree-prune.enabled", "true")
	runGit(t, repository, "config", "maintenance.worktree-prune.auto", "1")

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	if value := neighbourGitConfig(t, repository, "maintenance.worktree-prune.auto"); value != "0" {
		t.Fatalf("a neighbouring Git command reads maintenance.worktree-prune.auto = %q, want the harness's fence at %q", value, "0")
	}
	if value := neighbourGitConfig(t, repository, "gc.auto"); value != "0" {
		t.Fatalf("a neighbouring Git command reads gc.auto = %q, want the harness's fence at %q", value, "0")
	}
	if value := neighbourGitConfig(t, repository, "maintenance.auto"); value != "false" {
		t.Fatalf("a neighbouring Git command reads maintenance.auto = %q, want the harness's fence at %q", value, "false")
	}

	const concurrent = 8
	start := make(chan struct{})
	worktrees := make([]Worktree, concurrent)
	failures := make([]error, concurrent)
	var running sync.WaitGroup
	for index := range concurrent {
		running.Add(1)
		go func() {
			defer running.Done()
			<-start
			worktrees[index], failures[index] = manager.Create(context.Background(), CreateRequest{
				RunID:      fmt.Sprintf("run-%08x%s", index, strings.Repeat("0", 24)),
				WorkItemID: fmt.Sprintf("yoyodyne-neighboured-%d", index),
				BaseRef:    "HEAD",
			})
		}()
	}
	running.Add(1)
	go func() {
		defer running.Done()
		<-start
		// The maintenance a write command asks for, asked for directly. It is
		// the same two settings being read by the same tasks, and it takes none
		// of the locks a write would, so what this test observes is the prune
		// rather than two commands queueing over an index.
		for round := range concurrent {
			for _, args := range [][]string{
				{"-C", repository, "gc", "--auto"},
				{"-C", repository, "maintenance", "run", "--auto"},
			} {
				result, err := execution.OSProcessRunner{}.Run(context.Background(), execution.Command{
					Name:    "git",
					Args:    args,
					Env:     unfencedEnvironment(),
					Timeout: testGitBudget,
				}, nil)
				if err != nil || result.Status != execution.ProcessSucceeded {
					t.Errorf("the neighbouring git %v in round %d = %v (%v): %s", args, round, result.Status, err, result.Stderr)
					return
				}
			}
		}
	}()
	close(start)
	running.Wait()

	registered := gitOutput(t, repository, "worktree", "list", "--porcelain")
	for index := range concurrent {
		if failures[index] != nil {
			t.Fatalf("Create(%d) error = %v", index, failures[index])
		}
		if !strings.Contains(registered, worktrees[index].Path) {
			t.Fatalf("worktree %d at %s is not registered:\n%s", index, worktrees[index].Path, registered)
		}
	}
}

// neighbourGitConfig asks the repository for one setting from inside a process
// the harness launched, which is where the fence exists and the only place the
// answer means anything: the repository's own config says the opposite.
func neighbourGitConfig(t *testing.T, repository, setting string) string {
	t.Helper()

	result, err := execution.OSProcessRunner{}.Run(context.Background(), execution.Command{
		Name:    "git",
		Args:    []string{"-C", repository, "config", "--get", setting},
		Env:     unfencedEnvironment(),
		Timeout: testGitBudget,
	}, nil)
	if err != nil || result.Status != execution.ProcessSucceeded {
		t.Fatalf("git config --get %s = %v (%v): %s", setting, result.Status, err, result.Stderr)
	}
	return strings.TrimSpace(result.Stdout)
}

// The Git command that loses a run is not one the harness launched directly: it
// is a grandchild, started by an agent or a check whose working directory is a
// worktree this package cut, and it reads the repository's config through that
// worktree's link to the common Git directory. So the child here is a shell the
// runner starts inside a created worktree, and the Git command is the shell's own.
func TestAGitCommandAChildRunsInsideAWorktreeSeesMaintenanceOff(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	t.Cleanup(func() { disableBackgroundMaintenance(t, repository) })
	runGit(t, repository, "config", "gc.auto", "1")
	runGit(t, repository, "config", "maintenance.auto", "true")

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      "run-" + strings.Repeat("7", 32),
		WorkItemID: "yoyodyne-fenced-child",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := execution.OSProcessRunner{}.Run(context.Background(), execution.Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", "git rev-parse --git-common-dir && git config --get gc.auto && git config --get maintenance.auto"},
		Dir:     worktree.Path,
		Env:     unfencedEnvironment(),
		Timeout: testGitBudget,
	}, nil)
	if err != nil || result.Status != execution.ProcessSucceeded {
		t.Fatalf("the child's git commands = %v (%v): %s", result.Status, err, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("the child printed %q, want the common directory and two settings", result.Stdout)
	}
	// Proof the child's Git was addressing the shared repository through the
	// worktree rather than some repository of its own.
	if common := lines[0]; !filepath.IsAbs(common) || !strings.HasPrefix(evalPath(t, common), evalPath(t, filepath.Join(repository, ".git"))) {
		t.Fatalf("the child's common Git directory is %q, want the repository's own under %s", common, repository)
	}
	if lines[1] != "0" {
		t.Errorf("gc.auto = %q inside the worktree, want the fence's %q rather than the repository's own", lines[1], "0")
	}
	if lines[2] != "false" {
		t.Errorf("maintenance.auto = %q inside the worktree, want the fence's %q rather than the repository's own", lines[2], "false")
	}
}

// unfencedEnvironment is this process's environment with no Git configuration in
// it, for a child whose fence has to come from the runner. A test the harness
// itself launched already carries the fence, and a child inheriting that would
// pass these tests with the runner fencing nothing.
func unfencedEnvironment() []string {
	var environment []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_CONFIG_COUNT=") && !strings.HasPrefix(entry, "GIT_CONFIG_KEY_") && !strings.HasPrefix(entry, "GIT_CONFIG_VALUE_") {
			environment = append(environment, entry)
		}
	}
	return environment
}

// evalPath resolves symlinks, so a temporary directory reached through
// /var and through /private/var compares as the one directory it is.
func evalPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

// What two runs race over is the repository's bookkeeping rather than the
// configuration that points at it, so creations queue across managers and not
// only within one. Two products aimed at one repository have separate worktree
// roots and one worktrees/ directory between them, and two `yoyo run`
// invocations have separate processes and the same directory; a queue held in a
// manager would serialize neither.
func TestManagerCreationQueuesAcrossManagersOnOneRepository(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &creationWitness{delegate: execution.OSProcessRunner{}}
	const managers, each = 2, 3
	start := make(chan struct{})
	var creating sync.WaitGroup
	failures := make([]error, managers*each)
	for group := range managers {
		manager, err := New(Options{
			Runner:         runner,
			RepositoryRoot: repository,
			WorktreeRoot:   filepath.Join(t.TempDir(), fmt.Sprintf("worktrees-%d", group)),
			Timeout:        testGitBudget,
		})
		if err != nil {
			t.Fatalf("New(%d) error = %v", group, err)
		}
		for index := range each {
			creating.Add(1)
			go func() {
				defer creating.Done()
				<-start
				_, failures[group*each+index] = manager.Create(context.Background(), CreateRequest{
					RunID:      fmt.Sprintf("run-%08x%s", group*each+index, strings.Repeat("0", 24)),
					WorkItemID: fmt.Sprintf("yoyodyne-shared-%d-%d", group, index),
					BaseRef:    "HEAD",
				})
			}()
		}
	}
	close(start)
	creating.Wait()

	for index, failure := range failures {
		if failure != nil {
			t.Fatalf("Create(%d) error = %v", index, failure)
		}
	}
	adds, overlaps := runner.observed()
	if adds != managers*each {
		t.Fatalf("git worktree add ran %d time(s), want one per creation", adds)
	}
	if overlaps != 0 {
		t.Fatalf("%d worktree creation(s) started while another manager's was still in flight", overlaps)
	}
}

// creationWitness records what is true at the moment a worktree is registered:
// whether another registration is already in flight. It watches the Git command
// itself rather than the manager's method, because the bookkeeping two runs
// race over is written by that command and by nothing else.
type creationWitness struct {
	delegate execution.ProcessRunner
	mu       sync.Mutex
	adding   int
	adds     int
	overlaps int
}

func (w *creationWitness) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if !containsArguments(command.Args, "worktree", "add") {
		return w.delegate.Run(ctx, command, observer)
	}
	w.started()
	defer w.finished()
	return w.delegate.Run(ctx, command, observer)
}

func (w *creationWitness) started() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.adds++
	if w.adding > 0 {
		w.overlaps++
	}
	w.adding++
}

func (w *creationWitness) finished() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.adding--
}

func (w *creationWitness) observed() (adds, overlaps int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.adds, w.overlaps
}

// The creation lease serializes writers against each other, and nothing
// serializes a reader against them: a creation holds the lease while it
// verifies what it made, which is itself a listing, so a listing that waited
// for the lease would wait for itself. That leaves `git worktree list` able to
// cross an entry another run has registered and not yet filled in, where it
// fails the whole command rather than skipping the one entry — observed as
// `failed to read .git/worktrees/<other run>/commondir` in a run doing nothing
// but summarizing its own change. So the listing is read again, and a run is
// not lost to an instant that has already passed.
func TestManagerReadsTheWorktreeListingAgainWhenItCrossesACreation(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: 1}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-crossed",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want the half-written listing to have been read again", err)
	}
	if listings, refused := runner.observed(); refused != 1 || listings < 2 {
		t.Fatalf("listings = %d after %d refusal(s), want the refusal to have been followed by another read", listings, refused)
	}
	if _, err := manager.Inspect(context.Background(), worktree); err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
}

// The re-run covers the instant a creation is half-written and nothing else.
// An add whose process died between creating one of the entry's files and
// writing it leaves the entry that way for good — which is what the run-scoped
// reaping produces when it kills the group an agent's own `git worktree add` is
// in — and `git worktree prune` judges an entry by its gitdir file, so it leaves
// that one alone. Every listing on the repository then fails over it, and every
// run on the repository is lost to a neighbour that started and died. A killed
// add is cleared where nothing can still be writing it — see
// TestManagerCreatesAcrossARegistrationAKilledAddLeftBehind — and what this
// covers is a listing that meets an unfinished entry before that has happened,
// or the one shape that is never cleared: an entry that has an index, and so
// was checked out by an add that did finish. That entry is stepped over
// instead: the listing describes the repository without it, and says so.
func TestManagerListsAroundARegistrationAnotherRunNeverFinishedWriting(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	var notes []string
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Note:           func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) },
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	mine, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-beside",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	neighbour := halfWriteRegistration(t, repository, "yoyodyne-other-2113a23c")

	// The failure being tolerated is Git's own rather than a described one: if a
	// later Git stops refusing over this entry, this test is asserting nothing and
	// must be told so rather than passing quietly.
	if listing, err := attemptGit(repository, "worktree", "list", "--porcelain"); err == nil {
		t.Fatalf("git described the repository with a half-written registration present, so the failure this tolerates is gone:\n%s", listing)
	}

	entries, err := manager.listWorktrees(context.Background())
	if err != nil {
		t.Fatalf("listWorktrees() error = %v, want the unfinished registration to have been stepped over", err)
	}
	// Compared the way this package compares checkout paths, because a listing
	// names the path Git resolved rather than the one a test happened to write.
	described := func(path string) (string, bool) {
		for _, entry := range entries {
			if samePath(entry.path, path) {
				return entry.branch, true
			}
		}
		return "", false
	}
	if branch, listed := described(mine.Path); !listed || branch != mine.Branch {
		t.Fatalf("listWorktrees() = %v, want %s on %s", entries, mine.Path, mine.Branch)
	}
	if branch, listed := described(repository); !listed || branch != "main" {
		t.Fatalf("listWorktrees() = %v, want the primary checkout at %s on main", entries, repository)
	}
	if _, listed := described(neighbour); listed {
		t.Fatalf("listWorktrees() = %v, want the unfinished registration at %s left out", entries, neighbour)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], filepath.Base(neighbour)) {
		t.Fatalf("notes = %v, want one naming the registration that was left out", notes)
	}

	// The run this is really about is one doing nothing but reading its own
	// checkout while a neighbour starts beside it.
	if _, err := manager.Inspect(context.Background(), mine); err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
}

// halfWriteRegistration leaves the repository holding the entry `git worktree
// add` has registered and not yet filled in: the files are created one at a
// time, so one of them exists and is empty. commondir is the one Git refuses the
// whole listing over. The entry is finished again before the test's own cleanup
// reads the repository, because that cleanup lists the worktrees too.
func halfWriteRegistration(t *testing.T, repository, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	runGit(t, repository, "worktree", "add", "--quiet", "-b", name, path)
	commondir := filepath.Join(repository, ".git", "worktrees", name, "commondir")
	written, err := os.ReadFile(commondir)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", commondir, err)
	}
	if err := os.WriteFile(commondir, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", commondir, err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(commondir, written, 0o600); err != nil {
			t.Errorf("cleanup could not finish the registration at %s: %v", commondir, err)
		}
	})
	return path
}

// A listing that keeps failing is still a failure, reported with what Git said.
// The retry is for the instant that passes; a repository Git cannot describe at
// all must not be waited on forever or reported as empty. The bookkeeping is
// what decides between the two: this repository holds no unfinished
// registration, so nothing accounts for the refusal and it stands.
func TestManagerReportsAWorktreeListingThatKeepsFailing(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: registrationWalkAttempts}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-unreadable",
		BaseRef:    "HEAD",
	})
	if err == nil {
		t.Fatal("Create() succeeded with a listing Git never answered")
	}
	if !strings.Contains(err.Error(), "list worktrees failed with exit code 128") || !strings.Contains(err.Error(), "commondir") {
		t.Fatalf("Create() error = %v, want it to carry what Git said", err)
	}
	if listings, _ := runner.observed(); listings != registrationWalkAttempts {
		t.Fatalf("listings = %d, want %d attempts and no more", listings, registrationWalkAttempts)
	}
}

// A listing that timed out is not the passing instant, and it has already spent
// the command's whole budget. Reading it again would make a slow repository
// three times slower to fail, so the retry is for a Git that ran and refused
// and for nothing else.
func TestManagerDoesNotReadAWorktreeListingAgainAfterATimeout(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: 1, status: execution.ProcessTimedOut}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-slow",
		BaseRef:    "HEAD",
	})
	if err == nil {
		t.Fatal("Create() succeeded with a listing that never answered")
	}
	if listings, _ := runner.observed(); listings != 1 {
		t.Fatalf("listings = %d, want the timeout to have been believed the first time", listings)
	}
}

// listRefusingRunner fails the first refusals listings the way Git fails one
// that crossed a creation: the whole command exits 128 over a single entry it
// could not read. status is what the refusal is reported as, so a refusal and a
// listing that never answered can be told apart; stderr is what Git is made to
// say, and defaults to the crossing. command names the Git command refused, and
// defaults to the listing. Everything else runs for real, so what is being
// tested is the manager's own reading of the bookkeeping rather than a
// simulation of Git.
type listRefusingRunner struct {
	delegate execution.ProcessRunner
	status   execution.ProcessStatus
	stderr   string
	command  []string
	mu       sync.Mutex
	refusals int
	refused  int
	listings int
}

func (r *listRefusingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	refusable := r.command
	if refusable == nil {
		refusable = []string{"worktree", "list"}
	}
	if !containsArguments(command.Args, refusable[0], refusable[1]) {
		return r.delegate.Run(ctx, command, observer)
	}
	r.mu.Lock()
	r.listings++
	refuse := r.refused < r.refusals
	if refuse {
		r.refused++
	}
	r.mu.Unlock()
	if refuse {
		status := r.status
		if status == "" {
			status = execution.ProcessFailed
		}
		stderr := r.stderr
		if stderr == "" {
			stderr = "fatal: failed to read .git/worktrees/yoyodyne-other-2113a23c/commondir: Result too large\n"
		}
		return execution.ProcessResult{
			Status:   status,
			ExitCode: 128,
			Stderr:   stderr,
		}, nil
	}
	return r.delegate.Run(ctx, command, observer)
}

func (r *listRefusingRunner) observed() (listings, refused int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listings, r.refused
}

// The listing is not the only command Git walks the registrations from. A
// rebase, a checkout, and a branch deletion each check that a branch is not
// checked out elsewhere, and each of them dies over a half-written entry with
// the same words the listing does — a rebase was seen failing that way in a
// concurrent-runs test. So the tolerance lives under every Git command the
// manager runs rather than around the one that describes the repository.
func TestManagerRunsAnyGitCommandAgainWhenItCrossesACreation(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: 1, command: []string{"rev-parse", "--verify"}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-crossed-elsewhere",
		BaseRef:    "HEAD",
	}); err != nil {
		t.Fatalf("Create() error = %v, want the command that crossed a creation to have been run again", err)
	}
	if runs, refused := runner.observed(); refused != 1 || runs < 2 {
		t.Fatalf("runs = %d after %d refusal(s), want the refusal to have been followed by another run", runs, refused)
	}
}

// A removal crosses a walk as surely as a creation does, and Git says so in
// words of its own: the entry's lock marker gone between being seen and being
// read, or the entry itself gone between its commondir being read and the
// common directory being resolved through it. The second is what failed
// TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn over its own
// creation loop's removals. Both pass with the instant, so both are run again.
func TestManagerRunsAnyGitCommandAgainWhenItCrossesARemoval(t *testing.T) {
	t.Parallel()

	for name, stderr := range map[string]string{
		"the lock marker":  "fatal: failed to read '.git/worktrees/creation-loop-27/locked': No such file or directory\n",
		"the entry itself": "fatal: Invalid path '/private/var/folders/tmp/repository/.git/worktrees/creation-loop-27': No such file or directory\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repository := newRepository(t)
			runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: 1, command: []string{"rev-parse", "--verify"}, stderr: stderr}
			manager, err := New(Options{
				Runner:         runner,
				RepositoryRoot: repository,
				WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
				Timeout:        testGitBudget,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, err := manager.Create(context.Background(), CreateRequest{
				RunID:      testRunID,
				WorkItemID: "yoyodyne-crossed-removal",
				BaseRef:    "HEAD",
			}); err != nil {
				t.Fatalf("Create() error = %v, want the command that crossed a removal to have been run again", err)
			}
			if runs, refused := runner.observed(); refused != 1 || runs < 2 {
				t.Fatalf("runs = %d after %d refusal(s), want the refusal to have been followed by another run", runs, refused)
			}
		})
	}
}

// A creation is what a neighbour's cleanup costs most: the add is the step a run
// cannot start without, and a removal crossing it fails the run before its
// developer is ever asked for anything. Git says so in each of the removal's
// forms from inside the add itself — the entry it walks gone under it, or
// worktrees/ gone between the add making it and making its entry there, when the
// removal took the last entry and Git deleted the directory with it. Each is run
// again rather than failing the creation, and each is said where the caller asked
// to hear about crossings, naming the add, the attempt Git refused, and Git's own
// words, so a crossing absorbed is still one somebody can read.
func TestManagerRunsAWorktreeAddAgainWhenItCrossesARemoval(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ stderr, refusal string }{
		"the entry it walks": {
			stderr:  "Preparing worktree (checking out 'yoyodyne/yoyodyne-crossed-add/run')\nfatal: Invalid path '.git/worktrees/yoyodyne-ifd-12-0b1434ad': No such file or directory\n",
			refusal: "Invalid path '.git/worktrees/yoyodyne-ifd-12-0b1434ad'",
		},
		"the directory it makes its entry in": {
			stderr:  "fatal: could not create directory of '.git/worktrees/run-0b1434ad': No such file or directory\n",
			refusal: "could not create directory of '.git/worktrees/run-0b1434ad': No such file or directory",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repository := newRepository(t)
			runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: 1, command: []string{"worktree", "add"}, stderr: tc.stderr}
			manager, err := New(Options{
				Runner:         runner,
				RepositoryRoot: repository,
				WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
				Timeout:        testGitBudget,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			var (
				mu        sync.Mutex
				crossings []Crossing
			)
			ctx := WithCrossings(context.Background(), func(crossing Crossing) {
				mu.Lock()
				defer mu.Unlock()
				crossings = append(crossings, crossing)
			})
			worktree, err := manager.Create(ctx, CreateRequest{
				RunID:      testRunID,
				WorkItemID: "yoyodyne-crossed-add",
				BaseRef:    "HEAD",
			})
			if err != nil {
				t.Fatalf("Create() error = %v, want the add that crossed a removal to have been run again", err)
			}
			if adds, refused := runner.observed(); refused != 1 || adds != 2 {
				t.Fatalf("adds = %d after %d refusal(s), want the one refusal followed by one more add", adds, refused)
			}
			if inspection, err := manager.Inspect(context.Background(), worktree); err != nil || !inspection.Registered {
				t.Fatalf("Inspect() = %#v, %v, want the worktree the second add made", inspection, err)
			}
			mu.Lock()
			defer mu.Unlock()
			want := Crossing{Command: "git worktree add", Attempt: 1, Attempts: registrationWalkAttempts, Refusal: tc.refusal}
			if len(crossings) != 1 || crossings[0] != want {
				t.Fatalf("crossings said = %#v, want exactly %#v", crossings, want)
			}
		})
	}
}

// A crossing is said only where somebody asked to hear it, and a refusal that is
// not one is said nowhere: it is Git's answer and is believed, not run again.
func TestManagerSaysNoCrossingForARefusalItDoesNotRunAgain(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{
		delegate: execution.OSProcessRunner{},
		refusals: 1,
		command:  []string{"worktree", "add"},
		stderr:   "fatal: could not create directory of '.git/worktrees/run-0b1434ad': Permission denied\n",
	}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var crossings []Crossing
	ctx := WithCrossings(context.Background(), func(crossing Crossing) { crossings = append(crossings, crossing) })
	if _, err := manager.Create(ctx, CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-refused-add", BaseRef: "HEAD"}); err == nil {
		t.Fatal("Create() succeeded over an add Git refused for a reason that does not pass")
	}
	if adds, _ := runner.observed(); adds != 1 {
		t.Fatalf("adds = %d, want the refusal believed the first time", adds)
	}
	if len(crossings) != 0 {
		t.Fatalf("crossings said = %#v, want none for a refusal that was not run again", crossings)
	}
}

// The pattern is Git's wording and nothing wider: a refusal naming a file or a
// path that is not one entry of the registrations is Git's answer about
// something else, and running it again would only make it slower.
func TestACrossedRegistrationIsGitsWordingForAnEntryAndNothingElse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		stderr  string
		crossed bool
	}{
		{"fatal: failed to read .git/worktrees/yoyodyne-other-2113a23c/commondir: Result too large", true},
		{"fatal: failed to read '.git/worktrees/loop-3/locked': No such file or directory", true},
		{"fatal: Invalid path '/tmp/repository/.git/worktrees/creation-loop-27': No such file or directory", true},
		{`fatal: Invalid path 'C:\repository\.git\worktrees\creation-loop-27': No such file or directory`, true},
		{"fatal: Invalid path '/srv/repository.git/worktrees/creation-loop-27': No such file or directory", true},
		// A repository's own path can hold spaces, and none of the three forms
		// may stop matching over one.
		{"fatal: failed to read /Users/me/Application Support/My Repos/x/.git/worktrees/loop-3/commondir: Result too large", true},
		{"fatal: failed to read '/Users/me/Application Support/My Repos/x/.git/worktrees/loop-3/locked': No such file or directory", true},
		{"fatal: Invalid path '/Users/me/Application Support/My Repos/x/.git/worktrees/loop-3': No such file or directory", true},
		{"fatal: could not create directory of '.git/worktrees/yoyodyne-ifd-12-0b1434ad': No such file or directory", true},
		{"fatal: could not create directory of '/Users/me/Application Support/My Repos/x/.git/worktrees/loop-3': No such file or directory", true},
		{`fatal: could not create directory of 'C:\repository\.git\worktrees\loop-3': No such file or directory`, true},
		// An add refused its entry for any reason but the directory having gone is
		// not an instant that passes, and neither is a directory that is not the
		// registrations'.
		{"fatal: could not create directory of '.git/worktrees/loop-3': Permission denied", false},
		{"fatal: could not create directory of '/tmp/repository/src/worktrees/loop-3': No such file or directory", false},
		{"fatal: failed to read .git/worktrees/loop-3/gitdir: Is a directory", false},
		{"fatal: Invalid path '/tmp/repository/src': No such file or directory", false},
		// A directory that happens to be called worktrees is not the
		// registrations unless it is a Git directory's.
		{"fatal: Invalid path '/tmp/repository/src/worktrees/creation-loop-27': No such file or directory", false},
		{"fatal: Invalid path '/tmp/repository/.git/worktrees/creation-loop-27/nested': No such file or directory", false},
		{"fatal: Needed a single revision", false},
	} {
		if got := crossedRegistration.MatchString(tc.stderr); got != tc.crossed {
			t.Errorf("crossedRegistration(%q) = %t, want %t", tc.stderr, got, tc.crossed)
		}
	}
}

// Only the crossing is run again. Every other refusal is Git's answer, and
// asking the same question three times would turn each of those into a slower
// version of itself — and a command with an effect into one made twice.
func TestManagerBelievesAGitRefusalThatIsNotACrossing(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{
		delegate: execution.OSProcessRunner{},
		refusals: registrationWalkAttempts,
		command:  []string{"rev-parse", "--verify"},
		stderr:   "fatal: Needed a single revision\n",
	}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-refused",
		BaseRef:    "HEAD",
	})
	if err == nil || !strings.Contains(err.Error(), "Needed a single revision") {
		t.Fatalf("Create() error = %v, want Git's own refusal", err)
	}
	if runs, _ := runner.observed(); runs != 1 {
		t.Fatalf("runs = %d, want the refusal to have been believed the first time", runs)
	}
}

// A registration a killed add left half-written used to stop every later run on
// the repository at its own creation, until a person removed the directory by
// hand: `git worktree add` walks the registrations before it writes one, and
// walking that entry is what fails. So a creation clears such an entry first.
// The lease it already holds is what says the entry is not a creation of the
// harness's own still writing, and its age is what says it is not somebody
// else's; the branch the dead add made is left, being a branch like any other,
// and so is whatever directory it left on disk.
func TestManagerCreatesAcrossARegistrationAKilledAddLeftBehind(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	var notes []string
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Note:           func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) },
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	killed := killAddMidRegistration(t, repository, "yoyodyne-killed-4f2a9c1b", 2*unfinishedRegistrationGrace)

	// The failure being cleared is Git's own rather than a described one: if a
	// later Git creates across this entry, this test is asserting nothing and must
	// be told so rather than passing quietly.
	if output, err := attemptGit(repository, "worktree", "add", "--quiet", "--detach", filepath.Join(t.TempDir(), "probe"), "HEAD"); err == nil {
		t.Fatalf("git created a worktree with a killed add's registration present, so the failure this clears is gone:\n%s", output)
	}

	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-after-a-killed-add",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want the killed add's registration to have been cleared first", err)
	}
	if _, err := os.Lstat(killed.registration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want the registration gone", killed.registration, err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "cleared the worktree registration "+filepath.Base(killed.registration)) || !strings.Contains(notes[0], killed.reason) {
		t.Fatalf("notes = %v, want one saying the registration was cleared and why", notes)
	}
	// Git describes the repository again, with the new checkout and without the
	// dead one — which is the whole of what the next run needed.
	if _, err := manager.Inspect(context.Background(), worktree); err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	// What the dead add made before it died is not this package's to remove: the
	// branch is a branch, and the directory is one Git is not managing.
	if _, err := attemptGit(repository, "show-ref", "--verify", "--quiet", "refs/heads/"+killed.branch); err != nil {
		t.Fatalf("the killed add's branch %s is gone; clearing a registration must not delete a branch", killed.branch)
	}
	if _, err := os.Stat(killed.path); err != nil {
		t.Fatalf("Stat(%s) error = %v, want the directory the killed add left where it was", killed.path, err)
	}
	if !strings.Contains(notes[0], killed.path) {
		t.Fatalf("notes = %v, want the note to name the directory that is not a worktree any more", notes)
	}
}

// An add the harness did not make holds no lease, so an unfinished entry young
// enough to be one still working is not cleared on sight. A creation waits the
// grace out rather than failing over it, because the grace is short and a run
// lost at creation is not; the entry is judged again afterwards, and one that
// has stopped moving is cleared then.
func TestManagerWaitsOutTheGraceBeforeClearingAYoungRegistration(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	var notes []string
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Note:           func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) },
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	killed := killAddMidRegistration(t, repository, "yoyodyne-young-7e05c3d1", unfinishedRegistrationGrace-2*time.Second)

	started := time.Now()
	if _, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-after-a-young-kill",
		BaseRef:    "HEAD",
	}); err != nil {
		t.Fatalf("Create() error = %v, want the grace waited out and the registration cleared", err)
	}
	if waited := time.Since(started); waited < time.Second {
		t.Fatalf("Create() took %s, want the rest of the grace waited out before the entry was judged", waited)
	}
	if _, err := os.Lstat(killed.registration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want the registration cleared once the grace had passed", killed.registration, err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "cleared the worktree registration") {
		t.Fatalf("notes = %v, want one saying the registration was cleared", notes)
	}
}

// killedAdd is what a `git worktree add` that died leaves, as a test made it.
type killedAdd struct {
	registration string
	path         string
	branch       string
	reason       string
}

// killAddMidRegistration leaves the repository holding exactly what a `git
// worktree add` killed while registering leaves: the branch it made first, a
// directory on disk holding only its .git file, and under worktrees/ an entry
// still locked as initializing, with gitdir written, commondir created and
// empty — the file every registration walk then dies over — and no HEAD and no
// index, because it never reached the checkout. age is how long ago the kill
// is made to look, so a test can stand on either side of the grace.
func killAddMidRegistration(t *testing.T, repository, name string, age time.Duration) killedAdd {
	t.Helper()
	runGit(t, repository, "branch", name, "HEAD")
	path := filepath.Join(t.TempDir(), name)
	registration := filepath.Join(repository, ".git", "worktrees", name)
	for _, directory := range []string{path, registration} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", directory, err)
		}
	}
	writeFile(t, path, ".git", "gitdir: "+registration+"\n")
	writeFile(t, registration, "locked", "initializing")
	writeFile(t, registration, "gitdir", filepath.Join(path, ".git")+"\n")
	writeFile(t, registration, "commondir", "")
	then := time.Now().Add(-age)
	for _, file := range []string{"locked", "gitdir", "commondir", ""} {
		if err := os.Chtimes(filepath.Join(registration, file), then, then); err != nil {
			t.Fatalf("Chtimes(%s) error = %v", file, err)
		}
	}
	// The entry is cleared before the test's own cleanup lists the repository,
	// in case the code under test did not: that cleanup lists the worktrees too.
	t.Cleanup(func() { _ = os.RemoveAll(registration) })
	return killedAdd{
		registration: registration,
		path:         path,
		branch:       name,
		reason:       "its lock still says initializing",
	}
}

// killAddMidCheckout leaves what a `git worktree add` killed during its checkout
// leaves: every bookkeeping file written, the entry still locked as
// initializing, and no index, because the checkout is what writes it. Git
// describes a repository holding one, so it stops nothing — what it does is hold
// its branch and a sandbox deny path for good, since a locked entry is never
// pruned.
func killAddMidCheckout(t *testing.T, repository, name string, age time.Duration) killedAdd {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	runGit(t, repository, "worktree", "add", "--quiet", "-b", name, path)
	registration := filepath.Join(repository, ".git", "worktrees", name)
	if err := os.Remove(filepath.Join(registration, "index")); err != nil {
		t.Fatalf("Remove(index) error = %v", err)
	}
	// Written the way Git writes it, with the trailing newline its own
	// write_file adds. This shape is recognized by the lock alone — it is the
	// one a listing describes perfectly well, so no other rule catches it — and
	// a fixture that wrote the word bare would pass against a reader that only
	// ever compares the word, and say nothing about the file Git leaves.
	writeFile(t, registration, "locked", "initializing\n")
	then := time.Now().Add(-age)
	entries, err := os.ReadDir(registration)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", registration, err)
	}
	for _, entry := range entries {
		if err := os.Chtimes(filepath.Join(registration, entry.Name()), then, then); err != nil {
			t.Fatalf("Chtimes(%s) error = %v", entry.Name(), err)
		}
	}
	if err := os.Chtimes(registration, then, then); err != nil {
		t.Fatalf("Chtimes(%s) error = %v", registration, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(registration) })
	// Git recorded the checkout by the path it resolved rather than the one the
	// test named, and that is the path a reader of the entry reports.
	gitdir, err := os.ReadFile(filepath.Join(registration, "gitdir"))
	if err != nil {
		t.Fatalf("ReadFile(gitdir) error = %v", err)
	}
	return killedAdd{
		registration: registration,
		path:         filepath.Dir(strings.TrimSpace(string(gitdir))),
		branch:       name,
		reason:       "its lock still says initializing",
	}
}

// Git prunes worktree registrations during automatic maintenance, and it judges
// one stale by whether its gitdir file is there — which is precisely what an add
// still filling its entry in has not written yet. A prune reaching that window
// deletes the registration out from under the add, and the run is lost. The
// registry lease cannot queue a prune Git starts for itself, so no command the
// harness runs is allowed to start one; that is a property of every command
// rather than of the few that write, because maintenance is triggered by
// ordinary writing commands and prunes the whole repository when it runs.
func TestManagerGitCommandsNeverStartAutomaticMaintenance(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// The whole lifecycle is driven, because the assertion is about every command
	// the harness issues and the writing ones are what start maintenance.
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-maintenance",
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
	if _, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	}); err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}

	if len(runner.commands) == 0 {
		t.Fatal("no Git commands were recorded")
	}
	for _, command := range runner.commands {
		// The settings have to lead: Git reads -c only before the subcommand, so
		// one that arrived after it would be an argument to the subcommand and
		// disable nothing.
		if len(command) < len(maintenanceOptions) || !reflect.DeepEqual(command[:len(maintenanceOptions)], maintenanceOptions) {
			t.Fatalf("git %v does not disable automatic maintenance first", command)
		}
	}
}

// A removal writes the same bookkeeping an add does, and Git guards neither, so
// a creation that reached a half-deleted entry would exit rather than create
// anything. Removal therefore queues on the registry lease. The lease is held
// here from outside the removal, so what this observes is the removal waiting
// for it rather than two commands that happened not to overlap.
func TestManagerRemovalQueuesOnTheWorktreeRegistryLease(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &removalWitness{delegate: execution.OSProcessRunner{}, removing: make(chan struct{}, 1)}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-removal-queue",
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

	_, lease, err := manager.leaseRegistry(context.Background())
	if err != nil {
		t.Fatalf("leaseRegistry() error = %v", err)
	}
	cleaned := make(chan error, 1)
	go func() {
		_, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
			Worktree:     worktree,
			TargetBranch: worktree.TargetBranch,
			SourceCommit: integration.SourceCommit,
		})
		cleaned <- err
	}()

	// Nothing here waits for the removal to be attempted, because the assertion
	// is that it is not. A machine slow enough to make this window uninformative
	// still cannot fail it wrongly: only a removal that actually ran does that.
	select {
	case <-runner.removing:
		t.Fatal("the worktree was unregistered while the registry lease was held elsewhere")
	case err := <-cleaned:
		t.Fatalf("CleanupIntegrated() finished without waiting for the lease: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := lease.release(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	if err := <-cleaned; err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	if registrations := gitOutput(t, repository, "worktree", "list", "--porcelain"); strings.Contains(registrations, worktree.Path) {
		t.Fatalf("worktree registration survived cleanup: %q", registrations)
	}
}

// Concurrent creation is the condition every guard here exists for, and a
// guard only ever exercised one command at a time is a guard nothing has tested.
// So creations and registration walks are run against one repository at the same
// time, and the assertion is that none of them fails.
//
// Every one of them goes through a manager, which is the shape a machine running
// several developers actually has: each run has a manager of its own over the
// one repository, and two managers queue on the lease exactly as two processes
// do, because the lock belongs to the open file description rather than to the
// process. That is what makes this deterministic rather than likely — a creation
// holds the lease from before its `git worktree add` until the checkout is
// verified, and every walking command waits for it.
//
// Git run outside a manager — a check, an agent's own command, a second harness
// on an older binary — takes no lease, and nothing here asserts about it. What
// covers that is the re-run in runBounded, and the place to hold it to that is
// TestManagerRunsAnyGitCommandAgainWhenItCrossesACreation, which injects the
// crossing rather than racing for it. Asserting on it here would be asserting
// that roughly 150ms of re-run always outlasts a window this machine's load
// decides the width of, which is a flake waiting for a busy afternoon.
func TestRegistrationWalksAndCreationsRunBesideEachOtherWithoutFailing(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	newManager := func() *Manager {
		t.Helper()
		manager, err := New(Options{
			Runner:         execution.OSProcessRunner{},
			RepositoryRoot: repository,
			WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
			Timeout:        testGitBudget,
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return manager
	}

	// One worktree is made first and never removed, so `worktrees/` itself is
	// there throughout. Git deletes that directory when its last entry goes, and
	// a walk crossing *that* dies on the directory rather than on an entry — a
	// different race, covered within one harness by the lease over creations and
	// removals alike, and nothing this is measuring.
	runGit(t, repository, "worktree", "add", "--quiet", "--detach", filepath.Join(t.TempDir(), "held"), "HEAD")

	// Each side stops at the same deadline whatever becomes of the other, so a
	// failure on one does not leave the other running.
	deadline := time.Now().Add(3 * time.Second)
	var running sync.WaitGroup
	failures := make(chan error, 64)

	for creator := 0; creator < 2; creator++ {
		manager := newManager()
		running.Add(1)
		go func() {
			defer running.Done()
			for attempt := 0; time.Now().Before(deadline); attempt++ {
				worktree, err := manager.Create(context.Background(), CreateRequest{
					// The branch a creation makes is named from the front of the
					// run id, and a removal deliberately leaves the branch, so
					// the front of the id is what has to differ each time round
					// and between the two creators.
					RunID:      fmt.Sprintf("run-%02x%06x%024x", creator, attempt, attempt),
					WorkItemID: "yoyodyne-beside",
					BaseRef:    "HEAD",
				})
				if err != nil {
					failures <- fmt.Errorf("Create() beside a walk: %w", err)
					return
				}
				// Removing puts the other unguarded half of the bookkeeping — an
				// entry taken away a piece at a time — beside the walks as well.
				if _, err := manager.RemovePreservedWorktree(context.Background(), worktree, KeepUncommittedWork); err != nil {
					failures <- fmt.Errorf("RemovePreservedWorktree() beside a walk: %w", err)
					return
				}
			}
		}()
	}

	for walker := 0; walker < 4; walker++ {
		manager := newManager()
		// Made here rather than by the first `--force` below, because Git reads
		// the registrations to check a branch is not checked out elsewhere and a
		// branch that does not exist yet cannot be, so moving one that is
		// already there is what makes every iteration a walk.
		branch := fmt.Sprintf("walker-%d", walker)
		runGit(t, repository, "branch", branch, "HEAD")
		running.Add(1)
		go func() {
			defer running.Done()
			for time.Now().Before(deadline) {
				if _, err := manager.listWorktrees(context.Background()); err != nil {
					failures <- fmt.Errorf("listWorktrees() beside a creation: %w", err)
					return
				}
				// A rebase, a checkout and a branch deletion walk the
				// registrations the listing walks, and a branch is the cheapest
				// of them to run in a loop. It is also the one with the least
				// under it: a listing that crossed a creation would be caught
				// twice — the re-run, and then the bookkeeping answering what Git
				// refused — while a branch has only the lease and the re-run, so
				// a crossing it met would come back as a refusal nothing softens.
				result, err := manager.run(context.Background(), "-C", repository, "branch", "--force", branch, "HEAD")
				if err != nil {
					failures <- fmt.Errorf("branch beside a creation: %w", err)
					return
				}
				if result.Status != execution.ProcessSucceeded {
					failures <- fmt.Errorf("branch beside a creation failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
					return
				}
			}
		}()
	}

	running.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

// The branch is made apart from the add so that an add run again meets a
// repository it has not already changed. That leaves a branch to take back when
// the add fails for its own reasons, where `git worktree add -b` making both as
// one thing left nothing behind — so a creation that could not add its checkout
// removes the branch it had just made, and a failed creation leaves the
// repository as it found it.
func TestManagerRemovesTheBranchOfAWorktreeItCouldNotAdd(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{
		delegate: execution.OSProcessRunner{},
		refusals: 1,
		command:  []string{"worktree", "add"},
		stderr:   "fatal: could not create leading directories of '/nowhere'\n",
	}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-add-refused",
		BaseRef:    "HEAD",
	}); err == nil {
		t.Fatal("Create() succeeded although the add was refused")
	}
	branch := branchName("yoyodyne-add-refused", testRunID)
	if output, err := attemptGit(repository, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		t.Fatalf("branch %s survived a creation that added no worktree: %s", branch, output)
	}
}

// Running a command again is what covers a crossing that happened. Not
// crossing at all is the other half, and it is the lease: a command that walks
// the registrations reads them in a shared mode every other reader may hold at
// once and no write may hold beside, so it queues behind a creation rather than
// meeting the entry that creation has not filled in yet.
func TestManagerQueuesARegistrationWalkingCommandBehindAWrite(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &commandWitness{delegate: execution.OSProcessRunner{}, first: "worktree", second: "list", ran: make(chan struct{}, 1)}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-read-queue",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// The creation above verified itself with a listing of its own, so what it
	// announced is forgotten before the one this is about.
	select {
	case <-runner.ran:
	default:
	}

	// The lease is taken here rather than by a creation, so what this observes
	// is the reader waiting for a write rather than two commands that happened
	// not to overlap.
	_, lease, err := manager.leaseRegistry(context.Background())
	if err != nil {
		t.Fatalf("leaseRegistry() error = %v", err)
	}
	inspected := make(chan error, 1)
	go func() {
		_, err := manager.Inspect(context.Background(), worktree)
		inspected <- err
	}()

	// Nothing here waits for the listing to be run, because the assertion is
	// that it is not. A machine slow enough to make this window uninformative
	// still cannot fail it wrongly: only a listing that actually ran does that.
	select {
	case <-runner.ran:
		t.Fatal("the registrations were walked while the registry lease was held elsewhere")
	case err := <-inspected:
		t.Fatalf("Inspect() finished without waiting for the lease: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := lease.release(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	if err := <-inspected; err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
}

// A holder reads what it is writing: a creation verifies the checkout it just
// added, which walks the same registrations it holds the lease over. Queueing
// there would be the creation waiting for itself, so a read under the context
// the lease handed back takes nothing at all. Every creation in this package
// proves it by finishing; this says so in one place, because what would break
// it is a caller threading the wrong context rather than anything visible in a
// result.
func TestManagerReadsTheRegistrationsUnderItsOwnWrite(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-read-under-write",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	held, lease, err := manager.leaseRegistry(context.Background())
	if err != nil {
		t.Fatalf("leaseRegistry() error = %v", err)
	}
	defer func() { _ = lease.release() }()

	inspected := make(chan error, 1)
	go func() {
		_, err := manager.Inspect(held, worktree)
		inspected <- err
	}()
	select {
	case err := <-inspected:
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a read under the lease this process holds waited for the lease it is holding")
	}
}

// The lease is not a queue over every Git command. A command that never opens
// the registrations — the overwhelming majority of what the manager runs — is
// not held back by a creation, because serializing those would turn parallel
// development into one run at a time for no gain.
func TestManagerDoesNotQueueACommandThatWalksNoRegistrations(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, lease, err := manager.leaseRegistry(context.Background())
	if err != nil {
		t.Fatalf("leaseRegistry() error = %v", err)
	}
	defer func() { _ = lease.release() }()

	answered := make(chan error, 1)
	go func() {
		_, err := manager.CurrentBranch(context.Background())
		answered <- err
	}()
	select {
	case err := <-answered:
		if err != nil {
			t.Fatalf("CurrentBranch() error = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a command that walks no registrations waited for the registry lease")
	}
}

// Which commands take the lease is a judgement about what Git reads, so it is
// stated as a table rather than left to be inferred from the one command a test
// happens to drive. The global options in front of a subcommand are stepped
// over, because the manager puts them there on every command it runs.
func TestGitCommandsThatWalkTheRegistrationsAreTheOnesThatTakeTheLease(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		args  []string
		walks bool
	}{
		{name: "the listing", args: []string{"-C", "/repository", "worktree", "list", "--porcelain"}, walks: true},
		{name: "an add", args: []string{"-C", "/repository", "worktree", "add", "/path", "branch"}, walks: true},
		{name: "a rebase", args: []string{"-C", "/worktree", "rebase", "--onto", "main", "base"}, walks: true},
		{name: "a checkout", args: []string{"-C", "/worktree", "-c", "core.hooksPath=/dev/null", "checkout", "HEAD", "--", "file"}, walks: true},
		{name: "a branch", args: []string{"-C", "/repository", "branch", "feature", "HEAD"}, walks: true},
		{name: "a switch", args: []string{"-C", "/worktree", "switch", "main"}, walks: true},
		{name: "resolving a commit", args: []string{"-C", "/repository", "rev-parse", "--verify", "HEAD^{commit}"}, walks: false},
		{name: "a diff", args: []string{"-C", "/worktree", "diff", "--name-only", "HEAD"}, walks: false},
		{name: "a status", args: []string{"-C", "/worktree", "status", "--porcelain"}, walks: false},
		{name: "a push", args: []string{"-C", "/repository", "push", "origin", "branch"}, walks: false},
		// The subcommand is what decides, and a path that happens to be named
		// like one is not it.
		{name: "a path named like a subcommand", args: []string{"-C", "/repository", "diff", "--", "branch"}, walks: false},
		{name: "nothing at all", args: nil, walks: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if walks := walksRegistrations(testCase.args); walks != testCase.walks {
				t.Fatalf("walksRegistrations(%q) = %v, want %v", testCase.args, walks, testCase.walks)
			}
		})
	}
}

// commandWitness announces the moment one named Git command is actually run,
// which is what says a lease held it back rather than that it ran and answered.
type commandWitness struct {
	delegate      execution.ProcessRunner
	first, second string
	ran           chan struct{}
}

func (w *commandWitness) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if containsArguments(command.Args, w.first, w.second) {
		select {
		case w.ran <- struct{}{}:
		default:
		}
	}
	return w.delegate.Run(ctx, command, observer)
}

// removalWitness announces the moment the harness unregisters a worktree, which
// is the one step of a cleanup the registry lease has to hold back.
type removalWitness struct {
	delegate execution.ProcessRunner
	removing chan struct{}
}

func (w *removalWitness) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if containsArguments(command.Args, "worktree", "remove") {
		select {
		case w.removing <- struct{}{}:
		default:
		}
	}
	return w.delegate.Run(ctx, command, observer)
}

func TestManagerAllowsOnlyConfiguredPrimaryControlPlaneChanges(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager, err := New(Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          filepath.Join(t.TempDir(), "worktrees"),
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
		Timeout:               testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".beads", "interactions.jsonl"), []byte("base control state\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() base control state error = %v", err)
	}
	runGit(t, repository, "add", ".beads/interactions.jsonl")
	runGit(t, repository, "commit", "-m", "add control state")
	for _, name := range []string{"interactions.jsonl", "issues.jsonl"} {
		if err := os.WriteFile(filepath.Join(repository, ".beads", name), []byte("control state\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if err := manager.ValidateReady(context.Background()); err != nil {
		t.Fatalf("ValidateReady() allowed changes error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "product.go"), []byte("product change\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() product error = %v", err)
	}
	if err := manager.ValidateReady(context.Background()); err == nil || !strings.Contains(err.Error(), "product.go") {
		t.Fatalf("ValidateReady() product error = %v", err)
	}
}

// The race this holds: the work tracker rewrites its export atomically, leaving
// a temp file beside the target for as long as the write takes, and a readiness
// check landing inside that window used to read it as untracked state nobody
// declared and refuse. It cost two completed, reviewed rounds in one afternoon —
// one of them discarding an approved pull request — and it recurs for any run,
// because nothing about it depends on what the run was doing.
func TestManagerReadiesThroughAnExportBeingRewritten(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager, err := New(Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          filepath.Join(t.TempDir(), "worktrees"),
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
		Timeout:               testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// Both declared exports mid-write at once, which is what a tracker flushing
	// everything it holds leaves behind.
	inFlight := []string{".beads/.~issues.jsonl.2847119036", ".beads/.~interactions.jsonl.417", ".beads/.yoyo-write-SNAPSHOT.tmp"}
	for _, name := range inFlight {
		if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(name)), []byte("half an export\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if err := manager.ValidateReady(context.Background()); err != nil {
		t.Fatalf("ValidateReady() mid-export error = %v", err)
	}
	// Tolerated, not swept: the file belongs to the write still in flight, and
	// removing it out from under the writer would corrupt the export this check
	// was only ever meant to read past.
	for _, name := range inFlight {
		if _, err := os.Stat(filepath.Join(repository, filepath.FromSlash(name))); err != nil {
			t.Errorf("Stat(%s) error = %v, want the in-flight write left alone", name, err)
		}
	}

	// The tolerance is for the exporter's own temp names and nothing wider. A
	// file that merely looks like one, or that sits beside no declared export,
	// is still somebody's to resolve.
	for _, name := range []string{".beads/.~ledger.jsonl.417", ".~issues.jsonl.417", ".beads/.~issues.jsonl", ".yoyo-write-SNAPSHOT.tmp", ".beads/.yoyo-write-.tmp"} {
		if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(name)), []byte("not an export write\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
		if err := manager.ValidateReady(context.Background()); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("ValidateReady() with %s error = %v, want it named as unexpected", name, err)
		}
		if err := os.Remove(filepath.Join(repository, filepath.FromSlash(name))); err != nil {
			t.Fatalf("Remove(%s) error = %v", name, err)
		}
	}
}

func TestManagerSummarizeChangesIncludesTrackedAndUntrackedFiles(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-task",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, "README.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() tracked error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() untracked error = %v", err)
	}

	summary, err := manager.SummarizeChanges(context.Background(), worktree)
	if err != nil {
		t.Fatalf("SummarizeChanges() error = %v", err)
	}
	if !strings.Contains(summary.Status, "M README.txt") || !strings.Contains(summary.Status, "?? new.txt") {
		t.Fatalf("status = %q", summary.Status)
	}
	if !strings.Contains(summary.DiffStat, "README.txt") {
		t.Fatalf("diff stat = %q", summary.DiffStat)
	}

	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")
	if _, err := manager.SummarizeChanges(context.Background(), worktree); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("SummarizeChanges() committed HEAD error = %v", err)
	}
}

func TestManagerUnifiedChangesCoversTrackedAndUntrackedWorkWithoutMutating(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-diff", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "test\nedited\n")
	writeFile(t, worktree.Path, "new.txt", "brand new\n")
	writeFile(t, worktree.Path, filepath.Join("sub", "odd name.txt"), "nested\n")
	writeFile(t, worktree.Path, ".gitignore", "ignored.txt\n")
	writeFile(t, worktree.Path, "ignored.txt", "should not be reviewed\n")

	before := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all")
	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	for _, want := range []string{
		"diff --git a/README.txt b/README.txt",
		"+edited",
		"diff --git a/new.txt b/new.txt",
		"+brand new",
		"diff --git a/sub/odd name.txt b/sub/odd name.txt",
		"+nested",
	} {
		if !strings.Contains(changes.Patch, want) {
			t.Errorf("patch is missing %q:\n%s", want, changes.Patch)
		}
	}
	if strings.Contains(changes.Patch, "should not be reviewed") {
		t.Errorf("patch includes an ignored file:\n%s", changes.Patch)
	}
	wantUntracked := []string{".gitignore", "new.txt", "sub/odd name.txt"}
	if !reflect.DeepEqual(changes.UntrackedFiles, wantUntracked) {
		t.Errorf("untracked files = %#v, want %#v", changes.UntrackedFiles, wantUntracked)
	}
	if changes.Truncated || len(changes.OmittedFiles) != 0 {
		t.Errorf("changes = %#v, want an untruncated change", changes)
	}
	if !strings.Contains(changes.Status, "?? new.txt") || !strings.Contains(changes.DiffStat, "README.txt") {
		t.Errorf("changes summary = %#v", changes)
	}

	// Inspecting a change must never stage or otherwise alter what the
	// developer left behind.
	if after := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); after != before {
		t.Errorf("worktree status changed during inspection:\nbefore %q\nafter  %q", before, after)
	}
	if staged := gitOutput(t, worktree.Path, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("inspection staged files: %q", staged)
	}
}

func TestManagerChangedPathsNamesBothSidesOfEveryChange(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-paths", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A tracked file moved somewhere else. A caller deciding what a change was
	// allowed to touch has to see the path it left as well as the one it
	// arrived at, because moving a file out of a directory is as much of an
	// edit to that directory as writing in it.
	if err := os.Rename(filepath.Join(worktree.Path, "README.txt"), filepath.Join(worktree.Path, "MOVED.txt")); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	writeFile(t, worktree.Path, filepath.Join("sub", "odd name.txt"), "nested\n")
	writeFile(t, worktree.Path, ".gitignore", "ignored.txt\n")
	writeFile(t, worktree.Path, "ignored.txt", "should not be gated\n")

	before := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all")
	changed, err := manager.ChangedPaths(context.Background(), worktree)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	want := []string{".gitignore", "MOVED.txt", "README.txt", "sub/odd name.txt"}
	if !reflect.DeepEqual(changed, want) {
		t.Fatalf("ChangedPaths() = %#v, want %#v", changed, want)
	}
	// Inspecting a change never alters it, here for the same reason the unified
	// diff never does.
	if after := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); after != before {
		t.Errorf("worktree status changed during inspection:\nbefore %q\nafter  %q", before, after)
	}
}

func TestManagerChangedPathsSeesWhatAnAttemptAlreadyCommitted(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-committed", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	// Publishing commits each developer attempt, so a listing built from
	// uncommitted status would report a published change as touching nothing.
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"commit", "-m", "yoyodyne: published attempt")
	worktree.HarnessCommit = gitLine(t, worktree.Path, "rev-parse", "HEAD")

	changed, err := manager.ChangedPaths(context.Background(), worktree)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"feature.txt"}) {
		t.Fatalf("ChangedPaths() = %#v, want the committed change", changed)
	}
}

func TestManagerUnifiedChangesSeesWhatAnAttemptAlreadyCommitted(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-committed-diff", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "test\nedited\n")
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	// Publishing commits each attempt, so the change a reviewer is handed is
	// measured against the base commit and not against the index: a patch
	// collected the other way is empty here while the branch carries the work,
	// which is the review this repository ran on nothing and then diagnosed.
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: published attempt")
	// And the attempt that followed it, still uncommitted. The change is both
	// halves together; either one alone is not what would be promoted.
	writeFile(t, worktree.Path, "later.txt", "still working\n")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	for _, want := range []string{
		"diff --git a/README.txt b/README.txt",
		"+edited",
		"diff --git a/feature.txt b/feature.txt",
		"+implemented",
		"diff --git a/later.txt b/later.txt",
		"+still working",
	} {
		if !strings.Contains(changes.Patch, want) {
			t.Errorf("patch over a committed attempt is missing %q:\n%s", want, changes.Patch)
		}
	}
	for _, want := range []string{"M README.txt", "A feature.txt", "?? later.txt"} {
		if !strings.Contains(changes.Status, want) {
			t.Errorf("status = %q, want it to name %q", changes.Status, want)
		}
	}
	if !strings.Contains(changes.DiffStat, "feature.txt") {
		t.Errorf("diff stat = %q, want the committed file", changes.DiffStat)
	}
	// The commits are described only where the change over the base is nothing,
	// and this change is plainly something.
	if len(changes.CommitsWithoutEffect) != 0 {
		t.Errorf("commits without effect = %#v, want none beside a change", changes.CommitsWithoutEffect)
	}
	// What the patch spans is carried with it: the base it is measured against
	// and the attempt already published for it. A reader given the patch alone
	// cannot tell it from the uncommitted half of the same change.
	if changes.BaseCommit != worktree.BaseCommit {
		t.Errorf("base commit = %q, want the recorded base %q", changes.BaseCommit, worktree.BaseCommit)
	}
	if len(changes.Commits) != 1 || changes.Commits[0].Commit != worktree.HarnessCommit ||
		changes.Commits[0].Subject != "yoyodyne: published attempt" {
		t.Fatalf("commits = %#v, want the published attempt the patch spans", changes.Commits)
	}
	if changes.CommitsOmitted != 0 {
		t.Errorf("commits omitted = %d, want none", changes.CommitsOmitted)
	}
}

// The shape yoyodyne-ifd.321 was admitted on: a run that continues on a branch
// its earlier attempts already committed to. The patch spans both commits and
// the uncommitted repair beside them, and now says so — five reviews across two
// items discounted their own verdicts for want of that sentence.
func TestManagerUnifiedChangesNamesEveryCommitAContinuedRunCarries(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-continued", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "reduced.md", "the reduction\n")
	first := harnessCommit(t, worktree.Path, "yoyodyne: the reduction")
	writeFile(t, worktree.Path, "widened.go", "package widened\n")
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: the widening")
	writeFile(t, worktree.Path, "repair.go", "package repair\n")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if len(changes.Commits) != 2 {
		t.Fatalf("commits = %#v, want both commits above the base", changes.Commits)
	}
	if changes.Commits[0].Commit != first || changes.Commits[1].Commit != worktree.HarnessCommit {
		t.Errorf("commits = %#v, want them oldest first", changes.Commits)
	}
	// Named and shown: the listing is the account of a patch that carries the
	// same work, rather than a substitute for a patch that does not.
	for _, want := range []string{"+the reduction", "package widened", "package repair"} {
		if !strings.Contains(changes.Patch, want) {
			t.Errorf("patch over a continued run is missing %q:\n%s", want, changes.Patch)
		}
	}
	// A bound on the listing cuts the account of the change, never the change:
	// the patch is the whole range whichever commits the listing could hold.
	bounded, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxCommits: 1})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if len(bounded.Commits) != 1 || bounded.CommitsOmitted != 1 {
		t.Fatalf("bounded commits = %#v, omitted = %d, want one of each", bounded.Commits, bounded.CommitsOmitted)
	}
	if bounded.Truncated || bounded.Patch != changes.Patch {
		t.Errorf("a bounded commit listing truncated the change: truncated = %t", bounded.Truncated)
	}
}

func TestManagerUnifiedChangesNamesCommittedWorkThatLeavesTheBaseUnchanged(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-undone", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	harnessCommit(t, worktree.Path, "yoyodyne: first attempt")
	// The attempt that undid it, published exactly as the one before it was. The
	// branch carries both commits and the change over the base is nothing.
	if err := os.Remove(filepath.Join(worktree.Path, "feature.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: reverted attempt")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if changes.Patch != "" || changes.Status != "" {
		t.Fatalf("changes = %#v, want an empty change over the base", changes)
	}
	if len(changes.CommitsWithoutEffect) != 2 {
		t.Fatalf("commits without effect = %#v, want the two commits above the base", changes.CommitsWithoutEffect)
	}
	// Oldest first, and each one named by what it claimed to do, so a reader can
	// tell an undone change from evidence that was never collected.
	if changes.CommitsWithoutEffect[0].Subject != "yoyodyne: first attempt" ||
		changes.CommitsWithoutEffect[1].Subject != "yoyodyne: reverted attempt" {
		t.Fatalf("commits without effect = %#v, want them oldest first", changes.CommitsWithoutEffect)
	}
	if changes.CommitsWithoutEffect[1].Commit != worktree.HarnessCommit {
		t.Fatalf("last described commit = %q, want the recorded harness commit %q",
			changes.CommitsWithoutEffect[1].Commit, worktree.HarnessCommit)
	}
}

func TestManagerUnifiedChangesEnforcesDiffBounds(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-bounds", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "big.txt", strings.Repeat("a line of new content\n", 200))
	writeFile(t, worktree.Path, "small.txt", "small\n")

	perFile, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFileBytes: 64})
	if err != nil {
		t.Fatalf("UnifiedChanges() per-file error = %v", err)
	}
	// The file the per-file bound dropped is recorded by name, with the size it
	// actually is, the bound it exceeded, and the digest of what it delivers, so
	// what a reviewer is handed says "delivered but too large to show" rather than
	// nothing at all — and a person opening it can prove they opened that file.
	wantOversized := []OmittedFile{{
		Path: "big.txt", Bytes: 4400, Reason: OmittedTooLarge, Class: FileClassSource, Bound: 64,
		Digest: digestOf(strings.Repeat("a line of new content\n", 200)),
	}}
	if !perFile.Truncated || !reflect.DeepEqual(perFile.OmittedFiles, wantOversized) {
		t.Fatalf("per-file bound = %#v", perFile)
	}
	if !reflect.DeepEqual(perFile.UntrackedFiles, []string{"small.txt"}) {
		t.Fatalf("per-file untracked = %#v", perFile.UntrackedFiles)
	}
	if strings.Contains(perFile.Patch, "a line of new content") {
		t.Fatalf("patch included an oversized file:\n%s", perFile.Patch)
	}

	counted, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFiles: 1})
	if err != nil {
		t.Fatalf("UnifiedChanges() file-count error = %v", err)
	}
	wantCounted := []OmittedFile{{
		Path: "small.txt", Bytes: 6, Reason: OmittedTooManyFiles, Class: FileClassSource, Bound: 1,
		Digest: digestOf("small\n"),
	}}
	if len(counted.UntrackedFiles) != 1 || !counted.Truncated || !reflect.DeepEqual(counted.OmittedFiles, wantCounted) {
		t.Fatalf("file-count bound = %#v", counted)
	}

	// A total bound clamps to whole lines rather than cutting a diff mid-line.
	total, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxTotalBytes: 200})
	if err != nil {
		t.Fatalf("UnifiedChanges() total error = %v", err)
	}
	if !total.Truncated {
		t.Fatalf("total bound = %#v", total)
	}
	if len(total.Patch) > 200 {
		t.Fatalf("patch is %d bytes, want at most 200", len(total.Patch))
	}
	if total.Patch != "" && !strings.HasSuffix(total.Patch, "\n") {
		t.Fatalf("clamped patch ends mid-line: %q", total.Patch)
	}

	if _, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFiles: -1}); err == nil {
		t.Fatal("UnifiedChanges() negative limit error = nil")
	}
}

// The shape this forbids is a file the change delivers leaving the assembly
// without being shown and without being named: a reviewer handed that judges a
// delivery it cannot know happened. Every untracked file is therefore in the
// patch or in the omission record, and the record says how big the file is and
// which bound dropped it.
func TestManagerUnifiedChangesAccountsForEveryDeliveredFile(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-omissions", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	oversized := strings.Repeat("generated line\n", 6000)
	writeFile(t, worktree.Path, "corpus.txt", oversized)
	writeFile(t, worktree.Path, "asset.bin", "new\x00binary\n")
	writeFile(t, worktree.Path, "feature.go", "package main\n")
	if err := os.Symlink("feature.go", filepath.Join(worktree.Path, "alias.go")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	accounted := map[string]bool{}
	for _, file := range changes.UntrackedFiles {
		accounted[file] = true
	}
	for _, file := range changes.OmittedFiles {
		if file.Path == "" || file.Reason == "" {
			t.Errorf("omitted file = %#v, want a path and a reason", file)
		}
		accounted[file.Path] = true
	}
	for _, delivered := range []string{"corpus.txt", "asset.bin", "feature.go", "alias.go"} {
		if !accounted[delivered] {
			t.Errorf("%s is delivered by the change and is neither shown nor named as omitted", delivered)
		}
	}
	if !changes.Truncated {
		t.Errorf("a change with omitted files was reported as complete: %#v", changes)
	}

	omissions := map[string]OmittedFile{}
	for _, file := range changes.OmittedFiles {
		omissions[file.Path] = file
	}
	// The size is the file's own and the bound is the one it was measured
	// against, so a reviewer reads how far over the ceiling the file is rather
	// than being told a name and left to guess.
	want := OmittedFile{
		Path: "corpus.txt", Bytes: int64(len(oversized)), Reason: OmittedTooLarge, Class: FileClassSource,
		Bound: DefaultMaxDiffFileBytes, Digest: digestOf(oversized),
	}
	if omissions["corpus.txt"] != want {
		t.Errorf("oversized omission = %#v, want %#v", omissions["corpus.txt"], want)
	}
	if described := want.Describe(); !strings.Contains(described, "corpus.txt") ||
		!strings.Contains(described, "delivered but too large to show") ||
		!strings.Contains(described, strconv.Itoa(DefaultMaxDiffFileBytes)) {
		t.Errorf("described omission = %q, want the file, the bound, and what became of it", described)
	}
	// A symlink is not delivered content, so there is nothing to digest and the
	// omission carries none: a listing with no digest is what tells the rule
	// below that this omission is not one anybody could open.
	if alias := omissions["alias.go"]; alias.Reason != OmittedUnreadable || alias.Digest != "" || alias.ListedWhole() {
		t.Errorf("symlink omission = %#v, want it named as unreadable with nothing to open", alias)
	}
	if strings.Contains(changes.Patch, "generated line") {
		t.Errorf("patch included an oversized file:\n%s", changes.Patch)
	}
}

func TestManagerUnifiedChangesMarksBinaryContentIncomplete(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-binary", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "changed\x00binary\n")
	writeFile(t, worktree.Path, "new.bin", "new\x00binary\n")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if !changes.Truncated {
		t.Fatalf("binary changes were reported as complete: %#v", changes)
	}
	// The tracked binary and the new one are named the same way: a file with no
	// reviewable diff is in the omission record, with its size, rather than a
	// "Binary files differ" stub in the patch a reader has to notice. The tracked
	// one carries the size of the stub Git rendered for it as well.
	if len(changes.OmittedFiles) != 2 {
		t.Fatalf("omitted files = %#v, want README.txt and new.bin", changes.OmittedFiles)
	}
	if tracked := changes.OmittedFiles[0]; tracked.Path != "README.txt" || tracked.Bytes != 15 || tracked.Reason != OmittedBinary || tracked.DiffBytes == 0 {
		t.Fatalf("tracked binary omission = %#v, want README.txt named as binary with its diff measured", tracked)
	}
	if changes.OmittedFiles[1] != (OmittedFile{Path: "new.bin", Bytes: 11, Reason: OmittedBinary, Class: FileClassSource, Digest: digestOf("new\x00binary\n")}) {
		t.Fatalf("untracked binary omission = %#v, want new.bin", changes.OmittedFiles[1])
	}
	for _, unreviewable := range []string{"Binary files", "new.bin"} {
		if strings.Contains(changes.Patch, unreviewable) {
			t.Fatalf("unreviewable binary content was included in patch:\n%s", changes.Patch)
		}
	}
	// The listing is where a binary is seen: named, sized, and marked as what it
	// is, so a reviewer told the patch cannot show it can still hold the change
	// to delivering it.
	if len(changes.Files) != 2 {
		t.Fatalf("files = %#v, want both files of the change listed", changes.Files)
	}
	if changes.Files[0] != (ChangedFile{Path: "README.txt", Status: "M", Bytes: 15, Binary: true, Class: FileClassSource}) ||
		changes.Files[1] != (ChangedFile{Path: "new.bin", Status: "??", Bytes: 11, Binary: true, Class: FileClassSource}) {
		t.Fatalf("files = %#v, want each named as binary with its size", changes.Files)
	}
}

// The tracked half of a change is bounded a whole file at a time. Until this
// was written the patch was cut at the byte count, which kept whichever files
// Git rendered first and lost the rest without naming them; a reviewer handed
// that could not say which files its verdict covered, and one shown the first
// half of a hunk read it as a different change.
func TestManagerUnifiedChangesClipsTrackedWorkWholeFileByFile(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-whole", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "a-large.txt", strings.Repeat("a line of committed content\n", 200))
	writeFile(t, worktree.Path, "b-small.txt", "small\n")
	writeFile(t, worktree.Path, "c-medium.txt", strings.Repeat("medium\n", 40))
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: published attempt")

	whole, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if whole.Truncated || len(whole.OmittedFiles) != 0 {
		t.Fatalf("an unbounded change was reported as cut: %#v", whole.OmittedFiles)
	}

	// A bound the large file exceeds on its own and the two smaller ones fit
	// inside together. The scan does not stop at the first file that does not
	// fit: what is shown is every file that can be, each of them whole.
	bounded, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxTotalBytes: 600})
	if err != nil {
		t.Fatalf("UnifiedChanges() bounded error = %v", err)
	}
	if !bounded.Truncated {
		t.Fatalf("a bounded change was reported as complete: %#v", bounded)
	}
	if len(bounded.Patch) > 600 {
		t.Fatalf("patch is %d bytes, want at most 600", len(bounded.Patch))
	}
	for _, want := range []string{"diff --git a/b-small.txt b/b-small.txt", "+small", "diff --git a/c-medium.txt b/c-medium.txt"} {
		if !strings.Contains(bounded.Patch, want) {
			t.Errorf("a file that fits the bound is missing %q:\n%s", want, bounded.Patch)
		}
	}
	if strings.Count(bounded.Patch, "+medium") != 40 {
		t.Errorf("a file inside the bound is not shown whole:\n%s", bounded.Patch)
	}
	if strings.Contains(bounded.Patch, "a-large.txt") || strings.Contains(bounded.Patch, "committed content") {
		t.Errorf("the file the bound dropped is partly in the patch:\n%s", bounded.Patch)
	}
	want := []OmittedFile{{
		Path: "a-large.txt", Bytes: 5600, Reason: OmittedTooLarge, Class: FileClassSource, Bound: 600,
		DiffBytes: int64(len(whole.Patch) - len(bounded.Patch)),
		Digest:    digestOf(strings.Repeat("a line of committed content\n", 200)),
	}}
	if !reflect.DeepEqual(bounded.OmittedFiles, want) {
		t.Fatalf("omitted files = %#v, want %#v", bounded.OmittedFiles, want)
	}
	if described := want[0].Describe(); !strings.Contains(described, "a-large.txt (5600 bytes, "+want[0].Digest+")") ||
		!strings.Contains(described, "too large to show") || !strings.Contains(described, "600 bytes") {
		t.Errorf("described omission = %q, want the file, its size, its digest, and the bound", described)
	}

	// A file that would have fit on its own but reached the bound after another
	// spent it is named for that reason, which is a different fact about the
	// change: it could be shown, and this patch had no room left for it.
	spent, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxTotalBytes: int(want[0].DiffBytes) + 200})
	if err != nil {
		t.Fatalf("UnifiedChanges() spent error = %v", err)
	}
	if len(spent.OmittedFiles) != 1 || spent.OmittedFiles[0].Path != "c-medium.txt" || spent.OmittedFiles[0].Reason != OmittedPatchFull {
		t.Fatalf("omitted files = %#v, want c-medium.txt dropped for a spent bound", spent.OmittedFiles)
	}
	if !strings.Contains(spent.Patch, "committed content") || !strings.Contains(spent.Patch, "+small") {
		t.Errorf("files inside the bound are missing from the patch:\n%s", spent.Patch)
	}
}

// A change that deletes a file the base tracks is the most ordinary change
// there is, and the path it deletes is one the worktree no longer has. The
// deletion is described at the base rather than rendered — its size and blob
// there, measured from the base rather than from a path that is gone — it is
// listed with Git's own status at zero bytes, and however small the bound it is
// never omitted by it (yoyodyne-ifd.429.7).
func TestManagerUnifiedChangesHandlesAFileTheChangeDeletes(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, "obsolete.txt", strings.Repeat("an obsolete line\n", 50))
	runGit(t, repository, "add", "obsolete.txt")
	runGit(t, repository, "commit", "-m", "the file before its removal")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-deletion", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// One deletion an earlier attempt committed, one still uncommitted: both
	// are paths the worktree no longer holds.
	if err := os.Remove(filepath.Join(worktree.Path, "obsolete.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: remove the obsolete file")
	if err := os.Remove(filepath.Join(worktree.Path, "README.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if changes.Truncated || len(changes.OmittedFiles) != 0 {
		t.Fatalf("a deleting change was reported as cut: %#v", changes.OmittedFiles)
	}
	if changes.Patch != "" {
		t.Fatalf("a deletion is rendered as a removal diff:\n%s", changes.Patch)
	}
	obsolete := strings.Repeat("an obsolete line\n", 50)
	wantDeleted := []DeletedFile{
		{Path: "README.txt", Whole: true, BaseCommit: worktree.BaseCommit, BaseBytes: 5,
			BaseDigest: "git-blob:" + gitLine(t, repository, "rev-parse", worktree.BaseCommit+":README.txt"), RemovedLines: 1, Class: FileClassSource},
		{Path: "obsolete.txt", Whole: true, BaseCommit: worktree.BaseCommit, BaseBytes: int64(len(obsolete)),
			BaseDigest: "git-blob:" + gitLine(t, repository, "rev-parse", worktree.BaseCommit+":obsolete.txt"), RemovedLines: 50, Class: FileClassSource},
	}
	for index := range changes.DeletedFiles {
		changes.DeletedFiles[index].DiffBytes = 0
	}
	if !reflect.DeepEqual(changes.DeletedFiles, wantDeleted) {
		t.Fatalf("deleted files = %#v, want %#v", changes.DeletedFiles, wantDeleted)
	}
	want := []ChangedFile{
		{Path: "README.txt", Status: "D", Bytes: 0, Class: FileClassSource},
		{Path: "obsolete.txt", Status: "D", Bytes: 0, Committed: true, Class: FileClassSource},
	}
	if !reflect.DeepEqual(changes.Files, want) {
		t.Fatalf("files = %#v, want %#v", changes.Files, want)
	}

	bounded, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxTotalBytes: 200})
	if err != nil {
		t.Fatalf("UnifiedChanges() bounded error = %v", err)
	}
	if bounded.Truncated || len(bounded.OmittedFiles) != 0 || len(bounded.DeletedFiles) != 2 {
		t.Fatalf("under a small bound: truncated=%t omitted=%#v deleted=%#v, want both deletions described and nothing omitted",
			bounded.Truncated, bounded.OmittedFiles, bounded.DeletedFiles)
	}
}

// A type change — a tracked file that became a symlink — is one entry in Git's
// listing and two blocks in its patch, a deletion and a creation of the same
// path. It is one file's change and is carried as one section, so a legal
// change containing one is rendered and listed rather than refused as a patch
// that could not be paired with its listing.
func TestManagerUnifiedChangesCarriesAFileThatBecameASymlink(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, "target.txt", "the target\n")
	runGit(t, repository, "add", "target.txt")
	runGit(t, repository, "commit", "-m", "the link's target")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-typechange", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := os.Remove(filepath.Join(worktree.Path, "README.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(worktree.Path, "README.txt")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	writeFile(t, worktree.Path, "other.txt", "beside it\n")
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: link the README")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	// Both halves of the type change are in the patch, and so is the file
	// beside it, which the pairing would have misnamed had it taken the two
	// blocks for two files.
	for _, want := range []string{"deleted file mode 100644", "-test\n", "new file mode 120000", "+target.txt", "diff --git a/other.txt b/other.txt", "+beside it"} {
		if !strings.Contains(changes.Patch, want) {
			t.Errorf("patch is missing %q:\n%s", want, changes.Patch)
		}
	}
	if changes.Truncated || len(changes.OmittedFiles) != 0 {
		t.Errorf("a type change was reported as cut: %#v", changes.OmittedFiles)
	}
	// Listed once, with Git's own status for it. A symlink is not a regular
	// file the harness measures, so it is listed at zero bytes.
	want := []ChangedFile{
		{Path: "README.txt", Status: "T", Bytes: 0, Committed: true, Class: FileClassSource},
		{Path: "other.txt", Status: "A", Bytes: 10, Committed: true, Class: FileClassSource},
	}
	if !reflect.DeepEqual(changes.Files, want) {
		t.Fatalf("files = %#v, want %#v", changes.Files, want)
	}

	// Under a bound the type change is kept or dropped as one file, never as a
	// deletion shown without its creation.
	bounded, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxTotalBytes: 200})
	if err != nil {
		t.Fatalf("UnifiedChanges() bounded error = %v", err)
	}
	if len(bounded.OmittedFiles) != 1 || bounded.OmittedFiles[0].Path != "README.txt" || bounded.OmittedFiles[0].Reason != OmittedTooLarge {
		t.Fatalf("omitted files = %#v, want the type change dropped whole", bounded.OmittedFiles)
	}
	if strings.Contains(bounded.Patch, "README.txt") || !strings.Contains(bounded.Patch, "+beside it") {
		t.Fatalf("bounded patch = %q, want the file beside the dropped type change and nothing of the type change", bounded.Patch)
	}
}

// The tree listing: every file the change touches, with its size at the tip,
// whether it is binary, and whether an earlier attempt already committed it.
// The patch shows none of a binary and nothing distinguishes a committed file
// in it, and a reviewer not told a file is there infers it from whatever else
// passed — which is how a binary asset's approval came to rest on a link
// checker.
func TestManagerUnifiedChangesListsEveryFileOfTheChangeWithItsSize(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-listing", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	icon := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	writeFile(t, worktree.Path, filepath.Join("docs", "icon.png"), icon)
	writeFile(t, worktree.Path, "feature.go", "package feature\n")
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: published attempt")
	writeFile(t, worktree.Path, "README.txt", "test\nedited later\n")
	if err := os.Remove(filepath.Join(worktree.Path, "feature.go")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	writeFile(t, worktree.Path, "later.bin", "new\x00binary\n")
	writeFile(t, worktree.Path, "later.txt", "still working\n")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	// feature.go was committed and then deleted again, so against the base it
	// is no change at all and is rightly absent; README.txt is changed only in
	// the worktree; the icon is on the branch and binary; the two new files are
	// the worktree's own, one of them binary.
	want := []ChangedFile{
		{Path: "README.txt", Status: "M", Bytes: 18, Class: FileClassSource},
		{Path: "docs/icon.png", Status: "A", Bytes: int64(len(icon)), Binary: true, Committed: true, Class: FileClassSource},
		{Path: "later.bin", Status: "??", Bytes: 11, Binary: true, Class: FileClassSource},
		{Path: "later.txt", Status: "??", Bytes: 14, Class: FileClassSource},
	}
	if !reflect.DeepEqual(changes.Files, want) {
		t.Fatalf("files = %#v, want %#v", changes.Files, want)
	}
	if changes.FilesOmitted != 0 {
		t.Errorf("files omitted = %d, want none", changes.FilesOmitted)
	}
	if described := want[1].Describe(); described != "A docs/icon.png (16 bytes) — binary, already committed on this branch" {
		t.Errorf("described file = %q", described)
	}
	if changes.HeadCommit != worktree.HarnessCommit {
		t.Errorf("head commit = %q, want the tip the change was read at, %q", changes.HeadCommit, worktree.HarnessCommit)
	}

	// The listing is bounded on its own count, and the bound cuts the listing
	// alone: the patch is what it was.
	bounded, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxListedFiles: 2})
	if err != nil {
		t.Fatalf("UnifiedChanges() bounded error = %v", err)
	}
	if len(bounded.Files) != 2 || bounded.FilesOmitted != 2 || bounded.Patch != changes.Patch {
		t.Fatalf("bounded listing = %#v, omitted = %d", bounded.Files, bounded.FilesOmitted)
	}
}

// The handoff yoyodyne-ifd.121.5 was reported on, replayed: a first attempt
// commits a reduction that takes three thousand lines out of a README, and the
// round after it is a small uncommitted repair. The change a reviewer is handed
// on that round is the branch's whole diff against its base, so the reduction —
// the change the item is about — is in front of it, whole, beside the repair.
func TestManagerUnifiedChangesPresentsAReductionAnEarlierAttemptCommitted(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	var readme strings.Builder
	for i := 0; i < 3500; i++ {
		fmt.Fprintf(&readme, "line %d of the README nobody reads to the end\n", i)
	}
	writeFile(t, repository, "README.md", readme.String())
	runGit(t, repository, "add", "README.md")
	runGit(t, repository, "commit", "-m", "the README before the reduction")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-121-5", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reduced := strings.Join(strings.SplitAfter(readme.String(), "\n")[:400], "")
	writeFile(t, worktree.Path, "README.md", reduced)
	reduction := harnessCommit(t, worktree.Path, "yoyodyne: the README reduction")
	writeFile(t, worktree.Path, "widened.go", "package widened\n")
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: the widening")
	writeFile(t, worktree.Path, "repair.go", "package repair\n")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if changes.Truncated || len(changes.OmittedFiles) != 0 {
		t.Fatalf("the reduction is inside the default bounds and was cut anyway: %#v", changes.OmittedFiles)
	}
	if deleted := strings.Count(changes.Patch, "\n-line "); deleted != 3100 {
		t.Fatalf("patch removes %d README lines, want the whole 3100-line reduction", deleted)
	}
	for _, want := range []string{"diff --git a/README.md b/README.md", "+package widened", "+package repair"} {
		if !strings.Contains(changes.Patch, want) {
			t.Errorf("patch is missing %q", want)
		}
	}
	if !strings.Contains(changes.DiffStat, "3100 deletions") {
		t.Errorf("diff stat = %q, want it to count the reduction", changes.DiffStat)
	}
	if changes.BaseCommit != worktree.BaseCommit || changes.HeadCommit != worktree.HarnessCommit {
		t.Errorf("span = %s..%s, want the recorded base %s to the tip %s", changes.BaseCommit, changes.HeadCommit, worktree.BaseCommit, worktree.HarnessCommit)
	}
	if len(changes.Commits) != 2 || changes.Commits[0].Commit != reduction {
		t.Errorf("commits = %#v, want the reduction named first", changes.Commits)
	}
	if len(changes.Files) != 3 || changes.Files[0] != (ChangedFile{Path: "README.md", Status: "M", Bytes: int64(len(reduced)), Committed: true, Class: FileClassSource}) {
		t.Errorf("files = %#v, want the README listed as committed with its reduced size", changes.Files)
	}
}

func TestManagerUnifiedChangesRejectsAgentOwnedCommits(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-commit", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "new.txt", "new\n")
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")

	if _, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{}); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("UnifiedChanges() committed HEAD error = %v", err)
	}
}

func TestManagerRejectsUnsafeRootsAndTamperedOwnership(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := execution.OSProcessRunner{}
	for _, root := range []string{string(filepath.Separator), filepath.Join(repository, "worktrees"), filepath.Dir(repository)} {
		if _, err := New(Options{Runner: runner, RepositoryRoot: repository, WorktreeRoot: root, Timeout: testGitBudget}); err == nil {
			t.Errorf("New() root %q error = nil", root)
		}
	}

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1", BaseRef: "HEAD"}
	worktree, err := manager.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tampered := worktree
	tampered.Path = repository
	if _, err := manager.Inspect(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "owned path") {
		t.Fatalf("Inspect() tampered error = %v", err)
	}
	tampered = worktree
	tampered.Branch = "main"
	if _, err := manager.Inspect(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "owned branch") {
		t.Fatalf("Inspect() branch error = %v", err)
	}
}

// A test's own teardown is part of what it asserts: a run that passes every
// check and then fails deleting its temporary directory is still a red required
// check, and one that is usually spurious is one people stop reading. Both
// halves of that are guarded here, because both are a line someone can delete
// without any test noticing.
func TestRepositoryUnderTestLeavesNothingForTempDirToRace(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	// Writing commands otherwise detach "git maintenance run --auto", which
	// outlives them and is still working inside .git when TempDir deletes it.
	for _, setting := range []struct{ name, want string }{
		{"maintenance.auto", "false"},
		{"gc.auto", "0"},
	} {
		value, err := attemptGit(repository, "config", "--get", setting.name)
		if err != nil || strings.TrimSpace(value) != setting.want {
			t.Errorf("%s = %q (%v), want %q", setting.name, strings.TrimSpace(value), err, setting.want)
		}
	}

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-teardown", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "left behind\n")

	// Calling the cleanup here proves it removes a dirty worktree that was never
	// integrated. TempDir deletes directories and unregisters nothing, so an
	// unregistered repository is the state this has to reach.
	removeLinkedWorktrees(t, repository)
	if registrations := gitOutput(t, repository, "worktree", "list", "--porcelain"); strings.Contains(registrations, worktree.Path) {
		t.Errorf("worktree registration survived cleanup: %q", registrations)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory survived cleanup: %v", err)
	}

	// A second worktree is left registered on purpose, so the cleanup
	// newRepository registered has something real to remove rather than an
	// already-empty repository. Its own assertions fail this test if any
	// registration survives into TempDir's removal, which is the case that
	// every other test in this package relies on.
	survivor, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-teardown-survivor", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() survivor error = %v", err)
	}
	writeFile(t, survivor.Path, "work.txt", "left for teardown\n")
}

func newManager(t *testing.T, repository, worktreeRoot string) *Manager {
	t.Helper()
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   worktreeRoot,
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return manager
}

func newRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "config", "user.name", "Yoyodyne Test")
	runGit(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	disableBackgroundMaintenance(t, repository)
	// Registered after the first TempDir call above and therefore run before
	// TempDir's removal, so the repository is idle by the time Go deletes it.
	t.Cleanup(func() { removeLinkedWorktrees(t, repository) })
	if err := os.WriteFile(filepath.Join(repository, "README.txt"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGit(t, repository, "add", "README.txt")
	runGit(t, repository, "commit", "-m", "initial")
	return repository
}

// disableBackgroundMaintenance stops Git from handing this repository to a
// process that outlives the command which started it. Writing commands
// otherwise start "git maintenance run --auto --detach", which daemonizes into
// its own session: it escapes both the caller's wait and the process group the
// runner kills, and it is still working inside .git when a test's TempDir
// cleanup deletes that directory. A directory Go has already emptied and that
// then gains an entry again is exactly what makes the removal fail. Nothing
// under test needs maintenance, so none is started.
func disableBackgroundMaintenance(t *testing.T, repository string) {
	t.Helper()
	runGit(t, repository, "config", "maintenance.auto", "false")
	runGit(t, repository, "config", "gc.auto", "0")
}

// removeLinkedWorktrees makes a test responsible for the worktrees it
// registered. TempDir deletes directories and unregisters nothing, so a
// registration inside .git outlives the checkout it names and leaves the
// repository holding state no test still describes.
func removeLinkedWorktrees(t *testing.T, repository string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repository, ".git")); err != nil {
		return
	}
	clearUnfinishedRegistrations(t, repository)
	for _, path := range linkedWorktreePaths(t, repository) {
		// A worktree whose directory a test deleted on purpose cannot be
		// removed, only pruned. One that is still on disk must come off here —
		// including one a killed `git worktree add` left locked as
		// "initializing", which a single --force refuses; the tests that kill an
		// add on purpose leave that registration behind depending on where the
		// kill landed.
		if output, err := attemptGit(repository, "worktree", "remove", "--force", "--force", path); err != nil {
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("cleanup could not remove worktree %s: %v: %s", path, err, output)
			}
		}
	}
	if output, err := attemptGit(repository, "worktree", "prune"); err != nil {
		t.Errorf("cleanup could not prune worktree registrations: %v: %s", err, output)
	}
	if remaining := linkedWorktreePaths(t, repository); len(remaining) > 0 {
		t.Errorf("cleanup left worktree registrations behind: %v", remaining)
	}
}

// clearUnfinishedRegistrations takes out what a killed `git worktree add` left
// half-written, before anything lists the registrations. Where the kill landed
// while commondir was being written, `git worktree list` refuses the whole
// listing over that entry ("failed to read .git/worktrees/<name>/commondir"),
// so the tests that kill an add on purpose failed their own cleanup whenever
// the kill landed there. The harness clears such an entry on its next creation,
// once the grace for an add still in flight has passed; a test being torn down
// has nothing in flight, so it clears them at once, through the same reading
// and the same confined removal the harness uses.
func clearUnfinishedRegistrations(t *testing.T, repository string) {
	t.Helper()
	root, err := repowrite.NewRoot(filepath.Join(repository, ".git"))
	if err != nil {
		t.Errorf("cleanup could not resolve the Git directory: %v", err)
		return
	}
	unfinished, err := readUnfinishedRegistrations(root)
	if err != nil {
		t.Errorf("cleanup could not read the worktree registrations: %v", err)
		return
	}
	for _, entry := range unfinished {
		if err := clearRegistration(root, entry.Name); err != nil {
			t.Errorf("cleanup could not clear the unfinished registration %s: %v", entry.Name, err)
		}
	}
}

// linkedWorktreePaths names every worktree registered against repository apart
// from the primary checkout, which is the repository itself.
func linkedWorktreePaths(t *testing.T, repository string) []string {
	t.Helper()
	listing, err := attemptGit(repository, "worktree", "list", "--porcelain")
	if err != nil {
		t.Errorf("cleanup could not list worktrees: %v: %s", err, listing)
		return nil
	}
	var paths []string
	for _, line := range strings.Split(listing, "\n") {
		path, found := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !found || samePath(path, repository) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

// harnessCommit commits everything in a worktree the way publishing does, under
// the harness identity, and reports the commit it made — which is what durable
// run state records and what the ownership check then permits HEAD to be.
func harnessCommit(t *testing.T, worktreePath, message string) string {
	t.Helper()
	runGit(t, worktreePath, "add", "--all")
	runGit(t, worktreePath,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"commit", "-m", message)
	return gitLine(t, worktreePath, "rev-parse", "HEAD")
}

func runGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	if output, err := attemptGit(repository, args...); err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
}

// attemptGit runs a Git command whose failure is the caller's to interpret,
// rather than a test failure at the point of the call.
func attemptGit(repository string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func gitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v error = %v", args, err)
	}
	return string(output)
}

func writeFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", relative, err)
	}
}

// digestOf is the digest an omission record carries for content this test
// wrote. It is computed here rather than copied in as a hex literal so a test
// that changes the content it writes cannot go on asserting the old file's
// digest.
func digestOf(content string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
}

type recordingProcessRunner struct {
	delegate execution.ProcessRunner
	commands [][]string
	// bounds keeps every command as it was issued, so a test can hold Git to the
	// flat deadline that is exactly right for it.
	bounds []execution.Command
}

func (r *recordingProcessRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, append([]string(nil), command.Args...))
	r.bounds = append(r.bounds, command)
	return r.delegate.Run(ctx, command, observer)
}

type postCreateFailureRunner struct {
	delegate execution.ProcessRunner
	created  bool
}

func (r *postCreateFailureRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.created && containsArguments(command.Args, "worktree", "list") {
		return execution.ProcessResult{}, errors.New("injected post-create inspection failure")
	}
	result, err := r.delegate.Run(ctx, command, observer)
	if err == nil && result.Status == execution.ProcessSucceeded && containsArguments(command.Args, "worktree", "add") {
		r.created = true
	}
	return result, err
}

func containsArguments(arguments []string, first, second string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == first && arguments[index+1] == second {
			return true
		}
	}
	return false
}

// A Git command's duration is known and short, so a flat deadline is exactly
// the right bound for it. The activity bound that keeps a working provider
// alive would only mistake a quiet Git command for a stalled one.
func TestManagerBoundsGitCommandsByAFlatDeadlineOnly(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-bounds", BaseRef: "main"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(runner.bounds) == 0 {
		t.Fatal("no Git commands were recorded")
	}
	for _, command := range runner.bounds {
		if command.Timeout <= 0 {
			t.Fatalf("git %v carries no deadline", command.Args)
		}
		if command.IdleTimeout != 0 {
			t.Fatalf("git %v carries an idle bound of %s", command.Args, command.IdleTimeout)
		}
	}
}

// The one refusal a shape held still cannot produce is a lock that vanishes
// between Git seeing it and reading it, so its wording is pinned here instead:
// it is run again, and a lock Git failed to read for any other reason, or any
// other file's absence, is not.
func TestAVanishedLockIsRunAgainAndNothingElseAboutTheLockIs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		stderr string
		want   bool
	}{
		{"fatal: failed to read '.git/worktrees/creation-loop-5/locked': No such file or directory", true},
		{"fatal: failed to read /repo/.git/worktrees/run-1/locked: No such file or directory", true},
		{"fatal: failed to read .git/worktrees/run-1/commondir: Result too large", true},
		{"fatal: failed to read '.git/worktrees/run-1/locked': Permission denied", false},
		{"fatal: failed to read '.git/worktrees/run-1/gitdir': No such file or directory", false},
		{"fatal: failed to read '/repo/locked': No such file or directory", false},
	} {
		if got := crossedRegistration.MatchString(tc.stderr); got != tc.want {
			t.Errorf("crossedRegistration.MatchString(%q) = %t, want %t", tc.stderr, got, tc.want)
		}
	}
}

// What the re-run covers is decided by one regular expression over Git's own
// wording, and narrowing it to a single file would be a quiet regression if Git
// refused a walk over any of the others. So which shapes Git actually refuses is
// asked of Git rather than assumed: every file a registration carries is emptied
// and then removed in turn, and a walk is run over each shape.
//
// On git 2.50.1 exactly one of the twelve refuses anything — an empty commondir,
// which fails both the listing and a branch with the message the pattern is
// built from. A missing commondir does not, and neither does gitdir, HEAD, index
// or locked in either state: Git walks over them in silence. That is what makes
// the pattern's single file the whole of what there is to cover, and this test
// is what says so when a future Git changes its mind.
func TestOnlyAnEmptyCommondirMakesGitRefuseARegistrationWalk(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runGit(t, repository, "worktree", "add", "--quiet", "--detach", filepath.Join(t.TempDir(), "crossed"), "HEAD")
	registration := filepath.Join(repository, ".git", "worktrees", "crossed")
	saved := filepath.Join(t.TempDir(), "saved")
	// The branch is made before the walks so that moving it is a walk at all:
	// `git branch --force` reads the registrations to check the branch is not
	// checked out somewhere else, and a branch that does not exist yet cannot be
	// checked out anywhere, so Git skips the reading and the shape under test is
	// never reached.
	runGit(t, repository, "branch", "crossing-probe", "HEAD")

	for _, file := range []string{"commondir", "gitdir", "HEAD", "index", "locked"} {
		for _, shape := range []string{"empty", "missing"} {
			name := file + " " + shape
			// Every shape is made from the same finished entry, so one shape's
			// damage is never read as the next one's.
			copyTree(t, registration, saved)
			switch shape {
			case "empty":
				writeFile(t, registration, file, "")
			case "missing":
				if err := os.Remove(filepath.Join(registration, file)); err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Remove(%s) error = %v", name, err)
				}
			}

			refusals := 0
			for _, walk := range [][]string{
				{"worktree", "list", "--porcelain"},
				{"branch", "--force", "crossing-probe", "HEAD"},
			} {
				output, err := attemptGit(repository, walk...)
				if err == nil {
					continue
				}
				refusals++
				if !crossedRegistration.MatchString(output) {
					t.Errorf("git %v over %q refused with something the re-run does not cover: %s", walk, name, strings.TrimSpace(output))
				}
			}
			if file == "commondir" && shape == "empty" {
				if refusals != 2 {
					t.Errorf("%q refused %d of 2 walks, want both — the shape the whole re-run is for", name, refusals)
				}
			} else if refusals != 0 {
				t.Errorf("%q refused %d walk(s); Git refuses a shape this change believed it walked over in silence", name, refusals)
			}

			if err := os.RemoveAll(registration); err != nil {
				t.Fatalf("RemoveAll() error = %v", err)
			}
			copyTree(t, saved, registration)
			if err := os.RemoveAll(saved); err != nil {
				t.Fatalf("RemoveAll(saved) error = %v", err)
			}
		}
	}
}

// copyTree copies one flat directory of small files, which is what a worktree
// registration is.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", to, err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", from, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			copyTree(t, filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name()))
			continue
		}
		content, err := os.ReadFile(filepath.Join(from, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(to, entry.Name()), content, 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", entry.Name(), err)
		}
	}
}

// A checkout is work, and a budget that does not grow with it bounds the add by
// something it has nothing to do with. That is what killed three creations of
// yoyodyne-ifd.441 in three hours, one of them stopped at 87% of 1099 files, so
// this is what says the add's budget is about the tree rather than about Git
// commands in general.
func TestCreatingAWorktreeBudgetsTheCheckoutToTheTreeItWrites(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	// Enough files that the allowance for them is unmistakable beside the figure
	// for the command itself.
	for index := 0; index < 200; index++ {
		writeFile(t, repository, fmt.Sprintf("checked-out/file-%03d.txt", index), "content\n")
	}
	runGit(t, repository, "add", "--all")
	runGit(t, repository, "commit", "-m", "a tree worth checking out")

	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-budget", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	files, counted, err := manager.checkoutFiles(context.Background(), worktree.BaseCommit)
	if err != nil {
		t.Fatalf("checkoutFiles() error = %v", err)
	}
	if !counted {
		t.Fatal("checkoutFiles() could not count a tree it had just checked out")
	}
	if want := 201; files != want {
		t.Fatalf("checkout files = %d, want %d: the 200 written here and the repository's own README", files, want)
	}
	var add, reading time.Duration
	for _, command := range runner.bounds {
		switch {
		case containsArguments(command.Args, "worktree", "add"):
			add = command.Timeout
		case containsArguments(command.Args, "rev-parse", "--verify"):
			reading = command.Timeout
		}
	}
	if add == 0 || reading == 0 {
		t.Fatalf("add bound = %s, reading bound = %s; want both recorded", add, reading)
	}
	// A named budget is not scaled, so the add's bound is exactly that budget
	// plus the tree's allowance whatever the machine running this test is doing.
	if want := testGitBudget + time.Duration(files)*checkoutFileBudget; add != want {
		t.Fatalf("checkout bound = %s, want %s: the named budget plus %d file(s)", add, want, files)
	}
	// And a command that writes no tree is still held to the figure it always
	// was, so the allowance is the checkout's rather than every Git command's.
	if add <= reading {
		t.Fatalf("checkout bound = %s, reading bound = %s; want the checkout budgeted above a command that writes no tree", add, reading)
	}
}

// What a creation the harness's own budget ended leaves: no worktree, no branch,
// and a failure a reader can act on. Git says nothing about why a killed
// checkout stopped — its stderr is the progress meter and nothing else — so the
// failure is the harness's own sentence about the tree and the budget, and the
// sentinel is what the class it belongs to is read from rather than the words.
func TestAWorktreeCheckoutKilledByItsBudgetIsSaidInOneSentence(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	for index := 0; index < 200; index++ {
		writeFile(t, repository, fmt.Sprintf("checked-out/file-%03d.txt", index), "content\n")
	}
	runGit(t, repository, "add", "--all")
	runGit(t, repository, "commit", "-m", "a tree worth checking out")

	// A real deadline on the real add, rather than a killed process described to
	// the manager: what this is about is the manager reading its own runner's
	// answer, so the answer has to be one the runner actually produced.
	runner := shortCheckoutBudget{delegate: execution.OSProcessRunner{}, budget: time.Nanosecond}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-killed", BaseRef: "HEAD"}
	worktree, err := manager.Create(context.Background(), request)
	if !errors.Is(err, ErrCheckoutKilled) {
		t.Fatalf("Create() error = %v, want the checkout reported as ended by its budget", err)
	}
	if worktree.Path != "" {
		t.Fatalf("worktree = %#v, want nothing recorded for a creation that made none", worktree)
	}
	if !strings.Contains(err.Error(), "201 file(s)") || !strings.Contains(err.Error(), "no agent of this run was invoked") {
		t.Fatalf("Create() error = %q, want the tree and what did not happen named", err)
	}
	if strings.Contains(err.Error(), "Updating files") {
		t.Fatalf("Create() error = %q, want the cause rather than the checkout's progress", err)
	}
	// The branch the creation made first is taken back, exactly as it is when
	// the add fails for a reason of Git's own: nothing will ever check it out.
	branch := branchName(request.WorkItemID, request.RunID)
	result, err := manager.run(context.Background(), "-C", repository, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		t.Fatalf("show-ref error = %v", err)
	}
	if result.Status == execution.ProcessSucceeded {
		t.Fatalf("branch %s survived a creation that made no worktree", branch)
	}
}

// shortCheckoutBudget holds the creation's `git worktree add` to a budget it
// cannot possibly finish inside, and leaves every other command the manager
// runs on the budget the manager gave it — including the ones that take the
// abandoned branch back afterwards.
type shortCheckoutBudget struct {
	delegate execution.ProcessRunner
	budget   time.Duration
}

func (r shortCheckoutBudget) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if containsArguments(command.Args, "worktree", "add") {
		command.Timeout = r.budget
	}
	return r.delegate.Run(ctx, command, observer)
}

// Sizing the budget costs a Git command the creation did not used to run, so
// that command is a new way for a creation to die — and the one it must never
// die of is the deadline this item exists to remove. A count the harness ended
// is refused as the same environmental death the add's own budget produces,
// because the load that killed it a command earlier says exactly as little
// about the change.
func TestACountKilledByItsBudgetRefusesTheCreationAsAKilledCheckout(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := shortCountBudget{delegate: execution.OSProcessRunner{}, budget: time.Nanosecond}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-uncountable", BaseRef: "HEAD"}
	if _, err := manager.Create(context.Background(), request); !errors.Is(err, ErrCheckoutKilled) {
		t.Fatalf("Create() error = %v, want the killed count refused as a killed checkout", err)
	} else if !strings.Contains(err.Error(), "the checkout never started") {
		t.Fatalf("Create() error = %q, want the count named as what was ended", err)
	}
	// The count runs before anything is made, so there is nothing to take back —
	// and a branch left behind here would be a branch every later creation of
	// this item refuses over.
	branch := branchName(request.WorkItemID, request.RunID)
	result, err := manager.run(context.Background(), "-C", repository, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		t.Fatalf("show-ref error = %v", err)
	}
	if result.Status == execution.ProcessSucceeded {
		t.Fatalf("branch %s exists after a creation that never reached the branch", branch)
	}
}

// A count that failed for its own reasons is not a creation that fails. The
// count only sizes a bound, so a repository that would have checked out fine
// checks out, budgeted as an uncounted tree and saying so — refusing here would
// be this budget costing somebody a creation it was added to save.
func TestACountThatFailedLeavesTheCreationBudgetedAsAnUncountedTree(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &refusingCount{delegate: execution.OSProcessRunner{}}
	var notes []string
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Note:           func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) },
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-uncounted", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v, want a creation that runs on a count it could not read", err)
	}
	if worktree.Path == "" {
		t.Fatalf("worktree = %#v, want the creation to have made one", worktree)
	}
	if !runner.refused {
		t.Fatal("the count was never refused, so this proves nothing")
	}
	// What it was budgeted for is said rather than left to be inferred: an
	// operator reading a creation that later dies has to know the bound was a
	// stand-in rather than the tree's own size.
	var said bool
	for _, note := range notes {
		if strings.Contains(note, "bounded as an uncounted tree") {
			said = true
		}
	}
	if !said {
		t.Fatalf("notes = %q, want the fallback said out loud", notes)
	}
	// And the stand-in is what the add was actually given.
	if want := uncountedCheckoutFiles; checkoutAllowanceFiles(0, false) != want {
		t.Fatalf("uncounted allowance = %d file(s), want %d", checkoutAllowanceFiles(0, false), want)
	}
	if got := manager.checkoutTimeout(0, false); got != testGitBudget+uncountedCheckoutFiles*checkoutFileBudget {
		t.Fatalf("uncounted budget = %s, want %s: the named budget plus the stand-in tree's allowance", got, testGitBudget+uncountedCheckoutFiles*checkoutFileBudget)
	}
}

// A caller that names a Timeout is saying what a Git command is worth, and it
// cannot have meant the same figure for the one command that writes the tree
// out. A named budget that replaced the allowance would make this fix inert for
// every manager built with one, which is the same silent regression as never
// having made it.
func TestANamedBudgetStillGetsTheCheckoutsAllowance(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        7 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// Every other command is held to exactly what the caller named.
	if got := manager.localTimeout(); got != 7*time.Second {
		t.Fatalf("localTimeout() = %s, want the 7s the caller named", got)
	}
	if want := 7*time.Second + 1099*checkoutFileBudget; manager.checkoutTimeout(1099, true) != want {
		t.Fatalf("checkoutTimeout(1099) = %s, want %s: the named budget plus the tree's allowance", manager.checkoutTimeout(1099, true), want)
	}
	if want := 7*time.Second + uncountedCheckoutFiles*checkoutFileBudget; manager.checkoutTimeout(0, false) != want {
		t.Fatalf("checkoutTimeout(uncounted) = %s, want %s", manager.checkoutTimeout(0, false), want)
	}
}

// shortCountBudget holds the count that sizes the checkout to a budget it
// cannot finish inside, and leaves every other command the manager runs on the
// budget the manager gave it.
type shortCountBudget struct {
	delegate execution.ProcessRunner
	budget   time.Duration
}

func (r shortCountBudget) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if containsArguments(command.Args, "ls-tree", "-r") {
		command.Timeout = r.budget
	}
	return r.delegate.Run(ctx, command, observer)
}

// refusingCount makes the count fail the way Git fails rather than the way the
// harness ends a command: an answer, with an exit code, which must leave the
// creation running.
type refusingCount struct {
	delegate execution.ProcessRunner
	refused  bool
}

func (r *refusingCount) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if containsArguments(command.Args, "ls-tree", "-r") {
		r.refused = true
		return execution.ProcessResult{
			Status:   execution.ProcessFailed,
			ExitCode: 128,
			Stderr:   "fatal: not a tree object",
		}, nil
	}
	return r.delegate.Run(ctx, command, observer)
}

// The teardown every test here relies on survives the entry a killed add leaves
// when the kill lands on commondir. That shape is what failed
// TestAWorktreeCheckoutKilledByItsBudgetIsSaidInOneSentence's cleanup under
// `make race`, and only when the timing put the kill there; this puts it there
// every time.
func TestTeardownClearsARegistrationAKilledAddLeftHalfWritten(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	entry := filepath.Join(repository, ".git", "worktrees", "killed-add")
	if err := os.MkdirAll(entry, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for name, content := range map[string]string{"locked": "initializing", "gitdir": filepath.Join(t.TempDir(), "gone", ".git") + "\n", "commondir": ""} {
		if err := os.WriteFile(filepath.Join(entry, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if _, err := attemptGit(repository, "worktree", "list", "--porcelain"); err == nil {
		t.Fatal("git lists the registrations over a half-written commondir; the shape under test is not the one that fails")
	}

	removeLinkedWorktrees(t, repository)
	if output, err := attemptGit(repository, "worktree", "list", "--porcelain"); err != nil {
		t.Fatalf("the listing still fails after teardown: %v: %s", err, output)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("the unfinished registration survived teardown: %v", err)
	}
}

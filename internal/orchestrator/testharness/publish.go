package testharness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// AssertRemoteCarriesPromotion states the relationship a forge merge produces,
// which is the one the harness checks: the remote target is not the promoted
// commit — no merge method leaves it there — but it contains that commit
// unrewritten and carries exactly its content.
func AssertRemoteCarriesPromotion(t *testing.T, repository, remote, branch, promoted string) {
	t.Helper()
	target := PublishedCommit(t, remote, branch)
	if target == "" {
		t.Fatalf("remote %s does not exist", branch)
	}
	// The promoted commit and the remote target both live in the remote here, so
	// it can answer both questions about them.
	contained := exec.Command("git", "-C", remote, "merge-base", "--is-ancestor", promoted, target)
	if err := contained.Run(); err != nil {
		t.Errorf("remote %s at %q does not contain the promoted commit %q", branch, target, promoted)
	}
	remoteTree := strings.TrimSpace(GitOutput(t, remote, "rev-parse", target+"^{tree}"))
	localTree := strings.TrimSpace(GitOutput(t, repository, "rev-parse", promoted+"^{tree}"))
	if remoteTree != localTree {
		t.Errorf("remote %s carries tree %q, want the promoted commit's tree %q", branch, remoteTree, localTree)
	}
}

// ProtectBranch makes a bare remote refuse direct pushes to one branch, the way
// a repository that requires a pull request before anything reaches its target
// does. The refusal is proven rather than assumed: a hook that silently allowed
// the push would make the test resting on it meaningless.
func ProtectBranch(t *testing.T, remote, branch string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nwhile read -r _ _ ref; do\n\tif [ \"$ref\" = \"refs/heads/%s\" ]; then\n\t\techo \"branch %s requires a pull request\" >&2\n\t\texit 1\n\tfi\ndone\nexit 0\n", branch, branch)
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	clone := filepath.Join(t.TempDir(), "protected")
	RunPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	RunPipelineGit(t, clone, "config", "user.name", "Someone Else")
	RunPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	DisablePipelineMaintenance(t, clone)
	RunPipelineGit(t, clone, "commit", "--allow-empty", "-m", "a direct push")
	if err := exec.Command("git", "-C", clone, "push", "origin", "HEAD:refs/heads/"+branch).Run(); err == nil {
		t.Fatalf("the remote accepted a direct push to %s", branch)
	}
}

// DriftRemoteTarget moves the remote target branch onto work this repository
// has never seen, which is what someone else merging looks like from inside a
// run.
func DriftRemoteTarget(t *testing.T, remote, branch string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "elsewhere")
	RunPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	RunPipelineGit(t, clone, "config", "user.name", "Someone Else")
	RunPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	DisablePipelineMaintenance(t, clone)
	// Real content, not an empty commit: what makes this drift is that the remote
	// target no longer carries what the promotion was written against.
	if err := os.WriteFile(filepath.Join(clone, "elsewhere.txt"), []byte("someone else's work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	RunPipelineGit(t, clone, "add", ".")
	RunPipelineGit(t, clone, "commit", "-m", "someone else's work")
	RunPipelineGit(t, clone, "push", "origin", "HEAD:refs/heads/"+branch)
}

// NewPublishingPipeline is the automatic pipeline with publishing turned on.
func NewPublishingPipeline(t *testing.T, repository string, tracker orchestrator.WorkTracker, provider backend.Backend, forge orchestrator.PullRequests, commands []string) (orchestrator.Pipeline, *runstate.Store) {
	t.Helper()
	pipeline, store := NewAutomaticPipeline(t, repository, tracker, provider, commands)
	return Publishing(pipeline, forge), store
}

// Publishing turns a pipeline into one that publishes, the way Automatic turns
// one into a pipeline that reviews and integrates.
func Publishing(pipeline orchestrator.Pipeline, forge orchestrator.PullRequests) orchestrator.Pipeline {
	pipeline.Config.Approvals.Publishing = domain.ApprovalAutomatic
	pipeline.Publisher = forge
	return pipeline
}

// PublishedRepository is a pipeline repository with a bare remote that already
// carries its target branch.
func PublishedRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := PipelineRepository(t)
	return repository, AddBareRemote(t, repository)
}

// AddBareRemote gives an existing repository the remote publishing pushes to,
// carrying its target branch.
func AddBareRemote(t *testing.T, repository string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	RunPipelineGit(t, remote, "init", "--bare", "-b", "main")
	DisablePipelineMaintenance(t, remote)
	RunPipelineGit(t, repository, "remote", "add", "origin", remote)
	RunPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	return remote
}

func PublishedCommit(t *testing.T, repository, branch string) string {
	t.Helper()
	return strings.TrimSpace(GitOutput(t, repository, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch))
}

// QueuedFixture is a publishing run whose forge queues the merge, built over
// artifacts a second process can also see. Reconciliation is that second
// process: it settles a queued merge from the repository, the worktree root, and
// the run state store the run itself used.
type QueuedFixture struct {
	Repository   string
	Remote       string
	WorktreeRoot string
	Store        *runstate.Store
	Tracker      RecordingTracker
	Forge        *orchestratortest.Forge
	// Worktrees wraps the repository access the settlement makes, so a test can
	// drop the connection at one step of it. A fixture that sets none settles
	// through the real observer.
	Worktrees func(orchestrator.ReconcileWorktrees) orchestrator.ReconcileWorktrees
	// Sleep takes the backoff without taking the time.
	Sleep func(context.Context, time.Duration) error
	// Docket is the triage docket the sweep dockets and settles publications on,
	// where a test reads it; a fixture that sets none sweeps without one.
	Docket *MemoryDocket
}

func NewQueuedFixture(t *testing.T) QueuedFixture {
	t.Helper()
	repository, worktreeRoot, store := RestartableFixture(t)
	remote := AddBareRemote(t, repository)
	return QueuedFixture{
		Repository:   repository,
		Remote:       remote,
		WorktreeRoot: worktreeRoot,
		Store:        store,
		Tracker:      &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}},
		Forge:        &orchestratortest.Forge{Remote: remote, QueueMerge: true},
	}
}

// Run drives a whole run, which ends with its merge queued rather than made.
func (f QueuedFixture) Run(t *testing.T) orchestrator.Outcome {
	t.Helper()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, ApproveVerdict)
	pipeline := Publishing(Automatic(NewSharedPipeline(t, f.Repository, f.WorktreeRoot, f.Store, f.Tracker, provider, []string{"exit 0"}), provider), f.Forge)
	outcome, err := pipeline.Run(context.Background(), f.Tracker.Record().Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PullRequest == nil || !outcome.PullRequest.MergeQueued {
		t.Fatalf("outcome = %#v, want a run that finished with its merge queued", outcome)
	}
	return outcome
}

// Reconcile is the later sweep that asks the forge what became of the queued
// merge and settles the run on the answer.
func (f QueuedFixture) Reconcile(t *testing.T) []orchestrator.Reconciliation {
	t.Helper()
	results, err := f.Reconciler(t).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	return results
}

// Converge is the other half of that sweep: the local state brought onto what
// the forge has, once the runs themselves are settled.
func (f QueuedFixture) Converge(t *testing.T) orchestrator.Convergence {
	t.Helper()
	convergence, err := f.Reconciler(t).Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	return convergence
}

func (f QueuedFixture) Reconciler(t *testing.T) orchestrator.Reconciler {
	t.Helper()
	worktrees := NewObserver(t, f.Repository, f.WorktreeRoot)
	if f.Worktrees != nil {
		worktrees = f.Worktrees(worktrees)
	}
	reconciler := orchestrator.Reconciler{
		Tracker:   f.Tracker,
		Worktrees: worktrees,
		Store:     f.Store,
		Publisher: f.Forge,
		Drops:     f.Forge,
		Sleep:     f.Sleep,
	}
	if f.Docket != nil {
		reconciler.Docket = DocketerOverStore(f.Docket, f.Store, DocketConfig())
	}
	return reconciler
}

// RewriteRemoteTarget replaces the remote target branch with a history that does
// not contain the one this repository has. It is the movement DriftRemoteTarget
// is not: a fast-forward answers a target that grew, and nothing answers a
// target somebody rewrote.
func RewriteRemoteTarget(t *testing.T, remote, branch string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "rewritten")
	RunPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	RunPipelineGit(t, clone, "config", "user.name", "Someone Else")
	RunPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	DisablePipelineMaintenance(t, clone)
	RunPipelineGit(t, clone, "checkout", "--orphan", "rewritten")
	RunPipelineGit(t, clone, "rm", "-r", "-f", "-q", ".")
	if err := os.WriteFile(filepath.Join(clone, "rewritten.txt"), []byte("a history of its own\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	RunPipelineGit(t, clone, "add", ".")
	RunPipelineGit(t, clone, "commit", "-m", "a rewritten history")
	RunPipelineGit(t, clone, "push", "--force", "origin", "HEAD:refs/heads/"+branch)
}

package testharness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// HeldItems is what the read model holds out of the pull, by item, read from the
// same store the sweep settled: the hold has to lift because the facts changed,
// and this is the derivation the scheduler and every operator surface read.
type HeldItems map[string]bool

func HeldItemsOf(t *testing.T, store *runstate.Store, items ...string) HeldItems {
	t.Helper()
	holds, err := readmodel.HeldForAPerson(context.Background(), store, store.Triage(), nil)
	if err != nil {
		t.Fatalf("reading what the read model holds out of the pull: %v", err)
	}
	held := make(HeldItems)
	for _, id := range items {
		_, holding := holds.Reason(id)
		held[id] = holding
	}
	return held
}

// LandAnotherChange puts somebody else's commit on the remote's target branch,
// which is what every merge after this one's does to the tip: it carries content
// this promotion never had.
func LandAnotherChange(t *testing.T, remote string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "elsewhere")
	for _, arguments := range [][]string{
		{"clone", "--quiet", remote, clone},
		{"-C", clone, "config", "user.name", "Someone Else"},
		{"-C", clone, "config", "user.email", "someone@example.invalid"},
	} {
		if output, err := exec.Command("git", arguments...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
	DisablePipelineMaintenance(t, clone)
	if err := os.WriteFile(filepath.Join(clone, "elsewhere.txt"), []byte("someone else's work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	RunPipelineGit(t, clone, "add", ".")
	RunPipelineGit(t, clone, "commit", "--quiet", "-m", "someone else's work")
	RunPipelineGit(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return PublishedCommit(t, remote, "main")
}

type RefusingBranch struct {
	orchestrator.ReconcileWorktrees
}

func (r *RefusingBranch) DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error {
	if worktree.WorkItemID == "yoyodyne-task" {
		return fmt.Errorf("remote branch %s is at an unexpected tip, want the published commit %s", worktree.Branch, commit)
	}
	return r.ReconcileWorktrees.DeleteRemoteBranch(ctx, worktree, commit)
}

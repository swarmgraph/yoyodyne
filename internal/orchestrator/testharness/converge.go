package testharness

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// NewSweepManager is the real worktree manager, which the sweep needs as a
// concrete type here because these tests create the checkouts it retires.
func NewSweepManager(t *testing.T, repository, worktreeRoot string) *gitworktree.Manager {
	t.Helper()
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          worktreeRoot,
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
		Timeout:               TestGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	return worktrees
}

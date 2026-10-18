package testharness

import (
	"context"
	"errors"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func NewObserver(t *testing.T, repository, worktreeRoot string) orchestrator.ReconcileWorktrees {
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

// ReconcileSweep runs exactly one reconciliation over the durable state a
// restarted process would find.
func ReconcileSweep(t *testing.T, repository, worktreeRoot string, store orchestrator.ReconcileStore, tracker orchestrator.WorkTracker) []orchestrator.Reconciliation {
	t.Helper()
	worktrees := NewObserver(t, repository, worktreeRoot)
	results, err := orchestrator.Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	return results
}

// HaltingStore stops accepting writes for good once a run reaches a chosen
// phase, after letting `After` writes through there. Nothing the run does
// afterwards survives, including the terminal failure record it would normally
// write, so what is left on disk is what a killed process leaves.
type HaltingStore struct {
	orchestrator.StateStore
	At     runstate.Phase
	After  int
	Seen   int
	Halted bool
}

func (s *HaltingStore) Save(state runstate.State) error {
	if !s.Halted && state.Phase == s.At {
		if s.Seen >= s.After {
			s.Halted = true
		} else {
			s.Seen++
		}
	}
	if s.Halted {
		return errors.New("state store is unavailable")
	}
	return s.StateStore.Save(state)
}

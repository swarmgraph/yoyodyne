package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A Git lock file in a run's checkout, met by a granted repair round.
//
// A round that stops on index.lock left in its checkout's Git directory must
// not spend the item's repair on it. Here the round's developer
// writes its change and leaves index.lock behind. Where nothing is using it,
// the harness removes it, says so on the run, and the round carries on. Where
// something live is — this test process, holding it open — the harness leaves
// it alone, the run stops naming why, and the grant the round consumed is given
// back, because nothing judged the change.

// checkoutLock is where a run's checkout keeps index.lock.
func checkoutLock(t *testing.T, directory string) string {
	t.Helper()
	output, err := exec.Command("git", "-C", directory, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Errorf("rev-parse --absolute-git-dir in %s: %v", directory, err)
		return ""
	}
	return filepath.Join(strings.TrimSpace(string(output)), "index.lock")
}

func TestALiveGitLockStopsAGrantedRoundAndGivesTheGrantBack(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is not installed here")
	}
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, stopped.RunID), 2, docketedNow, environmentalCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	continueOnGrant(t, store, tracker, stopped.RunID)

	var held *os.File
	t.Cleanup(func() {
		if held != nil {
			_ = held.Close()
		}
	})
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "repaired.txt"), []byte("the repair\n"), 0o600); err != nil {
			return err
		}
		lock := checkoutLock(t, request.WorkingDirectory)
		file, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("take %s: %w", lock, err)
		}
		held = file
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := continuing.Run(context.Background(), tracker.Item.ID)
	if !errors.Is(err, gitworktree.ErrGitLockHeld) {
		t.Fatalf("Run() error = %v, want the round stopped on the live lock", err)
	}
	if outcome.Environmental == nil || outcome.Environmental.Cause != runstate.CauseGitLockHeld {
		t.Fatalf("environmental = %#v, want the round classified by the lock it would not remove", outcome.Environmental)
	}
	if !outcome.Environmental.Settled || !outcome.Environmental.Refused || !outcome.Environmental.GrantReturned {
		t.Fatalf("environmental = %#v, want the round refused and its grant returned", outcome.Environmental)
	}
	refused, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if carried := refused.CarriedOutRepairAttempts(); carried != 0 {
		t.Fatalf("carried-out repair attempts = %d, want the grant untouched", carried)
	}
	assertSavedStopClass(t, store, stopped.RunID, runstate.CauseGitLockHeld.StopClass())
	if len(refused.GitLocks) != 1 || refused.GitLocks[0].Removed || !strings.Contains(refused.GitLocks[0].Why, "live process") {
		t.Fatalf("git_locks = %#v, want the one lock recorded as left alone because a live process was using it", refused.GitLocks)
	}
	if _, err := os.Lstat(refused.GitLocks[0].Path); err != nil {
		t.Fatalf("the live lock was touched: %v", err)
	}
}

func TestALeftoverGitLockIsRemovedAndRecordedAndTheRunGoesOn(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(checkoutLock(t, request.WorkingDirectory), nil, 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() over a leftover lock error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(state.GitLocks) == 0 || !state.GitLocks[0].Removed || !strings.Contains(state.GitLocks[0].Why, "removed") {
		t.Fatalf("git_locks = %#v, want the leftover lock recorded as removed", state.GitLocks)
	}
	if state.Environmental != nil {
		t.Fatalf("environmental = %#v, want a removed lock to stop nothing", state.Environmental)
	}
}

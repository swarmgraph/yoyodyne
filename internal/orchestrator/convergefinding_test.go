package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestConvergeClearsARefusedBranchFindingAfterExternalRemoval(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	manager := newSweepManager(t, repository, worktreeRoot)
	state := settledRunWithCheckout(t, manager, store, 0)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Retire the leftover branch", Status: "open"}}
	worktrees := &refuseLocalBranch{ReconcileWorktrees: manager, refuse: true}
	reconciler := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}
	if sweep, swept := reconciler.sweepWorktree(context.Background(), state); !swept || sweep.Failure != "" {
		t.Fatalf("worktree sweep = %+v, swept = %t", sweep, swept)
	}
	first, err := reconciler.Converge(context.Background())
	if err != nil || len(first.Branches) != 1 || first.Branches[0].Finding == nil {
		t.Fatalf("first convergence = %+v, %v", first, err)
	}
	notes := len(tracker.NoteRecords)
	runPipelineGit(t, repository, "branch", "-D", state.Branch)
	worktrees.refuse = false
	// Finding maintenance must respect the same lease as cleanup.
	_, lease, err := store.AdoptRun(context.Background(), state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Converge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saved := loadRun(t, store, state.RunID); len(saved.ReconcileFindings) != 1 {
		t.Fatal("a sweep changed the finding while another process held the run")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if _, err := reconciler.Converge(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if saved := loadRun(t, store, state.RunID); !saved.BranchRemoved || len(saved.ReconcileFindings) != 0 {
		t.Fatalf("externally removed branch still has a finding: %+v", saved.ReconcileFindings)
	}
	if len(tracker.NoteRecords) != notes {
		t.Fatal("a resolved branch refusal was announced again")
	}
	standing := readmodel.ReadStanding(context.Background(), readmodel.Sources{Runs: store})
	for _, attention := range standing.NeedsHuman {
		if attention.OwedStep != nil && attention.ID == state.RunID {
			t.Fatalf("the shared attention surface still reports the removed branch: %+v", attention)
		}
	}
}

type refuseLocalBranch struct {
	ReconcileWorktrees
	refuse bool
}

func (w *refuseLocalBranch) RemoveMergedBranch(ctx context.Context, branch, target string) (gitworktree.Removal, error) {
	if w.refuse {
		return gitworktree.Removal{}, errors.New("branch deletion refused")
	}
	return w.ReconcileWorktrees.RemoveMergedBranch(ctx, branch, target)
}

func TestConvergeRetriesNotesAfterTheWorktreeHasBeenRetired(t *testing.T) {
	for _, failures := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d preservation refusals", failures), func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store := restartableFixture(t)
			manager := newSweepManager(t, repository, worktreeRoot)
			oldest := settledRunWithCheckout(t, manager, store, 0)
			for index := 1; index <= settledWorktreeTail; index++ {
				settledRunWithCheckout(t, manager, store, index)
			}
			writeSweepFile(t, filepath.Join(oldest.WorktreePath, "half-done.txt"), "preserve this work\n")
			notes := &durableFindingNotes{Tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: oldest.WorkItemID, Title: "Preserve the retired work", Status: "open"}}}
			tracker := &refuseRetirementNotes{WorkTracker: notes, preservationFailures: failures, findingFailures: 1}
			reconciler := Reconciler{Tracker: tracker, Worktrees: manager, Store: store}
			first, err := reconciler.Converge(context.Background())
			if err != nil || len(first.Worktrees) != 1 || !first.Worktrees[0].Removed || first.Worktrees[0].ItemProblem == "" || first.Worktrees[0].FindingProblem == "" {
				t.Fatalf("first convergence = %+v, %v", first, err)
			}
			saved := loadRun(t, store, oldest.RunID)
			if !saved.WorktreeRemoved || saved.PreservedWorkRef == "" || len(saved.ReconcileFindings) != 1 || !saved.ReconcileFindings[0].Pending {
				t.Fatalf("retirement lost its pending notes: %+v", saved)
			}
			for pass := 1; pass <= failures; pass++ {
				result, err := reconciler.Converge(context.Background())
				if err != nil || len(result.Worktrees) != 1 {
					t.Fatalf("retry convergence = %+v, %v, want the retired checkout's notes retried", result, err)
				}
				if result.Worktrees[0].Removed || result.Worktrees[0].FindingProblem != "" {
					t.Fatalf("retry must deliver notes without removing the worktree again: %+v", result.Worktrees[0])
				}
			}
			saved = loadRun(t, store, oldest.RunID)
			if len(saved.ReconcileFindings) != 0 || saved.PreservedWorkNotedAt == nil || !strings.Contains(notes.Item.Notes, renderPreservedWorkNotes(saved, saved.PreservedWorkRef)) {
				t.Fatalf("retirement obligation was not settled: findings %+v, notes %q", saved.ReconcileFindings, notes.Item.Notes)
			}
			if len(notes.NoteRecords) != 2 {
				t.Fatalf("notes = %q, want the preservation and finding notes delivered once each", notes.NoteRecords)
			}
			if result, err := reconciler.Converge(context.Background()); err != nil || len(result.Worktrees) != 0 || len(notes.NoteRecords) != 2 {
				t.Fatalf("settled retirement was repeated: %+v, %v, notes %q", result, err, notes.NoteRecords)
			}
		})
	}
}

func TestConvergeRetriesARetirementNoteWithoutRepeatingItsDeliveredCopy(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	manager := newSweepManager(t, repository, worktreeRoot)
	state := settledRunWithCheckout(t, manager, store, 0)
	writeSweepFile(t, filepath.Join(state.WorktreePath, "half-done.txt"), "preserve this work\n")
	tracker := &durableFindingNotes{Tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Record the preserved work", Status: "open"}}}
	markers := &refusedPreservationMarker{ReconcileStore: store, refuse: true}
	reconciler := Reconciler{Tracker: tracker, Worktrees: manager, Store: markers}
	// The note was delivered, but the process could not record that delivery.
	// No finding was saved yet, so the retired run itself must remain eligible.
	sweep, swept := reconciler.sweepWorktree(context.Background(), state)
	if !swept || !sweep.Removed || !strings.Contains(sweep.ItemProblem, "preservation marker refused") {
		t.Fatalf("retirement = %+v, swept = %t", sweep, swept)
	}
	if saved := loadRun(t, store, state.RunID); !saved.WorktreeRemoved || saved.PreservedWorkNotedAt != nil || len(saved.ReconcileFindings) != 0 {
		t.Fatalf("unexpected delivery state: %+v", saved)
	}
	result, err := reconciler.Converge(context.Background())
	if err != nil || len(result.Worktrees) != 1 || result.Worktrees[0].Finding == nil {
		t.Fatalf("retry convergence = %+v, %v", result, err)
	}
	if len(tracker.NoteRecords) != 2 {
		t.Fatalf("notes = %q, want one recovery note and one finding", tracker.NoteRecords)
	}
	markers.refuse = false
	for pass := 0; pass < 2; pass++ {
		if _, err := reconciler.Converge(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if saved := loadRun(t, store, state.RunID); saved.PreservedWorkNotedAt == nil || len(saved.ReconcileFindings) != 0 {
		t.Fatalf("delivery still outstanding: %+v", saved)
	}
	if len(tracker.NoteRecords) != 2 {
		t.Fatalf("recording the marker repeated a delivered note: %q", tracker.NoteRecords)
	}
}

type refusedPreservationMarker struct {
	ReconcileStore
	refuse bool
}

func (s *refusedPreservationMarker) Save(state runstate.State) error {
	if s.refuse && state.PreservedWorkNotedAt != nil {
		return errors.New("preservation marker refused")
	}
	return s.ReconcileStore.Save(state)
}

type refuseRetirementNotes struct {
	WorkTracker
	preservationFailures int
	findingFailures      int
}

func (t *refuseRetirementNotes) RecordOutcome(ctx context.Context, id, note string) (beads.WorkItem, error) {
	if strings.HasPrefix(note, "Yoyodyne retired the checkout") && t.preservationFailures > 0 {
		t.preservationFailures--
		return beads.WorkItem{}, errors.New("preservation note refused")
	}
	if strings.HasPrefix(note, "Settlement finding:") && t.findingFailures > 0 {
		t.findingFailures--
		return beads.WorkItem{}, errors.New("finding note refused")
	}
	return t.WorkTracker.RecordOutcome(ctx, id, note)
}

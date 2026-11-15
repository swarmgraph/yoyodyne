package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// busyStoreBD stands in for bd over a store so busy that a listing of every
// work item runs past its limit. It answers a `bd show` of any number of the
// items it holds and a note appended to one, and keeps every command it was
// given so a test can say which reads a sweep made.
type busyStoreBD struct {
	mu       sync.Mutex
	items    map[string]beads.WorkItem
	commands [][]string
}

func (b *busyStoreBD) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands = append(b.commands, append([]string(nil), command.Args...))
	switch command.Args[0] {
	case "list", "ready", "export":
		return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1, Stderr: "stopped at its limit"}, nil
	case "update":
		item, held := b.items[command.Args[1]]
		note, appends := strings.CutPrefix(command.Args[2], "--append-notes=")
		if !held || !appends {
			break
		}
		item.Notes = strings.TrimPrefix(item.Notes+"\n\n"+note, "\n\n")
		b.items[item.ID] = item
		return b.answer([]string{item.ID})
	case "show":
		return b.answer(command.Args[1:])
	}
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "this stand-in answers reads and notes only"}, nil
}

func (b *busyStoreBD) answer(ids []string) (execution.ProcessResult, error) {
	{
		var rows []map[string]any
		for _, id := range ids {
			if item, held := b.items[id]; held {
				rows = append(rows, map[string]any{"id": item.ID, "title": item.Title, "status": item.Status, "notes": item.Notes, "priority": 1, "issue_type": "task"})
			}
		}
		if len(rows) == 0 {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Error fetching: no issue found matching"}, nil
		}
		data, err := json.Marshal(rows)
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(data)}, err
	}
}

// forget drops the commands kept so far.
func (b *busyStoreBD) forget() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands = nil
}

func (b *busyStoreBD) asked() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.commands)
}

// The live case the sweep kept timing out on: finished runs whose clean-up is
// refused pass after pass, each read on its own and each waiting its turn for
// the store. On a store where listing every work item would run past its limit,
// the sweep still finishes; it reads the outstanding runs' items in one call,
// lists nothing, and keeps every run whose clean-up was refused exactly as a
// sweep that read them one by one does, with its worktree and its failure on
// the record.
func TestReconcileReadsItsRunsItemsInOneCallAndListsNothing(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	finished := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseCleaningUp}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, finished, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), finished.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	if !finished.Closed {
		t.Fatal("the run this test needs did not close its item before its clean-up")
	}

	// A second finished run of another item, stopped at its clean-up the same
	// way. It runs against a store of its own, because the suite's pipeline
	// takes one developer at a time, and its record is then kept beside the
	// first.
	const secondRunID = "run-fedcba9876543210fedcba9876543210"
	secondStore, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	secondItem := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-other", Title: "Other", Status: "open"}}
	otherProvider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "other.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	second := automatic(newSharedPipeline(t, repository, worktreeRoot, &haltingStore{StateStore: secondStore, at: runstate.PhaseCleaningUp}, secondItem, otherProvider, []string{"exit 0"}), otherProvider)
	second.NewRunID = func() (string, error) { return secondRunID, nil }
	if _, err := second.Run(context.Background(), secondItem.Item.ID); err == nil {
		t.Fatal("second interrupted Run() error = nil")
	}
	secondRecord, err := secondStore.Load(secondRunID)
	if err != nil {
		t.Fatalf("Load() of the second run error = %v", err)
	}
	secondRecord.Revision = 0
	if err := store.Create(secondRecord); err != nil {
		t.Fatalf("Create() of the second run error = %v", err)
	}
	recorded, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	bd := &busyStoreBD{items: map[string]beads.WorkItem{
		finished.Item.ID: {ID: finished.Item.ID, Title: finished.Item.Title, Status: "closed"},
		"yoyodyne-other": {ID: "yoyodyne-other", Title: "Other", Status: "closed"},
	}}
	tracker := beads.Client{Runner: bd, Reads: &beads.ItemReads{}}
	sweep := func() []Reconciliation {
		t.Helper()
		worktrees := &refusingCleanup{ReconcileWorktrees: newObserver(t, repository, worktreeRoot), refusals: 100}
		results, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		return results
	}
	// The first sweep finds the clean-up refused and writes that onto the item
	// once; every sweep after it finds the same, as the live ones did.
	sweep()
	if !strings.Contains(bd.items[finished.Item.ID].Notes, "Settlement finding") {
		t.Fatalf("the first sweep did not write its finding onto the item: %q", bd.items[finished.Item.ID].Notes)
	}
	bd.forget()

	results := sweep()
	if len(results) != 2 {
		t.Fatalf("reconciliation = %#v, want both runs", results)
	}
	for index, result := range results {
		if result.Action != ActionUnsettled || !strings.Contains(result.Failure, "worktree is busy") {
			t.Fatalf("reconciliation %d = %#v, want the run kept with its clean-up refused", index, result)
		}
		kept, err := store.Load(result.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if !kept.Outstanding() || kept.CleanupFailure == "" {
			t.Fatalf("kept state = %#v, want an outstanding run carrying its clean-up failure", kept)
		}
	}
	if _, err := os.Stat(filepath.Join(recorded.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the refused clean-up lost the run's worktree: %v", err)
	}
	ids := []string{finished.Item.ID, "yoyodyne-other"}
	if results[0].RunID != pipelineRunID {
		ids[0], ids[1] = ids[1], ids[0]
	}
	want := [][]string{{"show", ids[0], ids[1], "--json"}}
	if asked := bd.asked(); !slices.EqualFunc(asked, want, slices.Equal) {
		t.Fatalf("the sweep asked bd %v, want only the one read of its runs' items", asked)
	}
	if tracker.Reads.Held() != 0 {
		t.Fatalf("the sweep left %d items held after it ended", tracker.Reads.Held())
	}

	// Without the read ahead, the same sweep reads each item on its own, waiting
	// its turn for the store once for each.
	bd.forget()
	sweepWithout := Reconciler{Tracker: beads.Client{Runner: bd},
		Worktrees: &refusingCleanup{ReconcileWorktrees: newObserver(t, repository, worktreeRoot), refusals: 100}, Store: store}
	if _, err := sweepWithout.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() without the read ahead error = %v", err)
	}
	alone := [][]string{{"show", ids[0], "--json"}, {"show", ids[1], "--json"}}
	if asked := bd.asked(); !slices.EqualFunc(asked, alone, slices.Equal) {
		t.Fatalf("the sweep without the read ahead asked bd %v, want the reads of one item each it makes on its own, %v", asked, alone)
	}
}

// Several outstanding runs are read in one call, each item once, and a run that
// publishes a document is not read through the tracker at all.
func TestReconcilePrefetchesEachOutstandingItemOnce(t *testing.T) {
	t.Parallel()

	prefetcher := &recordingPrefetcher{}
	outstanding := []runstate.State{
		{WorkItemID: "yoyodyne-a"},
		{WorkItemID: "yoyodyne-b"},
		{WorkItemID: "yoyodyne-a"},
		{WorkItemID: "yoyodyne-doc", Document: &runstate.DocumentPublication{}},
		{WorkItemID: ""},
	}
	release := Reconciler{Tracker: prefetcher}.prefetchItems(context.Background(), outstanding)
	if !slices.Equal(prefetcher.asked, []string{"yoyodyne-a", "yoyodyne-b"}) {
		t.Fatalf("prefetched %v, want each tracker item once", prefetcher.asked)
	}
	release()
	if !prefetcher.released {
		t.Fatal("the hold was not ended")
	}
	Reconciler{Tracker: &orchestratortest.Tracker{}}.prefetchItems(context.Background(), outstanding)()
}

type recordingPrefetcher struct {
	orchestratortest.Tracker
	asked    []string
	released bool
}

func (p *recordingPrefetcher) PrefetchItems(_ context.Context, ids []string) func() {
	p.asked = append(p.asked, ids...)
	return func() { p.released = true }
}

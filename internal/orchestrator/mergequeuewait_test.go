package orchestrator

// A run whose approved change waits in the harness's merge queue is the
// queue's to finish. The sweep that settles interrupted runs used to read such
// a run, when it had listed it while its process was still recording the
// admission, as one interrupted in its cleaning-up phase with nothing
// integrated, and stopped it. These tests hold the sweep to leaving it waiting
// while its entry stands, stopping it with the reason once the entry is gone,
// and cleaning it up once the queue lands it.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// listedEarlier is a run store whose listing is a snapshot taken before the
// sweep adopts each run, as a slow sweep's is: the runs it lists are the ones
// given, as they stood then, and adopting one reads the record as it stands now.
type listedEarlier struct {
	ReconcileStore
	listed []runstate.State
}

func (s listedEarlier) Outstanding() ([]runstate.State, error) {
	return s.listed, nil
}

// queuedRun runs the fixture with the merge queue switched on and returns the
// run as it ended: succeeded, cleaning up, its change admitted and not landed.
func queuedRun(t *testing.T) (*queueRun, runstate.State) {
	t.Helper()
	r := newQueueRun(t, false)
	r.pipeline.Config.Execution.MergeQueue = true
	outcome := r.run(t)
	if outcome.MergeQueue == nil || outcome.Integration != nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", outcome)
	}
	state, err := r.runs.Load(outcome.RunID)
	if err != nil || !state.AwaitingMergeQueue() || state.Phase != runstate.PhaseCleaningUp {
		t.Fatalf("run = %#v, %v; want it ended waiting on the queue in its cleaning-up phase", state, err)
	}
	return r, state
}

// asListedInFlight is the run as a sweep listing it while its process was still
// recording the admission saw it.
func asListedInFlight(state runstate.State) runstate.State {
	listed := state
	listed.Status = runstate.StatusRunning
	listed.Phase = runstate.PhaseIntegrating
	listed.CompletedAt = nil
	return listed
}

func (r *queueRun) reconciler(store ReconcileStore, queues ReconcileMergeQueues) Reconciler {
	return Reconciler{Tracker: r.tracker, Worktrees: r.pipeline.Worktrees.(*gitworktree.Manager), Store: store, MergeQueues: queues}
}

func reconcileOne(t *testing.T, reconciler Reconciler) Reconciliation {
	t.Helper()
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Reconcile() = %#v, want one run settled", results)
	}
	return results[0]
}

func TestReconcileLeavesARunWaitingInTheMergeQueueAndCleansItUpOnceItLands(t *testing.T) {
	t.Parallel()

	r, queued := queuedRun(t)
	for _, queues := range []ReconcileMergeQueues{r.queue, nil} {
		result := reconcileOne(t, r.reconciler(listedEarlier{ReconcileStore: r.runs, listed: []runstate.State{asListedInFlight(queued)}}, queues))
		if result.Action != ActionQueued || result.Failure != "" {
			t.Fatalf("reconciliation = %#v, want the run left waiting on the queue", result)
		}
		if queues != nil && !strings.Contains(result.Detail, "waiting on the merge queue") {
			t.Fatalf("detail = %q, want it to say the run waits on the queue", result.Detail)
		}
		saved, err := r.runs.Load(queued.RunID)
		if err != nil || !saved.AwaitingMergeQueue() || saved.Blocker != "" || saved.Failure != "" {
			t.Fatalf("run = %#v, %v; want it still waiting on the queue with nothing stopping it", saved, err)
		}
		if r.tracker.Blocked {
			t.Fatal("the sweep blocked the item of a change waiting in the queue")
		}
	}

	// The queue lands a merge commit whose second parent is the approved head,
	// and records that commit as the run's integration; the run's own branch and
	// worktree stand at the approved head, and are cleaned up all the same.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if pass := r.driver().Pass(ctx, queued.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	landed, err := r.runs.Load(queued.RunID)
	if err != nil || landed.Integration == nil || landed.Integration.SourceCommit == queued.MergeQueue.ApprovedHead {
		t.Fatalf("run = %#v, %v; want the queue's own commit recorded as the integration", landed.Integration, err)
	}
	result := reconcileOne(t, r.reconciler(r.runs, r.queue))
	if result.Action != ActionCompleted || result.Failure != "" || !result.WorktreeRemoved || !result.BranchRemoved {
		t.Fatalf("reconciliation = %#v, want the landed run cleaned up", result)
	}
}

func TestReconcileEndsARunWhoseProcessDiedAfterAdmittingItsChangeAsWaiting(t *testing.T) {
	t.Parallel()

	r := newQueueRun(t, false)
	r.pipeline.Config.Execution.MergeQueue = true
	// The process dies once the admission is on the run's record: nothing it
	// writes after that survives, so the run is still recorded in flight.
	dying := &diesAfterAdmission{StateStore: r.pipeline.Store}
	r.pipeline.Store = dying
	if _, err := r.pipeline.Run(context.Background(), "yoyodyne-task"); err == nil {
		t.Fatal("Run() error = nil, want the process to have died after the admission")
	}
	listed, err := r.runs.Outstanding()
	if err != nil || len(listed) != 1 || listed[0].MergeQueue == nil || listed[0].Status.Terminal() {
		t.Fatalf("outstanding = %#v, %v; want the run in flight with its admission recorded", listed, err)
	}
	result := reconcileOne(t, r.reconciler(r.runs, r.queue))
	if result.Action != ActionQueued || result.Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run ended waiting on the queue", result)
	}
	saved, err := r.runs.Load(listed[0].RunID)
	if err != nil || !saved.AwaitingMergeQueue() || saved.Blocker != "" {
		t.Fatalf("run = %#v, %v; want it ended succeeded and waiting", saved, err)
	}
}

// diesAfterAdmission is a run store that stops accepting writes for good once
// a run's merge queue admission is saved, which is what a process killed right
// after recording the admission leaves on disk.
type diesAfterAdmission struct {
	StateStore
	dead bool
}

func (s *diesAfterAdmission) Save(state runstate.State) error {
	if s.dead {
		return errors.New("state store is unavailable")
	}
	if err := s.StateStore.Save(state); err != nil {
		return err
	}
	s.dead = state.MergeQueue != nil
	return nil
}

func (s *diesAfterAdmission) Update(ctx context.Context, runID string, update runstate.RunUpdate) (runstate.State, error) {
	if s.dead {
		return runstate.State{}, errors.New("state store is unavailable")
	}
	return s.StateStore.Update(ctx, runID, update)
}

// leftQueue is a merge queue that still lists the run's entry and no longer
// holds it: the entry was let go without landing.
type leftQueue struct {
	entry runstate.MergeQueueEntry
}

func (q leftQueue) Entries(runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error) {
	return []runstate.MergeQueueEntry{q.entry}, nil
}

func (q leftQueue) Standing(runstate.MergeQueueKey, string) (runstate.MergeQueueEntry, bool, error) {
	return runstate.MergeQueueEntry{}, false, nil
}

func TestReconcileStopsARunWhoseMergeQueueEntryIsGoneSayingSo(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		queues func(t *testing.T, r *queueRun) ReconcileMergeQueues
		says   string
	}{
		"dropped from the queue": {
			queues: func(t *testing.T, _ *queueRun) ReconcileMergeQueues {
				empty, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
				if err != nil {
					t.Fatal(err)
				}
				return empty
			},
			says: "the queue no longer has that entry and landed nothing",
		},
		"let go without landing": {
			queues: func(t *testing.T, r *queueRun) ReconcileMergeQueues {
				entries, err := r.queue.Entries(runstate.MergeQueueKey{Repository: r.queueRepository(t), TargetBranch: "main"})
				if err != nil || len(entries) != 1 {
					t.Fatalf("entries = %#v, %v", entries, err)
				}
				return leftQueue{entry: entries[0]}
			},
			says: "the queue let the entry go without landing it",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, queued := queuedRun(t)
			result := reconcileOne(t, r.reconciler(listedEarlier{ReconcileStore: r.runs, listed: []runstate.State{queued}}, test.queues(t, r)))
			if result.Action != ActionBlocked || !strings.Contains(result.Detail, test.says) {
				t.Fatalf("reconciliation = %#v, want the run stopped saying %q", result, test.says)
			}
			if strings.Contains(result.Detail, "interrupted") {
				t.Fatalf("detail = %q, want the queue named as the reason rather than an interruption", result.Detail)
			}
			saved, err := r.runs.Load(queued.RunID)
			if err != nil || saved.Status != runstate.StatusFailed || !strings.Contains(saved.Blocker, test.says) || !strings.Contains(saved.Failure, test.says) {
				t.Fatalf("run = %#v, %v; want it stopped with the queue's reason", saved, err)
			}
			if !r.tracker.Blocked || !strings.Contains(r.tracker.BlockReason, test.says) {
				t.Fatalf("item blocked %t for %q, want it blocked naming the queue", r.tracker.Blocked, r.tracker.BlockReason)
			}
		})
	}
}

// queueRepository is the repository the fixture's queue is keyed by, read off
// the one queue it holds.
func (r *queueRun) queueRepository(t *testing.T) string {
	t.Helper()
	keys, err := r.queue.Keys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("queues = %#v, %v", keys, err)
	}
	return keys[0].Repository
}

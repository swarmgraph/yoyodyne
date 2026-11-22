package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run made live again and then refused because the main checkout carries a
// file the harness does not own keeps counting against the developer slots,
// with the scheduler that took it still running and nothing holding its lease.
// The next slot release gives that slot back at once, without waiting out
// UnheldRunQuiet, and records it as the refusal it was rather than as a process
// that vanished; the run stays in flight with its change kept.
func TestASlotHeldByARunTheCheckoutRefusedIsGivenBackAsARefusal(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	reEnterAt(t, store, tracker, stopped.RunID, runstate.PhaseReviewing)

	turnedAway := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s by a dispatch the checkout turned away", request.WorkingDirectory)
		return nil
	}, repairVerdict)
	refusing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, turnedAway, []string{"exit 0"}), turnedAway)
	refusing.Worktrees = unreadyPrimaryWorktrees{refusing.Worktrees}
	if _, err := refusing.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() resumed against a checkout nothing may be resumed against")
	}
	turned := loadRun(t, store, stopped.RunID)
	if !turned.Status.InFlight() || !store.HoldsDeveloperSlotNow(turned, time.Now()) {
		t.Fatalf("the refused run is %s, counted %t; want it still in flight and counted, which is the stranded slot", turned.Status, store.HoldsDeveloperSlotNow(turned, time.Now()))
	}
	if refusal, refused := turned.LastWriteRefusal(); !refused || refusal.Cause != runstate.CauseDirtyPrimary {
		t.Fatalf("LastWriteRefusal() = %#v, %t; want the checkout's refusal read as the last thing written on the run", refusal, refused)
	}

	released, err := store.ReleaseSlotsWithNoLiveWorker(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("ReleaseSlotsWithNoLiveWorker() error = %v", err)
	}
	if len(released) != 1 || released[0].RunID != stopped.RunID {
		t.Fatalf("released = %+v, want the refused run's slot given back at once", released)
	}
	release := released[0].Release
	if release.Refusal != runstate.CauseDirtyPrimary {
		t.Fatalf("release refusal = %q, want it recorded as the checkout's refusal", release.Refusal)
	}
	for _, want := range []string{"because the run was refused, not because its process vanished", "primary checkout carried state the harness does not own", "notes.txt", "keeps its claim, branch and worktree"} {
		if !strings.Contains(release.Says, want) {
			t.Errorf("release says %q, want it to say %q", release.Says, want)
		}
	}
	kept := loadRun(t, store, stopped.RunID)
	if !kept.Status.InFlight() || kept.SlotRelease == nil || store.HoldsDeveloperSlotNow(kept, time.Now()) {
		t.Fatalf("the run is %s with release %+v; want it in flight, released, and holding no slot", kept.Status, kept.SlotRelease)
	}
	if present := presentHandbackFiles(kept.WorktreePath); len(present) != len(handbackFiles) {
		t.Fatalf("the release left %d of the change's %d file(s)", len(present), len(handbackFiles))
	}

	// The sweep finds a run it can continue and leaves it so; nothing it does
	// calls the refusal a vanished process.
	reconciler := Reconciler{Tracker: tracker, Store: store, Worktrees: newObserver(t, repository, worktreeRoot)}
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	swept := loadRun(t, store, stopped.RunID)
	if swept.StopClass == runstate.CauseProcessVanished.StopClass() || swept.Environmental == nil || swept.Environmental.Cause != runstate.CauseDirtyPrimary {
		t.Fatalf("after the sweep the run's stop is %q with refusal %#v; want the checkout's refusal kept", swept.StopClass, swept.Environmental)
	}
}

// A parked run whose last write is the main checkout refusing it, and which
// nothing then continued, is ended by the sweep as refused: the stop cause it
// saves is the refusal's, the refusal stays as the account of the round, and
// the reason says the run was refused rather than that its process vanished.
func TestTheSweepEndsARefusedParkedRunAsARefusalNotAVanishedProcess(t *testing.T) {
	t.Parallel()

	repository, tracker, _, pipeline, store := automaticFixture(t)
	holds := newOperatorHoldStore(t)
	pipeline.Holds = holds
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if _, err := holds.Hold(time.Now()); err != nil {
			return err
		}
		return writeFeature(request)
	}, approveVerdict)
	pipeline.Backend = provider
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !outcome.Paused {
		t.Fatalf("Run() = %+v, error = %v; want the spending hold to park the run", outcome, err)
	}
	if _, _, err := holds.Release(); err != nil {
		t.Fatal(err)
	}
	state := loadRun(t, store, outcome.RunID)
	// What refuseDispatchEnvironmentally writes when the checkout turns the
	// run away, as the run's last write.
	state.Environmental = &runstate.EnvironmentalRefusal{
		Cause:      runstate.CauseDirtyPrimary,
		Detail:     "the primary checkout is not as the harness left it: primary repository has uncommitted changes: notes.txt",
		RecordedAt: state.UpdatedAt,
		NothingRan: true,
		Settled:    true,
		Refused:    true,
	}
	saveRecord(t, store, state)

	reconciler := Reconciler{
		Tracker: tracker, Store: store, Holds: holds,
		Worktrees: newObserver(t, repository, filepath.Dir(state.WorktreePath)),
		Clock:     fixedClock{at: state.UpdatedAt.Add(DefaultVanishedGrace + time.Hour)},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("Reconcile() = %+v, error = %v; want the uncontinued park settled", results, err)
	}
	assertSavedStopClass(t, store, outcome.RunID, runstate.CauseDirtyPrimary.StopClass())
	settled := loadRun(t, store, outcome.RunID)
	if settled.Environmental == nil || settled.Environmental.Cause != runstate.CauseDirtyPrimary {
		t.Fatalf("refusal = %#v, want the checkout's refusal kept as the account of the round", settled.Environmental)
	}
	for _, want := range []string{"refused to go on with the run", "primary checkout carried state the harness does not own", "refused rather than lost"} {
		if !strings.Contains(settled.Failure, want) || !strings.Contains(results[0].Detail, want) {
			t.Errorf("failure = %q, detail = %q; want both to say %q", settled.Failure, results[0].Detail, want)
		}
	}
	if strings.Contains(settled.Failure, "no live process behind it") {
		t.Errorf("failure = %q, want no account of a vanished process", settled.Failure)
	}
}

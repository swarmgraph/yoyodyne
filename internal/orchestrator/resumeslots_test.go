//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package orchestrator

// A run taken up again — by a decided repair, a continuation after a silent
// stream or at its checks, a resumed merge, or the merge queue's catch-up —
// whose own developer slot another run now holds takes a free slot ahead of
// fresh work, pinned to that slot's own endpoint pair, rather than waiting for
// its own slot while the free one goes to something new. Each path is given
// the slots' pairs (SlotPairs) as the product wires it (SlotPairsFor), and
// each is driven here with the run kept on slot 2's pair, slot 2 held by
// another run, and slot 1 free.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// resumeSlotPair is slot number's explicit endpoint pair as the configuration
// resolves it: Claude Code first for slot 2 and Codex first for slot 1, so a
// run moved between them is visibly pinned to a different pair.
func resumeSlotPair(slot int) config.ResolvedEndpointPair {
	endpoint := func(provider domain.Backend, account, model string) config.RoutedEndpoint {
		return config.RoutedEndpoint{
			Endpoint: backend.Endpoint{Provider: provider, AdapterVersion: "adapter-1", AccountAlias: account, Model: model},
			Model:    model,
			Origins:  map[string]string{"model": "execution.developer_slots.routing.primary.model (/project/yoyodyne.yaml)"},
		}
	}
	primary := endpoint(domain.BackendClaudeCode, "default", "opus")
	alternate := endpoint(domain.BackendCodex, "codex-account", "gpt-6.1-sol")
	if slot == 1 {
		primary, alternate = alternate, primary
	}
	return config.ResolvedEndpointPair{
		Slot: slot, Role: domain.RoleDeveloper, Primary: primary, Alternate: &alternate,
		Enabled: true, EnabledOrigin: "execution.developer_slots", Explicit: true, Revision: "cfg-0123abcd",
		Origin: "execution.developer_slots (/project/yoyodyne.yaml)",
	}
}

// resumeSlotPairs is the resolver a take-up is handed, counting how often it
// is asked.
func resumeSlotPairs(asked *int) runstate.SlotPairs {
	return func(_ runstate.State, slot int) (runstate.RoutingSnapshot, error) {
		*asked++
		return runstate.RoutingSnapshotOf(resumeSlotPair(slot), runstate.RoutingReconfigured, time.Now()), nil
	}
}

// pinToSlot records state in developer slot number and pins it to that slot's
// pair, as a run claimed into a slot with a pair records it.
func pinToSlot(t *testing.T, store *runstate.Store, state runstate.State, number int) runstate.State {
	t.Helper()
	pinned, err := store.UpdateRouting(context.Background(), state, func(routing *runstate.RunRouting) (bool, error) {
		var claimed bool
		var err error
		if routing.Slot == nil {
			claimed, err = routing.ClaimSlot(number, runstate.RoutingClaimed, docketedNow)
		} else {
			// A run that already recorded another slot is moved to this one.
			claimed, err = routing.MoveSlot(number, "the test keeps this run on another slot", docketedNow)
		}
		if err != nil {
			return false, err
		}
		recorded, err := routing.RecordSnapshot(runstate.RoutingSnapshotOf(resumeSlotPair(number), runstate.RoutingClaimed, docketedNow))
		return claimed || recorded, err
	})
	if err != nil {
		t.Fatalf("pin run %s to slot %d: %v", state.RunID, number, err)
	}
	return pinned
}

// assertTakenUpInSlot1 is what every path owes the run: recorded in free slot
// 1, pinned to slot 1's own pair, and the move from slot 2 recorded on it with
// the run that held slot 2 and why it went first.
func assertTakenUpInSlot1(t *testing.T, store *runstate.Store, runID, holder string, asked int) {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", runID, err)
	}
	if asked != 1 {
		t.Fatalf("the slots' pairs were asked %d times, want once", asked)
	}
	routing := state.Routing
	if state.RecordedSlot() != 1 || routing.Slot.MovedFrom != 2 {
		t.Fatalf("slot = %#v, want the run moved from slot 2 to free slot 1", routing.Slot)
	}
	want := runstate.RoutingSnapshotOf(resumeSlotPair(1), runstate.RoutingReconfigured, docketedNow)
	if routing.Developer == nil || routing.Developer.Slot != 1 || routing.Developer.Digest != want.Digest {
		t.Fatalf("pinned to %#v, want slot 1's own pair", routing.Developer)
	}
	for _, says := range []string{"developer slot 2 was held by run " + holder, "free developer slot 1 ahead of new work", "slot 1's own endpoints"} {
		if !strings.Contains(routing.Slot.MovedBecause, says) {
			t.Fatalf("moved because %q, want it to say %q", routing.Slot.MovedBecause, says)
		}
	}
	if !state.Status.InFlight() {
		t.Fatalf("status = %s, want the run in flight again", state.Status)
	}
}

// A decided repair. Without the slots' pairs the run waits for slot 2 while
// slot 1 sits free, which is what kept decided re-runs behind fresh work.
func TestADecidedRepairWhoseSlotIsHeldTakesAFreeSlotAheadOfFreshWork(t *testing.T) {
	t.Parallel()
	harness := newContinueHarness(t, continuableState())
	pinToSlot(t, harness.runs, harness.reload(t), 2)
	holder := occupySlot(t, harness.runs, "yoyodyne-holder", 2)

	waiting, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil || waiting.Continued || waiting.CapacityFull == nil || waiting.CapacityFull.Slot != 2 {
		t.Fatalf("Continue() with no slot pairs = %#v, %v; want it waiting for slot 2", waiting, err)
	}

	asked := 0
	continuer := harness.continuer()
	continuer.SlotPairs = resumeSlotPairs(&asked)
	result, err := continuer.Continue(context.Background(), continueRequest())
	if err != nil || !result.Continued || len(harness.started) != 1 {
		t.Fatalf("Continue() = %#v, %v (started %d); want the repair continued in slot 1", result, err, len(harness.started))
	}
	assertTakenUpInSlot1(t, harness.runs, docketedRunID, holder.RunID, asked)
}

// With every slot held, the decided repair waits for any slot to free rather
// than for its own, and the run is left where it was.
func TestADecidedRepairWithEverySlotHeldWaitsForAnyFreeSlot(t *testing.T) {
	t.Parallel()
	harness := newContinueHarness(t, continuableState())
	pinToSlot(t, harness.runs, harness.reload(t), 2)
	occupySlot(t, harness.runs, "yoyodyne-holder-1", 1)
	occupySlot(t, harness.runs, "yoyodyne-holder-2", 2)

	asked := 0
	continuer := harness.continuer()
	continuer.SlotPairs = resumeSlotPairs(&asked)
	result, err := continuer.Continue(context.Background(), continueRequest())
	if err != nil || result.Continued || result.CapacityFull == nil || result.CapacityFull.Slot != 0 {
		t.Fatalf("Continue() = %#v, %v; want it waiting for any developer slot, not slot 2", result, err)
	}
	if says := capacityWaitSays(*result.CapacityFull); !strings.Contains(says, "every developer slot is occupied") {
		t.Fatalf("the wait says %q, want every slot occupied", says)
	}
	if clears := capacityWaitClears(*result.CapacityFull); clears != "a developer slot freeing, which needs nobody" {
		t.Fatalf("the wait clears on %q, want any slot freeing", clears)
	}
	if state := harness.reload(t); state.RecordedSlot() != 2 || state.Status.InFlight() || harness.carried(t) != 0 || asked != 0 {
		t.Fatalf("run = slot %d, %s, carried %d, pairs asked %d; want it left as it was", state.RecordedSlot(), state.Status, harness.carried(t), asked)
	}
}

// The harness's own continuation of a run at its checks.
func TestACheckStageContinuationWhoseSlotIsHeldTakesAFreeSlot(t *testing.T) {
	t.Parallel()
	stopped := crossRoleCheckStageState()
	harness := newUndecidedHarness(t, stopped)
	pinToSlot(t, harness.runs, loadRecoveryRun(t, harness.runs, stopped.RunID), 2)
	holder := occupySlot(t, harness.runs, "yoyodyne-holder", 2)

	asked := 0
	continuer := checkStageRecoveryContinuer(harness, &orchestratortest.RecoveryCheckout{Ownership: harness.ownership, Branch: true, Present: true})
	continuer.SlotPairs = resumeSlotPairs(&asked)
	result, err := continuer.Continue(context.Background(), CheckStageContinueRequest{Run: stopped.RunID})
	if err != nil || !result.Continued {
		t.Fatalf("Continue() = %#v, %v; want the check stage continued in slot 1", result, err)
	}
	assertTakenUpInSlot1(t, harness.runs, stopped.RunID, holder.RunID, asked)
}

// The harness's own continuation of a run after its provider stream went
// silent.
func TestAStallContinuationWhoseSlotIsHeldTakesAFreeSlot(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	worktrees := newSweepManager(t, repository, worktreeRoot)
	stalled := settledRunWithCheckout(t, worktrees, store, 0)
	stalled.ProviderSessionID = "stalled-developer-session"
	stalled.Environmental = &runstate.EnvironmentalRefusal{
		Cause: runstate.CauseProcessVanished, ProviderStop: runstate.ProviderStopStalled,
		RecordedAt: *stalled.CompletedAt, Settled: true,
	}
	stalled.Blocker = "The developer's first attempt stopped after its provider stream went silent."
	if err := store.Save(stalled); err != nil {
		t.Fatal(err)
	}
	stalled = pinToSlot(t, store, loadRecoveryRun(t, store, stalled.RunID), 2)
	writeSweepFile(t, filepath.Join(stalled.WorktreePath, "half-done.txt"), "the developer got this far\n")
	docket := &memoryDocket{}
	if _, err := docketerOver(nil, docket).RecordStoppedRun(stalled); err != nil {
		t.Fatal(err)
	}
	holder := occupySlot(t, store, "yoyodyne-holder", 2)

	asked, starts := 0, 0
	continuer := StallContinuer{
		Docket: docket, Runs: store, Intake: newIntakeHoldStore(t), Worktrees: worktrees,
		Items:    &orchestratortest.Tracker{Item: beads.WorkItem{ID: stalled.WorkItemID, Title: "Interrupted developer", Status: "blocked"}},
		Capacity: 2, Clock: docketClockAt{at: stalled.CompletedAt.Add(time.Minute)},
		Start: func(_ context.Context, workItemID, runID string) (Outcome, error) {
			starts++
			return Outcome{RunID: runID, WorkItemID: workItemID}, nil
		},
		SlotPairs: resumeSlotPairs(&asked),
	}
	result, err := continuer.Continue(context.Background(), StallContinueRequest{Run: stalled.RunID})
	if err != nil || !result.Continued || starts != 1 {
		t.Fatalf("Continue() = %#v, %v (starts %d); want the stall continued in slot 1", result, err, starts)
	}
	assertTakenUpInSlot1(t, store, stalled.RunID, holder.RunID, asked)
}

// Resuming a merge an approved change stopped short of.
func TestAnIntegrationResumptionWhoseSlotIsHeldTakesAFreeSlot(t *testing.T) {
	t.Parallel()
	harness := newResumeHarness(t, approvedStoppedState())
	pinToSlot(t, harness.runs, harness.reload(t), 2)
	holder := occupySlot(t, harness.runs, "yoyodyne-holder", 2)

	asked := 0
	resumer := harness.resumer()
	resumer.SlotPairs = resumeSlotPairs(&asked)
	result, err := resumer.Resume(context.Background(), resumeRequest())
	if err != nil || !result.Resumed || len(harness.started) != 1 {
		t.Fatalf("Resume() = %#v, %v (started %d); want the promotion resumed in slot 1", result, err, len(harness.started))
	}
	assertTakenUpInSlot1(t, harness.runs, docketedRunID, holder.RunID, asked)
}

// The merge queue's catch-up of a change handed back only because its target
// moved under it.
func TestAMergeQueueCatchUpWhoseSlotIsHeldTakesAFreeSlot(t *testing.T) {
	t.Parallel()
	r, _ := newCatchUpRun(t, resolveBothSides)
	admitted := r.run(t)
	if admitted.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", admitted)
	}
	key := admitted.MergeQueue.Key()
	queued, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	pinToSlot(t, r.runs, queued, 2)
	holder := occupySlot(t, r.runs, "yoyodyne-holder", 2)
	conflictOnTarget(t, r.repository, "something else\n")

	asked := 0
	driver := r.driver()
	driver.Promoter.Repair = MergeQueueRunHandback{
		Store: r.runs, Tracker: r.tracker, CatchUp: r.runs, Capacity: 2,
		SlotPairs: resumeSlotPairs(&asked),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if pass := driver.Pass(ctx, key); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflicting change handed back", pass)
	}
	put, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if put.Status != runstate.StatusRunning || !put.CatchingUpWithMergeQueueTarget() {
		t.Fatalf("run = status %q, blocker %q; want it put back live for its catch-up", put.Status, put.Blocker)
	}
	assertTakenUpInSlot1(t, r.runs, admitted.RunID, holder.RunID, asked)
}

// The product hands every one of these paths the same resolver a pipeline
// takes runs up with.
func TestSlotPairsForResolvesWhatThePipelineResolves(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuseNothing, writeRoutedFeature)
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	slots := routedSlots([2]*domain.EndpointSpec{nil, nil}, [2]*domain.EndpointSpec{codexSol, claudeOpus})
	pipeline := routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, slots)
	run := runstate.State{RunID: "run-e63fbad1", WorkItemID: tracker.Item.ID}
	for slot := 1; slot <= 2; slot++ {
		want, err := pipeline.slotSnapshot(run, slot)
		if err != nil {
			t.Fatalf("slotSnapshot(%d) error = %v", slot, err)
		}
		got, err := SlotPairsFor(pipeline.Config)(run, slot)
		if err != nil || got.Digest != want.Digest || got.Slot != slot {
			t.Fatalf("SlotPairsFor(%d) = %#v, %v; want %#v", slot, got, err, want)
		}
	}
}

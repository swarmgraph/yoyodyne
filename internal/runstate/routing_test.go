package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

var routingAt = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func routedEndpoint(provider domain.Backend, account, model string) config.RoutedEndpoint {
	return config.RoutedEndpoint{
		Endpoint: backend.Endpoint{Provider: provider, AdapterVersion: "adapter-1", AccountAlias: account, Model: model},
		Model:    model,
		Origins:  map[string]string{"model": "execution.developer_slots slot 1.routing.primary.model (/project/yoyodyne.yaml)", "provider": "agents.developer.backend (default)"},
	}
}

// developerPair is a resolved pair as configuration hands it over: Claude Code
// first and Codex second, or the other way round for slot 1.
func developerPair(slot int, revision string) config.ResolvedEndpointPair {
	primary := routedEndpoint(domain.BackendClaudeCode, "default", "opus")
	alternate := routedEndpoint(domain.BackendCodex, "codex-account", "gpt-6.1-sol")
	if slot == 1 {
		primary, alternate = alternate, primary
	}
	return config.ResolvedEndpointPair{
		Slot: slot, Role: domain.RoleDeveloper, Primary: primary, Alternate: &alternate,
		Enabled: true, EnabledOrigin: "execution.developer_slots", Explicit: true, Revision: revision,
		Origin: "execution.developer_slots (/project/yoyodyne.yaml)",
	}
}

func developerSnapshot(slot int, revision string) RoutingSnapshot {
	return RoutingSnapshotOf(developerPair(slot, revision), RoutingClaimed, routingAt)
}

func intPointer(value int) *int { return &value }

// routedRun is a run in flight that has claimed slot 2 and pinned its pair.
func routedRun(t *testing.T, store *Store) State {
	t.Helper()
	state := testState(t, StatusRunning)
	state.RepairAttempts, state.ReviewRounds = 2, 3
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	state, err := store.ClaimSlot(context.Background(), state, 2, 4, routingAt)
	if err != nil {
		t.Fatalf("ClaimSlot() error = %v", err)
	}
	return route(t, store, state, func(r *RunRouting) (bool, error) { return r.RecordSnapshot(developerSnapshot(2, "cfg-0123abcd")) })
}

func route(t *testing.T, store *Store, state State, change func(*RunRouting) (bool, error)) State {
	t.Helper()
	next, err := store.UpdateRouting(context.Background(), state, change)
	if err != nil {
		t.Fatalf("UpdateRouting() error = %v", err)
	}
	return next
}

func reopen(t *testing.T, store *Store) *Store {
	t.Helper()
	// The store is <product directory>/runs, and the product directory is
	// wherever the home's layout puts it, so the root is found by asking.
	product := filepath.Dir(store.Root())
	root := product
	for root != filepath.Dir(root) && home.ProductDirectory(root, "yoyodyne") != product {
		root = filepath.Dir(root)
	}
	again, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return again
}

func load(t *testing.T, store *Store, runID string) State {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

func openDevelop(id string) func(*RunRouting) (bool, error) {
	return func(r *RunRouting) (bool, error) {
		return r.OpenOperation(OperationRequest{ID: id, Kind: OperationDevelop, Budget: OperationBudget{RepairAttempts: intPointer(2), ReviewRounds: intPointer(3)}}, routingAt)
	}
}

func prepare(operation, attempt string) func(*RunRouting) (bool, error) {
	return func(r *RunRouting) (bool, error) {
		return r.PrepareAttempt(operation, AttemptRequest{ID: attempt, Mode: SessionFresh}, routingAt)
	}
}

func end(operation, attempt, classification string, termination Termination) func(*RunRouting) (bool, error) {
	return func(r *RunRouting) (bool, error) {
		return r.EndAttempt(operation, attempt, AttemptEnding{Classification: classification, Termination: termination, At: routingAt})
	}
}

func planSwitch(operation, id, source, destination string) func(*RunRouting) (bool, error) {
	return func(r *RunRouting) (bool, error) {
		trigger := SwitchUsageLimit
		if source == "" {
			trigger = SwitchPrimaryLimited
		}
		return r.PlanSwitch(operation, SwitchRequest{ID: id, SourceAttempt: source, Trigger: trigger, Evidence: "the provider reported the plan's usage limit", ConfigRevision: "cfg-0123abcd", DestinationAttempt: destination}, routingAt)
	}
}

func TestARunKeepsItsSlotAndSnapshotThroughRestartReloadAndRelabelling(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)

	restarted := load(t, reopen(t, store), state.RunID)
	if restarted.RecordedSlot() != 2 || restarted.Routing.Slot.Origin != RoutingClaimed {
		t.Fatalf("after restart the slot is %+v, want slot 2 as claimed", restarted.Routing.Slot)
	}
	snapshot := restarted.Routing.Developer
	if snapshot == nil || snapshot.Digest == "" || snapshot.ConfigRevision != "cfg-0123abcd" || snapshot.Origin != RoutingClaimed || snapshot.Primary.Provider != domain.BackendClaudeCode || snapshot.Alternate.Provider != domain.BackendCodex {
		t.Fatalf("after restart the snapshot is %+v", snapshot)
	}
	if len(snapshot.Primary.Origins) != 2 || snapshot.Primary.Origins[0].Field != "model" {
		t.Fatalf("the snapshot lost where its choices came from: %+v", snapshot.Primary.Origins)
	}

	// A reload that resolves the same pair records nothing; one that resolves a
	// different pair cannot replace the pinned one.
	same := route(t, store, restarted, func(r *RunRouting) (bool, error) { return r.RecordSnapshot(developerSnapshot(2, "cfg-0123abcd")) })
	if same.Routing.Generation != restarted.Routing.Generation {
		t.Fatalf("recording the same pair advanced the generation from %d to %d", restarted.Routing.Generation, same.Routing.Generation)
	}
	reloaded := developerPair(2, "cfg-fedcba98")
	reloaded.Primary.Model, reloaded.Primary.Endpoint.Model = "sonnet", "sonnet"
	_, err := store.UpdateRouting(context.Background(), same, func(r *RunRouting) (bool, error) {
		return r.RecordSnapshot(RoutingSnapshotOf(reloaded, RoutingClaimed, routingAt))
	})
	if !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("recording a reloaded pair over the pinned one: error = %v, want a routing conflict", err)
	}
	// Nor is the run renumbered, by a claim or by a lowered capacity.
	if _, err := store.UpdateRouting(context.Background(), same, func(r *RunRouting) (bool, error) { return r.ClaimSlot(1, RoutingClaimed, routingAt) }); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("renumbering the run: error = %v, want a routing conflict", err)
	}
	if err := CapacityReductionProblem([]State{same}, 1); err == nil || !strings.Contains(err.Error(), "slot 2") {
		t.Fatalf("lowering capacity under slot 2: error = %v, want it refused naming the slot", err)
	}
	if err := CapacityReductionProblem([]State{same}, 2); err != nil {
		t.Fatalf("a capacity that still holds slot 2 was refused: %v", err)
	}
	final := load(t, store, state.RunID)
	if final.RecordedSlot() != 2 || final.Routing.Developer.Digest != snapshot.Digest {
		t.Fatalf("the slot or snapshot changed: %+v", final.Routing)
	}
}

func TestClaimSlotRefusesASlotAnotherRunHoldsOrOneBeyondCapacity(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	routedRun(t, store)
	other := testState(t, StatusRunning)
	other.WorkItemID = "yoyodyne-other"
	if err := store.Create(other); err != nil {
		t.Fatal(err)
	}
	var occupied SlotOccupiedError
	if _, err := store.ClaimSlot(context.Background(), other, 2, 4, routingAt); !errors.As(err, &occupied) || occupied.Slot != 2 {
		t.Fatalf("claiming an occupied slot: error = %v, want it refused", err)
	}
	if _, err := store.ClaimSlot(context.Background(), other, 5, 4, routingAt); err == nil {
		t.Fatal("a slot beyond capacity was claimed")
	}
	claimed, err := store.ClaimSlot(context.Background(), other, 3, 4, routingAt)
	if err != nil || claimed.RecordedSlot() != 3 {
		t.Fatalf("claiming a free slot: slot %d, error = %v", claimed.RecordedSlot(), err)
	}
	// Capacity occupancy is still what Reserve counts, and is unchanged.
	if holding := HoldingDeveloperSlots([]State{claimed}); holding != 1 {
		t.Fatalf("holding = %d, want 1", holding)
	}
}

func TestAnOperationSwitchesOnceAndKeepsItsAlternateThroughWaitsAndRestarts(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	for _, step := range []func(*RunRouting) (bool, error){
		openDevelop("op-1"),
		prepare("op-1", "att-1"),
		func(r *RunRouting) (bool, error) { return r.MarkLaunched("op-1", "att-1", routingAt) },
		end("op-1", "att-1", UsageLimitClassification, TerminationConfirmed),
		planSwitch("op-1", "sw-1", "att-1", "att-2"),
		func(r *RunRouting) (bool, error) { return r.ReconcileSource("op-1", "sw-1", routingAt) },
		prepare("op-1", "att-2"),
	} {
		state = route(t, store, state, step)
	}
	operation, _ := state.Routing.Operation("op-1")
	if operation.Selected != EndpointAlternate || operation.SwitchAllowance != 0 || operation.Switch.Progress != TransitionDestinationPrepared {
		t.Fatalf("after the switch the operation is %+v", operation)
	}
	if got := operation.Attempts[1].Endpoint; got.Provider != domain.BackendCodex || operation.Attempts[1].Choice != EndpointAlternate {
		t.Fatalf("the destination attempt runs on %+v, want the pinned alternate", got)
	}

	// The alternate reaches its limit too: there is no second switch, and the
	// operation waits on the alternate rather than going back.
	state = route(t, store, state, end("op-1", "att-2", UsageLimitClassification, TerminationConfirmed))
	if _, err := store.UpdateRouting(context.Background(), state, planSwitch("op-1", "sw-2", "att-2", "att-3")); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("a second switch: error = %v, want it refused", err)
	}
	if _, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) {
		return r.SetWaiting("op-1", OperationWait{Reason: "the primary's plan is at its limit", Endpoint: EndpointPrimary, Since: routingAt})
	}); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("waiting on the primary after committing the alternate: error = %v, want it refused", err)
	}
	reset := routingAt.Add(3 * time.Hour)
	state = route(t, store, state, func(r *RunRouting) (bool, error) {
		return r.SetWaiting("op-1", OperationWait{Reason: "the alternate's plan is at its limit", Endpoint: EndpointAlternate, ResetAt: &reset, Since: routingAt})
	})

	// Restart, then ask for the same operation again: it is the same operation,
	// with no allowance found again.
	restarted := reopen(t, store)
	state = load(t, restarted, state.RunID)
	state = route(t, restarted, state, openDevelop("op-1"))
	operation, _ = state.Routing.Operation("op-1")
	if operation.Selected != EndpointAlternate || operation.SwitchAllowance != 0 || operation.Waiting == nil || operation.Waiting.Endpoint != EndpointAlternate {
		t.Fatalf("after restart the operation is %+v, want it waiting on its committed alternate with no switch left", operation)
	}
	state = route(t, restarted, state, prepare("op-1", "att-3"))
	operation, _ = state.Routing.Operation("op-1")
	if operation.Attempts[2].Choice != EndpointAlternate {
		t.Fatalf("a reissue after the wait chose %s, want the committed alternate", operation.Attempts[2].Choice)
	}

	// A genuinely new operation starts from its primary with its own allowance.
	state = route(t, restarted, state, end("op-1", "att-3", "succeeded", TerminationConfirmed))
	state = route(t, restarted, state, func(r *RunRouting) (bool, error) { return r.CompleteOperation("op-1", "developer replied", routingAt) })
	state = route(t, restarted, state, func(r *RunRouting) (bool, error) {
		return r.OpenOperation(OperationRequest{ID: "op-2", Kind: OperationRepair, Findings: "review round 4 findings", Budget: OperationBudget{RepairAttempts: intPointer(3)}}, routingAt)
	})
	next, _ := state.Routing.Operation("op-2")
	if next.Selected != EndpointPrimary || next.SwitchAllowance != 1 {
		t.Fatalf("a new operation is %+v, want its primary and one switch", next)
	}
	if state.RepairAttempts != 2 || state.ReviewRounds != 3 {
		t.Fatalf("routing changed the run's counters: repair %d, review %d", state.RepairAttempts, state.ReviewRounds)
	}
}

func TestAPrimaryKnownToBeLimitedIsSkippedWithoutAFictitiousAttempt(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	state = route(t, store, state, planSwitch("op-1", "sw-1", "", "att-1"))
	operation, _ := state.Routing.Operation("op-1")
	if len(operation.Attempts) != 0 || operation.Switch.Progress != TransitionSourceReconciled || operation.Selected != EndpointAlternate {
		t.Fatalf("skipping the primary recorded %+v", operation)
	}
	if _, err := store.UpdateRouting(context.Background(), state, prepare("op-1", "att-9")); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("preparing an attempt other than the reserved destination: error = %v", err)
	}
	state = route(t, store, state, prepare("op-1", "att-1"))
	state = route(t, store, state, func(r *RunRouting) (bool, error) { return r.MarkLaunched("op-1", "att-1", routingAt) })
	state = route(t, store, state, end("op-1", "att-1", "succeeded", TerminationConfirmed))
	operation, _ = state.Routing.Operation("op-1")
	if operation.Attempts[0].Choice != EndpointAlternate || operation.Switch.Progress != TransitionOutcomeRecorded || len(operation.Switch.Steps) != 5 {
		t.Fatalf("the switch is %+v", operation.Switch)
	}
}

func TestASwitchWaitsWhileItsSourceMayStillBeExecuting(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	state = route(t, store, state, prepare("op-1", "att-1"))
	// A source still running has not been classified, so no switch is planned
	// on it and the allowance stays unspent.
	if _, err := store.UpdateRouting(context.Background(), state, planSwitch("op-1", "sw-1", "att-1", "att-2")); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("planning a switch on a source still running: error = %v, want it refused", err)
	}
	if operation, _ := load(t, store, state.RunID).Routing.Operation("op-1"); operation.SwitchAllowance != 1 || operation.Switch != nil {
		t.Fatalf("a refused switch changed the operation: %+v", operation)
	}
	// Classified as a usage limit but not known to have stopped: the switch is
	// committed and its destination waits.
	state = route(t, store, state, end("op-1", "att-1", UsageLimitClassification, TerminationUncertain))
	state = route(t, store, state, planSwitch("op-1", "sw-1", "att-1", "att-2"))
	for name, step := range map[string]func(*RunRouting) (bool, error){
		"reconcile":           func(r *RunRouting) (bool, error) { return r.ReconcileSource("op-1", "sw-1", routingAt) },
		"prepare destination": prepare("op-1", "att-2"),
	} {
		if _, err := store.UpdateRouting(context.Background(), state, step); !errors.Is(err, ErrRoutingConflict) {
			t.Fatalf("%s with the source's termination uncertain: error = %v", name, err)
		}
	}
	if _, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) { return r.CompleteOperation("op-1", "done", routingAt) }); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("completing with uncertain termination: error = %v", err)
	}
	state = route(t, store, state, end("op-1", "att-1", UsageLimitClassification, TerminationConfirmed))
	state = route(t, store, state, func(r *RunRouting) (bool, error) { return r.ReconcileSource("op-1", "sw-1", routingAt) })
	route(t, store, state, prepare("op-1", "att-2"))
}

func TestOnlyAUsageLimitOnThePrimaryPermitsASwitch(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	state = route(t, store, state, prepare("op-1", "att-1"))
	state = route(t, store, state, end("op-1", "att-1", "authentication_refused", TerminationConfirmed))
	if _, err := store.UpdateRouting(context.Background(), state, planSwitch("op-1", "sw-1", "att-1", "att-2")); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("switching on an authentication refusal: error = %v", err)
	}
	// A late report cannot reclassify the refusal as a usage limit to unlock one.
	if _, err := store.UpdateRouting(context.Background(), state, end("op-1", "att-1", UsageLimitClassification, TerminationConfirmed)); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("reclassifying an ended attempt: error = %v", err)
	}
	if _, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) {
		return r.PlanSwitch("op-1", SwitchRequest{ID: "sw-1", Trigger: "unknown_error", Evidence: "x", DestinationAttempt: "att-2"}, routingAt)
	}); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("switching on an unknown trigger: error = %v", err)
	}
}

func TestRepeatingARequestIsIdempotentAndReusingItsIdentityIsRefused(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	stale := state
	state = route(t, store, state, openDevelop("op-1"))
	state = route(t, store, state, prepare("op-1", "att-1"))
	generation := state.Routing.Generation

	// The same requests again, even from a copy read before them, change nothing
	// and answer with the stored run.
	for _, step := range []func(*RunRouting) (bool, error){openDevelop("op-1"), prepare("op-1", "att-1")} {
		again, err := store.UpdateRouting(context.Background(), stale, step)
		if err != nil || again.Routing.Generation != generation {
			t.Fatalf("a repeated request: generation %d, error = %v; want %d and no error", again.Routing.Generation, err, generation)
		}
	}
	for name, step := range map[string]func(*RunRouting) (bool, error){
		"operation": func(r *RunRouting) (bool, error) {
			return r.OpenOperation(OperationRequest{ID: "op-1", Kind: OperationRepair}, routingAt)
		},
		"attempt": func(r *RunRouting) (bool, error) {
			return r.PrepareAttempt("op-1", AttemptRequest{ID: "att-1", Mode: SessionReconstruction}, routingAt)
		},
		"second open operation": openDevelop("op-2"),
		"second active attempt": prepare("op-1", "att-2"),
	} {
		if _, err := store.UpdateRouting(context.Background(), state, step); !errors.Is(err, ErrRoutingConflict) {
			t.Fatalf("%s with a reused identity or overlapping work: error = %v, want a conflict", name, err)
		}
	}
	state = route(t, store, state, end("op-1", "att-1", UsageLimitClassification, TerminationConfirmed))
	state = route(t, store, state, planSwitch("op-1", "sw-1", "att-1", "att-2"))
	again := route(t, store, state, planSwitch("op-1", "sw-1", "att-1", "att-2"))
	if again.Routing.Generation != state.Routing.Generation {
		t.Fatal("a repeated switch request advanced the generation")
	}
	if _, err := store.UpdateRouting(context.Background(), state, planSwitch("op-1", "sw-1", "att-1", "att-7")); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("the switch identity reused for another destination: error = %v", err)
	}
	if _, err := store.UpdateRouting(context.Background(), state, end("op-1", "att-1", "succeeded", TerminationConfirmed)); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("a late report rewriting an attempt's ending: error = %v", err)
	}
}

func TestCompetingTransitionsLeaveExactlyOneStanding(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	state = route(t, store, state, prepare("op-1", "att-1"))
	state = route(t, store, state, end("op-1", "att-1", UsageLimitClassification, TerminationConfirmed))

	// Two controllers, each with its own store over the same directory and the
	// same copy of the run, race to commit a different switch.
	const controllers = 8
	var wait sync.WaitGroup
	results := make([]error, controllers)
	for index := 0; index < controllers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			controller := reopen(t, store)
			id := "sw-" + string(rune('a'+index))
			_, results[index] = controller.UpdateRouting(context.Background(), state, planSwitch("op-1", id, "att-1", "att-dest-"+string(rune('a'+index))))
		}(index)
	}
	wait.Wait()
	won := 0
	for _, err := range results {
		var stale StaleRoutingError
		switch {
		case err == nil:
			won++
		case errors.As(err, &stale), errors.Is(err, ErrRoutingConflict):
			// A loser either held a copy the winner made stale or found the
			// switch already spent; both are refusals and neither wrote.
		default:
			t.Fatalf("a losing controller was refused for %v, want a stale copy or a spent switch", err)
		}
	}
	stored := load(t, store, state.RunID)
	operation, _ := stored.Routing.Operation("op-1")
	if won != 1 || operation.Switch == nil || operation.SwitchAllowance != 0 || stored.Routing.Generation != state.Routing.Generation+1 {
		t.Fatalf("%d controllers committed a switch; stored %+v", won, operation)
	}
	// The loser reads again and finds the switch spent.
	if _, err := store.UpdateRouting(context.Background(), stored, planSwitch("op-1", "sw-z", "att-1", "att-z")); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("a second switch after reading again: error = %v", err)
	}
}

func TestRestartAtEachPersistedTransitionContinuesFromTheRecord(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	steps := []struct {
		apply func(*RunRouting) (bool, error)
		want  TransitionProgress
	}{
		{openDevelop("op-1"), ""},
		{prepare("op-1", "att-1"), ""},
		{func(r *RunRouting) (bool, error) { return r.MarkLaunched("op-1", "att-1", routingAt) }, ""},
		{end("op-1", "att-1", UsageLimitClassification, TerminationConfirmed), ""},
		{planSwitch("op-1", "sw-1", "att-1", "att-2"), TransitionPlanned},
		{func(r *RunRouting) (bool, error) { return r.ReconcileSource("op-1", "sw-1", routingAt) }, TransitionSourceReconciled},
		{prepare("op-1", "att-2"), TransitionDestinationPrepared},
		{func(r *RunRouting) (bool, error) { return r.MarkLaunched("op-1", "att-2", routingAt) }, TransitionDestinationLaunched},
		{end("op-1", "att-2", "succeeded", TerminationConfirmed), TransitionOutcomeRecorded},
	}
	for index, step := range steps {
		state = route(t, store, state, step.apply)
		// The process dies here; another reads the run and asks every step so far
		// again, as recovery would, and nothing moves.
		store = reopen(t, store)
		recovered := load(t, store, state.RunID)
		for _, earlier := range steps[:index+1] {
			again := route(t, store, recovered, earlier.apply)
			if again.Routing.Generation != recovered.Routing.Generation {
				t.Fatalf("after step %d a repeated earlier step changed the record", index)
			}
		}
		operation, _ := recovered.Routing.Operation("op-1")
		if step.want != "" {
			if operation.Switch == nil || operation.Switch.Progress != step.want || operation.Selected != EndpointAlternate || operation.SwitchAllowance != 0 {
				t.Fatalf("after restarting at step %d the operation is %+v, want progress %s", index, operation, step.want)
			}
		} else if operation.Selected != EndpointPrimary || operation.SwitchAllowance != 1 {
			t.Fatalf("after restarting at step %d before any switch the operation is %+v", index, operation)
		}
		state = recovered
	}
}

func TestAStaleWholeRecordWriteIsRefusedAndLosesNothing(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	older := state // a writer that read the run before the operation opened
	state = route(t, store, state, openDevelop("op-1"))

	older.Phase = PhaseChecking
	var stale StaleRoutingError
	if err := store.Save(older); !errors.As(err, &stale) || stale.Stored != state.Routing.Generation {
		t.Fatalf("a stale whole-record write: error = %v, want it refused as stale", err)
	}
	legacyWriter := state
	legacyWriter.Routing = nil
	if err := store.Save(legacyWriter); !errors.As(err, &stale) {
		t.Fatalf("a whole-record write carrying no routing: error = %v, want it refused as stale", err)
	}
	forged := load(t, store, state.RunID)
	forged.Routing.Operations[0].SwitchAllowance = 1
	forged.Routing.Operations[0].Selected = EndpointPrimary
	forged.Routing.Slot.Origin = RoutingMigrated
	if err := store.Save(forged); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("a whole-record write that changed routing itself: error = %v, want it refused", err)
	}
	stored := load(t, store, state.RunID)
	if _, ok := stored.Routing.Operation("op-1"); !ok || stored.Phase == PhaseChecking || stored.RecordedSlot() != 2 {
		t.Fatalf("a refused write changed the record: %+v", stored)
	}
	// A writer that reads again saves its own field and keeps the routing.
	stored.Phase = PhaseChecking
	if err := store.Save(stored); err != nil {
		t.Fatalf("a current whole-record write: %v", err)
	}
	if after := load(t, store, state.RunID); after.Phase != PhaseChecking || !equalRouting(after.Routing, stored.Routing) {
		t.Fatalf("a current write lost something: %+v", after)
	}
}

func TestALegacyRecordReadsAsUnknownAndMigratesWithoutAFreshAllowance(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	legacy := testState(t, StatusRunning)
	legacy.RepairAttempts = 1
	if err := store.Create(legacy); err != nil {
		t.Fatal(err)
	}
	// The record a build from before routing wrote has no routing key at all.
	encoded, err := os.ReadFile(filepath.Join(store.Root(), legacy.RunID+".json"))
	if err != nil || strings.Contains(string(encoded), `"routing"`) {
		t.Fatalf("a run with no routing wrote %s, %v", encoded, err)
	}
	loaded := load(t, store, legacy.RunID)
	if loaded.Routing != nil || loaded.RecordedSlot() != 0 {
		t.Fatalf("a legacy run reads with routing %+v", loaded.Routing)
	}
	loaded.Phase = PhaseChecking
	if err := store.Save(loaded); err != nil {
		t.Fatalf("an ordinary write of a legacy run: %v", err)
	}

	holder := routedRun(t, store) // in slot 2
	_ = holder
	snapshotFor := func(slot int) (RoutingSnapshot, error) { return developerSnapshot(slot, "cfg-0123abcd"), nil }
	unfinished := &OperationRequest{ID: "op-legacy", Kind: OperationDevelop, Budget: OperationBudget{RepairAttempts: intPointer(1)}}
	migrated, err := store.MigrateRouting(context.Background(), load(t, store, legacy.RunID), 4, snapshotFor, unfinished, routingAt)
	if err != nil {
		t.Fatalf("MigrateRouting() error = %v", err)
	}
	if migrated.RecordedSlot() != 1 || migrated.Routing.Slot.Origin != RoutingMigrated || migrated.Routing.Developer.Origin != RoutingMigrated {
		t.Fatalf("migration recorded %+v, want slot 1 marked as migrated", migrated.Routing)
	}
	operation, _ := migrated.Routing.Operation("op-legacy")
	if !operation.HistoryUnknown || operation.SwitchAllowance != 0 || operation.TransientRelaunches != nil || operation.Budget.ReviewRounds != nil || *operation.Budget.RepairAttempts != 1 {
		t.Fatalf("the migrated operation is %+v, want unknown history, no allowance, and unknown counters kept unknown", operation)
	}
	if migrated.Phase != PhaseChecking || migrated.RepairAttempts != 1 {
		t.Fatalf("migration changed the run: phase %s, repairs %d", migrated.Phase, migrated.RepairAttempts)
	}
	// Missing history never becomes a switch, and an unknown relaunch count
	// stays unknown however many relaunches follow.
	if _, err := store.UpdateRouting(context.Background(), migrated, planSwitch("op-legacy", "sw-1", "", "att-1")); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("a switch on a migrated operation: error = %v, want it refused", err)
	}
	migrated = route(t, store, migrated, func(r *RunRouting) (bool, error) {
		return r.PrepareAttempt("op-legacy", AttemptRequest{ID: "att-1", Mode: SessionReconstruction, Transient: true}, routingAt)
	})
	if operation, _ := migrated.Routing.Operation("op-legacy"); operation.TransientRelaunches != nil {
		t.Fatalf("an unknown relaunch count became %d", *operation.TransientRelaunches)
	}
	// Migrating again is the same answer; a restart cannot make it a new one.
	again, err := store.MigrateRouting(context.Background(), migrated, 4, snapshotFor, unfinished, routingAt)
	if err != nil || again.Routing.Generation != migrated.Routing.Generation {
		t.Fatalf("a repeated migration: generation %d, error = %v", again.Routing.Generation, err)
	}
}

func TestMigrationWaitsRatherThanCreatingAWorkerBeyondCapacity(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	routedRun(t, store) // slot 2
	first := testState(t, StatusRunning)
	first.WorkItemID = "yoyodyne-first"
	if err := store.Create(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimSlot(context.Background(), first, 1, 2, routingAt); err != nil {
		t.Fatal(err)
	}
	legacy := testState(t, StatusRunning)
	legacy.WorkItemID = "yoyodyne-legacy"
	if err := store.Create(legacy); err != nil {
		t.Fatal(err)
	}
	snapshotFor := func(slot int) (RoutingSnapshot, error) { return developerSnapshot(slot, "cfg-0123abcd"), nil }
	// A second legacy run with no recorded slot is in flight too; it occupies no
	// numbered slot and is not counted as one.
	unnumbered := testState(t, StatusRunning)
	unnumbered.WorkItemID = "yoyodyne-unnumbered"
	if err := store.Create(unnumbered); err != nil {
		t.Fatal(err)
	}
	var full CapacityError
	if _, err := store.MigrateRouting(context.Background(), legacy, 2, snapshotFor, nil, routingAt); !errors.As(err, &full) || full.Active != 2 || full.Limit != 2 {
		t.Fatalf("migrating with every slot recorded: error = %v, want a capacity wait naming the two recorded slots", err)
	}
	if stored := load(t, store, legacy.RunID); stored.Routing != nil {
		t.Fatalf("a refused migration recorded %+v", stored.Routing)
	}
}

func TestARecordFromANewerRoutingVersionIsNeitherResumedNorReplaced(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	path := filepath.Join(store.Root(), state.RunID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(encoded, &record); err != nil {
		t.Fatal(err)
	}
	record["routing"].(map[string]any)["version"] = RoutingVersion + 1
	newer, _ := json.Marshal(record)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(state.RunID); !errors.Is(err, ErrUnsupportedRouting) {
		t.Fatalf("Load() of a newer routing version: error = %v", err)
	}
	if listed, err := store.Read(state.RunID); err != nil || listed.RunID != state.RunID {
		t.Fatalf("a listing could not read the run: %v", err)
	}
	if err := store.Save(state); !errors.Is(err, ErrUnsupportedRouting) {
		t.Fatalf("Save() over a newer routing version: error = %v", err)
	}
	if _, err := store.UpdateRouting(context.Background(), state, openDevelop("op-1")); !errors.Is(err, ErrUnsupportedRouting) {
		t.Fatalf("UpdateRouting() over a newer routing version: error = %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(newer) {
		t.Fatal("a refused write replaced the newer record")
	}
}

func TestNativeResumeNeedsACompatibleSessionOnTheSameEndpoint(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	resume := func(session SessionEvidence) func(*RunRouting) (bool, error) {
		return func(r *RunRouting) (bool, error) {
			return r.PrepareAttempt("op-1", AttemptRequest{ID: "att-1", Mode: SessionNativeResume, Session: &session}, routingAt)
		}
	}
	for name, session := range map[string]SessionEvidence{
		"other provider":     {SessionID: "s-1", Provider: domain.BackendCodex, AccountAlias: "default", Model: "opus", Compatible: true},
		"other model":        {SessionID: "s-1", Provider: domain.BackendClaudeCode, AccountAlias: "default", Model: "sonnet", Compatible: true},
		"not shown suitable": {SessionID: "s-1", Provider: domain.BackendClaudeCode, AccountAlias: "default", Model: "opus"},
	} {
		if _, err := store.UpdateRouting(context.Background(), state, resume(session)); !errors.Is(err, ErrRoutingConflict) {
			t.Fatalf("resuming a session on %s: error = %v, want it refused", name, err)
		}
	}
	state = route(t, store, state, resume(SessionEvidence{SessionID: "s-1", Provider: domain.BackendClaudeCode, AccountAlias: "default", Model: "opus", Compatible: true, Reason: "same session, account and model"}))
	operation, _ := state.Routing.Operation("op-1")
	if operation.Attempts[0].Session == nil || operation.Attempts[0].Mode != SessionNativeResume {
		t.Fatalf("the resume recorded %+v", operation.Attempts[0])
	}
}

func TestReconfigurationIsExplicitWaitsForQuietAndKeepsBudgets(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	state = route(t, store, state, planSwitch("op-1", "sw-1", "", "att-1"))
	state = route(t, store, state, prepare("op-1", "att-1"))
	replacement := developerPair(2, "cfg-fedcba98")
	replacement.Alternate.Model, replacement.Alternate.Endpoint.Model = "gpt-6.1", "gpt-6.1"
	snapshot := RoutingSnapshotOf(replacement, RoutingClaimed, routingAt)
	reconfigure := func(r *RunRouting) (bool, error) {
		return r.Reconfigure(snapshot, "the operator replaced slot 2's alternate", routingAt)
	}
	if _, err := store.UpdateRouting(context.Background(), state, reconfigure); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("reconfiguring while an attempt may run: error = %v", err)
	}
	state = route(t, store, state, end("op-1", "att-1", UsageLimitClassification, TerminationConfirmed))
	before := state.Routing.Developer.Digest
	state = route(t, store, state, reconfigure)
	if len(state.Routing.Reconfigurations) != 1 || state.Routing.Reconfigurations[0].FromDigest != before || state.Routing.Developer.Origin != RoutingReconfigured || state.Routing.Developer.ConfigRevision != "cfg-fedcba98" {
		t.Fatalf("the reconfiguration recorded %+v", state.Routing)
	}
	operation, _ := state.Routing.Operation("op-1")
	if operation.SwitchAllowance != 0 || operation.Selected != EndpointAlternate {
		t.Fatalf("reconfiguration reset the operation: %+v", operation)
	}
}

func TestNothingLaunchesForAnOperationWhileAnEarlierAttemptsStopIsUncertain(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	state = route(t, store, state, prepare("op-1", "att-1"))
	state = route(t, store, state, func(r *RunRouting) (bool, error) { return r.MarkLaunched("op-1", "att-1", routingAt) })
	// The attempt ended for something outside the work, and nothing confirmed
	// that its process stopped.
	state = route(t, store, state, end("op-1", "att-1", "connection_lost", TerminationUncertain))
	for name, step := range map[string]func(*RunRouting) (bool, error){
		"a plain relaunch": func(r *RunRouting) (bool, error) {
			return r.PrepareAttempt("op-1", AttemptRequest{ID: "att-2", Predecessor: "att-1", Mode: SessionReconstruction, Transient: true}, routingAt)
		},
		"a switch past a primary known to be limited": planSwitch("op-1", "sw-1", "", "att-2"),
		"a reconfiguration": func(r *RunRouting) (bool, error) {
			replacement := developerPair(2, "cfg-fedcba98")
			replacement.Alternate.Model, replacement.Alternate.Endpoint.Model = "gpt-6.1", "gpt-6.1"
			return r.Reconfigure(RoutingSnapshotOf(replacement, RoutingClaimed, routingAt), "replace the alternate", routingAt)
		},
	} {
		if _, err := store.UpdateRouting(context.Background(), state, step); !errors.Is(err, ErrRoutingConflict) {
			t.Fatalf("%s while attempt att-1 may still be executing: error = %v, want it refused", name, err)
		}
	}
	operation, _ := load(t, store, state.RunID).Routing.Operation("op-1")
	if len(operation.Attempts) != 1 || operation.Switch != nil || operation.SwitchAllowance != 1 || *operation.TransientRelaunches != 0 {
		t.Fatalf("a refused launch changed the operation: %+v", operation)
	}
	// Once its stop is confirmed, both are allowed again.
	state = route(t, store, state, end("op-1", "att-1", "connection_lost", TerminationConfirmed))
	relaunched := route(t, store, state, func(r *RunRouting) (bool, error) {
		return r.PrepareAttempt("op-1", AttemptRequest{ID: "att-2", Predecessor: "att-1", Mode: SessionReconstruction, Transient: true}, routingAt)
	})
	if operation, _ := relaunched.Routing.Operation("op-1"); *operation.TransientRelaunches != 1 {
		t.Fatalf("the relaunch was not counted: %+v", operation)
	}
	relaunched = route(t, store, relaunched, end("op-1", "att-2", "connection_lost", TerminationConfirmed))
	route(t, store, relaunched, planSwitch("op-1", "sw-1", "", "att-3"))
}

package runstate

import (
	"context"
	"errors"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A late result stays with the attempt that produced it: it cannot end the
// attempt that replaced it, rewrite the ending its own attempt already has, or
// give a verdict on a candidate its operation is not judging. What it reported
// using is kept in every case, so it is still counted.
func TestALateResultCannotOverwriteTheActiveAttemptAndKeepsItsUsage(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	for _, step := range []func(*RunRouting) (bool, error){
		openDevelop("op-1"),
		prepare("op-1", "att-1"),
		launched("op-1", "att-1"),
		end("op-1", "att-1", InterruptedClassification, TerminationConfirmed),
		func(r *RunRouting) (bool, error) {
			return r.PrepareAttempt("op-1", AttemptRequest{ID: "att-2", Predecessor: "att-1", Mode: SessionReconstruction, Transient: true}, routingAt)
		},
		launched("op-1", "att-2"),
	} {
		state = route(t, store, state, step)
	}
	var disposition ResultDisposition
	state = route(t, store, state, func(r *RunRouting) (bool, error) {
		var changed bool
		var err error
		disposition, changed, err = r.AcceptResult(AttemptResult{
			Operation: "op-1", Attempt: "att-1", Ending: AttemptEnding{Classification: "succeeded", Termination: TerminationConfirmed, At: routingAt},
			Usage: "event:run/41",
		})
		return changed, err
	})
	operation, _ := state.Routing.Operation("op-1")
	if disposition != ResultLate {
		t.Fatalf("a result from an attempt already ended = %s", disposition)
	}
	if first := operation.Attempts[0]; first.Ended.Classification != InterruptedClassification || len(first.Usage) != 1 || first.Usage[0] != "event:run/41" {
		t.Fatalf("the earlier attempt after its late result = %+v", first)
	}
	if second := operation.Attempts[1]; second.State != AttemptLaunched || len(second.Usage) != 0 {
		t.Fatalf("the active attempt after a late result from its predecessor = %+v", second)
	}
	// The same usage report arriving again is the same report.
	again, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) {
		_, changed, err := r.AcceptResult(AttemptResult{Operation: "op-1", Attempt: "att-1", Ending: AttemptEnding{Classification: "succeeded"}, Usage: "event:run/41"})
		return changed, err
	})
	if err != nil || again.Routing.Generation != state.Routing.Generation {
		t.Fatalf("a replayed usage report changed the record: %v", err)
	}
	// A result naming an operation it does not belong to completes nothing.
	if _, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) {
		_, changed, err := r.AcceptResult(AttemptResult{Operation: "op-other", Attempt: "att-2", Ending: AttemptEnding{Classification: "succeeded"}})
		return changed, err
	}); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("a result for the wrong operation: error = %v", err)
	}
}

// reviewerPair is the reviewer's resolved pair: Codex first, Claude Code second.
func reviewerPair() config.ResolvedEndpointPair {
	alternate := routedEndpoint(domain.BackendClaudeCode, "default", "opus")
	return config.ResolvedEndpointPair{
		Role: domain.RoleReviewer, Primary: routedEndpoint(domain.BackendCodex, "codex-account", "gpt-6.1-sol"), Alternate: &alternate,
		Enabled: true, EnabledOrigin: "agents.reviewer.routing", Explicit: true, Revision: "cfg-0123abcd",
		Origin: "agents.reviewer.routing (/project/yoyodyne.yaml)",
	}
}

func TestAResultForAnotherCandidateGivesNoVerdict(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	reviewing := "0123456789abcdef0123456789abcdef01234567"
	state = route(t, store, state, func(r *RunRouting) (bool, error) {
		return r.RecordSnapshot(RoutingSnapshotOf(reviewerPair(), RoutingClaimed, routingAt))
	})
	for _, step := range []func(*RunRouting) (bool, error){
		func(r *RunRouting) (bool, error) {
			return r.OpenOperation(OperationRequest{ID: "op-review", Kind: OperationReview, Candidate: reviewing, Budget: OperationBudget{ReviewRounds: intPointer(3)}}, routingAt)
		},
		prepare("op-review", "att-1"),
		launched("op-review", "att-1"),
	} {
		state = route(t, store, state, step)
	}
	var disposition ResultDisposition
	state = route(t, store, state, func(r *RunRouting) (bool, error) {
		var changed bool
		var err error
		disposition, changed, err = r.AcceptResult(AttemptResult{
			Operation: "op-review", Attempt: "att-1", Candidate: "fedcba9876543210fedcba9876543210fedcba98",
			Ending: AttemptEnding{Classification: "approved", Termination: TerminationConfirmed, At: routingAt}, Usage: "event:run/7",
		})
		return changed, err
	})
	operation, _ := state.Routing.Operation("op-review")
	if attempt := operation.Attempts[0]; disposition != ResultRefused || attempt.State != AttemptLaunched || len(attempt.Usage) != 1 {
		t.Fatalf("a verdict on another candidate = %s, attempt %+v", disposition, attempt)
	}
	state = route(t, store, state, func(r *RunRouting) (bool, error) {
		var changed bool
		var err error
		disposition, changed, err = r.AcceptResult(AttemptResult{
			Operation: "op-review", Attempt: "att-1", Candidate: reviewing,
			Ending: AttemptEnding{Classification: "approved", Termination: TerminationConfirmed, At: routingAt},
		})
		return changed, err
	})
	operation, _ = state.Routing.Operation("op-review")
	if disposition != ResultAdopted || operation.Attempts[0].Ended.Classification != "approved" {
		t.Fatalf("the verdict on the candidate under review = %s, attempt %+v", disposition, operation.Attempts[0])
	}
}

// An external effect recorded before it was attempted stops the operation
// launching again until somebody establishes whether it happened, and once it
// is established as performed it is never intended again.
func TestAnEffectNobodyHasEstablishedIsReconciledBeforeAnythingRepeatsIt(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	for _, step := range []func(*RunRouting) (bool, error){
		openDevelop("op-1"),
		prepare("op-1", "att-1"),
		launched("op-1", "att-1"),
		func(r *RunRouting) (bool, error) {
			return r.IntendEffect("op-1", ExternalEffect{Key: "push:refs/heads/candidate@0123abc", Kind: "push", Attempt: "att-1", At: routingAt})
		},
		end("op-1", "att-1", InterruptedClassification, TerminationConfirmed),
		func(r *RunRouting) (bool, error) {
			return r.PrepareAttempt("op-1", AttemptRequest{ID: "att-2", Predecessor: "att-1", Mode: SessionReconstruction, Transient: true}, routingAt)
		},
	} {
		state = route(t, store, state, step)
	}
	if _, err := store.BeginLaunch(context.Background(), state.RunID, "op-1", "att-2"); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("BeginLaunch() with an effect nobody has established: error = %v", err)
	}
	unknown := LaunchRecovery{Effect: func(ExternalEffect) (bool, bool, error) { return false, false, nil }}
	found, err := store.ReconcileLaunch(context.Background(), state.RunID, "op-1", unknown)
	if err != nil {
		t.Fatal(err)
	}
	if found.Verdict != LaunchUncertain || load(t, store, state.RunID).Routing.Operations[0].Reconciling == nil {
		t.Fatalf("reconciliation with the effect unknown = %+v", found)
	}
	performed := LaunchRecovery{Effect: func(effect ExternalEffect) (bool, bool, error) { return effect.Kind == "push", true, nil }}
	found, err = store.ReconcileLaunch(context.Background(), state.RunID, "op-1", performed)
	if err != nil {
		t.Fatal(err)
	}
	state = load(t, store, state.RunID)
	operation := state.Routing.Operations[0]
	if found.Verdict != LaunchNeverStarted || found.Attempt != "att-2" || operation.Effects[0].State != EffectPerformed || operation.Reconciling != nil {
		t.Fatalf("reconciliation once the effect is established = %+v, operation %+v", found, operation)
	}
	if _, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) {
		return r.IntendEffect("op-1", ExternalEffect{Key: "push:refs/heads/candidate@0123abc", Kind: "push", Attempt: "att-2", At: routingAt})
	}); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("intending a performed effect again: error = %v", err)
	}
}

// An attempt is recorded as launched only once its execution is registered,
// so no record can say a provider began without saying which one.
func TestAnAttemptIsNotMarkedLaunchedWithoutARegisteredExecution(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := routedRun(t, store)
	state = route(t, store, state, openDevelop("op-1"))
	state = route(t, store, state, prepare("op-1", "att-1"))
	if _, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) { return r.MarkLaunched("op-1", "att-1", routingAt) }); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("MarkLaunched() with nothing registered: error = %v", err)
	}
	state = route(t, store, state, func(r *RunRouting) (bool, error) { return r.RegisterExecution("op-1", "att-1", testExecution("att-1")) })
	other := testExecution("att-1")
	other.PID = 5151
	if _, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) { return r.RegisterExecution("op-1", "att-1", other) }); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("registering a second execution for one attempt: error = %v", err)
	}
	wrongHold := testExecution("att-1")
	wrongHold.Hold = "att-9.hold"
	if _, err := store.UpdateRouting(context.Background(), state, func(r *RunRouting) (bool, error) { return r.RegisterExecution("op-1", "att-1", wrongHold) }); err == nil {
		t.Fatal("registering an execution that holds another attempt's file was accepted")
	}
	route(t, store, state, func(r *RunRouting) (bool, error) { return r.MarkLaunched("op-1", "att-1", routingAt) })
}

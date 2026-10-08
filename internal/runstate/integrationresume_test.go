package runstate

import (
	"strings"
	"testing"
	"time"
)

// approvedStoppedState is a run whose change was approved and whose promotion
// the environment then refused, with the stop recorded on it.
func approvedStoppedState(t *testing.T) State {
	t.Helper()
	state := integratedState(t, PhaseIntegrating)
	state.Status = StatusFailed
	state.Integration = nil
	state.Failure = "integrate approved change: primary checkout is not ready for integration"
	state.IntegrationStop = &IntegrationStop{
		Cause:      CauseDirtyPrimary,
		Detail:     state.Failure,
		Phase:      PhaseIntegrating,
		RecordedAt: state.UpdatedAt,
	}
	return state
}

// The record is what makes a stop resumable, and the shape of one is held to
// what a resumption needs: an approving verdict, no promotion, and a cause the
// harness records.
func TestAnIntegrationStopIsOnlyRecordedOnAnApprovedUnpromotedChange(t *testing.T) {
	t.Parallel()

	stopped := approvedStoppedState(t)
	if err := stopped.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the stopped run valid", err)
	}
	if !stopped.ResumableIntegration() || !stopped.ApprovedAwaitingIntegration() {
		t.Fatalf("the stopped run is not resumable: %#v", stopped)
	}
	for name, shape := range map[string]func(State) State{
		"beside a promotion": func(state State) State {
			state.Status = StatusSucceeded
			state.Failure = ""
			state.Integration = &Integration{
				TargetBranch: "main", SourceCommit: strings.Repeat("b", 40),
				TargetCommit: strings.Repeat("b", 40), PreviousTargetCommit: strings.Repeat("a", 40),
			}
			return state
		},
		"without an approval": func(state State) State {
			state.ReviewDecision = ReviewRepair
			return state
		},
		"at a phase before the review": func(state State) State {
			state.IntegrationStop.Phase = PhaseDeveloping
			return state
		},
		"with a cause nothing records": func(state State) State {
			state.IntegrationStop.Cause = "a reason somebody typed"
			return state
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := shape(approvedStoppedState(t)).Validate(); err == nil {
				t.Fatal("Validate() accepted an integration stop the record cannot have written")
			}
		})
	}
}

// conflictedState is a run whose approved change conflicted when it was
// replayed onto its target, with the conflict recorded on it and no blocker —
// the shape yoyodyne-ifd.441's run was left in when its blocker write timed out.
func conflictedState(t *testing.T) State {
	t.Helper()
	state := integratedState(t, PhaseIntegrating)
	state.Status = StatusFailed
	state.Integration = nil
	state.Failure = "change cannot be replayed onto the moved target branch: replay onto main failed with exit code 1\nrecord the replay conflict as a blocker: bd update failed with status timed_out and exit code -1: "
	state.ReplayConflict = &ReplayConflict{
		TargetBranch: "main",
		Detail:       "change cannot be replayed onto the moved target branch",
		Phase:        PhaseIntegrating,
		RecordedAt:   state.UpdatedAt,
	}
	return state
}

// A replay conflict is its own record, and it is a decision about the change —
// its author's to reconcile first (yoyodyne-ifd.132), a person's after that: a
// run carrying one is never resumable, and the record refuses it beside the
// integration stop it is the alternative to — which is the 441 misreading
// written down.
func TestAReplayConflictIsRecordedAsADecisionAndNeverBesideAnIntegrationStop(t *testing.T) {
	t.Parallel()

	conflicted := conflictedState(t)
	if err := conflicted.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the conflicted run valid", err)
	}
	if conflicted.ResumableIntegration() {
		t.Fatalf("a replay conflict reads as resumable: %#v", conflicted)
	}
	if said := conflicted.ReplayConflict.Says(conflicted.RunID); !strings.Contains(said, "its replay onto main conflicted") ||
		!strings.Contains(said, "a person to settle the conflict") || !strings.Contains(said, "`yoyo triage repair "+conflicted.RunID+"`") || !strings.Contains(said, "not `yoyo triage resume`") {
		t.Fatalf("Says() = %q, want the conflict, the repair-continue or the person, and the verb that cannot help", said)
	}
	if described := conflicted.ReplayConflict.Describe(); described != "approved, then stopped at the integrating phase by a replay conflict onto main" {
		t.Fatalf("Describe() = %q", described)
	}
	for name, shape := range map[string]func(State) State{
		"beside an integration stop": func(state State) State {
			state.ReplayConflict.Detail = ""
			state.IntegrationStop = &IntegrationStop{
				Cause: CauseTransportFailure, Detail: state.Failure, Phase: PhaseIntegrating, RecordedAt: state.UpdatedAt,
			}
			return state
		},
		"beside a promotion": func(state State) State {
			state.Status = StatusSucceeded
			state.Failure = ""
			state.Integration = &Integration{
				TargetBranch: "main", SourceCommit: strings.Repeat("b", 40),
				TargetCommit: strings.Repeat("b", 40), PreviousTargetCommit: strings.Repeat("a", 40),
			}
			return state
		},
		"with no target branch": func(state State) State {
			state.ReplayConflict.TargetBranch = ""
			return state
		},
		"at a phase before the review": func(state State) State {
			state.ReplayConflict.Phase = PhaseDeveloping
			return state
		},
		"with no time recorded": func(state State) State {
			state.ReplayConflict.RecordedAt = time.Time{}
			return state
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := shape(conflictedState(t)).Validate(); err == nil {
				t.Fatal("Validate() accepted a replay conflict the record cannot have written")
			}
		})
	}
}

// What a resumed run reads as while it promotes, and what it does not read as
// once the promotion is over or the replay has put it back through the gate.
func TestARunReadsAsResumingItsIntegrationOnlyWhileItPromotesAgain(t *testing.T) {
	t.Parallel()

	resumed := approvedStoppedState(t)
	resumed.Status = StatusRunning
	resumed.CompletedAt = nil
	resumed.Failure = ""
	resumed.IntegrationStop = nil
	resumed.IntegrationResumptions = []IntegrationResumption{{
		Cause: CauseDirtyPrimary, Reason: "resumed", ResumedAt: resumed.UpdatedAt,
		SupersededFailure: "integrate approved change: primary checkout is not ready for integration",
	}}
	if err := resumed.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !resumed.ResumingIntegration() {
		t.Fatalf("a resumed run at its promotion does not read as resuming: %#v", resumed)
	}
	replayed := resumed
	replayed.Phase = PhaseChecking
	replayed.ReviewDecision = ""
	replayed.ReviewSessionID = ""
	if replayed.ResumingIntegration() {
		t.Fatal("a resumed run put back through the gate still reads as resuming its integration")
	}
	landed := resumed
	landed.Status = StatusSucceeded
	completed := landed.UpdatedAt
	landed.CompletedAt = &completed
	landed.Phase = PhaseComplete
	if landed.ResumingIntegration() {
		t.Fatal("a resumed run that landed still reads as resuming its integration")
	}
	// A resumption with no account of itself is refused, exactly as a repair
	// continuation is.
	unaccounted := resumed
	unaccounted.IntegrationResumptions = []IntegrationResumption{{Cause: CauseDirtyPrimary, ResumedAt: time.Now()}}
	if err := unaccounted.Validate(); err == nil {
		t.Fatal("Validate() accepted a resumption with no reason")
	}
}

// The summary a listing reads carries both facts, so a surface that shows the
// reason shows what the reason means beside it.
func TestTheSummaryCarriesTheIntegrationStopAndTheResumption(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	stopped := approvedStoppedState(t)
	if err := store.Create(stopped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	summary := store.summarize(stopped)
	if summary.IntegrationStop == nil || summary.IntegrationStop.Cause != CauseDirtyPrimary || summary.ResumingIntegration {
		t.Fatalf("summary = stop %#v, resuming %t; want the stop carried and the run not resuming", summary.IntegrationStop, summary.ResumingIntegration)
	}
	resumed := stopped
	resumed.Status = StatusRunning
	resumed.CompletedAt = nil
	resumed.Failure = ""
	resumed.IntegrationStop = nil
	resumed.IntegrationResumptions = []IntegrationResumption{{Cause: CauseDirtyPrimary, Reason: "resumed", ResumedAt: resumed.UpdatedAt}}
	summary = store.summarize(resumed)
	if summary.IntegrationStop != nil || !summary.ResumingIntegration {
		t.Fatalf("summary = stop %#v, resuming %t; want the resumed run read as resuming", summary.IntegrationStop, summary.ResumingIntegration)
	}
}

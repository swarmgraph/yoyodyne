package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// refuseForIdentity leaves the standing finding a decision refused for want of
// GitHub's acceptance of the harness's identity leaves on the record.
func (h *rerunHarness) refuseForIdentity(t *testing.T) {
	t.Helper()
	if _, err := h.runs.Triage().RecordCarryOutRefusal(context.Background(), docketedItem, runstate.TriageCarryOut{
		RunID: docketedRunID, Decision: runstate.TriageDecisionRerun, DecidedAt: docketedNow,
		Gate:    runstate.TriageGateHarness,
		Refusal: "git push: No user exists for uid 501",
		Clears:  gitHubIdentityClears,
	}, docketedNow); err != nil {
		t.Fatalf("RecordCarryOutRefusal() error = %v", err)
	}
}

func TestAnIdentityRefusalIsAskedAgainOnceGitHubAnswersWithoutWaitingForTheClock(t *testing.T) {
	t.Parallel()
	harness := newRerunHarness(t, stoppedState())
	harness.refuseForIdentity(t)
	carrying := harness.carryOutAt(time.Minute)
	calls := 0
	carrying.GitHub = func(context.Context) error { calls++; return nil }
	outstanding, err := carrying.Outstanding()
	if err != nil || len(outstanding) != 1 {
		t.Fatalf("Outstanding() = %#v, %v; want the decision offered again one minute after the refusal, GitHub answering", outstanding, err)
	}
	if calls != 1 {
		t.Fatalf("GitHub asked %d times, want once", calls)
	}
}

func TestAnIdentityRefusalIsNotAskedAgainWhileGitHubRefusesHoweverLongItIs(t *testing.T) {
	t.Parallel()
	harness := newRerunHarness(t, stoppedState())
	harness.refuseForIdentity(t)
	carrying := harness.carryOutAt(20 * time.Minute)
	carrying.GitHub = func(context.Context) error { return errors.New("Permission denied (publickey)") }
	outstanding, err := carrying.Outstanding()
	if err != nil || len(outstanding) != 0 {
		t.Fatalf("Outstanding() = %#v, %v; want nothing offered while GitHub refuses, past the pacing", outstanding, err)
	}
}

func TestAnIdentityRefusalWithoutAProbeKeepsItsOrdinaryPacing(t *testing.T) {
	t.Parallel()
	harness := newRerunHarness(t, stoppedState())
	harness.refuseForIdentity(t)
	if out, err := harness.carryOutAt(time.Minute).Outstanding(); err != nil || len(out) != 0 {
		t.Fatalf("Outstanding() = %#v, %v; want the refusal paced by the clock", out, err)
	}
	if out, err := harness.carryOutAt(20 * time.Minute).Outstanding(); err != nil || len(out) != 1 {
		t.Fatalf("Outstanding() = %#v, %v; want the decision offered after the pacing", out, err)
	}
}

func TestTheGateForAnIdentityRefusalNamesWhatClearsIt(t *testing.T) {
	t.Parallel()
	gate, clears := carryOutGate(errors.New("No user exists for uid 501"))
	if gate != runstate.TriageGateHarness || clears != gitHubIdentityClears {
		t.Fatalf("carryOutGate() = %q, %q; want the harness gate and the identity words", gate, clears)
	}
	if len(clears) > runstate.MaxTriageCarryOutClearsBytes {
		t.Fatalf("clears is %d bytes, over the %d the record holds", len(clears), runstate.MaxTriageCarryOutClearsBytes)
	}
}

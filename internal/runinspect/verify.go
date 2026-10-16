package runinspect

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Verdict is whether an expected state holds. Unknown is what any check whose
// evidence could not be read says, and it is never met.
type Verdict string

const (
	Met     Verdict = "met"
	NotMet  Verdict = "not met"
	Unknown Verdict = "unknown"
)

// Verification is one expected state checked against one run.
type Verification struct {
	Expect     Expected  `json:"expect"`
	Verdict    Verdict   `json:"verdict"`
	ObservedAt time.Time `json:"observed_at"`
	// Evidence is what was read to decide, one sentence each.
	Evidence []string `json:"evidence"`
}

// Verify checks one run for one expected state. An expected state off the
// closed list answers unknown here as well, though a request naming one is
// refused before it reaches this.
func (i Inspector) Verify(_ context.Context, runID string, expect Expected) Verification {
	now := i.now()
	verification := Verification{Expect: expect, Verdict: Unknown, ObservedAt: now}
	answer := func(verdict Verdict, evidence ...string) Verification {
		verification.Verdict = verdict
		verification.Evidence = append(verification.Evidence, evidence...)
		redactor := execution.NewRedactor(i.RedactValues...)
		for n, line := range verification.Evidence {
			verification.Evidence[n] = redactor.Redact(line)
		}
		return verification
	}
	if !expect.Known() {
		return answer(Unknown, fmt.Sprintf("%q is not an expected state this tool checks", expect))
	}
	state, err := i.Runs.Read(runID)
	if err != nil {
		return answer(Unknown, fmt.Sprintf("the run record could not be read: %v", err))
	}
	status := fmt.Sprintf("the record says the run is %s", state.Status)
	if state.Phase != "" {
		status += fmt.Sprintf(", at %s", state.Phase)
	}
	verification.Evidence = append(verification.Evidence, status)

	switch expect {
	case ExpectWorkerLive:
		reading := i.Runs.Worker(state, now)
		switch reading.Liveness {
		case runstate.WorkerLive:
			return answer(Met, reading.Says)
		case runstate.WorkerAbsent:
			return answer(NotMet, reading.Says)
		}
		return answer(Unknown, reading.Says)
	case ExpectWaitRecorded:
		if wait := WaitOf(state); wait != nil {
			return answer(Met, fmt.Sprintf("it waits on %s, released by %s", wait.Kind, wait.Release))
		}
		return answer(NotMet, "the record names no wait")
	case ExpectReservationReleased:
		reservation := reservationOf(state)
		if reservation.Held {
			return answer(NotMet, reservation.Says)
		}
		return answer(Met, reservation.Says)
	case ExpectContinuationStarted:
		last := lastContinuation(state)
		if last == nil {
			return answer(NotMet, "the record names no continuation")
		}
		if last.Pending {
			return answer(Unknown, describeContinuation(*last))
		}
		return answer(Met, describeContinuation(*last))
	case ExpectRepairStarted:
		if len(state.RepairContinuations) == 0 {
			return answer(NotMet, "the record names no repair")
		}
		repair := state.RepairContinuations[len(state.RepairContinuations)-1]
		if repair.DispatchPending {
			return answer(Unknown, fmt.Sprintf("a repair of %d attempt(s) was recorded at %s and the pipeline has not yet confirmed taking it up",
				repair.GrantedAttempts, repair.ContinuedAt.UTC().Format(time.RFC3339)))
		}
		return answer(Met, fmt.Sprintf("a repair of %d attempt(s) was taken up at %s", repair.GrantedAttempts, repair.ContinuedAt.UTC().Format(time.RFC3339)))
	case ExpectRerunHoldsItem:
		active, err := i.Runs.Incomplete()
		if err != nil {
			return answer(Unknown, fmt.Sprintf("the runs in flight could not be listed: %v", err))
		}
		for _, other := range active {
			if other.WorkItemID == state.WorkItemID && other.RunID != state.RunID && !other.StartedAt.Before(state.StartedAt) {
				return answer(Met, fmt.Sprintf("run %s, started at %s, is in flight on %s", other.RunID, other.StartedAt.UTC().Format(time.RFC3339), state.WorkItemID))
			}
		}
		return answer(NotMet, fmt.Sprintf("no later run is in flight on %s", state.WorkItemID))
	case ExpectIntegrationCompleted:
		if state.Integration != nil && state.Integration.TargetCommit != "" {
			return answer(Met, fmt.Sprintf("its change is on %s at %s", state.Integration.TargetBranch, state.Integration.TargetCommit))
		}
		return answer(NotMet, "the record names no completed integration")
	}
	// What remains is recorded by recovery actions this build does not carry
	// out, so it has nothing to read them from.
	return answer(Unknown, fmt.Sprintf("this build records nothing that shows %q; the recovery that records it is built by lifecycle recovery (yoyodyne-06m) and stopped-run recovery", expect))
}

func describeContinuation(c Continuation) string {
	text := fmt.Sprintf("%s at %s", c.Kind, c.At.UTC().Format(time.RFC3339))
	if c.Pending {
		text += ", recorded and not yet taken up by the pipeline"
	}
	return text
}

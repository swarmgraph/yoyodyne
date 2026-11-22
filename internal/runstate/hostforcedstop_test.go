package runstate

import (
	"testing"
	"time"
)

// The stop a forced scheduler could not record is written only on a run that
// scheduler was working and that is still running with nothing on its record:
// not on a run a later process holding the same number took up, nor on one
// already carrying a stop, and at the phase the run had reached.
func TestAForcedHostsStopIsRecordedOnlyOnTheRunsThatHostWasWorking(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 11, 22, 6, 2, 0, 0, time.UTC)
	at := started.Add(2 * time.Hour)
	host := WorkerIdentity{PID: 79627, StartedAt: started}
	update := HostForcedStopRecorded{Host: host, Stop: RedeployStop{At: at, BoundSeconds: 900, SessionID: "watch-forced", CutOff: "forced"}}
	run := func() State {
		return State{RunID: "run-1", Status: StatusRunning, Phase: PhaseReviewing, UpdatedAt: started.Add(time.Hour),
			Worker: &WorkerIdentity{PID: 79627, StartedAt: started, RunID: "run-1", Attempt: 1}}
	}

	worked := run()
	changed, err := update.apply(&worked)
	if err != nil || !changed || worked.RedeployStop == nil || worked.RedeployStop.Phase != PhaseReviewing || !worked.UpdatedAt.Equal(at) {
		t.Fatalf("apply() = %t, %v, run %+v, want the stop recorded at the run's phase", changed, err, worked.RedeployStop)
	}

	reused := run()
	reused.Worker.StartedAt = started.Add(time.Hour)
	if changed, err := update.apply(&reused); err != nil || changed {
		t.Fatalf("apply() on a run of a later process with the same number = %t, %v, want nothing changed", changed, err)
	}

	stoppedAlready := run()
	earlier := RedeployStop{At: started.Add(90 * time.Minute), Phase: PhaseReviewing, BoundSeconds: 900}
	stoppedAlready.RedeployStop = &earlier
	if changed, err := update.apply(&stoppedAlready); err != nil || changed || stoppedAlready.RedeployStop.At != earlier.At {
		t.Fatalf("apply() on a run already stopped = %t, %v, want the earlier stop kept", changed, err)
	}

	ended := run()
	ended.Status = StatusFailed
	if changed, err := update.apply(&ended); err != nil || changed {
		t.Fatalf("apply() on an ended run = %t, %v, want nothing changed", changed, err)
	}
}

package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// heldByTheBrake is a brake hold placed three hours before moment, on which
// the development manager decided a probe that has been out since two minutes
// after the trip.
func heldByTheBrake() runstate.IntakeHold {
	trippedAt := moment.Add(-3 * time.Hour)
	decidedAt := trippedAt.Add(2 * time.Minute)
	return runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        trippedAt,
		HeldBy:        runstate.IntakeHolderBrake,
		Reason:        "three runs blocked in a row with nothing landing",
		Brake: &runstate.IntakeBrake{
			Blocked:        []runstate.BrakeBlockedRun{{RunID: "run-a", WorkItemID: "yoyodyne-ifd.428.104", Reason: "paused for a new build"}},
			Decision:       runstate.BrakeDecisionProbe,
			DecidedAt:      &decidedAt,
			DecisionReason: "none of the three stops was a fault in a change",
			CooldownEndsAt: trippedAt.Add(30 * time.Minute),
			Probe:          &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.430.60", RunID: "run-probe", StartedAt: decidedAt},
			Probes:         1,
		},
	}
}

// While the brake holds new work, the standing reading carries the hold with
// what it is costing — how long it has stood, how many developer slots are idle
// under it, how long the probe run has been out — and puts it to the
// development manager as a decision to make again, her earlier decision shown
// beside it rather than taking it off.
func TestAHeldLineIsReadWithItsCostAndPutAgainWhateverWasDecided(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Capacity = 3
	sources.BrakeProbeOverdueAfter = 45 * time.Minute
	sources.IntakeHolds = fakeIntakeHolds{held: true, hold: heldByTheBrake()}
	probe := inFlight("run-probe", "yoyodyne-ifd.430.60", 0)
	probe.StartedAt = moment.Add(-3*time.Hour + 2*time.Minute)
	probe.UpdatedAt = moment
	sources.Runs = fakeRuns{incomplete: []runstate.State{probe}, prices: map[string]runstate.ItemPrice{}}

	standing := ReadStanding(context.Background(), sources)
	line := standing.HeldLine
	if line == nil {
		t.Fatalf("a brake hold was read with no held line: %+v", standing)
	}
	if line.Capacity != 3 || line.Busy != 1 || line.Idle != 2 {
		t.Errorf("slots = %d busy and %d idle of %d, want 1 busy and 2 idle of 3", line.Busy, line.Idle, line.Capacity)
	}
	if line.HeldFor != 3*time.Hour {
		t.Errorf("held for %s, want 3h", line.HeldFor)
	}
	if line.ProbeOut != 2*time.Hour+58*time.Minute || !line.ProbeOverdue {
		t.Errorf("probe out %s, overdue %t; want 2h58m and overdue", line.ProbeOut, line.ProbeOverdue)
	}
	rendered := line.Render()
	for _, want := range []string{
		"## The hold on new work: decide it again on this pass",
		"for 3 hours: three runs blocked in a row with nothing landing",
		"2 of 3 developer slots are idle under it",
		"A held line costs work every minute it stands",
		"The probe run run-probe of yoyodyne-ifd.430.60 has been out for 2 hours 58 minutes",
		`You decided "probe" at ` + localMoment(*heldByTheBrake().Brake.DecidedAt) + ": none of the three stops was a fault in a change",
		"That decision does not settle the hold for later passes; decide it again now.",
		`{"action":"brake","decision":"release","reason":"why"}`,
		"say in your account what keeps it held",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the held line does not say %q:\n%s", want, rendered)
		}
	}
}

// A probe run out past the configured time is put back on her list of stopped
// runs, naming the run and how long it has been out; one inside it is not, and
// a time of zero never puts one back.
func TestAnOverdueProbeRunReturnsToTheListOfStoppedRuns(t *testing.T) {
	t.Parallel()

	hold := heldByTheBrake()
	started := hold.Brake.Probe.StartedAt
	within, _ := HeldLineOf(hold, 3, 1, "", 45*time.Minute, started.Add(44*time.Minute))
	if entry := within.OverdueEntry(); entry != "" {
		t.Errorf("a probe out 44 minutes of 45 was put back: %q", entry)
	}
	past, _ := HeldLineOf(hold, 3, 1, "", 45*time.Minute, started.Add(70*time.Minute))
	entry := past.OverdueEntry()
	for _, want := range []string{
		"The probe run run-probe of yoyodyne-ifd.430.60",
		"has been out for 1 hour 10 minutes, past the 45 minutes a probe run is allowed",
		"back on this list as a stopped run",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("the overdue entry does not say %q: %q", want, entry)
		}
	}
	never, _ := HeldLineOf(hold, 3, 1, "", 0, started.Add(24*time.Hour))
	if entry := never.OverdueEntry(); entry != "" {
		t.Errorf("a zero bound put a probe back: %q", entry)
	}
}

// The operator's own hold is his and is not put to her; and where the runs in
// flight could not be read, the idle slots are said as uncounted rather than
// as every slot idle.
func TestAHeldLineIsTheBrakesAndSaysWhenItsSlotsCouldNotBeCounted(t *testing.T) {
	t.Parallel()

	operators := runstate.IntakeHold{HeldAt: moment, HeldBy: "operator", Reason: "holding for a release"}
	if _, braked := HeldLineOf(operators, 3, 0, "", 45*time.Minute, moment); braked {
		t.Error("the operator's own hold was read as a held line for the development manager")
	}
	line, _ := HeldLineOf(heldByTheBrake(), 3, -1, "the run log is locked", 45*time.Minute, moment)
	rendered := line.Render()
	if !strings.Contains(rendered, "How many of the 3 developer slots are idle could not be counted: the run log is locked.") {
		t.Errorf("an uncounted line rendered as:\n%s", rendered)
	}
	if strings.Contains(rendered, "are idle under it") {
		t.Errorf("an uncounted line claimed a count:\n%s", rendered)
	}
}

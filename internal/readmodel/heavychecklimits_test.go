package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// measuredLaunch is one finished launch of check with running of it at once,
// that ran for ran against a time limit of budget.
func measuredLaunch(check string, limit, running int, ran, budget time.Duration, outcome runstate.HeavyCheckOutcome) runstate.HeavyCheckMeasurement {
	launched := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	exited := launched.Add(ran)
	load := float64(running) * 2
	return runstate.HeavyCheckMeasurement{
		Permit: "permit-0123456789abcdef", Check: check, Limit: limit,
		AskedAt: launched.Add(-time.Minute), GrantedAt: launched, LaunchedAt: launched, ExitedAt: &exited,
		BudgetMS: budget.Milliseconds(), Outcome: outcome,
		Running: map[string]int{check: running}, Machine: runstate.HeavyCheckReading{Load: &load, Listed: true},
	}
}

func launches(count int, launch runstate.HeavyCheckMeasurement) []runstate.HeavyCheckMeasurement {
	out := make([]runstate.HeavyCheckMeasurement, count)
	for index := range out {
		out[index] = launch
	}
	return out
}

func readOne(t *testing.T, measured []runstate.HeavyCheckMeasurement) HeavyCheckLimitReading {
	t.Helper()
	limits := ReadHeavyCheckLimits(runstate.HeavyCheckMeasurements{SchemaVersion: runstate.HeavyCheckMeasurementSchemaVersion, Measurements: measured})
	if len(limits.Checks) != 1 {
		t.Fatalf("checks = %#v, want one", limits.Checks)
	}
	return limits.Checks[0]
}

// A check that finished well inside its time limit at the limit it ran under
// is proposed at that limit, and never above it: nothing measured says more
// would hold.
func TestAHeavyCheckThatHoldsAtItsLimitIsProposedAtItAndNoHigher(t *testing.T) {
	t.Parallel()
	measured := append(launches(5, measuredLaunch("make test", 2, 1, 4*time.Minute, 20*time.Minute, runstate.HeavyCheckPassed)),
		launches(5, measuredLaunch("make test", 2, 2, 9*time.Minute, 20*time.Minute, runstate.HeavyCheckFailed))...)
	reading := readOne(t, measured)
	if reading.Proposed != 2 || !strings.Contains(reading.Because, "not measured") {
		t.Fatalf("proposed %d because %q, want the limit of 2 kept and nothing above it claimed", reading.Proposed, reading.Because)
	}
	if reading.Outcomes[runstate.HeavyCheckFailed] != 5 || reading.WaitMedian != time.Minute || len(reading.Levels) != 2 {
		t.Fatalf("reading = %#v", reading)
	}
}

// A check stopped by its time limit at a number running at once is proposed
// below that number, however few launches there were at it.
func TestAHeavyCheckStoppedByItsTimeLimitIsProposedBelowWhereItStopped(t *testing.T) {
	t.Parallel()
	measured := append(launches(6, measuredLaunch("make race", 2, 1, 10*time.Minute, 30*time.Minute, runstate.HeavyCheckPassed)),
		measuredLaunch("make race", 2, 2, 30*time.Minute, 30*time.Minute, runstate.HeavyCheckTimedOut))
	reading := readOne(t, measured)
	if reading.Proposed != 1 || !strings.Contains(reading.Because, "stopped by their time limit") {
		t.Fatalf("proposed %d because %q, want 1", reading.Proposed, reading.Because)
	}
}

// A check that took most of its time limit fails the number it ran at too, and
// one that does so alone is told that fewer at once cannot help.
func TestAHeavyCheckNearItsTimeLimitAloneIsToldFewerCannotHelp(t *testing.T) {
	t.Parallel()
	reading := readOne(t, launches(5, measuredLaunch("make race", 1, 1, 27*time.Minute, 30*time.Minute, runstate.HeavyCheckPassed)))
	if reading.Proposed != 1 || !strings.Contains(reading.Because, "fewer at once cannot help") {
		t.Fatalf("proposed %d because %q", reading.Proposed, reading.Because)
	}
}

// Too few launches at the limit propose nothing, and say how many it takes.
func TestTooFewHeavyCheckLaunchesProposeNothing(t *testing.T) {
	t.Parallel()
	measured := launches(2, measuredLaunch("make race", 1, 1, 5*time.Minute, 30*time.Minute, runstate.HeavyCheckPassed))
	unfinished := measuredLaunch("make race", 1, 1, 0, 30*time.Minute, "")
	unfinished.ExitedAt = nil
	reading := readOne(t, append(measured, unfinished))
	if reading.Proposed != 0 || !strings.Contains(reading.Because, "only 2 launches finished") {
		t.Fatalf("proposed %d because %q", reading.Proposed, reading.Because)
	}
	if reading.Levels[0].Launches != 3 || reading.Levels[0].Finished != 2 {
		t.Fatalf("level = %#v, want the unfinished launch counted but not judged", reading.Levels[0])
	}
}

// The machine's load is read against how many heavy checks of every name were
// running, with the launches that started beside a suite run outside every
// place counted, and the ones whose processes could not be listed said.
func TestTheMachineIsReadByHowManyHeavyChecksWereRunning(t *testing.T) {
	t.Parallel()
	alone := measuredLaunch("make race", 1, 1, time.Minute, time.Hour, runstate.HeavyCheckPassed)
	beside := measuredLaunch("make test", 2, 1, time.Minute, time.Hour, runstate.HeavyCheckPassed)
	beside.Running = map[string]int{"make test": 1, "make race": 1}
	beside.Outside = 3
	unlisted := alone
	unlisted.Machine.Listed = false
	limits := ReadHeavyCheckLimits(runstate.HeavyCheckMeasurements{Measurements: []runstate.HeavyCheckMeasurement{alone, beside, unlisted}})
	if len(limits.Machine) != 2 || limits.Machine[0].Running != 1 || limits.Machine[0].Launches != 2 || limits.Machine[1].Running != 2 {
		t.Fatalf("machine = %#v", limits.Machine)
	}
	if limits.Machine[1].Outside != 1 || limits.Machine[1].OutsideMost != 3 || limits.Unlisted != 1 {
		t.Fatalf("machine = %#v, unlisted = %d", limits.Machine, limits.Unlisted)
	}
	if len(limits.Checks) != 2 || limits.Checks[1].Outside != 1 {
		t.Fatalf("checks = %#v, want the test suite's launch beside a developer's suite counted", limits.Checks)
	}
}

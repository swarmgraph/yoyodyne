//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A launch is written down as it starts, with what the machine looked like and
// how many of each heavy check held places, and finished as it exits with how
// it ended and the time limit it had. A process the listing showed in a placed
// check's own process group is that check's; one in any other group is a suite
// run outside the places, which is what a developer's own `make test` is.
func TestAHeavyCheckLaunchIsMeasuredFromLaunchToExit(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	other := acquireSoon(t, store, HeavyCheckAsk{
		Product: "yoyodyne", RunID: "run-a", Check: "make test", Command: "make test", Limit: 2, Priority: domain.HeavyCheckOrdinary,
	})
	otherCommand := launchHeld(t, other, "sleep 1")
	otherGroup := otherCommand.Process.Pid

	load, free := 3.5, uint64(8<<30)
	ask := heavyAsk("run-b", "make race", 1, domain.HeavyCheckForLanding)
	ask.Read = func() HeavyCheckReading {
		return HeavyCheckReading{Load: &load, Cores: 10, FreeMemory: &free, Listed: true, Seen: []HeavyCheckProcessSeen{
			{PID: otherGroup + 1, ProcessGroup: otherGroup, Check: "make test"},
			{PID: 4242, ProcessGroup: 4240, Check: "make test"},
		}}
	}
	grant := acquireSoon(t, store, ask)
	command := launchHeld(t, grant, "exit 0")
	_ = command.Wait()
	grant.Measure(HeavyCheckPassed, 20*time.Minute)
	if _, err := grant.Finish("succeeded, exit code 0"); err != nil {
		t.Fatal(err)
	}
	if grant.MeasureProblem != nil {
		t.Fatalf("measure problem = %v", grant.MeasureProblem)
	}
	_ = otherCommand.Wait()
	_, _ = other.Finish("succeeded, exit code 0")

	measurements, err := store.Measurements()
	if err != nil {
		t.Fatal(err)
	}
	var measured *HeavyCheckMeasurement
	for index := range measurements.Measurements {
		if measurements.Measurements[index].Permit == grant.Permit.ID {
			measured = &measurements.Measurements[index]
		}
	}
	if measured == nil {
		t.Fatalf("measurements = %#v, want the race launch among them", measurements)
	}
	if measured.Check != "make race" || measured.Limit != 1 || measured.Priority != domain.HeavyCheckForLanding || measured.RunID != "run-b" {
		t.Fatalf("measurement = %#v, want whose launch it was", measured)
	}
	if measured.Running["make race"] != 1 || measured.Running["make test"] != 1 {
		t.Fatalf("running = %v, want the race launch itself and the test suite in its place", measured.Running)
	}
	if measured.Machine.Load == nil || *measured.Machine.Load != 3.5 || measured.Machine.FreeMemory == nil || *measured.Machine.FreeMemory != free || !measured.Machine.Listed {
		t.Fatalf("machine = %#v, want the reading taken as it launched", measured.Machine)
	}
	if measured.Outside != 1 || len(measured.OutsideSeen) != 1 || measured.OutsideSeen[0].PID != 4242 {
		t.Fatalf("outside = %d %#v, want only the suite in no place's process group", measured.Outside, measured.OutsideSeen)
	}
	ran, ended := measured.Ran()
	if !ended || ran < 0 || measured.Outcome != HeavyCheckPassed || measured.Budget() != 20*time.Minute {
		t.Fatalf("measurement = %#v, want it finished as passed with its 20m time limit", measured)
	}
}

// A check whose processes have all gone can have its place given back by
// another run's request for one before its own harness writes its end. The end
// is still measured, so the launch does not read as one that never finished.
func TestAHeavyCheckReleasedBeforeItFinishesIsStillMeasuredToItsExit(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	grant := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	command := launchHeld(t, grant, "exit 0")
	_ = command.Wait()
	// The next request for a place settles the record first, and finds every
	// process of this check gone.
	deadline := time.Now().Add(time.Minute)
	for {
		record, err := store.Settle(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		released := false
		for _, permit := range record.Permits {
			released = released || permit.ID == grant.Permit.ID && permit.State == HeavyCheckReleased
		}
		if released {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record = %#v, want the exited check's place given back", record)
		}
		time.Sleep(50 * time.Millisecond)
	}
	grant.Measure(HeavyCheckPassed, 20*time.Minute)
	if _, err := grant.Finish("succeeded, exit code 0"); err != nil {
		t.Fatal(err)
	}
	if grant.MeasureProblem != nil {
		t.Fatalf("measure problem = %v", grant.MeasureProblem)
	}
	measurements, err := store.Measurements()
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements.Measurements) != 1 {
		t.Fatalf("measurements = %#v, want the one launch", measurements)
	}
	measured := measurements.Measurements[0]
	if _, ended := measured.Ran(); !ended || measured.Outcome != HeavyCheckPassed || measured.Budget() != 20*time.Minute {
		t.Fatalf("measurement = %#v, want it finished as passed with its 20m time limit", measured)
	}
}

// A launch whose harness stopped before writing its end down is kept as
// launched with no end, so it is read as unknown rather than as a pass; and
// the measurements keep only the last few launches.
func TestHeavyCheckMeasurementsKeepTheLastLaunchesAndLeaveAnUnwrittenEndOpen(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	grant := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	command := launchHeld(t, grant, "exit 0")
	_ = command.Wait()
	// The harness dies here: nothing records how the check ended.
	measurements, err := store.Measurements()
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements.Measurements) != 1 || measurements.Measurements[0].ExitedAt != nil || measurements.Measurements[0].Machine.Listed {
		t.Fatalf("measurements = %#v, want one launch with no end and no listing", measurements)
	}
	if _, ended := measurements.Measurements[0].Ran(); ended {
		t.Fatal("a launch with no end written down reads as ended")
	}

	session, err := store.open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	now := time.Now()
	for index := 0; index < heavyCheckMeasurementsKept+3; index++ {
		permit := HeavyCheckPermit{ID: "permit-" + "00000000" + leftPad(index), Product: "yoyodyne", RunID: "run-c", Check: "make race", Command: "make race",
			Priority: domain.HeavyCheckOrdinary, AskedAt: now, GrantedAt: &now, LaunchedAt: &now}
		if err := session.measureLaunch(permit, HeavyCheckReading{}); err != nil {
			t.Fatal(err)
		}
	}
	measurements, err = readHeavyCheckMeasurements(session.root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements.Measurements) != heavyCheckMeasurementsKept || measurements.Measurements[0].Permit == grant.Permit.ID {
		t.Fatalf("kept %d measurements, want the last %d", len(measurements.Measurements), heavyCheckMeasurementsKept)
	}
}

func leftPad(index int) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	for position := 7; position >= 0; position-- {
		out[position] = digits[index%16]
		index /= 16
	}
	return string(out)
}

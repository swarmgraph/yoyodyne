package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// heavyRun is one run's stage through the real check runner, the real process
// runner, and the real record of places.
type heavyRun struct {
	results []checks.Result
	waited  time.Duration
	err     error
}

// runHeavyStages runs one stage per run id at once, each running commands,
// against one record of places, and returns each stage's results by run id.
func runHeavyStages(t *testing.T, stateRoot string, heavy map[string]int, runIDs []string, commands []string, timeout time.Duration) map[string]heavyRun {
	t.Helper()
	store, err := runstate.NewHeavyCheckStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	var permits checks.Permits
	if len(heavy) > 0 {
		permits = HeavyCheckPermits{Store: store, Product: "yoyodyne", Execution: config.Execution{HeavyChecks: heavy}}
	}
	runner := checks.Runner{Process: execution.OSProcessRunner{}, Timeout: timeout, StageTimeout: timeout, Permits: permits}
	var mu sync.Mutex
	runs := map[string]heavyRun{}
	var wait sync.WaitGroup
	for _, runID := range runIDs {
		wait.Add(1)
		go func(runID string) {
			defer wait.Done()
			var waited time.Duration
			results, _, err := runner.Run(context.Background(), checks.Request{
				RunID: runID, Directory: t.TempDir(), Commands: commands,
				Permit: checks.PermitRequest{
					WorkItemID: "item", Priority: domain.HeavyCheckOrdinary,
					Granted: func(_ string, took time.Duration) { waited += took },
				},
			}, nil)
			mu.Lock()
			runs[runID] = heavyRun{results: results, waited: waited, err: err}
			mu.Unlock()
		}(runID)
	}
	wait.Wait()
	return runs
}

// spans reads the start and end lines the race stand-in appended, one pair per
// launch, as [start, end] in Unix nanoseconds.
func spans(t *testing.T, log string) [][2]int64 {
	t.Helper()
	source, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	starts := map[string]int64{}
	var found [][2]int64
	for _, line := range strings.Split(strings.TrimSpace(string(source)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		at, _ := strconv.ParseInt(fields[2], 10, 64)
		if fields[0] == "start" {
			starts[fields[1]] = at
		} else {
			found = append(found, [2]int64{starts[fields[1]], at})
		}
	}
	sort.Slice(found, func(first, second int) bool { return found[first][0] < found[second][0] })
	return found
}

// raceStandIn is a check that writes when it starts and ends and sleeps in
// between, so the order two of them ran in is read off a file.
func raceStandIn(t *testing.T, sleep string) (command, log string) {
	t.Helper()
	directory := t.TempDir()
	log = filepath.Join(directory, "spans")
	script := filepath.Join(directory, "race.sh")
	body := fmt.Sprintf("#!/bin/sh\nid=$$\necho \"start $id $(date +%%s)000000000\" >> '%s'\nsleep %s\necho \"end $id $(date +%%s)000000000\" >> '%s'\n", log, sleep, log)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return "sh " + script, log
}

// Two runs at the race suite with one place run one after the other, and the
// second, which waited out the whole of the first, is not stopped by a budget
// shorter than the two together: its time starts when it launches.
func TestTwoRunsAtARaceSuiteWithOnePlaceRunInTurnEachOnItsOwnTime(t *testing.T) {
	t.Parallel()
	command, log := raceStandIn(t, "2")
	stateRoot := t.TempDir()
	runs := runHeavyStages(t, stateRoot, map[string]int{command: 1}, []string{"run-a", "run-b"}, []string{command}, 3500*time.Millisecond)
	var waits []time.Duration
	for runID, run := range runs {
		if run.err != nil || len(run.results) != 1 || !run.results[0].Passed {
			t.Fatalf("%s: results = %#v, err = %v; want the check to pass within its own budget", runID, run.results, run.err)
		}
		waits = append(waits, run.waited)
	}
	launched := spans(t, log)
	if len(launched) != 2 || launched[1][0] < launched[0][1] {
		t.Fatalf("launches = %v, want the second to start only once the first had ended", launched)
	}
	sort.Slice(waits, func(first, second int) bool { return waits[first] < waits[second] })
	if waits[1] < time.Second {
		t.Fatalf("waits = %v, want one run to have waited out the other", waits)
	}
	record, err := mustHeavyStore(t, stateRoot).Record()
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Holding("")) != 0 || len(record.Waiting()) != 0 {
		t.Fatalf("record = %#v, want every place given back", record)
	}
}

func mustHeavyStore(t *testing.T, root string) *runstate.HeavyCheckStore {
	t.Helper()
	store, err := runstate.NewHeavyCheckStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A project that declares no heavy checks runs them as it always did: both
// runs' suites at once, and the record of places never made.
func TestNoHeavyChecksDeclaredRunsChecksAtOnceAndOpensNoRecord(t *testing.T) {
	t.Parallel()
	command, log := raceStandIn(t, "2")
	stateRoot := t.TempDir()
	runs := runHeavyStages(t, stateRoot, nil, []string{"run-a", "run-b"}, []string{command}, 30*time.Second)
	for runID, run := range runs {
		if run.err != nil || len(run.results) != 1 || !run.results[0].Passed || run.waited != 0 {
			t.Fatalf("%s: %#v", runID, run)
		}
	}
	launched := spans(t, log)
	if len(launched) != 2 || launched[1][0] >= launched[0][1] {
		t.Fatalf("launches = %v, want both at once", launched)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, runstate.HeavyCheckDirectory)); !os.IsNotExist(err) {
		t.Fatalf("the record of places was made with no heavy check declared: %v", err)
	}
}

// A command no heavy check names is never asked for a place.
func TestACheckNoHeavyCheckNamesIsNotAskedForAPlace(t *testing.T) {
	t.Parallel()
	permits := HeavyCheckPermits{Store: mustHeavyStore(t, t.TempDir()), Product: "yoyodyne", Execution: config.Execution{HeavyChecks: map[string]int{"make race": 1}}}
	permit, err := permits.Acquire(context.Background(), checks.PermitAsk{RunID: "run-a", Command: "make vet"}, nil)
	if err != nil || permit != nil {
		t.Fatalf("Acquire = %v, %v; want no permit", permit, err)
	}
}

func TestTheGatesChecksAreOrderedByWhatTheRunIsFor(t *testing.T) {
	t.Parallel()
	cases := map[domain.HeavyCheckPriority]runstate.State{
		domain.HeavyCheckForLanding:       {IntegrationRetries: 1},
		domain.HeavyCheckForPreservedWork: {LiftedCommit: "0123456789abcdef0123456789abcdef01234567"},
		domain.HeavyCheckOrdinary:         {},
	}
	for want, state := range cases {
		if got := heavyCheckPriority(state); got != want {
			t.Fatalf("priority of %#v = %s, want %s", state, got, want)
		}
	}
	if got := heavyCheckPriority(runstate.State{RepairContinuations: []runstate.RepairContinuation{{}}}); got != domain.HeavyCheckForPreservedWork {
		t.Fatalf("a continued repair is ordered %s", got)
	}
}

// TestMeasureHeavyCheckPermits measures what places cost and what they save,
// and runs only when YOYODYNE_MEASURE_HEAVY_CHECKS names a directory to write
// its figures to: it starts CPU-heavy and Go-building work several at once, so
// it is a measurement to run by hand on a quiet machine and never a check.
func TestMeasureHeavyCheckPermits(t *testing.T) {
	out := os.Getenv("YOYODYNE_MEASURE_HEAVY_CHECKS")
	if out == "" {
		t.Skip("set YOYODYNE_MEASURE_HEAVY_CHECKS to a directory to measure heavy check places")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	type scenario struct {
		name    string
		command string
		runs    int
	}
	// A CPU-bound shell loop that ends by itself after a fixed amount of work,
	// for the non-Go case; one narrow Go package and a broader set for the Go
	// cases, each with its own build cache so the cache is not what is measured.
	burn := "i=0; while [ $i -lt 3000000 ]; do i=$((i+1)); done"
	goTest := func(packages string) string {
		return fmt.Sprintf("cache=\"$PWD/gocache\"; cd '%s' && GOFLAGS=-count=1 GOCACHE=\"$cache\" go test %s >/dev/null", repository, packages)
	}
	scenarios := []scenario{
		{name: "non-go cpu loop", command: burn, runs: 4},
		{name: "narrow go package", command: goTest("./internal/domain"), runs: 2},
		{name: "broad go packages", command: goTest("./internal/checks ./internal/config ./internal/domain"), runs: 2},
	}
	var report strings.Builder
	fmt.Fprintf(&report, "scenario\tplaces\twall\tsum of check time\tlongest wait\tsum of waits\n")
	for _, s := range scenarios {
		for _, places := range []int{0, 1} {
			heavy := map[string]int(nil)
			if places > 0 {
				heavy = map[string]int{s.command: places}
			}
			var runIDs []string
			for index := 0; index < s.runs; index++ {
				runIDs = append(runIDs, fmt.Sprintf("run-%d", index))
			}
			began := time.Now()
			runs := runHeavyStages(t, t.TempDir(), heavy, runIDs, []string{s.command}, 20*time.Minute)
			wall := time.Since(began)
			var checkTime, waits, longest time.Duration
			for runID, run := range runs {
				if run.err != nil || len(run.results) != 1 || !run.results[0].Passed {
					t.Fatalf("%s %s: %#v", s.name, runID, run)
				}
				checkTime += run.results[0].Elapsed()
				waits += run.waited
				if run.waited > longest {
					longest = run.waited
				}
			}
			label := "unlimited"
			if places > 0 {
				label = strconv.Itoa(places)
			}
			fmt.Fprintf(&report, "%s x%d\t%s\t%s\t%s\t%s\t%s\n", s.name, s.runs, label, wall.Round(10*time.Millisecond), checkTime.Round(10*time.Millisecond), longest.Round(10*time.Millisecond), waits.Round(10*time.Millisecond))
		}
	}
	// The place's own cost, with nobody else asking: ask, register, finish.
	store := mustHeavyStore(t, t.TempDir())
	const rounds = 50
	began := time.Now()
	for index := 0; index < rounds; index++ {
		grant, err := store.Acquire(context.Background(), runstate.HeavyCheckAsk{Product: "yoyodyne", RunID: "run-a", Check: "make race", Command: "make race", Limit: 1, Priority: domain.HeavyCheckOrdinary}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (execution.OSProcessRunner{}).Run(context.Background(), execution.Command{
			Name: "/bin/sh", Args: []string{"-c", "exit 0"}, Dir: t.TempDir(), Timeout: time.Minute,
			Gate: &execution.LaunchGate{Hold: grant.Hold(), Register: grant.Register},
		}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := grant.Finish("succeeded, exit code 0"); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Fprintf(&report, "lifecycle of one uncontended place (ask, gated launch of exit 0, give back), mean of %d\t%s\n", rounds, (time.Since(began) / rounds).Round(100*time.Microsecond))
	if err := os.WriteFile(filepath.Join(out, "heavy-check-measurements.tsv"), []byte(report.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + report.String())
}

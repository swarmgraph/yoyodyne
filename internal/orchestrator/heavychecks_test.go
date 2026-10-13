package orchestrator

import (
	"context"
	"errors"
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

// A declared limit whose record could not be opened refuses the heavy check,
// saying why, and leaves every other check alone.
func TestAHeavyCheckWhoseRecordCannotBeOpenedIsRefusedNotRunUnlimited(t *testing.T) {
	t.Parallel()
	permits := UnopenedHeavyChecks{Execution: config.Execution{HeavyChecks: map[string]int{"make race": 1}}, Err: errors.New("state root must be an absolute path")}
	if permit, err := permits.Acquire(context.Background(), checks.PermitAsk{RunID: "run-a", Command: "make vet"}, nil); err != nil || permit != nil {
		t.Fatalf("make vet: %v, %v; want it launched as usual", permit, err)
	}
	_, err := permits.Acquire(context.Background(), checks.PermitAsk{RunID: "run-a", Command: "make race RACE_PACKAGES=./..."}, nil)
	if err == nil || !strings.Contains(err.Error(), "make race is not started without its execution.heavy_checks limit") || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("make race: %v, want it refused with the reason", err)
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

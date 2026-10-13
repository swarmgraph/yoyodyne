//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// childCPU is the user and system time every process this test process has
// started and waited for has spent, its descendants included.
func childCPU(t *testing.T) time.Duration {
	t.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &usage); err != nil {
		t.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// TestMeasureHeavyCheckPermits measures what places cost and what they save,
// and runs only when YOYODYNE_MEASURE_HEAVY_CHECKS names a directory to write
// its figures to: it starts CPU-heavy and Go-building work several at once, so
// it is a measurement to run by hand and never a check. Run it on its own
// (-run TestMeasureHeavyCheckPermits), because the CPU figures are read from
// everything this test process waited for.
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
	// for the case that is not Go; one Go package and a broader set for the Go
	// cases, each with a build cache of its own so the cache is not what is
	// measured; and the narrow and broad cases are run two at once, which is the
	// concurrent case.
	burn := "i=0; while [ $i -lt 3000000 ]; do i=$((i+1)); done"
	goTest := func(packages string) string {
		return fmt.Sprintf("cache=\"$PWD/gocache\"; cd '%s' && GOFLAGS=-count=1 GOCACHE=\"$cache\" go test %s >/dev/null", repository, packages)
	}
	scenarios := []scenario{
		{name: "shell loop, not Go", command: burn, runs: 4},
		{name: "one Go package (narrow)", command: goTest("./internal/domain"), runs: 2},
		{name: "three Go packages (broad)", command: goTest("./internal/checks ./internal/config ./internal/domain"), runs: 2},
	}
	var report strings.Builder
	fmt.Fprintf(&report, "| case | places | wall | CPU (user+system) | sum of check time | longest wait | sum of waits |\n")
	fmt.Fprintf(&report, "| --- | --- | --- | --- | --- | --- | --- |\n")
	round := func(d time.Duration) string { return d.Round(10 * time.Millisecond).String() }
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
			cpuBefore := childCPU(t)
			began := time.Now()
			runs := runHeavyStages(t, t.TempDir(), heavy, runIDs, []string{s.command}, 20*time.Minute)
			wall := time.Since(began)
			cpu := childCPU(t) - cpuBefore
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
			label := "no limit"
			if places > 0 {
				label = strconv.Itoa(places)
			}
			fmt.Fprintf(&report, "| %s, %d at once | %s | %s | %s | %s | %s | %s |\n", s.name, s.runs, label, round(wall), round(cpu), round(checkTime), round(longest), round(waits))
		}
	}
	// What a place costs with nobody else asking: ask, a gated launch of a
	// command that does nothing, and giving the place back, against the same
	// gated launch with no place asked for.
	const rounds = 50
	launch := func(gate *execution.LaunchGate) {
		if _, err := (execution.OSProcessRunner{}).Run(context.Background(), execution.Command{
			Name: "/bin/sh", Args: []string{"-c", "exit 0"}, Dir: t.TempDir(), Timeout: time.Minute, Gate: gate,
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	plain := time.Now()
	for index := 0; index < rounds; index++ {
		launch(nil)
	}
	plainMean := time.Since(plain) / rounds
	store := mustHeavyStore(t, t.TempDir())
	permitted := time.Now()
	for index := 0; index < rounds; index++ {
		grant, err := store.Acquire(context.Background(), runstate.HeavyCheckAsk{Product: "yoyodyne", RunID: "run-a", Check: "make race", Command: "make race", Limit: 1, Priority: domain.HeavyCheckOrdinary}, nil)
		if err != nil {
			t.Fatal(err)
		}
		launch(&execution.LaunchGate{Hold: grant.Hold(), Register: grant.Register})
		if _, err := grant.Finish("succeeded, exit code 0"); err != nil {
			t.Fatal(err)
		}
	}
	permittedMean := time.Since(permitted) / rounds
	fmt.Fprintf(&report, "\nOne launch of `exit 0`, mean of %d: %s with no place asked for; %s asking for a place, launching behind its gate, and giving it back; so a place costs about %s.\n",
		rounds, plainMean.Round(100*time.Microsecond), permittedMean.Round(100*time.Microsecond), (permittedMean - plainMean).Round(100*time.Microsecond))
	if err := os.WriteFile(filepath.Join(out, "heavy-check-measurements.md"), []byte(report.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + report.String())
}

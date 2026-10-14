package cli

// The places among the machine's heavy checks, read and, for one kept for a
// check's leftover processes, given back by hand.
//
// A heavy check (execution.heavy_checks) gives its place back by itself once
// every process it started has gone. One whose processes cannot be confirmed
// gone keeps it, and every other run's check of the same name on the machine
// waits behind it; the attention line names it as the development manager's.
// Whether the leftover process is a hang is her judgement, and this is the
// command she settles it with: the release is written onto the permit with who
// gave it back and why. A process the harness launched for any other role is
// refused, because a developer freeing a place for itself is a run jumping the
// line every other run is waiting in.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type heavyChecksOutput struct {
	Record   *runstate.HeavyCheckRecord  `json:"record,omitempty"`
	Released *runstate.HeavyCheckPermit  `json:"released,omitempty"`
	Limits   *readmodel.HeavyCheckLimits `json:"limits,omitempty"`
	Error    string                      `json:"error,omitempty"`
}

func runHeavyChecks(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "release" {
		return releaseHeavyCheck(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "measurements" {
		return heavyCheckMeasurements(args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("heavy-checks", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintf(stderr, "unknown heavy-checks command %q\n\n", positional[0])
		printHeavyChecksUsage(stderr)
		return 2
	}
	store, err := heavyCheckStore(*configPath)
	if err != nil {
		return reportHeavyChecksError(stdout, stderr, *jsonOutput, err)
	}
	record, err := store.Record()
	if err != nil {
		return reportHeavyChecksError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, heavyChecksOutput{Record: &record})
	}
	now := time.Now()
	printed := false
	for _, permit := range record.Permits {
		if permit.State == runstate.HeavyCheckReleased {
			continue
		}
		printed = true
		fmt.Fprintf(stdout, "%s  %s  %s of run %s (%s)", permit.ID, permit.State, permit.Check, permit.RunID, permit.Product)
		switch {
		case permit.State == runstate.HeavyCheckWaiting:
			fmt.Fprintf(stdout, ", waiting %s", now.Sub(permit.AskedAt).Round(time.Second))
			if permit.AbandonedAt != nil {
				fmt.Fprint(stdout, ", with nobody waiting on it")
			}
		case permit.Stuck():
			fmt.Fprintf(stdout, ", keeping its place because %s", singleLine(permit.Problem))
		}
		fmt.Fprintln(stdout)
	}
	if !printed {
		fmt.Fprintln(stdout, "no heavy check is running or waiting on this machine")
	}
	return 0
}

func releaseHeavyCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("heavy-checks release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	by := flags.String("by", "", "who is giving the place back")
	reason := flags.String("reason", "", "why the place can be given back")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || strings.TrimSpace(*by) == "" || strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(stderr, "heavy-checks release takes one permit, and --by and --reason saying who gives the place back and why")
		printHeavyChecksUsage(stderr)
		return 2
	}
	if role, launched := execution.LaunchedForRole(nil); launched && role != domain.RoleDevelopmentManager {
		return reportHeavyChecksError(stdout, stderr, *jsonOutput, fmt.Errorf("yoyo heavy-checks release is refused from a process the harness launched for the %s: the development manager, or a person, gives a kept place back", role))
	}
	store, err := heavyCheckStore(*configPath)
	if err != nil {
		return reportHeavyChecksError(stdout, stderr, *jsonOutput, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	released, err := store.ReleaseByHand(ctx, positional[0], *by, *reason)
	if err != nil {
		return reportHeavyChecksError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, heavyChecksOutput{Released: &released})
	}
	fmt.Fprintf(stdout, "gave back the place %s held for %s of run %s, recorded as released by %s: %s\n",
		released.ID, released.Check, released.RunID, released.ReleasedBy, *reason)
	fmt.Fprintln(stdout, "the next run waiting for it is let start the next time it reads the record")
	return 0
}

// heavyCheckMeasurements prints what every measured launch of each heavy check
// says about its limit, and the limit it proposes. It proposes and changes
// nothing: a limit is applied through the reviewed configuration path.
func heavyCheckMeasurements(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("heavy-checks measurements", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "heavy-checks measurements takes no arguments")
		printHeavyChecksUsage(stderr)
		return 2
	}
	store, err := heavyCheckStore(*configPath)
	if err != nil {
		return reportHeavyChecksError(stdout, stderr, *jsonOutput, err)
	}
	measurements, err := store.Measurements()
	if err != nil {
		return reportHeavyChecksError(stdout, stderr, *jsonOutput, err)
	}
	limits := readmodel.ReadHeavyCheckLimits(measurements)
	if *jsonOutput {
		return writeJSON(stdout, stderr, heavyChecksOutput{Limits: &limits})
	}
	if len(limits.Checks) == 0 {
		fmt.Fprintln(stdout, "no heavy check launch has been measured on this machine yet")
		return 0
	}
	for _, check := range limits.Checks {
		fmt.Fprintf(stdout, "%s: %d launches, limit %d at once\n", check.Check, check.Launches, check.Limit)
		fmt.Fprintf(stdout, "  ended: %s\n", heavyCheckOutcomes(check.Outcomes))
		fmt.Fprintf(stdout, "  waited for a place: median %s, longest %s\n", check.WaitMedian.Round(time.Second), check.WaitLongest.Round(time.Second))
		if check.LoadMedian != nil {
			fmt.Fprintf(stdout, "  load as it launched: median %.1f\n", *check.LoadMedian)
		}
		if check.Outside > 0 {
			fmt.Fprintf(stdout, "  %d launched while a suite ran outside every place, such as a developer's own\n", check.Outside)
		}
		for _, level := range check.Levels {
			fmt.Fprintf(stdout, "  %d running at once: %d launches, %d finished, %d stopped by their time limit, ran median %s and longest %s, at most %.0f%% of the time limit",
				level.Running, level.Launches, level.Finished, level.TimedOut, level.RunMedian.Round(time.Second), level.RunLongest.Round(time.Second), level.LongestShare*100)
			switch {
			case !level.Judged:
				fmt.Fprint(stdout, " (too few to judge)")
			case level.Holds:
				fmt.Fprint(stdout, " (holds)")
			default:
				fmt.Fprint(stdout, " (does not hold)")
			}
			fmt.Fprintln(stdout)
		}
		if check.Proposed > 0 {
			fmt.Fprintf(stdout, "  proposed limit: %d, because %s\n", check.Proposed, check.Because)
		} else {
			fmt.Fprintf(stdout, "  no limit proposed: %s\n", check.Because)
		}
	}
	fmt.Fprintln(stdout, "the machine, by heavy checks of every name running as one launched:")
	for _, level := range limits.Machine {
		fmt.Fprintf(stdout, "  %d running: %d launches", level.Running, level.Launches)
		if level.LoadMedian != nil {
			fmt.Fprintf(stdout, ", load median %.1f", *level.LoadMedian)
		}
		if level.FreeMemoryMedian != nil {
			fmt.Fprintf(stdout, ", free memory median %.1f GB", float64(*level.FreeMemoryMedian)/(1<<30))
		}
		if level.Outside > 0 {
			fmt.Fprintf(stdout, ", %d beside a suite run outside every place (at most %d such processes)", level.Outside, level.OutsideMost)
		}
		fmt.Fprintln(stdout)
	}
	if limits.Unlisted > 0 {
		fmt.Fprintf(stdout, "the machine's processes could not be listed for %d launches, so whether a suite ran outside every place beside them is not known\n", limits.Unlisted)
	}
	fmt.Fprintln(stdout, "a proposed limit is applied through the reviewed configuration path; nothing here changes one")
	return 0
}

// heavyCheckOutcomes is how the measured launches ended, in words.
func heavyCheckOutcomes(outcomes map[runstate.HeavyCheckOutcome]int) string {
	order := []runstate.HeavyCheckOutcome{runstate.HeavyCheckPassed, runstate.HeavyCheckFailed, runstate.HeavyCheckTimedOut, runstate.HeavyCheckStalled, runstate.HeavyCheckStopped, ""}
	var parts []string
	for _, outcome := range order {
		if count := outcomes[outcome]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, outcome.Says()))
		}
	}
	if len(parts) == 0 {
		return "none written down yet"
	}
	return strings.Join(parts, ", ")
}

// heavyCheckStore opens the record of places under the state root this
// checkout's configuration agrees on.
func heavyCheckStore(configPath string) (*runstate.HeavyCheckStore, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, err
	}
	return runstate.NewHeavyCheckStore(stateRoot)
}

func reportHeavyChecksError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, heavyChecksOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printHeavyChecksUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo heavy-checks [options]
       yoyo heavy-checks measurements [options]
       yoyo heavy-checks release <permit> --by <name> --reason <why> [options]

Read the places among this machine's heavy checks (execution.heavy_checks):
every check running or waiting for one, across every project under the state
root, and every place kept because a check's processes could not be confirmed
gone.

measurements reads every measured launch of each heavy check — how long it
waited, how long it ran against its time limit, how it ended, the machine's load
and free memory as it launched, and any suite running outside every place, such
as a developer's own — and proposes the limit at which the check still finishes
inside its time limit. It never proposes more than the limit it was measured
under, and it changes nothing.

A kept place is given back by itself once the last of those processes goes.
release gives it back now, recorded on the permit with who and why, for the
development manager or a person who has decided the leftover process is a
hang. A process the harness launched for any other role is refused.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}

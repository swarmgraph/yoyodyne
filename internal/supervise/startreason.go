package supervise

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Why a supervisor started, said in `yoyo status` beside when.
//
// A machine restart that brings nothing back looks, from the next morning, the
// same as a product that was never started: the record says only that no
// supervisor is running. So each supervisor records why it started, and the
// status says it, which is what tells an operator after a restart that the
// launch agent brought the product back rather than somebody typing `yoyo
// start`.
//
// Each way a supervisor starts leaves a different trace, and StartReason reads
// them in this order:
//
//   - Taking up a deployed build re-executes the same process, so the record
//     the supervisor left behind names this process and an older build.
//   - `yoyo start` puts StartVariable in the environment of the supervisor it
//     detaches.
//   - launchd sets XPC_SERVICE_NAME to the job's label in every process it
//     starts (execution.LaunchdJobVariable). Where the record the last
//     supervisor left was written before the machine started, nothing has run
//     since the restart, so this is the start at login.
//   - Anything else is `yoyo start --foreground` typed or run directly.

// StartVariable carries, from `yoyo start` to the supervisor it detaches, why
// that supervisor was started. The supervisor reads it and takes it out of its
// own environment before it starts anything, so no part inherits it.
const StartVariable = "YOYODYNE_SUPERVISOR_STARTED_BY"

// StartedByYoyoStart is what `yoyo start` puts in StartVariable.
const StartedByYoyoStart = "`yoyo start` started it"

// StartFacts is what StartReason reads.
type StartFacts struct {
	// LaunchAgent is the label of the product's own launch agent.
	LaunchAgent string
	// PID and Build are this supervisor's.
	PID   int
	Build string
	// Previous is the record the last supervisor left, where Found says there is
	// one.
	Previous      runstate.Supervision
	PreviousFound bool
	// Environ is this process's environment.
	Environ []string
	// BootedAt is when the machine last started, and zero where it could not be
	// read.
	BootedAt time.Time
}

// StartReason is why a supervisor started, in words that follow "it started at
// <time>, because".
func StartReason(facts StartFacts) string {
	if facts.PreviousFound && facts.Previous.PID == facts.PID && facts.Previous.Build != facts.Build &&
		(facts.BootedAt.IsZero() || facts.Previous.StartedAt.After(facts.BootedAt)) {
		// The same process with another build is the supervisor re-executing
		// itself; a process number reused after a restart is told apart by the
		// earlier record predating the restart.
		return fmt.Sprintf("the supervisor before it re-executed itself to take up the deployed build %s", short(facts.Build))
	}
	job := ""
	for _, entry := range facts.Environ {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case StartVariable:
			if said := strings.TrimSpace(value); said != "" {
				return said
			}
		case execution.LaunchdJobVariable:
			job = strings.TrimSpace(value)
		}
	}
	if facts.LaunchAgent != "" && job == facts.LaunchAgent {
		if !facts.BootedAt.IsZero() && (!facts.PreviousFound || facts.Previous.ObservedAt.Before(facts.BootedAt)) {
			return fmt.Sprintf("its launch agent %s started it when you logged in, after the machine started at %s", job, facts.BootedAt.Local().Format("2006-01-02 15:04 MST"))
		}
		return fmt.Sprintf("its launch agent %s started it: the supervisor before it had exited, or `yoyo start` or `yoyo setup` asked launchd to", job)
	}
	return "it was started directly with `yoyo start --foreground`, not through its launch agent or `yoyo start`"
}

// ReadBootTime asks the operating system when the machine last started. Only
// macOS is asked, because launchd, whose start at login it tells apart, is
// macOS's.
func ReadBootTime(ctx context.Context, runner execution.ProcessRunner) (time.Time, error) {
	if runtime.GOOS != "darwin" {
		return time.Time{}, fmt.Errorf("when the machine started is not read on %s", runtime.GOOS)
	}
	result, err := runner.Run(ctx, execution.Command{Name: "/usr/sbin/sysctl", Args: []string{"-n", "kern.boottime"}, Timeout: 15 * time.Second}, nil)
	if err != nil {
		return time.Time{}, err
	}
	if result.Status != execution.ProcessSucceeded {
		return time.Time{}, fmt.Errorf("read when the machine started: %s: %s", result.Status, strings.TrimSpace(result.Stderr))
	}
	return parseBootTime(result.Stdout)
}

// parseBootTime reads sysctl's kern.boottime, which reads
// "{ sec = 1762934941, usec = 81268 } Wed Nov 12 00:09:01 2026".
func parseBootTime(printed string) (time.Time, error) {
	_, after, found := strings.Cut(printed, "sec =")
	if !found {
		return time.Time{}, fmt.Errorf("read when the machine started: %q names no seconds", strings.TrimSpace(printed))
	}
	digits, _, _ := strings.Cut(after, ",")
	seconds, err := strconv.ParseInt(strings.TrimSpace(digits), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, fmt.Errorf("read when the machine started: %q names no seconds", strings.TrimSpace(printed))
	}
	return time.Unix(seconds, 0), nil
}

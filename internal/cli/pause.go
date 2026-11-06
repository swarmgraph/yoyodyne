package cli

// Pausing everything, and lifting the pause.
//
// The harness spends the operator's money on their behalf, in processes they are
// not watching. Until this verb existed the only way to stop that in a hurry was
// to kill the processes doing it, which is the most expensive thing an operator
// can do: a killed run lands as a cancelled run with its item still claimed and
// the work it had done unfinished, and getting back from that means reconciling,
// reopening, and developing it again.
//
// So this is a switch rather than a signal. `yoyo pause` records a durable hold
// at the state root; every provider-call boundary reads it and parks or refuses
// instead of spending; `yoyo resume` lifts it and the parked work carries on
// where it was. Nothing is cancelled, nothing is cleaned up, and nothing loses
// its claim, its branch, or its session — which is the whole difference between
// this and a kill.
//
// The honest boundary is that a call already in flight is not interrupted. The
// hold is read before a provider is invoked, so a generation that is already
// streaming finishes and is paid for; the pause takes effect at the next
// boundary, which for a developer attempt can be minutes away.

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type pauseOutput struct {
	Hold *runstate.OperatorHold `json:"hold,omitempty"`
	// Held reports what the command found rather than what it did: pausing what is
	// already paused and resuming what is not held are both no-ops, and a caller
	// reading JSON has to be able to tell those apart from the acts themselves.
	Held  bool   `json:"held"`
	Error string `json:"error,omitempty"`
}

func pauseHarness(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pause", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	team := flags.Bool("team", false, "set the team pause on the shared remote instead of pausing this machine (team mode only)")
	clear := flags.Bool("clear", false, "with --team, clear the team pause")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "pause does not accept positional arguments: it holds everything, not one item")
		printPauseUsage(stderr)
		return 2
	}
	if *clear && !*team {
		fmt.Fprintln(stderr, "--clear goes with --team and clears the team pause; `yoyo resume` lifts this machine's own pause")
		printPauseUsage(stderr)
		return 2
	}
	// Read before anything else is: the hold is a person's decision, and a
	// shell an agent opened is not a person.
	if err := refusedToAgentProcess("yoyo pause", "a person pauses the harness"); err != nil {
		return reportHoldError(stdout, stderr, *jsonOutput, err)
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportHoldError(stdout, stderr, *jsonOutput, err)
	}
	if *team {
		return pauseTeam(parts, *clear, *jsonOutput, stdout, stderr)
	}
	_, already, err := parts.holds.Held()
	if err != nil {
		return reportHoldError(stdout, stderr, *jsonOutput, err)
	}
	hold, err := parts.holds.Hold(time.Now().UTC())
	if err != nil {
		return reportHoldError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, pauseOutput{Hold: &hold, Held: true})
	}
	if already {
		fmt.Fprintf(stdout, "harness activity was already paused, since %s\n", hold.HeldAt.Format(time.RFC3339))
	} else {
		fmt.Fprintf(stdout, "paused all harness activity at %s\n", hold.HeldAt.Format(time.RFC3339))
	}
	fmt.Fprintln(stdout, "every developer attempt, reviewer invocation, and conversation turn parks or refuses at its next provider call instead of spending")
	fmt.Fprintln(stdout, "a provider call already in flight is not interrupted: it finishes and is charged for, and the pause takes effect at the boundary after it")
	fmt.Fprintln(stdout, "runs in flight keep their claim, branch, worktree, and developer session; nothing was cancelled and nothing needs reconciling")
	fmt.Fprintln(stdout, "`yoyo resume` lifts this, and a process still parked on it carries on within seconds")
	return 0
}

// resumeHarness lifts the hold. It is `yoyo resume` with nothing named, which is
// the same verb the operator uses to release one run's wait on a provider: both
// mean "stop waiting and carry on", and what differs is only whose decision is
// being withdrawn — the provider's refusal, or the operator's own.
func resumeHarness(stdout, stderr io.Writer, jsonOutput bool, holds *runstate.OperatorHoldStore) int {
	lifted, held, err := holds.Release()
	if err != nil {
		return reportHoldError(stdout, stderr, jsonOutput, err)
	}
	if jsonOutput {
		output := pauseOutput{Held: false}
		if held {
			output.Hold = &lifted
		}
		return writeJSON(stdout, stderr, output)
	}
	if !held {
		fmt.Fprintln(stdout, "harness activity was not paused; nothing was holding it and nothing changed")
		return 0
	}
	fmt.Fprintf(stdout, "resumed all harness activity, held since %s\n", lifted.HeldAt.Format(time.RFC3339))
	fmt.Fprintln(stdout, "a process still parked on the hold carries on within seconds; nothing needs restarting")
	fmt.Fprintln(stdout, "a run whose process has already exited is continued by `yoyo run <beads-id>`, from exactly where it parked")
	return 0
}

func reportHoldError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, pauseOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printPauseUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo pause [options]

Pause everything the harness would spend on a provider — to conserve tokens, or
for any other reason of your own. It is one durable switch over the whole
machine rather than one item: every developer attempt, reviewer invocation, and
conversation turn reads it before it spends, and parks or refuses instead.

A run that parks does so durably. It keeps its claim, its branch, its worktree,
and its developer session, exactly as a run waiting out a provider usage limit
does, and `+"`yoyo resume`"+` is what lifts it: a process still parked carries
on within seconds, and one that has since exited continues from
`+"`yoyo run`"+`. Nothing is cancelled, so nothing has to be reconciled
afterwards — which is what makes this the verb to reach for instead of killing
processes.

A provider call already in flight is not interrupted. The hold is read before a
call, so a generation already streaming finishes and is charged for; the pause
takes effect at the next boundary.

This pause is this machine's alone. It is never shared with another machine
working the same repository, so one collaborator cannot stop another's machine.

With --team, on a project whose configuration turns team mode on
(team.enabled: true), it sets the team pause instead: the reference
refs/yoyodyne/pause on the shared remote. While it is set, no factory working
the repository starts new work the harness chose for itself; runs in progress
finish, repair rounds and waiting pull requests carry on, and an item an
operator names on their own machine is still started. Every factory in team mode
reads it before it claims an item, and `+"`yoyo status`"+` says who set it.
`+"`yoyo pause --team --clear`"+` clears it. On a project with team mode off
there is no team pause, and --team is refused.

This is a person's verb. A process the harness launched for a role -- an
agent's shell, marked by YOYODYNE_AGENT_ROLE -- is refused it and told so.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --team            set the team pause on the shared remote (team mode only)
  --clear           with --team, clear the team pause
  --json            emit machine-readable JSON`)
}

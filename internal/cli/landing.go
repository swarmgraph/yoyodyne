package cli

// Holding landing on one branch, from a person's own script.
//
// A project may need the harness to land nothing on a branch for a while: a
// release being cut from it, a migration that must not see new commits.
// `yoyo landing hold` records that for one target branch, for a stated time,
// and `yoyo landing release` ends it sooner. The hold is not `yoyo pause`,
// which stops everything the harness spends on a provider, and it is not the
// intake hold, which stops the harness choosing new work. Checks and reviews
// carry on, and a change ready to land waits with its evidence kept.
//
// The duration is required and never more than runstate.MaxLandingHold, so a
// script that dies before releasing cannot stop landing for good. The hold is a
// file under this machine's state root and does not travel to another machine.

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const landingHoldLayout = "2006-01-02 15:04 MST"

type landingOutput struct {
	// Hold is the hold this command placed, or the one it released.
	Hold *runstate.LandingHold `json:"hold,omitempty"`
	// Held reports whether landing on the branch is held now, and Released what
	// a release did: releasing what was never held changes nothing, and a
	// caller reading JSON has to be able to tell that from the act itself.
	Held     bool   `json:"held"`
	Released bool   `json:"released,omitempty"`
	Error    string `json:"error,omitempty"`
}

func runLanding(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "landing needs a subcommand: hold or release")
		printLandingUsage(stderr)
		return 2
	}
	switch args[0] {
	case "hold":
		return holdLanding(args[1:], stdout, stderr)
	case "release":
		return releaseLanding(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printLandingUsage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "landing has no subcommand %q: use hold or release\n", args[0])
	printLandingUsage(stderr)
	return 2
}

// landingHoldStore is the store of the product the configuration names, under
// its state root on this machine.
func landingHoldStore(configPath string) (*runstate.LandingHoldStore, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, err
	}
	return runstate.NewLandingHoldStore(stateRoot, resolved.Config.Product.ID)
}

func holdLanding(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("landing hold", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	forText := flags.String("for", "", "how long the hold lasts, such as 30m or 2h; required, at most 4h")
	reason := flags.String("reason", "", "why landing is held; required")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "landing hold takes one argument, the target branch to hold landing on")
		printLandingUsage(stderr)
		return 2
	}
	target := positional[0]
	// A missing --for is a zero duration, which the store's own check refuses in
	// its own words; only text that is not a duration is refused here.
	var duration time.Duration
	if *forText != "" {
		parsed, err := time.ParseDuration(*forText)
		if err != nil {
			return reportLandingError(stdout, stderr, *jsonOutput, fmt.Errorf("--for takes a duration such as 30m or 2h, and %q is not one", *forText))
		}
		duration = parsed
	}
	// A person's script holds landing, never a shell an agent opened.
	if err := refusedToAgentProcess("yoyo landing hold", "a person or the project's own script holds landing"); err != nil {
		return reportLandingError(stdout, stderr, *jsonOutput, err)
	}
	store, err := landingHoldStore(*configPath)
	if err != nil {
		return reportLandingError(stdout, stderr, *jsonOutput, err)
	}
	hold, err := store.Place(target, *reason, duration, time.Now())
	if err != nil {
		return reportLandingError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, landingOutput{Hold: &hold, Held: true})
	}
	until := hold.Until.Local().Format(landingHoldLayout)
	fmt.Fprintf(stdout, "landing on %s is held until %s: %s\n", hold.TargetBranch, until, hold.Reason)
	fmt.Fprintf(stdout, "checks and reviews carry on; a change ready to land on %s waits with its evidence kept\n", hold.TargetBranch)
	fmt.Fprintf(stdout, "this does not pause the harness (`yoyo pause`) or stop new work (the intake hold); it holds landing on %s on this machine only\n", hold.TargetBranch)
	fmt.Fprintf(stdout, "`yoyo landing release %s` ends it sooner, and it ends on its own at %s if nobody does\n", hold.TargetBranch, until)
	return 0
}

func releaseLanding(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("landing release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "landing release takes one argument, the target branch to let the harness land on again")
		printLandingUsage(stderr)
		return 2
	}
	if err := refusedToAgentProcess("yoyo landing release", "a person or the project's own script releases a landing hold"); err != nil {
		return reportLandingError(stdout, stderr, *jsonOutput, err)
	}
	store, err := landingHoldStore(*configPath)
	if err != nil {
		return reportLandingError(stdout, stderr, *jsonOutput, err)
	}
	released, found, err := store.Release(positional[0])
	if err != nil {
		return reportLandingError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		output := landingOutput{Held: false, Released: found}
		if found {
			output.Hold = &released
		}
		return writeJSON(stdout, stderr, output)
	}
	if !found {
		fmt.Fprintf(stdout, "landing on %s was not held; nothing changed\n", positional[0])
		return 0
	}
	fmt.Fprintf(stdout, "released the hold on landing on %s, set at %s: the harness may land on it again\n",
		released.TargetBranch, released.SetAt.Local().Format(landingHoldLayout))
	fmt.Fprintf(stdout, "a change that was waiting to land on %s lands at the next pass, with the evidence it kept\n", released.TargetBranch)
	return 0
}

func reportLandingError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, landingOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printLandingUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo landing hold <target> --for <duration> --reason <text> [options]
       yoyo landing release <target> [options]

Stop the harness landing anything on one target branch for a while, and let it
land there again.

hold records the hold. --for says how long it lasts, such as 30m or 2h, and is
required; it may not be more than 4 hours, so a script that stops before
releasing cannot stop landing for good. --reason says why, and the status line
shows it. release ends a hold sooner, and releasing what is not held is not an
error.

While a hold stands, checks and reviews carry on, and a change ready to land on
that branch waits with its evidence kept. This is not `+"`yoyo pause`"+`, which
stops everything the harness spends on a provider, and it is not the intake
hold, which stops the harness choosing new work. It holds landing on one branch
on this machine only.

This is a person's verb, or your project's own script's. A process the harness
launched for a role -- an agent's shell, marked by YOYODYNE_AGENT_ROLE -- is
refused it and told so.

Options:
  --for <duration>  how long the hold lasts (hold only; required, at most 4h)
  --reason <text>   why landing is held (hold only; required)
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}

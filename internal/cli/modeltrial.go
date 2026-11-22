package cli

// yoyo model-trial: the trial of starting developer runs on the cheaper model
// (docs/designs/reducing-what-the-factory-costs.md, section 4). It shows the
// comparison the development manager decides on, read off the runs that took
// part, and records a defect found after a run's change landed, which stops
// the trial at once when that run started on the cheaper model.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func runModelTrial(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "defect" {
		return recordTrialDefect(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "show" {
		args = args[1:]
	}
	flags := flag.NewFlagSet("model-trial", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) > 0 {
		fmt.Fprintf(stderr, "model-trial does not take %q\n", positional[0])
		printModelTrialUsage(stderr)
		return 2
	}
	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "model-trial failed: %v\n", err)
		return 1
	}
	setting := parts.config.Execution.DeveloperCheaperFirst
	figures, err := parts.store.CheaperFirstTrialFigures(setting.TrialTarget())
	if err != nil {
		fmt.Fprintf(stderr, "model-trial failed: %v\n", err)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, figures)
	}
	printTrialFigures(stdout, figures, setting.Enabled)
	return 0
}

func printTrialFigures(writer io.Writer, figures runstate.CheaperFirstTrialFigures, enabled bool) {
	state := "off"
	if enabled {
		state = "on"
	}
	fmt.Fprintf(writer, "Developers starting on the cheaper model: %s. %d of at least %d trial runs so far.\n", state, figures.Runs, figures.Target)
	if figures.StoppedBy != "" {
		fmt.Fprintf(writer, "The trial is stopped: run %s started on the cheaper model and its change had a defect after landing (%s). Every eligible run since starts on the developer's configured model.\n", figures.StoppedBy, figures.StopNote)
	} else if figures.Runs >= figures.Target {
		fmt.Fprintln(writer, "The trial has reached its run count; the comparison below is due for a decision.")
	}
	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "%-26s %12s %12s\n", "", "cheaper", "stronger")
	row := func(label string, cheaper, stronger string) {
		fmt.Fprintf(writer, "%-26s %12s %12s\n", label, cheaper, stronger)
	}
	c, s := figures.Cheaper, figures.Stronger
	row("runs", fmt.Sprint(c.Runs), fmt.Sprint(s.Runs))
	row("changes merged", fmt.Sprint(c.Merged), fmt.Sprint(s.Merged))
	row("cost per merged change", trialDollars(c), trialDollars(s))
	row("first review approved", trialShare(c.FirstReviewApproved, c.Reviewed), trialShare(s.FirstReviewApproved, s.Reviewed))
	row("repair rounds", fmt.Sprint(c.RepairRounds), fmt.Sprint(s.RepairRounds))
	row("moved to stronger", fmt.Sprint(c.Moves), "-")
	row("defects after landing", fmt.Sprint(c.DefectsAfterLanding), fmt.Sprint(s.DefectsAfterLanding))
	if c.Unpriced+s.Unpriced > 0 {
		fmt.Fprintf(writer, "\n%d run(s) could not be priced and are left out of the cost.\n", c.Unpriced+s.Unpriced)
	}
	for _, defect := range figures.Defects {
		fmt.Fprintf(writer, "Defect after landing: run %s (%s, started %s): %s\n", defect.RunID, defect.WorkItemID, defect.Arm, defect.Note)
	}
}

func trialDollars(arm runstate.CheaperFirstArmFigures) string {
	if arm.Merged == 0 {
		return "-"
	}
	return fmt.Sprintf("$%.2f", arm.CostPerMergedUSD)
}

func trialShare(part, whole int) string {
	if whole == 0 {
		return "-"
	}
	return fmt.Sprintf("%d of %d", part, whole)
}

func recordTrialDefect(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("model-trial defect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	note := flags.String("note", "", "what the defect is and where it was found")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || strings.TrimSpace(*note) == "" {
		fmt.Fprintln(stderr, "model-trial defect takes one run id and a --note saying what the defect is")
		printModelTrialUsage(stderr)
		return 2
	}
	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "model-trial defect failed: %v\n", err)
		return 1
	}
	state, err := parts.store.Update(ctx, positional[0], runstate.RecordDefectAfterLanding{Note: *note, At: time.Now().UTC()})
	if err != nil {
		fmt.Fprintf(stderr, "model-trial defect failed: %v\n", err)
		return 1
	}
	if state.CheaperFirst != nil && state.CheaperFirst.Arm == runstate.CheaperFirstCheaper {
		fmt.Fprintf(stdout, "Recorded the defect on run %s. It started on the cheaper model, so the trial is stopped: every eligible run from now on starts on the developer's configured model.\n", state.RunID)
		return 0
	}
	fmt.Fprintf(stdout, "Recorded the defect on run %s. It started on the stronger model, so the trial goes on.\n", state.RunID)
	return 0
}

func printModelTrialUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo model-trial [show] [options]
       yoyo model-trial defect <run-id> --note <text> [options]

Shows the trial of starting developer runs on the cheaper model: with
execution.developer_cheaper_first on, eligible runs alternate between starting
on the cheaper model and starting on the stronger one, and a run that started
cheaper moves once to the stronger after its first failed review or failed
checks. For each half it shows the runs, the changes merged, the cost per
merged change, how often the first review approved, the repair rounds, how
many runs moved to the stronger model, and the defects found after landing.

"defect" records a defect traced to a run's change after it landed. On a run
that started on the cheaper model it stops the trial at once: every eligible
run after it starts on the developer's configured model.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON (show only)
  --note <text>     what the defect is and where it was found (defect only)`)
}

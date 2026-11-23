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

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func runModelTrial(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "defect" {
		return recordTrialDefect(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "reviews" {
		return showReviewComparison(args[1:], stdout, stderr)
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
	// The figures are the read model's, the same reading the development
	// manager's passes open with and the "model-trial" query answers.
	trial := readmodel.ReadModelTrial(standingSources(*configPath))
	if *jsonOutput {
		return writeJSON(stdout, stderr, trial)
	}
	fmt.Fprintln(stdout, trial.Render())
	if trial.Problem != "" {
		return 1
	}
	return 0
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

// showReviewComparison lists each document publication reviewed both on the
// stronger model, which decided, and on the cheaper one, which did not
// (execution.reviewer_models.compare_documents).
func showReviewComparison(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("model-trial reviews", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) > 0 {
		fmt.Fprintf(stderr, "model-trial reviews does not take %q\n", positional[0])
		printModelTrialUsage(stderr)
		return 2
	}
	listing := readmodel.ReadReviewComparison(standingSources(*configPath))
	if *jsonOutput {
		return writeJSON(stdout, stderr, listing)
	}
	fmt.Fprintln(stdout, listing.Render())
	if listing.Problem != "" {
		return 1
	}
	return 0
}

func printModelTrialUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo model-trial [show] [options]
       yoyo model-trial defect <run-id> --note <text> [options]
       yoyo model-trial reviews [options]

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

"reviews" lists each document publication whose review was made a second
time on the cheaper model, with execution.reviewer_models.compare_documents
on: both verdicts, whether they agree, how many agree, and how many times the
cheaper reviewer approved what the stronger refused. The stronger reviewer's
verdict alone decides whether a publication lands.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON (show and reviews only)
  --note <text>     what the defect is and where it was found (defect only)`)
}

// landedDefectRecorder records a defect after landing on a run through the run
// store's narrow update, for the development manager's block that records one.
// The command above and the block write the same update and are refused the
// same way.
type landedDefectRecorder struct{ store *runstate.Store }

func (r landedDefectRecorder) RecordDefectAfterLanding(ctx context.Context, runID, note string, at time.Time) (runstate.State, error) {
	if r.store == nil {
		return runstate.State{}, fmt.Errorf("no run store is wired to record a defect on run %s", runID)
	}
	return r.store.Update(ctx, runID, runstate.RecordDefectAfterLanding{Note: note, At: at.UTC()})
}

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ForgeCheckReadLine is the line `yoyo forge-check` writes on standard error
// once it has read the forge and acted on what it read, before it hosts the
// repair rounds and updates it started. The supervisor's forge check reads it
// to know the check is done reading, so a check that goes on to host a run
// does not hold back the next one (internal/forgecheck).
const ForgeCheckReadLine = "forge check: read and acted on; hosting what it started"

type forgeCheckOutput struct {
	orchestrator.ForgeCheck
	// Updates and FeedbackRepairs are the runs this check put back to bring a
	// pull request up to date or to answer feedback on it, and every other such
	// run nothing was hosting, each hosted by this process to its end.
	Updates         []orchestrator.UpdateContinuation   `json:"updates"`
	FeedbackRepairs []orchestrator.FeedbackContinuation `json:"feedback_repairs"`
	// Forge is what the check asked the forge in its batched state query and how
	// long the forge took.
	Forge *orchestrator.ForgeQuestions `json:"forge,omitempty"`
	// KeptAnswers is how many of the forge's answers are kept for the next check
	// to ask about conditionally, and KeptAnswersProblem why they could not be
	// read or written, where they could not; either costs full reads and
	// nothing else.
	KeptAnswers        int    `json:"kept_answers"`
	KeptAnswersProblem string `json:"kept_answers_problem,omitempty"`
	Error              string `json:"error,omitempty"`
}

// checkForge is one check of the forge: the pull requests waiting for a person
// to merge them, the reviews and comments on them, and how far the target
// branch has moved past each (orchestrator.CheckForge). The supervisor runs it
// on a schedule of its own, every execution.forge_check_interval, apart from
// the maintenance pass.
func checkForge(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("forge-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "forge-check does not accept positional arguments")
		printForgeCheckUsage(stderr)
		return 2
	}
	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportForgeCheck(stdout, stderr, *jsonOutput, forgeCheckOutput{}, err)
	}
	ctx = beads.OnBehalfOf(ctx, "yoyo forge-check, which reads the pull requests waiting for a person to merge them")
	answers, afresh, err := runstate.OpenForgeResponses(parts.stateRoot, parts.config.Product.ID, time.Now)
	if err != nil {
		return reportForgeCheck(stdout, stderr, *jsonOutput, forgeCheckOutput{}, err)
	}
	reconciler := forgeCheckerFrom(parts, answers)
	output := forgeCheckOutput{KeptAnswersProblem: afresh}
	output.ForgeCheck, err = reconciler.CheckForge(ctx)
	if saveErr := answers.Save(); saveErr != nil {
		output.KeptAnswersProblem = saveErr.Error()
	}
	output.KeptAnswers = answers.Len()
	output.Forge = reconciler.Forge
	if !*jsonOutput {
		printPersonMerges(stdout, stderr, output.PersonMerges)
		printTeamMerges(stdout, stderr, output.TeamMerges)
		printFeedback(stdout, stderr, output.Feedback)
		fmt.Fprintln(stdout, describeForgeCheck(output))
	}
	// Said once the reading is over and before anything is hosted: see
	// ForgeCheckReadLine.
	fmt.Fprintln(stderr, ForgeCheckReadLine)

	// The runs this check put back are hosted here, as the sweep hosts the ones
	// it puts back, side by side so neither waits behind the other.
	updater := reconciler
	updater.Continue = continueUpdateFrom(parts, stderr)
	repairer := reconciler
	repairer.Continue = continueFeedbackRepairFrom(parts, stderr)
	var updateErr, repairErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		output.Updates, updateErr = updater.ContinueUpdates(ctx)
	}()
	output.FeedbackRepairs, repairErr = repairer.ContinueFeedbackRepairs(ctx)
	<-done
	err = errors.Join(err, updateErr, repairErr)
	if !*jsonOutput {
		printUpdates(stdout, stderr, output.Updates)
		printFeedbackRepairs(stdout, stderr, output.FeedbackRepairs)
	}
	return reportForgeCheck(stdout, stderr, *jsonOutput, output, err)
}

// forgeCheckerFrom is the reconciler a forge check runs with: the sweep's,
// with the forge asked conditionally through the kept answers, and hosting the
// runs it puts back.
func forgeCheckerFrom(parts components, answers publish.ResponseCache) orchestrator.Reconciler {
	reconciler := reconcilerFrom(parts)
	forge := publish.GitHub{
		Runner:       parts.runner,
		Dir:          parts.repository,
		Remote:       parts.config.Execution.Remote,
		PushRemote:   parts.config.Execution.PushRemote,
		RedactValues: parts.redactValues,
		Responses:    answers,
	}
	reconciler.Publisher = forge
	reconciler.Checks = forge
	reconciler.Closers = forge
	reconciler.Feedback = forge
	reconciler.HostsRuns = true
	reconciler.RetryAtNextCheck = true
	reconciler.Forge = &orchestrator.ForgeQuestions{}
	return reconciler
}

// describeForgeCheck is the check's last line: how many pull requests it read,
// and how many it could not read or finish acting on.
func describeForgeCheck(output forgeCheckOutput) string {
	said := fmt.Sprintf("forge check: %d pull request(s) waiting for a person to merge them, %d read for feedback", len(output.PersonMerges), len(output.Feedback))
	if len(output.TeamMerges) > 0 {
		said += fmt.Sprintf(", %d landed change(s) waiting for the team to merge them", len(output.TeamMerges))
	}
	if failures := output.Failures(); failures > 0 {
		said += fmt.Sprintf("; %d could not be read or acted on, and the next check reads them again", failures)
	}
	return said
}

// printTeamMerges says what the check found of each change waiting for the
// team to merge it into their branch.
func printTeamMerges(stdout, stderr io.Writer, merges []orchestrator.TeamMergeSettlement) {
	for _, merge := range merges {
		switch {
		case merge.Failure != "":
			fmt.Fprintf(stderr, "%s waiting for the team's merge not settled: %s\n", merge.WorkItemID, merge.Failure)
		case merge.Kept != "":
			fmt.Fprintf(stdout, "%s left as it stands: %s\n", merge.WorkItemID, merge.Kept)
		case merge.Settled:
			fmt.Fprintf(stdout, "%s: the team merged %s into %s (read at %s); the item is closed\n", merge.WorkItemID, merge.LandingCommit, merge.UpstreamBranch, merge.FoundOn)
		default:
			fmt.Fprintf(stdout, "%s landed on %s as %s and is waiting for the team to merge it into %s\n", merge.WorkItemID, merge.TargetBranch, merge.LandingCommit, merge.UpstreamBranch)
		}
	}
}

func reportForgeCheck(stdout, stderr io.Writer, jsonOutput bool, output forgeCheckOutput, err error) int {
	code := 0
	if err != nil || output.Failures() > 0 {
		code = 1
	}
	if jsonOutput {
		if err != nil {
			output.Error = err.Error()
		}
		if output.PersonMerges == nil {
			output.PersonMerges = []orchestrator.PersonMergeSettlement{}
		}
		if output.TeamMerges == nil {
			output.TeamMerges = []orchestrator.TeamMergeSettlement{}
		}
		if output.Feedback == nil {
			output.Feedback = []orchestrator.FeedbackSettlement{}
		}
		if output.Updates == nil {
			output.Updates = []orchestrator.UpdateContinuation{}
		}
		if output.FeedbackRepairs == nil {
			output.FeedbackRepairs = []orchestrator.FeedbackContinuation{}
		}
		if written := writeJSON(stdout, stderr, output); written != 0 {
			return written
		}
		return code
	}
	if output.KeptAnswersProblem != "" {
		fmt.Fprintln(stderr, output.KeptAnswersProblem)
	}
	if err != nil {
		fmt.Fprintf(stderr, "forge check: %v\n", err)
	}
	return code
}

func printForgeCheckUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo forge-check [options]

Checks the forge once, which the supervisor does on a schedule of its own,
every execution.forge_check_interval (five minutes by default), apart from the
maintenance pass. It reads every pull request waiting for a person to merge it:
one merged closes its item and is followed locally, one closed without merging
gives its item back to the Lead Product Manager, and one the target branch has
moved past is brought up to date. It then reads the reviews and comments left
on the ones still open: a requested change starts a repair round on the same
run, and an ordinary comment goes on the development manager's list.

Reads the forge can answer conditionally are asked that way, carrying the
answer kept from the last check, so an unchanged answer is not fetched again in
full. A check that cannot reach the forge changes nothing, and the next check
tries again; a change it read and could not finish acting on, such as closing
an item whose pull request merged, is read and acted on again by the next
check, and a write repeated after a timeout in which it had gone through adds no
second note.

The repair rounds and updates it starts are hosted by this process to their
end, after the check's own report.

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --json             emit machine-readable JSON`)
}

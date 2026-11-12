package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// itemValues is the repeated --item flag.
type itemValues []string

func (i *itemValues) String() string { return strings.Join(*i, ", ") }

func (i *itemValues) Set(value string) error {
	*i = append(*i, value)
	return nil
}

// runPullRequest is `yoyo pull-request`: how an outside tool that opens its own
// pull request into the team's branch tells the harness which waiting items the
// pull request carries (orchestrator.Reconciler.TrackTeamPullRequest).
func runPullRequest(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPullRequestUsage(stderr)
		return 2
	}
	switch args[0] {
	case "track":
		return trackPullRequest(ctx, args[1:], stdout, stderr)
	case "untrack":
		return untrackPullRequest(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown pull-request command %q\n", args[0])
		printPullRequestUsage(stderr)
		return 2
	}
}

// pullRequestNumber is the one positional argument both commands take: a
// positive pull request number.
func pullRequestNumber(positional []string, stderr io.Writer) (int, bool) {
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "name the pull request by its number, as `yoyo pull-request track <number> --item <id>`")
		printPullRequestUsage(stderr)
		return 0, false
	}
	number, err := strconv.Atoi(positional[0])
	if err != nil || number <= 0 {
		fmt.Fprintf(stderr, "%q is not a pull request number; give a positive whole number\n", positional[0])
		printPullRequestUsage(stderr)
		return 0, false
	}
	return number, true
}

func trackPullRequest(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pull-request track", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	unverified := flags.Bool("unverified", false, "record items whose landing commit is not among the pull request's commits")
	var items itemValues
	flags.Var(&items, "item", "a work item the pull request carries; repeat for each")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	number, ok := pullRequestNumber(positional, stderr)
	if !ok {
		return 2
	}
	if len(items) == 0 {
		fmt.Fprintln(stderr, "name each item the pull request carries with --item <id>")
		printPullRequestUsage(stderr)
		return 2
	}
	ctx, reconciler, request, err := teamPullRequestReconciler(ctx, *configPath)
	if err != nil {
		return reportTeamPullRequest(stdout, stderr, *jsonOutput, orchestrator.TeamPullRequestTrack{}, err, nil)
	}
	request.Number = number
	request.Items = items
	request.Unverified = *unverified
	track, err := reconciler.TrackTeamPullRequest(ctx, request)
	return reportTeamPullRequest(stdout, stderr, *jsonOutput, track, err, printTrackedPullRequest)
}

func untrackPullRequest(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pull-request untrack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	number, ok := pullRequestNumber(positional, stderr)
	if !ok {
		return 2
	}
	ctx, reconciler, _, err := teamPullRequestReconciler(ctx, *configPath)
	if err != nil {
		return reportTeamPullRequest(stdout, stderr, *jsonOutput, orchestrator.TeamPullRequestTrack{}, err, nil)
	}
	track, err := reconciler.UntrackTeamPullRequest(ctx, number)
	return reportTeamPullRequest(stdout, stderr, *jsonOutput, track, err, printUntrackedPullRequest)
}

// teamPullRequestReconciler is the reconciler these commands run with, able to
// read a pull request, the context its tracker calls are made on behalf of, and
// the project's target and team branches. The target branch is the one runs land
// on: the branch the primary checkout is on.
func teamPullRequestReconciler(ctx context.Context, configPath string) (context.Context, orchestrator.Reconciler, orchestrator.TeamPullRequestTracking, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return ctx, orchestrator.Reconciler{}, orchestrator.TeamPullRequestTracking{}, err
	}
	ctx = beads.OnBehalfOf(ctx, "yoyo pull-request, which records the pull request into the team's branch that carries waiting items")
	target, err := parts.worktrees.CurrentBranch(ctx)
	if err != nil {
		return ctx, orchestrator.Reconciler{}, orchestrator.TeamPullRequestTracking{}, err
	}
	reconciler := reconcilerFrom(parts)
	reconciler.TeamPullRequests = publish.GitHub{
		Runner:       parts.runner,
		Dir:          parts.repository,
		Remote:       parts.config.Execution.Remote,
		PushRemote:   parts.config.Execution.PushRemote,
		RedactValues: parts.redactValues,
	}
	return ctx, reconciler, orchestrator.TeamPullRequestTracking{
		TargetBranch:   target,
		UpstreamBranch: parts.config.Execution.UpstreamBranch,
	}, nil
}

// reportTeamPullRequest writes what a command did and gives its exit code: 1 for a
// refusal or any item that could not be recorded.
func reportTeamPullRequest(stdout, stderr io.Writer, jsonOutput bool, track orchestrator.TeamPullRequestTrack, err error, print func(stdout, stderr io.Writer, track orchestrator.TeamPullRequestTrack)) int {
	code := 0
	if err != nil {
		code = 1
	}
	for _, item := range track.Items {
		if item.Failure != "" {
			code = 1
		}
	}
	if jsonOutput {
		if err != nil {
			if written := writeJSON(stdout, stderr, map[string]string{"error": err.Error()}); written != 0 {
				return written
			}
			return code
		}
		if track.Items == nil {
			track.Items = []orchestrator.TrackedItem{}
		}
		if written := writeJSON(stdout, stderr, track); written != 0 {
			return written
		}
		return code
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	if print != nil {
		print(stdout, stderr, track)
	}
	return code
}

// printTrackedPullRequest says what recording a pull request did to each item.
func printTrackedPullRequest(stdout, stderr io.Writer, track orchestrator.TeamPullRequestTrack) {
	for _, item := range track.Items {
		switch {
		case item.Failure != "":
			fmt.Fprintf(stderr, "%s: not recorded: %s\n", item.WorkItemID, item.Failure)
		case item.Unchanged:
			fmt.Fprintf(stdout, "%s: already recorded as carried by pull request #%d\n", item.WorkItemID, track.Number)
		case item.MovedFrom != 0:
			fmt.Fprintf(stdout, "%s: recorded as carried by pull request #%d (moved from pull request #%d)\n", item.WorkItemID, track.Number, item.MovedFrom)
		case item.Unverified:
			fmt.Fprintf(stdout, "%s: recorded as carried by pull request #%d (recorded as unverified: its landing commit is not among the pull request's commits)\n", item.WorkItemID, track.Number)
		default:
			fmt.Fprintf(stdout, "%s: recorded as carried by pull request #%d\n", item.WorkItemID, track.Number)
		}
	}
}

// printUntrackedPullRequest says what removing a pull request did to each item
// it was recorded on.
func printUntrackedPullRequest(stdout, stderr io.Writer, track orchestrator.TeamPullRequestTrack) {
	for _, item := range track.Items {
		switch {
		case item.Failure != "":
			fmt.Fprintf(stderr, "%s: not changed: %s\n", item.WorkItemID, item.Failure)
		case item.Unchanged:
			fmt.Fprintf(stdout, "%s: pull request #%d was not tracked on it any longer\n", item.WorkItemID, track.Number)
		default:
			fmt.Fprintf(stdout, "%s: pull request #%d no longer tracked; it still waits for its landing commit to be found on the team's branch\n", item.WorkItemID, track.Number)
		}
	}
}

func printPullRequestUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo pull-request track <number> --item <id> [--item <id>...] [options]
       yoyo pull-request untrack <number> [options]

Records which pull request into the team's branch carries items waiting for the
team's merge (execution.upstream_branch). Use it when an outside tool opens a
pull request of its own for a batch of landed changes.

track reads the pull request first and records nothing if it goes into another
branch, was closed without being merged, names an item that is not waiting for
the team's merge on this project's target branch, or names an item whose landing
commit is not among its commits. An item is carried by one pull request at a
time, so tracking it on a second moves it. untrack removes the record from every
item it is on; those items go on waiting for their landing commit to be found on
the team's branch.

Each forge check reads every tracked pull request: one merged closes its items,
one closed without merging gives them back to the Lead Product Manager, and one
still open picks up the waiting items whose landing commit is among its commits.

Options:
  --item <id>        a work item the pull request carries; repeat for each (track)
  --unverified       record items whose landing commit is not among the pull
                     request's commits, for a batch built from new commits such
                     as a squash (track)
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --json             emit machine-readable JSON`)
}

// printTeamPullRequests says what the check found of each tracked pull request
// into the team's branch.
func printTeamPullRequests(stdout, stderr io.Writer, pulls []orchestrator.TeamPullRequestSettlement) {
	for _, pull := range pulls {
		if pull.Failure != "" {
			fmt.Fprintf(stderr, "pull request #%d into the team's branch not settled: %s\n", pull.Number, pull.Failure)
		}
		for _, item := range pull.Items {
			switch {
			case item.Failure != "":
				fmt.Fprintf(stderr, "%s on pull request #%d not settled: %s\n", item.WorkItemID, pull.Number, item.Failure)
			case item.Kept != "":
				fmt.Fprintf(stdout, "%s left as it stands: %s\n", item.WorkItemID, item.Kept)
			case item.Settled && pull.Found == orchestrator.TeamPullRequestMerged:
				fmt.Fprintf(stdout, "pull request #%d into the team's branch merged as %s; %s closed\n", pull.Number, pull.MergeCommit, item.WorkItemID)
			case item.Settled && pull.Found == orchestrator.TeamPullRequestClosed:
				who := pull.ClosedBy
				if who == "" {
					who = "an account the forge did not name"
				}
				fmt.Fprintf(stdout, "pull request #%d was closed without being merged by %s; %s goes back to the Lead Product Manager\n", pull.Number, who, item.WorkItemID)
			case item.Added:
				fmt.Fprintf(stdout, "%s's landing commit is among the commits of pull request #%d; it is now tracked as carrying it\n", item.WorkItemID, pull.Number)
			}
		}
	}
}

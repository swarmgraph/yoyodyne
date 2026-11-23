package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamrecords"
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
	// TeamBranchUpdate is what the check did to bring the target branch up to
	// date with the team's branch, present only where execution.upstream_branch
	// is set.
	TeamBranchUpdate *orchestrator.TeamBranchUpdateOutcome `json:"team_branch_update,omitempty"`
	// TeamFeedbackRepairs are the new runs this check started for repairs the
	// development manager decided from feedback on a tracked pull request, each
	// hosted by this process to its end. Present only where
	// execution.upstream_branch is set.
	TeamFeedbackRepairs []orchestrator.TeamFeedbackRepairCarried `json:"team_feedback_repairs,omitempty"`
	// KeptAnswers is how many of the forge's answers are kept for the next check
	// to ask about conditionally, and KeptAnswersProblem why they could not be
	// read or written, where they could not; either costs full reads and
	// nothing else.
	KeptAnswers        int    `json:"kept_answers"`
	KeptAnswersProblem string `json:"kept_answers_problem,omitempty"`
	// Records is what sharing the team's records did, and is absent with team
	// mode off.
	Records *teamrecords.Result `json:"records,omitempty"`
	Error   string              `json:"error,omitempty"`
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
	endMergeHoldsLeftBehind(ctx, parts, stderr)
	reconciler := forgeCheckerFrom(parts, answers)
	output := forgeCheckOutput{KeptAnswersProblem: afresh}
	output.ForgeCheck, err = reconciler.CheckForge(ctx)
	if saveErr := answers.Save(); saveErr != nil {
		output.KeptAnswersProblem = saveErr.Error()
	}
	output.KeptAnswers = answers.Len()
	output.Forge = reconciler.Forge
	// With team mode on, the records other factories shared are copied in and the
	// ones filed here are sent, once per check. A remote that cannot be reached is
	// in the result and is not an error: the records stay here and the next check
	// sends them.
	if sharer, on, sharerErr := teamRecords(ctx, parts); sharerErr != nil {
		err = errors.Join(err, sharerErr)
	} else if on {
		shared, syncErr := sharer.Sync(ctx)
		output.Records = &shared
		err = errors.Join(err, syncErr)
		// The spends this machine counted before it joined are pushed once. A remote
		// that cannot be reached leaves that for the next check.
		if joinErr := joinTeamSpends(ctx, parts); joinErr != nil {
			fmt.Fprintf(stderr, "the spends this machine counted before joining the team are not shared yet: %v\n", joinErr)
		}
	}
	if !*jsonOutput {
		printForgeCheckFindings(stdout, stderr, output)
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
	var updateErr, repairErr, branchErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		output.Updates, updateErr = updater.ContinueUpdates(ctx)
	}()
	// Bringing the target branch up to date with the team's branch runs checks
	// and a provider review, so it is hosted here beside the other two rather
	// than before the read line.
	branchDone := make(chan struct{})
	go func() {
		defer close(branchDone)
		if parts.config.Execution.UpstreamBranch == "" {
			return
		}
		branchUpdater, buildErr := teamBranchUpdaterFrom(parts, reconciler)
		if buildErr != nil {
			branchErr = buildErr
			return
		}
		outcome, runErr := branchUpdater.Update(ctx)
		output.TeamBranchUpdate = &outcome
		branchErr = runErr
	}()
	// The repairs she decided from feedback on tracked pull requests are runs of
	// their own, started and hosted here beside the others.
	teamRepairDone := make(chan struct{})
	var teamRepairErr error
	go func() {
		defer close(teamRepairDone)
		if parts.config.Execution.UpstreamBranch == "" {
			return
		}
		output.TeamFeedbackRepairs, teamRepairErr = feedbackRepairerFrom(parts).CarryOut(ctx)
	}()
	output.FeedbackRepairs, repairErr = repairer.ContinueFeedbackRepairs(ctx)
	<-done
	<-branchDone
	<-teamRepairDone
	err = errors.Join(err, updateErr, repairErr, branchErr, teamRepairErr)
	if !*jsonOutput {
		printTeamBranchUpdate(stdout, output.TeamBranchUpdate)
		printUpdates(stdout, stderr, output.Updates)
		printFeedbackRepairs(stdout, stderr, output.FeedbackRepairs)
		printTeamFeedbackRepairs(stdout, stderr, output.TeamFeedbackRepairs)
	}
	return reportForgeCheck(stdout, stderr, *jsonOutput, output, err)
}

// printForgeCheckFindings is what a check without --json says it read and did,
// before it hosts anything: each pull request waiting for a person, each
// tracked pull request into the team's branch, each change waiting for the
// team's merge, the feedback read, what was shared with other factories when
// team mode is on, and the closing count.
func printForgeCheckFindings(stdout, stderr io.Writer, output forgeCheckOutput) {
	printPersonMerges(stdout, stderr, output.PersonMerges)
	printTeamPullRequests(stdout, stderr, output.TeamPullRequests)
	printTeamMerges(stdout, stderr, output.TeamMerges)
	printFeedback(stdout, stderr, output.Feedback)
	printTeamFeedback(stdout, stderr, output.TeamFeedback)
	if output.Records != nil {
		fmt.Fprintln(stdout, output.Records.Says())
	}
	if output.Tracker != nil {
		fmt.Fprintf(stdout, "tracker: %s\n", output.Tracker.Says())
	}
	if output.TrackerFailure != "" {
		fmt.Fprintf(stderr, "tracker: %s; the next check tries again\n", output.TrackerFailure)
	}
	fmt.Fprintln(stdout, describeForgeCheck(output))
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
	reconciler.TeamPullRequests = forge
	reconciler.HostsRuns = true
	reconciler.RetryAtNextCheck = true
	reconciler.Forge = &orchestrator.ForgeQuestions{}
	reconciler.TrackerShare = trackerShareFrom(parts)
	reconciler.BatchRoute = batchRouteFrom(parts)
	return reconciler
}

// teamBranchUpdaterFrom is the update of the target branch to the team's
// branch, built over the parts the rest of the forge check uses: the pipeline
// the harness lands work with, the branch review's reviewer for the independent
// review, and the reconciler's report collector.
func teamBranchUpdaterFrom(parts components, reconciler orchestrator.Reconciler) (orchestrator.TeamBranchUpdater, error) {
	records, err := runstate.NewTeamBranchUpdateStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return orchestrator.TeamBranchUpdater{}, err
	}
	pipeline := pipelineFrom(parts)
	return orchestrator.TeamBranchUpdater{
		Pipeline: &pipeline,
		Git:      parts.worktrees,
		Records:  records,
		Reviewer: branchReviewerFrom(parts, ""),
		Reports:  reconciler.Reports,
	}, nil
}

// printTeamBranchUpdate says in plain words what the check did to bring the
// target branch up to date with the team's branch.
func printTeamBranchUpdate(stdout io.Writer, outcome *orchestrator.TeamBranchUpdateOutcome) {
	if outcome == nil || outcome.TargetBranch == "" {
		return
	}
	target, upstream := outcome.TargetBranch, outcome.UpstreamBranch
	switch {
	case outcome.Busy:
		fmt.Fprintf(stdout, "bringing %s up to date with %s: %s\n", target, upstream, outcome.Waiting)
	case len(outcome.Conflict) > 0:
		fmt.Fprintf(stdout, "bringing %s up to date with %s is waiting: merging it conflicts in %s\n", target, upstream, strings.Join(outcome.Conflict, ", "))
		if outcome.Waiting != "" {
			fmt.Fprintf(stdout, "  %s\n", outcome.Waiting)
		}
	case outcome.Landed != "":
		fmt.Fprintf(stdout, "brought %s up to date with %s: merge %s\n", target, upstream, shortCommit(outcome.Landed))
	case outcome.Waiting != "":
		fmt.Fprintf(stdout, "bringing %s up to date with %s is waiting: %s\n", target, upstream, outcome.Waiting)
	case outcome.Current:
		fmt.Fprintf(stdout, "%s already carries %s\n", target, upstream)
	}
}

// endMergeHoldsLeftBehind ends every hold on an item whose two machines' versions
// could not be merged, where team mode is off. No check reads those versions any
// more, so nothing else would end them, and each item would stay out of what the
// harness starts with nobody left to clear it. It runs at each forge check and each
// reconcile sweep, and costs one directory read where nothing is held. A failure is
// said and left for the next one. It speaks on standard error because both verbs
// keep standard output for their JSON.
func endMergeHoldsLeftBehind(ctx context.Context, parts components, stderr io.Writer) {
	if parts.config.Team.Enabled {
		return
	}
	store, err := runstate.NewTrackerMergeStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		fmt.Fprintf(stderr, "tracker: read which items are held because two machines' versions cannot be merged: %v\n", err)
		return
	}
	ended, err := store.EndAllHolds(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "tracker: end the holds left from when team mode was on, on items whose two machines' versions could not be merged: %v\n", err)
	}
	for _, hold := range ended {
		fmt.Fprintf(stderr, "tracker: team mode is off, so %s is no longer held for two machines' versions that could not be merged, and it can start again\n", hold.Names())
	}
}

// trackerShareFrom is what merges the tracker's shared export at each forge
// check, and nil with team mode off, where this machine's tracker is the only
// one there is. A record that cannot be opened in team mode is not dropped: each
// check says so on its own failure line.
func trackerShareFrom(parts components) *orchestrator.TrackerShare {
	if !parts.config.Team.Enabled || parts.worktrees == nil {
		return nil
	}
	store, err := runstate.NewTrackerMergeStore(parts.stateRoot, parts.config.Product.ID)
	return &orchestrator.TrackerShare{
		Remote:       parts.worktrees,
		Tracker:      parts.tracker(),
		Store:        store,
		StoreProblem: err,
		Reports:      reportsOf(parts),
		ProductID:    parts.config.Product.ID,
		RepositoryID: string(parts.config.Product.RepositoryID),
		Build:        buildinfo.Commit(),
	}
}

// describeForgeCheck is the check's last line: how many pull requests it read,
// and how many it could not read or finish acting on.
func describeForgeCheck(output forgeCheckOutput) string {
	said := fmt.Sprintf("forge check: %d pull request(s) waiting for a person to merge them, %d read for feedback", len(output.PersonMerges), len(output.Feedback))
	if len(output.TeamMerges) > 0 {
		said += fmt.Sprintf(", %d landed change(s) waiting for the team to merge them", len(output.TeamMerges))
	}
	if len(output.TeamPullRequests) > 0 {
		said += fmt.Sprintf(", %d pull request(s) into the team's branch read", len(output.TeamPullRequests))
	}
	if failures := output.Failures(); failures > 0 {
		said += fmt.Sprintf("; %d could not be read or acted on, and the next check reads them again", failures)
	}
	return said
}

// printTeamFeedback says what the check did about the feedback on each tracked
// pull request into the team's branch.
func printTeamFeedback(stdout, stderr io.Writer, settlements []orchestrator.TeamFeedbackSettlement) {
	for _, settled := range settlements {
		switch {
		case settled.Failure != "":
			fmt.Fprintf(stderr, "feedback on pull request #%d into the team's branch: %s\n", settled.Number, settled.Failure)
		case settled.New > 0:
			fmt.Fprintf(stdout, "feedback on pull request #%d into the team's branch: %s\n", settled.Number, settled.Detail)
		}
	}
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
		if output.TeamPullRequests == nil {
			output.TeamPullRequests = []orchestrator.TeamPullRequestSettlement{}
		}
		if output.Feedback == nil {
			output.Feedback = []orchestrator.FeedbackSettlement{}
		}
		if output.TeamFeedback == nil {
			output.TeamFeedback = []orchestrator.TeamFeedbackSettlement{}
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

With team mode on, each check also copies the reports, their handling and the
directives other factories shared into this machine's own records, and sends the
ones filed here. Any that cannot be sent stay here and are counted on the status
line until a later check sends them.

With execution.upstream_branch set, it also brings the target branch up to date
with the team's branch: a merge of the team's branch, built in a checkout of its
own, is checked, independently reviewed, and landed without rebasing or forcing
the target. A conflict is put on the development manager's list and the update
waits until either branch moves.

The repair rounds and updates it starts are hosted by this process to their
end, after the check's own report.

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --json             emit machine-readable JSON`)
}

// printTeamFeedbackRepairs says what each repair decided from feedback on a
// tracked pull request came to. A decision that could not be carried out yet
// stands and is tried again at the next check, so it is said and does not fail
// the pass.
func printTeamFeedbackRepairs(stdout, stderr io.Writer, repairs []orchestrator.TeamFeedbackRepairCarried) {
	for _, repair := range repairs {
		switch {
		case repair.Started:
			fmt.Fprintf(stdout, "%s: repair from %s on pull request #%d started as run %s\n", repair.WorkItemID, repair.FeedbackID, repair.PullRequest, repair.NewRunID)
		case repair.Waiting != "":
			fmt.Fprintf(stdout, "%s: repair from %s on pull request #%d not started yet: %s\n", repair.WorkItemID, repair.FeedbackID, repair.PullRequest, repair.Waiting)
		}
		if repair.Failure != "" {
			fmt.Fprintf(stderr, "%s: repair from %s on pull request #%d: %s\n", repair.WorkItemID, repair.FeedbackID, repair.PullRequest, repair.Failure)
		}
	}
}

// batchRouteFrom is the harness's own batch pull request: nil unless the project
// enables it and sets execution.upstream_branch, which runs none of it. A record
// that cannot be opened leaves the route off for this check rather than failing it.
func batchRouteFrom(parts components) *orchestrator.BatchRoute {
	if !parts.config.Execution.BatchRouteOn() {
		return nil
	}
	store, err := runstate.NewBatchRecordStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return nil
	}
	return &orchestrator.BatchRoute{Settings: parts.config.Execution.BatchPullRequest, Records: store}
}

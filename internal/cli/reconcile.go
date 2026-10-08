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
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/watchdog"
)

type reconcileOutput struct {
	TrackerExports beads.ExportCleanup           `json:"tracker_exports"`
	Runs           []orchestrator.Reconciliation `json:"runs"`
	// Recoveries is what this sweep did about the promoted runs whose record
	// named no pull request: the request the forge holds for the run's branch,
	// written back onto the record so the refresh, the settlement, the docket,
	// and the status line can read it, and the merge the run never asked for,
	// armed through the run's own gate. It is reported first because it is what
	// the rest of the publication sweep then reads.
	Recoveries []orchestrator.PublicationRecovery `json:"recoveries"`
	// Publications is what the forge now says about the pull requests the harness
	// recorded and nothing settled. It is reported beside the runs rather than
	// folded into them because it is about records of finished work: nothing here
	// settles a run, and a corrected record is a fact about the forge rather than
	// a step somebody was owed.
	Publications []orchestrator.PublicationRefresh `json:"publications"`
	// Settlements is what this sweep did about the publications the harness had
	// recorded as merged and unfinished: the merge confirmed on the remote and the
	// record finished, or what still stands in the way. It is beside the refresh
	// rather than folded into it because the two are different acts — one writes
	// what the forge says, the other finishes what the record says is unfinished.
	Settlements []orchestrator.PublicationSettlement `json:"settlements"`
	// RedTargets is what this sweep did about the publications waiting on their
	// target's red check: still waiting on an open item, brought up to date
	// where the fix left the head behind, filed again where the check is still
	// red, or left for the watch to re-arm.
	RedTargets  []orchestrator.RedTargetResumption `json:"red_targets"`
	Convergence orchestrator.Convergence           `json:"convergence"`
	// Docketed is how many entries this sweep is what put on the triage docket.
	// It is a count rather than the entries because the docket is read where it
	// is acted on, which is the development manager's conversation; what this
	// command reports is that the sweep found something, not what it found.
	Docketed int `json:"docketed"`
	// ClosedWithItem is how many docket entries this sweep closed because the item
	// each is about is one the tracker holds as closed. A count, for the reason
	// Docketed is one.
	ClosedWithItem int `json:"closed_with_item"`
	// EscalationsEnded is each escalation to the operator this sweep found had
	// ended — its item parked, retired, or closed, or its run's branch and
	// worktree both gone — with the item it told and what ended it.
	EscalationsEnded []orchestrator.EscalationSettlement `json:"escalations_ended"`
	// Supervision is the whole of what this sweep made of the exchanges the roles
	// have put to each other. Most of it is what was recovered: a round a dead
	// process asked and never answered, a thread that ran out of rounds. The rest
	// is every exchange the sweep found free and did not take a round on, since it
	// carries no voice — which is the ordinary state of an exchange waiting its
	// turn, and is carried here rather than printed for the same reason `--json`
	// carries the whole of every other sweep.
	Supervision []orchestrator.SupervisionResult `json:"supervision"`
	// Stall is what this sweep made of whether anything is happening at all: the
	// silence it read, and what that opened, closed, or left standing in the
	// product's own stall record.
	Stall *watchdog.Reading `json:"stall,omitempty"`
	// StallProblem is why that reading was not made. It costs the sweep a line and
	// nothing else — every other step it took stands — so it is reported beside
	// the sweep rather than failing it, exactly as a staleness reading is.
	StallProblem string `json:"stall_problem,omitempty"`
	// Continuations is what this sweep did about the runs that exited on their
	// in-process usage-limit bound with the deadline since passed: each one
	// continued in its own worktree and session, hosted by this process to its
	// end, with what it came to. It is the last thing the sweep does and the one
	// step that invokes a provider, so a document carrying any is one that was
	// written after those runs finished.
	Continuations []orchestrator.WaitContinuation `json:"continuations"`
	// Updates is what this sweep did about the queued merges it put back at
	// their promotion because their head fell behind the target and failed
	// checks the change does not touch: each run hosted by this process through
	// the replay, the re-review, and the merge queued again, with what it came
	// to. Like the continuations it is written once those runs have finished.
	Updates []orchestrator.UpdateContinuation `json:"updates"`
	// Forge is what this sweep asked the forge about its publications and how
	// long the forge took: only the unsettled ones are asked, in batches, and a
	// pass that takes long says where the time went.
	Forge *orchestrator.ForgeQuestions `json:"forge,omitempty"`
	Error string                       `json:"error,omitempty"`
}

// reconcileSweep is everything one sweep found, gathered so the reporting takes
// the sweep rather than a growing list of positional arguments.
type reconcileSweep struct {
	TrackerExports beads.ExportCleanup
	Runs           []orchestrator.Reconciliation
	Recoveries     []orchestrator.PublicationRecovery
	Publications   []orchestrator.PublicationRefresh
	Settlements    []orchestrator.PublicationSettlement
	RedTargets     []orchestrator.RedTargetResumption
	Convergence    orchestrator.Convergence
	Docketed       int
	// ClosedWithItem is how many docket entries the sweep closed with their item.
	ClosedWithItem int
	// EscalationsEnded is each escalation to the operator the sweep ended.
	EscalationsEnded []orchestrator.EscalationSettlement
	Supervision      []orchestrator.SupervisionResult
	Stall            *watchdog.Reading
	StallProblem     string
	Continuations    []orchestrator.WaitContinuation
	Updates          []orchestrator.UpdateContinuation
	// Forge is what the sweep asked the forge about its publications, and how
	// long the forge took.
	Forge *orchestrator.ForgeQuestions
}

// reconcileRuns settles every run an interrupted process left outstanding and
// then converges local state onto the forge's. It is safe to repeat: a run that
// is already settled is not outstanding, a branch already caught up has nothing
// to catch up to, and the steps it does take are idempotent over artifacts that
// are already gone.
func reconcileRuns(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	// How long nothing may start, over ready work and with nothing accounting for
	// it, before this sweep records that the harness has stopped. It is a flag
	// because the right number is the operator's judgement about their own machine
	// rather than the harness's, and it is here rather than on the Slack sink
	// because this is the sweep that always runs: reporting is optional, and a
	// threshold set on an optional process is a threshold most products never get.
	//
	// How promptly a stall is noticed is this number and the cadence of whatever
	// runs this sweep, so an unattended pass should run at least as often as the
	// threshold it sets.
	stallAfter := flags.Duration("stall-after", readmodel.DefaultStallThreshold, "how long nothing may start over ready work before this records that the harness has stopped")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "reconcile does not accept positional arguments")
		printReconcileUsage(stderr)
		return 2
	}
	// There is deliberately no way to ask for no watchdog: a threshold of zero
	// would take the default rather than turn anything off, so reading it as a
	// switch would silently do the opposite of what somebody meant by it.
	if *stallAfter <= 0 {
		fmt.Fprintln(stderr, "stall-after must be positive; it is how long nothing may start rather than a switch, and there is no way to ask not to be told")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportReconcileResult(stdout, stderr, *jsonOutput, reconcileSweep{}, err)
	}
	// Typed by a person, this is the operator settling runs by hand; run by the
	// supervisor's maintenance pass it is the harness's own sweep, and the pass
	// marks it so nothing is recorded (see intervention.go).
	noteHandStep(parts.interventions, parts.config.Product.ID, stderr, handStep{
		kind: intervention.KindSettle,
		said: "settled interrupted runs and converged local state by hand with yoyo reconcile",
	})
	trackerExports, trackerExportErr := maintainTrackerExports(ctx, parts)
	reconciler := reconcilerFrom(parts)
	// This sweep hosts the runs it makes live, as its last step, so a queued head
	// that fell behind its target is put back at its promotion here rather than
	// left queued for a pass that will host it.
	reconciler.HostsRuns = true
	// What the sweep asks the forge is tallied, so the pass says how many
	// publications it asked about and how long the forge took.
	reconciler.Forge = &orchestrator.ForgeQuestions{}
	results, err := reconciler.Reconcile(ctx)
	err = errors.Join(err, trackerExportErr)
	// A promoted run whose record names no pull request is asked about first, by
	// its branch, and the request the forge holds is written onto the record: the
	// refresh, the settlement, the docket, and every status surface start from
	// the request, so a record without one is a publication none of them can see.
	recoveries, recoveryErr := reconciler.RecoverPublications(ctx)
	err = errors.Join(err, recoveryErr)
	// The publications of runs nothing is going to settle are refreshed next, and
	// before the docket is built: a record frozen at its run's death is what the
	// docket, the orphan sweep and every status surface read, so a sweep that
	// docketed first would decide against the very staleness it was about to fix.
	publications, publicationErr := reconciler.RefreshPublications(ctx)
	err = errors.Join(err, publicationErr)
	// The publications the harness recorded as merged and could not finish are
	// finished next, after the refresh has recorded which of them the forge has
	// since merged and before the docket is built: a docket built first would
	// docket a publication this sweep was about to settle, and a hold read first
	// would hold an item whose publication is about to stop being outstanding.
	settlements, settlementErr := reconciler.FinishPublications(ctx)
	err = errors.Join(err, settlementErr)
	// The publications waiting on their target's red check are taken up next,
	// before the docket is built and before the runs this sweep makes live are
	// hosted: one whose items have closed and whose head the fix left behind is
	// put back at its promotion here, and hosted with the other updates below.
	redTargets, redTargetErr := reconciler.ResumeRedTargets(ctx)
	err = errors.Join(err, redTargetErr)
	// Convergence is swept even when settling a run failed. The two are
	// independent — one finishes runs, the other finishes branches — and a
	// checkout left behind the forge because some unrelated run could not be
	// settled is exactly the manual seam this exists to close.
	convergence, convergeErr := reconciler.Converge(ctx)
	err = errors.Join(err, convergeErr)
	// The docket is built for the same reason the convergence sweep runs: this
	// is one of the two moments anything scans. A publication the forge quietly
	// never merged is not an event anybody can be present for, so it is found
	// here or when a development manager opens a conversation, and a sweep that
	// settled runs without looking would leave it for the other one.
	docketed, docketErr := docketerFrom(parts).Build()
	if docketErr != nil {
		err = errors.Join(err, docketErr)
	}
	// Every entry standing for an item the tracker holds as closed is closed with
	// it, whichever process closed the item: a landing, a sweep, or a
	// conversation. The places that close an item close its entries as they do,
	// and this is what catches the ones they missed and the ones left standing
	// from before any of them did.
	closedWithItem, closedErr := closeEntriesOfClosedItems(ctx, parts)
	err = errors.Join(err, closedErr)
	// An escalation to the operator whose item has since been parked, retired,
	// or closed, or whose run's change is gone, is told to its item once and
	// recorded on the run. The read model has already stopped naming it; this is
	// what says why on the item, and what the channel reads it from.
	escalationsEnded, escalationsErr := reconciler.EndEscalations(ctx)
	err = errors.Join(err, escalationsErr)
	// The exchanges the roles have put to each other are recovered here for the
	// same reason the runs are: a process died holding something, and this is the
	// sweep that finds out. It takes each exchange's own lease, so one a live
	// process is carrying is left to that process, and it invokes nothing.
	supervision, supervisionErr := sweepSupervision(parts)
	err = errors.Join(err, supervisionErr)
	// Whether anything is happening at all, read last and for two reasons. It is
	// the only step here that says something about the machine rather than about
	// one run, and every step above it moves what it would read: a run whose
	// process died goes on saying it is in flight until the settling above, and a
	// phantom run counted as activity would silence this for exactly the crash it
	// exists to catch.
	//
	// This sweep is where it lives because it is what an unattended pass runs and
	// what an operator runs by hand, neither of which depends on Slack reporting
	// having been turned on. A reading that fails is reported and does not fail the
	// sweep: nothing was recorded, and the next pass decides.
	stall, stallProblem := checkForStall(ctx, parts, *stallAfter)
	sweep := reconcileSweep{
		TrackerExports:   trackerExports,
		Runs:             results,
		Recoveries:       recoveries,
		Publications:     publications,
		Settlements:      settlements,
		RedTargets:       redTargets,
		Convergence:      convergence,
		Docketed:         docketed.Added,
		ClosedWithItem:   closedWithItem,
		EscalationsEnded: escalationsEnded,
		Supervision:      supervision,
		Stall:            stall,
		StallProblem:     stallProblem,
		Forge:            reconciler.Forge,
	}
	// The runs that exited on their in-process usage-limit bound and whose
	// deadline has since passed are continued last, after everything the sweep
	// reads and settles, because this is the step that invokes a provider
	// and takes as long as a developer attempt takes. This process hosts each
	// continued run to its end, exactly as `yoyo run` would, so the terminal
	// sees the rest of the sweep before it waits: in the text form the report
	// above is printed first and each continuation is said as it starts and as
	// it ends; the JSON form is one document, so it is written once the
	// continued runs have finished, with what they came to in it.
	//
	// The queued merges the settlement put back at their promotion, to bring a
	// head that fell behind its target up to date, are hosted the same way: each
	// is a replay, a re-run of the checks, and a review.
	//
	// The two are hosted side by side rather than one after the other: a run put
	// back at its promotion is live with nothing serving it until it is taken
	// up, and waiting behind a continuation that takes a developer attempt's
	// length would leave it looking to the claim audit like a claim nothing is
	// working on.
	reconciler.Continue = continueWaitFrom(parts, stderr)
	updater := reconciler
	updater.Continue = continueUpdateFrom(parts, stderr)
	type hostedRuns struct {
		continuations []orchestrator.WaitContinuation
		continueErr   error
		updates       []orchestrator.UpdateContinuation
		updateErr     error
	}
	host := func() hostedRuns {
		var hosted hostedRuns
		done := make(chan struct{})
		go func() {
			defer close(done)
			hosted.updates, hosted.updateErr = updater.ContinueUpdates(ctx)
		}()
		hosted.continuations, hosted.continueErr = reconciler.ContinueWaits(ctx)
		<-done
		return hosted
	}
	if *jsonOutput {
		hosted := host()
		sweep.Continuations = hosted.continuations
		sweep.Updates = hosted.updates
		return reportReconcileResult(stdout, stderr, true, sweep, errors.Join(err, hosted.continueErr, hosted.updateErr))
	}
	code := reportReconcileResult(stdout, stderr, false, sweep, err)
	hosted := host()
	continuations, continueErr, updates, updateErr := hosted.continuations, hosted.continueErr, hosted.updates, hosted.updateErr
	if continueErr != nil {
		fmt.Fprintf(stderr, "continuing the runs whose usage-limit deadline has passed failed: %v\n", continueErr)
		code = 1
	}
	if printContinuations(stdout, stderr, continuations) {
		code = 1
	}
	if updateErr != nil {
		fmt.Fprintf(stderr, "updating the queued merges whose head fell behind their target failed: %v\n", updateErr)
		code = 1
	}
	if printUpdates(stdout, stderr, updates) {
		code = 1
	}
	return code
}

// continueUpdateFrom is the continuation a queued head is brought up to date
// by: the same pipeline, re-entering the run named at its promotion. It says so
// on standard error first, for the reason continueWaitFrom does.
func continueUpdateFrom(parts components, stderr io.Writer) func(context.Context, string, string) (orchestrator.Outcome, error) {
	return func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
		fmt.Fprintf(stderr, "updating run %s for %s: its queued merge's head fell behind its target and failed checks its change does not touch; this sweep hosts the replay, the checks, the review, and the merge queued again\n", runID, workItemID)
		pipeline := pipelineFrom(parts)
		return pipeline.Continue(ctx, workItemID, runID)
	}
}

// printUpdates says what each continuation came to. Its refusals are per-item
// findings, so they do not fail a pass that could read and visit the other runs.
func printUpdates(stdout, stderr io.Writer, updates []orchestrator.UpdateContinuation) bool {
	for _, update := range updates {
		what := "queued-head continuation"
		if update.Retired {
			what = "run retired after its item merged"
		}
		fmt.Fprintf(stdout, "%s (%s): %s\n", update.RunID, update.WorkItemID, what)
		if update.Retired || !update.Continued {
			if update.Detail != "" {
				fmt.Fprintf(stdout, "  %s\n", update.Detail)
			}
		} else if outcome := update.Outcome; outcome != nil {
			switch {
			case outcome.PullRequest != nil && outcome.PullRequest.MergeQueued:
				fmt.Fprintf(stdout, "  replayed, checked, reviewed, and its merge queued again on pull request #%d\n", outcome.PullRequest.Number)
			case outcome.Blocked:
				fmt.Fprintf(stdout, "  ended %s in the %s phase with a blocker on the item; the development manager decides what happens to it\n", outcome.Status, outcome.Phase)
			default:
				fmt.Fprintf(stdout, "  ended %s in the %s phase\n", outcome.Status, nonEmptyValue(string(outcome.Phase), "unrecorded"))
			}
		}
		if update.Failure != "" {
			fmt.Fprintf(stderr, "  not updated: %s\n", update.Failure)
		}
		printReconcileFinding(stdout, stderr, update.Finding, update.FindingProblem)
	}
	return false
}

// continueWaitFrom is the continuation the sweep continues a run with: the
// same pipeline `yoyo run` builds, re-entering the run named. It says on
// standard error that the run is being continued before it is, because what
// follows is a developer attempt hosted by a command somebody may be waiting on
// the return of, and a terminal that went quiet for an hour with nothing said
// reads as a sweep that hung.
func continueWaitFrom(parts components, stderr io.Writer) func(context.Context, string, string) (orchestrator.Outcome, error) {
	return func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
		fmt.Fprintf(stderr, "continuing run %s for %s: its usage-limit deadline has passed and no process was serving the wait; this sweep hosts it until it ends\n", runID, workItemID)
		// The pipeline is a value, so each continued run gets its own, exactly as
		// each run a pull starts does.
		pipeline := pipelineFrom(parts)
		return pipeline.Continue(ctx, workItemID, runID)
	}
}

// printContinuations says what each continued run came to. A refused
// continuation leaves a finding on its item and does not fail the whole pass.
func printContinuations(stdout, stderr io.Writer, continuations []orchestrator.WaitContinuation) bool {
	for _, continuation := range continuations {
		fmt.Fprint(stdout, describeContinuation(continuation))
		if continuation.Failure != "" {
			fmt.Fprintf(stderr, "  not continued: %s\n", continuation.Failure)
		}
		printReconcileFinding(stdout, stderr, continuation.Finding, continuation.FindingProblem)
	}
	return false
}

// describeContinuation is the lines the text form prints for one continued
// run: the run and what it was waiting out, then what the sweep did with it,
// then what the continued run came to in the words `yoyo run` ends on.
func describeContinuation(continuation orchestrator.WaitContinuation) string {
	var lines strings.Builder
	if outcome := continuation.Outcome; outcome != nil && outcome.Retirement != nil {
		fmt.Fprintf(&lines, "%s (%s): run retired after its item merged\n  %s\n", continuation.RunID, continuation.WorkItemID, outcome.Summary)
		return lines.String()
	}
	fmt.Fprintf(&lines, "%s (%s): paused for %s past its deadline %s\n",
		continuation.RunID, continuation.WorkItemID, continuation.Waited, continuation.Deadline.Format(time.RFC3339))
	if !continuation.Continued {
		if continuation.Detail != "" {
			fmt.Fprintf(&lines, "  %s\n", continuation.Detail)
		}
		return lines.String()
	}
	fmt.Fprintln(&lines, "  continued by this sweep in its own worktree and developer session")
	outcome := continuation.Outcome
	if outcome == nil {
		return lines.String()
	}
	switch {
	case outcome.PausedByOperator != nil:
		fmt.Fprintf(&lines, "  the operator's pause placed at %s holds it, so it was left as it stands; `yoyo resume` lifts the pause\n",
			outcome.PausedByOperator.HeldAt.UTC().Format(time.RFC3339))
	case outcome.Paused && outcome.UsageLimitResetsAt != nil:
		fmt.Fprintf(&lines, "  the provider refused it again: paused for %s until %s, and a sweep after that continues it again\n",
			runstate.DescribePause(outcome.PauseCause, outcome.UsageLimitKind), outcome.UsageLimitResetsAt.UTC().Format(time.RFC3339))
	case outcome.Paused:
		fmt.Fprintln(&lines, "  paused again, and left in flight to be continued")
	case outcome.Integration != nil:
		fmt.Fprintf(&lines, "  ended %s: integrated into %s at %s\n", outcome.Status, outcome.Integration.TargetBranch, outcome.Integration.SourceCommit)
	case outcome.Blocked:
		fmt.Fprintf(&lines, "  ended %s in the %s phase with a blocker on the item; the development manager decides what happens to it\n", outcome.Status, outcome.Phase)
	default:
		fmt.Fprintf(&lines, "  ended %s in the %s phase\n", outcome.Status, nonEmptyValue(string(outcome.Phase), "unrecorded"))
	}
	return lines.String()
}

// checkForStall takes one reading of whether this product has gone quiet and
// reconciles it against the product's durable stall record.
//
// The record is where `yoyo status` reads the history back from and where the
// Slack sink reads the one message it says about it, so a product reporting
// nowhere still records its stalls and can be asked about them afterwards — which
// is the whole of what this being here rather than in the sink buys.
func checkForStall(ctx context.Context, parts components, threshold time.Duration) (*watchdog.Reading, string) {
	stalls, err := runstate.NewStallStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return nil, err.Error()
	}
	reading, err := watchdog.Checker{
		Runs:     parts.store,
		Sessions: parts.watch,
		Holds:    parts.holds,
		Intake:   parts.intake,
		Outages:  parts.outages,
		// A diverged target is a line held on purpose and said on the attention
		// line, so it accounts for the quiet as the outage does.
		Divergences: parts.divergences,
		// The tracker's own count of what a developer run could actually be started
		// for, which is the same reading the sink's heartbeat takes: work marked for
		// a conversation and work the product manager parked are ready to the tracker
		// and are not work anything will pull.
		Backlog:   readyBacklog{tracker: parts.tracker()},
		Stalls:    stalls,
		Threshold: threshold,
	}.Check(ctx)
	if err != nil {
		return nil, err.Error()
	}
	return &reading, ""
}

// closeEntriesOfClosedItems closes every docket entry standing for an item the
// tracker holds as closed, and reports how many it closed. A tracker that could
// not be listed closes nothing and is an error of the sweep's: an unread listing
// says nothing about which items are closed. A docket with nothing standing is
// not listed against at all, so a product with no entries asks the tracker
// nothing.
func closeEntriesOfClosedItems(ctx context.Context, parts components) (int, error) {
	if parts.docket == nil {
		return 0, nil
	}
	return sweepClosedItems(ctx, *docketerFrom(parts), parts.tracker())
}

// closedItemLister is the one tracker reading the sweep over closed items makes.
// It is satisfied by beads.Client.
type closedItemLister interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
}

// sweepClosedItems is that sweep over a docket and a tracker it is handed.
func sweepClosedItems(ctx context.Context, docketer orchestrator.Docketer, tracker closedItemLister) (int, error) {
	entries, err := docketer.Docket.List()
	if err != nil {
		return 0, fmt.Errorf("read the triage docket to close the entries of closed items: %w", err)
	}
	now := time.Now()
	standing := false
	for _, entry := range entries {
		if entry.Closed == nil || !entry.Closed.Holds(now) {
			standing = true
			break
		}
	}
	if !standing {
		return 0, nil
	}
	closed, err := tracker.List(ctx, "closed")
	if err != nil {
		return 0, fmt.Errorf("list the closed work items to close their docket entries: %w", err)
	}
	return docketer.SettleClosedItems(orchestrator.ClosedItemReasons(closed, "a reconcile sweep"))
}

// sweepSupervision takes one voice-less pass of the management loop. A product
// whose roles have never asked each other anything has nothing here, which is
// not a failure to sweep.
func sweepSupervision(parts components) ([]orchestrator.SupervisionResult, error) {
	loop, err := supervisionLoopFrom(parts)
	if err != nil {
		return nil, err
	}
	pass, err := loop.Run()
	return pass.Results, err
}

func reportReconcileResult(stdout, stderr io.Writer, jsonOutput bool, sweep reconcileSweep, err error) int {
	results, publications, convergence, docketed := sweep.Runs, sweep.Publications, sweep.Convergence, sweep.Docketed
	// An item that cannot be settled is a finding, not a failed maintenance
	// pass. Errors discovering the pass's state still fail it.
	failed := err != nil
	if convergence.Registrations.Failure != "" {
		failed = true
	}
	// A divergence that could not be lifted leaves a line holding on branches
	// that have converged, which nothing else will lift.
	if convergence.DivergenceProblem != "" {
		failed = true
	}
	for _, lift := range convergence.Divergences {
		if lift.Failure != "" {
			failed = true
		}
	}
	// Continuation refusals belong to their items' findings.
	if jsonOutput {
		output := reconcileOutput{
			TrackerExports:   sweep.TrackerExports,
			Runs:             results,
			Recoveries:       sweep.Recoveries,
			Publications:     publications,
			Settlements:      sweep.Settlements,
			RedTargets:       sweep.RedTargets,
			Convergence:      convergence,
			Docketed:         docketed,
			ClosedWithItem:   sweep.ClosedWithItem,
			EscalationsEnded: sweep.EscalationsEnded,
			Supervision:      sweep.Supervision,
			Stall:            sweep.Stall,
			StallProblem:     sweep.StallProblem,
			Continuations:    sweep.Continuations,
			Updates:          sweep.Updates,
			Forge:            sweep.Forge,
		}
		if results == nil {
			output.Runs = []orchestrator.Reconciliation{}
		}
		if output.Continuations == nil {
			output.Continuations = []orchestrator.WaitContinuation{}
		}
		if output.Updates == nil {
			output.Updates = []orchestrator.UpdateContinuation{}
		}
		if output.Supervision == nil {
			output.Supervision = []orchestrator.SupervisionResult{}
		}
		if output.EscalationsEnded == nil {
			output.EscalationsEnded = []orchestrator.EscalationSettlement{}
		}
		if output.Recoveries == nil {
			output.Recoveries = []orchestrator.PublicationRecovery{}
		}
		if output.Publications == nil {
			output.Publications = []orchestrator.PublicationRefresh{}
		}
		if output.Settlements == nil {
			output.Settlements = []orchestrator.PublicationSettlement{}
		}
		if output.RedTargets == nil {
			output.RedTargets = []orchestrator.RedTargetResumption{}
		}
		if output.Convergence.Targets == nil {
			output.Convergence.Targets = []gitworktree.Catchup{}
		}
		if output.Convergence.Publications == nil {
			output.Convergence.Publications = []orchestrator.PublicationSweep{}
		}
		if output.Convergence.Unsuperseded == nil {
			output.Convergence.Unsuperseded = []orchestrator.OpenPublication{}
		}
		if output.Convergence.Branches == nil {
			output.Convergence.Branches = []orchestrator.BranchSweep{}
		}
		if output.Convergence.Findings == nil {
			output.Convergence.Findings = []orchestrator.ReconcileFindingMaintenance{}
		}
		if output.Convergence.Worktrees == nil {
			output.Convergence.Worktrees = []orchestrator.WorktreeSweep{}
		}
		if output.Convergence.Registrations.Pruned == nil {
			output.Convergence.Registrations.Pruned = []string{}
		}
		if output.Convergence.Registrations.Unfinished == nil {
			output.Convergence.Registrations.Unfinished = []gitworktree.UnfinishedRegistration{}
		}
		if output.Convergence.Divergences == nil {
			output.Convergence.Divergences = []orchestrator.DivergenceLift{}
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
	} else {
		for _, removed := range sweep.TrackerExports.Removed {
			fmt.Fprintf(stdout, "removed abandoned tracker export temporary %s (%d bytes)\n", removed.Path, removed.Bytes)
		}
		if err != nil {
			fmt.Fprintf(stderr, "reconcile failed: %v\n", err)
		}
		if len(results) == 0 && err == nil {
			fmt.Fprintln(stdout, "no runs need reconciliation")
		}
		// What was docketed is said only when there is some: a line saying nothing
		// stopped, on every sweep, is a line nobody reads.
		if docketed > 0 {
			fmt.Fprintf(stdout, "%d stopped item(s) added to the triage docket for the development manager\n", docketed)
		}
		if sweep.ClosedWithItem > 0 {
			fmt.Fprintf(stdout, "%d triage docket entry(s) closed because the tracker holds their item as closed\n", sweep.ClosedWithItem)
		}
		for _, ended := range sweep.EscalationsEnded {
			if ended.Failure != "" {
				fmt.Fprintf(stdout, "escalation of %s (%s) to the operator has ended, and was not recorded: %s\n", ended.RunID, ended.WorkItemID, ended.Failure)
				continue
			}
			fmt.Fprintf(stdout, "escalation of %s (%s) to the operator ended, and the item was told: %s\n", ended.RunID, ended.WorkItemID, ended.Why)
		}
		for _, result := range results {
			fmt.Fprintf(stdout, "%s (%s): %s\n", result.RunID, result.WorkItemID, result.Action)
			// What the sweep did and what became of the run are two different facts,
			// and the second is the one an operator reads this for: the action says a
			// run was settled, and this says whether their change survived it. Both
			// words are the read model's, so a run described here and the same run in
			// `yoyo status` cannot be described differently.
			if result.Settled() {
				fmt.Fprintf(stdout, "  %s, %s\n", result.Outcome, result.Artifacts().Describe())
			}
			if result.Detail != "" {
				fmt.Fprintf(stdout, "  %s\n", result.Detail)
			}
			if result.Integration != nil {
				fmt.Fprintf(stdout, "  integrated into %s: %s\n", result.Integration.TargetBranch, result.Integration.SourceCommit)
			}
			if result.Action == orchestrator.ActionCompleted {
				fmt.Fprintf(stdout, "  worktree removed: %t, branch removed: %t\n", result.WorktreeRemoved, result.BranchRemoved)
			}
			if result.Catchup != nil && result.Catchup.Advanced {
				fmt.Fprintf(stdout, "  %s caught up to %s\n", result.Catchup.TargetBranch, result.Catchup.RemoteCommit)
			}
			if result.Catchup != nil && result.Catchup.Held != "" {
				fmt.Fprintf(stderr, "  %s not caught up: %s\n", result.Catchup.TargetBranch, result.Catchup.Held)
			}
			if result.Failure != "" {
				fmt.Fprintf(stderr, "  not reconciled: %s\n", result.Failure)
			}
			// A stoppage that was settled and could not be docketed is reported
			// without the run reading as unsettled: what is missing is the
			// delivery to the development manager, not the settlement.
			if result.DocketProblem != "" {
				fmt.Fprintf(stderr, "  not docketed: %s\n", result.DocketProblem)
			}
		}
		for _, result := range results {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		for _, result := range sweep.Recoveries {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		for _, result := range publications {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		for _, result := range sweep.Settlements {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		for _, result := range convergence.Publications {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		for _, result := range convergence.Worktrees {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		for _, result := range convergence.Branches {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		for _, result := range convergence.Findings {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
			if len(result.Cleared) > 0 {
				titles := readmodel.NewWorkItemTitles([]beads.WorkItem{{ID: result.WorkItemID, Title: result.WorkItemTitle}})
				fmt.Fprintln(stdout, titles.Cite(fmt.Sprintf("%s (%s): resolved settlement findings cleared: %v", result.RunID, result.WorkItemID, result.Cleared)))
			}
		}
		for _, result := range sweep.RedTargets {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		for _, result := range sweep.EscalationsEnded {
			printReconcileFinding(stdout, stderr, result.Finding, result.FindingProblem)
		}
		printRecoveries(stdout, stderr, sweep.Recoveries)
		printPublications(stdout, stderr, publications)
		printSettlements(stdout, stderr, sweep.Settlements)
		printRedTargets(stdout, stderr, sweep.RedTargets)
		printConvergence(stdout, stderr, convergence)
		printSupervision(stdout, sweep.Supervision)
		printStall(stdout, stderr, sweep.Stall, sweep.StallProblem)
		printContinuations(stdout, stderr, sweep.Continuations)
		// Said last, so the maintenance pass, which records the end of what this
		// prints, carries how long the forge took on every pass.
		if sweep.Forge != nil {
			fmt.Fprintln(stdout, sweep.Forge.Describe())
		}
	}
	if failed {
		return 1
	}
	return 0
}

// printStall says that this product has stopped doing anything, and says nothing
// at all about one that has not.
//
// A quiet machine with a drained queue, a hold somebody placed, or a run visibly
// working is the ordinary state, and a line about it on every sweep is a line
// nobody reads — which is the same rule the branch and publication sweeps print
// by. What is said is the stall while it stands, whether this sweep opened it or
// found it, because a sweep run by hand after the fact is exactly when somebody
// is asking whether the harness is alive.
//
// The second line is the one to act on: a session whose last word was `stopped`
// wants starting, and one still claiming to be watching wants killing first.
func printStall(stdout, stderr io.Writer, reading *watchdog.Reading, problem string) {
	if problem != "" {
		fmt.Fprintf(stderr, "whether this product has gone quiet was not decided: %s\n", problem)
		return
	}
	if reading == nil || reading.Standing == nil {
		return
	}
	standing := reading.Standing
	fmt.Fprintf(stdout, "nothing has started on this product for %s, with %d item(s) ready\n",
		standing.For().Round(time.Second), standing.Ready)
	if standing.Chooser != "" {
		fmt.Fprintf(stdout, "  %s\n", standing.Chooser)
	}
}

// printSupervision reports what the sweep actually did to an exchange, and
// leaves out every one it merely looked at.
//
// The division is between an outcome that changed a record and one that only
// describes what the sweep found, and it is the same rule the branch and
// publication sweeps above print by. This sweep carries no voice, so most of what
// it comes back with is the second kind: a thread waiting its turn, one another
// process is carrying, one the in-flight bound held back, one whose references
// have moved. Printing those would put a line about an exchange nothing is wrong
// with in front of the two or three that matter, on every sweep — and the noise
// is not hypothetical, since a product with more than a few open threads would
// print one for each of them every time.
//
// `--json` carries the whole pass, which is where to read the staleness and the
// queue.
func printSupervision(stdout io.Writer, results []orchestrator.SupervisionResult) {
	for _, result := range results {
		if !supervisionActed(result.Outcome) {
			continue
		}
		fmt.Fprintf(stdout, "%s: %s\n", result.ExchangeID, result.Outcome)
		if result.Detail != "" {
			fmt.Fprintf(stdout, "  %s\n", result.Detail)
		}
	}
}

// supervisionActed reports an outcome that changed a record, which is what a
// person reading a sweep is owed. Every other outcome is the sweep saying what it
// found, and is carried by `--json` rather than printed.
//
// It is stated as a closed classification rather than a list of what to skip,
// because the failure it guards against has already happened once: an outcome
// added later and left out of a skip list is printed as though the sweep had
// acted on it. TestEverySupervisionOutcomeIsClassified fails on an outcome
// neither side names, so adding one means deciding which it is.
func supervisionActed(outcome orchestrator.SupervisionOutcome) bool {
	switch outcome {
	case orchestrator.SupervisionReclaimed, orchestrator.SupervisionSettled:
		return true
	case orchestrator.SupervisionCarried, orchestrator.SupervisionQueued,
		orchestrator.SupervisionStale, orchestrator.SupervisionUndelivered:
		return false
	default:
		// An outcome nothing classifies is printed rather than swallowed: a sweep
		// that acted and said nothing is the worse of the two failures.
		return true
	}
}

// printRecoveries reports the promoted runs this sweep found a pull request for
// and wrote it onto, what it did about the merge, and the ones it could not. A
// run kept where it stands for a reason is not printed, for the reason the other
// publication sweeps do not print what they left alone. A run the forge could
// not answer for is said on every sweep it stands, because it is still a change
// the forge holds that nothing reports, and a sweep that went quiet about it
// would read as one that had recovered it.
func printRecoveries(stdout, stderr io.Writer, recoveries []orchestrator.PublicationRecovery) {
	for _, recovery := range recoveries {
		if recovery.Recovered {
			fmt.Fprintf(stdout, "pull request #%d of %s recovered from the forge by branch %s and recorded on run %s, which had none\n",
				recovery.Number, recovery.WorkItemID, recovery.Branch, recovery.RunID)
		}
		switch {
		case recovery.Failure != "":
			fmt.Fprintf(stderr, "pull request of %s (run %s) not recovered: %s\n", recovery.WorkItemID, recovery.RunID, recovery.Failure)
		case recovery.Armed && recovery.Queued:
			fmt.Fprintf(stdout, "  its merge is armed: the forge has it queued and performs it once the base branch's requirements are met; `yoyo reconcile` settles the run when it does\n")
		case recovery.Armed:
			fmt.Fprintf(stdout, "  its merge is armed: the forge merged it on the spot; `yoyo reconcile` finishes the publication on its next sweep\n")
		case recovery.Refused != "":
			fmt.Fprintf(stderr, "  its merge was not armed: %s\n", recovery.Refused)
		case recovery.Recovered && recovery.Kept != "":
			fmt.Fprintf(stdout, "  %s\n", recovery.Kept)
		}
	}
}

// printPublications reports only the records this sweep corrected and the ones
// it could not ask about, for the reason printConvergence reports only what it
// changed: a publication that already agreed with the forge is the ordinary
// state, and a line per recorded pull request on every sweep would bury the ones
// that were actually wrong. `--json` carries the whole sweep either way.
func printPublications(stdout, stderr io.Writer, publications []orchestrator.PublicationRefresh) {
	for _, publication := range publications {
		switch {
		case publication.Failure != "":
			fmt.Fprintf(stderr, "pull request #%d not refreshed: %s\n", publication.Number, publication.Failure)
		case publication.Updated:
			fmt.Fprintf(stdout, "pull request #%d of %s recorded as %s, was %s\n",
				publication.Number, publication.WorkItemID, publication.State, publication.Recorded)
		}
	}
}

// printSettlements reports the publications this sweep finished and the ones it
// could not, and says nothing about a record it deliberately left alone. A
// publication the remote still refuses is said on every sweep it stands, because
// it is the state a person has to act on and a sweep that went quiet about it
// would read as one that had settled it.
func printSettlements(stdout, stderr io.Writer, settlements []orchestrator.PublicationSettlement) {
	for _, settlement := range settlements {
		switch {
		case settlement.Failure != "":
			fmt.Fprintf(stderr, "pull request #%d of %s not settled: %s\n", settlement.Number, settlement.WorkItemID, settlement.Failure)
		case settlement.Settled:
			fmt.Fprintf(stdout, "pull request #%d of %s settled: its merge is confirmed on the remote, and nothing about the publication is outstanding\n",
				settlement.Number, settlement.WorkItemID)
			if settlement.Catchup != nil && settlement.Catchup.Advanced {
				fmt.Fprintf(stdout, "  %s caught up to %s\n", settlement.Catchup.TargetBranch, settlement.Catchup.RemoteCommit)
			}
			if settlement.Catchup != nil && settlement.Catchup.Held != "" {
				fmt.Fprintf(stderr, "  %s not caught up: %s\n", settlement.Catchup.TargetBranch, settlement.Catchup.Held)
			}
			if settlement.DocketProblem != "" {
				fmt.Fprintf(stderr, "  docket entry not closed: %s\n", settlement.DocketProblem)
			}
		case settlement.Remaining != "":
			fmt.Fprintf(stderr, "pull request #%d of %s still outstanding: %s\n", settlement.Number, settlement.WorkItemID, settlement.Remaining)
		}
	}
}

// printRedTargets reports each publication waiting on its target's red check:
// what it still waits on, or what the sweep did once that closed. It is said on
// every sweep it stands, because a merge waiting is a merge not landing.
func printRedTargets(stdout, stderr io.Writer, redTargets []orchestrator.RedTargetResumption) {
	for _, redTarget := range redTargets {
		fmt.Fprintf(stdout, "%s (%s): %s\n", redTarget.RunID, redTarget.WorkItemID, redTarget.Action)
		if redTarget.Detail != "" {
			fmt.Fprintf(stdout, "  %s\n", redTarget.Detail)
		}
		if redTarget.Failure != "" {
			fmt.Fprintf(stderr, "  not taken up: %s\n", redTarget.Failure)
		}
	}
}

// printConvergence reports what the sweep changed and what it could not do. A
// repository already level with the forge says nothing here, and neither does a
// branch kept for a good reason: both are the status quo, and a line per target
// and per preserved branch on every sweep would bury the ones that actually need
// reading. `--json` carries the whole sweep either way.
//
// A kept checkout is said, unlike a kept branch, because it is no longer a
// category the sweep declines to act on — the work in one is captured and the
// directory retired — so anything still kept is an anomaly: a directory Git is
// not managing, a registration on a branch the run never recorded, a capture
// that could not be written. Each of those is one line that should not be
// appearing at all, rather than a standing list somebody learns to scroll past.
func printConvergence(stdout, stderr io.Writer, convergence orchestrator.Convergence) {
	for _, target := range convergence.Targets {
		switch {
		case target.Advanced:
			fmt.Fprintf(stdout, "%s caught up to %s\n", target.TargetBranch, target.RemoteCommit)
			if len(target.Discarded) > 0 {
				fmt.Fprintf(stdout, "  discarded export churn: %s\n", strings.Join(target.Discarded, ", "))
			}
		case target.Held != "":
			fmt.Fprintf(stderr, "%s not caught up: %s\n", target.TargetBranch, target.Held)
		}
	}
	// A lifted divergence is said because it is what resumes the line: the
	// watching session held on it chooses again at its next poll.
	for _, lift := range convergence.Divergences {
		switch {
		case lift.Failure != "":
			fmt.Fprintf(stderr, "%s\n", lift.Failure)
		case lift.Lifted != nil:
			fmt.Fprintf(stdout, "%s has converged with the remote's, and the divergence recorded on it since %s is lifted; the line chooses work for it again\n",
				lift.TargetBranch, lift.Lifted.Since.UTC().Format(time.RFC3339))
		}
	}
	if convergence.DivergenceProblem != "" {
		fmt.Fprintf(stderr, "%s\n", convergence.DivergenceProblem)
	}
	// A pull request somebody else had already closed says nothing here, for the
	// reason a branch already gone does: it is the status quo, and this sweep
	// only reports what it changed and what it could not do.
	for _, publication := range convergence.Publications {
		switch {
		case publication.Failure != "":
			fmt.Fprintf(stderr, "pull request #%d not closed: %s\n", publication.Number, publication.Failure)
		case publication.Closed:
			fmt.Fprintf(stdout, "pull request #%d of %s closed: %s superseded it\n",
				publication.Number, publication.WorkItemID, publication.SupersededBy.Vehicle())
		}
	}
	printUnsuperseded(stdout, convergence.Unsuperseded)
	for _, worktree := range convergence.Worktrees {
		switch {
		// A checkout that is gone whose run was not told so is read first, because
		// it is the only one of these where doing nothing leaves somebody being
		// sent after a directory that does not exist.
		case worktree.RecordProblem != "":
			fmt.Fprintf(stderr, "%s retired but not recorded: %s\n", worktree.Path, worktree.RecordProblem)
		// "not swept cleanly" rather than "not retired", because this covers both
		// a retirement that was refused and one that happened and could not be
		// confirmed. The message says which.
		case worktree.Failure != "":
			fmt.Fprintf(stderr, "%s not swept cleanly: %s\n", worktree.Path, worktree.Failure)
		case worktree.Removed:
			fmt.Fprintf(stdout, "%s retired: run %s is settled\n", worktree.Path, worktree.RunID)
			// Where a half-finished change went is the one thing retiring it raises,
			// so it is said next to the retirement rather than left in `--json`.
			if worktree.PreservedWork != "" {
				fmt.Fprintf(stdout, "  uncommitted work preserved at %s\n", worktree.PreservedWork)
			}
		// The run rather than the path, because the reason already names the
		// checkout and the run is how an operator finds what it was for.
		case worktree.Kept != "":
			fmt.Fprintf(stdout, "%s kept: %s\n", worktree.RunID, worktree.Kept)
		}
		// Read outside the switch because it stands beside a retirement that
		// otherwise went perfectly: the work is on the ref either way, and what is
		// missing is the work item saying so — which is the only place the person
		// who picks that item up would find it.
		if worktree.ItemProblem != "" {
			fmt.Fprintf(stderr, "%s not told where the retired work went: %s\n", worktree.WorkItemID, worktree.ItemProblem)
		}
	}
	// The prune says something only when it removed registrations or could not
	// run. A repository with none to remove is the status quo, and a line about
	// it on every sweep is a line nobody reads.
	if failure := convergence.Registrations.Failure; failure != "" {
		fmt.Fprintf(stderr, "stale worktree registrations not pruned: %s\n", failure)
	}
	// A registration an add never finished is said one at a time rather than
	// counted, because until it was cleared it was stopping every new run on the
	// repository at creation, and one that was kept is still doing so.
	for _, unfinished := range convergence.Registrations.Unfinished {
		if unfinished.Cleared {
			fmt.Fprintln(stdout, unfinished.Describe())
		} else {
			fmt.Fprintln(stderr, unfinished.Describe())
		}
	}
	if pruned := len(convergence.Registrations.Pruned); pruned > 0 {
		fmt.Fprintf(stdout, "%d stale worktree registration(s) pruned\n", pruned)
	}
	for _, branch := range convergence.Branches {
		switch {
		// A branch that is gone whose run was not told so is read first, for the
		// reason the same case is among the checkouts: it is the only one here where
		// doing nothing leaves every reader of that run being told its change is
		// preserved on a branch that does not exist.
		case branch.RecordProblem != "":
			fmt.Fprintf(stderr, "%s deleted but not recorded: %s\n", branch.Branch, branch.RecordProblem)
		case branch.Failure != "":
			fmt.Fprintf(stderr, "%s not removed: %s\n", branch.Branch, branch.Failure)
		case branch.Removed:
			fmt.Fprintf(stdout, "%s removed: %s is already in %s\n", branch.Branch, branch.Commit, branch.TargetBranch)
		case branch.ItemProblem != "":
			fmt.Fprintf(stderr, "%s kept, and its item not corrected: %s\n", branch.Branch, branch.ItemProblem)
		case branch.ReleaseCorrected:
			// A release that did not say this branch was standing was read as the run
			// having preserved nothing; the item has now been told otherwise.
			fmt.Fprintf(stdout, "%s kept at %s, and %s told its claim was given back while the branch held the run's change\n",
				branch.Branch, branch.Commit, branch.WorkItemID)
		}
	}
}

// printUnsuperseded names the pull requests still open at the forge that the
// sweep could not close, on one line rather than one each. They are what a
// person decides from — pending work on a preserved branch, or work
// that landed by a vehicle the harness never recorded — and they stay open until
// somebody decides, so a line per request on every sweep would be a standing
// list nobody reads. The one line says how many and which; `--json` carries the
// reason for each under `convergence.unsuperseded`.
func printUnsuperseded(stdout io.Writer, open []orchestrator.OpenPublication) {
	if len(open) == 0 {
		return
	}
	named := make([]string, 0, len(open))
	for _, publication := range open {
		named = append(named, fmt.Sprintf("#%d (%s)", publication.Number, publication.WorkItemID))
	}
	fmt.Fprintf(stdout, "%d open pull request(s) belong to runs that ended without landing, and no later run of their item has landed to supersede them; each is a person's to merge or close: %s\n",
		len(open), strings.Join(named, ", "))
}

func printReconcileUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo reconcile [options]

Settles every run an interrupted process left outstanding, then converges local
state on the forge: each target branch is caught up onto its remote counterpart,
and the leftover branches of settled runs whose work the target already carries
are removed. Both are fast-forward-or-nothing and safe to repeat.

It also retires the leftover checkouts, so the worktree registrations a machine
carries are live runs plus a bounded tail rather than growing with the harness's
history until a command in the next worktree cannot spawn. Settled runs past the
most recent few have their checkout unregistered, and registrations whose
checkout is no longer on disk are pruned, whichever run or person left them
behind. A registration a killed "git worktree add" never finished filling in is
cleared too, and each one is named: Git's own prune never reaches one, and
while it stands every new worktree creation in the repository fails over it.
No branch is touched by any of these.

A checkout holding uncommitted work is retired too, and nothing is lost doing it:
the tree is recorded first on refs/yoyodyne/preserved-work/<run-id>, which the
retirement line names and the run's own record keeps. Recover it with
"git worktree add --detach <path> <ref>". A capture that cannot be written
leaves the checkout exactly where it was.

It also re-asks the forge about the pull request of every run that ended without
its publication being settled, and records what the forge now says — merged,
closed, or still open. A request recorded merged, closed, superseded, or handed
back for a fresh run is never asked about again, and the rest are asked in
batches, one forge query for up to 50 branches; the sweep's last line says how
many it asked about and how long the forge took. Nothing is merged for you: the
record is brought onto the truth, so what reads it afterwards reads truth too.

It then closes the pull requests whose work landed by another vehicle: a run
that ended without integrating, whose item a later run of this harness did
integrate — a relaunch after a killed run, the loser of a duplicate selection,
a re-run triage decided. Each is closed with a comment naming the pull request
or commit the work actually landed by, the remote branch it published is
deleted, and the supersession is recorded on the run so no later sweep asks
again. A request that merged is never touched. What is left open and cannot be
closed on the harness's own records — no run of its item has landed — is named
on one line, so what is left open is a list a person decides from; `+"`--json`"+` carries
the reason for each under convergence.unsuperseded.

A publication recorded as merged and unfinished — a merge that could not be
confirmed when it landed, a dropped merge somebody then made by hand, a consumed
branch that could not be deleted — is then finished where the remote now
confirms it: the merge commit is recorded, the local target caught up, the item
settled by its own landing, and the hold, the heartbeat's count, and the docket
entry it carried all clear together. One the remote still refuses stays
outstanding and says so.

It then builds the triage docket: the runs that ended on a durable blocker and
the approved publications the forge has not merged, put where the development
manager reads them. Docketing is keyed to what stopped, so sweeping twice
dockets nothing twice. Every entry standing for an item the tracker holds as
closed is closed with its item, with the reason, and the count is reported; an
unfinished publication's entry is left, since its merge can still be
outstanding after the item closed.

It also recovers the exchanges the roles have put to each other. Each one is
taken under its own lease, so one a live process is carrying is left alone: a
round a dead process asked and never got an answer to is closed saying so, and a
thread that has spent every round it was given is closed as unresolved and the
operator told. Nothing is put in front of a role here — recovering from a lost
process is never a reason to ask a question nobody asked for.

Its last reading is whether anything is happening at all. When nothing has started for
--stall-after, the tracker reports work ready, and no hold, no still-moving run
and no provider usage window accounts for it, that is recorded against the
product as a stall — which `+"`yoyo status`"+` reads back afterwards and the Slack sink,
if one is running, takes to the operators once. It is here because this sweep runs
whether or not reporting was ever turned on: how promptly a stopped harness is
noticed is this threshold and how often whatever runs this sweep does, so an
unattended pass should run at least as often as the threshold it sets.

After all of that, it continues the runs that exited on their in-process
usage-limit bound and whose recorded deadline has since passed with no process
serving the wait — each in its own worktree and developer session, as
`+"`yoyo run <item>`"+` would, with the run's record saying the sweep did it. This
process hosts each continued run to its end, so the command stays open for as
long as those runs take and says which run it is continuing on standard error
before it does; with --json the one document is written once they have
finished. A run whose deadline has not passed, and one a live process holds,
are left exactly as they are.

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --json             emit machine-readable JSON
  --stall-after <d>  how long nothing may start over ready work, with nothing
                     accounting for it, before that is recorded as a stall
                     (default 10m)`)
}

func printReconcileFinding(stdout, stderr io.Writer, finding *readmodel.Attention, problem string) {
	if finding != nil {
		fmt.Fprintf(stdout, "  settlement finding: %s — %s\n", finding.CitedWhat(), finding.CitedWhose())
	}
	if problem != "" {
		fmt.Fprintf(stderr, "  finding not recorded: %s\n", problem)
	}
}

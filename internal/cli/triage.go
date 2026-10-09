package cli

// Carrying out what triage decided.
//
// The development manager decides what becomes of work that stopped and records
// the decision on the work item; the harness is what acts on one. Three of those
// actions exist. Two are the opposite answers to a run that stopped: a re-run,
// which is what a correct change whose ground moved needs, and a repair, which
// continues the run that stopped on the change it already has. The third is
// about the other thing that stops — an approved change the forge queued a merge
// for and then dropped — and it repeats that merge request, once per publication.
//
// Neither of the first two waits on being typed. The scheduling pass fires a
// recorded repair or re-run itself, as many per pull as it has slots for,
// through exactly those two actions — see orchestrator/carryout.go — so what
// those verbs are for is firing one now rather than at the next pass, and for a
// harness where nothing is watching the queue. A recorded re-arm is fired by the
// pass as well, on its own path since it is a merge request rather than a run —
// see orchestrator/carryrearm.go — so the third verb is for firing one now too.
//
// The decision is not made here and cannot be. What each takes is the run the
// docket entry names, and what it does with it is the harness's own work —
// reading the intake hold, proving the stoppage is over, and then either
// claiming the one re-run that stoppage gets and starting a fresh run, spending
// the item's repair grant, superseding the blocker, and continuing the run that
// stopped, or taking the target branch's promotion lease and asking the forge
// for the identical merge the reviewer's verdict already authorized.
//
// A re-run takes nothing else. The reasoning it records as why the fresh run
// exists is read from the decision the development manager's conversation wrote
// to the item's durable triage record, so the attribution the run carries names
// a role that really wrote those words and cites where. A reason this command
// took as a flag was one anybody at a terminal could put in that role's mouth.

// The fourth verb here is not one of those and carries nothing out. The three
// require a decision recorded against the item's durable triage budget, and the
// caps that bound those budgets refused the recording as well as the carrying
// out -- so an item at the end of its rounds was unrunnable by every recorded
// path, escalation included, and the operator's ruling on one had to be executed
// by admitting fresh work instead. `yoyo triage override` is the recorded path
// that was missing: the operator crosses one of the item's caps, in their own
// name and with their reason, and the guards that refused the decision then
// permit it. It is the operator's hand and nothing else's, which is why it is a
// terminal command rather than a word in any role's vocabulary.
//
// The fifth, `yoyo triage resume`, carries out no decision either, because there
// is none to carry out: the change was approved, and what stopped it short of
// the target branch was the environment -- a dirty primary checkout, a tracker
// or a forge that did not answer. The run resumes at the promotion it stopped in
// with its approval standing, and it charges the item nothing: no review round,
// no repair grant, no re-run. Before it existed every verb here spent one of
// those for such a stop, and four operator overrides on one approved change
// paid for the environment rather than a verdict (yoyodyne-ifd.394).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

type triageOutput struct {
	Rerun    *orchestrator.RerunResult             `json:"rerun,omitempty"`
	Repair   *orchestrator.RepairContinueResult    `json:"repair,omitempty"`
	Rearm    *orchestrator.RearmResult             `json:"rearm,omitempty"`
	Resume   *orchestrator.IntegrationResumeResult `json:"resume,omitempty"`
	Override *triageOverrideResult                 `json:"override,omitempty"`
	Error    string                                `json:"error,omitempty"`
}

// triageOverrideResult is what an override came to: the decision as it was
// recorded, the item's counters with it on them, and the ceilings the item now
// stands under. The caps are reported beside the record rather than left to be
// worked out from it, because what an operator wants to see is what the guards
// will now permit -- which is the configured caps as every override on the item
// leaves them, not only the one just typed.
type triageOverrideResult struct {
	WorkItemID string                  `json:"work_item_id"`
	Recorded   runstate.TriageOverride `json:"recorded"`
	Caps       runstate.TriageCaps     `json:"caps"`
	Counters   runstate.TriageCounters `json:"counters"`
}

func runTriage(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printTriageUsage(stdout)
		return 0
	}
	switch args[0] {
	case "rerun":
		return rerunStoppage(ctx, args[1:], stdout, stderr)
	case "repair":
		return repairStoppage(ctx, args[1:], stdout, stderr)
	case "rearm":
		return rearmPublication(ctx, args[1:], stdout, stderr)
	case "resume":
		return resumeIntegration(ctx, args[1:], stdout, stderr)
	case "override":
		return overrideTriageCap(ctx, args[1:], stdout, stderr)
	case "show":
		return showStoppage(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown triage command %q\n\n", args[0])
		printTriageUsage(stderr)
		return 2
	}
}

// showStoppage prints live docket entries whole, as the development manager's
// docket shows an entry it has room for. It is the command an entry the docket
// had no room to show whole names, so it matches what that line names: the run,
// or the entry's key where nothing ran, and also the work item, which prints
// every live entry on it. It decides nothing and carries nothing out, but it
// does bring the docket up to date before printing, as the conversation's own
// reading does: a stoppage no entry recorded yet is recorded then.
func showStoppage(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		fmt.Fprintln(stderr, "triage show requires exactly one run identifier, docket entry key, or work item identifier")
		printTriageUsage(stderr)
		return 2
	}
	named := strings.TrimSpace(positional[0])
	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if parts.docket == nil {
		fmt.Fprintln(stderr, "this product keeps no triage docket, so there is no entry to show")
		return 1
	}
	built, buildErr := docketerFrom(parts).Build()
	listed := built.Listed()
	if buildErr != nil && len(listed) == 0 {
		fmt.Fprintf(stderr, "the triage docket could not be read: %v\n", buildErr)
		return 1
	}
	if buildErr != nil {
		fmt.Fprintf(stderr, "the triage docket could only be built in part, so an entry it names may be missing: %v\n", buildErr)
	}
	var matched []triage.Entry
	for _, entry := range listed {
		if entry.Key == named || entry.RunID == named || entry.WorkItemID == named {
			matched = append(matched, entry)
		}
	}
	if len(matched) == 0 {
		fmt.Fprintf(stderr, "no live docket entry is on %s: an entry somebody decided, or whose work item is closed, is not live\n", named)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, struct {
			Entries []triage.Entry `json:"entries"`
		}{matched})
	}
	for index, entry := range matched {
		if index > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprint(stdout, entry.Render())
	}
	return 0
}

func rerunStoppage(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage rerun", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "triage rerun requires exactly one run identifier, the run the docket entry names")
		printTriageUsage(stderr)
		return 2
	}

	rerunner, err := buildRerunner(*configPath)
	if err != nil {
		return reportRerun(stdout, stderr, *jsonOutput, orchestrator.RerunResult{}, err)
	}
	// The run and nothing else. The reasoning is read from the decision the
	// development manager recorded rather than typed here: a reason this command
	// accepted would be recorded as that role's, and nothing would have checked it
	// against anything they wrote.
	result, err := rerunner.Rerun(ctx, orchestrator.RerunRequest{Run: positional[0]})
	if err == nil {
		noteHandStepFor(*configPath, stderr, handStep{
			kind:  intervention.KindRerun,
			items: []string{result.WorkItemID},
			run:   positional[0],
			said:  "started a re-run of " + positional[0] + " with yoyo triage rerun",
		})
	}
	return reportRerun(stdout, stderr, *jsonOutput, result, err)
}

func repairStoppage(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage repair", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "triage repair requires exactly one run identifier, the run the docket entry names")
		printTriageUsage(stderr)
		return 2
	}

	continuer, err := buildRepairContinuer(*configPath)
	if err != nil {
		return reportRepair(stdout, stderr, *jsonOutput, orchestrator.RepairContinueResult{}, err)
	}
	// The run and nothing else, as a re-run takes. The decision, its grant, and the
	// reasoning are read from what the development manager recorded rather than
	// typed here: a reason this command accepted would be recorded on the run and
	// the item as that role's, and nothing would have checked it against anything
	// they wrote.
	result, err := continuer.Continue(ctx, orchestrator.RepairContinueRequest{Run: positional[0]})
	if err == nil {
		noteHandStepFor(*configPath, stderr, handStep{
			kind:  intervention.KindRepair,
			items: []string{result.WorkItemID},
			run:   positional[0],
			said:  "started a repair of " + positional[0] + " with yoyo triage repair",
		})
	}
	return reportRepair(stdout, stderr, *jsonOutput, result, err)
}

func rearmPublication(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage rearm", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	reason := flags.String("reason", "", "the development manager's recorded reasoning for deciding a re-arm")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "triage rearm requires exactly one run identifier, the run whose publication the docket entry names")
		printTriageUsage(stderr)
		return 2
	}

	rearmer, err := buildRearmer(*configPath)
	if err != nil {
		return reportRearm(stdout, stderr, *jsonOutput, orchestrator.RearmResult{}, err)
	}
	result, err := rearmer.Rearm(ctx, orchestrator.RearmRequest{Run: positional[0], Reason: *reason})
	if err == nil {
		noteHandStepFor(*configPath, stderr, handStep{
			kind:  intervention.KindRearm,
			items: []string{result.WorkItemID},
			run:   positional[0],
			said:  "asked the forge again for the merge of " + positional[0] + " with yoyo triage rearm",
		})
	}
	return reportRearm(stdout, stderr, *jsonOutput, result, err)
}

func resumeIntegration(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage resume", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	reason := flags.String("reason", "", "reasoning to record beside the harness's own account of the stop (optional)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "triage resume requires exactly one run identifier, the run the docket entry names")
		printTriageUsage(stderr)
		return 2
	}

	resumer, err := buildIntegrationResumer(*configPath)
	if err != nil {
		return reportResume(stdout, stderr, *jsonOutput, orchestrator.IntegrationResumeResult{}, err)
	}
	result, err := resumer.Resume(ctx, orchestrator.IntegrationResumeRequest{Run: positional[0], Reason: *reason})
	if err == nil {
		noteHandStepFor(*configPath, stderr, handStep{
			kind:  intervention.KindResume,
			items: []string{result.WorkItemID},
			run:   positional[0],
			said:  "resumed the integration of " + positional[0] + " with yoyo triage resume",
		})
	}
	return reportResume(stdout, stderr, *jsonOutput, result, err)
}

// buildIntegrationResumer wires the resume action over the same parts the repair
// beside it acts on: the docket it reads and settles, the runs it proves the
// stoppage from, the worktree it proves the change from, and the pipeline it
// continues. No triage budget is wired, because the resumption spends none.
func buildIntegrationResumer(configPath string) (orchestrator.IntegrationResumer, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.IntegrationResumer{}, err
	}
	return orchestrator.IntegrationResumer{
		Docket: parts.docket,
		Runs:   parts.store,
		Intake: parts.intake,
		// The item the stopped run holds, and the checkout and worktree a promotion
		// is made from. All three are read before anything is written: the item
		// because a closed one is not one a run may be resumed on, and the checkout
		// because it is what stopped the run once already.
		Items:     parts.tracker(),
		Worktrees: parts.worktrees,
		Remains:   parts.worktrees,
		// The same limit the reservation enforces, read before the run is made live
		// so a full harness leaves it stopped rather than live with no room to go.
		Capacity: parts.config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
			// The same entry point a repair takes, and for the same reason: what is
			// dispatched is this run's promotion and nothing else, and a fresh run
			// cannot satisfy it.
			return pipelineFrom(parts).Continue(ctx, workItemID, runID)
		},
	}, nil
}

// checkStageContinuerFrom wires the harness's own continuation of a check stage
// its bound stopped over parts that are already built: the docket it reads and
// settles, the runs it proves the stoppage from, the worktree it proves the
// change from, the machine's load it waits on, and the pipeline it continues.
// Like the resumption of an approved change, it is wired with no triage budget,
// because it spends none.
func checkStageContinuerFrom(parts components) orchestrator.CheckStageContinuer {
	return orchestrator.CheckStageContinuer{
		Docket: parts.docket,
		// A continuation the worktree refuses is handed back to the development
		// manager on the same docket every other stoppage reaches her on.
		Redocket:  docketerFrom(parts),
		Runs:      parts.store,
		Intake:    parts.intake,
		Items:     parts.tracker(),
		Worktrees: parts.worktrees,
		// Supplied for compatibility; continuations, like fresh work, do not
		// wait for machine load to fall.
		Load:     gitworktree.MachineLoad,
		Capacity: parts.config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
			return pipelineFrom(parts).Continue(ctx, workItemID, runID)
		},
	}
}

// stallContinuerFrom wires the harness's own continuation of a first
// silent-stream stall over parts that are already built, as the check stage's
// is: the docket it reads and settles, the runs it proves the stall from, the
// worktree it proves the change from, and the pipeline it continues. It is
// wired with no triage budget, because it spends none.
func stallContinuerFrom(parts components) orchestrator.StallContinuer {
	return orchestrator.StallContinuer{
		Docket:    parts.docket,
		Redocket:  docketerFrom(parts),
		Runs:      parts.store,
		Intake:    parts.intake,
		Items:     parts.tracker(),
		Worktrees: parts.worktrees,
		Capacity:  parts.config.Execution.MaxConcurrentDevelopers,
		Backends:  developerBackendsFrom(parts),
		Events:    parts.store,
		Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
			return pipelineFrom(parts).Continue(ctx, workItemID, runID)
		},
	}
}

// reportResume describes what the action did. A refusal before anything was
// written, an intake hold, a full harness, and a resumption whose run then
// stopped again are four different things for an operator to do something about.
func reportResume(stdout, stderr io.Writer, jsonOutput bool, result orchestrator.IntegrationResumeResult, err error) int {
	if jsonOutput {
		output := triageOutput{}
		if result.WorkItemID != "" || result.RunID != "" {
			output.Resume = &result
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	if !result.Resumed {
		if result.IntakeHeld != nil || result.CapacityFull != nil {
			fmt.Fprint(stdout, result.Render())
			return 0
		}
		fmt.Fprintf(stderr, "the integration was not resumed and nothing was written: %v\n", err)
		switch {
		case errors.Is(err, orchestrator.ErrCheckoutNotReady):
			fmt.Fprintln(stderr, "the primary checkout is what stopped this run; commit or stash what it carries and ask again, and the same run resumes")
		case errors.Is(err, orchestrator.ErrNotResumable):
			fmt.Fprintln(stderr, "a resumption is for an approved change the environment stopped short of its promotion; `yoyo triage repair` and `yoyo triage rerun` are what a change that was not approved needs")
		case errors.Is(err, orchestrator.ErrWorktreeNotAsLeft):
			fmt.Fprintln(stderr, "nothing was spent and the run is still stopped: say what became of that worktree before its integration is resumed")
		case errors.Is(err, orchestrator.ErrPreservedChangeMissing):
			fmt.Fprintln(stderr, "nothing was spent and the run is still stopped: the run's branch is where the approved change is, so put that worktree back on the change before its integration is resumed")
		}
		return 1
	}
	fmt.Fprint(stdout, result.Render())
	// The resumed run reports itself exactly as `yoyo run` reports one, because it
	// is the same run: what it integrated and what its agents reported are the
	// same facts however the run was picked up again.
	code := reportRunResult(stdout, stderr, false, result.Outcome, err)
	if result.RecordProblem != "" && code == 0 {
		return 1
	}
	return code
}

// buildRearmer wires the re-arm action over the same parts every other command
// acts on, so the docket it reads, the runs it proves the publication from, the
// forge it asks, and the pre-merge check it makes are the ones the rest of the
// harness uses.
func buildRearmer(configPath string) (orchestrator.Rearmer, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Rearmer{}, err
	}
	return rearmerFrom(parts), nil
}

// rearmerFrom wires the re-arm action over parts that are already built, for the
// verb and for the watch's carry-out alike, so the two make the one request.
func rearmerFrom(parts components) orchestrator.Rearmer {
	// The same forge access the run's own merge was made through and the same
	// reconciliation asks what became of one, so what repeats a request and what
	// opened it speak to the same repository.
	forge := publish.GitHub{
		Runner:       parts.runner,
		Dir:          parts.repository,
		Remote:       parts.config.Execution.Remote,
		PushRemote:   parts.config.Execution.PushRemote,
		RedactValues: parts.redactValues,
	}
	return orchestrator.Rearmer{
		Docket: parts.docket,
		Runs:   parts.store,
		Forge:  forge,
		// The reading the queued-merge sweep takes of a request's checks, which is
		// what gates arming a request nothing ever asked the forge to merge.
		Checks: forge,
		// The same manager the run's own merge checked the remote target with, so
		// the check that gated the original merge and the check that gates its
		// repeat are one thing rather than two.
		Worktrees: parts.worktrees,
		// The same per-item counters the development manager's decision spends and
		// `yoyo status` reports, so what proves the decision was made and what an
		// operator reads about it can never be two different records.
		Decisions: parts.store.Triage(),
		// Whether the items a merge withdrawn for its target's red check waits
		// on are closed, which is what authorizes the harness to arm it again.
		Items: parts.tracker(),
	}
}

// reportRearm describes what the action did. There are three outcomes and an
// operator does something different about each: a refusal before anything was
// spent, a repeat the forge then would not take, and a request it took.
//
// The middle one is why this does not branch on the repeat alone. The re-arm is
// spent by recording it, which happens before the forge is asked, so a request
// the forge refused leaves the publication's one re-arm gone and no merge
// pending — and reporting that as "no merge was asked for" beside an error
// saying it was spent tells an operator two opposite things about the budget
// they are about to decide against. The count is what tells them apart, because
// it is written before the request and stands whatever the request came to.
func reportRearm(stdout, stderr io.Writer, jsonOutput bool, result orchestrator.RearmResult, err error) int {
	if jsonOutput {
		output := triageOutput{}
		if result.WorkItemID != "" || result.RunID != "" {
			output.Rearm = &result
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	if !result.Rearmed {
		if result.Rearms > 0 {
			fmt.Fprintf(stderr, "the merge request for pull request %d was not taken by the forge, and the publication's re-arm is spent: %v\n",
				result.Number, err)
			fmt.Fprintln(stderr, "the re-arm is recorded whatever the forge answered, so this publication has none left; a further drop is an escalation rather than another re-arm")
			return 1
		}
		fmt.Fprintf(stderr, "the re-arm was refused and no merge was asked for: %v\n", err)
		if errors.Is(err, runstate.ErrTriageCapReached) {
			fmt.Fprintln(stderr, "triage repeats one publication's merge request once; a second drop is an escalation rather than a larger budget")
		}
		return 1
	}
	fmt.Fprint(stdout, result.Render())
	if result.RecordProblem != "" {
		return 1
	}
	return 0
}

// overrideTriageCap records the operator's decision to cross one of a work
// item's triage caps.
//
// It carries nothing out and starts nothing, which is deliberate and is what
// keeps the caps meaning what they say. What it changes is what the guards will
// permit next: the development manager can then record the decision their
// escalation was about, and the carry-out -- the scheduling pass, or either verb
// beside this one -- acts on that decision under every condition it already asks.
// The development manager still has to record it, which is one more step and the
// right one: crossing a cap and spending it are two decisions.
func overrideTriageCap(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage override", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	budget := flags.String("budget", runstate.TriageReviewRoundBudget,
		"the budget to cross: "+strings.Join(runstate.TriageOverrideBudgets(), ", "))
	ceiling := flags.Int("cap", -1, "the ceiling to raise the budget to")
	cleared := flags.Bool("clear", false, "lift the budget entirely rather than raising it to a number")
	by := flags.String("by", "", "the operator deciding this")
	reason := flags.String("reason", "", "why this item's cap is being crossed")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "triage override requires exactly one work item identifier, the item whose cap is being crossed")
		printTriageUsage(stderr)
		return 2
	}
	// A cleared budget has no number and a raised one has nothing else, so the two
	// are refused together rather than one being quietly preferred: a record
	// carrying both would leave its next reader to guess which the guards obey.
	switch {
	case *cleared && *ceiling >= 0:
		fmt.Fprintln(stderr, "give one of --cap or --clear: a cleared budget states no ceiling, and an override carrying both says two different things about what the guards permit")
		return 2
	case !*cleared && *ceiling < 0:
		fmt.Fprintln(stderr, "triage override requires --cap <n> or --clear: an override that names no new ceiling gives the item no more room and would change nothing")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportTriageOverride(stdout, stderr, *jsonOutput, triageOverrideResult{}, err)
	}
	workItemID := positional[0]
	caps := orchestrator.TriageCaps(parts.config.Execution, parts.config.Triage)
	// The budget is trimmed here as well as where it is recorded, because it is
	// also what this reads the written override back by: a name the store trimmed
	// and this did not would find nothing and report an override as unrecorded.
	recorded := runstate.TriageOverride{
		Budget:    strings.TrimSpace(*budget),
		Cleared:   *cleared,
		DecidedBy: *by,
		Reason:    *reason,
	}
	if !*cleared {
		recorded.Cap = *ceiling
	}
	counters, err := parts.store.Triage().Override(ctx, workItemID, recorded, time.Now().UTC(), caps)
	if err != nil {
		return reportTriageOverride(stdout, stderr, *jsonOutput, triageOverrideResult{WorkItemID: workItemID}, err)
	}
	// What the item now stands under is read back off the record that was just
	// written rather than assembled from what was typed, so the figures reported
	// and the figures the guards will read are one thing.
	result := triageOverrideResult{
		WorkItemID: workItemID,
		Caps:       caps.Overridden(counters.Overrides),
		Counters:   counters,
	}
	if latest, found := counters.OverrideOf(recorded.Budget); found {
		result.Recorded = latest
	}
	noteHandStep(parts.interventions, parts.config.Product.ID, stderr, handStep{
		kind:  intervention.KindOverride,
		items: []string{workItemID},
		said:  "crossed the " + recorded.Budget + " cap on " + workItemID + " with yoyo triage override",
	})
	return reportTriageOverride(stdout, stderr, *jsonOutput, result, nil)
}

// reportTriageOverride describes what the override did. A refusal is reported as
// one and says nothing was recorded, because an operator who thinks a cap was
// crossed and finds the next decision refused is back in the deadlock this verb
// exists to end.
func reportTriageOverride(stdout, stderr io.Writer, jsonOutput bool, result triageOverrideResult, err error) int {
	if jsonOutput {
		output := triageOutput{}
		if result.WorkItemID != "" && err == nil {
			output.Override = &result
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "the override was refused and nothing was recorded: %v\n", err)
		if errors.Is(err, runstate.ErrTriageOverrideNotARaise) {
			fmt.Fprintln(stderr, "`yoyo status <beads-id>` says what the item's budgets already stand at, overrides included")
		}
		return 1
	}
	fmt.Fprintf(stdout, "recorded an operator override on %s: %s\n", result.WorkItemID, result.Recorded.Describe())
	printItemTriage(stdout, result.Counters, result.Caps)
	fmt.Fprintln(stdout, "nothing was started, granted, or spent: an override changes what the guards permit, not what has happened")
	fmt.Fprintln(stdout, "the development manager can now record the decision the escalation was about, and a watching `yoyo work` session carries it out on its next pull under every condition it already asks; `yoyo triage rerun` or `yoyo triage repair` fires it now instead")
	return 0
}

// buildRepairContinuer wires the repair-continue action over the same parts the
// re-run beside it acts on, so the docket it reads, the runs it proves the
// stoppage from, and the pipeline it continues are the ones the rest of the
// harness uses.
func buildRepairContinuer(configPath string) (orchestrator.RepairContinuer, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.RepairContinuer{}, err
	}
	return repairContinuerFrom(parts), nil
}

// repairContinuerFrom is the same wiring over parts somebody else built, which is
// what the scheduling pass needs: a pull assembles its parts once from one
// reading of the configuration, and a carry-out that built its own would act on a
// configuration this pull never read.
func repairContinuerFrom(parts components) orchestrator.RepairContinuer {
	return orchestrator.RepairContinuer{
		Docket: parts.docket,
		Runs:   parts.store,
		Intake: parts.intake,
		// The same per-item counters the development manager's decision spends and
		// `yoyo status` reports, which is what says a grant was made and how much
		// of it the round cap left. What proves the decision and what an operator
		// reads about it can never be two different records.
		Decisions: parts.store.Triage(),
		// The budget the grant is added to, which is the operator's number rather
		// than this action's.
		ConfiguredAttempts: parts.config.Execution.RepairAttemptsBeforeReplan,
		// The item the stopped run blocked, and the worktree it stopped in. Both
		// are read before anything is spent: the item because a blocked one is not
		// one the pipeline resumes, and the worktree because what a continued
		// developer is handed back is whatever is in it.
		Items:     parts.tracker(),
		Worktrees: parts.worktrees,
		// The same repository the docket and the pull's hold look in, so the
		// refusal of an integration stop names the resume only while the branch
		// the resume needs is there.
		Remains: parts.worktrees,
		// The same limit the reservation enforces, read before the grant so a full
		// harness leaves the decision standing rather than spending the item's
		// grant on a run there is no room to continue.
		Capacity: parts.config.Execution.MaxConcurrentDevelopers,
		// The backend the stopped run's developer worked on, asked before the
		// grant is spent, and the run's event log, which a session an earlier
		// failed attempt erased from its record is restored from.
		Backends: developerBackendsFrom(parts),
		Events:   parts.store,
		Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
			// The continuation names the run it re-enters, and takes the entry
			// point that can do nothing else: a repair is worth the change one
			// stopped run preserved, and every recorded loss of one was a dispatch
			// that started something fresh in its place. No selection is stamped,
			// because this continues the run that was already reserved for this
			// item and why that run exists was recorded when it was. Why it is
			// going again is on the run and the item already.
			return pipelineFrom(parts).Continue(ctx, workItemID, runID)
		},
	}
}

// reportRepair describes what the action did. A refusal before anything was
// spent, an intake hold, a full harness, and a continuation whose run then
// failed are four different things for an operator to do something about.
func reportRepair(stdout, stderr io.Writer, jsonOutput bool, result orchestrator.RepairContinueResult, err error) int {
	if jsonOutput {
		output := triageOutput{}
		if result.WorkItemID != "" || result.RunID != "" {
			output.Repair = &result
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	if !result.Continued {
		// A hold and a full harness are states the carry-out is waiting on rather
		// than failures: nothing was spent, and the decision stands to be carried
		// out by asking again.
		if result.IntakeHeld != nil || result.CapacityFull != nil {
			fmt.Fprint(stdout, result.Render())
			return 0
		}
		fmt.Fprintf(stderr, "the repair continuation was not confirmed: %v\n", err)
		if errors.Is(err, orchestrator.ErrWorktreeNotAsLeft) {
			fmt.Fprintln(stderr, "nothing was spent and the item is still blocked: say what became of that worktree before the run is continued")
		}
		// The twin refusal, and the one an operator can most easily act on: the
		// worktree is the harness's own and empty, so the change is on the branch
		// the run recorded if it survived at all.
		if errors.Is(err, orchestrator.ErrPreservedChangeMissing) {
			fmt.Fprintln(stderr, "nothing was spent and the item is still blocked: the run's branch is where the preserved change is, so put that worktree back on the change before the run is continued")
		}
		return 1
	}
	fmt.Fprint(stdout, result.Render())
	// The continued run reports itself exactly as `yoyo run` reports one, because
	// it is the same run: what it integrated and what its agents reported are the
	// same facts however the run was picked up again.
	return reportRunResult(stdout, stderr, false, result.Outcome, err)
}

// buildRerunner wires the re-run action over the same parts every other command
// acts on, so the docket it reads, the runs it proves the stoppage from, and the
// pipeline it starts are the ones the rest of the harness uses.
func buildRerunner(configPath string) (orchestrator.Rerunner, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Rerunner{}, err
	}
	return rerunnerFrom(parts), nil
}

// rerunnerFrom is the same wiring over parts somebody else built; see
// repairContinuerFrom for why the scheduling pass needs it that way round.
func rerunnerFrom(parts components) orchestrator.Rerunner {
	return orchestrator.Rerunner{
		Docket: parts.docket,
		Runs:   parts.store,
		Intake: parts.intake,
		Reruns: parts.store.Reruns(),
		// The same per-item counters the development manager's decision spends and
		// `yoyo status` reports, so what proves the decision was made and what an
		// operator reads about it can never be two different records.
		Decisions: parts.store.Triage(),
		// The item itself, read before the stoppage's one re-run is claimed. It is
		// the same tracker the fresh run starts on, so what refuses here is exactly
		// what would otherwise have refused past the claim.
		Items: parts.tracker(),
		// The human acts on the record, from the same store every other surface
		// reads them from, so a step the operator reserved refuses the re-run
		// before the claim exactly as it refuses the scheduler's pull.
		Gates: parts.store,
		// The same limit the reservation enforces, read before the claim so a full
		// harness leaves the decision standing rather than spending the stoppage's
		// re-run on a run that would find no slot.
		Capacity: parts.config.Execution.MaxConcurrentDevelopers,
		// What the stopped run preserved is retired through the same manager that
		// created it, which is what keeps the removal inside the ownership rules
		// every other removal here is held to.
		Preserved: parts.worktrees,
		Remains:   parts.worktrees,
		// The pull request it published is retired through the same forge client
		// the sweep uses, so a publication closed at the moment of triage and one
		// closed by a later `yoyo reconcile` are closed the same way and say the
		// same thing.
		Publications: publish.GitHub{
			Runner:       parts.runner,
			Dir:          parts.repository,
			Remote:       parts.config.Execution.Remote,
			PushRemote:   parts.config.Execution.PushRemote,
			RedactValues: parts.redactValues,
		},
		Start: func(ctx context.Context, workItemID string, selection runstate.Selection) (orchestrator.Outcome, error) {
			// The pipeline is a value, so the run this starts carries its own
			// selection: the development manager's decision, which is also what
			// makes the intake hold apply to it.
			pipeline := pipelineFrom(parts)
			pipeline.Selection = selection
			return pipeline.Run(ctx, workItemID)
		},
	}
}

// carryOutFrom wires the firing of what the development manager decided over
// parts that are already built, so the docket it reads, the record it reads her
// decisions from, and the two actions it fires them through are the ones the rest
// of the harness uses.
//
// Both actions are wired, because both halves of her vocabulary are hers to
// decide and neither is anybody's to type. Nothing else is added: every gate this
// is held to is the gate one of those actions already asks, and the pause it reads
// itself is the same switch every provider invocation reads.
func carryOutFrom(parts components) *orchestrator.CarryOut {
	return &orchestrator.CarryOut{
		Docket: parts.docket,
		// The same per-item record her conversation writes the decision to, so what
		// authorizes the firing and what is read to fire it are one record.
		Decisions: parts.store.Triage(),
		Notes:     parts.tracker(),
		// What has already been carried out of those decisions, in the two shapes it
		// takes: a claimed re-run, and a continuation recorded on one of the item's
		// own runs.
		Reruns:   parts.store.Reruns(),
		Runs:     parts.store,
		Rerunner: rerunnerFrom(parts),
		Repairer: repairContinuerFrom(parts),
		// A re-arm decided about a request nothing ever asked the forge to merge,
		// made through the same action `yoyo triage rearm` makes it through, with
		// the same checks reading gating it.
		Rearmer: rearmerFrom(parts),
		// The items a merge withdrawn for its target's red check waits on, read so
		// the harness arms it again once they close and not before.
		Items: parts.tracker(),
		// The one thing fired here that nobody decided: a check stage its bound
		// stopped, continued by the harness at its checks on the change the run
		// already has. It spends nothing, so no triage budget is wired to it.
		CheckStages: checkStageContinuerFrom(parts),
		// The other: a first silent-stream stall, continued by the harness once in
		// the session and at the phase it stalled in. It spends nothing either.
		Stalls: stallContinuerFrom(parts),
		// The same pause every run and every turn reads. A carry-out spends on a
		// provider, so `yoyo pause` covers it exactly as it covers them — and it is
		// read here rather than left to the pipeline because a repair writes to the
		// item before it starts anything.
		Holds: parts.holds,
	}
}

// reportRerun describes what the action did. A re-run that was refused before
// anything was claimed, one intake held, one whose fresh run met a pause where it
// would have started, and one whose fresh run then failed are four different
// things for an operator to do something about, so none of them is reported as
// any of the others.
func reportRerun(stdout, stderr io.Writer, jsonOutput bool, result orchestrator.RerunResult, err error) int {
	if jsonOutput {
		output := triageOutput{}
		if result.WorkItemID != "" || result.PriorRunID != "" {
			output.Rerun = &result
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	if !result.Started {
		// A hold and a full harness are both states the carry-out is waiting on
		// rather than failures: nothing was claimed, and the decision stands to be
		// carried out by asking again. A record that could not be written while
		// waiting is a failure, because the stoppage has then paid for a wait that
		// was meant to cost it nothing.
		if result.IntakeHeld != nil || result.CapacityFull != nil {
			fmt.Fprint(stdout, result.Render())
			if result.RecordProblem != "" {
				return 1
			}
			return 0
		}
		// A pause the fresh run met where it would have started is the third such
		// state: nothing was reserved, the claim was given back, and what lifts the
		// pause is the same thing that lifts it for any other work. So the accounting
		// is reported here and the pause itself exactly as `yoyo run` reports one,
		// rather than in words this command would have to keep in step with those.
		if result.PausedBeforeStarting != nil {
			fmt.Fprint(stdout, result.Render())
			code := reportRunResult(stdout, stderr, false, *result.PausedBeforeStarting, err)
			if result.RecordProblem != "" {
				return 1
			}
			return code
		}
		// Nothing was started and intake is not held, so the action refused, and
		// a refusal always says why: it is the only way out of that path.
		fmt.Fprintf(stderr, "the re-run was refused and nothing was started: %v\n", err)
		if errors.Is(err, runstate.ErrRerunTaken) {
			fmt.Fprintln(stderr, "triage acts on one docketed stoppage once; a second is an escalation rather than a larger budget")
		}
		return 1
	}
	fmt.Fprint(stdout, result.Render())
	// The fresh run reports itself exactly as `yoyo run` reports one, because it
	// is the same run: what it integrated, what it preserved, and what its agents
	// reported are the same facts however the run was started.
	return reportRunResult(stdout, stderr, false, result.Outcome, err)
}

func printTriageUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo triage rerun    [options] <run-id>
       yoyo triage repair   [options] <run-id>
       yoyo triage rearm    [options] <run-id>
       yoyo triage resume   [options] <run-id>
       yoyo triage override [options] <beads-id>
       yoyo triage show     [options] <run-id | docket entry key | beads-id>

"rerun", "repair", and "rearm" carry out a decision the development manager
recorded about a docketed entry. "resume" carries out no decision, because the
stoppage it answers asks for none: an approved change the environment stopped
short of the target branch. "override" is yours rather than theirs: it crosses
one of a work item's triage caps so that a decision they could not record
becomes one they can.

A watching "yoyo work" session fires a recorded repair, re-run, or re-arm
itself, through the same three actions and under every condition each of them
asks -- so none of the three is a step anybody owes a decision. What they are
for is firing one now rather than at the next pull, and for a harness with
nothing watching the queue.

The first two are opposites. "rerun" starts a fresh run of the item, which
is what a correct change whose ground moved needs. "repair" continues the run
that stopped -- same branch, same worktree, same developer session, the
reviewer's findings handed back exactly as they were written -- under a grant of
further repair attempts sized by triage.repair_grant_attempts.

Precondition refusals happen before a claim or continuation is written, so they
cost nothing. A later failure can leave a claim or a charged continuation already
recorded, and a save whose durability was not confirmed is reported as uncertain.
Recovery reads the existing item and run first, reuses recorded claims and
expenditure, and dispatches only a continuation that is still pending. A pause
before the pipeline adopts the run leaves that dispatch pending; a repeated
request for an already served continuation reports its existing outcome.
An unconfirmed success note remains pending independently of dispatch. Later
pulls or repair requests check the existing notes and confirm delivery without
another continuation, expenditure, or dispatch, even after the run finishes.

A re-run and a repair each take the run and nothing else. What either records as
why its run is going -- the decision, who recorded it, in which conversation and
on which turn, and the reasoning it was recorded with -- is read from the durable
triage record of the item the run was made for, where the development manager's
own conversation wrote it. A stoppage with no such decision standing is refused
naming the record that is missing, and so is one whose standing decision is
something other than the verb asked for.

A re-run is claimed once per docketed stoppage, and the item has to be one a run
may start on, which for a run that stopped on a blocker means putting the item
back first. A repair needs no such reopening: it supersedes the blocker itself,
on the item and on the run's own record. It is refused once the item's repair
grant is spent or the review-round cap has no room left, and refused to a person
if the preserved worktree is not as the harness left it, or holds none of the
change it is a repair of -- what is in that worktree is what a continued
developer would be handed back, and an empty one buys an empty repair. Asked
for an approved change the environment stopped, it is refused in the one
sentence the docket entry carries: the change is approved, what stopped it, and
"resume" is what it needs.

A stall is the second stoppage "repair" continues, and the one it charges
nothing for. A run whose provider the harness stopped on time before anything
was returned to its developer -- a stream gone silent, or a total budget run
out -- is continued at the attempt it was stopped in, in the session it stalled
in, and counts no review round and no repair attempt, because a stall judges
nothing. It is the one continuation whose preserved worktree need not hold a
change already: an attempt stopped early may never have written one, and an
empty worktree is what the attempt it is owed starts from. A re-run of such a
stoppage is still available and still the right verb where the ground moved,
and it discards the session and whatever the stalled attempt left uncommitted.

"rearm" is about the other thing that stops: an approved change published to a
forge that queued its merge and then dropped it. It repeats exactly the request
the reviewer's verdict authorized -- the same pull request, by the method that
verdict's own merge recorded, pinned to the commit that was integrated -- and it
overrides nothing to do it: the forge's requirements run again in full. It is
refused where the forge's own merge state names something only a person can
satisfy, refused while the run that made the publication is still alive or the
item has any run in flight, and refused past one re-arm per publication, where a
further drop is an escalation rather than another re-arm. It takes the target
branch's promotion lease before it asks the forge anything and holds it across
the pre-merge check and the merge together, so nothing moves the target between
the check that authorizes the merge and the merge itself.

"resume" is about a stop that is not a stoppage at all: a change the reviewer
approved, which the environment then stopped between that approval and its
promotion -- the primary checkout carrying somebody's uncommitted edit, a tracker
read that timed out under load, a forge or a network that went away. Nothing
about that is a verdict, so nothing about it is a decision, and the other verbs
each spend something for it -- a repair grant for a run with no findings, or a
fresh run and a fresh review for a change nobody disputed. This one resumes the
run at the promotion it stopped in, with the approval it already has, and
charges the item nothing: no review round, no repair grant, no re-run. The run's
record says which stop it was, read from the error that ended the run rather
than from the prose afterwards -- a dirty checkout by its sentinel, a transport
that did not answer by the recovery rule's closed reading of the error -- and a
run whose record says anything else is refused naming what it is. It is refused
while the primary checkout is still not one a promotion can be made from,
refused to a person if the preserved worktree is not as the harness left it or
holds none of the approved change, and it waits rather than refusing when the
harness is full. A worktree the convergence sweep retired while the run stood
stopped is put back from the branch at the reviewed commit, and the run resumed
in it; a branch that moved past that commit, or a sweep that captured
uncommitted work, refuses to a person. The one thing that leaves the
resumed path is a replay onto a target that moved: that re-earns the checks and
the review exactly as any replay does, and a replay that conflicts stops the run
for a person exactly as it always did. "yoyo status" says "approved, resuming
integration" of the run while it promotes.

The intake hold applies to the first two and to "resume", because the harness
is choosing to carry work on there. A re-arm chooses none: it finishes a
publication of work that is already integrated.

A harness with no free developer is not a refusal at all: nothing is claimed or
granted, the decision stands, and asking again once a slot frees carries out the
same one.

"override" crosses one of a work item's caps by whatever ceiling you name, and is
the only thing that does that. The caps stop machines looping, so they refuse a
development manager past them -- but they refused the recording of your answer to
the escalation as well, which left an item at the end of its rounds unrunnable by
every recorded path. This is that answer as a record: it names the budget, what
you raised it to or that you cleared it, who you are, and why, and it is kept on
the item's own triage record where every guard and every reading of the item
finds it.

The development manager crosses a cap too, and narrowly: far enough for the one
decision that was refused and no further, at most five times per item, and only
with a justification, which lands on the item and
reaches you in the channel as the crossing happens. That is a veto by reading
rather than a request -- the crossing applies the moment it is recorded, and
what you do about one you disagree with is undo the work it bought. A sixth
crossing of the same item is refused naming this command, and so is any ceiling
beyond that; "yoyo status <beads-id>" says how many of an item's
five are spent, and each recorded crossing is listed there beside your own
overrides.

It clears or raises and never lowers -- an override that would give the item no
more room than it already has is refused. Lowering a cap is a judgement about the
project's pace rather than about one item, and triage.review_rounds_cap is where
that is made.

It carries nothing out. Recording it changes what the guards permit and nothing
else: the development manager then records the decision the escalation was about,
which spends the item's budget as it always did, and the carry-out acts on that
decision under every condition it already asks. Crossing a cap and spending it
are two decisions and stay two.

"show" decides nothing and carries nothing out: it prints live docket entries
whole, exactly as the development manager's docket shows an entry it has room
for. Every entry that docket has no room to show whole is named there in one
line ending in this command. It takes the run the line names, or the entry's key
where nothing ran, or a work item, which prints every live entry on it. An entry
somebody decided, or whose work item is closed, is not live and is not shown.
Before printing it brings the docket up to date, exactly as the development
manager's own reading of it does, so a stoppage nothing had recorded yet is
recorded then.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --reason <text>   rearm: the development manager's recorded reasoning
                    (required); override: why the cap is being crossed (required);
                    resume: reasoning recorded beside the harness's own account
                    of the stop (optional). "rerun" and "repair" take none: they
                    read the recorded decision instead
  --budget <name>   override: which cap to cross -- "review round" (the default),
                    "repair grant", "re-run", or "merge re-arm"
  --cap <n>         override: the ceiling to raise that budget to
  --clear           override: lift that budget entirely instead
  --by <name>       override: the operator deciding it (required)
  --json            emit machine-readable JSON`)
}

package orchestrator

// The check of each environment a work item names, run before the developer's
// turn (docs/designs/checking-a-run-environment.md, sections 2, 3 and 5).
//
// After the worktree is made and its due install steps have run, and before
// the developer's first turn in this process, the harness runs the check of
// each environment the item's Environments: line names, in the place its
// where gives it: under the developer session's own restrictions for
// where: developer, and where the declared checks run for where: harness.
// Both go through the pipeline's check runner, so both are behind its limits on
// how many heavy checks run at once, and each is bound by its own time_limit.
// A developer check is never given what an install step is given.
//
// Each environment's result is recorded on the run apart from the others, and
// each round replaces the last, so an environment that now passes stops being
// recorded as still to be checked. Where none passes, the run ends before the
// turn with no attempt spent, and the item is held for the development manager
// naming each environment, the criteria it blocks and what its check printed,
// quoting how_to_install for a machine environment. The harness never runs
// that text. Where some pass, the run goes on, and the ones that did not are
// recorded as still to be checked, said on the run, on yoyo status and in the
// item's notes.
//
// The checks run once per process, before its first developer turn: a resumed
// run or a continued attempt is a new process, so it runs them again before
// its next turn, because the machine may have changed in between. A repair
// round in the same process does not, because nothing about the machine has.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// checkEnvironmentsBeforeTurn runs the check of each environment the item
// names, once per process, and records the round on the run. It returns the
// hold that ends the run where none passed, and nil otherwise. An item that
// names none, or a project that declares none, runs nothing and records
// nothing.
func (a *activeRun) checkEnvironmentsBeforeTurn(ctx context.Context) error {
	if a.environmentsChecked {
		return nil
	}
	p := a.pipeline
	if len(p.Config.Environments) == 0 || a.state.TeamConflict != nil || a.landsInIntentRepository() {
		return nil
	}
	line, found, err := config.ReadEnvironmentsLine(a.item.Description)
	if !found {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the environments the work item names: %w", err)
	}
	a.environmentsChecked = true
	declared := map[string]config.Environment{}
	for _, environment := range p.Config.Environments {
		declared[strings.TrimSpace(environment.Name)] = environment
	}
	results := make([]runstate.EnvironmentCheck, 0, len(line.Needs))
	for index, need := range line.Needs {
		environment, ok := declared[need.Name]
		if !ok {
			// Admission refuses an item naming an undeclared environment, so this
			// is a declaration taken out since: nothing can be run for it.
			results = append(results, runstate.EnvironmentCheck{
				Name: need.Name, Result: runstate.EnvironmentFailed, Where: string(config.EnvironmentInDeveloper),
				Criteria: need.Criteria, StartedAt: p.clock().Now(),
				Output: "the project no longer declares this environment under environments, so there is no check to run",
			})
			continue
		}
		result, err := a.checkNamedEnvironment(ctx, environment, need, index)
		if err != nil {
			return err
		}
		results = append(results, result)
	}
	held := a.state.Revision
	written, err := p.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.EnvironmentsChecked{Checks: results, At: p.clock().Now()})
	if err != nil {
		return fmt.Errorf("record the checks of the environments the work item names: %w", err)
	}
	if written.Revision == held+1 {
		a.state.Environments, a.state.Revision, a.state.UpdatedAt = written.Environments, written.Revision, written.UpdatedAt
	} else {
		a.state = written
	}
	pending := a.state.EnvironmentsStillToCheck()
	if len(pending) == 0 {
		return nil
	}
	if len(pending) == len(results) {
		return a.blockOnEnvironments(results)
	}
	a.noteEnvironmentsStillToCheck(pending)
	return nil
}

// checkNamedEnvironment runs one environment's check once, in the place its where
// gives it, and says what became of it. Only a stop of the run itself is an
// error; a check that could not be started is a check that failed, with why
// as its output. index is the environment's place on the item's line, which
// names the folder this run of the check is given.
func (a *activeRun) checkNamedEnvironment(ctx context.Context, environment config.Environment, need config.EnvironmentNeed, index int) (runstate.EnvironmentCheck, error) {
	record, _, err := a.runEnvironmentCommand(ctx, environment, need, environment.Check, a.state.BaseCommit, fmt.Sprintf("check-%d", index+1))
	return record, err
}

// runEnvironmentCommand runs one of an environment's commands, its check or
// its test, once, in the place its where gives it, and says what became of it
// along with the runner's own result. commit is what the command is run
// against, for the heavy-check record. Each run is given an empty folder of its
// own under the run's scratch space, named in YOYODYNE_TEST_SCRATCH and
// removed once the command ends (docs/designs/checking-a-run-environment.md,
// section 6); folder is its name there.
func (a *activeRun) runEnvironmentCommand(ctx context.Context, environment config.Environment, need config.EnvironmentNeed, command, commit, folder string) (runstate.EnvironmentCheck, checks.Result, error) {
	p := a.pipeline
	place := environment.Place()
	record := runstate.EnvironmentCheck{
		Name:         need.Name,
		Where:        string(place),
		Criteria:     append([]int(nil), need.Criteria...),
		Machine:      environment.Machine,
		HowToInstall: strings.TrimSpace(environment.HowToInstall),
		StartedAt:    p.clock().Now(),
	}
	request := checks.EnvironmentRequest{
		RunID:        a.state.RunID,
		Directory:    a.worktree.Path,
		Command:      command,
		LastSequence: a.state.LastSequence,
		TimeLimit:    environment.Limit(),
		Permit: checks.PermitRequest{
			WorkItemID: a.state.WorkItemID,
			Commit:     commit,
			Priority:   heavyCheckPriority(a.state),
		},
	}
	if place == config.EnvironmentInDeveloper {
		restrictions, err := a.developerRestrictions()
		if err != nil {
			record.Result = runstate.EnvironmentFailed
			record.Output = runstate.InstallOutputTail([]string{"the developer session's restrictions could not be stated for the command, so it was not run: " + err.Error()})
			return record, checks.Result{Command: command, Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: -1, Stderr: record.Output}}, nil
		}
		request.Restrictions = &restrictions
	}
	if strings.TrimSpace(p.StateRoot) != "" {
		scratch, err := execution.PrepareTestScratch(p.StateRoot, a.state.RunID, folder)
		if err != nil {
			record.Result = runstate.EnvironmentFailed
			record.Output = runstate.InstallOutputTail([]string{"the empty folder the command is given could not be made, so it was not run: " + err.Error()})
			return record, checks.Result{Command: command, Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: -1, Stderr: record.Output}}, nil
		}
		request.TestScratch = scratch
		// Removed however the command ends. A removal that fails leaves a folder
		// inside the run's own scratch directory, which goes with the run.
		defer func() { _ = execution.RemoveTestScratch(p.StateRoot, a.state.RunID, folder) }()
	}
	var output []string
	collect := func(event execution.Event) error {
		if text, ok := processOutputText(event); ok {
			output = append(output, text)
			if len(output) > 2*runstate.InstallOutputLines {
				output = append(output[:0], output[len(output)-runstate.InstallOutputLines:]...)
			}
		}
		return a.sink(event)
	}
	result, _, runErr := checks.RunEnvironmentCheckWith(ctx, p.Checks, request, collect)
	finished := p.clock().Now()
	record.DurationMilliseconds = max(finished.Sub(record.StartedAt).Milliseconds(), 0)
	if runErr != nil && ctx.Err() != nil {
		return record, result, context.Cause(ctx)
	}
	switch {
	case runErr == nil && result.Passed:
		record.Result = runstate.EnvironmentPassed
	case result.Process.Status == execution.ProcessTimedOut:
		record.Result = runstate.EnvironmentTimedOut
	default:
		record.Result = runstate.EnvironmentFailed
		record.ExitCode = result.Process.ExitCode
	}
	if runErr != nil {
		output = append(output, runErr.Error())
	}
	if request.Restrictions != nil && checks.DeveloperSandboxRefused(strings.Join(output, "\n")) {
		output = append(output, "the developer session's restrictions could not be applied, because the harness is itself running inside a sandbox; the command never ran")
	}
	record.Output = runstate.InstallOutputTail(output)
	if result.Command == "" {
		result.Command = command
	}
	return record, result, nil
}

// processOutputText is the text of one line a check printed, where event is
// one.
func processOutputText(event execution.Event) (string, bool) {
	if event.Type != execution.EventProcessOutput {
		return "", false
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return "", false
	}
	return payload.Text, true
}

// developerRestrictions are the developer session's restrictions for this run:
// writes only to its worktree, its scratch folder and the repository's Git
// directory, and the harness's services folder unreadable, as the developer's
// own session is given them.
func (a *activeRun) developerRestrictions() (checks.DeveloperRestrictions, error) {
	p := a.pipeline
	restrictions := checks.DeveloperRestrictions{Worktree: a.worktree.Path}
	if folderExists(a.scratch) {
		restrictions.Scratch = a.scratch
	}
	if git := filepath.Join(p.Repository, ".git"); strings.TrimSpace(p.Repository) != "" && folderExists(git) {
		restrictions.GitDirectory = git
	}
	if strings.TrimSpace(p.StateRoot) != "" {
		services, err := execution.ServicesDirectoryDenied(p.StateRoot)
		if err != nil {
			return checks.DeveloperRestrictions{}, err
		}
		restrictions.Unreadable = []string{services}
	}
	return restrictions, nil
}

// folderExists reports whether folder names a folder that is there.
func folderExists(folder string) bool {
	if strings.TrimSpace(folder) == "" {
		return false
	}
	info, err := os.Stat(folder)
	return err == nil && info.IsDir()
}

// environmentsHeld is a run none of whose named environments was usable: the
// run ends before the developer's turn and the item is held for the
// development manager.
type environmentsHeld struct {
	checks []runstate.EnvironmentCheck
}

func (h environmentsHeld) Error() string {
	missing := make([]string, 0, len(h.checks))
	for _, check := range h.checks {
		missing = append(missing, check.Missing())
	}
	return "none of the environments the work item names was usable, so the run ended before any developer turn and is held for the development manager: " + strings.Join(missing, "; ")
}

// blockOnEnvironments holds a run none of whose named environments was usable.
// The cause is recorded as the machine's before the item is blocked, so the
// round is given back where it settles and no attempt is spent; the blocker
// names each environment, the criteria it blocks, and what its check printed,
// which is what the development manager's docket entry carries.
func (a *activeRun) blockOnEnvironments(results []runstate.EnvironmentCheck) error {
	held := environmentsHeld{checks: results}
	a.recordEnvironmentalRefusal(runstate.CauseEnvironmentsMissing, held.Error(), nothingRan)
	class := runstate.CauseEnvironmentsMissing.StopClass()
	if err := a.block(renderEnvironmentsHeldNotes(a.outcome, results)); err != nil {
		return stoppedBy(class, withFailedRecord(held, fmt.Errorf("record the environments that were not usable as a blocker: %w", err)))
	}
	return stoppedBy(class, held)
}

// renderEnvironmentsHeldNotes describes a run held because none of the
// environments its item names was usable.
func renderEnvironmentsHeldNotes(outcome Outcome, results []runstate.EnvironmentCheck) string {
	lines := []string{
		"Yoyodyne held this item for the development manager: none of the environments it names was usable by the run, so the run ended before any developer turn.",
		"No developer turn was taken and no developer or repair attempt was spent.",
		"Where the project's declaration of an environment is wrong, changing it is the operator's to write. Where a person has to install something on this machine, that is a step only a person can take. Otherwise the item can be parked or split.",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Environments that were not usable:",
	}
	lines = append(lines, environmentLines(results)...)
	return strings.Join(lines, "\n")
}

// noteEnvironmentsStillToCheck tells the item which of its environments were
// not usable while others were, so its notes say which criteria are still to
// be checked. The note is bounded rather than recovered: the run goes on
// either way, and the run's own record says the same.
func (a *activeRun) noteEnvironmentsStillToCheck(pending []runstate.EnvironmentCheck) {
	if !a.claimed {
		return
	}
	lines := []string{
		"Yoyodyne checked the environments this item names before the developer's turn. Some were usable, so the run goes on; the ones below were not, and the criteria they serve are still to be checked.",
		"Run: " + a.state.RunID,
	}
	lines = append(lines, environmentLines(pending)...)
	noteCtx, cancel := runTrackerContext(a.state.RunID, a.state.WorkItemID)
	defer cancel()
	_, _ = a.pipeline.Tracker.RecordOutcome(noteCtx, a.state.WorkItemID, strings.Join(lines, "\n"))
}

// environmentLines is one entry per environment: what became of its check,
// what it blocks, how a person installs a machine prerequisite, and the end of
// what the check printed.
func environmentLines(results []runstate.EnvironmentCheck) []string {
	var lines []string
	for _, check := range results {
		lines = append(lines, "- "+check.Describe()+".")
		if check.Machine {
			if instruction := check.HowToInstall; instruction != "" {
				lines = append(lines, fmt.Sprintf("  It needs something installed for the whole machine. A person has to run this on this machine; the harness never runs it: %s", instruction))
			} else {
				lines = append(lines, "  It needs something installed for the whole machine, which a person has to install on this machine.")
			}
		}
		if output := strings.TrimSpace(check.Output); output != "" {
			lines = append(lines, "  Its check ended on:", lastLines(output, 20))
		}
	}
	return lines
}

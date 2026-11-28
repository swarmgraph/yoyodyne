package orchestrator

// A change that lands while environments its item names are still to be
// checked, and the fresh run that checks only those
// (docs/designs/checking-a-run-environment.md, section 7).
//
// Such a change is otherwise the work, but the criteria those environments
// serve were never judged, so it does not discharge its item
// (runstate.State.LeavesEnvironmentsToCheck). The item is put back open and
// parked by the run, with a note naming each such environment, the criteria
// it serves and the commit the change landed at, and the run is put on the
// development manager's list as a stopped run whose failure says the same, so
// she can decide a re-run once she judges the environments available.
//
// A re-run of that run is given the environments to check on its selection
// (runstate.EnvironmentsRecheck). It starts from the target branch, never from
// the landed run's branch, and before its first developer turn it checks only
// those environments, runs the configured checks and then those environments'
// tests at the target's head, and records each test result against that
// commit. Where all of them pass, the item closes with no developer turn and
// nothing integrated. Where any does not, the run goes on as an ordinary run
// to repair it, and the developer is told what failed at that head.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// leftOnlyForEnvironments reports a run whose change would have discharged its
// item but for environments still to be checked.
func leftOnlyForEnvironments(state runstate.State) bool {
	return state.LeavesEnvironmentsToCheck() && state.LandingDischarges() && state.ApprovalDischarges() &&
		!state.Escalated() && strings.TrimSpace(state.ReleaseTagOwed) == "" && state.LandingProblem == ""
}

// environmentsLeftParking is the parking reason of such an item. It opens with
// the run, as every parking a run places does, so a re-run of that run is not
// held by it (runstate.RerunWaitsOnParking).
func environmentsLeftParking(state runstate.State) string {
	return fmt.Sprintf("yoyodyne run %s landed its change while the environments %s were still to be checked; the item is held for the development manager, who may decide a fresh run that checks only those",
		state.RunID, strings.Join(quotedNames(state.EnvironmentsStillToCheck()), ", "))
}

// environmentsLeftFailure is what the development manager's list says of such a
// run, in place of the failure a stopped run carries.
func environmentsLeftFailure(state runstate.State) string {
	return runstate.RecordFailure(state.EnvironmentsLeftAtLandingSays())
}

// landedLeavingEnvironments reports a run that ended with its change landed and
// environments its item names still to be checked, which is a run the
// development manager's list carries.
func landedLeavingEnvironments(state runstate.State) bool {
	return state.Status == runstate.StatusSucceeded && state.Integration != nil && leftOnlyForEnvironments(state)
}

// environmentsLeftEntry is the stopped-run entry the development manager reads
// for such a run. It carries no blocker, because the run was not blocked: its
// change landed, and the failure it carries is the account of what is still to
// be checked.
func (d Docketer) environmentsLeftEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	return d.stoppedRunEntryCarrying(state, now, d.look(state), environmentsLeftFailure(state))
}

// environmentNames is the names of the environments given, in their order.
func environmentNames(results []runstate.EnvironmentCheck) []string {
	if len(results) == 0 {
		return nil
	}
	names := make([]string, 0, len(results))
	for _, check := range results {
		names = append(names, check.Name)
	}
	return names
}

// quotedNames is environmentNames with each name quoted, as a person reads them.
func quotedNames(results []runstate.EnvironmentCheck) []string {
	names := environmentNames(results)
	for index, name := range names {
		names[index] = fmt.Sprintf("%q", name)
	}
	return names
}

// recheck is the environments this run was selected to check again, and nil for
// every run but a re-run of an item whose change landed with some still to be
// checked.
func (a *activeRun) recheck() *runstate.EnvironmentsRecheck {
	if a.state.Selection == nil {
		return nil
	}
	return a.state.Selection.Environments
}

// environmentsConfirmed ends a re-run whose rechecked environments all passed
// at the target's head: the item closes with no developer turn.
type environmentsConfirmed struct{}

func (environmentsConfirmed) Error() string {
	return "the configured checks and the tests of every environment still to be checked passed at the target's head, so the item closes with no developer turn"
}

// recheckLeftEnvironments runs, for a re-run of an item whose change landed with
// environments still to be checked, the configured checks and then the test of
// each of those environments whose check passed before this turn, at the
// commit the run started from, which is the target's head. It is run once, and
// only before the run's first developer turn. It answers environmentsConfirmed
// where every one passed, and nil where the run is to go on as an ordinary run,
// with what failed kept for the developer's prompt.
func (a *activeRun) recheckLeftEnvironments(ctx context.Context) error {
	recheck := a.recheck()
	if recheck == nil || a.environmentsRechecked || a.state.ProviderSessionID != "" || a.state.HarnessCommit != "" {
		return nil
	}
	a.environmentsRechecked = true
	p := a.pipeline
	head := a.state.BaseCommit
	failed, err := a.checksAtHead(ctx, head)
	if err != nil {
		return err
	}
	declared := map[string]config.Environment{}
	for _, environment := range p.Config.Environments {
		declared[strings.TrimSpace(environment.Name)] = environment
	}
	var results []runstate.EnvironmentCheck
	for index, check := range a.state.Environments {
		if !recheck.Rechecks(check.Name) || !check.Passed() {
			continue
		}
		environment, ok := declared[check.Name]
		if !ok || strings.TrimSpace(environment.Test) == "" {
			continue
		}
		need := config.EnvironmentNeed{Name: check.Name, Criteria: check.Criteria}
		record, _, err := a.runEnvironmentCommand(ctx, environment, need, environment.Test, head, fmt.Sprintf("recheck-%d", index+1))
		if err != nil {
			return err
		}
		record.Commit = head
		results = append(results, record)
		if !record.Passed() && failed == "" {
			failed = "the " + record.DescribeTest() + "."
			if output := strings.TrimSpace(record.Output); output != "" {
				failed += " It ended on:\n" + lastLines(output, 20)
			}
		}
	}
	if len(results) > 0 {
		held := a.state.Revision
		written, err := p.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.EnvironmentsTested{Tests: results, At: p.clock().Now()})
		if err != nil {
			return fmt.Errorf("record the tests of the environments still to be checked at the target's head: %w", err)
		}
		if written.Revision == held+1 {
			a.state.EnvironmentTests, a.state.Revision, a.state.UpdatedAt = written.EnvironmentTests, written.Revision, written.UpdatedAt
		} else {
			a.state = written
		}
	}
	// Every environment the re-run was given has to have been tested and have
	// passed. One whose check did not pass before this turn, or whose test the
	// project no longer declares, is still to be checked, and the run goes on.
	tested := map[string]bool{}
	for _, result := range results {
		if result.Passed() {
			tested[result.Name] = true
		}
	}
	for _, name := range recheck.Names {
		if !tested[strings.TrimSpace(name)] && failed == "" {
			failed = fmt.Sprintf("the environment %q, which an earlier run's change left still to be checked, was not tested at the target's head %s, because its check did not pass before this turn or the project declares no test for it.", name, head)
		}
	}
	if failed != "" {
		a.recheckFailure = failed
		return nil
	}
	return environmentsConfirmed{}
}

// checksAtHead runs the configured checks in the worktree, which stands at the
// target's head, before any developer turn, and says what failed, empty where
// every one passed. Only a stop of the run itself is an error.
func (a *activeRun) checksAtHead(ctx context.Context, head string) (string, error) {
	p := a.pipeline
	configured := p.Config.Checks
	if a.ciFlow() {
		configured = shortLocalChecks(p.Config.Checks, p.Config.Execution.CIChecks.Local)
	}
	commands, providerCLIs := withPathChecks(configured, nil)
	if len(commands) == 0 {
		return "", nil
	}
	results, last, err := p.Checks.Run(ctx, checks.Request{
		RunID:        a.state.RunID,
		Directory:    a.worktree.Path,
		Env:          a.checkEnvironment(),
		Commands:     commands,
		ProviderCLIs: providerCLIs,
		LastSequence: a.state.LastSequence,
		Permit: checks.PermitRequest{
			WorkItemID: a.state.WorkItemID,
			Commit:     head,
			Priority:   heavyCheckPriority(a.state),
		},
	}, a.sink)
	a.state.LastSequence = last
	if err != nil {
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		return fmt.Sprintf("the configured checks could not be run at the target's head %s: %v", head, err), nil
	}
	for _, result := range results {
		if result.Passed {
			continue
		}
		said := fmt.Sprintf("the configured check %s did not pass at the target's head %s, before any change was made.", result.Command, head)
		if output := strings.TrimSpace(result.FailureOutput); output != "" {
			said += " It ended on:\n" + lastLines(output, 20)
		}
		return said, nil
	}
	return "", nil
}

// recheckSection tells the developer of such a re-run what failed at the
// target's head before its turn, and is empty for every other run.
func (a *activeRun) recheckSection() string {
	if a.recheckFailure == "" {
		return ""
	}
	return "\n\n# What failed at the target's head before your turn\n\n" +
		"An earlier run's change for this item landed while environments it names were still to be checked. " +
		"Before your turn, the harness ran the configured checks and those environments' tests at the target's head, and the item stays open because " +
		a.recheckFailure + "\n\nRepair it from this worktree, which starts from the target's head."
}

// completeEnvironmentsConfirmed ends a re-run whose rechecked environments all
// passed: the outcome is recorded on the item, the item is closed, the entries
// the development manager's list held for it are closed with it, and the run is
// made terminal as succeeded and complete. No developer was invoked, nothing
// was reviewed, and nothing is integrated, because there is no change. The
// worktree is kept, as a run approved with no file changes keeps it, for the
// sweep that retires an unintegrated run's artifacts.
func (a *activeRun) completeEnvironmentsConfirmed(ctx context.Context) (Outcome, error) {
	p := a.pipeline
	// The closure reads the one rule every closure site reads. A run that ends
	// here has no claim and no review, so it discharges its item exactly when no
	// environment is still to be checked; anything else is a fault in getting
	// here, and the item is left as it is.
	if !a.state.Discharges() {
		return a.fail(fmt.Errorf("the run reached the closure on its environments' tests while %s", nonEmpty(a.state.EnvironmentsStillToCheckSays(), "its record says it does not discharge its item")), runstate.StatusFailed)
	}
	a.outcome.EnvironmentsConfirmed = true
	a.outcome.EnvironmentsStillToCheck = nil
	if err := a.recordOutcomeOnce(ctx, func() string { return renderEnvironmentsConfirmedNotes(a.state) }); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("record the outcome of a run whose environments' tests passed at the target's head: %w", err)), runstate.StatusFailed)
	}
	if _, err := recoveringTrackerWrite(ctx, a,
		itemLanded(p.Tracker, a.state.WorkItemID, func(item beads.WorkItem) bool { return item.Status == "closed" }),
		func(ctx context.Context) (beads.WorkItem, error) {
			return p.Tracker.Complete(ctx, a.state.WorkItemID, environmentsConfirmedReason(a.state))
		}); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("close the work item whose environments' tests passed at the target's head: %w", err)), runstate.StatusFailed)
	}
	a.outcome.WorkItemClosed = true
	p.closeDocketWithItem(a.state, "closed by run "+a.state.RunID+", whose environments' tests passed at the target's head")
	a.recordPrice()
	ended, err := p.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.EnvironmentsConfirmedCompleted{At: p.clock().Now()})
	if err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("save the completed run whose environments' tests passed: %w", err)), runstate.StatusFailed)
	}
	a.state = ended
	a.outcome.Status = runstate.StatusSucceeded
	a.outcome.Phase = a.state.Phase
	return a.outcome, nil
}

// environmentsConfirmedReason is the closure of such an item, naming the commit
// the tests ran on.
func environmentsConfirmedReason(state runstate.State) string {
	return fmt.Sprintf("Yoyodyne run %s ran the configured checks and the tests of the environments an earlier change left still to be checked at the target's head %s, and all passed, so the item is closed with no developer turn",
		state.RunID, shortCommit(state.BaseCommit))
}

// renderEnvironmentsConfirmedNotes is what the item is told when such a run
// closes it: each test, its result and the commit it ran on.
func renderEnvironmentsConfirmedNotes(state runstate.State) string {
	lines := []string{
		"Yoyodyne run passed the configured checks and the tests of every environment still to be checked at the target's head, so the item is closed with no developer turn and nothing integrated.",
		"Run: " + state.RunID,
		"Commit tested: " + state.BaseCommit,
	}
	if recheck := state.Selection; recheck != nil && recheck.Environments != nil {
		earlier := "Earlier run whose change left them to be checked: " + recheck.Environments.RunID
		if recheck.Environments.Landed != "" {
			earlier += ", landed at commit " + recheck.Environments.Landed
		}
		lines = append(lines, earlier)
	}
	for _, test := range state.EnvironmentTestsAt(state.BaseCommit) {
		lines = append(lines, "- "+test.DescribeTest()+".")
	}
	return strings.Join(lines, "\n")
}

// isEnvironmentsConfirmed reports the ending recheckLeftEnvironments answers
// where everything passed.
func isEnvironmentsConfirmed(err error) bool {
	var confirmed environmentsConfirmed
	return errors.As(err, &confirmed)
}

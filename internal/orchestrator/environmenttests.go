package orchestrator

// The test of each environment whose check passed, run after the developer
// submits and before review (docs/designs/checking-a-run-environment.md,
// sections 3 and 6).
//
// Once the configured checks pass on the submitted change, the harness runs the
// test of every environment whose check passed before the developer's turn, at
// the commit the harness made of the change, in the place its where gives it,
// exactly as that check ran (environments.go). Each result is recorded with the
// commit it ran on, and each round replaces the last. The first test that does
// not pass goes back to the developer as a failed check with the test's output,
// and spends one repair round as any failed check does. For a where: harness
// environment that hand-back is the only way the developer learns the result:
// it is told the environment exists before its turn, and is given no result
// until it has submitted.
//
// The reviewer is given each result recorded on the commit under review, with
// that commit and where the test ran, and the environments still to be checked.
// A result recorded on any other commit is never shown to it
// (runstate.State.EnvironmentTestsAt).

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// testEnvironmentsAtSubmittedHead runs the test of each environment whose
// check passed, at the submitted commit, records the round, and returns the
// first failure as a checkFailure for the repair loop to hand back. A run with
// no environment whose check passed runs nothing and records nothing.
func (a *activeRun) testEnvironmentsAtSubmittedHead(ctx context.Context, changed []string) error {
	p := a.pipeline
	if len(p.Config.Environments) == 0 || len(a.state.Environments) == 0 {
		return nil
	}
	declared := map[string]config.Environment{}
	for _, environment := range p.Config.Environments {
		declared[strings.TrimSpace(environment.Name)] = environment
	}
	commit := a.state.HarnessCommit
	var (
		results []runstate.EnvironmentCheck
		failing *checkFailure
	)
	for index, check := range a.state.Environments {
		if !check.Passed() {
			continue
		}
		environment, ok := declared[check.Name]
		if !ok || strings.TrimSpace(environment.Test) == "" {
			// A declaration taken out since its check passed has no test to run;
			// the environment is said as still to be checked to the reviewer.
			continue
		}
		need := config.EnvironmentNeed{Name: check.Name, Criteria: check.Criteria}
		record, result, err := a.runEnvironmentCommand(ctx, environment, need, environment.Test, commit, fmt.Sprintf("test-%d", index+1))
		if err != nil {
			return err
		}
		record.Commit = commit
		results = append(results, record)
		if !record.Passed() && failing == nil {
			tested := record
			failing = &checkFailure{result: result, changed: changed, changedListed: true, environment: &tested}
		}
	}
	if len(results) == 0 {
		return nil
	}
	held := a.state.Revision
	written, err := p.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.EnvironmentsTested{Tests: results, At: p.clock().Now()})
	if err != nil {
		return fmt.Errorf("record the tests of the environments the work item names: %w", err)
	}
	if written.Revision == held+1 {
		a.state.EnvironmentTests, a.state.Revision, a.state.UpdatedAt = written.EnvironmentTests, written.Revision, written.UpdatedAt
	} else {
		a.state = written
	}
	if failing != nil {
		a.state.KeptChecks = nil
		return *failing
	}
	return nil
}

// environmentTestFailureSays opens the output of a failed environment's test
// as the developer is handed it: which environment, which criteria, where it
// ran and on which commit, and, for one the harness runs, that the developer
// cannot run it itself.
func environmentTestFailureSays(test runstate.EnvironmentCheck) string {
	said := "This is " + test.DescribeTest() + ". The harness runs it after you submit and before review, because the work item names this environment."
	if test.Where == string(config.EnvironmentInHarness) {
		said += " It runs where the declared checks run, outside your sandbox, so you cannot run it yourself: change the code, then submit, and the harness runs it again."
	} else {
		said += " It runs under your own session's restrictions, so you can run it yourself before you submit."
	}
	return said
}

// environmentTestFailed reports whether a recorded check failure is the
// failure of an environment's test, which its output opens by saying
// (environmentTestFailureSays), so its repair prompt does not tell the
// developer that a configured check failed.
func environmentTestFailed(failure runstate.CheckFailure) bool {
	return strings.HasPrefix(failure.Output, "This is the test of the environment ")
}

// environmentsBeforeTurnSection tells the developer, before its turn, which
// environments the item names and which criteria each serves: each harness
// environment whose check passed is one whose test only the harness can run,
// and whose result the developer is given only after submitting. It is empty
// for a run whose item names none.
func environmentsBeforeTurnSection(state runstate.State) string {
	if len(state.Environments) == 0 {
		return ""
	}
	var harness, developer, pending []string
	for _, check := range state.Environments {
		switch {
		case !check.Passed():
			pending = append(pending, fmt.Sprintf("- %q, for %s: its check did not pass before your turn, so its test will not run on this run and those criteria are still to be checked.", check.Name, check.Serves()))
		case check.Where == string(config.EnvironmentInHarness):
			harness = append(harness, fmt.Sprintf("- %q, for %s.", check.Name, check.Serves()))
		default:
			developer = append(developer, fmt.Sprintf("- %q, for %s.", check.Name, check.Serves()))
		}
	}
	var said strings.Builder
	said.WriteString("\n\n# Environments this work item names\n\n")
	if len(harness) > 0 {
		said.WriteString("The harness runs the test of each environment below after you submit and before review, where the declared checks run, outside your sandbox. You cannot run these tests yourself, and you are given their results only after you submit: a test that fails comes back to you as a failed check with its output.\n\n")
		said.WriteString(strings.Join(harness, "\n"))
		said.WriteString("\n\n")
	}
	if len(developer) > 0 {
		said.WriteString("The test of each environment below runs under your own session's restrictions, so you can run it yourself before submitting. The harness runs it again after you submit, before review.\n\n")
		said.WriteString(strings.Join(developer, "\n"))
		said.WriteString("\n\n")
	}
	if len(pending) > 0 {
		said.WriteString(strings.Join(pending, "\n"))
		said.WriteString("\n")
	}
	return strings.TrimRight(said.String(), "\n")
}

// describeEnvironmentEvidence is what the reviewer is given about the
// environments the item names: each test result recorded on the commit under
// review, with that commit and where it ran, and each environment still to be
// checked. A result recorded on any other commit is left out. It is empty for
// a run whose item names none.
func describeEnvironmentEvidence(state runstate.State) string {
	if len(state.Environments) == 0 && len(state.EnvironmentTests) == 0 {
		return ""
	}
	var said strings.Builder
	tested := map[string]bool{}
	for _, test := range state.EnvironmentTestsAt(state.HarnessCommit) {
		tested[test.Name] = true
		said.WriteString("- " + test.DescribeTest() + ".\n")
		if output := strings.TrimSpace(test.Output); output != "" {
			said.WriteString("  It ended on:\n")
			for _, line := range strings.Split(lastLines(output, 20), "\n") {
				said.WriteString("    " + line + "\n")
			}
		}
	}
	var pending []string
	for _, check := range state.Environments {
		switch {
		case !check.Passed():
			pending = append(pending, "- "+check.Missing()+": its check did not pass before the developer's turn, so its test did not run.")
		case !tested[check.Name]:
			pending = append(pending, fmt.Sprintf("- %q, which %s blocks: its check passed, and no result of its test is recorded on the commit under review.", check.Name, check.Serves()))
		}
	}
	if len(pending) > 0 {
		said.WriteString("\nStill to be checked:\n")
		said.WriteString(strings.Join(pending, "\n"))
		said.WriteString("\n")
	}
	return said.String()
}

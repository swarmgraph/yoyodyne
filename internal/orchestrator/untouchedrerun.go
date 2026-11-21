package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The one rerun of a failure confined to tests the change does not touch,
// asked from verify for the checks before review and the checks over the merge
// candidate before promotion. Whether it runs is checks.PlanUntouchedRerun's
// decision; this file records it on the run before it starts, has the check
// runner launch it, and settles how it ended:
//
//   - every rerun test passing makes the check count as passed, and the checks
//     after it in the stage then run as they would have;
//   - a rerun test failing again, a rerun stopped by its time limit, and one
//     that could not be started all leave the first failure standing, handled
//     as an ordinary failed check;
//   - either way the record keeps the first failure with its output beside how
//     the rerun ended, and the reviewer is shown every test that failed and then
//     passed (describeUntouchedReruns).
//
// It is separate from the two reruns of a check stopped by its time limit
// (runstate.CheckStageContinuation): a rerun stopped by its limit is not a
// check stopped by its limit, so it spends none of those, and they spend none
// of this. The design is "A check that fails only in tests the change does not
// touch" in docs/designs/recoverable-and-terminal-failures.md.

// untouchedRerunStage is the check stage verify is running, as the bound
// counts it.
func (a *activeRun) untouchedRerunStage() string {
	if a.checkingCandidate {
		return runstate.UntouchedRerunBeforePromotion
	}
	return runstate.UntouchedRerunBeforeReview
}

// untouchedRerunPassedBeforeReview reports a check before review on the
// current candidate commit that counted as passed only because its failing
// tests passed when run once more. checkCandidateWhole then checks the merge
// candidate whole even where those checks were already told the whole module,
// so the test runs again before the change lands.
func (a *activeRun) untouchedRerunPassedBeforeReview() bool {
	for _, rerun := range a.state.PassedUntouchedReruns(a.state.HarnessCommit) {
		if rerun.Stage == runstate.UntouchedRerunBeforeReview {
			return true
		}
	}
	return false
}

// rerunUntouched takes the results of one check stage and, where the stage
// ended on a failure the plan lets run once more, runs it and what follows it.
// It returns the stage's results as verify is to judge them, and the event
// sequence the runs reached. An error is the runner's, from the checks after a
// passing rerun, and is handled as the stage's own would be; a rerun that could
// not be started is no error, because the first failure stands.
func (a *activeRun) rerunUntouched(ctx context.Context, request checks.Request, results []checks.Result, last uint64, changed []string) ([]checks.Result, uint64, error) {
	p := a.pipeline
	position := len(results) - 1
	if position < 0 || position >= len(request.Commands) || ctx.Err() != nil {
		return results, last, nil
	}
	first := results[position]
	if first.Passed || first.CouldNotRun != "" || first.Process.Status != execution.ProcessFailed || first.FailingTests == nil {
		return results, last, nil
	}
	judgment := judgeFailingTests(checkFailure{result: first, changed: changed, changedListed: true})
	stage, commit := a.untouchedRerunStage(), a.state.HarnessCommit
	plan := checks.PlanUntouchedRerun(judgment, commit, a.state.UntouchedRerunsSpent(stage, commit))
	if !plan.Run {
		return results, last, nil
	}
	tests := make([]checks.FailingTest, 0, len(judgment.Tests))
	for _, test := range judgment.Tests {
		tests = append(tests, checks.FailingTest{Name: test.Name, File: test.File})
	}
	record := runstate.UntouchedRerun{
		Stage:         stage,
		Commit:        commit,
		Command:       first.Command,
		Tests:         tests,
		FirstExitCode: first.Process.ExitCode,
		FirstOutput:   boundedCheckOutput(first),
		StartedAt:     p.clock().Now().UTC(),
	}
	// The rerun is on the record before it starts or it does not start: a rerun
	// a restart could not find is one the restart would grant again.
	if err := a.recordUntouchedRerun(ctx, record); err != nil {
		return results, last, nil
	}
	rerun := request
	rerun.Commands = []string{request.Commands[position]}
	rerun.ProviderCLIs = shiftedProviderCLIs(request.ProviderCLIs, position, position+1)
	rerun.Env = append(slices.Clone(request.Env), checks.FailingTestsEnv(plan.Names))
	rerun.LastSequence = last
	again, reached, err := p.Checks.Run(ctx, rerun, a.sink)
	if reached > last {
		last = reached
	}
	outcome, detail := untouchedRerunEnding(again, err)
	ended := p.clock().Now().UTC()
	record.Outcome, record.Detail, record.EndedAt = outcome, detail, &ended
	if len(again) > 0 {
		record.Output = boundedCheckOutput(again[0])
	}
	// A pass the record cannot keep is not counted: the run would go on to
	// review on a check its own record says never finished. The rerun stays
	// spent either way, because its start is already written.
	if err := a.recordUntouchedRerun(ctx, record); err != nil || outcome != runstate.UntouchedRerunPassed {
		return results, last, nil
	}
	judged := append(slices.Clone(results[:position]), again[0])
	if position+1 >= len(request.Commands) {
		return judged, last, nil
	}
	rest := request
	rest.Commands = request.Commands[position+1:]
	rest.ProviderCLIs = shiftedProviderCLIs(request.ProviderCLIs, position+1, len(request.Commands))
	rest.LastSequence = last
	more, reached, err := p.Checks.Run(ctx, rest, a.sink)
	if reached > last {
		last = reached
	}
	return append(judged, more...), last, err
}

// recordUntouchedRerun writes a rerun onto the run through the store's narrow
// update, and takes up what it wrote: only the reruns and the revision where
// nothing else changed the run in between, and the whole run where something
// did, as adoptInstallUpdate does.
func (a *activeRun) recordUntouchedRerun(ctx context.Context, rerun runstate.UntouchedRerun) error {
	p := a.pipeline
	held := a.state.Revision
	written, err := p.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.UntouchedRerunRecorded{Rerun: rerun, At: p.clock().Now()})
	if err != nil {
		return err
	}
	if written.Revision == held+1 {
		a.state.UntouchedReruns, a.state.Revision, a.state.UpdatedAt = written.UntouchedReruns, written.Revision, written.UpdatedAt
		return nil
	}
	a.state = written
	return nil
}

// untouchedRerunEnding is how a rerun ended, and why in ordinary words where
// it did not pass.
func untouchedRerunEnding(results []checks.Result, err error) (runstate.UntouchedRerunOutcome, string) {
	switch {
	case err != nil:
		return runstate.UntouchedRerunUnfinished, "the rerun could not be started, so the first failure stands: " + err.Error()
	case len(results) == 0:
		return runstate.UntouchedRerunUnfinished, "the rerun was not started, so the first failure stands"
	}
	result := results[0]
	switch {
	case result.Passed:
		return runstate.UntouchedRerunPassed, ""
	case result.CouldNotRun != "":
		return runstate.UntouchedRerunUnfinished, "the rerun could not run, so the first failure stands: " + result.CouldNotRun
	case result.Process.Status == execution.ProcessFailed:
		says := "a test failed again when run once more on the same commit, so the first failure stands"
		if result.FailingTests != nil && result.FailingTests.Named() {
			says += ": " + result.FailingTests.Describe()
		}
		return runstate.UntouchedRerunFailed, says
	case result.Process.Status == execution.ProcessTimedOut:
		return runstate.UntouchedRerunUnfinished, fmt.Sprintf("the rerun was stopped by its time limit after %s, so the first failure stands", result.Elapsed().Round(time.Second))
	default:
		return runstate.UntouchedRerunUnfinished, fmt.Sprintf("the rerun was %s and did not finish, so the first failure stands", result.Process.Status)
	}
}

// shiftedProviderCLIs is the request's ProviderCLIs for the commands from
// start up to end, renumbered from zero.
func shiftedProviderCLIs(marked map[int]bool, start, end int) map[int]bool {
	if len(marked) == 0 {
		return nil
	}
	shifted := map[int]bool{}
	for position, on := range marked {
		if on && position >= start && position < end {
			shifted[position-start] = true
		}
	}
	return shifted
}

// describeUntouchedReruns is the reviewer's evidence of the checks whose
// failing tests failed and then passed when run once more on the candidate it
// is shown: each test with its file, and the first failure's output. It is
// read from the run's record, so a review after a restart is shown it too, and
// it is empty where no rerun on this candidate passed.
func describeUntouchedReruns(state runstate.State) string {
	passed := state.PassedUntouchedReruns(state.HarnessCommit)
	if len(passed) == 0 {
		return ""
	}
	var said strings.Builder
	for _, rerun := range passed {
		fmt.Fprintf(&said, "- %s, in the checks %s, failed (exit %d) in tests the change does not touch, and passed when only those tests were run once more on the same commit. Each test that failed and then passed, with its file: %s.\n",
			rerun.Command, rerun.Stage, rerun.FirstExitCode, rerun.TestNames())
		if output := strings.TrimSpace(rerun.FirstOutput); output != "" {
			said.WriteString("  The first failure's output:\n")
			for _, line := range strings.Split(output, "\n") {
				said.WriteString("    " + line + "\n")
			}
		}
	}
	return said.String()
}

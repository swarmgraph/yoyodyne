package orchestrator

// What GitHub's log says about a required check that failed on a merge queue
// candidate. A candidate whose required checks ran on GitHub is handed back
// over the job that failed there, so the queue reads that job's log through the
// forge's CheckOutput, as a run's own wait on GitHub does (readCIOutputs), and
// keeps on the generation the failing tests the log names, each with its
// package and judged against the candidate's paths, and the end of the log
// with secrets removed (runstate.MergeQueueFailedCheck). The hand-back made from
// the generation carries it (forgeFailure in mergequeuequick.go).

import (
	"context"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// readForgeFailures returns the evidence with what GitHub's log says about
// each check in failed, read once per candidate commit: where prior, the
// evidence read on an earlier pass, already holds one for a check on the same
// commit, it is kept rather than read again. A log that cannot be read is
// recorded with why, so the hand-back says so rather than naming nothing.
func (w MergeQueueWorker) readForgeFailures(ctx context.Context, generation runstate.MergeQueueGeneration, evidence runstate.MergeQueueCIEvidence, prior *runstate.MergeQueueCIEvidence, failed []string) runstate.MergeQueueCIEvidence {
	evidence.Failures = nil
	var unread []string
	for _, name := range failed {
		if prior != nil && prior.Commit == evidence.Commit {
			if kept := prior.FailureOf(name); kept != nil {
				evidence.Failures = append(evidence.Failures, *kept)
				continue
			}
		}
		unread = append(unread, name)
	}
	if len(unread) == 0 {
		return evidence
	}
	// The record of GitHub's checks keeps no job ids, so the failing checks are
	// asked for again to find the jobs whose logs to read.
	reading, err := w.Pipeline.CIForge.RequiredChecks(ctx, generation.Candidate, unread)
	jobs := map[string]publish.RequiredCheck{}
	if err == nil && reading.Commit == generation.Candidate {
		for _, check := range reading.Checks {
			jobs[check.Name] = check
		}
	}
	var changed []string
	listed, unknown := false, ""
	for _, name := range unread {
		failure := runstate.MergeQueueFailedCheck{Command: name}
		check, found := jobs[name]
		switch {
		case err != nil:
			failure.Unread = boundedEvidence("GitHub's checks on the candidate could not be read again to find the job: " + err.Error())
		case !found:
			failure.Unread = "GitHub did not name the job behind the check on the candidate when asked again"
		default:
			output, readErr := w.Pipeline.CIForge.CheckOutput(ctx, check)
			if readErr != nil {
				failure.Unread = boundedEvidence(readErr.Error())
				if check.URL != "" {
					failure.Unread = boundedEvidence(readErr.Error() + "; the log is at " + check.URL)
				}
				break
			}
			if !listed {
				listed = true
				var pathsErr error
				if changed, pathsErr = w.candidatePaths(ctx, generation); pathsErr != nil {
					unknown = pathsErr.Error()
				}
			}
			judged := judgeForgeLog(output, changed, unknown)
			failure.ForgeTests = &judged
			failure.Output = boundedTail(strings.TrimSpace(output.Tail), runstate.MaxMergeQueueCheckOutputBytes)
		}
		evidence.Failures = append(evidence.Failures, failure)
	}
	// Kept in the order the checks failed, as FailedOnForge gives them.
	slices.SortStableFunc(evidence.Failures, func(a, b runstate.MergeQueueFailedCheck) int {
		return slices.Index(failed, a.Command) - slices.Index(failed, b.Command)
	})
	return evidence
}

// judgeForgeLog is the failing tests GitHub's log names, each with the package
// it failed in where the end of the log that was read names it, judged against
// the candidate's paths by the rule GitHub's failing tests on a run's own pull
// request are judged by (forgeTestTouched). CheckOutput names the failing tests
// from the whole log, but keeps only its end, where a package's failure line
// may be and the test's own lines may not; a test whose package is not in that
// end is kept with no package, said as such, and is never judged untouched.
func judgeForgeLog(output publish.CheckOutput, changed []string, changedUnknown string) runstate.ForgeTestJudgment {
	paired, unnamed := forgeTestNames(output.Tail)
	var judgment runstate.ForgeTestJudgment
	add := func(test runstate.ForgeTest) {
		if len(judgment.Tests) == runstate.MaxRecordedFailingChecks {
			judgment.More++
			return
		}
		judgment.Tests = append(judgment.Tests, test)
	}
	named := map[string]bool{}
	for _, test := range paired {
		named[test.name] = true
		add(runstate.ForgeTest{
			Name: oneline.Bound(test.name, 300), Package: oneline.Bound(test.pkg, 300),
			Touched: changedUnknown == "" && forgeTestTouched(test.pkg, changed),
		})
	}
	var withoutPackage []string
	for _, name := range output.FailingTests {
		name, _, _ = strings.Cut(name, "/")
		if name == "" || named[name] {
			continue
		}
		named[name] = true
		withoutPackage = append(withoutPackage, name)
		add(runstate.ForgeTest{Name: oneline.Bound(name, 300)})
	}
	if len(withoutPackage) > 0 {
		// The test's own lines are named failing by the whole log, so a
		// package line in the end read is not missing a test, only its lines.
		unnamed = slices.DeleteFunc(unnamed, func(why string) bool {
			return strings.HasSuffix(why, "failed without naming a failing test") || why == "the log names no failing Go test"
		})
		unnamed = append(unnamed, "the end of GitHub's log that was read does not say which package "+strings.Join(withoutPackage, ", ")+" failed in")
	}
	if len(paired) > 0 || len(withoutPackage) > 0 {
		unnamed = slices.DeleteFunc(unnamed, func(why string) bool { return why == "the log names no failing Go test" })
	}
	if changedUnknown != "" {
		unnamed = append([]string{changedUnknown}, unnamed...)
	}
	judgment.Unnamed = oneline.Bound(strings.Join(unnamed, "; "), 600)
	return judgment
}

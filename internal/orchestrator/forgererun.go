package orchestrator

// GitHub's checks failing only in tests the change does not touch.
//
// A merge the harness asked GitHub for — a run's own pull request queued to
// merge (queuedchecks.go), or a merge queue candidate's (mergequeuepromotion.go)
// — can be held by a check that fails on GitHub's runner in a test the change
// never came near: a busy runner, a timing assumption, a test that fails one
// run in fifty. Until this was built the harness read such a failure as the
// change's own and withdrew the merge, or, in the merge queue, waited the whole
// stuck-merge age on it before withdrawing.
//
// Now, before anything is withdrawn, GitHub's log of each failing job is read
// for the tests it names (forgeTestNames), and each test is judged touched or
// untouched by the change's paths (forgeTestTouched). Where every failure is a
// named test, there are at most runstate.MaxUntouchedForgeTests of them, and the
// change touches none, GitHub is asked to run the failing jobs once more on the
// same commit. The request is written down before it is made, and a commit gets
// one however often the harness restarts (runstate.ForgeChecksRerun). If the
// second run passes, GitHub makes the merge it is holding. If it fails, or the
// failure was not confined to untouched tests, or GitHub would not run the job
// again, what happened before happens, and the stop says which tests failed and
// whether the change touched them.
//
// Test names are read from Go's test output, which is what this repository's
// checks print; a check printing anything else names no test, and its failure is
// an ordinary one. The project-declared reading of a check's failing tests, and
// the change-aware analyser's evidence of which tests a change touches, are
// later work (yoyodyne-ifd.429.87.1 and yoyodyne-ifd.429.87.6): until they land
// the path rule here is the whole judgment, and it can only err towards a
// failure being the change's.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// forgeTestLogLines is how much of a failing job's log is read for the tests
// it names. go test prints a line per package, so this covers a few hundred
// packages' worth of output above the line the step failed on.
const forgeTestLogLines = 3000

// forgeListedFilesBound is how many of a pull request's files GitHub lists. A
// list that long may be missing files, so the change's paths are not known
// whole and nothing is judged untouched from it.
const forgeListedFilesBound = 100

// ForgeJobLogTail reads the end of GitHub's log of the job behind a check run.
// It is satisfied by publish.GitHub.
type ForgeJobLogTail interface {
	JobLogTail(ctx context.Context, checkRun int64, lines int) (string, error)
}

// ForgeRerunner asks GitHub to run a failed job again on the same commit. It is
// satisfied by publish.GitHub.
type ForgeRerunner interface {
	RerunCheck(ctx context.Context, checkRun int64) error
}

// forgeTestName is one failing test as a log names it.
type forgeTestName struct{ name, pkg string }

// forgeTestNames reads Go's test output for the failing tests it names, each
// with the package it failed in. go test prints a failing package's tests
// ("--- FAIL: TestName") and then the package ("FAIL\tpath/of/package"), so the
// tests named since the last package line are that package's. unnamed says why
// some failure could not be read as named tests: a package that failed without
// naming a test (a build failure, a panic, a test binary that timed out), or
// tests named with no package line after them, which is a log cut short.
func forgeTestNames(log string) (tests []forgeTestName, unnamed []string) {
	var pending []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimSpace(stripLogStamp(strings.TrimRight(raw, "\r")))
		switch {
		case strings.HasPrefix(line, "--- FAIL: "):
			fields := strings.Fields(strings.TrimPrefix(line, "--- FAIL: "))
			if len(fields) == 0 {
				continue
			}
			// A subtest is named under its test, and the test is what fails.
			name, _, _ := strings.Cut(fields[0], "/")
			if !containsString(pending, name) {
				pending = append(pending, name)
			}
		case strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "FAIL "):
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			pkg := fields[1]
			switch {
			case strings.Contains(line, "[build failed]") || strings.Contains(line, "[setup failed]"):
				unnamed = append(unnamed, fmt.Sprintf("%s did not build", pkg))
			case len(pending) == 0:
				unnamed = append(unnamed, fmt.Sprintf("%s failed without naming a failing test", pkg))
			}
			for _, name := range pending {
				if key := pkg + "\x00" + name; !seen[key] {
					seen[key] = true
					tests = append(tests, forgeTestName{name: name, pkg: pkg})
				}
			}
			pending = nil
		}
	}
	if len(pending) > 0 {
		unnamed = append(unnamed, fmt.Sprintf("%s failed with no package named after them, so the log may be cut short", strings.Join(pending, ", ")))
	}
	if len(tests) == 0 && len(unnamed) == 0 {
		unnamed = append(unnamed, "the log names no failing Go test")
	}
	return tests, unnamed
}

// stripLogStamp takes off the timestamp GitHub puts at the front of each line
// of a job's log.
func stripLogStamp(line string) string {
	stamp, text, found := strings.Cut(line, " ")
	if !found || len(stamp) < len("2006-01-02T15:04:05Z") || stamp[4] != '-' {
		return line
	}
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		return line
	}
	return text
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// forgeTestTouched reports a failing test the change touches: a changed path
// in the test's own directory, which is its package's directory. A Go package
// is named by its import path, which ends in its directory, so a changed
// path's directory naming the end of the import path is the same directory.
// The package at the top of the repository is named by the module's path alone,
// which nothing here can tell from a package's, so a Go file changed at the top
// touches every test, and so does go.mod or go.sum, which reach every package.
// A deleted file and a renamed file's old path count, where the list carries
// them.
func forgeTestTouched(pkg string, changed []string) bool {
	for _, file := range changed {
		file = strings.Trim(strings.TrimSpace(file), "/")
		if file == "" {
			continue
		}
		directory := path.Dir(file)
		if directory == "." {
			if strings.HasSuffix(file, ".go") || file == "go.mod" || file == "go.sum" {
				return true
			}
			continue
		}
		if pkg == directory || strings.HasSuffix(pkg, "/"+directory) {
			return true
		}
	}
	return false
}

// judgeForgeTests reads GitHub's log of every failing check for the tests it
// names and judges each against the change's paths. changedUnknown says why the
// change's paths are not known whole, and is empty where they are; a failure
// judged without them is never confined to untouched tests.
func judgeForgeTests(ctx context.Context, logs ForgeJobLogTail, failing []runstate.FailingCheck, changed []string, changedUnknown string) runstate.ForgeTestJudgment {
	var judgment runstate.ForgeTestJudgment
	var unnamed []string
	if changedUnknown != "" {
		unnamed = append(unnamed, changedUnknown)
	}
	for _, check := range failing {
		switch {
		case logs == nil:
			unnamed = append(unnamed, "nothing is wired to this harness to read GitHub's logs")
			continue
		case check.CheckRun <= 0:
			unnamed = append(unnamed, fmt.Sprintf("%s names no job whose log could be read", check.Name))
			continue
		}
		log, err := logs.JobLogTail(ctx, check.CheckRun, forgeTestLogLines)
		if err != nil {
			unnamed = append(unnamed, fmt.Sprintf("the log of %s could not be read (%s)", check.Name, oneline.Bound(err.Error(), 200)))
			continue
		}
		tests, missing := forgeTestNames(log)
		for _, why := range missing {
			unnamed = append(unnamed, check.Name+": "+why)
		}
		for _, test := range tests {
			// GitHub's own annotations putting the check on a file the change
			// touches makes every test it names touched.
			touched := forgeTestTouched(test.pkg, changed) || len(check.OnChange) > 0
			if len(judgment.Tests) == runstate.MaxRecordedFailingChecks {
				judgment.More++
				continue
			}
			judgment.Tests = append(judgment.Tests, runstate.ForgeTest{
				Name: oneline.Bound(test.name, 300), Package: oneline.Bound(test.pkg, 300), Touched: touched,
			})
		}
	}
	judgment.Unnamed = oneline.Bound(strings.Join(unnamed, "; "), 600)
	return judgment
}

// listedChangeUnknown is why a pull request's files, as GitHub lists them, are
// not known to be the change's whole list, or "" where they are.
func listedChangeUnknown(files []string) string {
	switch {
	case len(files) == 0:
		return "GitHub listed no file this change touches"
	case len(files) >= forgeListedFilesBound:
		return fmt.Sprintf("GitHub lists at most %d of a pull request's files, so the change's whole list of files is not known", forgeListedFilesBound)
	}
	return ""
}

// untouchedRerunDue reports a red reading whose failure is confined to tests
// the change does not touch, on a head given no further run yet, and every
// failing check of which names a job GitHub can run again.
func untouchedRerunDue(checks runstate.PullRequestChecks, judgment runstate.ForgeTestJudgment) bool {
	if !checks.Red() || checks.FailedInTheJob() || checks.ChangeFails() || !judgment.Untouched() {
		return false
	}
	if checks.UntouchedRerun != nil && checks.UntouchedRerun.HeadCommit == checks.HeadCommit {
		return false
	}
	for _, failing := range checks.Failing {
		if failing.CheckRun <= 0 {
			return false
		}
	}
	return len(checks.Failing) <= runstate.MaxRecordedFailingChecks
}

// newUntouchedRerun is the record of a further run, written before GitHub is
// asked for it.
func newUntouchedRerun(checks runstate.PullRequestChecks, judgment runstate.ForgeTestJudgment, now time.Time) runstate.ForgeChecksRerun {
	rerun := runstate.ForgeChecksRerun{HeadCommit: checks.HeadCommit, RecordedAt: now.UTC(), Tests: judgment}
	for _, failing := range checks.Failing {
		rerun.Checks = append(rerun.Checks, failing.Name)
		rerun.CheckRuns = append(rerun.CheckRuns, failing.CheckRun)
	}
	return rerun
}

// askForgeRerun asks GitHub to run every failed check of a reading again, and
// says what it refused if it refused any. A check with no check run to name
// cannot be run again, which is a refusal as well.
func askForgeRerun(ctx context.Context, rerunner ForgeRerunner, checks runstate.PullRequestChecks) string {
	if rerunner == nil {
		return "nothing is wired to this harness to ask GitHub to run a job again"
	}
	var refused []string
	for _, failing := range checks.Failing {
		if failing.CheckRun <= 0 {
			refused = append(refused, fmt.Sprintf("%s names no check run", failing.Name))
			continue
		}
		if err := rerunner.RerunCheck(ctx, failing.CheckRun); err != nil {
			if errors.Is(err, publish.ErrForgeAccessRefused) {
				refused = append(refused, fmt.Sprintf("%s: the forge would not let the harness's token run the job again (%v), and granting it that is the operator's", failing.Name, err))
				continue
			}
			refused = append(refused, fmt.Sprintf("%s: %v", failing.Name, err))
		}
	}
	return oneline.Bound(strings.Join(refused, "; "), 400)
}

// settleUntouchedRerun records GitHub's answer to a further run on the record
// written before it was asked.
func settleUntouchedRerun(rerun *runstate.ForgeChecksRerun, refused string, now time.Time) {
	if refused != "" {
		rerun.Refused = refused
		return
	}
	at := now.UTC()
	rerun.AskedAt = &at
}

// forgeFailureWhose is the sentence a stop over GitHub's failing checks ends
// on: whether the change touched the failing tests, in plain words.
func forgeFailureWhose(checks runstate.PullRequestChecks) string {
	if checks.ChangeFails() {
		return " GitHub's own annotations put the failure on a file this change touches."
	}
	if checks.Tests == nil {
		return ""
	}
	judgment := *checks.Tests
	switch {
	case judgment.Untouched() && checks.UntouchedRerun != nil && checks.UntouchedRerun.Asked():
		return " The failing tests are ones this change does not touch, and they failed on GitHub's second run of the job as well, so the failure is not put down to this change."
	case judgment.Untouched():
		return " The failing tests are ones this change does not touch, so the failure is not put down to this change."
	}
	for _, test := range judgment.Tests {
		if test.Touched {
			return " At least one failing test is in a directory this change touches."
		}
	}
	return " Whether this change touched what failed could not be told from GitHub's log."
}

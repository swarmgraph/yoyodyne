package checks

import (
	"path"
	"strings"
)

// The one rerun of a failure confined to tests the change does not touch:
// "What the harness does" and "The bounds" in
// docs/designs/recoverable-and-terminal-failures.md. This file is the plan
// that decides it; the pipeline asks it, records the rerun on the run before
// it starts, and has the runner launch it.
//
//   - Only a failure JudgeTouched found confined is run again, and only its
//     failing tests.
//   - Each check stage gets at most MaxUntouchedReruns such reruns per candidate
//     commit, counted from the run's record, so a restart of the harness finds
//     the rerun it already spent and a new candidate commit starts again.
//   - The rerun is the check's own command, told the failing tests in
//     FailingTestsVariable, under the check's own time limit. The harness knows
//     nothing of a project's test runner, so a check narrows itself by reading
//     the variable, as this repository's make targets do
//     (scripts/rerun-failing.sh); a check that does not read it runs whole again.
//   - It is separate from the two reruns of a check stopped by its time limit:
//     neither counts toward the other.

// FailingTestsVariable is the environment variable a check run once more is
// given, naming the tests to run, one to a line. The checks on main use it for
// their second run too (internal/maincheck). It is an environment variable for
// the reason ChangedGoPackagesVariable is.
const FailingTestsVariable = "YOYODYNE_FAILING_TESTS"

// MaxUntouchedReruns is how many reruns of a failure confined to untouched
// tests one check stage may make on one candidate commit. Exactly one: a test
// that passes one time in three would land a real fault if it were run until it
// passed.
const MaxUntouchedReruns = 1

// UntouchedRerunPlan is whether a failed check's failing tests are run once
// more, and why not where they are not.
type UntouchedRerunPlan struct {
	Run bool
	// Names are what FailingTestsVariable is set to, one to a line
	// (RerunNames). Empty where Run is false.
	Names []string
	// Why is the reason in ordinary words where Run is false.
	Why string
}

// PlanUntouchedRerun decides whether a failed check's failing tests are run
// once more. judgment is the check's failing tests judged against the change
// (JudgeTouched), nil where no reading applies to the check; commit is the
// candidate commit the check ran on; spent is how many such reruns the run's
// record already holds for this check stage and this commit, a rerun that was
// recorded and never finished included.
func PlanUntouchedRerun(judgment *TouchedTests, commit string, spent int) UntouchedRerunPlan {
	switch {
	case judgment == nil:
		return UntouchedRerunPlan{Why: "no reading of the project's names the check's failing tests"}
	case !judgment.Confined:
		return UntouchedRerunPlan{Why: "the failure is not confined to tests the change does not touch"}
	case strings.TrimSpace(commit) == "":
		return UntouchedRerunPlan{Why: "the run records no candidate commit to bind the rerun to"}
	case spent >= MaxUntouchedReruns:
		return UntouchedRerunPlan{Why: "this check stage already ran a failure's untouched tests once more on this commit"}
	}
	tests := make([]FailingTest, 0, len(judgment.Tests))
	for _, test := range judgment.Tests {
		tests = append(tests, FailingTest{Name: test.Name, File: test.File})
	}
	return UntouchedRerunPlan{Run: true, Names: RerunNames(tests)}
}

// RerunNames is what a check run once more is told in FailingTestsVariable:
// each failing test's name, then the directory of each test's file as a
// "./dir" pattern ("." for the repository's top), each once and in the order
// first named. The directory is what lets a runner find the test without
// reading every package; Go's tooling takes the pattern as a package.
func RerunNames(tests []FailingTest) []string {
	var names, directories []string
	seen := map[string]bool{}
	for _, test := range tests {
		if name := strings.TrimSpace(test.Name); name != "" && !seen["test "+name] {
			seen["test "+name] = true
			names = append(names, name)
		}
	}
	for _, test := range tests {
		file := strings.Trim(strings.TrimSpace(filepathToSlash(test.File)), "/")
		if file == "" {
			continue
		}
		directory := path.Dir(path.Clean(file))
		if directory != "." {
			directory = "./" + directory
		}
		if !seen["dir "+directory] {
			seen["dir "+directory] = true
			directories = append(directories, directory)
		}
	}
	return append(names, directories...)
}

// FailingTestsEnv is the variable and its value in the form a process
// environment takes.
func FailingTestsEnv(names []string) string {
	return FailingTestsVariable + "=" + strings.Join(names, "\n")
}

package checks

import (
	"fmt"
	"path"
	"strings"
)

// Which failing tests a change touches: the judgment that decides whether a
// failed check's failure is confined to tests the change does not touch. The
// rule is "Which tests a change touches" in
// docs/designs/recoverable-and-terminal-failures.md, and it knows nothing about
// any language: it reads the failing tests and their files as the project's
// declared reading named them (failingtests.go), the change's paths, and, where
// the project declares one, the change-aware analyser's answer.
//
//   - A failing test is touched when its own file changed, or when any changed
//     path is in the same directory as its file. The caller lists a deleted file
//     among the changed paths, and a renamed file under both its old and its new
//     path, which is what gitworktree's ChangedPaths does.
//   - Where the analyser's answer is complete evidence for the change, a test in
//     any directory it lists as affected is touched too. Where it failed, could
//     not tell, or said the whole suite must run, only the rule above decides.
//     The analyser only ever adds tests to the touched set.
//   - A failure is confined to untouched tests only when every failing test was
//     named with its file, there are at least one and at most
//     MaxUntouchedFailingTests of them, and none is touched.
//
// The directory rule itself is ChangedPathWhere, which the judgment of GitHub's
// failing tests (internal/orchestrator/forgererun.go) uses too.

// MaxUntouchedFailingTests is how many failing tests a failure may have and
// still be confined to tests the change does not touch. More than this failing
// at once is more likely the change than chance.
const MaxUntouchedFailingTests = 5

// The bounds on what a judgment keeps on the record.
const (
	maxTouchedWhy    = 300
	maxTouchedReason = 1000
)

// AffectedEvidence is the change-aware analyser's answer for one change: the
// directories it lists as affected, or why its answer is not complete
// evidence. A project that declares no analyser has no answer at all, which
// JudgeTouched is given as nil.
type AffectedEvidence struct {
	// Directories are the repository-relative directories the analyser lists as
	// affected by the change. "." is the repository's top.
	Directories []string
	// Incomplete says why the answer is not complete evidence for the change: the
	// analyser failed, could not tell, or said the whole suite must run. It is
	// empty where the answer is complete, and Directories are read only then.
	Incomplete string
}

// JudgedTest is one failing test with its file, and whether the change
// touches it.
type JudgedTest struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Touched bool   `json:"touched,omitempty"`
	// Why says what touches it, and is empty for a test the change does not
	// touch.
	Why string `json:"why,omitempty"`
}

// TouchedTests is the judgment of one failed check's failing tests against the
// change: each test, touched or not, whether the failure is confined to tests
// the change does not touch, and the reason in ordinary words.
type TouchedTests struct {
	Tests []JudgedTest `json:"tests,omitempty"`
	// More is how many further failing tests the reading named past the ones it
	// kept. They are not judged, and they count toward the bound.
	More int `json:"more,omitempty"`
	// Confined is the failure being confined to tests the change does not touch.
	Confined bool   `json:"confined,omitempty"`
	Reason   string `json:"reason"`
}

// ChangedPathWhere is the first of a change's paths that matches, given each
// path's directory and the path itself, both repository-relative with no
// leading or trailing slash. A path at the repository's top is in directory
// ".". Empty entries are passed over.
func ChangedPathWhere(changed []string, matches func(directory, file string) bool) (string, bool) {
	for _, file := range changed {
		file = strings.Trim(strings.TrimSpace(filepathToSlash(file)), "/")
		if file == "" {
			continue
		}
		file = path.Clean(file)
		if matches(path.Dir(file), file) {
			return file, true
		}
	}
	return "", false
}

func filepathToSlash(value string) string { return strings.ReplaceAll(value, "\\", "/") }

// cleanDirectory is a directory in the form ChangedPathWhere gives one.
func cleanDirectory(directory string) string {
	directory = strings.Trim(strings.TrimSpace(filepathToSlash(directory)), "/")
	if directory == "" {
		return "."
	}
	return path.Clean(directory)
}

// TestTouchedBy says what of a change touches a test in file: its own file
// changed, or another changed path in its directory. It is empty where nothing
// does.
func TestTouchedBy(file string, changed []string) string {
	file = path.Clean(strings.Trim(strings.TrimSpace(filepathToSlash(file)), "/"))
	directory := path.Dir(file)
	found, ok := ChangedPathWhere(changed, func(changedDirectory, changedFile string) bool {
		return changedFile == file || changedDirectory == directory
	})
	switch {
	case !ok:
		return ""
	case found == file:
		return "its own file changed"
	default:
		return found + " changed in its directory"
	}
}

// JudgeTouched judges a failed check's failing tests against the change.
// changedUnknown says why the change's paths are not known, and is empty where
// changed is the whole list; a failure judged without them is never confined.
// analyser is the change-aware analyser's answer, nil where the project
// declares none.
func JudgeTouched(failing FailingTests, changed []string, changedUnknown string, analyser *AffectedEvidence) TouchedTests {
	if !failing.Named() {
		unread := failing.Unread
		if unread == "" {
			unread = "the output names no failing test"
		}
		return TouchedTests{Reason: ordinary("not every failing test could be named with its file: " + unread)}
	}
	affected := map[string]bool{}
	if analyser != nil && analyser.Incomplete == "" {
		for _, directory := range analyser.Directories {
			affected[cleanDirectory(directory)] = true
		}
	}
	judgment := TouchedTests{More: failing.More}
	var touched []string
	for _, test := range failing.Tests {
		judged := JudgedTest{Name: test.Name, File: test.File}
		if changedUnknown == "" {
			judged.Why = TestTouchedBy(test.File, changed)
		}
		if directory := cleanDirectory(path.Dir(test.File)); judged.Why == "" && affected[directory] {
			judged.Why = "the change-aware analyser lists " + directory + " as affected"
		}
		judged.Why = cutRecordText(judged.Why, maxTouchedWhy)
		judged.Touched = judged.Why != ""
		if judged.Touched {
			touched = append(touched, fmt.Sprintf("%s (%s)", judged.Name, judged.Why))
		}
		judgment.Tests = append(judgment.Tests, judged)
	}
	total := len(failing.Tests) + failing.More
	switch {
	case changedUnknown != "":
		judgment.Reason = ordinary("the change's paths could not be listed (" + changedUnknown + ")")
	case len(touched) > 0:
		judgment.Reason = ordinary("the change touches " + strings.Join(touched, ", "))
	case total > MaxUntouchedFailingTests:
		judgment.Reason = ordinary(fmt.Sprintf("%d tests failed, more than the %d a failure confined to untouched tests may have", total, MaxUntouchedFailingTests))
	default:
		judgment.Confined = true
		names := make([]string, 0, len(judgment.Tests))
		for _, test := range judgment.Tests {
			names = append(names, fmt.Sprintf("%s (%s)", test.Name, test.File))
		}
		judgment.Reason = "Every failing test is one the change does not touch: " + strings.Join(names, ", ") +
			". No changed path is in any of their directories" + analyserSays(analyser) + "."
	}
	judgment.Reason = cutRecordText(judgment.Reason, maxTouchedReason)
	return judgment
}

// ordinary is the reason a failure is not confined to untouched tests.
func ordinary(why string) string {
	return "The failure is not confined to tests the change does not touch, so it is an ordinary failed check: " + why + "."
}

// analyserSays is what a confined judgment says about the analyser.
func analyserSays(analyser *AffectedEvidence) string {
	switch {
	case analyser == nil:
		return ", and the project declares no change-aware analyser"
	case analyser.Incomplete != "":
		return ", and the change-aware analyser's answer was not used, because " + analyser.Incomplete
	default:
		return ", and the change-aware analyser lists none of them as affected"
	}
}

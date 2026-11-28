package runstate

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/checks"
)

// MaxMergeQueueCheckOutputBytes bounds the end of a failed check's output that
// a candidate's record, and the hand-back made from it, keep.
const MaxMergeQueueCheckOutputBytes = maxMergeQueueEvidenceText

// MergeQueueFailedCheck is what a configured check that ran to its own end and
// failed on a merge queue candidate said about its failure: the failing tests
// the project's declared reading named (failing_tests in the configuration),
// each judged against the paths the candidate changes, and the end of what the
// check printed with secrets removed. The candidate's check record keeps it,
// and the hand-back made from that record carries it to the run, the work item
// and the development manager, so whoever decides what follows can tell which
// test failed, in which directory, and whether the change touched it, without
// running the check again.
type MergeQueueFailedCheck struct {
	Command string `json:"command"`
	// Tests is each failing test with its file, or why the failure could not be
	// read that way. It is nil for a check no reading applies to, such as a
	// linter, whose Output then says what failed.
	Tests *checks.FailingTests `json:"tests,omitempty"`
	// Touched is those tests judged against the candidate's changed paths
	// (checks.JudgeTouched): a test is touched where its own file, or another
	// path in its directory, changed. It is nil where Tests is.
	Touched *checks.TouchedTests `json:"touched,omitempty"`
	// ForgeTests is, for a required check GitHub ran, the failing tests its
	// job's log names, each with its package and judged against the candidate's
	// changed paths. GitHub's log names a Go test's package rather than its
	// file, so these are kept apart from Tests and Touched, which are nil for
	// such a check.
	ForgeTests *ForgeTestJudgment `json:"forge_tests,omitempty"`
	// Unread is why GitHub's log of the check could not be read, and empty
	// where it was or the check ran here. Output is empty beside it.
	Unread string `json:"unread,omitempty"`
	// Output is the end of what the check printed, at most
	// MaxMergeQueueCheckOutputBytes, with secrets removed.
	Output string `json:"output,omitempty"`
}

// DescribeTests is the failing tests in one line of ordinary words: each
// test's name, the directory its file is in, which for most test runners is
// its package, and whether the change touches that directory. It is empty for
// a check no reading applies to.
func (f MergeQueueFailedCheck) DescribeTests() string {
	if f.Unread != "" {
		return fmt.Sprintf("GitHub's log of %s could not be read, so its failing tests are not named here: %s.", f.Command, strings.TrimSuffix(f.Unread, "."))
	}
	if f.ForgeTests != nil {
		return f.describeForgeTests()
	}
	if f.Tests == nil {
		return ""
	}
	if !f.Tests.Named() || f.Touched == nil || len(f.Touched.Tests) != len(f.Tests.Tests) {
		return f.Tests.Describe()
	}
	parts := make([]string, 0, len(f.Touched.Tests))
	for _, test := range f.Touched.Tests {
		directory := path.Dir(test.File)
		where := "a directory the change does not touch"
		if test.Touched {
			where = "a directory the change touches: " + test.Why
		}
		parts = append(parts, fmt.Sprintf("%s in %s (%s), %s", test.Name, directory, test.File, where))
	}
	noun := "Failing test"
	if len(parts) > 1 || f.Tests.More > 0 {
		noun = "Failing tests"
	}
	text := noun + ": " + strings.Join(parts, "; ")
	if f.Tests.More > 0 {
		text += fmt.Sprintf("; and %d more, not judged", f.Tests.More)
	}
	return text + "."
}

// describeForgeTests is DescribeTests for the tests GitHub's log names, each
// with its package.
func (f MergeQueueFailedCheck) describeForgeTests() string {
	judged := *f.ForgeTests
	parts := make([]string, 0, len(judged.Tests))
	for _, test := range judged.Tests {
		switch {
		case test.Package == "":
			parts = append(parts, test.Name+", whose package the end of GitHub's log that was read does not name")
		case test.Touched:
			parts = append(parts, fmt.Sprintf("%s in %s, a package the change touches", test.Name, test.Package))
		default:
			parts = append(parts, fmt.Sprintf("%s in %s, a package the change does not touch", test.Name, test.Package))
		}
	}
	var text string
	switch {
	case len(parts) == 0:
		text = "GitHub's log names no failing test"
	case len(parts) == 1 && judged.More == 0:
		text = "Failing test in GitHub's log: " + parts[0]
	default:
		text = "Failing tests in GitHub's log: " + strings.Join(parts, "; ")
	}
	if judged.More > 0 {
		text += fmt.Sprintf("; and %d more, not judged", judged.More)
	}
	if judged.Unnamed != "" {
		text += "; not every failure could be named as a test: " + strings.TrimSuffix(judged.Unnamed, ".")
	}
	return text + "."
}

func (f *MergeQueueFailedCheck) validate() error {
	if f == nil {
		return nil
	}
	var problems []error
	if strings.TrimSpace(f.Command) == "" {
		problems = append(problems, errors.New("a failed check's record names no check"))
	}
	if len(f.Output) > MaxMergeQueueCheckOutputBytes {
		problems = append(problems, errors.New("a failed check's output exceeds its bound"))
	}
	if f.Tests != nil && len(f.Tests.Tests) > checks.MaxFailingTests {
		problems = append(problems, errors.New("a failed check names more failing tests than a record keeps"))
	}
	if f.Touched != nil && (f.Tests == nil || len(f.Touched.Tests) > checks.MaxFailingTests) {
		problems = append(problems, errors.New("a failed check's judged tests are not the failing tests it names"))
	}
	if f.ForgeTests != nil {
		if f.Tests != nil || f.Touched != nil {
			problems = append(problems, errors.New("a failed check names its tests either as the check here read them or as GitHub's log does, never both"))
		}
		problems = append(problems, f.ForgeTests.validate()...)
	}
	if len(f.Unread) > maxMergeQueueEvidenceText || (f.Unread != "" && (f.Output != "" || f.ForgeTests != nil)) {
		problems = append(problems, errors.New("why GitHub's log could not be read exceeds its bound, or stands beside what was read of it"))
	}
	return errors.Join(problems...)
}

// sameCheckResults compares two lists of check results by what they say rather
// than by where each failure's details were decoded to.
func sameCheckResults(a, b []MergeQueueCheckResult) bool {
	return slices.EqualFunc(a, b, func(x, y MergeQueueCheckResult) bool { return reflect.DeepEqual(x, y) })
}

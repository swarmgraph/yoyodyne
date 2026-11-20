package checks

import (
	"fmt"
	"strings"
	"testing"
)

func named(tests ...FailingTest) FailingTests { return FailingTests{Tests: tests} }

func judgedTouched(t *testing.T, judgment TouchedTests, name string) JudgedTest {
	t.Helper()
	for _, test := range judgment.Tests {
		if test.Name == name {
			return test
		}
	}
	t.Fatalf("judgment %#v does not judge %s", judgment, name)
	return JudgedTest{}
}

// A failing test is touched when its own file changed, or when any changed
// path is in its directory; one elsewhere is not, and a failure of untouched
// tests alone is confined to them.
func TestAFailingTestIsTouchedByItsOwnFileOrAChangedPathBesideIt(t *testing.T) {
	t.Parallel()

	failing := named(
		FailingTest{Name: "TestOwn", File: "internal/alpha/alpha_test.go"},
		FailingTest{Name: "TestBeside", File: "internal/beta/beta_test.go"},
		FailingTest{Name: "TestElsewhere", File: "internal/gamma/gamma_test.go"},
	)
	changed := []string{"internal/alpha/alpha_test.go", "internal/beta/beta.go", "internal/gamma/sub/deep.go", "docs/x.md"}
	judgment := JudgeTouched(failing, changed, "", nil)

	if own := judgedTouched(t, judgment, "TestOwn"); !own.Touched || own.Why != "its own file changed" {
		t.Fatalf("a test in a changed file = %#v, want it touched by its own file", own)
	}
	if beside := judgedTouched(t, judgment, "TestBeside"); !beside.Touched || beside.Why != "internal/beta/beta.go changed in its directory" {
		t.Fatalf("a test beside a changed file = %#v, want it touched by that file", beside)
	}
	if elsewhere := judgedTouched(t, judgment, "TestElsewhere"); elsewhere.Touched {
		t.Fatalf("a test whose directory only has a changed subdirectory = %#v, want it untouched", elsewhere)
	}
	if judgment.Confined || !strings.Contains(judgment.Reason, "ordinary failed check") || !strings.Contains(judgment.Reason, "TestOwn (its own file changed)") {
		t.Fatalf("judgment = %#v, want an ordinary failure naming what the change touches", judgment)
	}

	untouched := JudgeTouched(named(failing.Tests[2]), changed, "", nil)
	if !untouched.Confined || !strings.Contains(untouched.Reason, "TestElsewhere (internal/gamma/gamma_test.go)") || !strings.Contains(untouched.Reason, "declares no change-aware analyser") {
		t.Fatalf("judgment = %#v, want the failure confined to the untouched test", untouched)
	}
}

// A test at the repository's top is beside every path changed there.
func TestATestAtTheTopIsTouchedByAPathChangedAtTheTop(t *testing.T) {
	t.Parallel()

	judgment := JudgeTouched(named(FailingTest{Name: "TestTop", File: "main_test.go"}), []string{"README.md"}, "", nil)
	if judgment.Confined || !judgment.Tests[0].Touched {
		t.Fatalf("judgment = %#v, want the top-level test touched", judgment)
	}
}

// A renamed file counts under both its old and its new path, and a deleted
// file counts too: the changed paths list both halves of a rename and every
// deletion, and either is a changed path in a test's directory.
func TestARenameCountsUnderBothPathsAndADeletionCounts(t *testing.T) {
	t.Parallel()

	failing := named(
		FailingTest{Name: "TestLeft", File: "internal/old/old_test.go"},
		FailingTest{Name: "TestArrived", File: "internal/new/new_test.go"},
		FailingTest{Name: "TestDeletedBeside", File: "internal/gone/kept_test.go"},
	)
	// A rename of internal/old/thing.go to internal/new/thing.go, and the
	// deletion of internal/gone/dropped.go.
	changed := []string{"internal/new/thing.go", "internal/old/thing.go", "internal/gone/dropped.go"}
	judgment := JudgeTouched(failing, changed, "", nil)
	for _, test := range judgment.Tests {
		if !test.Touched {
			t.Fatalf("%s = %#v, want it touched", test.Name, test)
		}
	}
	if judgedTouched(t, judgment, "TestLeft").Why != "internal/old/thing.go changed in its directory" {
		t.Fatalf("judgment = %#v, want the renamed file's old path to touch the test it left", judgment)
	}
	if judgedTouched(t, judgment, "TestDeletedBeside").Why != "internal/gone/dropped.go changed in its directory" {
		t.Fatalf("judgment = %#v, want the deleted file to touch the test beside it", judgment)
	}
	if judgment.Confined {
		t.Fatalf("judgment = %#v, want an ordinary failure", judgment)
	}
}

// Complete evidence from the change-aware analyser adds the tests in every
// directory it lists as affected; it never takes a test out of the touched set.
func TestCompleteAnalyserEvidenceAddsATestAndNeverRemovesOne(t *testing.T) {
	t.Parallel()

	failing := named(
		FailingTest{Name: "TestDependent", File: "internal/dependent/dependent_test.go"},
		FailingTest{Name: "TestBeside", File: "internal/changed/changed_test.go"},
	)
	changed := []string{"internal/changed/changed.go"}
	analyser := &AffectedEvidence{Directories: []string{"internal/dependent/", "internal/other"}}

	judgment := JudgeTouched(failing, changed, "", analyser)
	dependent := judgedTouched(t, judgment, "TestDependent")
	if !dependent.Touched || dependent.Why != "the change-aware analyser lists internal/dependent as affected" {
		t.Fatalf("a test the analyser lists = %#v, want it touched by the analyser", dependent)
	}
	if beside := judgedTouched(t, judgment, "TestBeside"); !beside.Touched {
		t.Fatalf("a test beside a changed file the analyser does not list = %#v, want it still touched", beside)
	}
	if judgment.Confined {
		t.Fatalf("judgment = %#v, want an ordinary failure", judgment)
	}

	alone := JudgeTouched(named(failing.Tests[0]), changed, "", nil)
	if !alone.Confined {
		t.Fatalf("without the analyser, judgment = %#v, want the dependent test untouched", alone)
	}
	listedNone := JudgeTouched(named(failing.Tests[0]), changed, "", &AffectedEvidence{Directories: []string{"internal/other"}})
	if !listedNone.Confined || !strings.Contains(listedNone.Reason, "analyser lists none of them as affected") {
		t.Fatalf("judgment = %#v, want the failure confined and the analyser's answer named", listedNone)
	}
}

// Where the analyser failed, could not tell, or said the whole suite must run,
// what it listed is not read and the path rule decides alone.
func TestAnAnalyserThatFailedFallsBackToThePathRule(t *testing.T) {
	t.Parallel()

	failing := named(FailingTest{Name: "TestDependent", File: "internal/dependent/dependent_test.go"})
	changed := []string{"internal/changed/changed.go"}
	for _, incomplete := range []string{"the analyser exited with 2", "the analyser could not tell", "go.mod changed, so the whole suite must run"} {
		judgment := JudgeTouched(failing, changed, "", &AffectedEvidence{Directories: []string{"internal/dependent"}, Incomplete: incomplete})
		if judgment.Tests[0].Touched || !judgment.Confined || !strings.Contains(judgment.Reason, "was not used, because "+incomplete) {
			t.Fatalf("with %q, judgment = %#v, want only the path rule to decide", incomplete, judgment)
		}
	}
	touched := JudgeTouched(named(FailingTest{Name: "TestBeside", File: "internal/changed/changed_test.go"}), changed, "", &AffectedEvidence{Incomplete: "the analyser could not tell"})
	if touched.Confined || !touched.Tests[0].Touched {
		t.Fatalf("judgment = %#v, want the path rule to still find the test touched", touched)
	}
}

// Five untouched failing tests are confined to untouched tests; six are an
// ordinary failure, and so are tests named past the ones the reading kept.
func TestSixFailingTestsAreAnOrdinaryFailure(t *testing.T) {
	t.Parallel()

	var tests []FailingTest
	for index := 1; index <= 6; index++ {
		tests = append(tests, FailingTest{Name: fmt.Sprintf("TestFlaky%d", index), File: fmt.Sprintf("internal/far%d/far_test.go", index)})
	}
	changed := []string{"internal/near/near.go"}
	if five := JudgeTouched(named(tests[:5]...), changed, "", nil); !five.Confined {
		t.Fatalf("five untouched failing tests = %#v, want them confined", five)
	}
	six := JudgeTouched(named(tests...), changed, "", nil)
	if six.Confined || !strings.Contains(six.Reason, "6 tests failed, more than the 5") {
		t.Fatalf("six untouched failing tests = %#v, want an ordinary failure saying why", six)
	}
	for _, test := range six.Tests {
		if test.Touched {
			t.Fatalf("%s = %#v, want each still judged untouched", test.Name, test)
		}
	}
	more := JudgeTouched(FailingTests{Tests: tests[:2], More: 4}, changed, "", nil)
	if more.Confined {
		t.Fatalf("two kept and four more = %#v, want an ordinary failure", more)
	}
}

// A failure with any failing test the reading could not name with its file is
// an ordinary failure, judged no further.
func TestOneUnnamedTestIsAnOrdinaryFailure(t *testing.T) {
	t.Parallel()

	unread := FailingTests{Unread: "TestNoFile was named failing without the file it is in"}
	judgment := JudgeTouched(unread, []string{"docs/x.md"}, "", nil)
	if judgment.Confined || len(judgment.Tests) != 0 || !strings.Contains(judgment.Reason, "could be named with its file: TestNoFile was named failing without the file it is in") {
		t.Fatalf("judgment = %#v, want an ordinary failure naming the unnamed test", judgment)
	}
	if empty := JudgeTouched(FailingTests{}, nil, "", nil); empty.Confined {
		t.Fatalf("a reading that named nothing = %#v, want an ordinary failure", empty)
	}
}

// Where the change's paths are not known, no failure is confined.
func TestUnknownChangedPathsAreAnOrdinaryFailure(t *testing.T) {
	t.Parallel()

	judgment := JudgeTouched(named(FailingTest{Name: "TestFar", File: "internal/far/far_test.go"}), nil, "the listing failed", nil)
	if judgment.Confined || !strings.Contains(judgment.Reason, "could not be listed (the listing failed)") {
		t.Fatalf("judgment = %#v, want an ordinary failure", judgment)
	}
}

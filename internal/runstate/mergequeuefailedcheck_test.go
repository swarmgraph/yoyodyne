package runstate

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/checks"
)

func TestAFailedChecksRecordIsHeldToItsBounds(t *testing.T) {
	t.Parallel()

	tests := &checks.FailingTests{Tests: []checks.FailingTest{{Name: "TestA", File: "a_test.go"}}}
	for _, tc := range []struct {
		name   string
		failed MergeQueueFailedCheck
		valid  bool
	}{
		{"a whole record", MergeQueueFailedCheck{Command: "make test", Tests: tests, Touched: &checks.TouchedTests{Tests: []checks.JudgedTest{{Name: "TestA", File: "a_test.go"}}}, Output: "--- FAIL: TestA"}, true},
		{"no check named", MergeQueueFailedCheck{Output: "boom"}, false},
		{"output past its bound", MergeQueueFailedCheck{Command: "make vet", Output: strings.Repeat("x", MaxMergeQueueCheckOutputBytes+1)}, false},
		{"judged tests with no failing tests", MergeQueueFailedCheck{Command: "make test", Touched: &checks.TouchedTests{}}, false},
		{"tests GitHub's log names", MergeQueueFailedCheck{Command: "test", ForgeTests: &ForgeTestJudgment{Tests: []ForgeTest{{Name: "TestA", Package: "example.com/shop/a"}}}, Output: "FAIL"}, true},
		{"tests named both ways", MergeQueueFailedCheck{Command: "test", Tests: tests, ForgeTests: &ForgeTestJudgment{}}, false},
		{"an unread log beside its output", MergeQueueFailedCheck{Command: "test", Unread: "HTTP 410", Output: "FAIL"}, false},
	} {
		if err := tc.failed.validate(); (err == nil) != tc.valid {
			t.Errorf("%s: validate() = %v, want valid %v", tc.name, err, tc.valid)
		}
	}
}

func TestAFailedCheckNamesEachTestItsDirectoryAndWhetherTheChangeTouchesIt(t *testing.T) {
	t.Parallel()

	one := MergeQueueFailedCheck{
		Command: "make test",
		Tests:   &checks.FailingTests{Tests: []checks.FailingTest{{Name: "TestA", File: "internal/beads/a_test.go"}}},
		Touched: &checks.TouchedTests{Tests: []checks.JudgedTest{{Name: "TestA", File: "internal/beads/a_test.go"}}},
	}
	if got, want := one.DescribeTests(), "Failing test: TestA in internal/beads (internal/beads/a_test.go), a directory the change does not touch."; got != want {
		t.Errorf("one failing test = %q, want %q", got, want)
	}

	several := MergeQueueFailedCheck{
		Command: "make test",
		Tests: &checks.FailingTests{More: 3, Tests: []checks.FailingTest{
			{Name: "TestA", File: "internal/beads/a_test.go"}, {Name: "TestB", File: "internal/chat/b_test.go"},
		}},
		Touched: &checks.TouchedTests{Tests: []checks.JudgedTest{
			{Name: "TestA", File: "internal/beads/a_test.go"},
			{Name: "TestB", File: "internal/chat/b_test.go", Touched: true, Why: "its own file changed"},
		}},
	}
	want := "Failing tests: TestA in internal/beads (internal/beads/a_test.go), a directory the change does not touch; " +
		"TestB in internal/chat (internal/chat/b_test.go), a directory the change touches: its own file changed; and 3 more, not judged."
	if got := several.DescribeTests(); got != want {
		t.Errorf("several failing tests = %q, want %q", got, want)
	}

	unread := MergeQueueFailedCheck{Command: "make test", Tests: &checks.FailingTests{Unread: "the output names no failing test"}, Touched: &checks.TouchedTests{}}
	if got := unread.DescribeTests(); !strings.Contains(got, "could not be named") {
		t.Errorf("unreadable failure = %q, want it to say the tests could not be named", got)
	}
	if got := (MergeQueueFailedCheck{Command: "make vet", Output: "vet: x"}).DescribeTests(); got != "" {
		t.Errorf("a check no reading applies to = %q, want nothing", got)
	}
}

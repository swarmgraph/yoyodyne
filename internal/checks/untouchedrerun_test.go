package checks

import (
	"slices"
	"strings"
	"testing"
)

func confinedJudgment() *TouchedTests {
	return &TouchedTests{
		Tests:    []JudgedTest{{Name: "TestTotals", File: "internal/alpha/alpha_test.go"}, {Name: "TestTop", File: "top_test.go"}, {Name: "TestMore", File: "internal/alpha/more_test.go"}},
		Confined: true,
		Reason:   "Every failing test is one the change does not touch.",
	}
}

// Only a failure confined to untouched tests is run once more, once per check
// stage and candidate commit, told its tests and their directories.
func TestAFailureConfinedToUntouchedTestsIsRunOnceMorePerCommit(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"
	plan := PlanUntouchedRerun(confinedJudgment(), commit, 0)
	want := []string{"TestTotals", "TestTop", "TestMore", "./internal/alpha", "."}
	if !plan.Run || !slices.Equal(plan.Names, want) || plan.Why != "" {
		t.Fatalf("PlanUntouchedRerun() = %#v, want a rerun told %v", plan, want)
	}
	if got := FailingTestsEnv(plan.Names); got != "YOYODYNE_FAILING_TESTS=TestTotals\nTestTop\nTestMore\n./internal/alpha\n." {
		t.Fatalf("FailingTestsEnv() = %q", got)
	}

	touched := confinedJudgment()
	touched.Confined = false
	for name, refused := range map[string]UntouchedRerunPlan{
		"no reading":    PlanUntouchedRerun(nil, commit, 0),
		"not confined":  PlanUntouchedRerun(touched, commit, 0),
		"no commit":     PlanUntouchedRerun(confinedJudgment(), " ", 0),
		"already spent": PlanUntouchedRerun(confinedJudgment(), commit, MaxUntouchedReruns),
	} {
		if refused.Run || len(refused.Names) != 0 || strings.TrimSpace(refused.Why) == "" {
			t.Errorf("%s: PlanUntouchedRerun() = %#v, want no rerun and the reason", name, refused)
		}
	}
	if MaxUntouchedReruns != 1 {
		t.Fatalf("MaxUntouchedReruns = %d, want exactly one", MaxUntouchedReruns)
	}
}

package runstate

import (
	"strings"
	"testing"
)

func TestACIResultCountsOnlyForTheExactCandidateRequiredChecksAndWorkflowFiles(t *testing.T) {
	t.Parallel()

	candidate := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	end := func() *CIWaitEnd {
		return &CIWaitEnd{
			Commit: candidate, Passed: true, WorkflowDigest: "sha256:one",
			Checks: []CICheckEvidence{{Name: "test", Result: "passed", Commit: candidate}, {Name: "race", Result: "passed", Commit: candidate}},
		}
	}
	required := []string{"test", "race"}

	if ok, why := end().CountsFor(candidate, required, "sha256:one"); !ok {
		t.Fatalf("a passing result for the candidate did not count: %s", why)
	}

	cases := map[string]func() (bool, string){
		"a later change to the candidate": func() (bool, string) { return end().CountsFor(other, required, "sha256:one") },
		"a check added to required": func() (bool, string) {
			return end().CountsFor(candidate, []string{"test", "race", "vet"}, "sha256:one")
		},
		"a check removed from required": func() (bool, string) { return end().CountsFor(candidate, []string{"test"}, "sha256:one") },
		"a required check renamed":      func() (bool, string) { return end().CountsFor(candidate, []string{"test", "lint"}, "sha256:one") },
		"a change to the workflow file": func() (bool, string) { return end().CountsFor(candidate, required, "sha256:two") },
		"no workflow digest recorded": func() (bool, string) {
			e := end()
			e.WorkflowDigest = ""
			return e.CountsFor(candidate, required, "")
		},
		"a check that ran on another pull request head": func() (bool, string) {
			e := end()
			e.Checks[1].Commit = other
			return e.CountsFor(candidate, required, "sha256:one")
		},
		"a result recorded for another head": func() (bool, string) {
			e := end()
			e.Commit = other
			return e.CountsFor(candidate, required, "sha256:one")
		},
		"a check that did not pass": func() (bool, string) {
			e := end()
			e.Checks[0].Result = "failed"
			return e.CountsFor(candidate, required, "sha256:one")
		},
		"a wait that did not end passing": func() (bool, string) {
			e := end()
			e.Passed = false
			return e.CountsFor(candidate, required, "sha256:one")
		},
		"no required checks": func() (bool, string) { return end().CountsFor(candidate, nil, "sha256:one") },
		"no result at all":   func() (bool, string) { return (*CIWaitEnd)(nil).CountsFor(candidate, required, "sha256:one") },
	}
	for name, run := range cases {
		if ok, why := run(); ok || why == "" {
			t.Errorf("%s: the result still counted (%v, %q)", name, ok, why)
		}
	}
}

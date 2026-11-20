package runstate

// GitHub's checks failing only in tests a change does not touch, and the one
// further run of the failing job the harness asks GitHub for before it
// withdraws the merge.
//
// A test can fail on GitHub's runner for reasons that have nothing to do with
// the change being merged: a busy machine, a timing assumption, a test that
// fails one run in fifty. When every failing test GitHub's log names is one the
// change does not touch, the harness asks GitHub to run the failing job once
// more on the same commit before it withdraws anything. The request is written
// down before it is made, and one commit is given one such request however
// often the harness restarts: a test that fails twice on the same commit is not
// the runner having a bad minute, and what follows is what followed before.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// MaxUntouchedForgeTests is how many failing tests a failure may name and
	// still count as confined to tests the change does not touch. More than
	// this failing at once is not a flaky test.
	MaxUntouchedForgeTests = 5
	// maxRecordedForgeTests bounds the failing tests one judgment keeps.
	maxRecordedForgeTests = 20
	// maxForgeTestNameBytes bounds a test's or a package's name.
	maxForgeTestNameBytes = 300
	// maxForgeTestUnnamedBytes bounds why a failure could not be read as tests.
	maxForgeTestUnnamedBytes = 600
)

// ForgeTest is one failing test GitHub's log of a failing job names: the test,
// the package it is in, and whether the change touches it.
type ForgeTest struct {
	Name    string `json:"name"`
	Package string `json:"package"`
	// Touched is the change having changed a file in the test's own directory,
	// or one at the top of the repository that reaches every package.
	Touched bool `json:"touched,omitempty"`
}

// ForgeTestJudgment is what GitHub's logs of the failing jobs say about which
// tests failed, and whether the change touches any of them.
type ForgeTestJudgment struct {
	Tests []ForgeTest `json:"tests,omitempty"`
	// More is how many further failing tests were named past the ones kept.
	More int `json:"more,omitempty"`
	// Unnamed says why some part of the failure could not be read as named
	// tests, and is empty where every failure was. A failure that is not all
	// named tests is never judged confined to untouched tests.
	Unnamed string `json:"unnamed,omitempty"`
}

// Untouched reports a failure confined to tests the change does not touch:
// every failure named as a test, at least one and at most
// MaxUntouchedForgeTests of them, and none of them touched.
func (j ForgeTestJudgment) Untouched() bool {
	if j.Unnamed != "" || len(j.Tests) == 0 || len(j.Tests)+j.More > MaxUntouchedForgeTests {
		return false
	}
	for _, test := range j.Tests {
		if test.Touched {
			return false
		}
	}
	return true
}

// Describe says in plain words which tests failed and whether the change
// touched them.
func (j ForgeTestJudgment) Describe() string {
	var touched, untouched []string
	for _, test := range j.Tests {
		named := test.Name
		if test.Package != "" {
			named += " in " + test.Package
		}
		if test.Touched {
			touched = append(touched, named)
		} else {
			untouched = append(untouched, named)
		}
	}
	var parts []string
	if len(touched) > 0 {
		parts = append(parts, fmt.Sprintf("the failing tests the change touches: %s", strings.Join(touched, ", ")))
	}
	if len(untouched) > 0 {
		parts = append(parts, fmt.Sprintf("the failing tests the change does not touch: %s", strings.Join(untouched, ", ")))
	}
	if j.More > 0 {
		parts = append(parts, fmt.Sprintf("%d more failing test(s) not listed", j.More))
	}
	if j.Unnamed != "" {
		parts = append(parts, "not every failure could be named as a test: "+j.Unnamed)
	}
	if len(parts) == 0 {
		return "GitHub's log named no failing test"
	}
	said := strings.Join(parts, "; ")
	if total := len(j.Tests) + j.More; j.Unnamed == "" && total > MaxUntouchedForgeTests && len(touched) == 0 {
		said += fmt.Sprintf("; %d tests failing at once is more than the %d a flaky test is allowed", total, MaxUntouchedForgeTests)
	}
	return said
}

func (j ForgeTestJudgment) validate() []error {
	var problems []error
	if len(j.Tests) > maxRecordedForgeTests {
		problems = append(problems, fmt.Errorf("%d failing tests are recorded, which exceeds the bound of %d", len(j.Tests), maxRecordedForgeTests))
	}
	if j.More < 0 {
		problems = append(problems, errors.New("a count of further failing tests cannot be negative"))
	}
	if len(j.Unnamed) > maxForgeTestUnnamedBytes {
		problems = append(problems, fmt.Errorf("why a failure could not be named is at most %d bytes", maxForgeTestUnnamedBytes))
	}
	for index, test := range j.Tests {
		if strings.TrimSpace(test.Name) == "" || len(test.Name) > maxForgeTestNameBytes || len(test.Package) > maxForgeTestNameBytes {
			problems = append(problems, fmt.Errorf("failing test %d must have a name, and name and package at most %d bytes", index, maxForgeTestNameBytes))
		}
	}
	return problems
}

// ForgeChecksRerun is the one further run of GitHub's failing jobs the harness
// asked for on a commit whose failure was confined to tests the change does not
// touch. It is written down before GitHub is asked, so a harness that restarts
// in between finds it and never asks a second time for the same commit.
type ForgeChecksRerun struct {
	// HeadCommit is the commit the failing jobs ran on and are run again on.
	HeadCommit string    `json:"head_commit"`
	RecordedAt time.Time `json:"recorded_at"`
	// Checks and CheckRuns are the failing checks and GitHub's ids for the runs
	// that failed. GitHub gives the second run an id of its own, so a reading
	// that still names only these was taken before the second run began.
	Checks    []string `json:"checks"`
	CheckRuns []int64  `json:"check_runs"`
	// Tests is the first failure's judgment, kept beside whatever the second
	// run brings.
	Tests ForgeTestJudgment `json:"tests"`
	// AskedAt is when GitHub accepted the request. Refused says why it did not;
	// a request recorded with neither was cut off before GitHub answered, and
	// is never asked again.
	AskedAt *time.Time `json:"asked_at,omitempty"`
	Refused string     `json:"refused,omitempty"`
}

// Asked reports a request GitHub accepted.
func (r ForgeChecksRerun) Asked() bool { return r.AskedAt != nil && r.Refused == "" }

// Awaiting reports a reading on the same commit whose every failing check run
// is one the request named: it was taken before the second run began, and
// says nothing new.
func (r ForgeChecksRerun) Awaiting(head string, failing []int64) bool {
	if !r.Asked() || head != r.HeadCommit || len(failing) == 0 {
		return false
	}
	asked := make(map[int64]bool, len(r.CheckRuns))
	for _, id := range r.CheckRuns {
		asked[id] = true
	}
	for _, id := range failing {
		if id <= 0 || !asked[id] {
			return false
		}
	}
	return true
}

// Describe says what became of the request, in plain words.
func (r ForgeChecksRerun) Describe() string {
	head := r.HeadCommit
	if len(head) > 12 {
		head = head[:12]
	}
	what := fmt.Sprintf("GitHub's failing job (%s) failed on %s only in tests this change does not touch (%s)", strings.Join(r.Checks, ", "), head, r.Tests.Describe())
	switch {
	case r.Refused != "":
		return what + ", and GitHub would not run it once more: " + r.Refused
	case r.AskedAt == nil:
		return what + ", and the request to run it once more was cut off before GitHub answered, so it is not asked again"
	default:
		return what + fmt.Sprintf(", so the harness asked GitHub at %s to run it once more on the same commit", r.AskedAt.UTC().Format(time.RFC3339))
	}
}

// Validate rejects a request that cannot describe a real one.
func (r ForgeChecksRerun) Validate() error {
	var problems []error
	if !commitPattern.MatchString(r.HeadCommit) {
		problems = append(problems, errors.New("a further run of GitHub's checks names an invalid commit"))
	}
	if r.RecordedAt.IsZero() {
		problems = append(problems, errors.New("a further run of GitHub's checks says when it was recorded"))
	}
	if len(r.CheckRuns) == 0 || len(r.CheckRuns) > MaxRecordedFailingChecks || len(r.Checks) > MaxRecordedFailingChecks {
		problems = append(problems, fmt.Errorf("a further run of GitHub's checks names between 1 and %d check runs", MaxRecordedFailingChecks))
	}
	for _, name := range r.Checks {
		if strings.TrimSpace(name) == "" || len(name) > maxCheckNameBytes {
			problems = append(problems, fmt.Errorf("a further run of GitHub's checks names a check that is empty or longer than %d bytes", maxCheckNameBytes))
			break
		}
	}
	for _, id := range r.CheckRuns {
		if id <= 0 {
			problems = append(problems, errors.New("a further run of GitHub's checks names a check run that is not one"))
			break
		}
	}
	if len(r.Refused) > maxForgeTestUnnamedBytes {
		problems = append(problems, fmt.Errorf("why GitHub refused a further run is at most %d bytes", maxForgeTestUnnamedBytes))
	}
	problems = append(problems, r.Tests.validate()...)
	return errors.Join(problems...)
}

// Extends reports why now cannot follow was as the record of the same
// request, or "" where it can: what it asked for is fixed when it is written
// down, and its answer, once recorded, is never changed.
func (r ForgeChecksRerun) Extends(now *ForgeChecksRerun) string {
	if now == nil {
		return "a further run of GitHub's checks, once recorded, is never removed"
	}
	if now.HeadCommit != r.HeadCommit || !now.RecordedAt.Equal(r.RecordedAt) || !equalInt64s(now.CheckRuns, r.CheckRuns) {
		return "what a further run of GitHub's checks asks for is fixed when it is recorded"
	}
	if (r.AskedAt != nil && (now.AskedAt == nil || !now.AskedAt.Equal(*r.AskedAt))) || (r.Refused != "" && now.Refused != r.Refused) {
		return "GitHub's answer to a further run, once recorded, is never changed"
	}
	return ""
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

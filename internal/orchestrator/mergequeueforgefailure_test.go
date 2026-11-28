package orchestrator

// A merge queue candidate handed back over a required check GitHub failed
// names the failing tests GitHub's log names, each with its package and
// whether the change touches it, and keeps the end of the log; a log that
// cannot be read is said as such.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// heldReviewer holds every review until its context ends, so GitHub's checks
// settle first.
type heldReviewer struct{ started chan struct{} }

func (r *heldReviewer) Review(ctx context.Context, _ review.Request) (review.Result, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return review.Result{}, ctx.Err()
}

func TestACandidateHandedBackOverAFailedGitHubCheckNamesItsFailingTests(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		output publish.CheckOutput
		err    error
		says   []string
		tests  int
		unread bool
	}{
		{
			name: "one failing test",
			output: publish.CheckOutput{FailingTests: []string{"TestBeads"}, Tail: "--- FAIL: TestBeads (0.01s)\n" +
				"    beads_test.go:12: the store said no\nFAIL\nFAIL\texample.com/shop/internal/beads\t0.2s\nok  \texample.com/shop/internal/chat\t0.1s\n"},
			says: []string{
				"Failing test in GitHub's log: TestBeads in example.com/shop/internal/beads, a package the change does not touch",
			},
			tests: 1,
		},
		{
			name: "several failing tests, one whose package the end of the log does not name",
			output: publish.CheckOutput{FailingTests: []string{"TestEarlier", "TestBeads/sub", "TestChat"}, Cut: true, Tail: "--- FAIL: TestBeads (0.01s)\n" +
				"    --- FAIL: TestBeads/sub (0.00s)\nFAIL\nFAIL\texample.com/shop/internal/beads\t0.2s\n" +
				"--- FAIL: TestChat (0.02s)\n    chat_test.go:4: timed out\nFAIL\nFAIL\texample.com/shop/internal/chat\t0.3s\n"},
			says: []string{
				"Failing tests in GitHub's log: TestBeads in example.com/shop/internal/beads, a package the change does not touch",
				"TestChat in example.com/shop/internal/chat, a package the change does not touch",
				"TestEarlier, whose package the end of GitHub's log that was read does not name",
			},
			tests: 3,
		},
		{
			name:   "a log that cannot be read",
			err:    errors.New("read the forge's log of check \"test\": HTTP 410: the log has expired"),
			says:   []string{"GitHub's log of test could not be read, so its failing tests are not named here: read the forge's log of check \"test\": HTTP 410: the log has expired"},
			unread: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var base string
			f, forge := quickFixture(t, func(commit, name string, _ int) publish.CheckResult {
				if commit != base && name == "test" {
					return publish.CheckFailed
				}
				return publish.CheckPassed
			})
			base = gitLine(t, f.repository, "rev-parse", "main")
			forge.output = func(check publish.RequiredCheck) (publish.CheckOutput, error) {
				if check.Name != "test" {
					return publish.CheckOutput{}, errors.New("only test failed")
				}
				return tc.output, tc.err
			}
			f.worker.Pipeline.Reviewer = &heldReviewer{started: make(chan struct{}, 1)}
			if verification, err := f.work(); err != nil || verification.Verified {
				t.Fatalf("Work() = %#v, %v; want the candidate refused for the failed required check", verification, err)
			}
			generations := f.generations()
			recorded := generations[len(generations)-1].CIChecks.FailureOf("test")
			if recorded == nil {
				t.Fatalf("GitHub's checks = %#v, want what the failed job's log said recorded", generations[len(generations)-1].CIChecks)
			}
			if tc.unread != (recorded.Unread != "") || (!tc.unread && (recorded.ForgeTests == nil || len(recorded.ForgeTests.Tests) != tc.tests || recorded.Output == "")) {
				t.Fatalf("the failed job's record = %#v, want %d tests and the end of its log, or why it could not be read", recorded, tc.tests)
			}
			failure, failed, err := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{
				Generations: generations, Configured: generations[len(generations)-1].Checks,
			})
			if err != nil || !failed || failure.Class != runstate.MergeQueueCandidateDefect || failure.FailedCheck == nil || failure.FailedCheck.Command != "test" {
				t.Fatalf("ClassifyMergeQueueFailure() = %#v, %v, %v; want the change's defect carrying the failed job's record", failure, failed, err)
			}
			for _, want := range tc.says {
				if !strings.Contains(failure.Reason, want) {
					t.Fatalf("the hand-back's reason does not say %q:\n%s", want, failure.Reason)
				}
			}
			if !tc.unread && !strings.Contains(failure.FailedCheck.Output, "FAIL\texample.com/shop/internal/beads") {
				t.Fatalf("the hand-back keeps %q, want the end of GitHub's log", failure.FailedCheck.Output)
			}
		})
	}
}

// GitHub's failing tests are judged by the rule a run's own pull request's are:
// a changed file in the package's directory touches its tests.
func TestGitHubsFailingTestsAreJudgedAgainstTheCandidatesPaths(t *testing.T) {
	t.Parallel()

	output := publish.CheckOutput{FailingTests: []string{"TestBeads", "TestChat"}, Tail: "--- FAIL: TestBeads (0.01s)\nFAIL\nFAIL\texample.com/shop/internal/beads\t0.2s\n" +
		"--- FAIL: TestChat (0.01s)\nFAIL\nFAIL\texample.com/shop/internal/chat\t0.2s\n"}
	judged := judgeForgeLog(output, []string{"internal/beads/client.go"}, "")
	if len(judged.Tests) != 2 || !judged.Tests[0].Touched || judged.Tests[1].Touched || judged.Unnamed != "" {
		t.Fatalf("judgment = %#v, want TestBeads touched and TestChat not", judged)
	}
	unknown := judgeForgeLog(output, nil, "the candidate's paths could not be listed")
	if unknown.Tests[0].Touched || !strings.Contains(unknown.Unnamed, "could not be listed") || unknown.Untouched() {
		t.Fatalf("judgment = %#v, want no test judged untouched without the candidate's paths", unknown)
	}
	described := runstate.MergeQueueFailedCheck{Command: "test", ForgeTests: &judged}.DescribeTests()
	if want := "Failing tests in GitHub's log: TestBeads in example.com/shop/internal/beads, a package the change touches; TestChat in example.com/shop/internal/chat, a package the change does not touch."; described != want {
		t.Fatalf("described = %q, want %q", described, want)
	}
}

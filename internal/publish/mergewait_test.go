package publish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// recordedMergeWait is a forge answering the merge-wait question with one of
// the replies recorded under testdata/mergewait.
func recordedMergeWait(t *testing.T, name string) *scriptedRunner {
	t.Helper()
	reply, err := os.ReadFile(filepath.Join("testdata", "mergewait", name))
	if err != nil {
		t.Fatalf("read the recorded reply %s: %v", name, err)
	}
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("mergeStateStatus", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(reply)})
	return runner
}

// A merge held on a required check still running is read as exactly that: the
// required check named, the one no rule requires left out, and GitHub's own
// state words kept beside the plain ones.
func TestGitHubMergeWaitNamesARequiredCheckStillRunning(t *testing.T) {
	t.Parallel()

	runner := recordedMergeWait(t, "pending-required-check.json")
	wait, err := (GitHub{Runner: runner}).MergeWait(context.Background(), 1828)
	if err != nil {
		t.Fatalf("MergeWait() error = %v", err)
	}
	asked := runner.matching("mergeStateStatus")
	if len(asked) != 1 || !contains(asked[0], "number=1828") || apiRepositoryScope(runner) == "" {
		t.Fatalf("query = %v (scope %q), want pull request 1828 asked about in the configured repository", asked, apiRepositoryScope(runner))
	}
	if len(wait.Required) != 2 || wait.Required[0].Name != "build" || wait.Required[0].State != RequiredCheckPending || wait.Required[1].State != RequiredCheckPassed {
		t.Fatalf("required checks = %#v, want build pending and race passed, and the optional check left out", wait.Required)
	}
	says := wait.WaitingOn()
	for _, want := range []string{`the required check "build" has not finished`, "merge state BLOCKED", "mergeable MERGEABLE"} {
		if !strings.Contains(says, want) {
			t.Errorf("WaitingOn() = %q, want it to say %q", says, want)
		}
	}
	if strings.Contains(says, "docs-preview") || len(wait.FailedRequired()) != 0 {
		t.Errorf("WaitingOn() = %q, failed %v; want nothing said of a check no rule requires", says, wait.FailedRequired())
	}
	for _, verb := range []string{"pr merge", "dequeuePullRequest", "rerun"} {
		if found := runner.matching(verb); len(found) != 0 {
			t.Errorf("reading the wait ran %v", found)
		}
	}
}

// A merge blocked by a branch rule with every required check passed names the
// rule GitHub reports: here an approving review it requires.
func TestGitHubMergeWaitNamesTheBranchRuleBlockingTheMerge(t *testing.T) {
	t.Parallel()

	wait, err := (GitHub{Runner: recordedMergeWait(t, "branch-rule.json")}).MergeWait(context.Background(), 1667)
	if err != nil {
		t.Fatalf("MergeWait() error = %v", err)
	}
	if len(wait.Required) != 2 || len(wait.PendingRequired()) != 0 || len(wait.FailedRequired()) != 0 {
		t.Fatalf("required checks = %#v, want the check run and the status both read as passed", wait.Required)
	}
	says := wait.WaitingOn()
	if !strings.Contains(says, "requires an approving review") || !strings.Contains(says, "merge state BLOCKED") {
		t.Errorf("WaitingOn() = %q, want the required review named with GitHub's BLOCKED", says)
	}
	// With no review asked for, BLOCKED and every required check green still
	// says a rule is unmet rather than nothing.
	wait.ReviewDecision = ""
	if says := wait.WaitingOn(); !strings.Contains(says, "a branch rule of the target is not met") {
		t.Errorf("WaitingOn() = %q, want an unnamed branch rule said", says)
	}
}

// A required check that failed is named with how it ended, and a failing
// check no rule requires is not.
func TestGitHubMergeWaitNamesAFailedRequiredCheck(t *testing.T) {
	t.Parallel()

	wait, err := (GitHub{Runner: recordedMergeWait(t, "failed-required-check.json")}).MergeWait(context.Background(), 1704)
	if err != nil {
		t.Fatalf("MergeWait() error = %v", err)
	}
	failed := wait.FailedRequired()
	if len(failed) != 1 || failed[0].Name != "build" || failed[0].ID != 7301 || failed[0].Conclusion != "failure" {
		t.Fatalf("failed required checks = %#v, want the build alone", failed)
	}
	if says := wait.WaitingOn(); !strings.Contains(says, `the required check "build" ended in failure`) || strings.Contains(says, "docs-preview") {
		t.Errorf("WaitingOn() = %q, want the failed build named and nothing else", says)
	}
}

// A read that fails — GitHub out of reach, a certificate that cannot be
// checked, or an answer carrying an error — fails the whole reading, so the
// caller records it as unread rather than as GitHub's account.
func TestGitHubMergeWaitThatCannotBeReadFails(t *testing.T) {
	t.Parallel()

	unreachable := &scriptedRunner{}
	unreachable.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	unreachable.reply("mergeStateStatus", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1,
		Stderr: `Post "https://api.github.com/graphql": tls: failed to verify certificate: x509: certificate signed by unknown authority`})
	if _, err := (GitHub{Runner: unreachable}).MergeWait(context.Background(), 1710); err == nil || !strings.Contains(err.Error(), "x509") {
		t.Errorf("MergeWait() error = %v, want the certificate failure named", err)
	}

	broken := &scriptedRunner{}
	broken.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	broken.fail("mergeStateStatus", errors.New("dial tcp: lookup api.github.com: no such host"))
	if _, err := (GitHub{Runner: broken}).MergeWait(context.Background(), 1710); err == nil || !strings.Contains(err.Error(), "no such host") {
		t.Errorf("MergeWait() error = %v, want the unreachable host named", err)
	}

	refused := &scriptedRunner{}
	refused.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	refused.reply("mergeStateStatus", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":null,"errors":[{"message":"Could not resolve to a PullRequest with the number of 1710."}]}`})
	if _, err := (GitHub{Runner: refused}).MergeWait(context.Background(), 1710); err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Errorf("MergeWait() error = %v, want GitHub's own error named", err)
	}
}

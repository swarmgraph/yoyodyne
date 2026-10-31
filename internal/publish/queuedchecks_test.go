package publish

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// queueRunsAnswer is the forge's list of its merge queue's workflow runs, as gh
// selects it: two runs for pull request 1543, the newer on the merge the queue
// built at 06:22 PDT, and one for pull request 15430, which must not be taken
// for it.
const queueRunsAnswer = `[
	{"head_branch":"gh-readonly-queue/main/pr-15430-1111111111111111111111111111111111111111","head_sha":"2222222222222222222222222222222222222222","created_at":"2026-10-30T13:30:00Z"},
	{"head_branch":"gh-readonly-queue/main/pr-1543-3333333333333333333333333333333333333333","head_sha":"4444444444444444444444444444444444444444","created_at":"2026-10-30T13:22:00Z"},
	{"head_branch":"gh-readonly-queue/main/pr-1543-5555555555555555555555555555555555555555","head_sha":"6666666666666666666666666666666666666666","created_at":"2026-10-29T09:00:00Z"}
]`

// A pull request removed from the forge's merge queue for failed checks is read
// for the checks the queue ran on the merge it built, not the request's own
// head: the newest run on a branch the queue made for that request names the
// commit, and every check on it is read with its annotations.
func TestGitHubQueuedMergeChecksReadsTheChecksOnTheMergeTheQueueBuilt(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("actions/runs?event=merge_group", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: queueRunsAnswer})
	runner.reply("commits/4444444444444444444444444444444444444444/check-runs", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"check_runs":[
		{"id":901,"name":"test","status":"completed","conclusion":"failure","html_url":"https://example.invalid/acme/thing/actions/runs/9/job/901"},
		{"id":902,"name":"lint","status":"completed","conclusion":"success"},
		{"id":903,"name":"race","status":"in_progress","conclusion":null}
	]}`})
	runner.reply("check-runs/901/annotations", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `[
		{"path":".github","start_line":1,"annotation_level":"failure","message":"Process completed with exit code 2."}
	]`})

	queued, err := (GitHub{Runner: runner}).QueuedMergeChecks(context.Background(), 1543)
	if err != nil {
		t.Fatalf("QueuedMergeChecks() error = %v", err)
	}
	if !queued.Found() || queued.Commit != "4444444444444444444444444444444444444444" || !strings.Contains(queued.Branch, "/pr-1543-") {
		t.Fatalf("queued merge = %q on %q, want the newest merge the queue built of pull request 1543", queued.Commit, queued.Branch)
	}
	if len(queued.Failing) != 1 || queued.Failing[0].Name != "test" || queued.Failing[0].ID != 901 || queued.Failing[0].Conclusion != "failure" {
		t.Fatalf("failing = %#v, want the test check that failed on the queued merge", queued.Failing)
	}
	if len(queued.Failing[0].Annotations) != 1 || !strings.Contains(queued.Failing[0].Annotations[0].Message, "exit code 2") {
		t.Errorf("annotations = %#v, want the forge's own annotation kept", queued.Failing[0].Annotations)
	}
	if queued.Passing != 1 || len(queued.Pending) != 1 || queued.Pending[0] != "race" {
		t.Errorf("passing = %d, pending = %v; want lint passing and race still running", queued.Passing, queued.Pending)
	}
	if asked := runner.matching("pr-15430"); len(asked) != 0 {
		t.Errorf("asked about another request's queued merge: %v", asked)
	}
	if asked := runner.matching("commits/2222222222222222222222222222222222222222"); len(asked) != 0 {
		t.Errorf("read the checks of pull request 15430's queued merge: %v", asked)
	}
	// Nothing about the request was changed.
	for _, verb := range []string{"dequeuePullRequest", "pr merge", "pr close", "rerun"} {
		if found := runner.matching(verb); len(found) != 0 {
			t.Errorf("reading the queued merge's checks ran %v", found)
		}
	}
}

// A forge that lists no merge-queue run for the request says it found none,
// which is not a pass; a forge that cannot be asked, or whose checks on the
// queued merge cannot be read, fails the reading so the caller says the checks
// went unread.
func TestGitHubQueuedMergeChecksSaysWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	none := &scriptedRunner{}
	none.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	none.reply("actions/runs?event=merge_group", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: queueRunsAnswer})
	queued, err := (GitHub{Runner: none}).QueuedMergeChecks(context.Background(), 77)
	if err != nil || queued.Found() || queued.Number != 77 {
		t.Fatalf("QueuedMergeChecks() = %#v, %v; want nothing found and no error", queued, err)
	}
	if asked := none.matching("check-runs"); len(asked) != 0 {
		t.Errorf("read checks with no queued merge found: %v", asked)
	}

	refused := &scriptedRunner{}
	refused.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	refused.reply("actions/runs?event=merge_group", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "gh: Resource not accessible by integration (HTTP 403)"})
	if _, err := (GitHub{Runner: refused}).QueuedMergeChecks(context.Background(), 1543); err == nil || !strings.Contains(err.Error(), "merge queue's runs for pull request 1543") || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("QueuedMergeChecks() error = %v, want the refused listing named with the forge's words", err)
	}

	garbled := &scriptedRunner{}
	garbled.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	garbled.reply("actions/runs?event=merge_group", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "<html>bad gateway</html>"})
	if _, err := (GitHub{Runner: garbled}).QueuedMergeChecks(context.Background(), 1543); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("QueuedMergeChecks() error = %v, want an answer that is not the list said to be undecodable", err)
	}

	unreadChecks := &scriptedRunner{}
	unreadChecks.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	unreadChecks.reply("actions/runs?event=merge_group", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: queueRunsAnswer})
	unreadChecks.fail("check-runs", errors.New("connection reset"))
	if _, err := (GitHub{Runner: unreadChecks}).QueuedMergeChecks(context.Background(), 1543); err == nil || !strings.Contains(err.Error(), "checks on the queued merge of pull request 1543") {
		t.Errorf("QueuedMergeChecks() error = %v, want the unread checks named", err)
	}
}

// Only a branch the merge queue made for the request is that request's.
func TestQueuedForMatchesOnlyTheRequestsOwnQueueBranch(t *testing.T) {
	t.Parallel()

	for branch, want := range map[string]bool{
		"gh-readonly-queue/main/pr-15-abc":           true,
		"gh-readonly-queue/release/1.x/pr-15-abc":    true,
		"gh-readonly-queue/main/pr-154-abc":          false,
		"gh-readonly-queue/main/pr-1-abc":            false,
		"feature/pr-15-abc":                          false,
		"gh-readonly-queue/pr-15-abc-without-a-base": false,
	} {
		if got := queuedFor(branch, 15); got != want {
			t.Errorf("queuedFor(%q, 15) = %v, want %v", branch, got, want)
		}
	}
}

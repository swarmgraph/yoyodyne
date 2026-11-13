package publish

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func teamPullRequestRunner(request, commits string) *scriptedRunner {
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pulls/9 --jq", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: request})
	runner.reply("pulls/9/commits", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: commits})
	return runner
}

func TestTeamPullRequestReadsAnOpenRequestWithItsCommits(t *testing.T) {
	t.Parallel()
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	runner := teamPullRequestRunner(`{"state":"open","merged":false,"base":"main","head":"staging-batch","head_sha":"`+b+`","merge_commit":null,"url":"https://example.invalid/pr/9","commits":2}`, a+"\n"+b+"\n")
	got, err := GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}.TeamPullRequest(context.Background(), 9)
	if err != nil {
		t.Fatalf("TeamPullRequest() error = %v", err)
	}
	want := TeamPullRequest{Number: 9, URL: "https://example.invalid/pr/9", State: "OPEN", BaseBranch: "main", HeadBranch: "staging-batch", HeadCommit: b, Commits: []string{a, b}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TeamPullRequest() = %+v, want %+v", got, want)
	}
	found := runner.matching("pulls/9/commits")
	if len(found) != 1 || !contains(found[0], "--paginate") {
		t.Fatalf("commits were asked %v, want once with every page", found)
	}
}

func TestTeamPullRequestReadsAMergedRequestWithItsMergeCommit(t *testing.T) {
	t.Parallel()
	a, m := strings.Repeat("a", 40), strings.Repeat("e", 40)
	runner := teamPullRequestRunner(`{"state":"closed","merged":true,"base":"main","head":"x","head_sha":"`+a+`","merge_commit":"`+m+`","url":"u","commits":1}`, a+"\n")
	got, err := GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}.TeamPullRequest(context.Background(), 9)
	if err != nil || got.State != "MERGED" || !got.Merged || got.MergeCommit != m || got.CommitsCapped {
		t.Fatalf("TeamPullRequest() = %+v, %v, want merged with merge commit %s", got, err, m)
	}
}

func TestTeamPullRequestForgeFailuresAreFailedReads(t *testing.T) {
	t.Parallel()
	runner := teamPullRequestRunner(`{}`, "")
	runner.reply("pulls/9 --jq", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502: Bad Gateway"})
	if _, err := (GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}).TeamPullRequest(context.Background(), 9); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %v, want the forge's refusal", err)
	}

	runner = teamPullRequestRunner(`{"state":"open","commits":1}`, "")
	runner.reply("pulls/9/commits", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 500"})
	if _, err := (GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}).TeamPullRequest(context.Background(), 9); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, want the commit listing's refusal", err)
	}

	runner = teamPullRequestRunner(`{}`, "")
	runner.reply("pulls/9 --jq", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "gh: Not Found (HTTP 404)"})
	if _, err := (GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}).TeamPullRequest(context.Background(), 9); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want it to say the request does not exist", err)
	}

	runner = teamPullRequestRunner(`{"state":"open","commits":1}`, "not-a-commit\n")
	if _, err := (GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}).TeamPullRequest(context.Background(), 9); err == nil {
		t.Fatal("a listed value that is not a commit was accepted")
	}
	if _, err := (GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}).TeamPullRequest(context.Background(), 0); err == nil {
		t.Fatal("request number 0 was read")
	}
}

func TestTeamPullRequestSaysWhenTheCommitListMayBeIncomplete(t *testing.T) {
	t.Parallel()
	a := strings.Repeat("a", 40)
	for name, count := range map[string]string{"at the forge's limit": "250", "more than listed": "300"} {
		runner := teamPullRequestRunner(`{"state":"open","merged":false,"commits":`+count+`}`, a+"\n")
		got, err := GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}.TeamPullRequest(context.Background(), 9)
		if err != nil || !got.CommitsCapped {
			t.Errorf("%s: TeamPullRequest() = %+v, %v, want the listing marked capped", name, got, err)
		}
	}
}

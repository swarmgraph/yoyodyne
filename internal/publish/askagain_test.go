package publish

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const askedCommit = "0123456789abcdef0123456789abcdef01234567"

// A check that ended names its job, and asking for it again re-runs the job.
func TestAskChecksAgainRerunsTheJobOfACheckThatEnded(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("actions/jobs/4215/rerun", execution.ProcessResult{Status: execution.ProcessSucceeded})
	said, err := (GitHub{Runner: runner}).AskChecksAgain(context.Background(), askedCommit, 4215)
	if err != nil || !strings.Contains(said, "job 4215") || len(runner.matching("actions/jobs/4215/rerun")) != 1 {
		t.Fatalf("AskChecksAgain() = %q, %v; want the job run again", said, err)
	}
}

// A check GitHub never started names no job: the commit's workflow run stuck
// before starting is cancelled and run again, one that finished without
// passing is run again, and one that passed is left alone.
func TestAskChecksAgainRunsTheCommitsUnstartedWorkflowRunAgain(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("head_sha="+askedCommit, execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `[{"id":77,"name":"ci","status":"queued","conclusion":null},{"id":78,"name":"docs","status":"completed","conclusion":"skipped"},{"id":79,"name":"lint","status":"completed","conclusion":"success"}]`})
	runner.reply("runs/77/force-cancel", execution.ProcessResult{Status: execution.ProcessSucceeded})
	runner.reply("runs/77 --jq", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "completed\n"})
	runner.reply("runs/77/rerun", execution.ProcessResult{Status: execution.ProcessSucceeded})
	runner.reply("runs/78/rerun", execution.ProcessResult{Status: execution.ProcessSucceeded})
	said, err := (GitHub{Runner: runner}).AskChecksAgain(context.Background(), askedCommit, 0)
	if err != nil || !strings.Contains(said, "workflow run 77 (ci)") || !strings.Contains(said, "workflow run 78 (docs)") {
		t.Fatalf("AskChecksAgain() = %q, %v; want both runs asked for again", said, err)
	}
	if len(runner.matching("runs/77/force-cancel")) != 1 || len(runner.matching("runs/79/")) != 0 {
		t.Fatalf("calls = %v, want the stuck run cancelled first and the passed run left alone", runner.commands)
	}

	none := &scriptedRunner{}
	none.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	none.reply("head_sha=", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]"})
	if _, err := (GitHub{Runner: none}).AskChecksAgain(context.Background(), askedCommit, 0); err == nil || !strings.Contains(err.Error(), "no workflow run") {
		t.Fatalf("AskChecksAgain() with no run = %v, want it to say GitHub holds none", err)
	}
}

// A required check GitHub has queued but not begun, and one the project names
// that GitHub reports nothing for, are both unstarted; one running is not.
func TestMergeWaitNamesTheRequiredChecksGitHubHasNotStarted(t *testing.T) {
	t.Parallel()

	reply := `{"data":{"repository":{"pullRequest":{"mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED","commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
{"__typename":"CheckRun","name":"build","status":"QUEUED","conclusion":null,"databaseId":1,"isRequired":true},
{"__typename":"CheckRun","name":"race","status":"IN_PROGRESS","conclusion":null,"databaseId":2,"isRequired":true}
]}}}}]}}}}}`
	wait, err := decodeMergeWait(12, reply)
	if err != nil {
		t.Fatal(err)
	}
	got := wait.UnstartedRequired([]string{"build", "race", "test"})
	if strings.Join(got, ",") != "build,test" {
		t.Fatalf("UnstartedRequired() = %v, want the queued build and the unreported test", got)
	}
}

package publish

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func feedbackRunner() *scriptedRunner {
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pulls/7 --jq", execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `{"state":"open","merged":false}`})
	runner.reply("pulls/7/reviews", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: strings.Join([]string{
		`{"id":11,"author":"ada","state":"CHANGES_REQUESTED","body":"Please rename the flag.","submitted_at":"2026-11-02T10:00:00Z","commit_id":"abc","html_url":"https://example.invalid/r/11"}`,
		`{"id":12,"author":"grace","state":"APPROVED","body":"","submitted_at":"2026-11-02T11:00:00Z","commit_id":"abc","html_url":"https://example.invalid/r/12"}`,
	}, "\n") + "\n"})
	runner.reply("pulls/7/comments", execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `{"id":21,"author":"ada","body":"This line leaks.","created_at":"2026-11-02T10:01:00Z","path":"main.go","line":null,"original_line":14,"html_url":"https://example.invalid/c/21"}` + "\n"})
	runner.reply("issues/7/comments", execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `{"id":31,"author":"linus","body":"Why this approach?","created_at":"2026-11-02T12:00:00Z","html_url":"https://example.invalid/c/31"}` + "\n"})
	return runner
}

// Feedback reads all three kinds of feedback, every page of each, with who left
// it, when, and where on the change.
func TestFeedbackReadsReviewsLineCommentsAndCommentsWithTheirAuthors(t *testing.T) {
	t.Parallel()
	runner := feedbackRunner()
	reading, err := GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}.Feedback(context.Background(), 7)
	if err != nil {
		t.Fatalf("Feedback() error = %v", err)
	}
	if reading.State != "OPEN" {
		t.Fatalf("reading = state %q, want OPEN", reading.State)
	}
	if len(reading.Reviews) != 2 || reading.Reviews[0].State != ReviewChangesRequested || reading.Reviews[0].Author != "ada" ||
		!reading.Reviews[0].At.Equal(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)) || reading.Reviews[1].State != ReviewApproved {
		t.Fatalf("reviews = %+v", reading.Reviews)
	}
	if len(reading.LineComments) != 1 || reading.LineComments[0].Path != "main.go" || reading.LineComments[0].Line != 14 {
		t.Fatalf("line comments = %+v, want main.go at its original line 14", reading.LineComments)
	}
	if len(reading.Comments) != 1 || reading.Comments[0].Author != "linus" || reading.Comments[0].Path != "" {
		t.Fatalf("comments = %+v", reading.Comments)
	}
	for _, listing := range []string{"pulls/7/reviews", "pulls/7/comments", "issues/7/comments"} {
		found := runner.matching(listing)
		if len(found) != 1 || !contains(found[0], "--paginate") {
			t.Fatalf("%s was asked %v, want once with every page", listing, found)
		}
	}
}

// A merged request is said as merged whatever state the forge gives it.
func TestFeedbackSaysAMergedRequestIsMerged(t *testing.T) {
	t.Parallel()
	runner := feedbackRunner()
	runner.reply("pulls/7 --jq", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"state":"closed","merged":true,"opener":"factory-bot"}`})
	reading, err := GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}.Feedback(context.Background(), 7)
	if err != nil || reading.State != "MERGED" {
		t.Fatalf("Feedback() = %q, %v, want MERGED", reading.State, err)
	}
}

// A forge that refuses a listing is a failed read, never an empty one: no
// feedback read is not the same as none given.
func TestFeedbackAForgeThatRefusesIsAFailedReadNotAnEmptyOne(t *testing.T) {
	t.Parallel()
	runner := feedbackRunner()
	runner.reply("issues/7/comments", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502: Bad Gateway"})
	if _, err := (GitHub{Runner: runner, Binary: "gh", GitBinary: "git"}).Feedback(context.Background(), 7); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("Feedback() error = %v, want the forge's refusal", err)
	}
}

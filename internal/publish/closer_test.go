package publish

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Who closed a pull request is read off its timeline's last closing event, and
// reading it changes nothing about the request.
func TestGitHubCloserReadsWhoClosedTheRequestAndWhen(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("CLOSED_EVENT", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{"pullRequest":{"closedAt":"2026-10-30T02:15:00Z","timelineItems":{"nodes":[
		{"createdAt":"2026-10-30T02:15:00Z","actor":{"login":"octo-reviewer"}}
	]}}}}}`})

	closure, err := (GitHub{Runner: runner}).Closer(context.Background(), 812)
	if err != nil {
		t.Fatalf("Closer() error = %v", err)
	}
	asked := runner.matching("CLOSED_EVENT")
	if len(asked) != 1 || !contains(asked[0], "number=812") || apiRepositoryScope(runner) == "" {
		t.Fatalf("closure query = %v (scope %q), want request 812 asked about in the configured repository", asked, apiRepositoryScope(runner))
	}
	want := Closer{Number: 812, By: "octo-reviewer", At: time.Date(2026, 10, 30, 2, 15, 0, 0, time.UTC)}
	if closure.Number != want.Number || closure.By != want.By || !closure.At.Equal(want.At) {
		t.Errorf("Closer() = %#v, want %#v", closure, want)
	}
	for _, verb := range []string{"pr merge", "pr close", "pr reopen"} {
		if found := runner.matching(verb); len(found) != 0 {
			t.Errorf("reading the closure ran %v", found)
		}
	}
}

// A closing the forge recorded without an actor is answered with nobody named
// and the request's own closing time; a request with no closing at all is an
// error, because the caller only asks about a request it was told is closed.
func TestGitHubCloserSaysWhatTheForgeDidNotRecord(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("CLOSED_EVENT", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{"pullRequest":{"closedAt":"2026-10-30T02:15:00Z","timelineItems":{"nodes":[]}}}}}`})
	closure, err := (GitHub{Runner: runner}).Closer(context.Background(), 812)
	if err != nil {
		t.Fatalf("Closer() error = %v", err)
	}
	if closure.By != "" || !closure.At.Equal(time.Date(2026, 10, 30, 2, 15, 0, 0, time.UTC)) {
		t.Errorf("Closer() = %#v, want nobody named, closed at the request's closing time", closure)
	}

	open := &scriptedRunner{}
	open.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	open.reply("CLOSED_EVENT", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{"pullRequest":{"closedAt":null,"timelineItems":{"nodes":[]}}}}}`})
	if _, err := (GitHub{Runner: open}).Closer(context.Background(), 812); err == nil || !strings.Contains(err.Error(), "no closing of pull request 812") {
		t.Errorf("Closer() error = %v, want a request with no closing refused", err)
	}
}

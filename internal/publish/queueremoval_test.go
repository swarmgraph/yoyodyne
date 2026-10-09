package publish

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A merge the forge stopped holding is explained in the forge's own words: the
// reason on the last removal from its merge queue, and the requirement its merge
// state reports unmet. Both are read, not asked for: nothing here changes what
// the forge holds.
func TestGitHubQueueRemovalReadsTheForgesOwnReasonAndUnmetRequirement(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("timelineItems", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{"pullRequest":{"timelineItems":{"nodes":[
		{"__typename":"RemovedFromMergeQueueEvent","createdAt":"2026-10-05T20:32:00Z","reason":"Required status check \"build\" is expected."},
		{"__typename":"AddedToMergeQueueEvent","createdAt":"2026-10-05T20:10:00Z"},
		{"__typename":"AutoMergeDisabledEvent","createdAt":"2026-10-05T20:32:01Z","reason":"","reasonCode":"MERGE_QUEUE_REMOVAL"}
	]}}}}}`})
	runner.reply("pr view 713", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})

	removal, err := (GitHub{Runner: runner}).QueueRemoval(context.Background(), 713)
	if err != nil {
		t.Fatalf("QueueRemoval() error = %v", err)
	}
	asked := runner.matching("timelineItems")
	if len(asked) != 1 || !contains(asked[0], "number=713") || apiRepositoryScope(runner) == "" {
		t.Fatalf("timeline query = %v (scope %q), want request 713 asked about in the configured repository", asked, apiRepositoryScope(runner))
	}
	if len(removal.Events) != 3 || removal.Events[0].Kind != "AddedToMergeQueueEvent" {
		t.Fatalf("events = %#v, want all three, oldest first", removal.Events)
	}
	if got := removal.Reason(); got != "MERGE_QUEUE_REMOVAL" {
		t.Errorf("Reason() = %q, want the last removal's own words, which here are only its code", got)
	}
	if !strings.Contains(removal.Events[1].Describe(), `the forge's reason: "Required status check \"build\" is expected."`) {
		t.Errorf("removal described as %q, want the forge's reason quoted", removal.Events[1].Describe())
	}
	if !strings.Contains(removal.Requirement(), "protection rules are not satisfied (BLOCKED)") {
		t.Errorf("Requirement() = %q, want the BLOCKED merge state said in words", removal.Requirement())
	}
	// Nothing about the request was changed.
	for _, verb := range []string{"dequeuePullRequest", "pr merge", "pr close"} {
		if found := runner.matching(verb); len(found) != 0 {
			t.Errorf("reading the removal ran %v", found)
		}
	}
}

// A forge that gives no reason is recorded as having given none, and a merge
// state that could not be read is kept beside the events rather than replacing
// them; a timeline that could not be read fails the whole reading.
func TestGitHubQueueRemovalSaysWhatTheForgeDidNotSay(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("timelineItems", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{"pullRequest":{"timelineItems":{"nodes":[
		{"__typename":"RemovedFromMergeQueueEvent","createdAt":"2026-10-05T20:32:00Z","reason":null}
	]}}}}}`})
	runner.reply("pr view 713", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502"})
	removal, err := (GitHub{Runner: runner}).QueueRemoval(context.Background(), 713)
	if err != nil {
		t.Fatalf("QueueRemoval() error = %v", err)
	}
	if removal.Reason() != "" || !strings.Contains(removal.Events[0].Describe(), "no reason given") {
		t.Errorf("reason = %q, described %q; want a removal with no reason said to have none", removal.Reason(), removal.Events[0].Describe())
	}
	if removal.MergeStatus != "" || !strings.Contains(removal.MergeStatusError, "HTTP 502") {
		t.Errorf("merge status = %q, error = %q; want the unread state recorded beside the events", removal.MergeStatus, removal.MergeStatusError)
	}

	failed := &scriptedRunner{}
	failed.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	failed.fail("timelineItems", errors.New("connection reset"))
	if _, err := (GitHub{Runner: failed}).QueueRemoval(context.Background(), 713); err == nil || !strings.Contains(err.Error(), "merge queue events of pull request 713") {
		t.Errorf("QueueRemoval() error = %v, want the unread timeline named", err)
	}
}

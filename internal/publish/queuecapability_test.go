package publish

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// What GitHub's merge queue is read as establishing. Its queue is read from
// the forge, its timeout is the forge's own, and what GitHub cannot establish
// is reported as unmet whatever the repository configures.
func TestGitHubQueueCapabilitiesReadTheQueueAndItsOwnTimeout(t *testing.T) {
	t.Parallel()

	queue := func(configuration string) execution.ProcessResult {
		return execution.ProcessResult{Status: execution.ProcessSucceeded,
			Stdout: `{"data":{"repository":{"mergeQueue":{"id":"MQ_1","configuration":` + configuration + `}}}}`}
	}
	cases := []struct {
		name        string
		answer      execution.ProcessResult
		wantQueue   bool
		wantTimeout int
	}{
		{name: "a sixty-minute queue", answer: queue(`{"checkResponseTimeout":60}`), wantQueue: true, wantTimeout: 60},
		{name: "a fifteen-minute queue", answer: queue(`{"checkResponseTimeout":15}`), wantQueue: true, wantTimeout: 15},
		{name: "a queue that does not say its timeout", answer: queue(`{"checkResponseTimeout":null}`), wantQueue: true},
		{name: "a queue with no configuration", answer: queue(`null`), wantQueue: true},
		{name: "no queue", answer: execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{"mergeQueue":null}}}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runner := protectedRunner()
			runner.reply("mergeQueue(branch", tc.answer)
			got, err := (GitHub{Runner: runner}).QueueCapabilities(context.Background(), "main")
			if err != nil {
				t.Fatalf("QueueCapabilities() error = %v", err)
			}
			if got.Forge != "github" || got.TargetBranch != "main" || got.ObservedAt.IsZero() || !got.Protected || got.ProtectedBy != "ruleset" {
				t.Fatalf("QueueCapabilities() = %#v, want the observation of main and its protection", got)
			}
			if got.QueueAvailable != tc.wantQueue || got.QueueRequired != tc.wantQueue || got.TimeoutMinutes != tc.wantTimeout {
				t.Fatalf("QueueCapabilities() = %#v, want queue %t with a %d-minute timeout", got, tc.wantQueue, tc.wantTimeout)
			}
			asked := runner.matching("mergeQueue(branch")
			if len(asked) != 1 || !contains(asked[0], "branch=main") {
				t.Fatalf("merge queue query = %v, want main named", asked)
			}
			if !tc.wantQueue {
				if len(got.Requirements) != 0 {
					t.Fatalf("Requirements = %#v for a branch with no queue", got.Requirements)
				}
				return
			}
			byRequirement := map[QueueRequirement]QueueRequirementEvidence{}
			for _, evidence := range got.Requirements {
				byRequirement[evidence.Requirement] = evidence
			}
			if len(byRequirement) != len(QueueRequirements()) {
				t.Fatalf("Requirements = %#v, want every requirement answered once", got.Requirements)
			}
			met := func(requirement QueueRequirement) bool {
				evidence := byRequirement[requirement]
				return evidence.Established && evidence.Enforced
			}
			// GitHub names entries, bases, heads in order, and merge commits.
			for _, requirement := range []QueueRequirement{QueueTargetBase, QueueContributingHeads, QueueLandingIdentity} {
				if !met(requirement) {
					t.Errorf("%s = %#v, want it established and enforced", requirement, byRequirement[requirement])
				}
			}
			// It does not name groups or their combined commit, bind its checks to
			// the project's, or approve the combined commit.
			for _, requirement := range []QueueRequirement{QueueEntryIdentity, QueueCandidateCommit, QueueRequiredChecks, QueueIndependentApproval} {
				if met(requirement) || byRequirement[requirement].Missing == "" {
					t.Errorf("%s = %#v, want it unmet with the reason", requirement, byRequirement[requirement])
				}
			}
			if byRequirement[QueueIndependentApproval].BoundTo != BoundToPullRequestHead {
				t.Errorf("approval = %#v, want it bound to the pull request's head", byRequirement[QueueIndependentApproval])
			}
			if timeout := byRequirement[QueueTimeoutPolicy]; met(QueueTimeoutPolicy) != (tc.wantTimeout > 0) ||
				(tc.wantTimeout == 0 && !strings.Contains(timeout.Missing, "did not say how long")) {
				t.Errorf("timeout = %#v, want it met only where the forge reported one", timeout)
			}
		})
	}
}

// A question the forge did not answer is an error, never "no queue" or "not
// protected".
func TestGitHubQueueCapabilitiesRefuseAnUnreadableAnswer(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*scriptedRunner){
		"protection unanswered": func(r *scriptedRunner) {
			r.reply("{repo}/branches/main", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "gh: Server Error (HTTP 502)\n"})
		},
		"queue unanswered": func(r *scriptedRunner) {
			r.reply("mergeQueue(branch", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "gh: Server Error (HTTP 502)\n"})
		},
		"queue answer garbled": func(r *scriptedRunner) {
			r.reply("mergeQueue(branch", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":`})
		},
		"queue answer names no repository": func(r *scriptedRunner) {
			r.reply("mergeQueue(branch", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":null}}`})
		},
	}
	for name, script := range cases {
		runner := protectedRunner()
		script(runner)
		if got, err := (GitHub{Runner: runner}).QueueCapabilities(context.Background(), "main"); err == nil {
			t.Errorf("%s: QueueCapabilities() = %#v, want an error", name, got)
		}
	}
	if _, err := (GitHub{Runner: protectedRunner()}).QueueCapabilities(context.Background(), "--main"); err == nil {
		t.Error("QueueCapabilities() accepted a branch that reads as an option")
	}
}

// protectedRunner answers for main protected by a ruleset; each test adds
// the queue's answer.
func protectedRunner() *scriptedRunner {
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("{repo}/branches/main", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"name":"main","protected":false}`})
	runner.reply("rules/branches/main", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `[{"type":"merge_queue"},{"type":"pull_request"}]`})
	return runner
}

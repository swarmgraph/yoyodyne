package queuemode

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ghAnswers stands in for the gh command line: each invocation is answered by
// the first fragment of its arguments it contains, so what is fed to the
// selector is the GitHub adapter's own reading of those answers rather than a
// capability value written by hand.
type ghAnswers map[string]string

func (answers ghAnswers) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	joined := strings.Join(command.Args, " ")
	for match, stdout := range answers {
		if strings.Contains(joined, match) {
			return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: stdout}, nil
		}
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

// What GitHub says about main, before and after its merge-queue rule is
// removed: protected by branch protection either way, with a merge queue only
// while the rule stands.
func githubMain(withQueue bool) ghAnswers {
	queue := `{"data":{"repository":{"mergeQueue":null}}}`
	if withQueue {
		queue = `{"data":{"repository":{"mergeQueue":{"id":"MQ_1","configuration":{"checkResponseTimeout":60}}}}}`
	}
	return ghAnswers{
		"remote get-url":       "https://example.invalid/acme/thing\n",
		"{repo}/branches/main": `{"name":"main","protected":true}`,
		"rules/branches/main":  `[{"type":"merge_queue"},{"type":"pull_request"}]`,
		"mergeQueue(branch":    queue,
	}
}

// The GitHub adapter's own answer for a branch whose rules require GitHub's
// merge queue, fed to the admission the pipeline makes with
// execution.merge_queue on: GitHub's queue is not chosen, the harness's queue
// is not chosen either, nothing is admitted, and the change is held naming the
// repository setting a person has to change.
func TestGitHubsAnswerForABranchRequiringItsQueueHoldsTheChangeOutOfBothQueues(t *testing.T) {
	t.Parallel()

	forge := publish.GitHub{Runner: githubMain(true)}
	observed, err := forge.QueueCapabilities(context.Background(), "main")
	if err != nil {
		t.Fatalf("QueueCapabilities() error = %v", err)
	}
	if !observed.QueueAvailable || !observed.QueueRequired {
		t.Fatalf("QueueCapabilities() = %#v, want GitHub's queue read as required on main", observed)
	}

	store := newStore(t, t.TempDir())
	admitted, err := Admitter{Queue: store, Forge: forge, Harness: harness}.Admit(context.Background(), admission(t))
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if admitted.Hold == nil || admitted.Entry.RunID != "" || admitted.Entry.Mode != "" {
		t.Fatalf("Admit() = %#v, want the change held and admitted to neither queue", admitted)
	}
	hold := admitted.Hold
	if hold.PersonOnly == nil || hold.PersonOnly.Reason != ownership.PersonRepositorySetting || hold.Mover != ownership.MoverOperator {
		t.Fatalf("hold = %#v, want a repository setting only a person can change", hold)
	}
	for _, want := range []string{"only through the forge's merge queue", "merge queue requirement"} {
		if !strings.Contains(hold.Requirement+" "+hold.Step, want) {
			t.Fatalf("hold %#v does not say %q", hold, want)
		}
	}
	if entries, err := store.Entries(mainQueue); err != nil || len(entries) != 0 {
		t.Fatalf("Entries() = %#v, %v; want nothing admitted", entries, err)
	}

	// Once the rule is removed, GitHub's answer for the same branch admits the
	// change to the harness's own queue, never to GitHub's.
	admitted, err = Admitter{Queue: store, Forge: publish.GitHub{Runner: githubMain(false)}, Harness: harness}.Admit(context.Background(), admission(t))
	if err != nil || admitted.Hold != nil || admitted.Entry.Mode != runstate.MergeQueueHarness || admitted.Entry.ModeEvidence.Forge != "github" {
		t.Fatalf("Admit() once the rule is removed = %#v, %v; want the harness's queue", admitted, err)
	}
}

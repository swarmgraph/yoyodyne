package publish

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Ensure has to be idempotent, because it runs after every developer attempt.
// The first attempt opens the pull request; each repair attempt finds the same
// one rather than opening a second for the same branch.
func TestGitHubEnsureOpensOncePerBranch(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}
	request := Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "task Do the thing", Body: "opened by the harness"}

	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/pull/7\n"})

	opened, err := forge.Ensure(context.Background(), request)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if opened.Number != 7 || opened.URL != "https://example.invalid/pull/7" || opened.Merged {
		t.Fatalf("Ensure() = %#v", opened)
	}
	created := runner.matching("pr create")
	if len(created) != 1 {
		t.Fatalf("pull request creations = %d, want exactly one", len(created))
	}
	for _, expected := range []string{"--base", "main", "--head", "yoyodyne/task/abcd1234"} {
		if !contains(created[0], expected) {
			t.Errorf("pr create args = %v, missing %q", created[0], expected)
		}
	}

	again, err := forge.Ensure(context.Background(), request)
	if err != nil {
		t.Fatalf("Ensure() repeated error = %v", err)
	}
	if again.Number != 7 {
		t.Fatalf("Ensure() repeated = %#v", again)
	}
	if creations := runner.matching("pr create"); len(creations) != 1 {
		t.Fatalf("pull request creations = %d after a second Ensure, want exactly one", len(creations))
	}
}

// A branch whose pull request is already closed or merged cannot receive more
// of this run's work. Opening a second one would publish the branch twice and
// leave two answers about what is under review.
func TestGitHubEnsureRefusesAPullRequestThatIsNoLongerOpen(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":9,"url":"https://example.invalid/pull/9","state":"MERGED","mergedAt":"2026-08-16T00:00:00Z"}]`,
	})
	forge := GitHub{Runner: runner}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "task"}); err == nil || !strings.Contains(err.Error(), "cannot be republished into") {
		t.Fatalf("Ensure() merged error = %v", err)
	}
	if creations := runner.matching("pr create"); len(creations) != 0 {
		t.Fatalf("a merged pull request was republished into: %v", creations)
	}
}

// Merging is what publishes a promotion now, so the request has to name the
// method deliberately and pin the commit that may merge: a head that moved
// since the harness published it must be refused by the forge rather than
// merged in place of what the run integrated. The method reaches the CLI as the
// flag for it, because that is what decides whether the base ends up with the
// reviewed commit or a rewritten copy of it. The merge is asked for as of when
// the requirements are met rather than as of now, which is what a protected
// branch with required checks is asking for.
func TestGitHubMergeNamesTheMethodAndPinsTheHeadCommit(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{Status: execution.ProcessSucceeded})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if !result.Queued {
		t.Errorf("Merge() = %#v, want the merge queued for when the requirements are met", result)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 1 {
		t.Fatalf("merges = %d, want exactly one", len(merges))
	}
	for _, expected := range []string{"7", "--merge", "--auto", "--match-head-commit", head, "--repo", "https://example.invalid/acme/thing"} {
		if !contains(merges[0], expected) {
			t.Errorf("pr merge args = %v, missing %q", merges[0], expected)
		}
	}
	// Merging with administrator privileges would bypass the checks the branch
	// protection expresses, which removes the gate rather than satisfying it.
	if contains(merges[0], "--admin") {
		t.Errorf("pr merge args = %v, want no administrator override", merges[0])
	}
	for method, flag := range map[MergeMethod]string{MergeRebase: "--rebase", MergeSquash: "--squash"} {
		if _, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: method}); err != nil {
			t.Fatalf("Merge(%s) error = %v", method, err)
		}
		asked := runner.matching(flag)
		if len(asked) != 1 {
			t.Errorf("merges asking for %s = %d, want exactly one", flag, len(asked))
		}
	}
	// A method the forge does not offer, a missing one, and a head that is not a
	// commit are all refused before anything runs: the merge method is explicit
	// or there is no merge.
	for _, request := range []MergeRequest{
		{Number: 7, HeadCommit: head},
		{Number: 7, HeadCommit: head, Method: MergeMethod("fast-forward")},
		{Number: 7, HeadCommit: "--repo=elsewhere", Method: MergeCommit},
		{Number: 0, HeadCommit: head, Method: MergeCommit},
	} {
		if _, err := forge.Merge(context.Background(), request); err == nil {
			t.Errorf("Merge(%#v) error = nil", request)
		}
	}
	if merges := runner.matching("pr merge"); len(merges) != 3 {
		t.Fatalf("refused requests still asked for a merge: %v", merges)
	}
}

// A repository whose settings forbid queued merges cannot be published to when
// something is holding the request back, and the operator has to be told which
// setting to change rather than left reading a refusal that names no remedy.
func TestGitHubMergeReportsARepositoryThatCannotQueueAMerge(t *testing.T) {
	t.Parallel()

	for name, reason := range map[string]string{
		"the forge's own wording": "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
		"hyphenated":              "failed to enable auto-merge: auto-merge is not enabled for this repository",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runner := &scriptedRunner{}
			runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
			runner.reply("pr merge", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: reason})
			// The base branch is holding the request back, which is the half that
			// makes a repository without queued merges unpublishable.
			runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})
			forge := GitHub{Runner: runner, Dir: t.TempDir()}

			_, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
			var unavailable AutoMergeUnavailable
			if !errors.As(err, &unavailable) {
				t.Fatalf("Merge() error = %v, want an AutoMergeUnavailable", err)
			}
			if unavailable.Number != 7 || unavailable.Status != "BLOCKED" {
				t.Errorf("unavailable = %#v, want the pull request and the state that held it back named", unavailable)
			}
			for _, want := range []string{"Allow auto-merge", "protection rules", "BLOCKED", reason} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err.Error(), want)
				}
			}
			// The request was never merged past the requirement, and above all not
			// with an override: that would satisfy nothing the repository asked for.
			calls := runner.matching("pr merge")
			if len(calls) != 1 {
				t.Fatalf("pr merge calls = %v, want the queued request and nothing after it", calls)
			}
			if contains(calls[0], "--admin") {
				t.Errorf("pr merge args = %v, want no administrator override", calls[0])
			}
		})
	}
}

// "Allow auto-merge" is off by default on GitHub, so a repository that forbids
// queued merges is the ordinary case rather than a broken one. When nothing is
// holding the request back there is nothing for a queue to wait for, and the
// merge is simply made — reporting the setting instead would fail the
// publication of every project without branch protection, which is the manual
// step queuing exists to remove.
func TestGitHubMergeMergesWhenQueuingIsForbiddenAndNothingIsWaiting(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
	})
	runner.replyAfter("pr merge", 1, execution.ProcessResult{Status: execution.ProcessSucceeded})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"CLEAN"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
	if err != nil {
		t.Fatalf("Merge() error = %v, want the request merged rather than reported as unpublishable", err)
	}
	if result.Queued {
		t.Errorf("Merge() = %#v, want a merge the forge performed rather than queued", result)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 2 {
		t.Fatalf("pr merge calls = %v, want the queued request and then the merge itself", merges)
	}
	if contains(merges[1], "--auto") || contains(merges[1], "--admin") {
		t.Errorf("second pr merge args = %v, want an ordinary merge with no override", merges[1])
	}
	for _, expected := range []string{"--merge", "--match-head-commit", head} {
		if !contains(merges[1], expected) {
			t.Errorf("second pr merge args = %v, missing %q", merges[1], expected)
		}
	}
}

// A repository with no required checks has nothing for a queued merge to wait
// for, and the forge says so by refusing to queue one. Merging then is what the
// queued request asked for rather than a way around it — every requirement is
// already met — and treating the refusal as final would leave the unprotected
// case unable to publish at all.
func TestGitHubMergeMergesWhenTheForgeHasNothingLeftToWaitFor(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: Pull request is in clean status (enablePullRequestAutoMerge)",
	})
	runner.replyAfter("pr merge", 1, execution.ProcessResult{Status: execution.ProcessSucceeded})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if result.Queued {
		t.Errorf("Merge() = %#v, want a merge the forge performed rather than queued", result)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 2 {
		t.Fatalf("merges = %d, want the queued request and then the merge itself", len(merges))
	}
	if !contains(merges[0], "--auto") {
		t.Errorf("first pr merge args = %v, want the queued request", merges[0])
	}
	if contains(merges[1], "--auto") || contains(merges[1], "--admin") {
		t.Errorf("second pr merge args = %v, want an ordinary merge with no override", merges[1])
	}
	for _, expected := range []string{"--merge", "--match-head-commit", head} {
		if !contains(merges[1], expected) {
			t.Errorf("second pr merge args = %v, missing %q", merges[1], expected)
		}
	}
}

// The words a forge refuses a queued merge in can be reworded, and a repository
// with no required checks would then stop publishing entirely — it is refused on
// every run. So the same question is asked of the merge state the forge reports,
// which is its own vocabulary: a clean request has nothing for a queue to wait
// for, whatever the message says.
func TestGitHubMergeMergesACleanRequestItDoesNotRecognizeTheRefusalOf(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: some wording nobody has seen before (enablePullRequestAutoMerge)",
	})
	runner.replyAfter("pr merge", 1, execution.ProcessResult{Status: execution.ProcessSucceeded})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"CLEAN"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if result.Queued {
		t.Errorf("Merge() = %#v, want a merge the forge performed rather than queued", result)
	}
	merges := runner.matching("pr merge")
	if len(merges) != 2 || contains(merges[1], "--auto") || contains(merges[1], "--admin") {
		t.Fatalf("pr merge calls = %v, want an ordinary merge after the queue was refused", merges)
	}

	// A request that is not clean is still a refusal, and it names the state it
	// was refused in even when nothing recognized the message.
	blocked := &scriptedRunner{}
	blocked.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	blocked.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: some wording nobody has seen before (enablePullRequestAutoMerge)",
	})
	blocked.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"DIRTY"}`})
	_, err = (GitHub{Runner: blocked, Dir: t.TempDir()}).Merge(context.Background(),
		MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	var refused MergeRefused
	if !errors.As(err, &refused) || refused.Status != "DIRTY" {
		t.Fatalf("Merge() error = %v, want a refusal naming the state it was refused in", err)
	}
	if calls := blocked.matching("pr merge"); len(calls) != 1 {
		t.Errorf("pr merge calls = %v, want the refused request not to be merged anyway", calls)
	}
}

// A protected branch declining the merge is the repository's rules being
// applied, not the harness failing, so the refusal has to name the requirement
// that was unmet rather than read as a generic error.
func TestGitHubMergeReportsARefusalAsTheUnmetRequirement(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: Pull Request is not mergeable: the merge commit cannot be cleanly created. (enablePullRequestAutoMerge)",
	})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	_, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	var refused MergeRefused
	if !errors.As(err, &refused) {
		t.Fatalf("Merge() error = %v, want a MergeRefused", err)
	}
	if refused.Number != 7 || refused.Method != MergeCommit || refused.Status != "BLOCKED" {
		t.Fatalf("refusal = %#v", refused)
	}
	for _, want := range []string{"protection rules", "BLOCKED", "the merge commit cannot be cleanly created"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err.Error(), want)
		}
	}

	// A forge that cannot say why still reports what it printed. Losing the
	// refusal to a failed follow-up query would be the worst of both.
	quiet := &scriptedRunner{}
	quiet.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	quiet.reply("pr merge", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Pull request is not mergeable"})
	quiet.reply("pr view", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1})
	_, err = (GitHub{Runner: quiet, Dir: t.TempDir()}).Merge(context.Background(),
		MergeRequest{Number: 9, HeadCommit: "0123456789abcdef0123456789abcdef01234567", Method: MergeCommit})
	if err == nil || !strings.Contains(err.Error(), "Pull request is not mergeable") {
		t.Fatalf("Merge() unexplained refusal error = %v", err)
	}
}

// State is how the harness confirms that the merge it asked the forge for
// actually happened, rather than assuming it did.
func TestGitHubStateReportsMergeAndAbsence(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"MERGED","mergedAt":"2026-08-16T12:00:00Z","autoMergeRequest":null,"mergeCommit":{"oid":"9f1c2ab7e05c4d3b9a1b6e8f0d2a4c71deadbeef"}}]`,
	})
	forge := GitHub{Runner: runner}
	merged, err := forge.State(context.Background(), "yoyodyne/task/abcd1234")
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if !merged.Merged || merged.Number != 7 {
		t.Fatalf("State() = %#v", merged)
	}
	// The forge's own record of which commit merged the request is what confirms
	// a merge other merges have since landed on top of, so it is carried through.
	if merged.MergeCommit != "9f1c2ab7e05c4d3b9a1b6e8f0d2a4c71deadbeef" {
		t.Fatalf("State() merge commit = %q, want the forge's recorded merge commit", merged.MergeCommit)
	}

	absent := &scriptedRunner{}
	absent.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	absent.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]"})
	if _, err := (GitHub{Runner: absent}).State(context.Background(), "yoyodyne/task/abcd1234"); err == nil || !strings.Contains(err.Error(), "no pull request exists") {
		t.Fatalf("State() absent error = %v", err)
	}
}

// An open, unmerged request means two different things depending on whether the
// forge is still holding a merge for it: one is a queued merge that has not
// landed yet, the other is a queued merge the forge dropped, which needs a
// person. The state has to tell them apart.
func TestGitHubStateReportsAQueuedMergeSeparatelyFromADroppedOne(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		reported string
		// queue is the forge's answer about its merge queue, asked only of an open
		// request holding no auto-merge.
		queue string
		want  bool
	}{
		"still queued": {reported: `{"authorEmail":"harness@example.invalid","mergeMethod":"MERGE"}`, want: true},
		"dropped":      {reported: "null", queue: `{"data":{"repository":{"pullRequest":{"isInMergeQueue":false}}}}`, want: false},
		// The merge queue consumes the auto-merge as it takes the request, so the
		// listing reads exactly as a drop does. Pull requests 832 and 834 were
		// handed to a person that way on 2026-09-27 while the queue landed them.
		"in the merge queue": {reported: "null", queue: `{"data":{"repository":{"pullRequest":{"isInMergeQueue":true}}}}`, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runner := &scriptedRunner{}
			runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
			runner.reply("pr list", execution.ProcessResult{
				Status: execution.ProcessSucceeded,
				Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"OPEN","mergedAt":"","autoMergeRequest":` + test.reported + `}]`,
			})
			runner.reply("api graphql", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: test.queue})
			observed, err := (GitHub{Runner: runner}).State(context.Background(), "yoyodyne/task/abcd1234")
			if err != nil {
				t.Fatalf("State() error = %v", err)
			}
			asked := runner.matching("api graphql")
			if test.queue == "" && len(asked) != 0 {
				t.Errorf("the merge queue was asked about a request holding an auto-merge: %v", asked)
			}
			if test.queue != "" && (len(asked) != 1 || !contains(asked[0], "number=7") || apiRepositoryScope(runner) == "") {
				t.Errorf("merge queue query = %v (scope %q), want request 7 asked about in the configured repository", asked, apiRepositoryScope(runner))
			}
			if observed.Merged {
				t.Fatalf("State() = %#v, want an open request", observed)
			}
			if observed.AutoMerge != test.want {
				t.Errorf("State() auto-merge = %t, want %t", observed.AutoMerge, test.want)
			}
			// An unmerged request has no merge commit, and the forge says so as null
			// rather than as an empty object.
			if observed.MergeCommit != "" {
				t.Errorf("State() merge commit = %q, want none on an unmerged request", observed.MergeCommit)
			}
		})
	}

	// A forge that does not say whether the request is in its merge queue has not
	// said the merge was dropped, so the reading fails rather than reporting one.
	unanswered := &scriptedRunner{}
	unanswered.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	unanswered.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":7,"url":"https://example.invalid/pull/7","state":"OPEN","mergedAt":"","autoMergeRequest":null}]`,
	})
	unanswered.reply("api graphql", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502"})
	if observed, err := (GitHub{Runner: unanswered}).State(context.Background(), "yoyodyne/task/abcd1234"); err == nil || !strings.Contains(err.Error(), "merge queue") {
		t.Fatalf("State() with the merge queue unanswered = %#v, %v; want an error naming the merge queue", observed, err)
	}

	// The queued merge, the head the request carries, and the commit that merged
	// it are only knowable if they were asked for, so the query has to ask for
	// them.
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]"})
	_, _ = (GitHub{Runner: runner}).State(context.Background(), "yoyodyne/task/abcd1234")
	listed := runner.matching("pr list")
	if len(listed) != 1 || !contains(listed[0], "number,url,state,mergedAt,autoMergeRequest,headRefOid,mergeCommit") {
		t.Errorf("pr list args = %v, want the queued merge, the head, and the merge commit among the requested fields", listed)
	}
}

func TestGitHubAvailabilityReportsMissingAndUnauthenticatedCLIs(t *testing.T) {
	t.Parallel()

	missing := &scriptedRunner{}
	missing.fail("--version", exec.ErrNotFound)
	availability, err := (GitHub{Runner: missing}).Availability(context.Background())
	if err != nil {
		t.Fatalf("Availability() missing error = %v", err)
	}
	if availability.Installed || availability.Authenticated {
		t.Fatalf("Availability() missing = %#v", availability)
	}

	unauthenticated := &scriptedRunner{}
	unauthenticated.reply("--version", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "gh version 2.0.0\nhttps://example.invalid\n"})
	unauthenticated.reply("auth", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1})
	availability, err = (GitHub{Runner: unauthenticated}).Availability(context.Background())
	if err != nil {
		t.Fatalf("Availability() unauthenticated error = %v", err)
	}
	if !availability.Installed || availability.Authenticated {
		t.Fatalf("Availability() unauthenticated = %#v", availability)
	}
	if availability.Version != "gh version 2.0.0" {
		t.Errorf("Availability() version = %q", availability.Version)
	}
}

// Branch names reach a command line, so anything that could read as an option
// is refused before it gets there.
func TestGitHubRefusesArgumentsThatCouldReadAsOptions(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	forge := GitHub{Runner: runner}
	for _, request := range []Request{
		{Head: "--repo=elsewhere", Base: "main", Title: "task"},
		{Head: "yoyodyne/task/abcd1234", Base: "-f", Title: "task"},
		{Head: "yoyodyne/task/abcd1234", Base: "main"},
		{Head: "with space", Base: "main", Title: "task"},
	} {
		if _, err := forge.Ensure(context.Background(), request); err == nil {
			t.Errorf("Ensure(%#v) error = nil", request)
		}
	}
	if len(runner.commands) != 0 {
		t.Fatalf("refused requests still ran commands: %v", runner.commands)
	}
}

// scriptedRunner answers gh invocations by the first argument fragment they
// contain, so a test states what the CLI reports rather than how it is invoked.
type scriptedRunner struct {
	commands [][]string
	// environments is what each command was given beside its arguments, for the
	// one verb whose repository is named there rather than on the command line.
	environments [][]string
	replies      map[string]execution.ProcessResult
	later        map[string]execution.ProcessResult
	after        map[string]int
	failures     map[string]error
	seen         map[string]int
}

func (r *scriptedRunner) reply(match string, result execution.ProcessResult) {
	if r.replies == nil {
		r.replies = map[string]execution.ProcessResult{}
	}
	r.replies[match] = result
}

// replyAfter switches a match to a different answer once it has been called a
// given number of times, which is how "the pull request did not exist and then
// it did" is expressed.
func (r *scriptedRunner) replyAfter(match string, calls int, result execution.ProcessResult) {
	if r.later == nil {
		r.later = map[string]execution.ProcessResult{}
		r.after = map[string]int{}
	}
	r.later[match] = result
	r.after[match] = calls
}

func (r *scriptedRunner) fail(match string, err error) {
	if r.failures == nil {
		r.failures = map[string]error{}
	}
	r.failures[match] = err
}

func (r *scriptedRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, append([]string(nil), command.Args...))
	r.environments = append(r.environments, append([]string(nil), command.Env...))
	joined := strings.Join(command.Args, " ")
	for match, err := range r.failures {
		if strings.Contains(joined, match) {
			return execution.ProcessResult{}, err
		}
	}
	if r.seen == nil {
		r.seen = map[string]int{}
	}
	for match, result := range r.replies {
		if !strings.Contains(joined, match) {
			continue
		}
		r.seen[match]++
		if later, ok := r.later[match]; ok && r.seen[match] > r.after[match] {
			return later, nil
		}
		return result, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func (r *scriptedRunner) matching(match string) [][]string {
	var found [][]string
	for _, command := range r.commands {
		if strings.Contains(strings.Join(command, " "), match) {
			found = append(found, command)
		}
	}
	return found
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// apiRepositoryScope reads the GH_REPO the API verb was given, or "" when no
// API verb ran or none carried one.
func apiRepositoryScope(runner *scriptedRunner) string {
	for index, command := range runner.commands {
		if !contains(command, "api") {
			continue
		}
		for _, entry := range runner.environments[index] {
			if value, found := strings.CutPrefix(entry, "GH_REPO="); found {
				return value
			}
		}
	}
	return ""
}

// TestGitHubScopesCommandsToTheConfiguredRemote pins the forge to the remote the
// project named. Without it the CLI infers a repository from the working
// directory, which is wrong rather than merely redundant in a checkout with
// more than one remote.
func TestGitHubScopesCommandsToTheConfiguredRemote(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":3,"url":"https://example.invalid/pull/3","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded})

	forge := GitHub{Runner: runner, Dir: t.TempDir(), Remote: "upstream"}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	resolved := runner.matching("remote get-url")
	if len(resolved) == 0 {
		t.Fatal("the remote was never resolved")
	}
	if !contains(resolved[0], "upstream") {
		t.Errorf("remote resolution args = %v, want the configured remote", resolved[0])
	}
	for _, kind := range []string{"pr list", "pr create"} {
		calls := runner.matching(kind)
		if len(calls) == 0 {
			t.Fatalf("%s was never called", kind)
		}
		for _, call := range calls {
			if !contains(call, "--repo") || !contains(call, "https://example.invalid/acme/thing") {
				t.Errorf("%s args = %v, want scoping to the resolved repository", kind, call)
			}
		}
	}
}

// A contributor's run branch lives in their fork, so the request has to be
// opened across two repositories: against the one the work is going into, from a
// head that repository has never heard of. What qualifies the head is the fork's
// owner, read out of the fork remote's own URL.
func TestGitHubOpensACrossRepositoryRequestFromTheFork(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url upstream", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("remote get-url fork", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "git@example.invalid:contributor/thing.git\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":9,"url":"https://example.invalid/pull/9","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded})

	forge := GitHub{Runner: runner, Dir: t.TempDir(), Remote: "upstream", PushRemote: "fork"}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	created := runner.matching("pr create")
	if len(created) != 1 {
		t.Fatalf("pr create calls = %d, want exactly one", len(created))
	}
	if !contains(created[0], "contributor:yoyodyne/task/abcd1234") {
		t.Errorf("pr create args = %v, want the head qualified with the fork's owner", created[0])
	}
	// The request is still opened against the repository the work is going into,
	// which is the half of a cross-repository request that must not move.
	if !contains(created[0], "https://example.invalid/acme/thing") {
		t.Errorf("pr create args = %v, want the request opened against the publishing remote", created[0])
	}
	// The listing is by head reference name, which is what a request from a fork
	// carries on the base repository. Qualifying it would name a reference that
	// repository does not have.
	for _, call := range runner.matching("pr list") {
		if !contains(call, "yoyodyne/task/abcd1234") {
			t.Errorf("pr list args = %v, want the run branch's head reference", call)
		}
	}
}

// A project that pushes run branches to the repository it publishes into names
// the branch and nothing else, and resolves one remote to do it.
func TestGitHubLeavesTheHeadUnqualifiedWithoutAFork(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":4,"url":"https://example.invalid/pull/4","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded})

	forge := GitHub{Runner: runner, Dir: t.TempDir(), Remote: "origin"}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	created := runner.matching("pr create")
	if len(created) != 1 {
		t.Fatalf("pr create calls = %d, want exactly one", len(created))
	}
	if !contains(created[0], "yoyodyne/task/abcd1234") {
		t.Errorf("pr create args = %v, want the plain head reference", created[0])
	}
}

// A fork remote can be written any way Git accepts one, and the account is the
// same in all of them. A URL that names no repository is refused rather than
// guessed at: a request opened from an unidentifiable head is worse than one
// that was not opened.
func TestRepositoryOwnerReadsEveryRemoteURLForm(t *testing.T) {
	t.Parallel()

	for _, url := range []string{
		"git@github.com:contributor/thing.git",
		"ssh://git@github.com/contributor/thing.git",
		"https://github.com/contributor/thing.git",
		"https://github.com/contributor/thing",
		"https://github.com/contributor/thing/",
	} {
		owner, err := repositoryOwner(url)
		if err != nil || owner != "contributor" {
			t.Errorf("repositoryOwner(%q) = %q, %v, want contributor", url, owner, err)
		}
	}
	for _, url := range []string{"", "   ", "https://github.com", "github.com:thing.git"} {
		if owner, err := repositoryOwner(url); err == nil {
			t.Errorf("repositoryOwner(%q) = %q, want a refusal", url, owner)
		}
	}
}

// The repository GH_REPO is given is OWNER/REPO whichever way the remote was
// written — the https and ssh forms git reports, with or without the .git
// suffix — because that is the form gh documents for the variable. A remote on
// any host but github.com keeps its host in front, so an enterprise remote is
// not resolved against the public forge; a URL that names no repository is
// refused rather than sent to gh to fail in the placeholder step.
func TestRemoteRepositoryIsOwnerAndNameInTheFormGHRepoTakes(t *testing.T) {
	t.Parallel()

	for url, want := range map[string]string{
		"git@github.com:mason-bryant/yoyodyne.git":         "mason-bryant/yoyodyne",
		"git@github.com:mason-bryant/yoyodyne":             "mason-bryant/yoyodyne",
		"ssh://git@github.com/mason-bryant/yoyodyne.git":   "mason-bryant/yoyodyne",
		"ssh://git@github.com:22/mason-bryant/yoyodyne":    "mason-bryant/yoyodyne",
		"https://github.com/mason-bryant/yoyodyne.git":     "mason-bryant/yoyodyne",
		"https://github.com/mason-bryant/yoyodyne":         "mason-bryant/yoyodyne",
		"https://github.com/mason-bryant/yoyodyne/":        "mason-bryant/yoyodyne",
		"https://GitHub.com/mason-bryant/yoyodyne":         "mason-bryant/yoyodyne",
		"https://token@github.com/mason-bryant/yoyodyne":   "mason-bryant/yoyodyne",
		"git@ghe.example.com:acme/thing.git":               "ghe.example.com/acme/thing",
		"https://ghe.example.com/acme/thing.git":           "ghe.example.com/acme/thing",
		"https://example.invalid/acme/thing":               "example.invalid/acme/thing",
		"https://ghe.example.com:8443/acme/thing":          "ghe.example.com/acme/thing",
		"https://ghe.example.com/team/subgroup/acme/thing": "ghe.example.com/acme/thing",
	} {
		got, err := remoteRepository(url)
		if err != nil || got != want {
			t.Errorf("remoteRepository(%q) = %q, %v, want %q", url, got, err, want)
		}
	}
	for _, url := range []string{"", "   ", "https://github.com", "github.com:thing.git", "/srv/git/thing.git", "https://github.com/-owner/thing", "https://github.com/owner/na me"} {
		if got, err := remoteRepository(url); err == nil {
			t.Errorf("remoteRepository(%q) = %q, want a refusal", url, got)
		}
	}
}

// TestGitHubRefusesWhenTheRemoteCannotBeResolved keeps the failure closed: a
// forge command that cannot tell which repository it is acting on must not fall
// back to inferring one.
func TestGitHubRefusesWhenTheRemoteCannotBeResolved(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2, Stderr: "no such remote"})
	forge := GitHub{Runner: runner, Dir: t.TempDir(), Remote: "missing"}

	if _, err := forge.State(context.Background(), "yoyodyne/task/abcd1234"); err == nil {
		t.Fatal("State() error = nil, want a refusal when the remote cannot be resolved")
	}
	if calls := runner.matching("pr "); len(calls) != 0 {
		t.Errorf("forge commands ran despite an unresolved remote: %v", calls)
	}
}

// TestGitHubMergeMergesWhenChecksAreNotRequired covers the ordinary repository
// that has CI and no branch protection. Its checks are not required by anything,
// so the forge reports UNSTABLE rather than CLEAN, and nothing is holding the
// request back. Treating that as unpublishable would misreport the commonest
// configuration there is as needing a setting changed.
func TestGitHubMergeMergesWhenChecksAreNotRequired(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"UNSTABLE", "HAS_HOOKS", "clean"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			runner := &scriptedRunner{}
			runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
			runner.reply("pr merge", execution.ProcessResult{
				Status:   execution.ProcessFailed,
				ExitCode: 1,
				Stderr:   "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
			})
			runner.replyAfter("pr merge", 1, execution.ProcessResult{Status: execution.ProcessSucceeded})
			runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"` + status + `"}`})
			forge := GitHub{Runner: runner, Dir: t.TempDir()}

			head := "0123456789abcdef0123456789abcdef01234567"
			result, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
			if err != nil {
				t.Fatalf("Merge() error = %v, want the request merged rather than reported as unpublishable", err)
			}
			if result.Queued {
				t.Errorf("Merge() = %#v, want a merge rather than a queued request", result)
			}
		})
	}
}

// TestGitHubMergeStillReportsAnUnavailableSettingWhenSomethingIsWaiting keeps
// the fallback narrow: a state with something genuinely outstanding must not be
// merged past just because queuing was refused.
func TestGitHubMergeStillReportsAnUnavailableSettingWhenSomethingIsWaiting(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge", execution.ProcessResult{
		Status:   execution.ProcessFailed,
		ExitCode: 1,
		Stderr:   "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
	})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	head := "0123456789abcdef0123456789abcdef01234567"
	_, err := forge.Merge(context.Background(), MergeRequest{Number: 7, HeadCommit: head, Method: MergeCommit})
	var unavailable AutoMergeUnavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("Merge() error = %v, want the unavailable setting reported when something is genuinely waiting", err)
	}
	if merges := runner.matching("pr merge"); len(merges) != 1 {
		t.Errorf("pr merge calls = %v, want no second merge attempt when something is waiting", merges)
	}
}

// What a caller about to repeat a dropped merge has to be able to ask: whether
// what the forge is holding the request on is something a person supplies. The
// harness never merges past one, so the line decides whether the request may be
// made again at all.
func TestAwaitsOnlyAPersonSeparatesWhatAPersonSuppliesFromWhatHasPassed(t *testing.T) {
	t.Parallel()

	// Nothing outstanding a person has to supply: the forge has no requirement
	// left, or has not finished deciding, so a merge that was dropped was dropped
	// for a cause that has passed.
	for _, status := range []string{"CLEAN", "unstable", "HAS_HOOKS", "UNKNOWN"} {
		if AwaitsOnlyAPerson(status) {
			t.Errorf("AwaitsOnlyAPerson(%q) = true, want a request nothing is holding", status)
		}
	}
	// Each of these names somebody's work, and an unrecognized state is one too:
	// a forge vocabulary that grew a word since must not read as nothing
	// outstanding.
	for _, status := range []string{"BLOCKED", "BEHIND", "DIRTY", "DRAFT", "", "SOMETHING_NEW"} {
		if !AwaitsOnlyAPerson(status) {
			t.Errorf("AwaitsOnlyAPerson(%q) = false, want it left to a person", status)
		}
	}
}

// The merge state a gate reads reports its failures rather than swallowing them,
// unlike the reading that only explains a refusal that already happened: a state
// nothing could be read of must refuse rather than read as clean.
func TestMergeStateReportsWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr view", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "not found"})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	if _, err := forge.MergeState(context.Background(), 7); err == nil {
		t.Fatal("MergeState() over a forge that would not answer = nil error, want the failure reported")
	}
	answering := &scriptedRunner{}
	answering.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	answering.reply("pr view", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"mergeStateStatus":"BLOCKED"}`})
	status, err := (GitHub{Runner: answering, Dir: t.TempDir()}).MergeState(context.Background(), 7)
	if err != nil || status != "BLOCKED" {
		t.Fatalf("MergeState() = %q, %v, want the forge's own word for it", status, err)
	}
	if requirement := MergeRequirement(status); !strings.Contains(requirement, "protection rules") {
		t.Fatalf("MergeRequirement(%q) = %q, want the rule stated in words", status, requirement)
	}
}

// The listing of open requests is the forge-hygiene pass's whole view of the
// forge, so it has to be scoped to the configured repository, bounded above the
// forge's own default, and carry the branches and head each request is judged
// by.
func TestGitHubListOpenReadsEveryOpenRequestWithItsBranches(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":445,"url":"https://example.invalid/pull/445","headRefName":"yoyodyne/yoyodyne-ifd-283/aaaaaaaa","baseRefName":"main","headRefOid":"3333333333333333333333333333333333333333"},
		         {"number":470,"url":"https://example.invalid/pull/470","headRefName":"feature/by-hand","baseRefName":"main","headRefOid":"2222222222222222222222222222222222222222"}]`,
	})
	forge := GitHub{Runner: runner, Remote: "upstream"}
	open, err := forge.ListOpen(context.Background())
	if err != nil {
		t.Fatalf("ListOpen() error = %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("ListOpen() = %+v, want both open requests", open)
	}
	if open[0].Number != 445 || open[0].HeadBranch != "yoyodyne/yoyodyne-ifd-283/aaaaaaaa" || open[0].BaseBranch != "main" || open[0].HeadCommit != "3333333333333333333333333333333333333333" {
		t.Errorf("ListOpen()[0] = %+v, want the request's branches and head carried", open[0])
	}
	calls := runner.matching("pr list")
	if len(calls) != 1 {
		t.Fatalf("pr list was called %d times, want once", len(calls))
	}
	call := calls[0]
	if !contains(call, "--repo") || !contains(call, "https://example.invalid/acme/thing") {
		t.Errorf("pr list args = %v, want scoping to the configured repository", call)
	}
	if !contains(call, "--state") || !contains(call, "open") {
		t.Errorf("pr list args = %v, want only open requests", call)
	}
	if !contains(call, "--limit") || !contains(call, "200") {
		t.Errorf("pr list args = %v, want a bound above the forge's default of thirty", call)
	}
}

// Whether a base already carries a commit is asked of the forge's comparison,
// which says how far ahead the commit is; contained is ahead by nothing. The
// API verb takes no repository flag, so the configured remote is named in the
// environment instead.
func TestGitHubContainsAsksTheForgeHowFarAheadTheCommitIs(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("compare/main...1111111111111111111111111111111111111111", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `{"status":"behind","ahead_by":0,"behind_by":12,"commits":[]}`,
	})
	runner.reply("compare/main...2222222222222222222222222222222222222222", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `{"status":"ahead","ahead_by":3,"behind_by":0,"commits":[{"sha":"2222222222222222222222222222222222222222"}]}`,
	})
	forge := GitHub{Runner: runner, Remote: "upstream"}

	contained, err := forge.Contains(context.Background(), "main", "1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("Contains() error = %v", err)
	}
	if !contained {
		t.Error("Contains() = false for a commit the base is ahead of, want true")
	}
	ahead, err := forge.Contains(context.Background(), "main", "2222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("Contains() error = %v", err)
	}
	if ahead {
		t.Error("Contains() = true for a commit three ahead of the base, want false")
	}
	calls := runner.matching("compare/")
	if len(calls) != 2 {
		t.Fatalf("the comparison was asked %d times, want twice", len(calls))
	}
	if !contains(calls[0], "api") || !contains(calls[0], "--method") || !contains(calls[0], "GET") {
		t.Errorf("compare args = %v, want the API verb asked as a GET", calls[0])
	}
	// The repository reaches the API verb as the `[HOST/]OWNER/REPO` gh
	// documents for GH_REPO, derived from the remote's URL, not as the URL.
	if scope := apiRepositoryScope(runner); scope != "example.invalid/acme/thing" {
		t.Errorf("GH_REPO = %q, want the repository derived from the remote's URL", scope)
	}

	// A comparison that says nothing about how far ahead the commit is cannot be
	// read as contained.
	silent := &scriptedRunner{}
	silent.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	silent.reply("compare/", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"status":"unknown"}`})
	if _, err := (GitHub{Runner: silent}).Contains(context.Background(), "main", "1111111111111111111111111111111111111111"); err == nil {
		t.Error("Contains() over a comparison naming no distance returned no error")
	}
	if _, err := (GitHub{Runner: silent}).Contains(context.Background(), "--main", "1111111111111111111111111111111111111111"); err == nil {
		t.Error("Contains() accepted a base that reads as an option")
	}
}

// Whether a target branch is protected decides whether a run may move the
// primary checkout's copy of it, so it is asked both ways the forge protects a
// branch, and a question the forge did not answer is never read as "open".
func TestGitHubProtectionAsksBothWaysAForgeProtectsABranch(t *testing.T) {
	t.Parallel()

	url := execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"}
	open := execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"name":"main","protected":false}`}
	cases := []struct {
		name    string
		branch  execution.ProcessResult
		rules   *execution.ProcessResult
		want    BranchProtection
		wantErr string
	}{
		{
			name:   "per-branch protection",
			branch: execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"name":"main","protected":true}`},
			want:   BranchProtection{Protected: true, By: "branch protection"},
		},
		{
			name:   "a ruleset requiring a pull request",
			branch: open,
			rules:  &execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `[{"type":"deletion"},{"type":"pull_request"}]`},
			want:   BranchProtection{Protected: true, By: "ruleset"},
		},
		{
			name:   "a ruleset that only shapes what is pushed",
			branch: open,
			rules:  &execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `[{"type":"deletion"},{"type":"non_fast_forward"}]`},
			want:   BranchProtection{},
		},
		{
			name:   "nothing at all",
			branch: open,
			rules:  &execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `[]`},
			want:   BranchProtection{},
		},
		{
			name:    "a branch the forge would not describe",
			branch:  execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "gh: Not Found (HTTP 404)\n"},
			wantErr: "HTTP 404",
		},
		{
			name:    "an answer that does not say",
			branch:  execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"name":"main"}`},
			wantErr: "does not say whether it is protected",
		},
		{
			name:    "rulesets the forge would not list",
			branch:  open,
			rules:   &execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "gh: Server Error (HTTP 502)\n"},
			wantErr: "HTTP 502",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runner := &scriptedRunner{}
			runner.reply("remote get-url", url)
			// Keyed so it cannot match the ruleset endpoint, whose path also
			// carries branches/main.
			runner.reply("{repo}/branches/main", tc.branch)
			if tc.rules != nil {
				runner.reply("rules/branches/main", *tc.rules)
			}
			got, err := (GitHub{Runner: runner}).Protection(context.Background(), "main")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Protection() = %#v, %v; want an error naming %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Protection() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("Protection() = %#v, want %#v", got, tc.want)
			}
			// The administrator-only protection endpoint is never what decides it.
			if asked := runner.matching("/protection"); len(asked) != 0 {
				t.Errorf("the admin-only protection endpoint was asked: %v", asked)
			}
			if scope := apiRepositoryScope(runner); scope != "example.invalid/acme/thing" {
				t.Errorf("GH_REPO = %q, want the repository derived from the remote's URL", scope)
			}
		})
	}
	if _, err := (GitHub{Runner: &scriptedRunner{}}).Protection(context.Background(), "--main"); err == nil {
		t.Error("Protection() accepted a branch that reads as an option")
	}
}

// The forge CLI is the one command the harness runs that needs a forge
// credential, and it is given one by name on top of the same allowlist every
// other harness-launched process gets. Nothing else the harness's environment
// happens to carry goes with it.
//
// The Git command this adapter runs beside it — resolving the remote's URL —
// reaches no network, so it gets the allowlist and no credential at all.
func TestTheForgeCLICarriesTheForgeCredentialAndNothingElseTheHarnessHolds(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp-for-the-forge")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-not-for-the-forge")
	t.Setenv("ANTHROPIC_API_KEY", "sk-not-for-the-forge")

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]\n"})
	runner.replyAfter("pr list", 1, execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":3,"url":"https://example.invalid/pull/3","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr create", execution.ProcessResult{Status: execution.ProcessSucceeded})

	forge := GitHub{Runner: runner, Dir: t.TempDir()}
	if _, err := forge.Ensure(context.Background(), Request{Head: "yoyodyne/task/abcd1234", Base: "main", Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	credentialed := 0
	for index, command := range runner.commands {
		environment := runner.environments[index]
		if len(environment) == 0 {
			t.Fatalf("%v was given no environment at all, so it inherited the harness's", command)
		}
		for _, name := range []string{"SLACK_BOT_TOKEN", "ANTHROPIC_API_KEY"} {
			if environmentCarries(environment, name) {
				t.Errorf("%v was given %s", command, name)
			}
		}
		switch {
		case contains(command, "remote"):
			if environmentCarries(environment, "GH_TOKEN") {
				t.Errorf("%v is a local Git command and was given the forge credential", command)
			}
		case environmentCarries(environment, "GH_TOKEN"):
			credentialed++
		default:
			t.Errorf("%v is a forge command and was given no forge credential", command)
		}
	}
	if credentialed == 0 {
		t.Fatal("no forge command was given the forge credential")
	}
}

func environmentCarries(environment []string, name string) bool {
	for _, entry := range environment {
		if carried, _, named := strings.Cut(entry, "="); named && carried == name {
			return true
		}
	}
	return false
}

// A queued merge's checks are read as the head, the files its change touches,
// each check's standing with the files a failing one annotates, and how far the
// base has moved on without the head.
func TestGitHubChecksReadsTheHeadItsChecksAndHowFarBehindItIs(t *testing.T) {
	t.Parallel()

	head := "1111111111111111111111111111111111111111"
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr view 713", execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `{"headRefOid":"` + head + `","files":[{"path":"internal/feature.go"}]}`})
	runner.reply("/check-runs?per_page", execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `{"check_runs":[{"id":9,"name":"go test","status":"completed","conclusion":"failure","html_url":"https://example.invalid/acme/thing/actions/runs/1/job/9"},{"id":10,"name":"vet","status":"completed","conclusion":"success"},{"id":11,"name":"race","status":"in_progress","conclusion":null}]}`})
	runner.reply("check-runs/9/annotations", execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `[{"path":"internal/other/other_test.go","start_line":12,"annotation_level":"failure","message":"TestOther failed"},{"path":".github","annotation_level":"failure","message":"Process completed with exit code 1."},{"path":"internal/other/other_test.go"}]`})
	runner.reply("compare/"+head+"...main", execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `{"ahead_by":31,"behind_by":0}`})

	reading, err := (GitHub{Runner: runner}).Checks(context.Background(), 713, "main")
	if err != nil {
		t.Fatalf("Checks() error = %v", err)
	}
	if reading.HeadCommit != head || len(reading.Files) != 1 || reading.Files[0] != "internal/feature.go" {
		t.Errorf("head = %q, files = %v", reading.HeadCommit, reading.Files)
	}
	if len(reading.Failing) != 1 || reading.Failing[0].Name != "go test" {
		t.Fatalf("failing = %#v, want the one failed check", reading.Failing)
	}
	if reading.Failing[0].ID != 9 || reading.Failing[0].Conclusion != "failure" {
		t.Errorf("failed check run = %d, conclusion = %q; want the run a re-run names and how it ended", reading.Failing[0].ID, reading.Failing[0].Conclusion)
	}
	if paths := reading.Failing[0].Paths; len(paths) != 2 || paths[0] != ".github" || paths[1] != "internal/other/other_test.go" {
		t.Errorf("annotated paths = %v, want each file once", paths)
	}
	if reading.Failing[0].URL != "https://example.invalid/acme/thing/actions/runs/1/job/9" {
		t.Errorf("url = %q, want the forge's page for the job", reading.Failing[0].URL)
	}
	if annotations := reading.Failing[0].Annotations; len(annotations) != 3 || annotations[0] != (Annotation{Path: "internal/other/other_test.go", Line: 12, Level: "failure", Message: "TestOther failed"}) {
		t.Errorf("annotations = %#v, want each kept whole in the forge's words", annotations)
	}
	if reading.Passing != 1 || len(reading.Pending) != 1 || reading.Pending[0] != "race" {
		t.Errorf("passing = %d, pending = %v", reading.Passing, reading.Pending)
	}
	if reading.BehindBy != 31 {
		t.Errorf("behind by = %d, want how far main has moved on without the head", reading.BehindBy)
	}
	comparison := runner.matching("compare/")[0]
	if !contains(comparison, "--jq") || !contains(comparison, "{ahead_by: .ahead_by}") {
		t.Errorf("comparison args = %v, want the distance selected before output is retained", comparison)
	}

	// A comparison that says nothing about the distance is not a head level with
	// its base.
	silent := &scriptedRunner{}
	for match, result := range runner.replies {
		silent.reply(match, result)
	}
	silent.reply("compare/"+head+"...main", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{}`})
	if _, err := (GitHub{Runner: silent}).Checks(context.Background(), 713, "main"); err == nil {
		t.Error("Checks() over a comparison naming no distance returned no error")
	}
}

// Running a failed check again is a POST to re-run its Actions job, and a
// forge that refuses says so.
func TestGitHubRerunCheckAsksTheForgeToRunTheJobAgain(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("actions/jobs/4215/rerun", execution.ProcessResult{Status: execution.ProcessSucceeded})
	if err := (GitHub{Runner: runner}).RerunCheck(context.Background(), 4215); err != nil {
		t.Fatalf("RerunCheck() error = %v", err)
	}
	calls := runner.matching("actions/jobs/4215/rerun")
	if len(calls) != 1 || !contains(calls[0], "POST") {
		t.Fatalf("calls = %v, want one POST re-running the job", calls)
	}

	refused := &scriptedRunner{}
	refused.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	refused.reply("actions/jobs/4215/rerun", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 403: Resource not accessible by integration\n"})
	if err := (GitHub{Runner: refused}).RerunCheck(context.Background(), 4215); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("RerunCheck() refused = %v, want the forge's refusal", err)
	}
	if err := (GitHub{Runner: runner}).RerunCheck(context.Background(), 0); err == nil {
		t.Error("RerunCheck() of no check run returned no error")
	}
}

// A failing job's log is read as its tail, trailing blank lines dropped, and a
// forge that will not give it says so.
func TestGitHubJobLogTailReadsTheLastLinesOfTheJobsLog(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("actions/jobs/4215/logs", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "one\ntwo\nthree\nfour\n\n\n"})
	tail, err := (GitHub{Runner: runner}).JobLogTail(context.Background(), 4215, 2)
	if err != nil {
		t.Fatalf("JobLogTail() error = %v", err)
	}
	if tail != "three\nfour" {
		t.Fatalf("tail = %q, want the last two lines and no trailing blank ones", tail)
	}
	calls := runner.matching("actions/jobs/4215/logs")
	if len(calls) != 1 || !contains(calls[0], "GET") {
		t.Fatalf("calls = %v, want one GET of the job's log", calls)
	}
	if whole := logTail("a\nb\n", 60); whole != "a\nb" {
		t.Errorf("logTail of a short log = %q, want all of it", whole)
	}

	refused := &scriptedRunner{}
	refused.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	refused.reply("actions/jobs/4215/logs", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 410: logs expired\n"})
	if _, err := (GitHub{Runner: refused}).JobLogTail(context.Background(), 4215, 60); err == nil || !strings.Contains(err.Error(), "HTTP 410") {
		t.Errorf("JobLogTail() refused = %v, want the forge's refusal", err)
	}
	if _, err := (GitHub{Runner: runner}).JobLogTail(context.Background(), 0, 60); err == nil {
		t.Error("JobLogTail() of no check run returned no error")
	}
	if errors.Is(func() error {
		_, err := (GitHub{Runner: refused}).JobLogTail(context.Background(), 4215, 60)
		return err
	}(), ErrForgeAccessRefused) {
		t.Error("a log the forge no longer holds was read as the token being refused")
	}
}

// The failing step's lines are those ending at the last error the log marks,
// not the forge's clean-up steps after it; a log marking none is read as its
// tail.
func TestJobLogTailEndsAtTheFailingStep(t *testing.T) {
	t.Parallel()

	log := "setup\n##[group]Run make test\n--- FAIL: TestThing\n##[error]Process completed with exit code 2.\nPost job cleanup.\nCleaning up orphan processes\n"
	if tail := logTail(log, 2); tail != "--- FAIL: TestThing\n##[error]Process completed with exit code 2." {
		t.Errorf("logTail = %q, want the lines ending at the failing step's error", tail)
	}
	if tail := logTail("a\nb\nc\n", 2); tail != "b\nc" {
		t.Errorf("logTail with no error marked = %q, want the last lines", tail)
	}
}

// failingJob is a job whose "Check" step failed between its "Download
// dependencies" step and the forge's clean-up after it.
const failingJob = `{"name":"build","steps":[` +
	`{"name":"Download dependencies","number":3,"conclusion":"success","started_at":"2026-10-07T05:40:00Z","completed_at":"2026-10-07T05:40:09Z"},` +
	`{"name":"Check","number":5,"conclusion":"failure","started_at":"2026-10-07T05:40:10Z","completed_at":"2026-10-07T05:44:12Z"},` +
	`{"name":"Build","number":6,"conclusion":"skipped","started_at":null,"completed_at":null}]}`

// failingJobLog is that job's log as the forge stamps it: the download step's
// lines, the check step's, with a test failing well above the line the step
// failed on and a line per passing package below it, and the clean-up.
func failingJobLog(passingPackages int) string {
	lines := []string{
		"2026-10-07T05:40:01.1000000Z ##[group]Run go mod download",
		"2026-10-07T05:40:08.2000000Z go: downloading something",
		"2026-10-07T05:40:10.0500000Z ##[group]Run make check",
		"2026-10-07T05:40:10.0600000Z ##[endgroup]",
		"2026-10-07T05:41:00.0000000Z --- FAIL: TestLauncher (0.20s)",
		"2026-10-07T05:41:00.0000001Z     launcher_test.go:40: the provider's tool is not installed",
		"2026-10-07T05:41:00.0000002Z FAIL\tgithub.com/acme/thing/internal/launcher\t0.4s",
	}
	for index := 0; index < passingPackages; index++ {
		lines = append(lines, fmt.Sprintf("2026-10-07T05:43:%02d.0000000Z ok  \tgithub.com/acme/thing/internal/package%03d\t1.0s", index%60, index))
	}
	lines = append(lines,
		"2026-10-07T05:44:11.9000000Z make: *** [Makefile:79: test] Error 1",
		"2026-10-07T05:44:12.4000000Z ##[error]Process completed with exit code 2.",
		"2026-10-07T05:44:13.0000000Z Post job cleanup.",
		"2026-10-07T05:44:14.0000000Z Cleaning up orphan processes",
		"",
	)
	return strings.Join(lines, "\n")
}

// The failing step is named from the forge's account of the job, and its own
// lines are kept, stamps taken off, ending at the line it failed on: not the
// steps before it and not the clean-up after.
func TestGitHubFailedStepNamesTheStepAndKeepsItsOwnLines(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("actions/jobs/4215 --jq", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: failingJob})
	runner.reply("actions/jobs/4215/logs", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: failingJobLog(3)})
	failure, err := (GitHub{Runner: runner}).FailedStep(context.Background(), 4215, 6<<10)
	if err != nil {
		t.Fatalf("FailedStep() error = %v", err)
	}
	if failure.Job != "build" || failure.Step != "Check" || failure.StepNumber != 5 || failure.StepUnread != "" {
		t.Errorf("failure = %#v, want job build, step 5 Check", failure)
	}
	if failure.Cut {
		t.Error("a step shorter than the bound was reported cut")
	}
	if !strings.HasPrefix(failure.Output, "##[group]Run make check\n") || !strings.HasSuffix(failure.Output, "##[error]Process completed with exit code 2.") {
		t.Errorf("output = %q, want the check step's lines from its start to the line it failed on", failure.Output)
	}
	for _, unwanted := range []string{"go mod download", "Post job cleanup", "2026-10-07T"} {
		if strings.Contains(failure.Output, unwanted) {
			t.Errorf("output carries %q, which is not the failing step's text:\n%s", unwanted, failure.Output)
		}
	}
}

// A step whose output is longer than the bound keeps its end, whole lines up to
// and including the one it failed on, within the bound, and says it was cut.
func TestGitHubFailedStepKeepsTheEndOfAStepLongerThanTheBound(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("actions/jobs/4215 --jq", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: failingJob})
	runner.reply("actions/jobs/4215/logs", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: failingJobLog(500)})
	const bound = 2 << 10
	failure, err := (GitHub{Runner: runner}).FailedStep(context.Background(), 4215, bound)
	if err != nil {
		t.Fatalf("FailedStep() error = %v", err)
	}
	if !failure.Cut || len(failure.Output) > bound {
		t.Fatalf("output is %d bytes, cut %v; want it cut to at most %d", len(failure.Output), failure.Cut, bound)
	}
	if !strings.HasSuffix(failure.Output, "make: *** [Makefile:79: test] Error 1\n##[error]Process completed with exit code 2.") {
		t.Errorf("output does not end at the line the step failed on:\n%s", failure.Output)
	}
	if !strings.HasPrefix(failure.Output, "ok  \t") {
		t.Errorf("output does not begin on a whole line:\n%s", failure.Output)
	}
	if tail, cut := failingTail([]string{strings.Repeat("é", 100)}, 51); !cut || len(tail) > 51 || !utf8.ValidString(tail) {
		t.Errorf("one line longer than the bound = %q, cut %v; want its end, whole characters, within the bound", tail, cut)
	}
}

// A log the forge will not give is an error that still carries the job and the
// step it learnt of, and a job whose steps cannot be read still has its log
// read whole, saying why no step is named.
func TestGitHubFailedStepWhenTheLogOrTheStepsCannotBeRead(t *testing.T) {
	t.Parallel()

	unfetched := &scriptedRunner{}
	unfetched.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	unfetched.reply("actions/jobs/4215 --jq", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: failingJob})
	unfetched.reply("actions/jobs/4215/logs", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 410: logs expired\n"})
	failure, err := (GitHub{Runner: unfetched}).FailedStep(context.Background(), 4215, 6<<10)
	if err == nil || !strings.Contains(err.Error(), "HTTP 410") || errors.Is(err, ErrForgeAccessRefused) {
		t.Errorf("FailedStep() error = %v, want the forge's own words and not a refused token", err)
	}
	if failure.Step != "Check" || failure.Output != "" {
		t.Errorf("failure = %#v, want the step named and no output", failure)
	}

	cut := &scriptedRunner{}
	cut.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	cut.reply("actions/jobs/4215/logs", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "the beginning\n", OutputTruncation: "[output truncated at 8388608 bytes; the rest was not retained]"})
	if _, err := (GitHub{Runner: cut}).FailedStep(context.Background(), 4215, 6<<10); err == nil || !strings.Contains(err.Error(), "its end, where the step failed, was not read") {
		t.Errorf("FailedStep() of a log longer than the harness keeps = %v, want it refused", err)
	}

	stepless := &scriptedRunner{}
	stepless.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	stepless.reply("actions/jobs/4215 --jq", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502: bad gateway\n"})
	stepless.reply("actions/jobs/4215/logs", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: failingJobLog(1)})
	failure, err = (GitHub{Runner: stepless}).FailedStep(context.Background(), 4215, 6<<10)
	if err != nil {
		t.Fatalf("FailedStep() error = %v, want the log read without the steps", err)
	}
	if failure.Step != "" || !strings.Contains(failure.StepUnread, "HTTP 502") {
		t.Errorf("failure = %#v, want no step and the reason none is named", failure)
	}
	if !strings.Contains(failure.Output, "go mod download") || !strings.HasSuffix(failure.Output, "##[error]Process completed with exit code 2.") {
		t.Errorf("output = %q, want the whole log's lines ending at the line it failed on", failure.Output)
	}
	if _, err := (GitHub{Runner: stepless}).FailedStep(context.Background(), 0, 6<<10); err == nil {
		t.Error("FailedStep() of no check run returned no error")
	}
}

// A token the forge refuses — reading a job's log, or running it again — is
// told apart from anything else the forge says, so the item can name the grant
// as the operator's.
func TestGitHubTellsATokenTheForgeRefusesApart(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("actions/jobs/4215/logs", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "gh: Resource not accessible by integration (HTTP 403)\n"})
	runner.reply("actions/jobs/4215/rerun", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "gh: Must have admin rights to Repository. (HTTP 403)\n"})
	if _, err := (GitHub{Runner: runner}).JobLogTail(context.Background(), 4215, 60); !errors.Is(err, ErrForgeAccessRefused) || !strings.Contains(err.Error(), "Resource not accessible") {
		t.Errorf("JobLogTail() = %v, want the forge refusing the token, in its words", err)
	}
	if err := (GitHub{Runner: runner}).RerunCheck(context.Background(), 4215); !errors.Is(err, ErrForgeAccessRefused) {
		t.Errorf("RerunCheck() = %v, want the forge refusing the token", err)
	}
}

// Withdrawing a queued merge is the forge's disable-auto, and a request with
// nothing armed has nothing to withdraw.
func TestGitHubDisableAutoMergeWithdrawsTheQueuedMerge(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr merge 713", execution.ProcessResult{Status: execution.ProcessSucceeded})
	runner.reply("isInMergeQueue", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{"pullRequest":{"id":"PR_713","isInMergeQueue":false}}}}`})
	if err := (GitHub{Runner: runner}).DisableAutoMerge(context.Background(), 713); err != nil {
		t.Fatalf("DisableAutoMerge() error = %v", err)
	}
	if calls := runner.matching("pr merge 713"); len(calls) != 1 || !contains(calls[0], "--disable-auto") {
		t.Fatalf("calls = %v, want one disable-auto", calls)
	}
	// A request the queue does not hold — which is every request on a forge with
	// no merge queue — has nothing to take out of it.
	if dequeued := runner.matching("dequeuePullRequest"); len(dequeued) != 0 {
		t.Errorf("dequeued a request the merge queue does not hold: %v", dequeued)
	}

	refused := &scriptedRunner{}
	refused.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	refused.reply("pr merge 713", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "GraphQL: Resource not accessible by integration"})
	if err := (GitHub{Runner: refused}).DisableAutoMerge(context.Background(), 713); err == nil {
		t.Error("DisableAutoMerge() reported a refused withdrawal as done")
	}
}

// The merge queue consumes a request's auto-merge as it takes the request, so
// turning auto-merge off leaves a queued request queued, and the queue would
// land the head the harness withdrew it to rewrite. Withdrawing the merge takes
// the request out of the queue as well, and a dequeue that fails, or a forge
// that will not say whether the queue holds the request, refuses the withdrawal.
func TestGitHubDisableAutoMergeTakesAQueuedRequestOutOfTheMergeQueue(t *testing.T) {
	t.Parallel()

	queued := execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{"pullRequest":{"id":"PR_kwDO713","isInMergeQueue":true}}}}`}
	script := func() *scriptedRunner {
		runner := &scriptedRunner{}
		runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
		// The queue took the auto-merge with the request, so the forge says there
		// is none to disable.
		runner.reply("pr merge 713", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Pull request acme/thing#713 does not have auto merge enabled"})
		runner.reply("isInMergeQueue", queued)
		return runner
	}

	runner := script()
	runner.reply("dequeuePullRequest", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"dequeuePullRequest":{"mergeQueueEntry":{"id":"MQE_1"}}}}`})
	if err := (GitHub{Runner: runner}).DisableAutoMerge(context.Background(), 713); err != nil {
		t.Fatalf("DisableAutoMerge() error = %v", err)
	}
	asked := runner.matching("isInMergeQueue")
	if len(asked) != 1 || !contains(asked[0], "number=713") {
		t.Errorf("merge queue query = %v, want request 713 asked about", asked)
	}
	dequeued := runner.matching("dequeuePullRequest")
	if len(dequeued) != 1 || !contains(dequeued[0], "id=PR_kwDO713") || apiRepositoryScope(runner) == "" {
		t.Fatalf("dequeue = %v (scope %q), want request 713 taken out of the queue by its node id in the configured repository", dequeued, apiRepositoryScope(runner))
	}
	// The auto-merge goes first: a request whose auto-merge is still armed could
	// be taken into the queue again the moment it was taken out.
	var order []string
	for _, command := range runner.commands {
		switch joined := strings.Join(command, " "); {
		case strings.Contains(joined, "pr merge 713"):
			order = append(order, "disable-auto")
		case strings.Contains(joined, "dequeuePullRequest"):
			order = append(order, "dequeue")
		}
	}
	if strings.Join(order, ",") != "disable-auto,dequeue" {
		t.Errorf("order = %v, want the auto-merge turned off before the request is dequeued", order)
	}

	failed := script()
	failed.reply("dequeuePullRequest", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "GraphQL: Resource not accessible by integration (dequeuePullRequest)"})
	if err := (GitHub{Runner: failed}).DisableAutoMerge(context.Background(), 713); err == nil || !strings.Contains(err.Error(), "take pull request 713 out of the merge queue") {
		t.Errorf("DisableAutoMerge() with a refused dequeue error = %v, want the withdrawal refused naming the dequeue", err)
	}

	unanswered := script()
	unanswered.reply("isInMergeQueue", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502"})
	if err := (GitHub{Runner: unanswered}).DisableAutoMerge(context.Background(), 713); err == nil {
		t.Error("DisableAutoMerge() reported a withdrawal done without learning whether the merge queue holds the request")
	}
	if dequeued := unanswered.matching("dequeuePullRequest"); len(dequeued) != 0 {
		t.Errorf("dequeued on an unanswered question: %v", dequeued)
	}
}

// A publication whose work landed by another vehicle is closed with that
// vehicle named. The comment is the whole point of closing it here rather than
// by hand: a pull request closed by a machine with no account of why is the same
// unexplained state as one left open.
func TestGitHubCloseRetiresASupersededRequestWithItsComment(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":44,"url":"https://example.invalid/pull/44","state":"OPEN","mergedAt":""}]`,
	})
	runner.reply("pr close", execution.ProcessResult{Status: execution.ProcessSucceeded})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	comment := "superseded by pull request #46"
	closure, err := forge.Close(context.Background(), CloseRequest{Head: "yoyodyne/task/abcd1234", Number: 44, Comment: comment})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !closure.Closed || closure.State != "CLOSED" {
		t.Fatalf("Close() = %#v, want the request closed", closure)
	}
	closes := runner.matching("pr close")
	if len(closes) != 1 {
		t.Fatalf("pr close calls = %v, want exactly one", closes)
	}
	for _, expected := range []string{"44", "--comment", comment, "--repo", "https://example.invalid/acme/thing"} {
		if !contains(closes[0], expected) {
			t.Errorf("pr close args = %v, missing %q", closes[0], expected)
		}
	}
	// The branch is the harness's own compare-and-swap deletion, not the forge's:
	// asking the forge for it would delete a branch somebody else had moved.
	if contains(closes[0], "--delete-branch") {
		t.Errorf("pr close args = %v, want the branch left to the harness's own deletion", closes[0])
	}
}

// The sweep that closes these repeats on every `yoyo reconcile`, so a request
// somebody has already closed — by hand, or by an earlier sweep whose record of
// it never reached disk — must not collect a second comment.
func TestGitHubCloseLeavesAnAlreadyClosedRequestAlone(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":44,"url":"https://example.invalid/pull/44","state":"CLOSED","mergedAt":""}]`,
	})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	closure, err := forge.Close(context.Background(), CloseRequest{Head: "yoyodyne/task/abcd1234", Number: 44, Comment: "superseded"})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if closure.Closed || closure.State != "CLOSED" {
		t.Fatalf("Close() = %#v, want the request reported as it stands rather than closed again", closure)
	}
	if closes := runner.matching("pr close"); len(closes) != 0 {
		t.Fatalf("pr close calls = %v, want none for a request already closed", closes)
	}
}

// A merged request is the one thing this must never touch. Closing it would be a
// claim about where the work is that the merge has already answered, so the
// merge is reported back and the caller decides what to make of the record that
// said otherwise.
func TestGitHubCloseRefusesToRetireAMergedRequest(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":44,"url":"https://example.invalid/pull/44","state":"MERGED","mergedAt":"2026-08-18T00:00:00Z"}]`,
	})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	closure, err := forge.Close(context.Background(), CloseRequest{Head: "yoyodyne/task/abcd1234", Number: 44, Comment: "superseded"})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if closure.Closed || !closure.Merged {
		t.Fatalf("Close() = %#v, want the merge reported and nothing closed", closure)
	}
	if closes := runner.matching("pr close"); len(closes) != 0 {
		t.Fatalf("pr close calls = %v, want none for a merged request", closes)
	}
}

// The number and the branch have to still describe one request. A number that
// has stopped naming this branch's request is a record that drifted, and acting
// on it would close somebody else's pull request.
func TestGitHubCloseRefusesANumberThatIsNotTheBranchesRequest(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("pr list", execution.ProcessResult{
		Status: execution.ProcessSucceeded,
		Stdout: `[{"number":51,"url":"https://example.invalid/pull/51","state":"OPEN","mergedAt":""}]`,
	})
	forge := GitHub{Runner: runner, Dir: t.TempDir()}

	_, err := forge.Close(context.Background(), CloseRequest{Head: "yoyodyne/task/abcd1234", Number: 44, Comment: "superseded"})
	if err == nil || !strings.Contains(err.Error(), "carries pull request 51") {
		t.Fatalf("Close() drifted error = %v", err)
	}
	if closes := runner.matching("pr close"); len(closes) != 0 {
		t.Fatalf("pr close calls = %v, want none when the record and the forge disagree", closes)
	}
}

// A close with nothing to say is refused before it reaches the CLI, because a
// request closed without a comment is exactly the unexplained state this exists
// to end.
func TestGitHubCloseRequiresAComment(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	forge := GitHub{Runner: runner, Dir: t.TempDir()}
	if _, err := forge.Close(context.Background(), CloseRequest{Head: "yoyodyne/task/abcd1234", Number: 44}); err == nil || !strings.Contains(err.Error(), "comment naming where its work landed") {
		t.Fatalf("Close() commentless error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("a refused close still ran commands: %v", runner.commands)
	}
}

// A branch's own checks are read on the commit it points at, each by name and
// standing, which is what confirms a check red on a level head is the branch's.
func TestGitHubBranchChecksReadsEachCheckOnTheBranchesHead(t *testing.T) {
	t.Parallel()

	head := "2222222222222222222222222222222222222222"
	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("commits/main/check-runs?per_page", execution.ProcessResult{Status: execution.ProcessSucceeded,
		Stdout: `{"check_runs":[{"name":"build","head_sha":"` + head + `","status":"completed","conclusion":"success"},{"name":"adoption","head_sha":"` + head + `","status":"completed","conclusion":"failure"},{"name":"race","head_sha":"` + head + `","status":"queued","conclusion":null}]}`})

	reading, err := (GitHub{Runner: runner}).BranchChecks(context.Background(), "main")
	if err != nil {
		t.Fatalf("BranchChecks() error = %v", err)
	}
	if reading.HeadCommit != head {
		t.Errorf("head = %q, want the commit main points at", reading.HeadCommit)
	}
	if len(reading.Passing) != 1 || reading.Passing[0] != "build" || len(reading.Failing) != 1 || reading.Failing[0] != "adoption" || len(reading.Pending) != 1 || reading.Pending[0] != "race" {
		t.Errorf("reading = %#v, want build passing, adoption failing, race pending", reading)
	}

	refused := &scriptedRunner{}
	refused.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	refused.reply("commits/main/check-runs?per_page", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502"})
	if _, err := (GitHub{Runner: refused}).BranchChecks(context.Background(), "main"); err == nil {
		t.Error("BranchChecks() over a refused read returned no error")
	}
	if _, err := (GitHub{Runner: runner}).BranchChecks(context.Background(), "--main"); err == nil {
		t.Error("BranchChecks() of a branch starting with a dash returned no error")
	}
}

// A sweep asks about many branches in one query rather than one listing each,
// and reads each answer as State reads one: a merged request, an open one the
// merge queue holds, an open one nothing holds, and a branch with no request at
// all, which is absent from the answer rather than a failure of the batch.
func TestGitHubStatesAsksAboutManyBranchesInOneQuery(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{}
	runner.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	runner.reply("api graphql", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"data":{"repository":{
		"h0":{"nodes":[{"number":10,"url":"u10","state":"MERGED","mergedAt":"2026-09-29T00:00:00Z","headRefOid":"abc","autoMergeRequest":null,"mergeCommit":{"oid":"def"},"isInMergeQueue":false}]},
		"h1":{"nodes":[{"number":11,"url":"u11","state":"OPEN","mergedAt":null,"headRefOid":"abd","autoMergeRequest":null,"mergeCommit":null,"isInMergeQueue":true}]},
		"h2":{"nodes":[{"number":12,"url":"u12","state":"OPEN","mergedAt":null,"headRefOid":"abe","autoMergeRequest":null,"mergeCommit":null,"isInMergeQueue":false}]},
		"h3":{"nodes":[]}}}}`})

	heads := []string{"yoyodyne/a/1", "yoyodyne/b/2", "yoyodyne/c/3", "yoyodyne/d/4"}
	states, err := (GitHub{Runner: runner}).States(context.Background(), heads)
	if err != nil {
		t.Fatalf("States() error = %v", err)
	}
	if queries := runner.matching("api graphql"); len(queries) != 1 {
		t.Fatalf("queries = %v, want every branch asked about in one", queries)
	}
	if listings := runner.matching("pr list"); len(listings) != 0 {
		t.Fatalf("listings = %v, want no branch listed on its own", listings)
	}
	query := strings.Join(runner.matching("api graphql")[0], " ")
	for index, head := range heads {
		if !strings.Contains(query, fmt.Sprintf("h%d=%s", index, head)) {
			t.Errorf("query %q does not pass branch %s as a string variable", query, head)
		}
	}
	if merged := states[heads[0]]; !merged.Merged || merged.MergeCommit != "def" || merged.Number != 10 {
		t.Errorf("merged = %#v, want a merged request with its merge commit", merged)
	}
	if queued := states[heads[1]]; queued.Merged || !queued.AutoMerge {
		t.Errorf("queued = %#v, want the merge queue read as a merge the forge holds", queued)
	}
	if open := states[heads[2]]; open.AutoMerge || open.State != "OPEN" {
		t.Errorf("open = %#v, want an open request nothing holds", open)
	}
	if _, found := states[heads[3]]; found {
		t.Errorf("a branch with no request was answered: %#v", states[heads[3]])
	}

	failed := &scriptedRunner{}
	failed.reply("remote get-url", execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "https://example.invalid/acme/thing\n"})
	failed.reply("api graphql", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "HTTP 502"})
	if _, err := (GitHub{Runner: failed}).States(context.Background(), heads); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("States() on an unanswered query error = %v, want the forge's words", err)
	}

	tooMany := make([]string, MaxStatesPerQuery+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("yoyodyne/x/%d", index)
	}
	if _, err := (GitHub{Runner: &scriptedRunner{}}).States(context.Background(), tooMany); err == nil {
		t.Error("States() asked about more branches than one query may")
	}
}

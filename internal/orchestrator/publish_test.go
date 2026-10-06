package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The shape the operator asked for: the developer phase pushes the branch and
// opens the pull request, and the approving verdict is what merges it. Both are
// performed by the harness — the fake forge here is the harness's own adapter,
// and neither agent is ever asked to run anything.
func TestPipelinePublishesInTheDeveloperPhaseAndMergesOnApproval(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		// The branch must already be published by the time the reviewer is asked,
		// because a pull request nobody can see is not what a reviewer reviews.
		if request.Role == domain.RoleReviewer && len(forge.Opened) == 0 {
			return errors.New("the reviewer ran before the branch was published")
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PullRequest == nil {
		t.Fatalf("outcome = %#v, want a published pull request", outcome)
	}
	if outcome.PullRequest.Number != 1 || !outcome.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want the approved request merged", outcome.PullRequest)
	}
	if outcome.PublishFailure != "" || outcome.PublishSkipped != "" {
		t.Fatalf("outcome = %#v, want a clean publication", outcome)
	}
	if len(forge.Opened) != 1 {
		t.Fatalf("pull requests opened = %d, want exactly one", len(forge.Opened))
	}
	if forge.Opened[0].Base != "main" || forge.Opened[0].Head != outcome.Branch {
		t.Fatalf("pull request request = %#v, want the run branch into main", forge.Opened[0])
	}
	// The forge is what merged, and it was asked for the one method that puts the
	// promoted commit itself on the remote target, for exactly the commit this
	// run integrated.
	if len(forge.Merges) != 1 {
		t.Fatalf("forge merges = %d, want exactly one", len(forge.Merges))
	}
	if forge.Merges[0].Method != publish.MergeCommit || forge.Merges[0].Number != outcome.PullRequest.Number {
		t.Errorf("merge request = %#v, want the published request merged under a merge commit", forge.Merges[0])
	}
	if forge.Merges[0].HeadCommit != outcome.Integration.SourceCommit {
		t.Errorf("merged head = %q, want the integrated commit %q", forge.Merges[0].HeadCommit, outcome.Integration.SourceCommit)
	}
	if outcome.PullRequest.MergeMethod != string(publish.MergeCommit) {
		t.Errorf("recorded merge method = %q, want the method the forge was asked for", outcome.PullRequest.MergeMethod)
	}
	// The relationship the merge actually produces: the remote target is the
	// forge's own merge commit, it contains the promoted commit unrewritten, and
	// it carries exactly that commit's content.
	remoteTarget := publishedCommit(t, remote, "main")
	if remoteTarget == outcome.Integration.TargetCommit {
		t.Errorf("remote main = %q, want the forge's merge commit above the promoted commit", remoteTarget)
	}
	if outcome.PullRequest.MergeCommit != remoteTarget {
		t.Errorf("recorded merge commit = %q, want the remote target %q", outcome.PullRequest.MergeCommit, remoteTarget)
	}
	assertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	// The last step of the promotion is local: the merge left the remote a
	// commit ahead, so the run catches the local branch up onto it rather than
	// leaving a checkout somebody has to pull by hand.
	if local := publishedCommit(t, repository, "main"); local != remoteTarget {
		t.Errorf("local main = %q, want the forge's merge commit %q", local, remoteTarget)
	}
	if outcome.Catchup == nil || !outcome.Catchup.Advanced || outcome.Catchup.RemoteCommit != remoteTarget {
		t.Errorf("catch-up = %#v, want main advanced onto %q", outcome.Catchup, remoteTarget)
	}
	if outcome.Catchup.Held != "" {
		t.Errorf("catch-up was held: %s", outcome.Catchup.Held)
	}
	if published := publishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}

	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PullRequest == nil || !state.PullRequest.Merged || state.PullRequest.Branch != outcome.Branch {
		t.Fatalf("durable pull request = %#v", state.PullRequest)
	}
	if !strings.Contains(tracker.Notes, outcome.PullRequest.URL) {
		t.Errorf("tracker notes do not name the pull request:\n%s", tracker.Notes)
	}
}

// A repair attempt updates the pull request its first attempt opened. A second
// request for one branch would give the same change two places to be reviewed.
func TestPipelinePublishesEveryAttemptOntoOnePullRequest(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		attempts++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"),
			[]byte(fmt.Sprintf("attempt %d\n", attempts)), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want one", outcome.RepairAttempts)
	}
	if len(forge.Opened) != 2 || forge.Number != 1 {
		t.Fatalf("forge saw %d requests and issued number %d, want one pull request updated twice", len(forge.Opened), forge.Number)
	}
	// Every attempt is published, so what merged is the repaired change rather
	// than the one the reviewer rejected.
	if outcome.PullRequest.HeadCommit != outcome.Integration.SourceCommit {
		t.Errorf("published head = %q, want the integrated commit %q", outcome.PullRequest.HeadCommit, outcome.Integration.SourceCommit)
	}
	assertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
}

// The request the forge is actually asked for says what it contains: the work
// item's own text and the shape of the change, rather than the boilerplate a
// reader of PR #314 got. Both are wired from what the run already holds — the
// claimed item and the summary the harness computed of its worktree — so this
// goes through the pipeline rather than through the rendering alone.
func TestPipelineOpensAPullRequestThatSaysWhatItContains(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Task",
		Description:        "The published body says nothing a reader wants.",
		AcceptanceCriteria: "A reader can tell what the request contains.",
		Notes:              "Admitted to the backlog by the product manager.",
		Status:             "open",
	}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(forge.Opened) != 1 {
		t.Fatalf("pull requests opened = %d, want exactly one", len(forge.Opened))
	}
	body := forge.Opened[0].Body
	for _, want := range []string{
		tracker.Item.Description,
		tracker.Item.AcceptanceCriteria,
		"feature.txt",
		"Run: `" + outcome.RunID + "`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("published body omits %q:\n%s", want, body)
		}
	}
	// The notes are where a run's own record goes, so they are the one part of a
	// work item a published body must not republish.
	if strings.Contains(body, tracker.Item.Notes) {
		t.Errorf("published body carries the item's notes:\n%s", body)
	}
}

// What the body carries, and what it still must not: the item's governed text
// and the computed change, with nothing the developer wrote and nothing from
// the notes a run records itself in.
func TestPullRequestBodyCarriesGovernedTextAndComputedFacts(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID:                 "yoyodyne-ifd.222",
		Title:              "A pull request says what it contains",
		Description:        "The body is identical on every request and says nothing about the change.",
		AcceptanceCriteria: "A reader can tell what the request contains without opening the tracker.",
		Notes:              "Goal served: publish that work as pull requests the harness opens.",
	}
	outcome := Outcome{
		RunID:      "run-222",
		WorkItemID: item.ID,
		Branch:     "yoyodyne/yoyodyne-ifd-222",
		BaseCommit: "1a2b3c4",
		Summary:    "I rewrote the body, and I think it reads very well now.",
		Changes: gitworktree.ChangeSummary{
			Status:   "M internal/orchestrator/publish.go\nA internal/orchestrator/publish_test.go",
			DiffStat: " internal/orchestrator/publish.go | 40 ++++++---\n 1 file changed, 30 insertions(+), 10 deletions(-)",
		},
	}

	body := pullRequestBody(item, outcome, "main")
	for _, want := range []string{
		item.Description,
		item.AcceptanceCriteria,
		"M internal/orchestrator/publish.go",
		"1 file changed, 30 insertions(+), 10 deletions(-)",
		"- Run: `run-222`",
		"- Branch: `yoyodyne/yoyodyne-ifd-222` into `main`",
		"- Base commit: `1a2b3c4`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body omits %q:\n%s", want, body)
		}
	}
	// The rationale the item preserved: the body is governed text and computed
	// fact, so what an agent wrote is not in it.
	for _, unwanted := range []string{outcome.Summary, item.Notes} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body carries unreviewed text %q:\n%s", unwanted, body)
		}
	}
	// The merge mechanics is one line rather than the paragraph it was.
	mechanics := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, string(mergeMethod)+"` method") {
			mechanics++
		}
	}
	if mechanics != 1 {
		t.Errorf("lines describing the merge mechanics = %d, want exactly one:\n%s", mechanics, body)
	}
}

// An item with no description, and a run whose change was never summarized,
// leave their sections out entirely: an empty heading reads as a work item that
// says nothing and a change that touched nothing.
func TestPullRequestBodyOmitsWhatItHasNothingToSay(t *testing.T) {
	t.Parallel()

	body := pullRequestBody(beads.WorkItem{ID: "yoyodyne-task", Title: "Task"},
		Outcome{RunID: "run-1", WorkItemID: "yoyodyne-task", Branch: "run", BaseCommit: "1a2b3c4"}, "main")
	for _, unwanted := range []string{"## What the work item asks for", "## Acceptance criteria", "## What changed"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body carries an empty %q section:\n%s", unwanted, body)
		}
	}
	if !strings.Contains(body, "- Run: `run-1`") {
		t.Errorf("body omits the harness evidence it always carries:\n%s", body)
	}
}

// A forge refuses a body past its own limit, so a long item or a change
// touching hundreds of files must not be what stops a run publishing.
func TestPullRequestBodyStaysInsideWhatAForgeAccepts(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a line of the work item, written at length\n", 2000)
	item := beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Description: long, AcceptanceCriteria: long}
	outcome := Outcome{
		RunID:      "run-1",
		WorkItemID: item.ID,
		Changes:    gitworktree.ChangeSummary{Status: long, DiffStat: long},
	}

	body := pullRequestBody(item, outcome, "main")
	// GitHub refuses a body over 65536 bytes, and a body that fits is worth
	// nothing if it does not say it was cut.
	if len(body) > 64<<10 {
		t.Fatalf("body is %d bytes, want it inside what the forge accepts", len(body))
	}
	if strings.Count(body, "cut to keep this body") != 4 {
		t.Errorf("body does not say each of its four cut blocks was cut:\n%s", body[:2000])
	}
}

// The defect PR #314 showed: a title cut at the commit-subject bound ended
// mid-word, at "regist".
func TestPullRequestTitleCutsAtAWordBoundary(t *testing.T) {
	t.Parallel()

	long := "Every Resolved() call site means has-a-disposition, proven by an audit that names the registry it walked"
	for _, test := range []struct {
		name  string
		title string
		want  string
	}{
		{name: "keeps a title that fits", title: "Wire the\n happy path\t", want: "yoyodyne-ifd.222 Wire the happy path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := pullRequestTitle(beads.WorkItem{Title: test.title}, Outcome{WorkItemID: "yoyodyne-ifd.222"}); got != test.want {
				t.Fatalf("pullRequestTitle() = %q, want %q", got, test.want)
			}
		})
	}

	got := pullRequestTitle(beads.WorkItem{Title: long}, Outcome{WorkItemID: "yoyodyne-ifd.222"})
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("pullRequestTitle() = %q, want a cut title to say it was cut", got)
	}
	cut := strings.TrimSuffix(strings.TrimPrefix(got, "yoyodyne-ifd.222 "), "…")
	if len(cut) > maxPullRequestTitleBytes {
		t.Errorf("pullRequestTitle() carried %d bytes of the title, want at most %d", len(cut), maxPullRequestTitleBytes)
	}
	if !strings.HasPrefix(long, cut+" ") {
		t.Errorf("pullRequestTitle() = %q, which cuts %q inside a word", got, long)
	}
	// A pull request title is not a commit subject, so the bound that produced
	// "regist" is not the bound used here.
	if len(cut) <= maxCommitSubjectBytes {
		t.Errorf("pullRequestTitle() carried %d bytes, want more than a commit subject's %d", len(cut), maxCommitSubjectBytes)
	}
	// A single word longer than the bound has no boundary to cut at, and is cut
	// on a rune boundary rather than left whole or left invalid.
	oneWord := pullRequestTitle(beads.WorkItem{Title: strings.Repeat("é", 200)}, Outcome{WorkItemID: "yoyodyne-ifd.222"})
	if !utf8.ValidString(oneWord) || len(oneWord) > maxPullRequestTitleBytes+len("yoyodyne-ifd.222 ") {
		t.Errorf("pullRequestTitle() = %q, want a valid title inside the bound", oneWord)
	}
}

// The shape yoyodyne-ifd.321 was admitted on, end to end, replaying the
// yoyodyne-ifd.121.5 handoff that yoyodyne-ifd.387 was admitted on: publishing
// commits each attempt, so the second review judges a branch that already
// carries the first one — a reduction that takes three thousand lines out of a
// README. The patch it is handed is the branch's whole diff against the run's
// recorded base, so the reduction is in front of it beside the repair, and the
// evidence and the record both name the base and the tip it was judged between.
func TestPipelineTellsAReviewerWhatThePatchOfAContinuedRunSpans(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	var readme strings.Builder
	for i := 0; i < 3500; i++ {
		fmt.Fprintf(&readme, "line %d of the README before the reduction\n", i)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte(readme.String()), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", "README.md")
	runPipelineGit(t, repository, "commit", "-m", "the README before the reduction")
	runPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	reduced := strings.Join(strings.SplitAfter(readme.String(), "\n")[:400], "")

	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		attempts++
		if attempts == 1 {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "README.md"), []byte(reduced), 0o600)
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "repair.md"), []byte("the repair\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.RepairAttempts != 1 {
		t.Fatalf("outcome = %#v, want one repair round that was then approved", outcome)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 {
		t.Fatalf("reviews = %d, want the first verdict and the repair's", len(reviews))
	}
	if outcome.BaseCommit == "" {
		t.Fatalf("outcome = %#v, want the base the change is measured against", outcome)
	}
	repair := reviews[1].Prompt
	for _, want := range []string{
		"## What this patch covers",
		"This change is measured against base commit " + outcome.BaseCommit,
		"read at tip commit ",
		"### Commits already made for this change, oldest first",
		"No committed work of this change is missing from it",
		// The reduction is a commit by now and is in the patch all the same,
		// whole, beside the repair that followed it.
		"diff --git a/README.md b/README.md",
		"-line 3499 of the README before the reduction",
		"+the repair",
		// And the listing names both files as work already on the branch — the
		// repair too, because publishing commits each attempt before its review
		// — with the README at the size it was reduced to.
		"## Files in this change",
		fmt.Sprintf("- M README.md (%d bytes) — already committed on this branch", len(reduced)),
		"- A repair.md (11 bytes) — already committed on this branch",
	} {
		if !strings.Contains(repair, want) {
			t.Errorf("the continued run's review evidence is missing %q:\n%s", want, repair)
		}
	}
	if deleted := strings.Count(repair, "\n-line "); deleted != 3100 {
		t.Errorf("the repair review was shown %d removed README lines, want the whole 3100-line reduction", deleted)
	}
	if strings.Contains(repair, "This patch is truncated") {
		t.Errorf("a reduction inside the bounds was reported as truncated:\n%s", repair)
	}

	// The record names what the approving review was judged against: the run's
	// recorded base, and the tip carrying the reduction the first attempt
	// published. What the reviewer saw is then two commits in the run state and
	// on the item, rather than a byte count to reconstruct from.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReviewBaseCommit != outcome.BaseCommit || state.ReviewHeadCommit == "" || state.ReviewHeadCommit == outcome.BaseCommit {
		t.Errorf("review span = %s..%s, want the recorded base %s to a tip above it", state.ReviewBaseCommit, state.ReviewHeadCommit, outcome.BaseCommit)
	}
	if outcome.ReviewBaseCommit != state.ReviewBaseCommit || outcome.ReviewHeadCommit != state.ReviewHeadCommit {
		t.Errorf("outcome review span = %s..%s, want the record's %s..%s", outcome.ReviewBaseCommit, outcome.ReviewHeadCommit, state.ReviewBaseCommit, state.ReviewHeadCommit)
	}
	if want := "Reviewed against: base " + state.ReviewBaseCommit + ", tip " + state.ReviewHeadCommit; !strings.Contains(tracker.Notes, want) {
		t.Errorf("item notes are missing %q:\n%s", want, tracker.Notes)
	}
	if !strings.Contains(repair, "read at tip commit "+state.ReviewHeadCommit) {
		t.Errorf("the evidence names a different tip from the record's %s:\n%s", state.ReviewHeadCommit, repair)
	}
}

// A repository with no remote is the degradation the design requires: the run
// behaves exactly as it did before publishing existed, and says why.
func TestPipelineWithoutARemoteRunsExactlyAsItDidBefore(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want the ordinary local integration", outcome)
	}
	if outcome.PullRequest != nil || len(forge.Opened) != 0 {
		t.Fatalf("a repository with no remote published anyway: %#v", outcome.PullRequest)
	}
	if !strings.Contains(outcome.PublishSkipped, "no \"origin\" remote") {
		t.Fatalf("publish skipped = %q, want it to name the missing remote", outcome.PublishSkipped)
	}
}

// remoteAnswers answers only the two questions resolvePublishing asks about
// remotes, which is what lets a fork arrangement be decided without a
// repository that has one.
type remoteAnswers struct {
	WorktreeManager
	remote     bool
	pushRemote bool
}

func (r remoteAnswers) RemoteConfigured(context.Context) (bool, error) { return r.remote, nil }

func (r remoteAnswers) PushRemoteConfigured(context.Context) (bool, error) {
	return r.pushRemote, nil
}

// A contributor publishing from a fork needs two remotes, and the one that is
// missing is the one that has to be named: a run that reported the project's
// remote when the fork is what is absent would send them to look at the remote
// that is already there.
func TestPipelinePublishingNamesWhicheverRemoteIsMissing(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		answers remoteAnswers
		want    string
	}{
		"no publishing remote": {remoteAnswers{remote: false, pushRemote: true}, `no "origin" remote`},
		"no fork":              {remoteAnswers{remote: true, pushRemote: false}, `no "fork" remote to push run branches to`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pipeline := Pipeline{Publisher: &orchestratortest.Forge{}, Worktrees: expectation.answers}
			pipeline.Config.Approvals.Publishing = domain.ApprovalAutomatic
			pipeline.Config.Execution.Remote = "origin"
			pipeline.Config.Execution.PushRemote = "fork"

			publishes, skipped, err := pipeline.resolvePublishing(context.Background())
			if err != nil {
				t.Fatalf("resolvePublishing() error = %v", err)
			}
			if publishes {
				t.Fatal("resolvePublishing() = true with a remote the repository does not have")
			}
			if !strings.Contains(skipped, expectation.want) {
				t.Errorf("skipped = %q, want it to name %q", skipped, expectation.want)
			}
		})
	}
}

// A project that asked to publish and cannot must fail before it claims
// anything, rather than quietly producing work nobody sees.
func TestPipelineRefusesPublishingItCannotPerform(t *testing.T) {
	t.Parallel()

	for name, forge := range map[string]*orchestratortest.Forge{
		"no forge CLI":          {ReportedAvailability: publish.Availability{}},
		"unauthenticated forge": {ReportedAvailability: publish.Availability{Installed: true}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repository, _ := publishedRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
			pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
			forge.ReportAvailability = true

			if _, err := pipeline.Run(context.Background(), "yoyodyne-task"); err == nil {
				t.Fatal("Run() error = nil, want a refusal before the item is claimed")
			}
			if tracker.Claimed {
				t.Error("the work item was claimed by a run that could not publish")
			}
		})
	}
}

// A promotion that could not be published is an outstanding publication, not a
// failed run: the local target branch is the authoritative one and it moved.
func TestPipelineReportsAnOutstandingPublicationWithoutFailingThePromotion(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, StateErr: errors.New("the forge is unreachable")}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want a succeeded, integrated, closed run", outcome)
	}
	if !strings.Contains(outcome.PublishFailure, "the forge is unreachable") {
		t.Fatalf("publish failure = %q", outcome.PublishFailure)
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PublishFailure == "" {
		t.Fatal("the outstanding publication was not recorded durably")
	}
	if !strings.Contains(tracker.Notes, "Publication outstanding") {
		t.Errorf("tracker notes do not report the outstanding publication:\n%s", tracker.Notes)
	}
}

// A target branch that refuses direct pushes is the ordinary reason to open
// pull requests at all, and it is what the harness could not finish a promotion
// into while it published by pushing that branch. Merging through the forge is
// what makes the protected case work.
func TestPipelineMergesIntoATargetThatRefusesDirectPushes(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	protectBranch(t, remote, "main")
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PublishFailure != "" {
		t.Fatalf("publish failure = %q, want a protected branch merged into cleanly", outcome.PublishFailure)
	}
	if len(forge.Merges) != 1 || !outcome.PullRequest.Merged {
		t.Fatalf("forge merges = %d, pull request = %#v; want the request merged through the forge", len(forge.Merges), outcome.PullRequest)
	}
	assertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	if published := publishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
}

// A forge that declines to merge is answering with the repository's own rules.
// Reporting that as a generic failure would leave an operator with a promotion
// that did not publish and no way to learn which requirement was unmet.
func TestPipelineReportsAForgeRefusalAsTheUnmetRequirement(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, MergeErr: publish.MergeRefused{
		Number: 1,
		Method: publish.MergeRebase,
		Status: "BLOCKED",
		Reason: `Required status check "build" is expected`,
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want the promotion to stand", outcome)
	}
	for _, want := range []string{"refused to merge pull request 1", "protection rules", `Required status check "build" is expected`} {
		if !strings.Contains(outcome.PublishFailure, want) {
			t.Errorf("publish failure %q does not name %q", outcome.PublishFailure, want)
		}
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PublishFailure != outcome.PublishFailure {
		t.Errorf("durable publish failure = %q, want the reported one", state.PublishFailure)
	}
	// Nothing merged, so the remote target must not have moved and the branch
	// carrying the work has to survive for whoever satisfies the requirement.
	if head := publishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
	if published := publishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q", published, outcome.PullRequest.HeadCommit)
	}
}

// A human push to the target branch during a run is the ordinary way the remote
// moves underneath one, and it is a cost rather than a wedge. The remote target
// is settled before the promotion, so the local target takes the push on, the
// change is replayed onto it, and everything the promotion depends on is
// re-earned: the checks run again and a second independent review judges the
// replayed change. What must not happen is what did happen before: a promotion
// made onto a base the remote had left, an item closed as integrated, and a
// local target no fast-forward could ever reconcile.
func TestPipelineReplaysOntoARemoteTargetSomebodyPushedTo(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	// Someone else's work lands on the remote target after this run published its
	// branch, which is the window a merge the harness does not perform opens.
	forge.OnEnsure = func() { driftRemoteTarget(t, remote, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	cut := publishedCommit(t, repository, "main")

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want the replayed change promoted and the item closed", outcome)
	}
	// The promotion was made from where the target actually went rather than from
	// the base the run was cut at, which is what a replay is for.
	if outcome.IntegrationRetries != 1 {
		t.Fatalf("integration retries = %d, want the drift to have re-prepared the change once", outcome.IntegrationRetries)
	}
	if outcome.BaseCommit == cut || outcome.Integration.PreviousTargetCommit != outcome.BaseCommit {
		t.Fatalf("promotion base = %q (run was cut at %q), want the commit the human push left", outcome.BaseCommit, cut)
	}
	// The change was re-judged on its new ground: the approval that authorized the
	// first attempt described a diff on a base the target had left.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 2 {
		t.Errorf("reviews = %d, want the replayed change reviewed again", reviews)
	}
	// The whole point of the replay: both branches end up carrying both changes,
	// and the local target is not left somewhere the remote cannot be reached from.
	if len(forge.Merges) != 1 {
		t.Fatalf("forge merges = %#v, want the replayed promotion merged", forge.Merges)
	}
	assertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	remoteTarget := publishedCommit(t, remote, "main")
	if local := publishedCommit(t, repository, "main"); local != remoteTarget {
		t.Errorf("local main = %q, want the merge commit %q the forge left on the remote", local, remoteTarget)
	}
	for _, name := range []string{"feature.txt", "elsewhere.txt"} {
		if _, err := os.Stat(filepath.Join(repository, name)); err != nil {
			t.Errorf("main is missing %s after the replay: %v", name, err)
		}
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PublishFailure != "" {
		t.Errorf("durable publish failure = %q, want a publication that finished", state.PublishFailure)
	}
	if state.IntegrationRetries != 1 {
		t.Errorf("durable integration retries = %d, want 1", state.IntegrationRetries)
	}
}

// The check before the promotion narrows the window the remote can move in; it
// does not close it. A remote that moves between that check and the merge
// request is found by the check publishIntegration makes, and by then the local
// target already carries the promotion. That used to be recorded as an
// outstanding publication and the item closed as integrated, which left a
// divergence nothing reconciled and nobody owned. It now stops the run instead —
// which is still before the item closes — and says on the item that the work is
// on the local branch and only the two branches and the publication need
// settling.
func TestPipelineStopsWhenTheRemoteTargetDivergesAfterThePromotion(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	// The one instant nothing else in this harness can express: the world moves
	// after the local promotion has happened and before the forge is asked.
	pipeline.Worktrees = remoteMovingWorktrees{
		WorktreeManager: pipeline.Worktrees,
		afterIntegrate:  func() { driftRemoteTarget(t, remote, "main") },
	}

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil || !strings.Contains(err.Error(), "cannot be brought onto origin afterwards") {
		t.Fatalf("Run() error = %v, want the divergence to stop the run", err)
	}
	// The promotion happened and is not taken back, but the item is not closed on
	// it: that closure is the receipt this whole outcome used to hide behind.
	if outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the local promotion recorded", outcome)
	}
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("work item closed = %t (tracker %t), want the item left open over the divergence", outcome.WorkItemClosed, tracker.Closed)
	}
	if !outcome.Blocked || !tracker.Blocked {
		t.Fatalf("blocked = %t (tracker %t), want the divergence on the item", outcome.Blocked, tracker.Blocked)
	}
	// A divergence found after the promotion is the same held catch-up to the
	// brake as one found before it: a stop the harness made, counted toward
	// nothing.
	if outcome.DivergedTarget == nil || outcome.DivergedTarget.Held == "" {
		t.Fatalf("diverged target = %#v, want the held catch-up on the outcome", outcome.DivergedTarget)
	}
	if !environmentalStop(outcome) {
		t.Fatal("the brake would count this stop, want a diverged target classified environmental where the brake counts stops")
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("the harness asked for a merge into a drifted target: %#v", forge.Merges)
	}
	if local := publishedCommit(t, repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want the promoted commit %q left where it is", local, outcome.Integration.TargetCommit)
	}
	// The blocker is what owns the divergence: where the work is, where each
	// branch stands, that neither was chosen over the other, and where the steps
	// for choosing are written down — a blocker that only said a person was
	// needed is what left the last divergence standing.
	for _, want := range []string{
		"promoted onto the local target branch",
		"The item is deliberately left open rather than closed as integrated",
		"which history is right is a decision for a person",
		"onto local main at " + outcome.Integration.TargetCommit,
		"origin main: " + publishedCommit(t, remote, "main"),
		"Unwedging a target branch that diverged from the forge",
	} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Errorf("blocker does not name %q:\n%s", want, tracker.BlockReason)
		}
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Both halves are durable: the promotion the run made, and the outstanding
	// publication that names the drift it was refused for.
	if state.Integration == nil || state.Blocker == "" {
		t.Fatalf("state = %#v, want the promotion and the blocker both recorded", state)
	}
	if !strings.Contains(state.PublishFailure, "moved away from the content the promotion was written against") {
		t.Errorf("durable publish failure = %q, want the drift named", state.PublishFailure)
	}
	// And the moment the merge stopped being expected, which is the half of this
	// nothing held before: the outstanding publication was always readable by
	// whoever went and looked, and a state that only exists at read time is one
	// no channel can announce. Four promotions refused this way waited hours in
	// silence for exactly that reason.
	if state.MergeDrop == nil {
		t.Fatalf("state = %#v, want the moment the merge was refused recorded", state)
	}
	if state.MergeDrop.At.IsZero() || state.MergeDrop.Reason != state.PublishFailure {
		t.Errorf("merge drop = %#v, want the moment and the publication's own reason", state.MergeDrop)
	}
}

// remoteMovingWorktrees is the real worktree manager with one seam: the callback
// runs the moment a promotion returns, which is inside the window between the
// remote target check the run makes before it promotes and the one it makes
// before it asks the forge to merge. That window is a race against another
// machine, so nothing a test can drive from outside reaches it.
type remoteMovingWorktrees struct {
	WorktreeManager
	afterIntegrate func()
}

func (w remoteMovingWorktrees) Integrate(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Integration, error) {
	integration, err := w.WorktreeManager.Integrate(ctx, worktree, message)
	if err == nil && w.afterIntegrate != nil {
		w.afterIntegrate()
	}
	return integration, err
}

// The one path left on which a moved remote target still closes an item as
// integrated, and the reason it is allowed to: the remote swept the promotion in
// and carried somebody else's work above it, so the work this item records really
// is on both branches and only the merge request did not happen. That is an
// ordinary outstanding publication rather than the wedge — and what separates
// the two is exactly that the remote contains the promoted commit, which is
// asserted here rather than assumed from the catch-up having succeeded.
func TestPipelineClosesTheItemWhenTheRemoteSweptThePromotionIn(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	// The same window as the divergence above — after the promotion, before the
	// forge is asked — with the remote moving somewhere the local branch can still
	// be brought onto.
	pipeline.Worktrees = remoteMovingWorktrees{
		WorktreeManager: pipeline.Worktrees,
		afterIntegrate:  func() { sweepRemoteTarget(t, repository, remote, "main") },
	}

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the promotion to stand", outcome)
	}
	if outcome.Blocked || tracker.Blocked {
		t.Fatalf("blocked = %t (tracker %t), want a reconcilable remote to leave the run alone", outcome.Blocked, tracker.Blocked)
	}
	if !outcome.WorkItemClosed || !tracker.Closed {
		t.Fatalf("work item closed = %t (tracker %t), want the item closed on work that is on both branches", outcome.WorkItemClosed, tracker.Closed)
	}
	// The whole safety of closing this item: the remote target really does carry
	// the commit this run promoted. A branch that closed an item against a remote
	// without it is the defect this test exists to catch.
	remoteTarget := publishedCommit(t, remote, "main")
	if remoteTarget == "" {
		t.Fatal("remote main does not exist")
	}
	contained := exec.Command("git", "-C", remote, "merge-base", "--is-ancestor", outcome.Integration.TargetCommit, remoteTarget)
	if err := contained.Run(); err != nil {
		t.Errorf("remote main at %q does not contain the promoted commit %q, yet the item was closed as integrated",
			remoteTarget, outcome.Integration.TargetCommit)
	}
	// The local target was brought onto it, so the two branches are together and
	// nothing is left for a person to reconcile.
	if local := publishedCommit(t, repository, "main"); local != remoteTarget {
		t.Errorf("local main = %q, want it fast-forwarded onto the remote %q", local, remoteTarget)
	}
	if outcome.Catchup == nil || !outcome.Catchup.Advanced || outcome.Catchup.RemoteCommit != remoteTarget {
		t.Fatalf("catch-up = %#v, want main advanced onto %q", outcome.Catchup, remoteTarget)
	}
	if outcome.Catchup.Held != "" {
		t.Errorf("catch-up was held: %s", outcome.Catchup.Held)
	}
	// What is unfinished is the publication and only the publication: the drift is
	// named as outstanding, and the merge was never asked for.
	if len(forge.Merges) != 0 {
		t.Fatalf("the harness asked for a merge into a moved target: %#v", forge.Merges)
	}
	if !strings.Contains(outcome.PublishFailure, "moved away from the content the promotion was written against") {
		t.Errorf("publish failure = %q, want the drift named", outcome.PublishFailure)
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PublishFailure != outcome.PublishFailure {
		t.Errorf("durable publish failure = %q, want the reported one %q", state.PublishFailure, outcome.PublishFailure)
	}
	if !strings.Contains(tracker.Notes, "Publication outstanding") {
		t.Errorf("tracker notes do not report the outstanding publication:\n%s", tracker.Notes)
	}
}

// A remote target the local one cannot be brought onto is the divergence only a
// person settles, and it stops the run before anything is promoted. Promoting
// anyway is what used to close an item as integrated against a state nothing
// resolves: the local target would carry a promotion the remote does not have,
// no fast-forward could reconcile the two, and the item would say the work
// landed. Nothing is forced here and nothing is reset — both positions are named
// on the item, and the change is preserved for whoever settles them.
func TestPipelineStopsBeforePromotingIntoADivergedRemoteTarget(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	// A rewritten remote history, which is the movement no fast-forward answers:
	// the remote target no longer contains the commit this run was cut from.
	forge.OnEnsure = func() { rewriteRemoteTarget(t, remote, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	base := publishedCommit(t, repository, "main")

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil || !strings.Contains(err.Error(), "cannot be brought onto origin before promoting") {
		t.Fatalf("Run() error = %v, want the divergence to stop the run", err)
	}
	if outcome.Integration != nil || outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("outcome = %#v, closed = %t, want nothing promoted and the item left open", outcome, tracker.Closed)
	}
	// Nothing was promoted, so nothing was ever asked of the forge either.
	if len(forge.Merges) != 0 {
		t.Fatalf("the harness asked for a merge into a diverged target: %#v", forge.Merges)
	}
	if local := publishedCommit(t, repository, "main"); local != base {
		t.Errorf("local main = %q, want the untouched base %q", local, base)
	}
	// The item carries the divergence rather than a closure: both branch
	// positions, and the statement that neither was chosen over the other.
	if !outcome.Blocked || !tracker.Blocked {
		t.Fatalf("blocked = %t (tracker %t), want the stoppage on the item", outcome.Blocked, tracker.Blocked)
	}
	// And the outcome carries the held catch-up as what stopped the run, which
	// is what the brake reads to count the stop toward nothing: a refusal the
	// harness will not catch up from is the environment's, not a verdict.
	if outcome.DivergedTarget == nil || outcome.DivergedTarget.Held == "" || outcome.DivergedTarget.RemoteCommit != publishedCommit(t, remote, "main") {
		t.Fatalf("diverged target = %#v, want the held catch-up on the outcome", outcome.DivergedTarget)
	}
	if !environmentalStop(outcome) {
		t.Fatal("the brake would count this refusal, want a diverged target classified environmental where the brake counts stops")
	}
	for _, want := range []string{
		"target branch and the one on the remote have diverged",
		"which history is right is a decision for a person",
		"Local main: " + base,
		"origin main: " + publishedCommit(t, remote, "main"),
		"Unwedging a target branch that diverged from the forge",
	} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Errorf("blocker does not name %q:\n%s", want, tracker.BlockReason)
		}
	}
	// The change is preserved for whoever settles the branches, and the run's own
	// record carries the same words the item does.
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
		t.Errorf("worktree was not preserved: %v", err)
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Integration != nil || state.Blocker == "" {
		t.Fatalf("state = %#v, want a blocked run with nothing integrated", state)
	}
	// The change is approved and nothing was promoted, so the stop is one the
	// harness resumes once the branches are settled rather than one that costs a
	// re-run (yoyodyne-ifd.429.9): the record says so, and so does the blocker.
	if state.IntegrationStop == nil || state.IntegrationStop.Cause != runstate.CauseDivergedTarget || !state.ResumableIntegration() {
		t.Fatalf("integration stop = %#v, resumable = %t; want a resumable diverged-target stop", state.IntegrationStop, state.ResumableIntegration())
	}
	if outcome.IntegrationStop == nil || outcome.IntegrationStop.Cause != runstate.CauseDivergedTarget {
		t.Fatalf("outcome integration stop = %#v, want the diverged target on the outcome too", outcome.IntegrationStop)
	}
	if state.StopClass != runstate.CauseDivergedTarget.StopClass() || outcome.StopClass != state.StopClass {
		t.Errorf("saved cause = %q, outcome cause = %q; want the diverged target", state.StopClass, outcome.StopClass)
	}
	if !strings.Contains(tracker.BlockReason, "`yoyo triage resume "+pipelineRunID+"`") {
		t.Errorf("blocker does not name the resume:\n%s", tracker.BlockReason)
	}
}

// A forge that replays what it merges — which is what GitHub's rebase and
// squash methods do — never puts the reviewed commit on the base at all. The
// harness asks for a merge commit precisely so that cannot happen, and reports
// it rather than accepting a rewritten copy if it does: the remote would then
// carry work no local branch has, and which history is right is a person's
// decision.
func TestPipelineReportsAMergeThatRewroteThePromotedCommit(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, ReplayMerge: true}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the promotion to stand", outcome)
	}
	// The rewritten commit carries the promotion's content, so content alone
	// would have accepted it. What is checked is that the promoted commit itself
	// is there.
	for _, want := range []string{"does not contain the promoted commit", outcome.Integration.TargetCommit} {
		if !strings.Contains(outcome.PublishFailure, want) {
			t.Errorf("publish failure %q does not name %q", outcome.PublishFailure, want)
		}
	}
	if outcome.PullRequest.MergeCommit != "" {
		t.Errorf("recorded merge commit = %q, want none for a merge that did not carry the promotion", outcome.PullRequest.MergeCommit)
	}
	// The branch is the evidence for whoever reconciles the two, so it stays.
	if published := publishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q left in place", published, outcome.PullRequest.HeadCommit)
	}
}

// The remote target a published run leaves behind is the forge's merge commit,
// which this repository does not have and never will. The run after it must
// still publish: a drift check that could not tell that state from someone
// else's work would block every publication after the first, permanently.
func TestPipelinePublishesTheRunAfterAForgeMerge(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	write := func(name, content string) func(backend.RunRequest) error {
		return func(request backend.RunRequest) error {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, name), []byte(content), 0o600)
		}
	}
	first := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-first", Title: "First", Status: "open"}}
	firstForge := &orchestratortest.Forge{Remote: remote}
	firstPipeline, _ := newPublishingPipeline(t, repository, first,
		orchestratortest.RoleBackend(write("first.txt", "first\n"), approveVerdict), firstForge, []string{"exit 0"})
	firstOutcome, err := firstPipeline.Run(context.Background(), first.Item.ID)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if firstOutcome.PublishFailure != "" {
		t.Fatalf("first publish failure = %q", firstOutcome.PublishFailure)
	}

	second := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-second", Title: "Second", Status: "open"}}
	secondForge := &orchestratortest.Forge{Remote: remote}
	secondPipeline, store := newPublishingPipeline(t, repository, second,
		orchestratortest.RoleBackend(write("second.txt", "second\n"), approveVerdict), secondForge, []string{"exit 0"})
	secondRunID := "run-fedcba9876543210fedcba9876543210"
	secondPipeline.NewRunID = func() (string, error) { return secondRunID, nil }

	outcome, err := secondPipeline.Run(context.Background(), second.Item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if outcome.PublishFailure != "" {
		t.Fatalf("second publish failure = %q, want the run after a forge merge to publish too", outcome.PublishFailure)
	}
	if len(secondForge.Merges) != 1 || !outcome.PullRequest.Merged {
		t.Fatalf("forge merges = %d, pull request = %#v; want the second run merged as well", len(secondForge.Merges), outcome.PullRequest)
	}
	// The remote is now two forge merge commits ahead of the local branch and
	// still carries exactly its content, which is the relationship the harness
	// maintains rather than an equality it cannot have.
	assertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	if published := publishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	state, err := store.Load(secondRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PullRequest == nil || state.PullRequest.MergeCommit == "" {
		t.Fatalf("durable pull request = %#v, want the forge's merge commit recorded", state.PullRequest)
	}
}

// What a forge merges is the pull request's head, so a promotion that
// integrated a commit the published branch never received must not be merged:
// the remote would get a change the authoritative branch does not have. A check
// that writes into the worktree is how that happens.
func TestPipelineRefusesToMergeAPullRequestThatIsNotWhatIntegrated(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"printf 'left behind\n' > residue.txt"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.PullRequest.HeadCommit == outcome.Integration.SourceCommit {
		t.Fatalf("outcome = %#v, want a promotion of a commit the branch was never published at", outcome)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("the harness merged a request that is not what integrated: %#v", forge.Merges)
	}
	if !strings.Contains(outcome.PublishFailure, "is not what would merge") {
		t.Fatalf("publish failure = %q, want the mismatch reported", outcome.PublishFailure)
	}
	if head := publishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
}

// assertRemoteCarriesPromotion states the relationship a forge merge produces,
// which is the one the harness checks: the remote target is not the promoted
// commit — no merge method leaves it there — but it contains that commit
// unrewritten and carries exactly its content.
func assertRemoteCarriesPromotion(t *testing.T, repository, remote, branch, promoted string) {
	t.Helper()
	target := publishedCommit(t, remote, branch)
	if target == "" {
		t.Fatalf("remote %s does not exist", branch)
	}
	// The promoted commit and the remote target both live in the remote here, so
	// it can answer both questions about them.
	contained := exec.Command("git", "-C", remote, "merge-base", "--is-ancestor", promoted, target)
	if err := contained.Run(); err != nil {
		t.Errorf("remote %s at %q does not contain the promoted commit %q", branch, target, promoted)
	}
	remoteTree := strings.TrimSpace(gitOutput(t, remote, "rev-parse", target+"^{tree}"))
	localTree := strings.TrimSpace(gitOutput(t, repository, "rev-parse", promoted+"^{tree}"))
	if remoteTree != localTree {
		t.Errorf("remote %s carries tree %q, want the promoted commit's tree %q", branch, remoteTree, localTree)
	}
}

// protectBranch makes a bare remote refuse direct pushes to one branch, the way
// a repository that requires a pull request before anything reaches its target
// does. The refusal is proven rather than assumed: a hook that silently allowed
// the push would make the test resting on it meaningless.
func protectBranch(t *testing.T, remote, branch string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nwhile read -r _ _ ref; do\n\tif [ \"$ref\" = \"refs/heads/%s\" ]; then\n\t\techo \"branch %s requires a pull request\" >&2\n\t\texit 1\n\tfi\ndone\nexit 0\n", branch, branch)
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	clone := filepath.Join(t.TempDir(), "protected")
	runPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	runPipelineGit(t, clone, "config", "user.name", "Someone Else")
	runPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	disablePipelineMaintenance(t, clone)
	runPipelineGit(t, clone, "commit", "--allow-empty", "-m", "a direct push")
	if err := exec.Command("git", "-C", clone, "push", "origin", "HEAD:refs/heads/"+branch).Run(); err == nil {
		t.Fatalf("the remote accepted a direct push to %s", branch)
	}
}

// driftRemoteTarget moves the remote target branch onto work this repository
// has never seen, which is what someone else merging looks like from inside a
// run.
func driftRemoteTarget(t *testing.T, remote, branch string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "elsewhere")
	runPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	runPipelineGit(t, clone, "config", "user.name", "Someone Else")
	runPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	disablePipelineMaintenance(t, clone)
	// Real content, not an empty commit: what makes this drift is that the remote
	// target no longer carries what the promotion was written against.
	if err := os.WriteFile(filepath.Join(clone, "elsewhere.txt"), []byte("someone else's work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, clone, "add", ".")
	runPipelineGit(t, clone, "commit", "-m", "someone else's work")
	runPipelineGit(t, clone, "push", "origin", "HEAD:refs/heads/"+branch)
}

// rewriteRemoteTarget replaces the remote target branch with a history that does
// not contain the one this repository has. It is the movement driftRemoteTarget
// is not: a fast-forward answers a target that grew, and nothing answers a
// target somebody rewrote.
func rewriteRemoteTarget(t *testing.T, remote, branch string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "rewritten")
	runPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	runPipelineGit(t, clone, "config", "user.name", "Someone Else")
	runPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	disablePipelineMaintenance(t, clone)
	runPipelineGit(t, clone, "checkout", "--orphan", "rewritten")
	runPipelineGit(t, clone, "rm", "-r", "-f", "-q", ".")
	if err := os.WriteFile(filepath.Join(clone, "rewritten.txt"), []byte("a history of its own\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, clone, "add", ".")
	runPipelineGit(t, clone, "commit", "-m", "a rewritten history")
	runPipelineGit(t, clone, "push", "--force", "origin", "HEAD:refs/heads/"+branch)
}

// sweepRemoteTarget is the third thing a remote target can do to a promotion
// that has already happened, and the only one that leaves the two branches
// reconcilable: it takes the promotion in and carries somebody else's work above
// it. That is what another machine merging this very pull request looks like, or
// a person pushing the local target on and then committing further — the local
// branch's tip goes to the remote, and a commit lands on top of it.
func sweepRemoteTarget(t *testing.T, repository, remote, branch string) {
	t.Helper()
	// The promotion itself reaches the remote first, which is what makes this a
	// sweep rather than the drift the local branch cannot be brought onto.
	runPipelineGit(t, repository, "push", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	clone := filepath.Join(t.TempDir(), "swept")
	runPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	runPipelineGit(t, clone, "config", "user.name", "Someone Else")
	runPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	disablePipelineMaintenance(t, clone)
	if err := os.WriteFile(filepath.Join(clone, "elsewhere.txt"), []byte("someone else's work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, clone, "add", ".")
	runPipelineGit(t, clone, "commit", "-m", "someone else's work above the promotion")
	runPipelineGit(t, clone, "push", "origin", "HEAD:refs/heads/"+branch)
}

// newPublishingPipeline is the automatic pipeline with publishing turned on.
func newPublishingPipeline(t *testing.T, repository string, tracker WorkTracker, provider backend.Backend, forge PullRequests, commands []string) (Pipeline, *runstate.Store) {
	t.Helper()
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, commands)
	return publishing(pipeline, forge), store
}

// publishing turns a pipeline into one that publishes, the way automatic turns
// one into a pipeline that reviews and integrates.
func publishing(pipeline Pipeline, forge PullRequests) Pipeline {
	pipeline.Config.Approvals.Publishing = domain.ApprovalAutomatic
	pipeline.Publisher = forge
	return pipeline
}

// publishedRepository is a pipeline repository with a bare remote that already
// carries its target branch.
func publishedRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := pipelineRepository(t)
	return repository, addBareRemote(t, repository)
}

// addBareRemote gives an existing repository the remote publishing pushes to,
// carrying its target branch.
func addBareRemote(t *testing.T, repository string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runPipelineGit(t, remote, "init", "--bare", "-b", "main")
	disablePipelineMaintenance(t, remote)
	runPipelineGit(t, repository, "remote", "add", "origin", remote)
	runPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	return remote
}

func publishedCommit(t *testing.T, repository, branch string) string {
	t.Helper()
	return strings.TrimSpace(gitOutput(t, repository, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch))
}

// Publishing and automatic integration are separate opt-ins, so a project can
// have the harness open pull requests and still merge them itself. The run then
// stops where a human-integration run always stopped: checks pass, nothing is
// promoted, and the worktree is preserved — with the work now published.
func TestPipelinePublishesWithoutIntegratingWhenAHumanMerges(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration != nil || outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want nothing promoted under a human policy", outcome)
	}
	if outcome.PullRequest == nil || outcome.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want it opened and left for the human", outcome.PullRequest)
	}
	// Nothing merges it, so the remote target must not have moved and the run
	// branch must still be there for whoever does.
	if head := publishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
	if published := publishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q", published, outcome.PullRequest.HeadCommit)
	}
	if outcome.WorktreeRemoved || outcome.BranchRemoved {
		t.Errorf("outcome = %#v, want the artifacts preserved for the human", outcome)
	}
}

// A forge's own record of a pull request can lag the merge it just performed.
// Asking once would report a successful publication as outstanding and leave
// the merged branch on the remote, so the confirmation waits the way the thing
// it observes behaves.
func TestPipelineWaitsForTheForgeToReportItsOwnMerge(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, OpenReplies: 2}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	waits := 0
	pipeline.Sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PublishFailure != "" {
		t.Fatalf("publish failure = %q, want a publication that waited and succeeded", outcome.PublishFailure)
	}
	if !outcome.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want it merged", outcome.PullRequest)
	}
	if waits != 2 || forge.StateCalls != 3 {
		t.Errorf("waits = %d and forge queries = %d, want two waits and three queries", waits, forge.StateCalls)
	}
	// A confirmation that never arrived would have skipped the branch removal.
	if published := publishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
}

// A forge that keeps reporting the request open past the bounded wait is an
// outstanding publication, not a run that waits forever.
func TestPipelineStopsWaitingForAMergeThatNeverArrives(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, OpenReplies: 99}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline.Sleep = func(context.Context, time.Duration) error { return nil }

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the promotion to stand", outcome)
	}
	if !strings.Contains(outcome.PublishFailure, "is still open") {
		t.Fatalf("publish failure = %q, want the unmerged request reported", outcome.PublishFailure)
	}
	if forge.StateCalls != len(mergeConfirmationDelays)+1 {
		t.Errorf("forge queries = %d, want %d", forge.StateCalls, len(mergeConfirmationDelays)+1)
	}
	// Nothing confirmed the merge, so the branch a person may still need is left
	// exactly where it was published.
	if published := publishedCommit(t, remote, outcome.Branch); published == "" {
		t.Error("an unconfirmed merge removed the published branch anyway")
	}
}

// The case the harness exists to handle: a base branch whose required checks
// have not finished. The merge is queued with the forge rather than demanded, so
// the run finishes and reports the request as queued instead of waiting out a
// confirmation that arrives minutes later — and leaves the published branch on
// the remote, because that branch is what the forge still has to merge.
func TestPipelineQueuesTheMergeAndFinishesWithoutWaitingForIt(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, QueueMerge: true}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	waits := 0
	pipeline.Sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want a succeeded, integrated run", outcome)
	}
	// The promotion is made, but nothing has merged it anywhere but here. Closing
	// the item now would record it integrated against a publication the forge may
	// yet drop, so the closure waits for the forge's answer.
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("a queued merge closed the item as integrated before the forge merged it: reason = %q", tracker.CloseReason)
	}
	// A queued merge is not an outstanding publication: the forge accepted it.
	if outcome.PublishFailure != "" {
		t.Fatalf("publish failure = %q, want a queued merge to be a clean outcome", outcome.PublishFailure)
	}
	if !outcome.PullRequest.MergeQueued || outcome.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want the merge queued and not yet performed", outcome.PullRequest)
	}
	if outcome.PullRequest.MergeMethod != string(publish.MergeCommit) {
		t.Errorf("recorded merge method = %q, want the method the forge was asked for", outcome.PullRequest.MergeMethod)
	}
	// Nothing is waited for, because nothing can arrive while the run watches.
	if waits != 0 || forge.StateCalls != 0 {
		t.Errorf("waits = %d and forge queries = %d, want a run that finished without confirming", waits, forge.StateCalls)
	}
	// The forge has not merged yet, so the remote target has not moved and the
	// branch the queued merge will consume must still be there.
	if head := publishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
	if published := publishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q left for the queued merge", published, outcome.PullRequest.HeadCommit)
	}
	// The run is over, but it still owes the answer to that merge, so it stays
	// outstanding for reconciliation rather than being settled and forgotten.
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PullRequest == nil || !state.PullRequest.MergeQueued {
		t.Fatalf("durable pull request = %#v, want the queued merge recorded", state.PullRequest)
	}
	if !state.Outstanding() {
		t.Error("a run with a queued merge is not outstanding, so nothing would ever settle it")
	}
	for _, want := range []string{"Merge queued", "stays open until then"} {
		if !strings.Contains(tracker.Notes, want) {
			t.Errorf("tracker notes do not report %q:\n%s", want, tracker.Notes)
		}
	}
}

// A repository whose settings forbid queued merges and whose base branch
// requires checks cannot be published to at all. The operator has to be told
// which setting to change, rather than reading a refusal on every run.
func TestPipelineReportsARepositoryThatCannotQueueAMerge(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, MergeErr: publish.AutoMergeUnavailable{
		Number: 1,
		Status: "BLOCKED",
		Reason: "GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the promotion to stand", outcome)
	}
	for _, want := range []string{"cannot queue a merge", "Allow auto-merge", "protection rules"} {
		if !strings.Contains(outcome.PublishFailure, want) {
			t.Errorf("publish failure %q does not name %q", outcome.PublishFailure, want)
		}
	}
	if outcome.PullRequest.MergeQueued {
		t.Errorf("pull request = %#v, want no queued merge recorded for a forge that queued nothing", outcome.PullRequest)
	}
}

// The merge the forge queued lands minutes later, with no run watching.
// Reconciliation is what finds out, and it finishes the publication the run
// could not: the remote target is confirmed to carry the promotion, the forge's
// merge commit is recorded, and the branch the merge consumed is deleted.
func TestReconcileFinishesAQueuedMergeTheForgePerformed(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the queued merge settled as completed", results)
	}
	if !strings.Contains(results[0].Detail, "merged pull request 1") {
		t.Errorf("detail = %q, want the merge named", results[0].Detail)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.MergeDrop != nil {
		t.Errorf("merge drop = %#v recorded over a merge the forge performed", settled.MergeDrop)
	}
	if settled.PullRequest.MergeQueued || !settled.PullRequest.Merged {
		t.Fatalf("settled pull request = %#v, want a merge that is no longer queued", settled.PullRequest)
	}
	if settled.PublishFailure != "" {
		t.Fatalf("publish failure = %q, want a publication that finished", settled.PublishFailure)
	}
	remoteTarget := publishedCommit(t, fixture.remote, "main")
	if settled.PullRequest.MergeCommit != remoteTarget {
		t.Errorf("recorded merge commit = %q, want the remote target %q", settled.PullRequest.MergeCommit, remoteTarget)
	}
	assertRemoteCarriesPromotion(t, fixture.repository, fixture.remote, "main", outcome.Integration.TargetCommit)
	if published := publishedCommit(t, fixture.remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	if !strings.Contains(fixture.tracker.Record().Notes, "settled the merge this run left queued") {
		t.Errorf("tracker notes do not report the settled merge:\n%s", fixture.tracker.Record().Notes)
	}
	// The run left the closure to this answer, so this is where the item closes —
	// and the reason says what actually happened rather than describing a run
	// somebody interrupted.
	if !fixture.tracker.Record().Closed {
		t.Fatal("the confirmed merge did not close the item, so nothing ever will")
	}
	for _, want := range []string{"merged by the forge", "Reviewed and integrated"} {
		if !strings.Contains(fixture.tracker.Record().CloseReason, want) {
			t.Errorf("close reason %q does not name %q", fixture.tracker.Record().CloseReason, want)
		}
	}
	// Nothing is left owed, so a second sweep finds nothing at all.
	if again := fixture.reconcile(t); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// A merge the forge is still holding decides nothing. The run stays outstanding
// and the next sweep asks again, rather than being settled on an answer that has
// not arrived.
func TestReconcileLeavesAQueuedMergeThatIsStillWaiting(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionQueued || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run reported as queued", results)
	}
	held, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !held.PullRequest.MergeQueued || held.PublishFailure != "" {
		t.Fatalf("held pull request = %#v, publish failure = %q; want the queued merge untouched", held.PullRequest, held.PublishFailure)
	}
	// The branch the forge still has to merge must not be swept away underneath
	// it, and the remote target must not have moved.
	if published := publishedCommit(t, fixture.remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q kept for the queued merge", published, outcome.PullRequest.HeadCommit)
	}
	if head := publishedCommit(t, fixture.remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}

	// The forge merges it, and the very next sweep settles the run.
	fixture.forge.PerformQueuedMerge(t)
	if settled := fixture.reconcile(t); len(settled) != 1 || settled[0].Action != ActionCompleted {
		t.Fatalf("reconciliation after the merge = %#v, want it completed", settled)
	}
}

// A publication triage has already re-armed, dropped a second time, is not a
// transient cause to repeat past: the request has been through the forge's
// requirements twice and been dropped twice. So the sweep records it as an
// escalation, and it records it on the blocker the item is handed back with —
// which is the surface the configuration and conversation guides both promise
// says so in as many words, and the one a development manager reads before
// deciding anything about it.
func TestReconcileRecordsASecondDropAsAnEscalationRatherThanAReArm(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.run(t)

	// The state a carried-out re-arm leaves behind: the identical request
	// repeated, the forge holding a merge for it again, and the publication's one
	// re-arm spent on its own durable counter.
	rearmed, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if rearmed.PullRequest == nil || !rearmed.PullRequest.MergeQueued {
		t.Fatalf("run = %#v, want a queued merge to have been re-armed", rearmed.PullRequest)
	}
	rearmed.PullRequest.MergeRearms = 1
	if err := fixture.store.Save(rearmed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	fixture.forge.DropQueuedMerge()
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run settled on a blocker", results)
	}
	for _, want := range []string{
		// Still everything a first drop says, because it is still a dropped merge
		// somebody has to look at.
		"dropped the queued merge", "needs a person",
		// And what this one is beyond that.
		"second drop of this publication", "escalation rather than something to re-arm again",
	} {
		if !strings.Contains(results[0].Detail, want) {
			t.Errorf("detail %q does not name %q", results[0].Detail, want)
		}
	}
	// The blocker on the item is the surface the guides describe, and it is where
	// the development manager reads this rather than in a sweep's output.
	if !fixture.tracker.Record().Blocked {
		t.Fatal("a second drop left the item unblocked")
	}
	for _, want := range []string{"second drop of this publication", "escalation rather than something to re-arm again"} {
		if !strings.Contains(fixture.tracker.Record().BlockReason, want) {
			t.Fatalf("the blocker %q does not say %q", fixture.tracker.Record().BlockReason, want)
		}
	}
	// Nothing about the second drop closes the item or repeats the merge: the
	// sweep can only ask the forge, and a second re-arm is not its to make.
	if fixture.tracker.Record().Closed {
		t.Fatalf("a second drop closed the item as integrated: reason = %q", fixture.tracker.Record().CloseReason)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.PullRequest.MergeRearms != 1 {
		t.Fatalf("the settled publication records %d re-arm(s), want the one it had spent", settled.PullRequest.MergeRearms)
	}
}

// A queued merge the forge dropped is a requirement that went unmet, and the
// harness does not merge past one — not by asking again and not with
// administrator privileges. The publication is reported as outstanding, on the
// run and on the work item, and the item is handed to a person with a blocker
// rather than closed as integrated: nothing merged the change anywhere but here.
// The change itself is safe: the local target branch it was integrated into is
// the authoritative one.
func TestReconcileReportsAQueuedMergeTheForgeDropped(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.DropQueuedMerge()
	merges := len(fixture.forge.MergeRequests())

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run settled on a blocker", results)
	}
	for _, want := range []string{"dropped the queued merge", "needs a person"} {
		if !strings.Contains(results[0].Detail, want) {
			t.Errorf("detail %q does not name %q", results[0].Detail, want)
		}
	}
	// The forge never merged it, so nothing may record the item as integrated.
	if fixture.tracker.Record().Closed {
		t.Fatalf("a dropped merge closed the item as integrated: reason = %q", fixture.tracker.Record().CloseReason)
	}
	if !fixture.tracker.Record().Blocked || !strings.Contains(fixture.tracker.Record().BlockReason, "dropped the queued merge") {
		t.Fatalf("blocked = %t, reason = %q; want the dropped merge handed to a person",
			fixture.tracker.Record().Blocked, fixture.tracker.Record().BlockReason)
	}
	// A first drop is work for a person and is also the one drop triage may
	// re-arm, so it must not read as the escalation a second one is.
	if strings.Contains(fixture.tracker.Record().BlockReason, "escalation rather than something to re-arm again") {
		t.Fatalf("a first drop was handed over as an escalation: %q", fixture.tracker.Record().BlockReason)
	}
	// Reconciliation can only ask the forge, so nothing was merged a second time.
	if len(fixture.forge.MergeRequests()) != merges {
		t.Fatalf("reconciliation asked for %d further merge(s)", len(fixture.forge.MergeRequests())-merges)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.PullRequest.MergeQueued || settled.PullRequest.Merged {
		t.Fatalf("settled pull request = %#v, want a dropped merge recorded as neither queued nor merged", settled.PullRequest)
	}
	if !strings.Contains(settled.PublishFailure, "dropped the queued merge") {
		t.Errorf("durable publish failure = %q, want the dropped merge recorded", settled.PublishFailure)
	}
	// The drop is a moment as well as a state, and the moment is what a channel
	// can say. Without it the only account of a dropped merge was a status
	// surface somebody thought to run.
	if settled.MergeDrop == nil {
		t.Fatalf("settled state = %#v, want the moment the forge dropped the merge recorded", settled)
	}
	if settled.MergeDrop.At.IsZero() || !strings.Contains(settled.MergeDrop.Reason, "dropped the queued merge") {
		t.Errorf("merge drop = %#v, want the moment and the reason it was dropped", settled.MergeDrop)
	}
	if !strings.Contains(fixture.tracker.Record().Notes, "Publication outstanding") {
		t.Errorf("tracker notes do not report the outstanding publication:\n%s", fixture.tracker.Record().Notes)
	}
	// The work is where it belongs, and the evidence a person needs survives.
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want the integrated commit %q", local, outcome.Integration.TargetCommit)
	}
	if published := publishedCommit(t, fixture.remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q left for whoever finishes it", published, outcome.PullRequest.HeadCommit)
	}
	// It is settled, so the sweep does not keep reporting it forever.
	if again := fixture.reconcile(t); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// Settling a queued merge is a question for the forge, and reconciliation asks
// it before it observes the repository at all. A local target that moved on
// since the run finished says nothing about a merge nobody has answered for
// yet, and must never settle one as a disagreement.
func TestReconcileAsksTheForgeAboutAQueuedMergeWhateverTheLocalTargetShows(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	// Unrelated work lands on the local target, so it no longer stands where
	// this run's promotion left it.
	if err := os.WriteFile(filepath.Join(fixture.repository, "later.txt"), []byte("later work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, fixture.repository, "add", "later.txt")
	runPipelineGit(t, fixture.repository, "commit", "-m", "later work")

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionQueued || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the forge asked and the run reported as queued", results)
	}
	if fixture.tracker.Record().Blocked {
		t.Fatalf("reconciliation blocked a run whose merge the forge still holds: %q", fixture.tracker.Record().BlockReason)
	}
	held, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if held.Integration == nil || !held.PullRequest.MergeQueued {
		t.Fatalf("held state = %#v, want the queued merge and its promotion untouched", held)
	}

	// The forge merges it, and the next sweep settles the run on that answer
	// rather than on what the local target happens to show.
	fixture.forge.PerformQueuedMerge(t)
	settled := fixture.reconcile(t)
	if len(settled) != 1 || settled[0].Action != ActionCompleted || settled[0].Failure != "" {
		t.Fatalf("reconciliation after the merge = %#v, want it completed", settled)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local == outcome.Integration.TargetCommit {
		t.Fatalf("local main = %q, want the target this test moved on", local)
	}
}

// The forge is asked before the repository is observed at all, so a local
// target that does not carry the promotion cannot settle a merge nobody has
// answered for yet. This builds the one repository the two orderings disagree
// about — the run's branch back at its promoted commit, the local target back
// where it started, and the artifacts recorded as still present — because the
// pipeline's own queued run cleans up locally and never produces it.
func TestReconcileAsksTheForgeAboutAQueuedMergeBeforeObservingTheRepository(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	runPipelineGit(t, fixture.repository, "branch", outcome.Branch, outcome.Integration.SourceCommit)
	runPipelineGit(t, fixture.repository, "update-ref", "refs/heads/main", outcome.BaseCommit)
	queued, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	queued.Phase = runstate.PhaseCleaningUp
	queued.WorktreeRemoved = false
	queued.BranchRemoved = false
	if err := fixture.store.Save(queued); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionQueued || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the forge asked rather than the repository believed", results)
	}
	if fixture.tracker.Record().Blocked {
		t.Fatalf("reconciliation blocked a run whose merge the forge still holds: %q", fixture.tracker.Record().BlockReason)
	}
	held, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if held.Integration == nil || !held.PullRequest.MergeQueued {
		t.Fatalf("held state = %#v, want the queued merge and its promotion untouched", held)
	}
}

// The yoyodyne-ifd.295 shape, replayed. The forge performed the merge it had
// queued, and the connection dropped at the one step the settlement had left:
// deleting the branch that merge consumed. That step is hygiene, so the item
// closes on the confirmed merge anyway, the local target is still caught up, and
// what the failure leaves is a dead branch on the remote recorded for triage.
//
// The old ordering closed the item only if the deletion succeeded. Twice on
// yoyodyne-ifd.295 it did not, and the item stayed open with nothing holding it
// back: the scheduler pulled it as ordinary ready work, and three developer runs
// and three reviews were spent re-deriving that the change had already landed.
func TestASettledMergeClosesTheItemWhenTheMergedBranchCannotBeDeleted(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	dropped := &droppingRemote{deletions: 1_000}
	fixture.worktrees = func(observer ReconcileWorktrees) ReconcileWorktrees {
		dropped.ReconcileWorktrees = observer
		return dropped
	}
	waits := 0
	fixture.sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the merge settled as completed", results)
	}
	// The whole of the change: the merge is confirmed, so the item closes.
	if !fixture.tracker.Record().Closed {
		t.Fatal("the confirmed merge did not close the item because a branch deletion failed, which is the loop this exists to end")
	}
	if !strings.Contains(fixture.tracker.Record().CloseReason, "merged by the forge") {
		t.Errorf("close reason = %q, want the forge's merge named", fixture.tracker.Record().CloseReason)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The failure is recorded rather than swallowed: nothing sweeps a remote
	// branch afterwards, so the leftover is a person's and the outstanding
	// publication is what puts it on the docket and out of the pull.
	if !strings.Contains(settled.PublishFailure, "delete the merged remote branch") {
		t.Errorf("durable publish failure = %q, want the leftover branch recorded", settled.PublishFailure)
	}
	if !strings.Contains(settled.PublishFailure, "handed to a person") {
		t.Errorf("durable publish failure = %q, want the spent recovery window named", settled.PublishFailure)
	}
	// Retried under yoyodyne-ifd.264's rule rather than given up on at the first
	// reset, and the record says so.
	retries := 0
	for _, retry := range settled.Retries {
		if retry.Boundary == runstate.RetryDeleteRemoteBranch {
			retries++
		}
	}
	if retries < 2 || waits != retries {
		t.Errorf("recorded %d retr(ies) of the deletion over %d wait(s), want the connection reset waited out repeatedly", retries, waits)
	}
	if dropped.attempts != retries+1 {
		t.Errorf("the deletion was attempted %d time(s) for %d recorded retr(ies)", dropped.attempts, retries)
	}
	// The catch-up is not the deletion's to hold up either: the merge is
	// confirmed, so the local target moves onto what the forge made of it.
	if local, remote := publishedCommit(t, fixture.repository, "main"), publishedCommit(t, fixture.remote, "main"); local != remote {
		t.Errorf("local main = %q, want it caught up to the remote's %q", local, remote)
	}
	if published := publishedCommit(t, fixture.remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the branch the deletion could not remove left at %q", published, outcome.PullRequest.HeadCommit)
	}
	// A closed item that says nothing about what it left on the forge tells the
	// person who has to remove it nothing.
	for _, want := range []string{"could not delete the branch that merge consumed", outcome.Branch} {
		if !strings.Contains(fixture.tracker.Record().Notes, want) {
			t.Errorf("tracker notes do not report %q:\n%s", want, fixture.tracker.Record().Notes)
		}
	}
	// The run is settled, so nothing pulls this item's thread again.
	if again := fixture.reconcile(t); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// The same drop, survived. A reset that the next attempt gets past leaves a
// publication with nothing outstanding at all — which is the half of the
// recoverable-failure rule that keeps a dead branch off the triage docket for a
// connection that came back.
func TestASettledMergeWaitsOutADroppedConnectionAtTheBranchDeletion(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	dropped := &droppingRemote{deletions: 1}
	fixture.worktrees = func(observer ReconcileWorktrees) ReconcileWorktrees {
		dropped.ReconcileWorktrees = observer
		return dropped
	}
	fixture.sleep = func(context.Context, time.Duration) error { return nil }
	outcome := fixture.run(t)
	fixture.forge.PerformQueuedMerge(t)

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the merge settled as completed", results)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.PublishFailure != "" {
		t.Errorf("publish failure = %q, want a deletion that succeeded on the second attempt to leave none", settled.PublishFailure)
	}
	if published := publishedCommit(t, fixture.remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
	if len(settled.Retries) != 1 || settled.Retries[0].Boundary != runstate.RetryDeleteRemoteBranch {
		t.Errorf("retries = %#v, want the one waited-out reset recorded", settled.Retries)
	}
}

// droppingRemote is the connection to the forge going away at the step a
// settlement has left, which is where it actually went twice on
// yoyodyne-ifd.295. The wording is the one `git` produced there, so the class
// the recoverable-failure rule reads is the class it was given.
type droppingRemote struct {
	ReconcileWorktrees
	deletions int
	attempts  int
}

func (d *droppingRemote) DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error {
	d.attempts++
	if d.attempts <= d.deletions {
		return fmt.Errorf("resolve %s on origin failed with exit code 128: Read from remote host ssh.github.com: Connection reset by peer", worktree.Branch)
	}
	return d.ReconcileWorktrees.DeleteRemoteBranch(ctx, worktree, commit)
}

// queuedFixture is a publishing run whose forge queues the merge, built over
// artifacts a second process can also see. Reconciliation is that second
// process: it settles a queued merge from the repository, the worktree root, and
// the run state store the run itself used.
type queuedFixture struct {
	repository   string
	remote       string
	worktreeRoot string
	store        *runstate.Store
	tracker      recordingTracker
	forge        *orchestratortest.Forge
	// worktrees wraps the repository access the settlement makes, so a test can
	// drop the connection at one step of it. A fixture that sets none settles
	// through the real observer.
	worktrees func(ReconcileWorktrees) ReconcileWorktrees
	// sleep takes the backoff without taking the time.
	sleep func(context.Context, time.Duration) error
	// docket is the triage docket the sweep dockets and settles publications on,
	// where a test reads it; a fixture that sets none sweeps without one.
	docket *memoryDocket
}

func newQueuedFixture(t *testing.T) queuedFixture {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	remote := addBareRemote(t, repository)
	return queuedFixture{
		repository:   repository,
		remote:       remote,
		worktreeRoot: worktreeRoot,
		store:        store,
		tracker:      &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}},
		forge:        &orchestratortest.Forge{Remote: remote, QueueMerge: true},
	}
}

// run drives a whole run, which ends with its merge queued rather than made.
func (f queuedFixture) run(t *testing.T) Outcome {
	t.Helper()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, f.repository, f.worktreeRoot, f.store, f.tracker, provider, []string{"exit 0"}), provider), f.forge)
	outcome, err := pipeline.Run(context.Background(), f.tracker.Record().Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PullRequest == nil || !outcome.PullRequest.MergeQueued {
		t.Fatalf("outcome = %#v, want a run that finished with its merge queued", outcome)
	}
	return outcome
}

// reconcile is the later sweep that asks the forge what became of the queued
// merge and settles the run on the answer.
func (f queuedFixture) reconcile(t *testing.T) []Reconciliation {
	t.Helper()
	results, err := f.reconciler(t).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	return results
}

// converge is the other half of that sweep: the local state brought onto what
// the forge has, once the runs themselves are settled.
func (f queuedFixture) converge(t *testing.T) Convergence {
	t.Helper()
	convergence, err := f.reconciler(t).Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	return convergence
}

func (f queuedFixture) reconciler(t *testing.T) Reconciler {
	t.Helper()
	worktrees := newObserver(t, f.repository, f.worktreeRoot)
	if f.worktrees != nil {
		worktrees = f.worktrees(worktrees)
	}
	reconciler := Reconciler{
		Tracker:   f.tracker,
		Worktrees: worktrees,
		Store:     f.store,
		Publisher: f.forge,
		Sleep:     f.sleep,
	}
	if f.docket != nil {
		reconciler.Docket = docketerOverStore(f.docket, f.store, docketConfig())
	}
	return reconciler
}

// A repository with no remote reports the same thing on every pass. A resumed
// run that said nothing about publishing would read as a run whose policy had
// quietly changed between processes.
func TestResumedRunReportsTheSkippedPublicationToo(t *testing.T) {
	t.Parallel()

	// The restartable fixture's repository has no remote, which is the case that
	// degrades to purely local behavior.
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	write := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}

	interrupted := &interruptedStore{StateStore: store, atAttempt: 2}
	first := orchestratortest.RoleBackend(write, repairVerdict)
	firstPipeline := publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{"exit 0"}), first), &orchestratortest.Forge{})
	firstOutcome, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !interrupted.stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.stopped)
	}
	if !strings.Contains(firstOutcome.PublishSkipped, "no \"origin\" remote") {
		t.Fatalf("interrupted publish skipped = %q", firstOutcome.PublishSkipped)
	}

	second := orchestratortest.RoleBackend(write, approveVerdict)
	resumed := publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second), &orchestratortest.Forge{})
	outcome, err := resumed.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != firstOutcome.RunID {
		t.Fatalf("resumed run = %q, want the interrupted run %q", outcome.RunID, firstOutcome.RunID)
	}
	if outcome.PublishSkipped != firstOutcome.PublishSkipped {
		t.Errorf("resumed publish skipped = %q, want the same reason the first pass reported (%q)", outcome.PublishSkipped, firstOutcome.PublishSkipped)
	}
}

// Publishing commits the developer's work before the checks and the review run,
// so the whole merge gate depends on the reviewer still being handed that work.
// This asserts it against the prompt the reviewer is actually given: an approval
// granted over an empty patch would be a rubber stamp, and every other test here
// uses a reviewer that approves whatever it sees.
func TestPublishedWorkStillReachesTheReviewer(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"),
			[]byte("the developer's work\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 {
		t.Fatalf("reviewer invocations = %d, want exactly one", len(reviews))
	}
	reviewPrompt := reviews[0].Prompt
	// The work really was committed before the review, which is the condition
	// that makes the rest of this test worth asserting.
	if outcome.PullRequest == nil || outcome.PullRequest.HeadCommit == "" {
		t.Fatalf("outcome = %#v, want the attempt published before review", outcome)
	}
	for _, want := range []string{"feature.txt", "the developer's work"} {
		if !strings.Contains(reviewPrompt, want) {
			t.Fatalf("the reviewer was not shown %q in a published run:\n%s", want, reviewPrompt)
		}
	}
	// The summary beside the patch has to describe the change too, rather than
	// reporting a published worktree as having no changes at all.
	if strings.Contains(reviewPrompt, "No reported working tree changes.") {
		t.Errorf("the reviewer was told a published change is no change:\n%s", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "No textual diff content.") {
		t.Errorf("the reviewer was given no patch for a published change:\n%s", reviewPrompt)
	}
}

// A promotion that lost its target is replayed, and the branch the pull request
// carries has to become the replayed one: what the forge merges is that head,
// so a request left at the pre-replay commit would put work on the remote that
// the authoritative local branch does not have. The remote branch is replaced
// from exactly the commit the harness published there, never blindly.
func TestPipelineRepublishesAReplayedChangeOntoItsPullRequest(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	published := ""
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// The target moves once the branch is published and the pull request exists,
	// which is the window a losing promotion opens for a published run.
	forge.OnEnsure = func() {
		if published != "" {
			return
		}
		published = publishedCommit(t, remote, "yoyodyne/yoyodyne-task/01234567")
		writePipelineFile(t, repository, "elsewhere.txt", "somebody else's work\n")
		runPipelineGit(t, repository, "add", "elsewhere.txt")
		runPipelineGit(t, repository, "commit", "-m", "concurrent target change")
		runPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	}
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.IntegrationRetries != 1 || outcome.Integration == nil {
		t.Fatalf("Run() outcome = %#v, want one retried promotion", outcome)
	}
	if outcome.PublishFailure != "" {
		t.Fatalf("publication failed: %q", outcome.PublishFailure)
	}
	// The pull request is the one that was opened, carrying the replayed commit
	// rather than the one it was opened with.
	if len(forge.Opened) != 1 || forge.Number != 1 {
		t.Fatalf("forge saw %d requests, want the one request updated in place", len(forge.Opened))
	}
	if published == "" || outcome.PullRequest.HeadCommit == published {
		t.Fatalf("published head = %q, want the replayed commit rather than %q", outcome.PullRequest.HeadCommit, published)
	}
	if outcome.PullRequest.HeadCommit != outcome.Integration.SourceCommit {
		t.Fatalf("published head = %q, want the promoted commit %q", outcome.PullRequest.HeadCommit, outcome.Integration.SourceCommit)
	}
	if len(forge.Merges) != 1 || forge.Merges[0].HeadCommit != outcome.Integration.SourceCommit {
		t.Fatalf("forge merges = %#v, want the replayed commit merged", forge.Merges)
	}
	assertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PullRequest == nil || state.PullRequest.HeadCommit != outcome.Integration.SourceCommit {
		t.Fatalf("durable pull request = %#v", state.PullRequest)
	}
}

// conflictedPublishedRun is a published run whose approved change conflicts
// when its promotion is replayed: once the pull request is opened, the target
// takes a different answer on the same line and the remote carries it. The
// first developer attempt writes this run's answer; every later one is the
// reconciliation, handed to reconcile, which runs the given hook before
// writing both answers reconciled. It reports the commit the run first
// published, once the pull request has been opened.
func conflictedPublishedRun(t *testing.T, repository string, forge *orchestratortest.Forge, reconciling func(request backend.RunRequest)) (*orchestratortest.Backend, *string) {
	t.Helper()
	published := ""
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		path := filepath.Join(request.WorkingDirectory, "feature.txt")
		if attempts == 1 {
			return os.WriteFile(path, []byte("this run's version\n"), 0o600)
		}
		reconciling(request)
		return os.WriteFile(path, []byte("both versions, reconciled\n"), 0o600)
	}, approveVerdict)
	forge.OnEnsure = func() {
		if published != "" {
			return
		}
		published = publishedCommit(t, forge.Remote, "yoyodyne/yoyodyne-task/01234567")
		writePipelineFile(t, repository, "feature.txt", "someone else's version\n")
		runPipelineGit(t, repository, "add", "feature.txt")
		runPipelineGit(t, repository, "commit", "-m", "a conflicting edit on main")
		runPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	}
	return provider, &published
}

// yoyodyne-ifd.132 on a published run: an approved change whose replay
// conflicts is moved onto the target and handed back to its developer, and the
// branch the pull request carries is no longer an ancestor of what that
// attempt commits. So the attempt replaces the remote run branch rather than
// extending it, by compare-and-swap from exactly the commit the harness
// published there — and never pushes it while the attempt is uncommitted, when
// the worktree's HEAD is the bare target and a pull request whose head sits
// inside its own base reads to a forge as already merged. The same request
// then carries the reconciled commit, and that is what merges.
func TestPipelineReplacesAPublishedBranchWithTheReconciledReplayConflict(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	var remoteWhileReconciling, headWhileReconciling, targetWhileReconciling string
	var markersSeen bool
	provider, published := conflictedPublishedRun(t, repository, forge, func(request backend.RunRequest) {
		content, err := os.ReadFile(filepath.Join(request.WorkingDirectory, "feature.txt"))
		if err != nil {
			t.Errorf("read the conflicted file: %v", err)
		}
		markersSeen = strings.Contains(string(content), "<<<<<<<")
		remoteWhileReconciling = publishedCommit(t, remote, "yoyodyne/yoyodyne-task/01234567")
		headWhileReconciling = gitLine(t, request.WorkingDirectory, "rev-parse", "HEAD")
		targetWhileReconciling = gitLine(t, repository, "rev-parse", "refs/heads/main")
	})
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if *published == "" || outcome.Integration == nil || outcome.PublishFailure != "" {
		t.Fatalf("Run() outcome = %#v, published = %q; want the reconciled change promoted and published", outcome, *published)
	}
	developers := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developers) != 2 || developers[1].SessionID != provider.DeveloperSession {
		t.Fatalf("developer invocations = %d, want the first attempt and one reconciliation in the same session", len(developers))
	}
	if !markersSeen || !strings.Contains(developers[1].Prompt, "Integration conflict: repair required") {
		t.Fatalf("the reconciling developer was not handed the conflict (markers seen = %t):\n%s", markersSeen, developers[1].Prompt)
	}
	// While the attempt was uncommitted, the worktree stood on the bare target
	// and the remote run branch was left exactly where the harness had published
	// it: neither the target nor anything else was pushed in its place.
	if headWhileReconciling != targetWhileReconciling {
		t.Fatalf("worktree HEAD while reconciling = %q, want the moved target %q", headWhileReconciling, targetWhileReconciling)
	}
	if remoteWhileReconciling != *published {
		t.Fatalf("remote run branch while reconciling = %q, want the commit first published %q", remoteWhileReconciling, *published)
	}
	// The one pull request carries the reconciled commit, built on the target,
	// and that is what the forge merged.
	reconciled := outcome.PullRequest.HeadCommit
	if len(forge.Opened) != 1 || forge.Number != 1 {
		t.Fatalf("forge saw %d requests, want the one request carried through", len(forge.Opened))
	}
	if reconciled == *published || reconciled != outcome.Integration.SourceCommit {
		t.Fatalf("published head = %q, want the promoted reconciled commit %q rather than %q", reconciled, outcome.Integration.SourceCommit, *published)
	}
	if remoteBranch := publishedCommit(t, remote, "yoyodyne/yoyodyne-task/01234567"); remoteBranch != "" && remoteBranch != reconciled {
		t.Fatalf("remote run branch = %q, want the reconciled commit %q or the branch cleaned up after the merge", remoteBranch, reconciled)
	}
	if parent := gitLine(t, repository, "rev-parse", reconciled+"^"); parent != targetWhileReconciling {
		t.Fatalf("reconciled commit's parent = %q, want the moved target %q", parent, targetWhileReconciling)
	}
	if len(forge.Merges) != 1 || forge.Merges[0].Number != 1 || forge.Merges[0].HeadCommit != reconciled {
		t.Fatalf("forge merges = %#v, want the reconciled commit merged through the same request", forge.Merges)
	}
	assertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	if integrated := gitLine(t, remote, "show", "main:feature.txt"); integrated != "both versions, reconciled" {
		t.Fatalf("remote main:feature.txt = %q, want the reconciliation", integrated)
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReplayConflict != nil || state.PullRequest == nil || state.PullRequest.HeadCommit != reconciled {
		t.Fatalf("durable state = conflict %#v, pull request %#v; want the conflict settled and the reconciled head recorded", state.ReplayConflict, state.PullRequest)
	}
}

// The replacement is a compare-and-swap from the commit the harness published,
// not a force: a remote run branch that carries anything else by the time the
// reconciled attempt is pushed — somebody else pushed to it — is refused rather
// than overwritten, and the run stops with nothing promoted or merged.
func TestPipelineRefusesToReplaceAPublishedBranchSomebodyElseMoved(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	foreign := ""
	var published *string
	var provider *orchestratortest.Backend
	provider, published = conflictedPublishedRun(t, repository, forge, func(backend.RunRequest) {
		foreign = strings.TrimSpace(gitOutput(t, repository, "commit-tree", *published+"^{tree}", "-p", *published, "-m", "somebody else's push"))
		runPipelineGit(t, repository, "push", "--force", "origin", foreign+":refs/heads/yoyodyne/yoyodyne-task/01234567")
	})
	pipeline, _ := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil || !strings.Contains(err.Error(), "republish") {
		t.Fatalf("Run() error = %v, want the replacement refused", err)
	}
	if foreign == "" {
		t.Fatal("the reconciling attempt never ran, so nothing was moved under the published branch")
	}
	if outcome.Integration != nil || len(forge.Merges) != 0 || tracker.Closed {
		t.Fatalf("Run() outcome = %#v, merges = %#v, closed = %t; want nothing promoted or merged", outcome, forge.Merges, tracker.Closed)
	}
	if remoteBranch := publishedCommit(t, remote, "yoyodyne/yoyodyne-task/01234567"); remoteBranch != foreign {
		t.Fatalf("remote run branch = %q, want somebody else's push %q left standing", remoteBranch, foreign)
	}
}

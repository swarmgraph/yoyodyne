package publicationtests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The shape the operator asked for: the developer phase pushes the branch and
// opens the pull request, and the approving verdict is what merges it. Both are
// performed by the harness — the fake forge here is the harness's own adapter,
// and neither agent is ever asked to run anything.
func TestPipelinePublishesInTheDeveloperPhaseAndMergesOnApproval(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		// The branch must already be published by the time the reviewer is asked,
		// because a pull request nobody can see is not what a reviewer reviews.
		if request.Role == domain.RoleReviewer && len(forge.Opened) == 0 {
			return errors.New("the reviewer ran before the branch was published")
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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
	remoteTarget := testharness.PublishedCommit(t, remote, "main")
	if remoteTarget == outcome.Integration.TargetCommit {
		t.Errorf("remote main = %q, want the forge's merge commit above the promoted commit", remoteTarget)
	}
	if outcome.PullRequest.MergeCommit != remoteTarget {
		t.Errorf("recorded merge commit = %q, want the remote target %q", outcome.PullRequest.MergeCommit, remoteTarget)
	}
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	// The last step of the promotion is local: the merge left the remote a
	// commit ahead, so the run catches the local branch up onto it rather than
	// leaving a checkout somebody has to pull by hand.
	if local := testharness.PublishedCommit(t, repository, "main"); local != remoteTarget {
		t.Errorf("local main = %q, want the forge's merge commit %q", local, remoteTarget)
	}
	if outcome.Catchup == nil || !outcome.Catchup.Advanced || outcome.Catchup.RemoteCommit != remoteTarget {
		t.Errorf("catch-up = %#v, want main advanced onto %q", outcome.Catchup, remoteTarget)
	}
	if outcome.Catchup.Held != "" {
		t.Errorf("catch-up was held: %s", outcome.Catchup.Held)
	}
	if published := testharness.PublishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}

	state, err := store.Load(testharness.PipelineRunID)
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

	repository, remote := testharness.PublishedRepository(t)
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
	}, testharness.RepairVerdict, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
}

// The request the forge is actually asked for says what it contains: the work
// item's own text and the shape of the change, rather than the boilerplate a
// reader of PR #314 got. Both are wired from what the run already holds — the
// claimed item and the summary the harness computed of its worktree — so this
// goes through the pipeline rather than through the rendering alone.
func TestPipelineOpensAPullRequestThatSaysWhatItContains(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
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
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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

// The shape yoyodyne-ifd.321 was admitted on, end to end, replaying the
// yoyodyne-ifd.121.5 handoff that yoyodyne-ifd.387 was admitted on: Publishing
// commits each attempt, so the second review judges a branch that already
// carries the first one — a reduction that takes three thousand lines out of a
// README. The patch it is handed is the branch's whole diff against the run's
// recorded base, so the reduction is in front of it beside the repair, and the
// evidence and the record both name the base and the tip it was judged between.
func TestPipelineTellsAReviewerWhatThePatchOfAContinuedRunSpans(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	var readme strings.Builder
	for i := 0; i < 3500; i++ {
		fmt.Fprintf(&readme, "line %d of the README before the reduction\n", i)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte(readme.String()), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	testharness.RunPipelineGit(t, repository, "add", "README.md")
	testharness.RunPipelineGit(t, repository, "commit", "-m", "the README before the reduction")
	testharness.RunPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
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
	}, testharness.RepairVerdict, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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
		// repair too, because Publishing commits each attempt before its review
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
// behaves exactly as it did before Publishing existed, and says why.
func TestPipelineWithoutARemoteRunsExactlyAsItDidBefore(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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

			repository, _ := testharness.PublishedRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, testharness.ApproveVerdict)
			pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
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

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, StateErr: errors.New("the forge is unreachable")}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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
	state, err := store.Load(testharness.PipelineRunID)
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

	repository, remote := testharness.PublishedRepository(t)
	testharness.ProtectBranch(t, remote, "main")
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	if published := testharness.PublishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
}

// A forge that declines to merge is answering with the repository's own rules.
// Reporting that as a generic failure would leave an operator with a promotion
// that did not publish and no way to learn which requirement was unmet.
func TestPipelineReportsAForgeRefusalAsTheUnmetRequirement(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, MergeErr: publish.MergeRefused{
		Number: 1,
		Method: publish.MergeRebase,
		Status: "BLOCKED",
		Reason: `Required status check "build" is expected`,
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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
	state, err := store.Load(testharness.PipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PublishFailure != outcome.PublishFailure {
		t.Errorf("durable publish failure = %q, want the reported one", state.PublishFailure)
	}
	// Nothing merged, so the remote target must not have moved and the branch
	// carrying the work has to survive for whoever satisfies the requirement.
	if head := testharness.PublishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
	if published := testharness.PublishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
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

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	// Someone else's work lands on the remote target after this run published its
	// branch, which is the window a merge the harness does not perform opens.
	forge.OnEnsure = func() { testharness.DriftRemoteTarget(t, remote, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	cut := testharness.PublishedCommit(t, repository, "main")

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
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	remoteTarget := testharness.PublishedCommit(t, remote, "main")
	if local := testharness.PublishedCommit(t, repository, "main"); local != remoteTarget {
		t.Errorf("local main = %q, want the merge commit %q the forge left on the remote", local, remoteTarget)
	}
	for _, name := range []string{"feature.txt", "elsewhere.txt"} {
		if _, err := os.Stat(filepath.Join(repository, name)); err != nil {
			t.Errorf("main is missing %s after the replay: %v", name, err)
		}
	}
	state, err := store.Load(testharness.PipelineRunID)
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

// A forge that replays what it merges — which is what GitHub's rebase and
// squash methods do — never puts the reviewed commit on the base at all. The
// harness asks for a merge commit precisely so that cannot happen, and reports
// it rather than accepting a rewritten copy if it does: the remote would then
// carry work no local branch has, and which history is right is a person's
// decision.
func TestPipelineReportsAMergeThatRewroteThePromotedCommit(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, ReplayMerge: true}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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
	if published := testharness.PublishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
		t.Errorf("remote branch = %q, want the published commit %q left in place", published, outcome.PullRequest.HeadCommit)
	}
}

// The remote target a published run leaves behind is the forge's merge commit,
// which this repository does not have and never will. The run after it must
// still publish: a drift check that could not tell that state from someone
// else's work would block every publication after the first, permanently.
func TestPipelinePublishesTheRunAfterAForgeMerge(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	write := func(name, content string) func(backend.RunRequest) error {
		return func(request backend.RunRequest) error {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, name), []byte(content), 0o600)
		}
	}
	first := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-first", Title: "First", Status: "open"}}
	firstForge := &orchestratortest.Forge{Remote: remote}
	firstPipeline, _ := testharness.NewPublishingPipeline(t, repository, first,
		orchestratortest.RoleBackend(write("first.txt", "first\n"), testharness.ApproveVerdict), firstForge, []string{"exit 0"})
	firstOutcome, err := firstPipeline.Run(context.Background(), first.Item.ID)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if firstOutcome.PublishFailure != "" {
		t.Fatalf("first publish failure = %q", firstOutcome.PublishFailure)
	}

	second := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-second", Title: "Second", Status: "open"}}
	secondForge := &orchestratortest.Forge{Remote: remote}
	secondPipeline, store := testharness.NewPublishingPipeline(t, repository, second,
		orchestratortest.RoleBackend(write("second.txt", "second\n"), testharness.ApproveVerdict), secondForge, []string{"exit 0"})
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
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	if published := testharness.PublishedCommit(t, remote, outcome.Branch); published != "" {
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

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"printf 'left behind\n' > residue.txt"})

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
	if head := testharness.PublishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
}

// Publishing and Automatic integration are separate opt-ins, so a project can
// have the harness open pull requests and still merge them itself. The run then
// stops where a human-integration run always stopped: checks pass, nothing is
// promoted, and the worktree is preserved — with the work now published.
func TestPipelinePublishesWithoutIntegratingWhenAHumanMerges(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
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
	if head := testharness.PublishedCommit(t, remote, "main"); head != outcome.BaseCommit {
		t.Errorf("remote main = %q, want the untouched base %q", head, outcome.BaseCommit)
	}
	if published := testharness.PublishedCommit(t, remote, outcome.Branch); published != outcome.PullRequest.HeadCommit {
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

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, OpenReplies: 2}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
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
	if published := testharness.PublishedCommit(t, remote, outcome.Branch); published != "" {
		t.Errorf("merged remote branch survived at %q", published)
	}
}

// A repository with no remote reports the same thing on every pass. A resumed
// run that said nothing about Publishing would read as a run whose policy had
// quietly changed between processes.
func TestResumedRunReportsTheSkippedPublicationToo(t *testing.T) {
	t.Parallel()

	// The restartable fixture's repository has no remote, which is the case that
	// degrades to purely local behavior.
	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	write := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}

	interrupted := &testharness.InterruptedStore{StateStore: store, AtAttempt: 2}
	first := orchestratortest.RoleBackend(write, testharness.RepairVerdict)
	firstPipeline := testharness.Publishing(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{"exit 0"}), first), &orchestratortest.Forge{})
	firstOutcome, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !interrupted.Stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.Stopped)
	}
	if !strings.Contains(firstOutcome.PublishSkipped, "no \"origin\" remote") {
		t.Fatalf("interrupted publish skipped = %q", firstOutcome.PublishSkipped)
	}

	second := orchestratortest.RoleBackend(write, testharness.ApproveVerdict)
	resumed := testharness.Publishing(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second), &orchestratortest.Forge{})
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

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"),
			[]byte("the developer's work\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	published := ""
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	// The target moves once the branch is published and the pull request exists,
	// which is the window a losing promotion opens for a published run.
	forge.OnEnsure = func() {
		if published != "" {
			return
		}
		published = testharness.PublishedCommit(t, remote, "yoyodyne/yoyodyne-task/01234567")
		testharness.WritePipelineFile(t, repository, "elsewhere.txt", "somebody else's work\n")
		testharness.RunPipelineGit(t, repository, "add", "elsewhere.txt")
		testharness.RunPipelineGit(t, repository, "commit", "-m", "concurrent target change")
		testharness.RunPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	}
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

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
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	state, err := store.Load(testharness.PipelineRunID)
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
	}, testharness.ApproveVerdict)
	forge.OnEnsure = func() {
		if published != "" {
			return
		}
		published = testharness.PublishedCommit(t, forge.Remote, "yoyodyne/yoyodyne-task/01234567")
		testharness.WritePipelineFile(t, repository, "feature.txt", "someone else's version\n")
		testharness.RunPipelineGit(t, repository, "add", "feature.txt")
		testharness.RunPipelineGit(t, repository, "commit", "-m", "a conflicting edit on main")
		testharness.RunPipelineGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
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

	repository, remote := testharness.PublishedRepository(t)
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
		remoteWhileReconciling = testharness.PublishedCommit(t, remote, "yoyodyne/yoyodyne-task/01234567")
		headWhileReconciling = testharness.GitLine(t, request.WorkingDirectory, "rev-parse", "HEAD")
		targetWhileReconciling = testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")
	})
	pipeline, store := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"test -f feature.txt"})

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
	if remoteBranch := testharness.PublishedCommit(t, remote, "yoyodyne/yoyodyne-task/01234567"); remoteBranch != "" && remoteBranch != reconciled {
		t.Fatalf("remote run branch = %q, want the reconciled commit %q or the branch cleaned up after the merge", remoteBranch, reconciled)
	}
	if parent := testharness.GitLine(t, repository, "rev-parse", reconciled+"^"); parent != targetWhileReconciling {
		t.Fatalf("reconciled commit's parent = %q, want the moved target %q", parent, targetWhileReconciling)
	}
	if len(forge.Merges) != 1 || forge.Merges[0].Number != 1 || forge.Merges[0].HeadCommit != reconciled {
		t.Fatalf("forge merges = %#v, want the reconciled commit merged through the same request", forge.Merges)
	}
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", outcome.Integration.TargetCommit)
	if integrated := testharness.GitLine(t, remote, "show", "main:feature.txt"); integrated != "both versions, reconciled" {
		t.Fatalf("remote main:feature.txt = %q, want the reconciliation", integrated)
	}
	state, err := store.Load(testharness.PipelineRunID)
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

	repository, remote := testharness.PublishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	foreign := ""
	var published *string
	var provider *orchestratortest.Backend
	provider, published = conflictedPublishedRun(t, repository, forge, func(backend.RunRequest) {
		foreign = strings.TrimSpace(testharness.GitOutput(t, repository, "commit-tree", *published+"^{tree}", "-p", *published, "-m", "somebody else's push"))
		testharness.RunPipelineGit(t, repository, "push", "--force", "origin", foreign+":refs/heads/yoyodyne/yoyodyne-task/01234567")
	})
	pipeline, _ := testharness.NewPublishingPipeline(t, repository, tracker, provider, forge, []string{"test -f feature.txt"})

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
	if remoteBranch := testharness.PublishedCommit(t, remote, "yoyodyne/yoyodyne-task/01234567"); remoteBranch != foreign {
		t.Fatalf("remote run branch = %q, want somebody else's push %q left standing", remoteBranch, foreign)
	}
}

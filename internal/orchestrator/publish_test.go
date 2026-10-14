package orchestrator

import (
	"context"
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
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

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
		Drops:     f.forge,
		Sleep:     f.sleep,
	}
	if f.docket != nil {
		reconciler.Docket = docketerOverStore(f.docket, f.store, docketConfig())
	}
	return reconciler
}

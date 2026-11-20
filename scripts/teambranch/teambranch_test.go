package teambranch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RootVariable names the temporary root scripts/teambranch/validate.sh makes
// for a validation and removes afterwards. Without it the validation does not
// run, so nothing here is ever written anywhere the script did not make.
const RootVariable = "YOYODYNE_TEAM_BRANCH_ROOT"

const (
	// target is the branch of the project's own the factory lands work on, and
	// team the branch the team keeps and merges into.
	target = "staging"
	team   = "main"
	// requestNumber is the pull request an outside tool opens into the team's
	// branch for the batch.
	requestNumber = 41
	itemID        = "scratch-item"
)

const approve = testharness.ApproveVerdict

// fixedClock gives the independent review the time it records.
type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }

// countingChecks counts the check stages run through it and where each ran, so
// a step can say the checks ran on the merge it is about.
type countingChecks struct {
	mu          sync.Mutex
	runner      orchestrator.CheckRunner
	directories []string
}

func (c *countingChecks) Run(ctx context.Context, request checks.Request, sink func(execution.Event) error) ([]checks.Result, uint64, error) {
	c.mu.Lock()
	c.directories = append(c.directories, request.Directory)
	c.mu.Unlock()
	return c.runner.Run(ctx, request, sink)
}

func (c *countingChecks) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.directories)
}

// forge is the forge as the validation needs it, standing in for GitHub: the
// pull request into the team's branch as the outside tool opened it, and what
// people have said on it. No request leaves the machine.
type forge struct {
	mu       sync.Mutex
	request  publish.TeamPullRequest
	feedback publish.FeedbackReading
}

func (f *forge) TeamPullRequest(_ context.Context, number int) (publish.TeamPullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if number != f.request.Number {
		return publish.TeamPullRequest{}, os.ErrNotExist
	}
	return f.request, nil
}

func (f *forge) Feedback(_ context.Context, number int) (publish.FeedbackReading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reading := f.feedback
	reading.Number = number
	if reading.State == "" {
		reading.State = "OPEN"
	}
	return reading, nil
}

func (f *forge) set(change func(*forge)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// trackers is the work tracker as the forge check's sweep sees it: every item
// the scratch project has run, each answered by the tracker its run used.
type trackers struct {
	orchestrator.WorkTracker
	items map[string]*orchestratortest.Tracker
}

func (m *trackers) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	return m.items[id].Show(ctx, id)
}

func (m *trackers) RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error) {
	return m.items[id].RecordOutcome(ctx, id, notes)
}

func (m *trackers) Release(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	return m.items[id].Release(ctx, id, reason)
}

func (m *trackers) Complete(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	return m.items[id].Complete(ctx, id, reason)
}

func (m *trackers) Reopen(ctx context.Context, id, reason string, parking domain.WorkItemParking) (beads.WorkItem, error) {
	return m.items[id].Reopen(ctx, id, reason, parking)
}

// flow is the one project every step works: a scratch repository checked out on
// the target branch, a bare remote holding both branches, a clone the team
// merges into its own branch from, and the harness's own pipeline, run store and
// forge check over them.
type flow struct {
	repository   string
	remote       string
	teamClone    string
	worktreeRoot string
	store        *runstate.Store
	tracker      *orchestratortest.Tracker
	provider     *orchestratortest.Backend
	pipeline     orchestrator.Pipeline
	checks       *countingChecks
	reports      *runstate.ReportStore
	records      *runstate.TeamBranchUpdateStore
	forge        *forge
	updater      orchestrator.TeamBranchUpdater
	// trackers holds every item's tracker by identifier, which the forge check's
	// sweep reads as one tracker.
	trackers *trackers

	// What the steps hand to the ones after them.
	runID   string
	landing string
	// failed is the step that failed first, so a later step says it could not
	// run rather than reporting a second, misleading failure.
	failed string
}

func newFlow(t *testing.T) *flow { return newFlowFor(t, team) }

// newFlowFor is newFlow for a project whose execution.upstream_branch is the
// given branch; empty leaves it unset.
func newFlowFor(t *testing.T, upstream string) *flow {
	t.Helper()
	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	// The scratch project has one commit on the team's branch. The target branch
	// starts level with it and the primary checkout is on the target, as it is
	// for a project that lands on a branch of its own.
	testharness.RunPipelineGit(t, repository, "branch", target)
	remote := testharness.AddBareRemote(t, repository)
	testharness.RunPipelineGit(t, repository, "push", "origin", "refs/heads/"+target+":refs/heads/"+target)
	testharness.RunPipelineGit(t, repository, "switch", target)
	teamClone := filepath.Join(t.TempDir(), "team")
	testharness.RunPipelineGit(t, repository, "clone", "--branch", team, remote, teamClone)
	testharness.RunPipelineGit(t, teamClone, "config", "user.name", "Teammate")
	testharness.RunPipelineGit(t, teamClone, "config", "user.email", "teammate@example.invalid")

	f := &flow{repository: repository, remote: remote, teamClone: teamClone, worktreeRoot: worktreeRoot, store: store, forge: &forge{}}
	f.tracker = &orchestratortest.Tracker{Item: beads.WorkItem{ID: itemID, Title: "Scratch item", Status: "open"}}
	f.trackers = &trackers{items: map[string]*orchestratortest.Tracker{itemID: f.tracker}}
	f.provider = orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approve)
	f.pipeline = testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, f.tracker, f.provider, []string{"test -f docs/design.md"}), f.provider)
	f.pipeline.Config.Execution.UpstreamBranch = upstream
	f.checks = &countingChecks{runner: f.pipeline.Checks}
	f.pipeline.Checks = f.checks

	stateRoot := t.TempDir()
	reviews, err := runstate.NewBranchReviewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("open the review record: %v", err)
	}
	if f.reports, err = runstate.NewReportStore(stateRoot, "yoyodyne"); err != nil {
		t.Fatalf("open the development manager's list: %v", err)
	}
	if f.records, err = runstate.NewTeamBranchUpdateStore(stateRoot, "yoyodyne"); err != nil {
		t.Fatalf("open the record of updates: %v", err)
	}
	manager, ok := f.pipeline.Worktrees.(*gitworktree.Manager)
	if !ok {
		t.Fatalf("the pipeline's worktrees are a %T; expected the harness's own manager", f.pipeline.Worktrees)
	}
	f.updater = orchestrator.TeamBranchUpdater{
		Pipeline: &f.pipeline,
		Git:      manager,
		Records:  f.records,
		Reviewer: orchestrator.BranchReviewer{
			Worktrees: manager,
			Reviewer:  review.Reviewer{Backend: f.provider, Model: testharness.TestReviewerModel, Clock: fixedClock{}},
			Reviews:   reviews, Reports: f.reports, Clock: fixedClock{},
			Repository: repository, Config: f.pipeline.Config,
		},
		Reports: f.reports,
	}
	return f
}

// reconciler is the forge check's sweep over the run store.
func (f *flow) reconciler(t *testing.T) orchestrator.Reconciler {
	t.Helper()
	return orchestrator.Reconciler{
		Tracker:          f.trackers,
		Worktrees:        testharness.NewObserver(t, f.repository, f.worktreeRoot),
		Store:            f.store,
		TeamPullRequests: f.forge,
		Feedback:         f.forge,
		Reports:          f.reports,
		LandingPaths:     f.pipeline.Worktrees.(orchestrator.LandingPathReader),
	}
}

// pipelineFor is the project's pipeline for another item, which writes a file
// of its own so no two items' changes are the same.
func (f *flow) pipelineFor(t *testing.T, id string, dependencies ...beads.Dependency) (orchestrator.Pipeline, *orchestratortest.Tracker) {
	t.Helper()
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: id, Title: "Item " + id, Status: "open", Dependencies: dependencies}}
	f.trackers.items[id] = tracker
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, id+".txt"), []byte("implemented "+id+"\n"), 0o600)
	}, approve)
	pipeline := testharness.Automatic(f.pipeline, provider)
	pipeline.Tracker, pipeline.Backend = tracker, provider
	pipeline.NewRunID = runstate.NewRunID
	return pipeline, tracker
}

// land runs another item to its landing and returns its run.
func (f *flow) land(t *testing.T, id string) (runstate.State, *orchestratortest.Tracker) {
	t.Helper()
	pipeline, tracker := f.pipelineFor(t, id)
	outcome, err := pipeline.Run(context.Background(), id)
	if err != nil || outcome.Integration == nil {
		t.Fatalf("running %s gave %#v, error %v; expected its change landed", id, outcome, err)
	}
	return testharness.LoadRun(t, f.store, outcome.RunID), tracker
}

// opens makes the forge report a pull request into the team's branch carrying
// the given commits.
func (f *flow) opens(commits ...string) {
	f.forge.set(func(g *forge) {
		g.request = publish.TeamPullRequest{
			Number: requestNumber, URL: "https://forge.example.invalid/pull/41", State: "OPEN",
			BaseBranch: team, HeadBranch: target, HeadCommit: commits[len(commits)-1], Commits: commits,
		}
	})
}

func (f *flow) track(t *testing.T, unverified bool, items ...string) (orchestrator.TeamPullRequestTrack, error) {
	t.Helper()
	return f.reconciler(t).TrackTeamPullRequest(context.Background(), orchestrator.TeamPullRequestTracking{
		Number: requestNumber, Items: items, Unverified: unverified, TargetBranch: target, UpstreamBranch: team,
	})
}

func (f *flow) settle(t *testing.T) []orchestrator.TeamPullRequestSettlement {
	t.Helper()
	settled, err := f.reconciler(t).SettleTeamPullRequests(context.Background())
	if err != nil {
		t.Fatalf("settling the pull requests failed: %v", err)
	}
	return settled
}

func (f *flow) runOf(t *testing.T, id string) runstate.State {
	t.Helper()
	runs, err := f.store.Runs(id)
	if err != nil || len(runs) != 1 {
		t.Fatalf("the runs of %s are %d (error %v); expected one", id, len(runs), err)
	}
	return runs[0]
}

// statusLines is what `yoyo status` renders from the read model over this
// project's run store and update records.
func (f *flow) statusLines() string {
	return readmodel.ReadStanding(context.Background(), readmodel.Sources{Runs: f.store, TeamBranchUpdates: f.records, Capacity: 1}).RenderLines()
}

func (f *flow) run(t *testing.T) runstate.State {
	t.Helper()
	return testharness.LoadRun(t, f.store, f.runID)
}

func (f *flow) toldTheDevelopmentManager(t *testing.T) []report.Report {
	t.Helper()
	all, err := f.reports.List()
	if err != nil {
		t.Fatalf("read the development manager's list: %v", err)
	}
	var told []report.Report
	for _, each := range all {
		if each.For == domain.RoleDevelopmentManager {
			told = append(told, each)
		}
	}
	return told
}

// step is one thing the design says happens. The first eight follow one item
// from its start to its closing and run in order; a step that fails stops the
// ones after it. The rest are cases of their own, each against a project of its
// own, so one failing never hides another. A case whose piece has not landed
// names the work it waits on instead of a function, and is reported as not yet
// built: counted as neither passed nor failed.
type step struct {
	name    string
	run     func(t *testing.T, f *flow)
	own     func(t *testing.T)
	waitsOn string
}

var steps = []step{
	{name: "01 an item is run from its start and lands on the target branch, and the team's branch does not hold it", run: stepStart},
	{name: "02 the item is left open, waiting for the team's merge, and its developer slot is freed", run: stepWaiting},
	{name: "03 the outside tool's pull request into the team's branch is refused where it is wrong and recorded where the forge confirms it", run: stepTrack},
	{name: "04 requested changes on the pull request reach the development manager's list naming the item, and start no run", run: stepFeedback},
	{name: "05 the pull request still open leaves the item waiting", run: stepStillOpen},
	{name: "06 the team's branch moves, and the target branch gains one merge that was checked and reviewed", run: stepMovedTarget},
	{name: "07 the team merges the pull request, and the item closes naming the merge commit", run: stepTeamMerge},
	{name: "08 nothing is left waiting, and a later check changes nothing", run: stepSettled},
	{name: "09 with the team's branch unset, an item closes when it lands", own: caseUnset},
	{name: "10 an item that depends on a waiting item starts on the same target branch, and one that depends on any other open item does not", own: caseDependency},
	{name: "11 naming an item whose landing commit is not among the pull request's commits is refused unless it is marked unverified", own: caseUnverified},
	{name: "12 a tracked pull request closed without merging sends its items to the Lead Product Manager with the change kept", own: caseClosedUnmerged},
	{name: "13 an item that lands while a tracked pull request's head is the target branch is added to that pull request's record with a note", own: caseAddedByLanding},
	{name: "14 squash merging a tracked pull request closes its items", own: caseSquash},
	{name: "15 a conflict while bringing the target branch up to date reaches the development manager's list, and landing carries on", own: caseConflict},
	{name: "16 with a landing hold set nothing lands, checks and reviews carry on, and the hold ends at its expiry", own: caseHold},
	{name: "17 a comment on a line of a file reaches the development manager's list naming the items whose landed commits touched that file and no others, and an ordinary comment names none", own: caseLineComment},
	{name: "18 the development manager's decision to repair an item whose run has ended starts a new run bound to the feedback entry, which lands on the target branch and leaves the item waiting for the team's merge", own: caseFeedbackRepair},
}

// TestTeamBranchFlow runs every step.
func TestTeamBranchFlow(t *testing.T) {
	root := os.Getenv(RootVariable)
	if root == "" {
		t.Skipf("the team-branch validation runs only from scripts/teambranch/validate.sh, which sets %s", RootVariable)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("%s is %q, which is not an absolute path", RootVariable, root)
	}
	f := newFlow(t)
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			switch {
			case s.waitsOn != "":
				// The script reads this line by its number to say what the step
				// waits on.
				t.Skipf("not yet built: step %s waits on %s", strings.SplitN(s.name, " ", 2)[0], s.waitsOn)
			case s.own != nil:
				s.own(t)
				return
			}
			if f.failed != "" {
				t.Fatalf("not run: step %q did not happen, and this step follows from it", f.failed)
			}
			defer func() {
				if t.Failed() {
					f.failed = s.name
				}
			}()
			s.run(t, f)
		})
	}
}

// stepStart is step 01.
func stepStart(t *testing.T, f *flow) {
	before := testharness.PublishedCommit(t, f.repository, target)
	outcome, err := f.pipeline.Run(context.Background(), itemID)
	if err != nil {
		t.Fatalf("running the item failed: %v; expected it to run to its landing", err)
	}
	if outcome.Integration == nil {
		t.Fatalf("the run's outcome is %#v; expected its change integrated on %s", outcome, target)
	}
	f.runID = outcome.RunID
	f.landing = testharness.PublishedCommit(t, f.repository, target)
	if f.landing == before {
		t.Fatalf("%s is still at %s; expected the change landed on it", target, before)
	}
	if got := testharness.GitLine(t, f.repository, "show", f.landing+":feature.txt"); got != "implemented" {
		t.Fatalf("feature.txt on %s holds %q; expected the item's change", target, got)
	}
	if held := testharness.PublishedCommit(t, f.repository, team); held == f.landing || testharness.GitLine(t, f.repository, "branch", "--contains", f.landing, "--list", team) != "" {
		t.Fatalf("the team's branch %s holds the landing commit %s; expected only %s to", team, f.landing, target)
	}
}

// stepWaiting is step 02.
func stepWaiting(t *testing.T, f *flow) {
	run := f.run(t)
	if !run.WaitingForTeamMerge() || run.TeamMerge == nil {
		t.Fatalf("the run's wait is %#v; expected it waiting for the team's merge", run.TeamMerge)
	}
	wait := run.TeamMerge
	if wait.TargetBranch != target || wait.UpstreamBranch != team || wait.LandingCommit != f.landing {
		t.Fatalf("the wait is %#v; expected landing commit %s on %s, for %s", wait, f.landing, target, team)
	}
	if f.tracker.Closed || !f.tracker.Claimed {
		t.Fatalf("the item is closed = %t, claimed = %t; expected it still claimed and open", f.tracker.Closed, f.tracker.Claimed)
	}
	want := "waiting for the team to merge it into " + team
	if !strings.Contains(f.tracker.Notes, want) || !strings.Contains(f.tracker.Notes, "Landed on "+target) {
		t.Fatalf("the item's notes are %q; expected them to say it landed on %s and is %q", f.tracker.Notes, target, want)
	}
	if run.HoldsDeveloperSlot() {
		t.Fatalf("the run is %s and still holds a developer slot; expected the slot freed", run.Status)
	}
	waiting, err := f.store.WaitingForTeamMerge()
	if err != nil || len(waiting) != 1 || waiting[0].RunID != f.runID {
		t.Fatalf("the runs waiting for the team's merge are %v (error %v); expected this one alone", waiting, err)
	}

	// The item is not pulled again: asking the pipeline to run it once more
	// starts nothing, and it still has the one run.
	if outcome, err := f.pipeline.Run(context.Background(), itemID); err == nil && outcome.Integration != nil {
		t.Fatalf("running the waiting item again gave %#v; expected it refused", outcome)
	}
	if runs, err := f.store.Runs(itemID); err != nil || len(runs) != 1 {
		t.Fatalf("the item has %d run(s) after being asked for again (error %v); expected the one", len(runs), err)
	}
	if line := f.statusLines(); !strings.Contains(line, "waiting for the team to merge it into "+team) || !strings.Contains(line, itemID) {
		t.Fatalf("the status line reads:\n%s\nexpected it to name %s as landed on %s and waiting for the team to merge it into %s", line, itemID, target, team)
	}
}

// stepTrack is step 03.
func stepTrack(t *testing.T, f *flow) {
	reconciler := f.reconciler(t)
	track := func(request orchestrator.TeamPullRequestTracking) (orchestrator.TeamPullRequestTrack, error) {
		request.TargetBranch, request.UpstreamBranch = target, team
		return reconciler.TrackTeamPullRequest(context.Background(), request)
	}

	// A pull request that does not exist, and one into another branch, are
	// refused and record nothing.
	if _, err := track(orchestrator.TeamPullRequestTracking{Number: 99, Items: []string{itemID}}); err == nil {
		t.Fatal("recording a pull request the forge does not have was accepted; expected a refusal")
	}
	f.forge.set(func(g *forge) {
		g.request = publish.TeamPullRequest{
			Number: requestNumber, URL: "https://forge.example.invalid/pull/41", State: "OPEN",
			BaseBranch: "elsewhere", HeadBranch: target, HeadCommit: f.landing, Commits: []string{f.landing},
		}
	})
	if _, err := track(orchestrator.TeamPullRequestTracking{Number: requestNumber, Items: []string{itemID}}); err == nil {
		t.Fatalf("recording a pull request into %q, not into %s, was accepted; expected a refusal", "elsewhere", team)
	}
	if f.run(t).TeamMerge.PullRequest != nil {
		t.Fatal("a refused record left a pull request on the run; expected nothing recorded")
	}

	// The same pull request into the team's branch, carrying the landing commit,
	// is recorded.
	f.forge.set(func(g *forge) { g.request.BaseBranch = team })
	tracked, err := track(orchestrator.TeamPullRequestTracking{Number: requestNumber, Items: []string{itemID}})
	if err != nil || len(tracked.Items) != 1 || tracked.Items[0].Failure != "" || tracked.Items[0].Unverified {
		t.Fatalf("recording the pull request gave %#v (error %v); expected the item recorded, verified, with no failure", tracked, err)
	}
	pull := f.run(t).TeamMerge.PullRequest
	if pull == nil || pull.Number != requestNumber {
		t.Fatalf("the run's record of the pull request is %#v; expected #%d", pull, requestNumber)
	}
}

// stepFeedback is step 04.
func stepFeedback(t *testing.T, f *flow) {
	before, err := f.store.Recorded()
	if err != nil {
		t.Fatalf("list the runs: %v", err)
	}
	at := f.run(t).TeamMerge.Since.Add(10 * time.Minute).UTC().Truncate(time.Second)
	f.forge.set(func(g *forge) {
		g.feedback = publish.FeedbackReading{Reviews: []publish.FeedbackReview{{
			ID: 11, Author: "ada", State: publish.ReviewChangesRequested, Body: "Please rename the flag.", At: at,
		}}}
	})
	settled, err := f.reconciler(t).TakeUpTeamFeedback(context.Background())
	if err != nil || len(settled) != 1 {
		t.Fatalf("reading feedback gave %#v (error %v); expected one pull request read", settled, err)
	}
	if got := settled[0]; got.Number != requestNumber || got.New != 1 || got.Requested != 1 || got.Failure != "" || !slices.Equal(got.Items, []string{itemID}) {
		t.Fatalf("reading feedback gave %#v; expected one requested change on #%d carrying %s", got, requestNumber, itemID)
	}
	told := f.toldTheDevelopmentManager(t)
	if len(told) != 1 || !strings.Contains(told[0].Message, "ada") || !strings.Contains(told[0].Message, "Please rename the flag.") || !strings.Contains(told[0].Message, itemID) {
		t.Fatalf("the development manager's list holds %#v; expected one entry quoting ada and naming %s", told, itemID)
	}
	after, err := f.store.Recorded()
	if err != nil || len(after) != len(before) {
		t.Fatalf("there were %d runs and now there are %d (error %v); expected the feedback to start none", len(before), len(after), err)
	}
	if run := f.run(t); !run.WaitingForTeamMerge() || run.RepairAttempts != 0 {
		t.Fatalf("the run is %s with %d repair attempts; expected it still waiting with nothing spent", run.Status, run.RepairAttempts)
	}
	again, err := f.reconciler(t).TakeUpTeamFeedback(context.Background())
	if err != nil || len(again) != 1 || again[0].New != 0 || len(f.toldTheDevelopmentManager(t)) != 1 {
		t.Fatalf("reading the same feedback again gave %#v (error %v); expected nothing new and the list unchanged", again, err)
	}
}

// stepStillOpen is step 05.
func stepStillOpen(t *testing.T, f *flow) {
	settled, err := f.reconciler(t).SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Found != orchestrator.TeamPullRequestOpen || settled[0].Failure != "" {
		t.Fatalf("settling the open pull request gave %#v (error %v); expected it found open", settled, err)
	}
	if f.tracker.Closed || !f.run(t).WaitingForTeamMerge() {
		t.Fatalf("the item is closed = %t and the run waiting = %t; expected the item left waiting", f.tracker.Closed, f.run(t).WaitingForTeamMerge())
	}
}

// stepMovedTarget is step 06.
func stepMovedTarget(t *testing.T, f *flow) {
	// The team merges a change of its own into its branch, which the target
	// branch does not hold.
	if err := os.WriteFile(filepath.Join(f.teamClone, "team.txt"), []byte("the team's work\n"), 0o600); err != nil {
		t.Fatalf("write the team's change: %v", err)
	}
	testharness.RunPipelineGit(t, f.teamClone, "add", "team.txt")
	testharness.RunPipelineGit(t, f.teamClone, "commit", "-m", "the team's change")
	testharness.RunPipelineGit(t, f.teamClone, "push", "origin", "HEAD:refs/heads/"+team)
	tip := testharness.GitLine(t, f.teamClone, "rev-parse", "HEAD")

	checksBefore := f.checks.count()
	reviewsBefore := len(f.provider.RequestsForRole(domain.RoleReviewer))
	outcome, err := f.updater.Update(context.Background())
	if err != nil {
		t.Fatalf("bringing %s up to date failed: %v (outcome %#v)", target, err, outcome)
	}
	merged := testharness.PublishedCommit(t, f.repository, target)
	if outcome.Landed != merged || merged == f.landing || outcome.TeamCommit != tip || outcome.Conflict != nil || outcome.Waiting != "" {
		t.Fatalf("the update gave %#v; %s was %s and is %s; expected one merge of the team's tip %s landed", outcome, target, f.landing, merged, tip)
	}
	if parents := testharness.GitLine(t, f.repository, "rev-list", "--parents", "-n", "1", merged); parents != merged+" "+f.landing+" "+tip {
		t.Fatalf("the merge's parents are %q; expected the old target %s, then the team's tip %s", parents, f.landing, tip)
	}
	if got := testharness.GitLine(t, f.repository, "show", merged+":team.txt"); got != "the team's work" {
		t.Fatalf("team.txt on the merge holds %q; expected the team's work", got)
	}
	if f.checks.count() <= checksBefore {
		t.Fatalf("the check stage ran %d time(s) before the update and %d after; expected it to run on the merge", checksBefore, f.checks.count())
	}
	if got := len(f.provider.RequestsForRole(domain.RoleReviewer)); got != reviewsBefore+1 {
		t.Fatalf("the independent reviewer was invoked %d time(s) for the merge; expected once", got-reviewsBefore)
	}
	record, found, err := f.records.Load(target)
	if err != nil || !found || len(record.Landed) != 1 {
		t.Fatalf("the update record is %#v (found %t, error %v); expected one landed update", record, found, err)
	}
	landed := record.Landed[0]
	if landed.LandedCommit != merged || landed.Candidate != merged || landed.TeamCommit != tip || landed.TargetBase != f.landing {
		t.Fatalf("the landed update is %#v; expected merge %s of team commit %s onto %s", landed, merged, tip, f.landing)
	}
	if len(landed.Checks) == 0 || slices.ContainsFunc(landed.Checks, func(c runstate.TeamBranchUpdateCheck) bool { return !c.Passed }) {
		t.Fatalf("the update's checks are %#v; expected at least one, all passed", landed.Checks)
	}
	if landed.Review == nil || landed.Review.Decision != "approve" {
		t.Fatalf("the update's review is %#v; expected an approval by the independent reviewer", landed.Review)
	}
	// The status line says when the target branch was last brought up to date.
	if line := f.statusLines(); !strings.Contains(line, target+" was last brought up to date with "+team) || !strings.Contains(line, merged[:12]) {
		t.Fatalf("the status line reads:\n%s\nexpected it to say %s was last brought up to date with %s, by merge %s", line, target, team, merged[:12])
	}
	// The merge brought nothing of the item's closing with it: the team has not
	// merged the item yet.
	if f.tracker.Closed || !f.run(t).WaitingForTeamMerge() {
		t.Fatal("the update closed the item or ended its wait; expected only the team's merge to")
	}
	// The update is one merge per check: nothing more to do now.
	again, err := f.updater.Update(context.Background())
	if err != nil || again.Landed != "" || !again.Current {
		t.Fatalf("a second update gave %#v (error %v); expected the target found current", again, err)
	}
}

// stepTeamMerge is step 07.
func stepTeamMerge(t *testing.T, f *flow) {
	// The team merges the pull request by taking the target branch as it stands,
	// which already holds the team's own tip, so the push is a fast-forward.
	head := testharness.PublishedCommit(t, f.repository, target)
	testharness.RunPipelineGit(t, f.repository, "push", "origin", head+":refs/heads/"+team)
	f.forge.set(func(g *forge) {
		g.request.State, g.request.Merged, g.request.MergeCommit = "MERGED", true, head
	})
	settled, err := f.reconciler(t).SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Found != orchestrator.TeamPullRequestMerged || settled[0].Failure != "" ||
		len(settled[0].Items) != 1 || settled[0].Items[0].Failure != "" {
		t.Fatalf("settling the merged pull request gave %#v (error %v); expected it found merged and its item settled", settled, err)
	}
	if !f.tracker.Closed || !strings.Contains(f.tracker.CloseReason, head) {
		t.Fatalf("the item closed = %t with reason %q; expected it closed naming the merge commit %s", f.tracker.Closed, f.tracker.CloseReason, head)
	}
	run := f.run(t)
	if run.WaitingForTeamMerge() || run.TeamMerge == nil || !run.TeamMerge.Found() {
		t.Fatalf("the run's wait is %#v; expected it ended with where the team's merge was found", run.TeamMerge)
	}
}

// stepSettled is step 08.
func stepSettled(t *testing.T, f *flow) {
	calls := len(f.tracker.Calls)
	reconciler := f.reconciler(t)
	if settled, err := reconciler.SettleTeamPullRequests(context.Background()); err != nil || len(settled) != 0 {
		t.Fatalf("settling pull requests again gave %#v (error %v); expected none left", settled, err)
	}
	if settled, err := reconciler.SettleTeamMerges(context.Background()); err != nil || len(settled) != 0 {
		t.Fatalf("settling the team's merges again gave %#v (error %v); expected none left", settled, err)
	}
	if feedback, err := reconciler.TakeUpTeamFeedback(context.Background()); err != nil || len(feedback) != 0 {
		t.Fatalf("reading feedback again gave %#v (error %v); expected no pull request left to read", feedback, err)
	}
	if len(f.tracker.Calls) != calls {
		t.Fatalf("the later checks made %v on the work item; expected nothing", f.tracker.Calls[calls:])
	}
}

// caseUnset is step 09.
func caseUnset(t *testing.T) {
	f := newFlowFor(t, "")
	run, tracker := f.land(t, "plain-item")
	if run.TeamMerge != nil || run.WaitingForTeamMerge() {
		t.Fatalf("the run's wait is %#v; expected none with no team's branch set", run.TeamMerge)
	}
	if !tracker.Closed {
		t.Fatalf("the item's tracker calls are %v; expected the item closed when its change landed", tracker.Calls)
	}
}

// caseDependency is step 10.
func caseDependency(t *testing.T) {
	f := newFlow(t)
	f.land(t, "needed")
	blocker := func(id string) beads.Dependency {
		return beads.Dependency{ID: id, Type: "blocks", Status: "in_progress"}
	}

	// An item that depends on one waiting for the team's merge starts and lands
	// on the same target branch.
	pipeline, tracker := f.pipelineFor(t, "dependent", blocker("needed"))
	outcome, err := pipeline.Run(context.Background(), "dependent")
	if err != nil || outcome.Integration == nil || tracker.Closed {
		t.Fatalf("running the dependent item gave %#v, error %v, closed %t; expected it landed and waiting for the team's merge", outcome, err, tracker.Closed)
	}
	if !f.runOf(t, "dependent").WaitingForTeamMerge() {
		t.Fatal("the dependent item's run is not waiting for the team's merge")
	}

	// One that depends on any other unfinished item is still held back.
	before := testharness.PublishedCommit(t, f.repository, target)
	pipeline, _ = f.pipelineFor(t, "held-back", blocker("unrelated"))
	if outcome, err := pipeline.Run(context.Background(), "held-back"); err == nil && outcome.Integration != nil {
		t.Fatalf("running an item that depends on an unrelated open item gave %#v; expected it refused", outcome)
	}
	if after := testharness.PublishedCommit(t, f.repository, target); after != before {
		t.Fatalf("%s moved from %s to %s; expected the held-back item to land nothing", target, before, after)
	}
}

// caseUnverified is step 11.
func caseUnverified(t *testing.T) {
	f := newFlow(t)
	run, _ := f.land(t, "squashed")
	f.opens(strings.Repeat("a", 40))
	_, err := f.track(t, false, "squashed")
	if err == nil || !strings.Contains(err.Error(), "--unverified") || !strings.Contains(err.Error(), run.TeamMerge.LandingCommit) {
		t.Fatalf("recording an item whose landing commit is not among the pull request's commits gave error %v; expected a refusal naming the commit and --unverified", err)
	}
	if f.runOf(t, "squashed").TeamMerge.PullRequest != nil {
		t.Fatal("the refused record left a pull request on the run")
	}
	tracked, err := f.track(t, true, "squashed")
	if err != nil || len(tracked.Items) != 1 || !tracked.Items[0].Unverified || tracked.Items[0].Failure != "" {
		t.Fatalf("recording it as unverified gave %#v, error %v; expected it recorded and marked unverified", tracked, err)
	}
	if pull := f.runOf(t, "squashed").TeamMerge.PullRequest; pull == nil || !pull.Unverified {
		t.Fatalf("the run's record of the pull request is %#v; expected it marked unverified", pull)
	}
	if notes := f.trackers.items["squashed"].Notes; !strings.Contains(notes, "unverified") {
		t.Fatalf("the item's notes are %q; expected them to say it was recorded as unverified", notes)
	}
}

// caseClosedUnmerged is step 12.
func caseClosedUnmerged(t *testing.T) {
	f := newFlow(t)
	run, tracker := f.land(t, "refused-item")
	f.opens(run.TeamMerge.LandingCommit)
	if _, err := f.track(t, false, "refused-item"); err != nil {
		t.Fatalf("recording the pull request failed: %v", err)
	}
	closedAt := time.Date(2026, 11, 19, 9, 30, 0, 0, time.UTC)
	f.forge.set(func(g *forge) { g.request.State = "CLOSED" })
	reconciler := f.reconciler(t)
	reconciler.Closers = closers{publish.Closer{By: "octo-reviewer", At: closedAt}}
	settled, err := reconciler.SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Found != orchestrator.TeamPullRequestClosed || settled[0].Failure != "" ||
		len(settled[0].Items) != 1 || !settled[0].Items[0].Settled {
		t.Fatalf("settling the closed pull request gave %#v, error %v; expected its item given back", settled, err)
	}
	if !tracker.Reopened || tracker.Closed {
		t.Fatalf("the item's tracker calls are %v; expected it given back, not closed", tracker.Calls)
	}
	for _, phrase := range []string{"closed without being merged, by octo-reviewer", "Lead Product Manager", "nothing was undone"} {
		if !strings.Contains(tracker.ReopenReason, phrase) {
			t.Errorf("the note on the item is %q; expected it to say %q", tracker.ReopenReason, phrase)
		}
	}
	if parking := tracker.Item.Parking; !parking.Parked() || !strings.Contains(parking.Reason(), "octo-reviewer") {
		t.Errorf("the item's parking is %q; expected it parked for the Lead Product Manager naming octo-reviewer", parking)
	}
	if after := f.runOf(t, "refused-item"); after.WaitingForTeamMerge() || after.TeamMerge.Returned == nil || after.TeamMerge.Returned.ClosedBy != "octo-reviewer" {
		t.Errorf("the run's wait is %#v; expected it ended on the closing", after.TeamMerge)
	}
	// The change is kept: nothing was undone on the target branch.
	if got := testharness.GitLine(t, f.repository, "branch", "--contains", run.TeamMerge.LandingCommit, "--list", target); !strings.Contains(got, target) {
		t.Errorf("%s no longer holds the landing commit %s; expected the change kept", target, run.TeamMerge.LandingCommit)
	}
}

// closers answers who closed a pull request.
type closers struct{ closer publish.Closer }

func (c closers) Closer(_ context.Context, number int) (publish.Closer, error) {
	closer := c.closer
	closer.Number = number
	return closer, nil
}

// caseAddedByLanding is step 13.
func caseAddedByLanding(t *testing.T) {
	f := newFlow(t)
	first, _ := f.land(t, "first-item")
	f.opens(first.TeamMerge.LandingCommit)
	if _, err := f.track(t, false, "first-item"); err != nil {
		t.Fatalf("recording the pull request failed: %v", err)
	}

	// Another item lands on the target branch, which is the pull request's head,
	// and so is among its commits at the next check.
	second, tracker := f.land(t, "second-item")
	if second.TeamMerge.PullRequest != nil {
		t.Fatal("the second item was recorded under a pull request before any check read it")
	}
	f.opens(first.TeamMerge.LandingCommit, second.TeamMerge.LandingCommit)
	settled := f.settle(t)
	if len(settled) != 1 || settled[0].Found != orchestrator.TeamPullRequestOpen || len(settled[0].Items) != 1 ||
		settled[0].Items[0].WorkItemID != "second-item" || !settled[0].Items[0].Added || settled[0].Items[0].Failure != "" {
		t.Fatalf("settling the open pull request gave %#v; expected second-item added to it", settled)
	}
	if pull := f.runOf(t, "second-item").TeamMerge.PullRequest; pull == nil || pull.Number != requestNumber || !pull.Found {
		t.Fatalf("the second item's record of the pull request is %#v; expected #%d, found among its commits", pull, requestNumber)
	}
	if !strings.Contains(tracker.Notes, "found this item's landing commit") || !strings.Contains(tracker.Notes, "#41") {
		t.Fatalf("the item's notes are %q; expected a note saying the pull request now carries it", tracker.Notes)
	}
}

// caseSquash is step 14.
func caseSquash(t *testing.T) {
	f := newFlow(t)
	run, tracker := f.land(t, "squash-item")
	f.opens(run.TeamMerge.LandingCommit)
	if _, err := f.track(t, false, "squash-item"); err != nil {
		t.Fatalf("recording the pull request failed: %v", err)
	}
	// The team squashes the batch into one new commit, which does not hold the
	// landing commit.
	if err := os.WriteFile(filepath.Join(f.teamClone, "squash-item.txt"), []byte("implemented squash-item\n"), 0o600); err != nil {
		t.Fatalf("write the squash commit: %v", err)
	}
	testharness.RunPipelineGit(t, f.teamClone, "add", "squash-item.txt")
	testharness.RunPipelineGit(t, f.teamClone, "commit", "-m", "the batch, squashed")
	testharness.RunPipelineGit(t, f.teamClone, "push", "origin", "HEAD:refs/heads/"+team)
	squash := testharness.GitLine(t, f.teamClone, "rev-parse", "HEAD")
	if _, err := testharness.AttemptPipelineGit(f.repository, "merge-base", "--is-ancestor", run.TeamMerge.LandingCommit, squash); err == nil {
		t.Fatal("the squash commit holds the landing commit; expected a commit that does not")
	}
	f.forge.set(func(g *forge) { g.request.State, g.request.Merged, g.request.MergeCommit = "MERGED", true, squash })
	settled := f.settle(t)
	if len(settled) != 1 || settled[0].Found != orchestrator.TeamPullRequestMerged || settled[0].Failure != "" || len(settled[0].Items) != 1 || !settled[0].Items[0].Settled {
		t.Fatalf("settling the squash-merged pull request gave %#v; expected its item settled", settled)
	}
	if !tracker.Closed || !strings.Contains(tracker.CloseReason, run.TeamMerge.LandingCommit) {
		t.Fatalf("the item closed = %t with reason %q; expected it closed", tracker.Closed, tracker.CloseReason)
	}
	if after := f.runOf(t, "squash-item"); after.WaitingForTeamMerge() || !after.TeamMerge.Found() || after.TeamMerge.FoundOn != squash {
		t.Fatalf("the run's wait is %#v; expected it ended with the squash commit %s recorded", after.TeamMerge, squash)
	}
}

// caseConflict is step 15.
func caseConflict(t *testing.T) {
	f := newFlow(t)
	f.land(t, "conflicting-item")
	// The team adds the same file with other content, which cannot be merged.
	if err := os.WriteFile(filepath.Join(f.teamClone, "conflicting-item.txt"), []byte("the team's own version\n"), 0o600); err != nil {
		t.Fatalf("write the team's change: %v", err)
	}
	testharness.RunPipelineGit(t, f.teamClone, "add", "conflicting-item.txt")
	testharness.RunPipelineGit(t, f.teamClone, "commit", "-m", "the team writes the same file")
	testharness.RunPipelineGit(t, f.teamClone, "push", "origin", "HEAD:refs/heads/"+team)

	before := testharness.PublishedCommit(t, f.repository, target)
	checks, reviews := f.checks.count(), len(f.provider.RequestsForRole(domain.RoleReviewer))
	outcome, err := f.updater.Update(context.Background())
	if err != nil || outcome.Landed != "" || !slices.Equal(outcome.Conflict, []string{"conflicting-item.txt"}) || outcome.Waiting == "" {
		t.Fatalf("the update gave %#v, error %v; expected a conflict in conflicting-item.txt with nothing landed", outcome, err)
	}
	if testharness.PublishedCommit(t, f.repository, target) != before || f.checks.count() != checks || len(f.provider.RequestsForRole(domain.RoleReviewer)) != reviews {
		t.Fatal("a merge that conflicts moved the target branch or was checked or reviewed")
	}
	told := f.toldTheDevelopmentManager(t)
	if len(told) != 1 || !strings.Contains(told[0].Message, "conflicting-item.txt") || !strings.Contains(told[0].Message, "conflicting-item") {
		t.Fatalf("the development manager's list holds %#v; expected one entry naming the path and the item", told)
	}

	// Work keeps landing on the target branch meanwhile, and she is not told
	// twice of the same conflict.
	f.land(t, "carries-on")
	if landed := testharness.PublishedCommit(t, f.repository, target); landed == before {
		t.Fatal("an item could not land while the update waited on a conflict")
	}
	again, err := f.updater.Update(context.Background())
	if err != nil || again.Landed != "" || len(again.Conflict) != 1 || len(f.toldTheDevelopmentManager(t)) != 1 {
		t.Fatalf("the second update gave %#v, error %v with %d entries on her list; expected the same conflict, told once", again, err, len(f.toldTheDevelopmentManager(t)))
	}
}

// steppedClock is a clock the test moves by hand, so a hold's expiry is reached
// without waiting for it.
type steppedClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *steppedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *steppedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
}

// caseHold is step 16.
func caseHold(t *testing.T) {
	f := newFlow(t)
	holds, err := runstate.NewLandingHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("open the landing holds: %v", err)
	}
	clock := &steppedClock{}
	hold, err := holds.Place(target, "an outside tool is building a batch", 10*time.Minute, clock.Now())
	if err != nil {
		t.Fatalf("place the hold: %v", err)
	}

	pipeline, tracker := f.pipelineFor(t, "held-item")
	provider := pipeline.Backend.(*orchestratortest.Backend)
	pipeline.LandingHolds, pipeline.Clock = holds, clock
	before := testharness.PublishedCommit(t, f.repository, target)
	checksBefore := f.checks.count()
	waits := 0
	// Each time the run waits on the hold, nothing has landed and the run's
	// checks and review are already done. The wait ends when the clock passes
	// the hold's expiry; nobody releases it.
	pipeline.Sleep = func(_ context.Context, d time.Duration) error {
		waits++
		if now := testharness.PublishedCommit(t, f.repository, target); now != before {
			t.Errorf("%s moved to %s while the hold stood; expected it left at %s", target, now, before)
		}
		if f.checks.count() == checksBefore || len(provider.RequestsForRole(domain.RoleReviewer)) == 0 {
			t.Errorf("the run waited with %d check stage(s) and %d review(s) done; expected both finished before it waited on the hold", f.checks.count()-checksBefore, len(provider.RequestsForRole(domain.RoleReviewer)))
		}
		if _, held, _ := holds.Standing(target, clock.Now()); !held {
			t.Error("the run waited on a hold that no longer stands")
		}
		clock.advance(d)
		return nil
	}
	outcome, err := pipeline.Run(context.Background(), "held-item")
	if err != nil || outcome.Integration == nil {
		t.Fatalf("running the held item gave %#v, error %v; expected it landed once the hold ended", outcome, err)
	}
	if waits == 0 {
		t.Fatal("the run never waited on the hold; expected it to")
	}
	if after := testharness.PublishedCommit(t, f.repository, target); after == before {
		t.Fatalf("%s did not move after the hold expired", target)
	}
	if _, held, err := holds.Standing(target, clock.Now()); err != nil || held {
		t.Fatalf("the hold stands after its expiry %s (held %t, error %v); expected it ended on its own", hold.Until, held, err)
	}
	if !tracker.Claimed {
		t.Fatal("the held item was never claimed")
	}
}

// caseLineComment is step 17.
func caseLineComment(t *testing.T) {
	f := newFlow(t)
	touchesA, _ := f.land(t, "touches-a")
	touchesB, _ := f.land(t, "touches-b")
	f.opens(touchesA.TeamMerge.LandingCommit, touchesB.TeamMerge.LandingCommit)
	if _, err := f.track(t, false, "touches-a", "touches-b"); err != nil {
		t.Fatalf("recording the pull request failed: %v", err)
	}
	at := touchesB.TeamMerge.Since.Add(10 * time.Minute).UTC().Truncate(time.Second)
	f.forge.set(func(g *forge) {
		g.feedback = publish.FeedbackReading{
			LineComments: []publish.FeedbackComment{{ID: 21, Author: "grace", Body: "This leaks a file handle.", At: at, Path: "touches-a.txt", Line: 1}},
			Comments:     []publish.FeedbackComment{{ID: 31, Author: "linus", Body: "Why this approach?", At: at.Add(time.Minute)}},
		}
	})
	before, err := f.store.Recorded()
	if err != nil {
		t.Fatalf("list the runs: %v", err)
	}
	settled, err := f.reconciler(t).TakeUpTeamFeedback(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Requested != 1 || settled[0].Listed != 1 || settled[0].Failure != "" {
		t.Fatalf("reading feedback gave %#v (error %v); expected one line comment and one ordinary comment listed", settled, err)
	}
	var lineEntry, plainEntry *report.Report
	for _, each := range f.toldTheDevelopmentManager(t) {
		switch {
		case strings.Contains(each.Message, "This leaks a file handle."):
			lineEntry = &each
		case strings.Contains(each.Message, "Why this approach?"):
			plainEntry = &each
		}
	}
	if lineEntry == nil || !strings.Contains(lineEntry.Message, "touches-a") || strings.Contains(lineEntry.Message, "touches-b") {
		t.Fatalf("the entry for the line comment is %#v; expected it to name touches-a, which touched touches-a.txt, and not touches-b", lineEntry)
	}
	if plainEntry == nil || strings.Contains(plainEntry.Message, "touches-a") || strings.Contains(plainEntry.Message, "touches-b") {
		t.Fatalf("the entry for the ordinary comment is %#v; expected it to name no item", plainEntry)
	}
	if after, err := f.store.Recorded(); err != nil || len(after) != len(before) {
		t.Fatalf("there were %d runs and now there are %d (error %v); expected the feedback to start none", len(before), len(after), err)
	}
}

// attemptBudget is the item's attempt budget as the decision spends it, counting
// what is spent so the step can say one attempt went.
type attemptBudget struct{ spent []string }

func (b *attemptBudget) RecordFeedbackRepair(_ context.Context, item, runID string, _ time.Time) (runstate.TriageCounters, error) {
	b.spent = append(b.spent, item+"@"+runID)
	return runstate.TriageCounters{}, nil
}

type noHold struct{}

func (noHold) Held() (runstate.IntakeHold, bool, error) { return runstate.IntakeHold{}, false, nil }
func (noHold) DischargedGates() (map[string][]string, error) {
	return nil, nil
}

// caseFeedbackRepair is step 18.
func caseFeedbackRepair(t *testing.T) {
	f := newFlow(t)
	const id = "repair-item"
	pipeline, tracker := f.pipelineFor(t, id)
	outcome, err := pipeline.Run(context.Background(), id)
	if err != nil || outcome.Integration == nil {
		t.Fatalf("running %s gave %#v, error %v; expected its change landed", id, outcome, err)
	}
	landed := testharness.LoadRun(t, f.store, outcome.RunID)
	f.opens(landed.TeamMerge.LandingCommit)
	if _, err := f.track(t, false, id); err != nil {
		t.Fatalf("recording the pull request failed: %v", err)
	}
	at := landed.TeamMerge.Since.Add(10 * time.Minute).UTC().Truncate(time.Second)
	f.forge.set(func(g *forge) {
		g.feedback = publish.FeedbackReading{
			LineComments: []publish.FeedbackComment{{ID: 21, Author: "grace", Body: "This leaks a file handle.", At: at, Path: id + ".txt", Line: 1}},
		}
	})
	if settled, err := f.reconciler(t).TakeUpTeamFeedback(context.Background()); err != nil || len(settled) != 1 || settled[0].Requested != 1 || settled[0].Failure != "" {
		t.Fatalf("reading feedback gave %#v (error %v); expected the line comment listed", settled, err)
	}

	// The decision is recorded against the entry, and spends one attempt.
	budget := &attemptBudget{}
	var selections []runstate.Selection
	repaired := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, id+".txt"), []byte("repaired "+id+"\n"), 0o600)
	}, approve)
	second := testharness.Automatic(pipeline, repaired)
	second.Tracker, second.Backend = tracker, repaired
	repairer := orchestrator.TeamFeedbackRepairer{
		Runs: f.store, Items: f.trackers, Feedback: f.forge, Budget: budget, Intake: noHold{}, Gates: noHold{}, Capacity: 1,
		Start: func(ctx context.Context, item string, selection runstate.Selection) (orchestrator.Outcome, error) {
			selections = append(selections, selection)
			second.Selection = selection
			return second.Run(ctx, item)
		},
	}
	decided, err := repairer.Decide(context.Background(), orchestrator.TeamFeedbackRepairRequest{
		PullRequest: requestNumber, FeedbackID: "line-comment-21", WorkItemID: id, Reason: "the handle leak is a real defect",
		DecidedBy: "Development Manager", Conversation: "chat-dm", Turn: 1,
	})
	if err != nil || decided.RunID != landed.RunID || len(budget.spent) != 1 {
		t.Fatalf("deciding gave %#v (error %v) and spent %v; expected the decision on run %s and one attempt spent", decided, err, budget.spent, landed.RunID)
	}
	// An entry is answered once.
	if _, err := repairer.Decide(context.Background(), orchestrator.TeamFeedbackRepairRequest{
		PullRequest: requestNumber, FeedbackID: "line-comment-21", WorkItemID: id, Reason: "again", DecidedBy: "Development Manager", Conversation: "chat-dm", Turn: 2,
	}); err == nil || !strings.Contains(err.Error(), "already answered") || len(budget.spent) != 1 {
		t.Fatalf("a second decision on the same entry gave error %v and spent %v; expected it refused as already answered, spending nothing", err, budget.spent)
	}

	before := testharness.PublishedCommit(t, f.repository, target)
	carried, err := repairer.CarryOut(context.Background())
	if err != nil || len(carried) != 1 || !carried[0].Started || carried[0].Failure != "" || carried[0].Outcome == nil || carried[0].Outcome.Integration == nil {
		t.Fatalf("carrying out gave %#v (error %v); expected one new run started and landed", carried, err)
	}
	if len(selections) != 1 || !strings.Contains(selections[0].Reason, "line-comment-21") || !strings.Contains(selections[0].Reason, "#41") {
		t.Fatalf("the new run's selection is %#v; expected it to name the entry it answers", selections)
	}
	if carried[0].NewRunID == landed.RunID {
		t.Fatalf("the new run is the run that landed the item; expected a run of its own")
	}
	if after := testharness.LoadRun(t, f.store, landed.RunID); after.Integration == nil || after.TeamMerge == nil {
		t.Fatalf("the earlier run is now %#v; expected it left ended as integrated", after)
	}
	landing := testharness.PublishedCommit(t, f.repository, target)
	if landing == before {
		t.Fatalf("%s is still at %s; expected the repair landed on it", target, before)
	}
	if got := testharness.GitLine(t, f.repository, "show", landing+":"+id+".txt"); got != "repaired "+id {
		t.Fatalf("%s.txt on %s holds %q; expected the repair", id, target, got)
	}
	// The tracked pull request picks the new landing commit up, and the item
	// stays waiting for the team's merge.
	newRun := testharness.LoadRun(t, f.store, carried[0].NewRunID)
	if !newRun.WaitingForTeamMerge() || newRun.TeamMerge == nil {
		t.Fatalf("the new run's wait is %#v; expected it waiting for the team's merge", newRun.TeamMerge)
	}
	if tracker.Closed {
		t.Fatal("the item closed when its repair landed; expected it to stay open, waiting for the team's merge")
	}
	f.opens(landed.TeamMerge.LandingCommit, newRun.TeamMerge.LandingCommit)
	settled := f.settle(t)
	if len(settled) != 1 || settled[0].Found != orchestrator.TeamPullRequestOpen || settled[0].Failure != "" {
		t.Fatalf("settling the open pull request gave %#v; expected it open", settled)
	}
	if pull := testharness.LoadRun(t, f.store, carried[0].NewRunID).TeamMerge.PullRequest; pull == nil || pull.Number != requestNumber {
		t.Fatalf("the new run's record of the pull request is %#v; expected #%d to have picked the new landing commit up", pull, requestNumber)
	}
	if tracker.Closed {
		t.Fatal("the item closed while its pull request is open; expected it still waiting for the team's merge")
	}

}

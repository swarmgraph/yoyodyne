package twofactory

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These checks run the harness's real pipeline and reconciler on factory A's
// home against the world's shared remote, with a fake forge, tracker and role
// backend from the harness's test packages. They are about what a factory does
// with the pull requests it publishes under human merge approval; the behaviour
// itself is held to account in internal/orchestrator/publicationtests.

// publisher is factory A's side of those checks: its clone, worktree root and
// run store, and the forge its pull requests are opened on.
type publisher struct {
	repository   string
	worktreeRoot string
	store        *runstate.Store
	forge        *orchestratortest.Forge
	// opened is how many pull requests the forge has numbered so far.
	opened int
}

// publishedItem is one work item the factory has taken through to a published
// change that waits for a person: the pipeline that ran it, which a repair
// round is continued through, and what it was run against.
type publishedItem struct {
	tracker  *orchestratortest.Tracker
	provider *orchestratortest.Backend
	pipeline orchestrator.Pipeline
	run      runstate.State
}

// newPublisher prepares factory A's clone the way the pipeline's own test
// repository is prepared (a committer, no background maintenance, the design
// document the pipeline reads), and pushes that to the shared remote so the
// remote's main is the clone's main.
func newPublisher(t *testing.T, w *world) *publisher {
	t.Helper()
	repository := w.a.clone
	git(t, repository, "config", "user.name", "Yoyodyne Test")
	git(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	testharness.DisablePipelineMaintenance(t, repository)
	t.Cleanup(func() { testharness.RemoveLinkedPipelineWorktrees(t, repository) })
	writeFile(t, filepath.Join(repository, "docs", "design.md"), "design content\n")
	git(t, repository, "add", ".")
	git(t, repository, "commit", "--quiet", "-m", "the design the pipeline reads")
	git(t, repository, "push", "--quiet", "origin", "main")
	if local, remote := git(t, repository, "rev-parse", "main"), git(t, w.remote, "rev-parse", "main"); local != remote {
		t.Fatalf("factory A's main is %s and the shared remote's main is %s; expected them to be the same after pushing", local, remote)
	}
	store, err := runstate.NewStore(w.a.stateRoot(), product)
	if err != nil {
		t.Fatalf("open factory A's run store: %v", err)
	}
	return &publisher{
		repository:   repository,
		worktreeRoot: w.a.worktreeRoot,
		store:        store,
		forge:        &orchestratortest.Forge{Remote: w.remote},
	}
}

// publish runs one work item under human merge approval, so its approved change
// is published as a pull request and the run ends waiting for a person.
func (p *publisher) publish(t *testing.T, itemID, runID string) *publishedItem {
	t.Helper()
	// The fake forge gives every request it opens the number it holds, so each
	// item is given its own and two waiting requests are told apart.
	p.opened++
	p.forge.Number = p.opened
	item := &publishedItem{tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: itemID, Title: "Task " + itemID, Status: "open"}}}
	attempt := 0
	item.provider = orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempt++
		// What each item writes is its own, so a change a person has merged for
		// one item does not leave another's with nothing to publish.
		content := "implemented " + itemID + "\n"
		if attempt > 1 {
			content = "implemented " + itemID + ", with the flag renamed\n"
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(content), 0o600)
	}, testharness.ApproveVerdict)
	pipeline := testharness.Publishing(testharness.Automatic(testharness.NewSharedPipeline(t, p.repository, p.worktreeRoot, p.store, item.tracker, item.provider, []string{"exit 0"}), item.provider), p.forge)
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	// The pipeline's configuration names the product the world's stores are opened for.
	pipeline.Config.Product.ID = product
	pipeline.NewRunID = func() (string, error) { return runID, nil }
	item.pipeline = pipeline

	outcome, err := pipeline.Run(context.Background(), itemID)
	if err != nil {
		t.Fatalf("running %s on factory A failed: %v; expected the run to publish its change", itemID, err)
	}
	if outcome.PullRequest == nil || outcome.Integration != nil || outcome.WorkItemClosed {
		t.Fatalf("running %s gave %#v; expected the change published as a pull request, nothing promoted and the item still open", itemID, outcome)
	}
	item.run = testharness.LoadRun(t, p.store, outcome.RunID)
	return item
}

// standing is the status reading over factory A's run store, which is what
// `yoyo status` renders from; only the runs are wired, so nothing else on it is
// asserted.
func (p *publisher) standing() readmodel.Standing {
	return readmodel.ReadStanding(context.Background(), readmodel.Sources{Runs: p.store, Capacity: 1})
}

// checkWaitingForAPerson is check 01.
func checkWaitingForAPerson(t *testing.T, w *world) {
	p := newPublisher(t, w)

	// A change published under human merge approval waits for a person.
	merged := p.publish(t, "shared-merged", "run-00000000000000000000000000000a01")
	run := merged.run
	if run.PullRequest == nil || run.PersonMerge == nil || !run.WaitingForPersonMerge() || run.PersonMerge.Number != run.PullRequest.Number {
		t.Fatalf("the run's wait is %#v; expected it to be waiting for a person to merge its own pull request", run.PersonMerge)
	}
	if !run.Status.Terminal() || run.HoldsDeveloperSlot() {
		t.Fatalf("the run is %s and holds a developer slot: %t; expected it to be over and to hold no developer slot", run.Status, run.HoldsDeveloperSlot())
	}
	if merged.tracker.Closed || merged.tracker.Reopened || !merged.tracker.Claimed {
		t.Fatalf("the item's tracker calls are %v; expected the item still claimed and neither closed nor given back", merged.tracker.Calls)
	}
	want := "waiting for a person to merge pull request #" + strconv.Itoa(run.PullRequest.Number)
	if !strings.Contains(merged.tracker.Notes, want) {
		t.Fatalf("the item's notes are %q; expected them to say %q", merged.tracker.Notes, want)
	}
	var line string
	for _, entry := range p.standing().NeedsHuman {
		if entry.ID == run.RunID {
			line = entry.What()
		}
	}
	if !strings.Contains(line, want) || !strings.Contains(line, merged.run.WorkItemID) {
		t.Fatalf("the status line says %q; expected it to name %s and say %q", line, merged.run.WorkItemID, want)
	}

	// The forge reports it merged: the person merged by fast-forward, so the
	// remote's main is the change's own head. The settle step closes the item.
	git(t, p.repository, "push", "origin", run.PullRequest.HeadCommit+":refs/heads/main")
	mergedForge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{
		Number: run.PullRequest.Number, URL: run.PullRequest.URL, State: "MERGED", Merged: true, MergeCommit: run.PullRequest.HeadCommit,
	}}
	settled := p.settle(t, merged.tracker, mergedForge, &closers{})
	if len(settled) != 1 || !settled[0].Settled || settled[0].Found != orchestrator.PersonMergeMerged || settled[0].Failure != "" {
		t.Fatalf("settling the merged pull request gave %#v; expected the merge found and settled without a failure", settled)
	}
	if !merged.tracker.Closed || !strings.Contains(merged.tracker.CloseReason, run.PullRequest.HeadCommit) {
		t.Fatalf("after the merge the item closed = %t with reason %q; expected it closed naming merge commit %s", merged.tracker.Closed, merged.tracker.CloseReason, run.PullRequest.HeadCommit)
	}
	after := testharness.LoadRun(t, p.store, run.RunID)
	if after.WaitingForPersonMerge() || after.PersonMerge == nil || !after.PersonMerge.Merged() {
		t.Fatalf("after the merge the run's wait is %#v; expected it ended on the merge", after.PersonMerge)
	}

	// A second item whose pull request is closed without merging goes back to
	// the Lead Product Manager, with its branch and checkout kept.
	closedItem := p.publish(t, "shared-closed", "run-00000000000000000000000000000a02")
	closedRun := closedItem.run
	closedAt := time.Date(2026, 10, 30, 2, 15, 0, 0, time.UTC)
	closedForge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{Number: closedRun.PullRequest.Number, URL: closedRun.PullRequest.URL, State: "CLOSED"}}
	settled = p.settle(t, closedItem.tracker, closedForge, &closers{closer: publish.Closer{By: "octo-reviewer", At: closedAt}})
	if len(settled) != 1 || !settled[0].Settled || settled[0].Found != orchestrator.PersonMergeClosed || settled[0].Failure != "" {
		t.Fatalf("settling the closed pull request gave %#v; expected the closing found and settled without a failure", settled)
	}
	if !closedItem.tracker.Reopened || closedItem.tracker.Closed {
		t.Fatalf("after the closing the tracker calls are %v; expected the item given back, not closed", closedItem.tracker.Calls)
	}
	when := closedAt.Local().Format("2006-01-02 15:04 MST")
	for _, phrase := range []string{"closed without being merged, by octo-reviewer at " + when, "Lead Product Manager", closedRun.Branch + " and the run's checkout", "nothing was deleted"} {
		if !strings.Contains(closedItem.tracker.ReopenReason, phrase) {
			t.Errorf("the note on the item is %q; expected it to say %q", closedItem.tracker.ReopenReason, phrase)
		}
	}
	if parking := closedItem.tracker.Item.Parking; !parking.Parked() || !strings.Contains(parking.Reason(), "octo-reviewer") {
		t.Errorf("the item's parking is %q; expected it parked for the Lead Product Manager naming octo-reviewer", parking)
	}
	closedAfter := testharness.LoadRun(t, p.store, closedRun.RunID)
	if closedAfter.WaitingForPersonMerge() || closedAfter.PersonMerge == nil || !closedAfter.PersonMerge.Returned() || closedAfter.PersonMerge.ClosedBy != "octo-reviewer" {
		t.Errorf("after the closing the run's wait is %#v; expected it ended on the closing, naming octo-reviewer", closedAfter.PersonMerge)
	}
	if kept := testharness.PublishedCommit(t, w.remote, closedRun.Branch); kept != closedRun.PullRequest.HeadCommit {
		t.Errorf("branch %s on the shared remote is at %q; expected it kept at %s", closedRun.Branch, kept, closedRun.PullRequest.HeadCommit)
	}
	if _, err := os.Stat(closedRun.WorktreePath); err != nil {
		t.Errorf("the run's checkout %s: %v; expected it kept", closedRun.WorktreePath, err)
	}
}

// settle runs the harness's settle step for waiting pull requests.
func (p *publisher) settle(t *testing.T, tracker *orchestratortest.Tracker, forge orchestrator.ReconcilePullRequests, closed orchestrator.ReconcileClosers) []orchestrator.PersonMergeSettlement {
	t.Helper()
	settled, err := orchestrator.Reconciler{
		Tracker:   tracker,
		Worktrees: testharness.NewObserver(t, p.repository, p.worktreeRoot),
		Store:     p.store,
		Publisher: forge,
		Closers:   closed,
	}.SettlePersonMerges(context.Background())
	if err != nil {
		t.Fatalf("settling the pull requests waiting for a person failed: %v; expected the step to run", err)
	}
	return settled
}

// closers answers who closed a pull request, for every request alike.
type closers struct{ closer publish.Closer }

func (c *closers) Closer(_ context.Context, number int) (publish.Closer, error) {
	closer := c.closer
	closer.Number = number
	return closer, nil
}

// feedbackByRequest answers what people have said on each pull request, by its
// number; a request with nothing scripted has nothing said on it.
type feedbackByRequest map[int]publish.FeedbackReading

func (f feedbackByRequest) Feedback(_ context.Context, number int) (publish.FeedbackReading, error) {
	reading := f[number]
	reading.Number = number
	if reading.State == "" {
		reading.State = "OPEN"
	}
	return reading, nil
}

// collectedReports is the development manager's list as the check writes it.
type collectedReports struct{ reports []report.Report }

func (c *collectedReports) Append(reported report.Report) error {
	c.reports = append(c.reports, reported)
	return nil
}

// reconciler is the sweep that reads feedback on the pull requests waiting for
// a person, with one developer slot free and three repair attempts allowed.
func (p *publisher) reconciler(t *testing.T, tracker *orchestratortest.Tracker, reader orchestrator.ReconcileFeedback, reports orchestrator.ReportCollector) orchestrator.Reconciler {
	t.Helper()
	return orchestrator.Reconciler{
		Tracker:     tracker,
		Worktrees:   testharness.NewObserver(t, p.repository, p.worktreeRoot),
		Store:       p.store,
		Feedback:    reader,
		Reports:     reports,
		Capacity:    1,
		RepairLimit: 3,
		HostsRuns:   true,
	}
}

// takeUp reads feedback and returns the settlement of the one request asked
// about.
func (p *publisher) takeUp(t *testing.T, tracker *orchestratortest.Tracker, reader orchestrator.ReconcileFeedback, reports orchestrator.ReportCollector, number int) orchestrator.FeedbackSettlement {
	t.Helper()
	settled, err := p.reconciler(t, tracker, reader, reports).TakeUpFeedback(context.Background())
	if err != nil {
		t.Fatalf("reading feedback on the waiting pull requests failed: %v; expected the step to run", err)
	}
	for _, each := range settled {
		if each.Number == number {
			return each
		}
	}
	t.Fatalf("reading feedback gave %#v; expected a settlement for pull request #%d", settled, number)
	return orchestrator.FeedbackSettlement{}
}

// checkFeedback is check 02.
func checkFeedback(t *testing.T, w *world) {
	p := newPublisher(t, w)
	reports := &collectedReports{}

	// Requested changes and a comment on a line, written by people, start a
	// repair round on the same run.
	repaired := p.publish(t, "shared-repair", "run-00000000000000000000000000000b01")
	before := repaired.run
	number := before.PullRequest.Number
	if before.PullRequest.PushedAt == nil {
		t.Fatal("the published pull request does not say when its head was pushed; expected it to, so feedback older than the head can be told apart")
	}
	at := before.PullRequest.PushedAt.Add(5 * time.Minute).UTC().Truncate(time.Second)
	reader := feedbackByRequest{number: {
		Reviews:      []publish.FeedbackReview{{ID: 11, Author: "ada", State: publish.ReviewChangesRequested, Body: "Please rename the flag.", At: at}},
		LineComments: []publish.FeedbackComment{{ID: 21, Author: "grace", Body: "This leaks a file handle.", At: at.Add(time.Minute), Path: "feature.txt", Line: 1}},
	}}
	settled := p.takeUp(t, repaired.tracker, reader, reports, number)
	if !settled.RepairStarted || settled.New != 2 || settled.Failure != "" {
		t.Fatalf("reading the review and the line comment gave %#v; expected two new pieces of feedback and a repair round started", settled)
	}
	live := testharness.LoadRun(t, p.store, before.RunID)
	if live.RunID != before.RunID || live.Status != runstate.StatusRunning || live.Phase != runstate.PhaseDeveloping ||
		live.RepairAttempts != before.RepairAttempts+1 || live.WorktreePath != before.WorktreePath || live.Branch != before.Branch {
		t.Fatalf("after the review the run is %s %s with %d repair attempts; expected the same run live at its developer with %d", live.Status, live.Phase, live.RepairAttempts, before.RepairAttempts+1)
	}
	if live.ForgeFeedback == nil || len(live.ForgeFeedback.Items) != 2 {
		t.Fatalf("the repair input is %#v; expected the review and the line comment", live.ForgeFeedback)
	}
	kinds := map[string]string{}
	for _, input := range live.ForgeFeedback.Items {
		kinds[input.Author] = string(input.Kind)
	}
	if kinds["ada"] != string(runstate.FeedbackChangesRequested) || kinds["grace"] != string(runstate.FeedbackLineComment) {
		t.Fatalf("the repair input's kinds by author are %v; expected ada's requested change and grace's line comment", kinds)
	}
	if !strings.Contains(repaired.tracker.Notes, "repair round") || !strings.Contains(repaired.tracker.Notes, "Please rename the flag.") {
		t.Fatalf("the item's notes are %q; expected them to say a repair round started and quote the feedback", repaired.tracker.Notes)
	}

	// The round is carried out on the same run and the same pull request: the
	// developer is handed the feedback, and the repaired change is reviewed
	// before it is pushed.
	developerBefore := developerRequests(repaired.provider)
	repairer := p.reconciler(t, repaired.tracker, reader, reports)
	repairer.Continue = repaired.pipeline.Continue
	hosted, err := repairer.ContinueFeedbackRepairs(context.Background())
	if err != nil || len(hosted) != 1 || !hosted[0].Continued || hosted[0].Failure != "" {
		t.Fatalf("carrying out the repair round gave %#v, error %v; expected the one round continued", hosted, err)
	}
	developer := developerRequests(repaired.provider)
	if len(developer) != len(developerBefore)+1 {
		t.Fatalf("the developer was invoked %d more time(s); expected one repair attempt", len(developer)-len(developerBefore))
	}
	prompt := developer[len(developer)-1].Prompt
	for _, phrase := range []string{"ada", "Please rename the flag.", "grace", "This leaks a file handle."} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("the repair prompt does not say %q; expected the feedback and who wrote it:\n%s", phrase, prompt)
		}
	}
	done := testharness.LoadRun(t, p.store, before.RunID)
	if !done.WaitingOnAPersonsMerge() || done.ForgeFeedback != nil || done.ReviewDecision != runstate.ReviewApprove ||
		done.PullRequest.Number != number || done.PullRequest.HeadCommit == before.PullRequest.HeadCommit {
		t.Fatalf("after the round the run is %s %s with review %q and head %s; expected the repaired head approved, published on pull request #%d, and waiting for a person again",
			done.Status, done.Phase, done.ReviewDecision, done.PullRequest.HeadCommit, number)
	}

	// An ordinary comment on another item's pull request goes on the
	// development manager's list and starts no repair round. Nothing is said on
	// the first item's request now, so only the second is read for anything.
	commented := p.publish(t, "shared-comment", "run-00000000000000000000000000000b02")
	commentedNumber := commented.run.PullRequest.Number
	reader[commentedNumber] = publish.FeedbackReading{Comments: []publish.FeedbackComment{{
		ID: 31, Author: "linus", Body: "Why this approach rather than a flag?", At: commented.run.PullRequest.PushedAt.Add(2 * time.Minute).UTC().Truncate(time.Second),
	}}}
	listed := p.takeUp(t, commented.tracker, reader, reports, commentedNumber)
	if listed.RepairStarted || listed.RepairStopped || listed.Listed != 1 || listed.Failure != "" {
		t.Fatalf("reading the ordinary comment gave %#v; expected it listed once and no repair round", listed)
	}
	if len(reports.reports) != 1 || reports.reports[0].For != domain.RoleDevelopmentManager ||
		!strings.Contains(reports.reports[0].Message, "linus") || !strings.Contains(reports.reports[0].Message, "Why this approach") {
		t.Fatalf("the development manager's list holds %#v; expected one report for her quoting linus's comment", reports.reports)
	}
	run := testharness.LoadRun(t, p.store, commented.run.RunID)
	if !run.WaitingOnAPersonsMerge() || run.RepairAttempts != commented.run.RepairAttempts || run.ForgeFeedback != nil {
		t.Fatalf("after the comment the run is %s %s with %d repair attempts; expected it still waiting with nothing spent", run.Status, run.Phase, run.RepairAttempts)
	}
	if again := p.takeUp(t, commented.tracker, reader, reports, commentedNumber); again.New != 0 || len(reports.reports) != 1 {
		t.Fatalf("reading the same comment again gave %#v with %d report(s) on the list; expected nothing new and the comment listed once", again, len(reports.reports))
	}
}

// developerRequests are the requests the developer role was invoked with.
func developerRequests(provider *orchestratortest.Backend) []backend.RunRequest {
	var developer []backend.RunRequest
	for _, request := range provider.Requests {
		if request.Role == domain.RoleDeveloper {
			developer = append(developer, request)
		}
	}
	return developer
}

package publicationtests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/factory"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive yoyodyne-ifd.429.100: what people say on a pull request waiting
// for a person to merge it. A requested change or a comment on a line starts a
// repair round on the same run, whose repaired change is checked and reviewed
// again before it is pushed; an ordinary comment goes on the development
// manager's list; an approval is recorded as information and nothing more.
// What a factory of the product wrote is told apart by its mark and the run's
// list of what it wrote, never by the account (yoyodyne-ifd.429.125).

// The forge account the fixture's harness publishes under. On a project with
// one person it is that person's own account, so what it wrote is read unless
// it is marked or listed as the harness's.
const factoryAccount = "factory-bot"

// marked is a body ending with the hidden line a factory of the run's product
// writes, as the harness's own comments and reviews end.
func (f *feedbackFixture) marked(body string) string {
	return body + "\n\n" + factory.ProductMark(f.run.ProductID)
}

// feedbackFixture is a run under a merge setting that leaves the merge to a
// person, driven to the end: its approved change is published and the run is
// over, waiting on its pull request.
type feedbackFixture struct {
	repository   string
	worktreeRoot string
	store        *runstate.Store
	tracker      *orchestratortest.Tracker
	provider     *orchestratortest.Backend
	forge        *orchestratortest.Forge
	pipeline     orchestrator.Pipeline
	run          runstate.State
	// ensuredAfter is how many provider requests had been made each time the
	// forge was asked to open or update the request.
	ensuredAfter []int
}

func newFeedbackFixture(t *testing.T) *feedbackFixture {
	t.Helper()
	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	remote := testharness.AddBareRemote(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	fixture := &feedbackFixture{repository: repository, worktreeRoot: worktreeRoot, store: store, tracker: tracker}
	attempt := 0
	fixture.provider = orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempt++
		content := "implemented\n"
		if attempt > 1 {
			content = "implemented, with the flag renamed\n"
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(content), 0o600)
	}, testharness.ApproveVerdict)
	fixture.forge = &orchestratortest.Forge{Remote: remote}
	fixture.forge.OnEnsure = func() { fixture.ensuredAfter = append(fixture.ensuredAfter, len(fixture.provider.Requests)) }
	pipeline := testharness.Publishing(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, fixture.provider, []string{"exit 0"}), fixture.provider), fixture.forge)
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	fixture.pipeline = pipeline

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration != nil || outcome.PullRequest == nil {
		t.Fatalf("outcome = %#v, want the change published and nothing promoted", outcome)
	}
	fixture.run = testharness.LoadRun(t, store, outcome.RunID)
	if !fixture.run.WaitingOnAPersonsMerge() {
		t.Fatalf("run = status %s phase %s stop %s, want it waiting for a person to merge its pull request", fixture.run.Status, fixture.run.Phase, fixture.run.StopClass)
	}
	if fixture.run.PullRequest.PushedAt == nil {
		t.Fatal("the published request does not say when its head was pushed")
	}
	return fixture
}

// after is a moment the given number of minutes after the head was pushed.
func (f *feedbackFixture) after(minutes int) time.Time {
	return f.run.PullRequest.PushedAt.Add(time.Duration(minutes) * time.Minute).UTC().Truncate(time.Second)
}

// waitingListing gives the run store the listing of runs waiting for a person
// to merge that the check reads. The run store gains it from the build that
// holds a published change for a person's merge (yoyodyne-ifd.429.99); this
// test reads the same runs from every record, which test code may.
type waitingListing struct{ *runstate.Store }

func (w waitingListing) WaitingForPersonMerge() ([]runstate.State, error) {
	recorded, err := w.Store.Recorded()
	if err != nil {
		return nil, err
	}
	var waiting []runstate.State
	for _, state := range recorded {
		if state.WaitingOnAPersonsMerge() {
			waiting = append(waiting, state)
		}
	}
	return waiting, nil
}

type feedbackReader struct {
	reading publish.FeedbackReading
	err     error
	asked   int
}

func (r *feedbackReader) Feedback(_ context.Context, number int) (publish.FeedbackReading, error) {
	r.asked++
	reading := r.reading
	reading.Number = number
	if reading.State == "" {
		reading.State = "OPEN"
	}
	return reading, r.err
}

type collectedReports struct{ reports []report.Report }

func (c *collectedReports) Append(reported report.Report) error {
	c.reports = append(c.reports, reported)
	return nil
}

func (f *feedbackFixture) reconciler(t *testing.T, reader orchestrator.ReconcileFeedback, reports orchestrator.ReportCollector, capacity, repairLimit int) orchestrator.Reconciler {
	t.Helper()
	return orchestrator.Reconciler{
		Tracker:     f.tracker,
		Worktrees:   testharness.NewObserver(t, f.repository, f.worktreeRoot),
		Store:       waitingListing{f.store},
		Feedback:    reader,
		Reports:     reports,
		Capacity:    capacity,
		RepairLimit: repairLimit,
		HostsRuns:   true,
	}
}

func (f *feedbackFixture) check(t *testing.T, reader orchestrator.ReconcileFeedback, reports orchestrator.ReportCollector, capacity, repairLimit int) orchestrator.FeedbackSettlement {
	t.Helper()
	settled, err := f.reconciler(t, reader, reports, capacity, repairLimit).TakeUpFeedback(context.Background())
	if err != nil {
		t.Fatalf("TakeUpFeedback() error = %v", err)
	}
	if len(settled) != 1 {
		t.Fatalf("TakeUpFeedback() = %#v, want the one request waiting", settled)
	}
	return settled[0]
}

func (f *feedbackFixture) developerRequests() []backend.RunRequest {
	var developer []backend.RunRequest
	for _, request := range f.provider.Requests {
		if request.Role == domain.RoleDeveloper {
			developer = append(developer, request)
		}
	}
	return developer
}

// A review requesting changes starts a repair round on the same run, in its
// kept checkout and against its repair budget, with the feedback handed to the
// developer as evidence naming its author and time. The repaired change is
// checked and approved by the independent reviewer before it is pushed, and
// the feedback is never acted on twice. What the factory's own account wrote
// is left out.
func TestARequestedChangeStartsARepairRoundOnTheSameRun(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	before := fixture.run
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Reviews: []publish.FeedbackReview{
			{ID: 11, Author: "ada", State: publish.ReviewChangesRequested, Body: "Please rename the flag.", At: fixture.after(5), URL: "https://example.invalid/r/11"},
			{ID: 12, Author: factoryAccount, State: publish.ReviewChangesRequested, Body: fixture.marked("the factory's own words"), At: fixture.after(6)},
		},
	}}
	reports := &collectedReports{}

	settled := fixture.check(t, reader, reports, 1, 3)
	if !settled.RepairStarted || settled.New != 1 || settled.Failure != "" {
		t.Fatalf("settlement = %#v, want one piece of feedback and a repair round started", settled)
	}
	live := testharness.LoadRun(t, fixture.store, before.RunID)
	if live.RunID != before.RunID || live.Status != runstate.StatusRunning || live.Phase != runstate.PhaseDeveloping ||
		live.RepairAttempts != before.RepairAttempts+1 || live.WorktreePath != before.WorktreePath || live.Branch != before.Branch {
		t.Fatalf("run = %s %s attempts %d, want the same run live at its developer with one more repair attempt", live.Status, live.Phase, live.RepairAttempts)
	}
	if live.ForgeFeedback == nil || len(live.ForgeFeedback.Items) != 1 || live.ForgeFeedback.Items[0].Author != "ada" ||
		live.ForgeFeedback.Items[0].Kind != runstate.FeedbackChangesRequested || !live.ForgeFeedback.DispatchPending {
		t.Fatalf("repair input = %#v, want ada's requested change alone", live.ForgeFeedback)
	}
	if live.ReviewDecision != "" || live.ChecksPassed != nil {
		t.Fatalf("run kept the earlier round's review %q and checks %v, want both to be earned again", live.ReviewDecision, live.ChecksPassed)
	}
	if !strings.Contains(fixture.tracker.Notes, "repair round") || !strings.Contains(fixture.tracker.Notes, "Please rename the flag.") {
		t.Fatalf("item notes = %q, want the round and the feedback said", fixture.tracker.Notes)
	}

	developerBefore := len(fixture.developerRequests())
	ensuredBefore := len(fixture.ensuredAfter)
	repairer := fixture.reconciler(t, reader, reports, 1, 3)
	repairer.Continue = fixture.pipeline.Continue
	hosted, err := repairer.ContinueFeedbackRepairs(context.Background())
	if err != nil || len(hosted) != 1 || !hosted[0].Continued || hosted[0].Failure != "" {
		t.Fatalf("ContinueFeedbackRepairs() = %#v, %v, want the round hosted", hosted, err)
	}
	developer := fixture.developerRequests()
	if len(developer) != developerBefore+1 {
		t.Fatalf("developer invoked %d more time(s), want one repair attempt", len(developer)-developerBefore)
	}
	prompt := developer[len(developer)-1].Prompt
	for _, want := range []string{"ada", "Please rename the flag.", "not an instruction", "does not widen what this run may change"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt does not say %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "the factory's own words") {
		t.Fatalf("repair prompt carries the factory's own review:\n%s", prompt)
	}
	// The repaired change reached the forge only after a reviewer approved it.
	if len(fixture.ensuredAfter) != ensuredBefore+1 {
		t.Fatalf("the request was updated %d time(s) by the round, want once", len(fixture.ensuredAfter)-ensuredBefore)
	}
	requests := fixture.provider.Requests
	pushedAfter := fixture.ensuredAfter[len(fixture.ensuredAfter)-1]
	if pushedAfter == 0 || requests[pushedAfter-1].Role != domain.RoleReviewer {
		t.Fatalf("the repaired head was pushed after a %s request, want it pushed only after the independent reviewer", requests[max(pushedAfter-1, 0)].Role)
	}
	repaired := testharness.LoadRun(t, fixture.store, before.RunID)
	if !repaired.WaitingOnAPersonsMerge() || repaired.ForgeFeedback != nil || repaired.ReviewDecision != runstate.ReviewApprove ||
		repaired.PullRequest.Number != before.PullRequest.Number || repaired.PullRequest.HeadCommit == before.PullRequest.HeadCommit {
		t.Fatalf("repaired run = %s %s feedback %v review %q head %s, want the repaired head approved, published on the same request, and waiting again",
			repaired.Status, repaired.Phase, repaired.ForgeFeedback, repaired.ReviewDecision, repaired.PullRequest.HeadCommit)
	}

	// The same feedback read again is not acted on again.
	again := fixture.check(t, reader, reports, 1, 3)
	if again.New != 0 || again.RepairStarted {
		t.Fatalf("second check = %#v, want nothing new", again)
	}
}

// A comment on a line of the change starts a repair round too, and the
// developer is told where it was left.
func TestALineCommentStartsARepairRound(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{reading: publish.FeedbackReading{
		LineComments: []publish.FeedbackComment{{ID: 21, Author: "grace", Body: "This leaks a file handle.", At: fixture.after(3), Path: "feature.txt", Line: 1}},
	}}
	settled := fixture.check(t, reader, &collectedReports{}, 1, 3)
	if !settled.RepairStarted {
		t.Fatalf("settlement = %#v, want a repair round started", settled)
	}
	live := testharness.LoadRun(t, fixture.store, fixture.run.RunID)
	item := live.ForgeFeedback.Items[0]
	if item.Kind != runstate.FeedbackLineComment || item.Path != "feature.txt" || item.Line != 1 || item.Author != "grace" {
		t.Fatalf("repair input = %#v, want grace's comment on feature.txt line 1", item)
	}
}

// An ordinary comment is put on the development manager's list as a report
// addressed to her, starts no repair round, and is listed once.
func TestAnOrdinaryCommentGoesOnTheDevelopmentManagersList(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Comments: []publish.FeedbackComment{{ID: 31, Author: "linus", Body: "Why this approach rather than a flag?", At: fixture.after(2)}},
	}}
	reports := &collectedReports{}
	settled := fixture.check(t, reader, reports, 1, 3)
	if settled.RepairStarted || settled.RepairStopped || settled.Listed != 1 {
		t.Fatalf("settlement = %#v, want the comment listed and no repair", settled)
	}
	if len(reports.reports) != 1 || reports.reports[0].For != domain.RoleDevelopmentManager ||
		!strings.Contains(reports.reports[0].Message, "linus") || !strings.Contains(reports.reports[0].Message, "Why this approach") {
		t.Fatalf("reports = %#v, want one for the development manager quoting linus", reports.reports)
	}
	run := testharness.LoadRun(t, fixture.store, fixture.run.RunID)
	if !run.WaitingOnAPersonsMerge() || run.RepairAttempts != fixture.run.RepairAttempts || run.ForgeFeedback != nil {
		t.Fatalf("run = %s %s attempts %d, want it still waiting and nothing spent", run.Status, run.Phase, run.RepairAttempts)
	}
	if again := fixture.check(t, reader, reports, 1, 3); again.New != 0 || len(reports.reports) != 1 {
		t.Fatalf("second check = %#v with %d report(s), want the comment listed once", again, len(reports.reports))
	}
}

// A person's approval on the forge is recorded as information: the run goes on
// waiting, the independent reviewer's verdict on it is unchanged, nothing is
// repaired, and the item is told the approval does not stand in for review.
func TestAnApprovalOnTheForgeIsRecordedAsInformationOnly(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Reviews: []publish.FeedbackReview{{ID: 41, Author: "margaret", State: publish.ReviewApproved, At: fixture.after(4)}},
	}}
	reports := &collectedReports{}
	settled := fixture.check(t, reader, reports, 1, 3)
	if settled.Approvals != 1 || settled.RepairStarted || settled.Listed != 0 || len(reports.reports) != 0 {
		t.Fatalf("settlement = %#v, want the approval recorded and nothing else", settled)
	}
	run := testharness.LoadRun(t, fixture.store, fixture.run.RunID)
	if !run.WaitingOnAPersonsMerge() || run.ReviewDecision != fixture.run.ReviewDecision || run.ReviewSessionID != fixture.run.ReviewSessionID ||
		run.WorkItemTitle != fixture.run.WorkItemTitle {
		t.Fatalf("run = %s review %q by %q, want it waiting with the independent review unchanged", run.Status, run.ReviewDecision, run.ReviewSessionID)
	}
	if run.PullRequest.Feedback == nil || len(run.PullRequest.Feedback.Approvals) != 1 || run.PullRequest.Feedback.Approvals[0].Author != "margaret" {
		t.Fatalf("publication feedback = %#v, want margaret's approval kept", run.PullRequest.Feedback)
	}
	if !strings.Contains(fixture.tracker.Notes, "does not stand in for the independent review") {
		t.Fatalf("item notes = %q, want them to say the approval does not stand in for review", fixture.tracker.Notes)
	}
}

// The owner's comment on a line of the change, written under the very account
// the harness publishes under, is the owner's feedback and starts a repair
// round: the account that wrote it is never the test.
func TestTheOwnersLineCommentUnderThePublishingAccountStartsARepairRound(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{reading: publish.FeedbackReading{
		LineComments: []publish.FeedbackComment{{ID: 23, Author: factoryAccount, Body: "Rename this before it merges.", At: fixture.after(3), Path: "feature.txt", Line: 1}},
	}}
	settled := fixture.check(t, reader, &collectedReports{}, 1, 3)
	if !settled.RepairStarted || settled.New != 1 {
		t.Fatalf("settlement = %#v, want the owner's line comment to start a repair round", settled)
	}
	live := testharness.LoadRun(t, fixture.store, fixture.run.RunID)
	if live.ForgeFeedback == nil || len(live.ForgeFeedback.Items) != 1 || live.ForgeFeedback.Items[0].Author != factoryAccount ||
		live.ForgeFeedback.Items[0].Body != "Rename this before it merges." {
		t.Fatalf("repair input = %#v, want the owner's line comment", live.ForgeFeedback)
	}
}

// Feedback left before the head was pushed is not acted on, and neither is
// anything a factory of this product wrote, however it reaches the reading: a
// marked review and comment the run's record lists, a marked comment whose
// write the forge took after the harness stopped waiting so the record never
// listed it, a listed comment whose mark a person edited out, and a comment
// marked by another factory of the same product on another machine.
func TestFeedbackOlderThanTheHeadOrAFactorysOwnIsIgnored(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	// The harness wrote comment 52, review 53 and comment 54, and the forge
	// answered each; comment 55 it wrote too, but that write's answer was lost.
	recorded := testharness.LoadRun(t, fixture.store, fixture.run.RunID)
	published := *recorded.PullRequest
	for _, id := range []string{runstate.FeedbackID("comment", 52), runstate.FeedbackID("review", 53), runstate.FeedbackID("comment", 54)} {
		published.RecordWrite(id)
	}
	recorded.PullRequest = &published
	if err := fixture.store.Save(recorded); err != nil {
		t.Fatal(err)
	}
	fixture.run = recorded
	elsewhere := factory.Identity{Product: string(fixture.run.ProductID), Machine: "machine-00000000000000000000000000000002", ForgeAccount: "teammate-bot"}
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Reviews: []publish.FeedbackReview{
			{ID: 51, Author: "ada", State: publish.ReviewChangesRequested, Body: "about the old head", At: fixture.after(-10)},
			{ID: 53, Author: factoryAccount, State: publish.ReviewChangesRequested, Body: fixture.marked("the harness's own review"), At: fixture.after(2)},
		},
		LineComments: []publish.FeedbackComment{
			{ID: 56, Author: "teammate-bot", Body: "another factory's note\n\n" + elsewhere.Mark(), At: fixture.after(3), Path: "feature.txt", Line: 1},
		},
		Comments: []publish.FeedbackComment{
			{ID: 52, Author: factoryAccount, Body: fixture.marked("the harness's own comment"), At: fixture.after(2)},
			{ID: 54, Author: factoryAccount, Body: "the harness's own comment, its mark edited out by a person", At: fixture.after(2)},
			{ID: 55, Author: factoryAccount, Body: fixture.marked("a comment whose write timed out after the forge took it"), At: fixture.after(2)},
		},
	}}
	reports := &collectedReports{}
	settled := fixture.check(t, reader, reports, 1, 3)
	if settled.New != 0 || settled.RepairStarted || settled.Listed != 0 || len(reports.reports) != 0 {
		t.Fatalf("settlement = %#v with %d report(s), want nothing acted on", settled, len(reports.reports))
	}
	run := testharness.LoadRun(t, fixture.store, fixture.run.RunID)
	if !run.WaitingOnAPersonsMerge() || run.RepairAttempts != fixture.run.RepairAttempts || run.ForgeFeedback != nil {
		t.Fatalf("run = %s %s attempts %d, want it still waiting and nothing spent", run.Status, run.Phase, run.RepairAttempts)
	}
}

// A mark naming another product is not this product's factory, so what it is
// on is read like anybody's.
func TestAMarkNamingAnotherProductIsRead(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Comments: []publish.FeedbackComment{{ID: 61, Author: "linus", Body: "Quoting another project's bot:\n\n" + factory.ProductMark("someone-else"), At: fixture.after(2)}},
	}}
	reports := &collectedReports{}
	if settled := fixture.check(t, reader, reports, 1, 3); settled.Listed != 1 {
		t.Fatalf("settlement = %#v, want the comment read and listed", settled)
	}
}

// Requested changes on a run whose repair budget is spent stop it in the
// ordinary way: the item is blocked, the run is stopped with the feedback as
// the repair input a decided repair hands back, and it goes to the development
// manager.
func TestARequestedChangeWithTheRepairBudgetSpentStopsTheRunForTheDevelopmentManager(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Reviews: []publish.FeedbackReview{{ID: 61, Author: "ada", State: publish.ReviewChangesRequested, Body: "Please rename the flag.", At: fixture.after(5)}},
	}}
	settled := fixture.check(t, reader, &collectedReports{}, 1, fixture.run.RepairAttempts)
	if !settled.RepairStopped || settled.RepairStarted {
		t.Fatalf("settlement = %#v, want the run stopped on its spent budget", settled)
	}
	stopped := testharness.LoadRun(t, fixture.store, fixture.run.RunID)
	if !stopped.Status.Terminal() || stopped.StopClass != runstate.StopRepairBudget || !stopped.HandedBack() || stopped.ForgeFeedback == nil ||
		stopped.Blocker == "" || !fixture.tracker.Blocked {
		t.Fatalf("run = %s %s blocker %q, item blocked %t; want it stopped on its repair budget with the feedback kept", stopped.Status, stopped.StopClass, stopped.Blocker, fixture.tracker.Blocked)
	}
	if stopped.RepairAttempts != fixture.run.RepairAttempts {
		t.Fatalf("repair attempts = %d, want none spent by a round that was not started", stopped.RepairAttempts)
	}
}

// With no developer slot free nothing is acted on, so the next check finds the
// same feedback and starts the round then.
func TestRequestedChangesWithNoSlotFreeAreLeftForTheNextCheck(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{reading: publish.FeedbackReading{
		Reviews:  []publish.FeedbackReview{{ID: 71, Author: "ada", State: publish.ReviewChangesRequested, Body: "Please rename the flag.", At: fixture.after(5)}},
		Comments: []publish.FeedbackComment{{ID: 72, Author: "linus", Body: "And a question.", At: fixture.after(6)}},
	}}
	reports := &collectedReports{}
	settled := fixture.check(t, reader, reports, 0, 3)
	if settled.RepairStarted || settled.Listed != 0 || len(reports.reports) != 0 || !strings.Contains(settled.Detail, "next check") {
		t.Fatalf("settlement = %#v, want nothing acted on until a slot frees", settled)
	}
	if run := testharness.LoadRun(t, fixture.store, fixture.run.RunID); !run.WaitingOnAPersonsMerge() || run.PullRequest.Feedback != nil {
		t.Fatalf("run = %s with feedback %#v, want it untouched", run.Status, run.PullRequest.Feedback)
	}
	if again := fixture.check(t, reader, reports, 1, 3); !again.RepairStarted || again.Listed != 1 {
		t.Fatalf("next check = %#v, want the round started and the comment listed", again)
	}
}

// A run store that records no run as waiting for a person has nothing to read
// feedback on: the check reads nothing, and never reads every record instead.
func TestACheckWhoseStoreListsNoWaitingRunsReadsNothing(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	reader := &feedbackReader{}
	settled, err := orchestrator.Reconciler{
		Tracker: fixture.tracker, Worktrees: testharness.NewObserver(t, fixture.repository, fixture.worktreeRoot),
		Store: storeWithoutListing{fixture.store}, Feedback: reader,
	}.TakeUpFeedback(context.Background())
	if err != nil || len(settled) != 0 || reader.asked != 0 {
		t.Fatalf("TakeUpFeedback() = %#v, %v after %d read(s), want nothing read", settled, err, reader.asked)
	}
}

// storeWithoutListing hides any listing of the runs waiting for a person.
type storeWithoutListing struct{ orchestrator.ReconcileStore }

// A forge that cannot be read changes nothing.
func TestAnUnreadableForgeChangesNothing(t *testing.T) {
	t.Parallel()
	fixture := newFeedbackFixture(t)
	settled := fixture.check(t, &feedbackReader{err: os.ErrDeadlineExceeded}, &collectedReports{}, 1, 3)
	if settled.Failure == "" || settled.RepairStarted {
		t.Fatalf("settlement = %#v, want the failed read said and nothing acted on", settled)
	}
	if run := testharness.LoadRun(t, fixture.store, fixture.run.RunID); !run.WaitingOnAPersonsMerge() {
		t.Fatalf("run = %s, want it still waiting", run.Status)
	}
}

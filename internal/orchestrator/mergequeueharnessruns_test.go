package orchestrator

// With execution.merge_queue on, the runs of the harness's own — a confirmed
// document's landing and a run that brings a pinned outside file in — are
// admitted to the harness's queue and land only as a combined candidate that
// passed the queue's checks and independent review; and the checks the queue
// runs on any combined candidate are the project's configured checks whole, the
// full race check among them.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// queuedDocuments is automaticFixture with the merge queue switched on, and the
// driver that works the queue reading a document run's bookkeeping item as the
// command line wires it.
func queuedDocuments(t *testing.T) (string, *orchestratortest.Backend, Pipeline, *runstate.Store, *runstate.MergeQueueStore, func() MergeQueueDriver) {
	t.Helper()
	repository, tracker, provider, pipeline, runs := automaticFixture(t)
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue
	pipeline.Config.Execution.MergeQueue = true
	driver := func() MergeQueueDriver {
		queued := pipeline
		items := HarnessRunItems{WorkTracker: tracker, Runs: runs}
		queued.Tracker = items
		worktrees := queued.Worktrees.(*gitworktree.Manager)
		return MergeQueueDriver{
			Worker: MergeQueueWorker{Pipeline: &queued, Queue: queue, Candidates: worktrees, Events: queue.AppendEvent, Waiting: MergeQueueWaiting(queue)},
			Promoter: MergeQueuePromoter{
				Pipeline: &queued, Queue: queue, Lander: worktrees,
				Completion: MergeQueueRunCompletion{Store: runs, Tracker: items},
				Repair:     MergeQueueRunHandback{Store: runs, Tracker: items},
			},
			Queues: queue,
		}
	}
	return repository, provider, pipeline, runs, queue, driver
}

func queuePass(t *testing.T, driver MergeQueueDriver, key runstate.MergeQueueKey) runstate.MergeQueuePass {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return driver.Pass(ctx, key)
}

// A confirmed document, with the queue on and a target that does not require
// the forge's own merge queue, is admitted to the harness's queue rather than
// landed by its run, and reaches main only as the candidate the queue checked
// and had independently reviewed. Its conversation is told it has not landed
// while it waits, and that it landed once the queue lands it.
func TestWithTheQueueOnAConfirmedDocumentLandsOnlyThroughTheQueuesCandidate(t *testing.T) {
	t.Parallel()

	repository, provider, pipeline, runs, queue, driver := queuedDocuments(t)
	base := gitLine(t, repository, "rev-parse", "main")
	state := landDesign(t, pipeline, provider, runs)
	if !state.AwaitingMergeQueue() || state.HoldsDeveloperSlot() || state.MergeQueue.ApprovedHead != state.HarnessCommit {
		t.Fatalf("run = %s %+v; want the document admitted to the queue, holding no slot", state.Status, state.MergeQueue)
	}
	if gitLine(t, repository, "rev-parse", "main") != base {
		t.Fatal("the document's run moved main although its change went to the queue")
	}
	// Asked again while it waits, the conversation keeps the document and no
	// second run is started.
	waiting, err := pipeline.PublishDocument(context.Background(), *state.Document)
	if err != nil || waiting.Settled || waiting.Landed || waiting.RunID != state.RunID {
		t.Fatalf("PublishDocument() while queued = %+v, %v; want it unsettled on the same run", waiting, err)
	}
	if recorded, _ := runs.Recorded(); len(recorded) != 1 {
		t.Fatalf("runs = %d, want the one run", len(recorded))
	}
	// Somebody lands on main meanwhile: the queue's candidate is built on that.
	moveTarget(t, repository, "landed-meanwhile.txt")
	key := state.MergeQueue.Key()
	pass := queuePass(t, driver(), key)
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	generations, err := queue.Generations(key, state.MergeQueue.EntryID)
	if err != nil || len(generations) == 0 {
		t.Fatalf("generations = %#v, %v", generations, err)
	}
	landed := generations[len(generations)-1]
	if gitLine(t, repository, "rev-parse", "main") != landed.Candidate || landed.CheckRun == nil || landed.CheckRun.FinishedAt == nil || landed.Review == nil || landed.Review.Decision != "approve" {
		t.Fatalf("main = %s, landed generation = %#v; want exactly the checked and reviewed candidate on main", gitLine(t, repository, "rev-parse", "main"), landed)
	}
	if landed.AuthorSession != state.Document.AuthorSession {
		t.Fatalf("the candidate's author is %q, want the document's author session %q", landed.AuthorSession, state.Document.AuthorSession)
	}
	if got := len(provider.RequestsForRole(domain.RoleReviewer)); got != 2 {
		t.Fatalf("the reviewer was asked %d times, want the run's review and the candidate's", got)
	}
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("a developer was invoked %d times", got)
	}
	if content := publicationGit(t, repository, "show", "main:docs/designs/checked-design.md"); content != strings.TrimSpace(state.Document.Candidate.Content) {
		t.Fatalf("main carries different bytes: %s", content)
	}
	if readQueueFile(t, repository, "landed-meanwhile.txt") == "" {
		t.Fatal("main lost what landed meanwhile")
	}
	settled, err := runs.Load(state.RunID)
	if err != nil || settled.Integration == nil || settled.Integration.TargetCommit != landed.Candidate {
		t.Fatalf("run integration = %#v, %v; want the queue's landing recorded on the run", settled.Integration, err)
	}
	delivered, err := pipeline.PublishDocument(context.Background(), *state.Document)
	if err != nil || !delivered.Settled || !delivered.Landed {
		t.Fatalf("PublishDocument() after the landing = %+v, %v; want it landed", delivered, err)
	}
}

// The queue checks a document's candidate with the document checks the project
// declares, as the document's own landing does, and never with the full checks
// and their race check.
func TestTheQueueChecksADocumentsCandidateWithTheDeclaredDocumentChecks(t *testing.T) {
	t.Parallel()

	_, provider, pipeline, runs, queue, driver := queuedDocuments(t)
	full, document, fullChecks, documentChecks := documentCheckMarkers(t)
	pipeline.Config.Checks, pipeline.Config.DocumentChecks = fullChecks, documentChecks
	state := landDesign(t, pipeline, provider, runs)
	if !state.AwaitingMergeQueue() {
		t.Fatalf("run = %s; want it waiting in the queue", state.Status)
	}
	if err := os.Remove(document); err != nil {
		t.Fatalf("the run's own document checks did not run: %v", err)
	}
	key := state.MergeQueue.Key()
	queued := driverWithChecks(driver, fullChecks)
	queued.Worker.Pipeline.Config.DocumentChecks = documentChecks
	if pass := queuePass(t, queued, key); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	if _, err := os.Stat(document); err != nil {
		t.Fatalf("the queue did not run the document checks on the candidate: %v", err)
	}
	if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the full checks, race check among them, ran over a document's candidate: %v", err)
	}
	generations, _ := queue.Generations(key, state.MergeQueue.EntryID)
	if checked := generations[len(generations)-1].Checks; !checked.Same(runstate.NewMergeQueueCheckConfiguration(documentChecks)) {
		t.Fatalf("candidate checked by %+v, want the document checks", checked)
	}
}

// A document whose candidate the queue's reviewer refuses ends its run with the
// reason recorded, hands nothing to a developer, and goes back to its owner as
// a document run that did not land does.
func TestADocumentTheQueueRefusesGoesBackToItsOwner(t *testing.T) {
	t.Parallel()

	repository, provider, pipeline, runs, _, driver := queuedDocuments(t)
	state := landDesign(t, pipeline, provider, runs)
	if !state.AwaitingMergeQueue() {
		t.Fatalf("run = %s; want it waiting in the queue", state.Status)
	}
	approve := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := approve(request)
		if request.Role == domain.RoleReviewer {
			result.FinalText = repairVerdict
		}
		return result, err
	}
	base := gitLine(t, repository, "rev-parse", "main")
	if pass := queuePass(t, driver(), state.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the candidate refused", pass)
	}
	if gitLine(t, repository, "rev-parse", "main") != base {
		t.Fatal("a refused candidate moved main")
	}
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("a developer was invoked %d times", got)
	}
	ended, err := runs.Load(state.RunID)
	if err != nil || ended.Status != runstate.StatusFailed || ended.Integration != nil || !ended.DocumentJudged() || !strings.Contains(ended.Failure, "merge queue") {
		t.Fatalf("run = %s, %q, judged %v, %v; want it ended, judged, with the queue's reason", ended.Status, ended.Failure, ended.DocumentJudged(), err)
	}
	delivered, err := pipeline.PublishDocument(context.Background(), *state.Document)
	if err != nil || !delivered.Settled || delivered.Landed || !delivered.Judged || !strings.Contains(delivered.Detail, "must revise the document") {
		t.Fatalf("PublishDocument() after the refusal = %+v, %v; want it back with its owner", delivered, err)
	}
	if recorded, _ := runs.Recorded(); len(recorded) != 1 {
		t.Fatalf("runs = %d, want no fresh run of the refused text", len(recorded))
	}
}

// A run that brings a file in whose combined change fails a check the target
// passes ends with its reason on the item its record is for, hands nothing to
// a developer or the operator, and a later pull starts a fresh run.
func TestAPinnedFileWhoseCombinedChangeFailsItsChecksEndsAndIsTriedAfresh(t *testing.T) {
	t.Parallel()

	repository, tracker, provider, pipeline, store, reports, _, driver := queuedOutsideFile(t)
	// The check fails only where main carries a file the change does not, and
	// the change carries the pinned file: on the combined change.
	pipeline.Config.Checks = append(pipeline.Config.Checks, "! { test -f assets/forbids-the-chart && test -f "+pinnedFile+"; }")
	ctx := context.Background()
	landing, _, _ := pipeline.NextOutsideFile(ctx)
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	saved, _ := store.Load(landing.RunID())
	if !saved.AwaitingMergeQueue() {
		t.Fatalf("run = %s; want it waiting in the queue", saved.Status)
	}
	moveTarget(t, repository, "assets/forbids-the-chart")
	if pass := queuePass(t, driverWithChecks(driver, pipeline.Config.Checks), saved.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the candidate handed back", pass)
	}
	assertPinnedRefusalSettled(t, tracker, provider, pipeline, store, reports, landing)
}

// A run that brings a file in whose change will not merge onto main as it now
// stands ends the same way.
func TestAPinnedFileWhoseChangeCannotBeAppliedToMainEndsAndIsTriedAfresh(t *testing.T) {
	t.Parallel()

	repository, tracker, provider, pipeline, store, reports, _, driver := queuedOutsideFile(t)
	ctx := context.Background()
	landing, _, _ := pipeline.NextOutsideFile(ctx)
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	saved, _ := store.Load(landing.RunID())
	if !saved.AwaitingMergeQueue() {
		t.Fatalf("run = %s; want it waiting in the queue", saved.Status)
	}
	// Somebody puts other bytes at the pinned file's path on main.
	writePinnedFixtureFile(t, repository, pinnedFile, "something else entirely\n")
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "other bytes at the pinned path")
	if pass := queuePass(t, driver(), saved.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflict handed back", pass)
	}
	if handed, _ := store.Load(landing.RunID()); handed.ReplayConflict == nil {
		t.Fatalf("run = %s, %q; want the conflict recorded", handed.Status, handed.Failure)
	}
	assertPinnedRefusalSettled(t, tracker, provider, pipeline, store, reports, landing)
}

// driverWithChecks is the queue's driver over a pipeline with the given checks.
func driverWithChecks(driver func() MergeQueueDriver, commands []string) MergeQueueDriver {
	d := driver()
	d.Worker.Pipeline.Config.Checks = commands
	d.Promoter.Pipeline.Config.Checks = commands
	return d
}

// assertPinnedRefusalSettled checks a pinned-file run the queue handed back:
// ended with its reason on the item the record is for, nothing asked of a
// developer and nothing reported, and the next pull counting the refusal and
// the one after starting a fresh run.
func assertPinnedRefusalSettled(t *testing.T, tracker *orchestratortest.Tracker, provider *orchestratortest.Backend, pipeline Pipeline, store *runstate.Store, reports *collectedReports, landing runstate.OutsideFileLanding) {
	t.Helper()
	ctx := context.Background()
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("a developer was invoked %d times", got)
	}
	if handed, _ := store.Load(landing.RunID()); !queueRefused(handed) {
		t.Fatalf("handed back run = %s; want it ended by the queue", handed.Status)
	}
	if !strings.Contains(tracker.Notes, "The merge queue handed this change back") || !strings.Contains(tracker.Notes, "No developer is asked to repair it") {
		t.Fatalf("the item the record is for was not told why: %q", tracker.Notes)
	}
	next, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due || next.RunID() != landing.RunID() {
		t.Fatalf("NextOutsideFile() = %+v, %v, %v; want the refused run settled first", next, due, err)
	}
	if _, err := pipeline.BringInOutsideFile(ctx, next); err != nil {
		t.Fatalf("BringInOutsideFile() settling the refusal error = %v", err)
	}
	attempt, _, _ := store.OutsideFileAttempt(pinnedRecord)
	if attempt.Attempt != 1 || len(attempt.QueueFailures) != 1 || attempt.Stopped != "" {
		t.Fatalf("attempt = %+v; want one refusal counted and nothing stopped", attempt)
	}
	reports.mu.Lock()
	reported := len(reports.reported)
	reports.mu.Unlock()
	if reported != 0 {
		t.Fatalf("%d reports filed for one refusal", reported)
	}
	if fresh, due, err := pipeline.NextOutsideFile(ctx); err != nil || !due || fresh.Attempt != 1 || fresh.RunID() == landing.RunID() {
		t.Fatalf("NextOutsideFile() after the refusal = %+v, %v, %v; want a fresh run", fresh, due, err)
	}
}

// Three runs in a row whose combined change the queue refuses, for one record
// unchanged on main, stop the record and file exactly one warning report, for
// the development manager; nothing is asked of a developer or the operator.
func TestThreeQueueRefusedRunsInARowStopTheRecordWithOneWarning(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, store, reports, _, driver := queuedOutsideFile(t)
	approve := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := approve(request)
		// Every run's own review approves; every candidate's review refuses.
		if request.Role == domain.RoleReviewer && len(provider.RequestsForRole(domain.RoleReviewer))%2 == 0 {
			result.FinalText = repairVerdict
		}
		return result, err
	}
	ctx := context.Background()
	for round := range runstate.MaxOutsideFileQueueFailures {
		landing, due, err := pipeline.NextOutsideFile(ctx)
		if err != nil || !due || landing.Attempt != round {
			t.Fatalf("round %d: NextOutsideFile() = %+v, %v, %v; want attempt %d due", round, landing, due, err, round)
		}
		if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
			t.Fatalf("round %d: BringInOutsideFile() error = %v", round, err)
		}
		saved, _ := store.Load(landing.RunID())
		if !saved.AwaitingMergeQueue() {
			t.Fatalf("round %d: run = %s; want it waiting in the queue", round, saved.Status)
		}
		if pass := queuePass(t, driver(), saved.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
			t.Fatalf("round %d: Pass() = %#v, want the candidate refused", round, pass)
		}
		// The next pull settles the refused run.
		settle, due, err := pipeline.NextOutsideFile(ctx)
		if err != nil || !due || settle.RunID() != landing.RunID() {
			t.Fatalf("round %d: NextOutsideFile() = %+v, %v, %v; want the refused run settled", round, settle, due, err)
		}
		if _, err := pipeline.BringInOutsideFile(ctx, settle); err != nil {
			t.Fatalf("round %d: settling error = %v", round, err)
		}
	}
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("a developer was invoked %d times", got)
	}
	attempt, _, _ := store.OutsideFileAttempt(pinnedRecord)
	if attempt.Stopped == "" || !attempt.Reported {
		t.Fatalf("attempt = %+v; want the record stopped and reported", attempt)
	}
	reports.mu.Lock()
	defer reports.mu.Unlock()
	if len(reports.reported) != 1 {
		t.Fatalf("reports = %d, want exactly one", len(reports.reported))
	}
	filed := reports.reported[0]
	if filed.Severity != report.SeverityWarning || filed.Addressee() != domain.RoleDevelopmentManager || !strings.Contains(filed.Message, pinnedRecord) {
		t.Fatalf("report = %+v; want one warning for the development manager naming the record", filed)
	}
	if _, due, _ := pipeline.NextOutsideFile(ctx); due {
		t.Fatal("a stopped record was due again")
	}
}

// The checks the harness's queue runs on a combined candidate are every check
// the project declares, run whole: the full race check is among them and is
// told the whole module, even where the run's own checks before review were
// narrowed to the packages its change touches.
func TestTheQueueRunsTheFullRaceCheckOnTheCombinedChange(t *testing.T) {
	t.Parallel()

	r := newQueueRun(t, false)
	// A Go module, so the run's own checks are narrowed: its change touches no
	// package.
	writeQueueFile(t, r.repository, "go.mod", "module example.invalid/feature\n\ngo 1.22\n")
	writeQueueFile(t, r.repository, "lib/lib.go", "package lib\n")
	runPipelineGit(t, r.repository, "add", ".")
	runPipelineGit(t, r.repository, "commit", "-m", "make the repository a Go module")
	told := filepath.Join(t.TempDir(), "race-told")
	race := `printf '%s\n' "${YOYODYNE_CHANGED_GO_PACKAGES-unset}" >> ` + told + ` && test -f feature.txt`
	r.pipeline.Config.Checks = []string{"test -f feature.txt", race}
	r.pipeline.Config.Execution.MergeQueue = true
	outcome := r.run(t)
	if outcome.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the change admitted to the queue", outcome)
	}
	before := readLines(t, told)
	if len(before) == 0 || before[0] == "./..." {
		t.Fatalf("the run's own race check was told %q; want it narrowed before review, so the queue's whole run is what this test shows", before)
	}
	key := outcome.MergeQueue.Key()
	if pass := queuePass(t, r.driver(), key); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	after := readLines(t, told)
	if len(after) != len(before)+1 || after[len(after)-1] != "./..." {
		t.Fatalf("the race check was told %q; want one more run on the candidate, told the whole module", after)
	}
	generations, err := r.queue.Generations(key, outcome.MergeQueue.EntryID)
	if err != nil || len(generations) == 0 {
		t.Fatalf("generations = %#v, %v", generations, err)
	}
	landed := generations[len(generations)-1]
	if !landed.Checks.Same(runstate.NewMergeQueueCheckConfiguration(r.pipeline.Config.Checks)) || landed.CheckRun == nil || len(landed.CheckRun.Results) != 2 {
		t.Fatalf("landed generation checks = %+v, run %+v; want every configured check, the race check among them", landed.Checks, landed.CheckRun)
	}
	for _, result := range landed.CheckRun.Results {
		if !result.Passed {
			t.Fatalf("check %q did not pass on the landed candidate", result.Command)
		}
	}
	if landed.CheckRun.Results[1].Command != race {
		t.Fatalf("the candidate's checks were %+v; want the race check run on it", landed.CheckRun.Results)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

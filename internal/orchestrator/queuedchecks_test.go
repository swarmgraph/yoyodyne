package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// These drive yoyodyne-ifd.429.16: a merge the forge still holds is read with
// its checks, and a red one is never simply left queued. Each case is a whole
// run that ends with its merge queued on a protected target, and a forge that
// then reports the head's checks.

// checkedForge is the fabricated forge with check state: what it reports about
// the head's checks, and every queued merge it was asked to withdraw. Withdrawing
// one is the forge no longer holding it.
type checkedForge struct {
	queuedForge
	reading   publish.CheckReading
	readError error
	withdrawn []int
	// reruns are the check runs it was asked to run again; refuseRerun, where
	// set, is its answer to every such request.
	reruns      []int64
	refuseRerun error
}

func (f *checkedForge) RerunCheck(_ context.Context, checkRun int64) error {
	if f.refuseRerun != nil {
		return f.refuseRerun
	}
	f.reruns = append(f.reruns, checkRun)
	return nil
}

func (f *checkedForge) Checks(_ context.Context, number int, _ string) (publish.CheckReading, error) {
	if f.readError != nil {
		return publish.CheckReading{}, f.readError
	}
	reading := f.reading
	if reading.HeadCommit == "" {
		merges := f.MergeRequests()
		reading.HeadCommit = merges[len(merges)-1].HeadCommit
	}
	return reading, nil
}

func TestAQueuedMergeRecordsAnUnreadCheckStateAndReadsItAgain(t *testing.T) {
	t.Parallel()
	for _, message := range []string{"HTTP 403: Resource not accessible by integration", "decode the comparison: unexpected end of JSON input"} {
		t.Run(message, func(t *testing.T) {
			fixture, forge, _ := queuedOnProtectedTarget(t)
			reconciler := fixture.sweep(t, forge, false)
			forge.readError = errors.New(message)
			if _, err := reconciler.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			initial, err := fixture.store.Load(pipelineRunID)
			if err != nil {
				t.Fatal(err)
			}
			if initial.PullRequest.Checks == nil || initial.PullRequest.Checks.ReadError != message || initial.PullRequest.Checks.HeadCommit != "" {
				t.Fatalf("first failed read = %#v", initial.PullRequest.Checks)
			}
			forge.readError = nil
			// Re-run accounting must survive a failed read of the same head.
			forge.reading = jobFailure(41)
			if _, err := reconciler.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			forge.readError = errors.New(message)
			results, err := reconciler.Reconcile(context.Background())
			if err != nil || len(results) != 1 || results[0].Action != ActionQueued {
				t.Fatalf("Reconcile() = %#v, %v", results, err)
			}
			recorded, err := fixture.store.Load(pipelineRunID)
			if err != nil {
				t.Fatal(err)
			}
			checks := recorded.PullRequest.Checks
			if checks == nil || checks.ReadError != message || checks.Red() || checks.Reruns != 1 {
				t.Fatalf("recorded checks = %#v", checks)
			}
			if !recorded.PullRequest.MergeQueued || len(forge.withdrawn) != 0 || fixture.tracker.Record().Blocked {
				t.Fatal("an unread state withdrew or handed back the merge")
			}
			forge.readError = nil
			if _, err := reconciler.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			recorded, err = fixture.store.Load(pipelineRunID)
			if err != nil {
				t.Fatal(err)
			}
			if checks = recorded.PullRequest.Checks; checks.ReadError != "" || checks.Reruns != 1 {
				t.Fatalf("successful rereading = %#v, want the error cleared and re-run preserved", checks)
			}
		})
	}
}

func (f *checkedForge) DisableAutoMerge(_ context.Context, number int) error {
	f.withdrawn = append(f.withdrawn, number)
	f.DropQueuedMerge()
	return nil
}

// queuedOnProtectedTarget is a run that landed through its pull request and
// finished with the forge holding the merge.
func queuedOnProtectedTarget(t *testing.T) (queuedFixture, *checkedForge, Outcome) {
	t.Helper()
	fixture := newQueuedFixture(t)
	fixture.forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "ruleset"})
	outcome := fixture.run(t)
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	return fixture, &checkedForge{queuedForge: fixture.forge}, outcome
}

// sweep is the reconciler the reconcile verb builds: the forge's checks read,
// a free slot, and — where hosts is set — the run it makes
// live continued through the same pipeline a run is.
func (f queuedFixture) sweep(t *testing.T, forge *checkedForge, hosts bool) Reconciler {
	t.Helper()
	reconciler := f.reconciler(t)
	reconciler.Publisher = forge
	reconciler.Checks = forge
	reconciler.Capacity = 1
	reconciler.HostsRuns = hosts
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, f.repository, f.worktreeRoot, f.store, f.tracker, provider, []string{"exit 0"}), provider), f.forge)
	pipeline.Publisher = forge
	reconciler.Continue = pipeline.Continue
	return reconciler
}

// A head that fell behind its target and fails a check on a file its change
// does not touch is brought up to date by the harness: the queued merge is
// withdrawn, the change is replayed onto the target, checked and reviewed
// again, and its merge queued again — spending one integration retry.
func TestAQueuedHeadBehindItsTargetFailingUnrelatedChecksIsUpdatedAndRequeued(t *testing.T) {
	t.Parallel()

	fixture, forge, outcome := queuedOnProtectedTarget(t)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.reading = publish.CheckReading{
		Files:    []string{"feature.txt"},
		Failing:  []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}},
		Passing:  3,
		BehindBy: 1,
	}
	reconciler := fixture.sweep(t, forge, true)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionUpdating || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the queued head put back at its promotion", results)
	}
	if len(forge.withdrawn) != 1 || forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v, queued = %t; want the queued merge withdrawn before the head is rewritten", forge.withdrawn, forge.HoldsQueuedMerge())
	}
	resumed, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !updatingQueuedHead(resumed) || resumed.Integration != nil {
		t.Fatalf("record = status %q, phase %q, integration %#v, resumptions %#v; want a live run at its promotion",
			resumed.Status, resumed.Phase, resumed.Integration, resumed.IntegrationResumptions)
	}
	if fixture.tracker.Record().Blocked || fixture.tracker.Record().Closed {
		t.Fatalf("blocked = %t, closed = %t; an update hands nothing back and closes nothing", fixture.tracker.Record().Blocked, fixture.tracker.Record().Closed)
	}
	if !strings.Contains(fixture.tracker.Record().Notes, "go test (on internal/elsewhere/elsewhere_test.go, which this change does not touch)") {
		t.Errorf("the item was not told which check failed and on what:\n%s", fixture.tracker.Record().Notes)
	}

	updates, err := reconciler.ContinueUpdates(context.Background())
	if err != nil {
		t.Fatalf("ContinueUpdates() error = %v", err)
	}
	if len(updates) != 1 || !updates[0].Continued || updates[0].Failure != "" || updates[0].Outcome == nil {
		t.Fatalf("updates = %#v, want the run hosted through its replay", updates)
	}
	replayed := *updates[0].Outcome
	if replayed.IntegrationRetries != 1 {
		t.Errorf("integration retries = %d, want the update charged as the one replay it is", replayed.IntegrationRetries)
	}
	if replayed.PullRequest == nil || !replayed.PullRequest.MergeQueued || len(forge.MergeRequests()) != 2 {
		t.Fatalf("outcome pull request = %#v, merges = %d; want the replayed change's merge queued again", replayed.PullRequest, len(forge.MergeRequests()))
	}
	if again := forge.MergeRequests()[1].HeadCommit; again == outcome.PullRequest.HeadCommit {
		t.Errorf("the merge was queued again on the old head %s, want the replayed one", again)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.Outstanding() || settled.PullRequest == nil || !settled.PullRequest.MergeQueued || settled.Integration == nil {
		t.Fatalf("record = %#v, want a finished run waiting on its merge queued again", settled)
	}
	if settled.BaseCommit == outcome.BaseCommit {
		t.Errorf("base commit = %s, want the replay's new base rather than the one the head fell behind", settled.BaseCommit)
	}
	// The replayed change was reviewed again rather than carrying the old verdict.
	if settled.ReviewHeadCommit != settled.Integration.SourceCommit {
		t.Errorf("review head = %s, promoted = %s; want the verdict to be about the replayed change", settled.ReviewHeadCommit, settled.Integration.SourceCommit)
	}
}

// A head whose own change fails a check is handed back for repair, with the
// check named, rather than left queued.
func TestAQueuedHeadFailingACheckOnItsOwnChangeIsHandedBack(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = publish.CheckReading{
		Files:    []string{"feature.txt"},
		Failing:  []publish.FailedCheck{{Name: "lint", Paths: []string{"feature.txt"}}},
		BehindBy: 4,
	}
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, true)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the red merge handed back", results)
	}
	if len(forge.withdrawn) != 1 || forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v, queued = %t; want the queued merge withdrawn so nothing lands it", forge.withdrawn, forge.HoldsQueuedMerge())
	}
	if !fixture.tracker.Record().Blocked {
		t.Fatal("the red change was left queued rather than handed back")
	}
	for _, want := range []string{"lint (on feature.txt, which this change touches)", "fail on this change", "withdrew the queued merge"} {
		if !strings.Contains(fixture.tracker.Record().BlockReason, want) {
			t.Errorf("blocker does not say %q:\n%s", want, fixture.tracker.Record().BlockReason)
		}
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.MergeDrop == nil || settled.PullRequest.MergeQueued || settled.Outstanding() {
		t.Fatalf("record = drop %#v, queued %t, outstanding %t; want the withdrawn merge recorded for repair", settled.MergeDrop, settled.PullRequest.MergeQueued, settled.Outstanding())
	}
	if settled.PullRequest.Checks == nil || !settled.PullRequest.Checks.ChangeFails() {
		t.Errorf("checks = %#v, want the reading that decided it kept on the publication", settled.PullRequest.Checks)
	}
	if updates, err := reconciler.ContinueUpdates(context.Background()); err != nil || len(updates) != 0 {
		t.Errorf("ContinueUpdates() = %#v, %v; a handed-back run is not updated", updates, err)
	}
}

// A head level with its target that fails a required check on a file its
// change never touched is the September 2026 shape (yoyodyne-ifd.362): a red
// test inherited from the local target held every queued merge on the forge for
// six days, and no sweep said so. It is handed back on the first sweep that
// reads it, with the check named and the merge drop on the record — which is
// what the channel is told as a dropped merge — rather than left queued.
func TestAQueuedHeadLevelWithItsTargetFailingAnUnrelatedCheckIsHandedBackOnTheFirstSweep(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = publish.CheckReading{
		Files:   []string{"feature.txt"},
		Failing: []publish.FailedCheck{{Name: "build", Paths: []string{"internal/backend/codex/codex_test.go"}}},
		Passing: 2,
	}
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, true)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the held merge handed back on the first sweep", results)
	}
	if len(forge.withdrawn) != 1 || forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v, queued = %t; want the queued merge withdrawn", forge.withdrawn, forge.HoldsQueuedMerge())
	}
	for _, want := range []string{"build (on internal/backend/codex/codex_test.go, which this change does not touch)", "level with main", "needs a person"} {
		if !strings.Contains(fixture.tracker.Record().BlockReason, want) {
			t.Errorf("blocker does not say %q:\n%s", want, fixture.tracker.Record().BlockReason)
		}
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.MergeDrop == nil || settled.PullRequest.MergeQueued || settled.Outstanding() {
		t.Fatalf("record = drop %#v, queued %t, outstanding %t; want a dropped merge on the record, not a merge still queued", settled.MergeDrop, settled.PullRequest.MergeQueued, settled.Outstanding())
	}
	if updates, err := reconciler.ContinueUpdates(context.Background()); err != nil || len(updates) != 0 {
		t.Errorf("ContinueUpdates() = %#v, %v; a head level with its target has nothing to be brought up to date onto", updates, err)
	}
}

// jobFailure is a head level with main whose adoption job the forge ended
// itself — here cancelled — with its only annotation the forge's own on
// .github. The shape is that of pull request 863's reading at 04:15Z on
// 2026-09-28, whose record kept no conclusion; the conclusion here is the
// fixture's, not that run's. checkRun is the check run the forge reports, and
// a re-run is given a new one.
func jobFailure(checkRun int64) publish.CheckReading {
	return publish.CheckReading{
		Files:   []string{"feature.txt"},
		Failing: []publish.FailedCheck{{Name: "adoption", Paths: []string{".github"}, ID: checkRun, Conclusion: "cancelled"}},
		Passing: 1,
	}
}

// A head level with its target whose job the forge ended itself is run again,
// and its merge left queued, rather than handed to a person as main being red.
// A reading taken before the re-run began spends nothing; a job ended again on
// every re-run the bound allows is handed back saying the forge ended it.
func TestAQueuedHeadWhoseJobTheForgeEndedIsRunAgainBeforeItIsHandedBack(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, true)

	for sweep := 1; sweep <= runstate.MaxCheckReruns; sweep++ {
		checkRun := int64(4214 + sweep)
		forge.reading = jobFailure(checkRun)
		results, err := reconciler.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("sweep %d: Reconcile() error = %v", sweep, err)
		}
		if len(results) != 1 || results[0].Action != ActionQueued || !strings.Contains(results[0].Detail, "asked it to run them again") {
			t.Fatalf("sweep %d: reconciliation = %#v, want the job run again and the merge left queued", sweep, results)
		}
		if len(forge.reruns) != sweep || forge.reruns[sweep-1] != checkRun {
			t.Fatalf("sweep %d: re-runs = %v, want check run %d run again", sweep, forge.reruns, checkRun)
		}
		if len(forge.withdrawn) != 0 || !forge.HoldsQueuedMerge() || fixture.tracker.Record().Blocked {
			t.Fatalf("sweep %d: withdrawn = %v, blocked = %t; a job being run again is not withdrawn or handed back", sweep, forge.withdrawn, fixture.tracker.Record().Blocked)
		}
		recorded, err := fixture.store.Load(pipelineRunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if recorded.PullRequest.Checks == nil || recorded.PullRequest.Checks.Reruns != sweep {
			t.Fatalf("sweep %d: checks = %#v, want the re-runs on this head counted", sweep, recorded.PullRequest.Checks)
		}

		// The next sweep reads the forge before it has started the re-run: the
		// same check run, still cancelled. That spends nothing.
		results, err = reconciler.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("sweep %d again: Reconcile() error = %v", sweep, err)
		}
		if len(results) != 1 || results[0].Action != ActionQueued || !strings.Contains(results[0].Detail, "has not yet started the re-run") || len(forge.reruns) != sweep {
			t.Fatalf("sweep %d again: reconciliation = %#v, re-runs = %v; want a stale reading to spend no re-run", sweep, results, forge.reruns)
		}
	}

	forge.reading = jobFailure(9999)
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || len(forge.reruns) != runstate.MaxCheckReruns {
		t.Fatalf("reconciliation = %#v, re-runs = %v; want it handed back once the re-runs are spent", results, forge.reruns)
	}
	for _, want := range []string{"adoption (the forge cancelled the job before any step failed, naming no file)", "ended that way again on each of 2 re-run(s)", "The forge's account of each, read under the harness's forge access, is in this item's notes", "needs a person"} {
		if !strings.Contains(fixture.tracker.Record().BlockReason, want) {
			t.Errorf("blocker does not say %q:\n%s", want, fixture.tracker.Record().BlockReason)
		}
	}
	if strings.Contains(fixture.tracker.Record().BlockReason, "which this change does not touch") {
		t.Errorf("blocker names .github as a file the change did not touch:\n%s", fixture.tracker.Record().BlockReason)
	}
}

// A step that failed with its only annotation on .github — "Process completed
// with exit code 2", which is what a genuine red test looks like — is not
// re-run, and is handed back as a head level with its target failing always
// was, without saying the tree is not at fault.
func TestAFailedStepAnnotatedOnlyOnDotGithubIsNotRunAgain(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = publish.CheckReading{
		Files:   []string{"feature.txt"},
		Failing: []publish.FailedCheck{{Name: "build", Paths: []string{".github"}, ID: 606, Conclusion: "failure"}},
		Passing: 1,
	}
	fixture.docket = &memoryDocket{}
	results, err := fixture.sweep(t, forge, true).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || len(forge.reruns) != 0 {
		t.Fatalf("reconciliation = %#v, re-runs = %v; want a failed step handed back, not run again", results, forge.reruns)
	}
	blocker := fixture.tracker.Record().BlockReason
	if !strings.Contains(blocker, "build (a step failed without naming a file; the forge filed it on .github, and the forge's account of it on the item says which step)") {
		t.Errorf("blocker does not say a step failed and where to look:\n%s", blocker)
	}
	for _, unwanted := range []string{"before any step failed", "which this change does not touch", "do not decide"} {
		if strings.Contains(blocker, unwanted) {
			t.Errorf("blocker says %q of a failed step:\n%s", unwanted, blocker)
		}
	}
}

// A job run again that then passes leaves the merge queued with nothing
// withdrawn, which is the forge landing it.
func TestAJobThatPassesWhenRunAgainLeavesTheMergeQueued(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = jobFailure(4215)
	reconciler := fixture.sweep(t, forge, true)
	if results, err := reconciler.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != ActionQueued {
		t.Fatalf("Reconcile() = %#v, %v; want the job run again", results, err)
	}
	forge.reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 2}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionQueued {
		t.Fatalf("Reconcile() = %#v, %v; want the merge left queued once the re-run passed", results, err)
	}
	if len(forge.withdrawn) != 0 || !forge.HoldsQueuedMerge() || fixture.tracker.Record().Blocked || len(forge.reruns) != 1 {
		t.Fatalf("withdrawn = %v, re-runs = %v, blocked = %t; want the merge left to land", forge.withdrawn, forge.reruns, fixture.tracker.Record().Blocked)
	}
}

// A forge that will not run the job again — a token without the right to, or a
// check no Actions job ran — hands it back on that sweep, saying so, as a head
// level with its target always was.
func TestAJobTheForgeWillNotRunAgainIsHandedBackSayingSo(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = jobFailure(4215)
	forge.refuseRerun = errors.New("HTTP 403: Resource not accessible by integration")
	fixture.docket = &memoryDocket{}
	results, err := fixture.sweep(t, forge, true).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || len(forge.withdrawn) != 1 {
		t.Fatalf("reconciliation = %#v, withdrawn = %v; want the merge withdrawn and handed back", results, forge.withdrawn)
	}
	for _, want := range []string{"the forge would not run them again", "HTTP 403: Resource not accessible by integration"} {
		if !strings.Contains(fixture.tracker.Record().BlockReason, want) {
			t.Errorf("blocker does not say %q:\n%s", want, fixture.tracker.Record().BlockReason)
		}
	}
}

// A request queued past triage.stuck_merge_age with red checks — here one a
// pass that hosts no runs could not bring up to date — is docketed with its
// checks beside it. The attention line's half is TestAQueuedPublicationLineCarriesItsChecks.
func TestAStuckQueuedMergeIsDocketedWithItsChecks(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = publish.CheckReading{
		Files:    []string{"feature.txt"},
		Failing:  []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}},
		BehindBy: 31,
	}
	results, err := fixture.sweep(t, forge, false).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionQueued || !strings.Contains(results[0].Detail, "left queued for the next sweep") {
		t.Fatalf("reconciliation = %#v, want the merge left queued for a sweep that hosts runs", results)
	}
	if len(forge.withdrawn) != 0 || !forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v; a merge nothing will update is not withdrawn", forge.withdrawn)
	}

	docket := &memoryDocket{}
	docketer := docketerOverStore(docket, fixture.store, docketConfig())
	docketer.Clock = fixedClock{at: time.Now().Add(3 * time.Hour)}
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var entry triage.Entry
	for _, docketed := range docket.entries {
		if docketed.Class == triage.ClassPublication {
			entry = docketed
		}
	}
	if entry.Publication == nil || !entry.Publication.MergeQueued {
		t.Fatalf("docket = %#v, want the stuck queued merge docketed", docket.entries)
	}
	rendered := entry.Render()
	for _, want := range []string{"the forge has its merge queued", "Checks: checks failing: go test (on internal/elsewhere/elsewhere_test.go, which this change does not touch)", "31 commit(s) behind main"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("docket entry does not say %q:\n%s", want, rendered)
		}
	}

	// A queued merge no sweep has read the checks of says so, rather than being
	// shown as approved and queued with nothing beside it.
	unread := entry
	published := *entry.Publication
	published.Checks = ""
	unread.Publication = &published
	if !strings.Contains(unread.Render(), "Checks: not yet read") {
		t.Errorf("a queued merge with unread checks is shown without saying so:\n%s", unread.Render())
	}
}

// assertKept holds what yoyodyne-atc is about: a run whose change lands through
// its pull request keeps its branch and worktree until the forge's merge is
// confirmed, because nothing before that proves the change is on the target and
// the kept branch is what a head fallen behind is brought up to date from.
func assertKept(t *testing.T, fixture queuedFixture, outcome Outcome, when string) {
	t.Helper()
	if _, err := os.Stat(outcome.WorktreePath); err != nil {
		t.Errorf("%s: the worktree %s is gone: %v", when, outcome.WorktreePath, err)
	}
	if err := runGitQuiet(fixture.repository, "rev-parse", "--verify", "--quiet", "refs/heads/"+outcome.Branch); err != nil {
		t.Errorf("%s: the branch %s is gone: %v", when, outcome.Branch, err)
	}
	recorded, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.WorktreeRemoved || recorded.BranchRemoved {
		t.Errorf("%s: record says worktree removed %t, branch removed %t; want both kept", when, recorded.WorktreeRemoved, recorded.BranchRemoved)
	}
}

// A queued landing keeps its branch and worktree through the run's end and
// through a sweep that finds the merge still held, and a merge the forge then
// drops with nothing to replay is handed to a person with both still there and
// said to be there. Until 2026-09-27 that blocker said both were gone — they
// were not — and the notes said the change was integrated into the local main,
// which is the authoritative one, over a main that was never moved.
func TestAQueuedLandingKeepsItsBranchAndWorktreeUntilTheForgesMergeIsConfirmed(t *testing.T) {
	t.Parallel()

	fixture, forge, outcome := queuedOnProtectedTarget(t)
	assertKept(t, fixture, outcome, "after the run")
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.BaseCommit {
		t.Fatalf("local main = %q after a queued landing, want the base %q", local, outcome.BaseCommit)
	}
	notes := fixture.tracker.Record().Notes
	for _, unwanted := range []string{"Integrated into: main", "integrated automatically", "authoritative", "cleanup pending"} {
		if strings.Contains(notes, unwanted) {
			t.Errorf("the run's notes on a protected target say %q:\n%s", unwanted, notes)
		}
	}
	for _, want := range []string{"handed to the forge to land through its pull request", "Lands on: main", "kept with its branch until the forge's merge is confirmed"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the run's notes do not say %q:\n%s", want, notes)
		}
	}

	// A sweep that finds the merge still held, its checks passing, leaves it all.
	forge.reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3}
	reconciler := fixture.sweep(t, forge, true)
	if results, err := reconciler.Reconcile(context.Background()); err != nil || len(results) != 1 || results[0].Action != ActionQueued {
		t.Fatalf("Reconcile() = %#v, %v; want the merge left queued", results, err)
	}
	assertKept(t, fixture, outcome, "while the forge holds the merge")

	// The forge drops it with its head level with main: nothing to bring up to
	// date, so it is a person's — with the artifacts there and said to be.
	forge.DropQueuedMerge()
	forge.reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("Reconcile() = %#v, %v; want the dropped merge handed back", results, err)
	}
	assertKept(t, fixture, outcome, "after the drop is handed back")
	blocker := fixture.tracker.Record().BlockReason
	for _, want := range []string{"Preserved worktree: " + outcome.WorktreePath, "Preserved branch: " + outcome.Branch, "The local main was not moved", "while settling the merge its finished run left queued"} {
		if !strings.Contains(blocker, want) {
			t.Errorf("blocker does not say %q:\n%s", want, blocker)
		}
	}
	for _, unwanted := range []string{"is gone", "interrupted run", "authoritative"} {
		if strings.Contains(blocker, unwanted) {
			t.Errorf("blocker says %q:\n%s", unwanted, blocker)
		}
	}
	if strings.Contains(fixture.tracker.Record().Notes, "integrated into the local target branch") {
		t.Errorf("the settlement's notes say the change is integrated into the local target:\n%s", fixture.tracker.Record().Notes)
	}
	(&protectedRun{repository: fixture.repository, remote: fixture.remote}).assertMainNotAhead(t)
}

// An unread drop stays queued for the next sweep, under harness ownership.
// Once its checks can be read, it is replayed or handed back using that reading.
func TestADroppedMergeWithUnreadChecksStaysQueuedUntilASuccessfulRetry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		behind int
		want   ReconcileAction
	}{
		{"behind target, replayed", 1, ActionUpdating},
		{"level with target, handed back", 0, ActionBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, forge, _ := queuedOnProtectedTarget(t)
			if tc.behind > 0 {
				driftRemoteTarget(t, fixture.remote, "main")
			}
			forge.DropQueuedMerge()
			forge.readError = errors.New("decode the comparison: unexpected end of JSON input")
			reconciler := fixture.sweep(t, forge, true)
			for sweep := 0; sweep < 2; sweep++ {
				results, err := reconciler.Reconcile(context.Background())
				if err != nil || len(results) != 1 || results[0].Action != ActionQueued {
					t.Fatalf("unread sweep %d: Reconcile() = %#v, %v", sweep, results, err)
				}
				recorded, err := fixture.store.Load(pipelineRunID)
				if err != nil {
					t.Fatal(err)
				}
				if !recorded.PullRequest.MergeQueued || recorded.MergeDrop != nil || recorded.PublishFailure != "" || recorded.PullRequest.Checks == nil || recorded.PullRequest.Checks.ReadError != forge.readError.Error() {
					t.Fatalf("unread sweep %d: publication = %#v, drop = %#v, failure = %q", sweep, recorded.PullRequest, recorded.MergeDrop, recorded.PublishFailure)
				}
				standing := readmodel.ReadStanding(context.Background(), readmodel.Sources{Runs: fixture.store})
				found := false
				for _, entry := range standing.NeedsHuman {
					if entry.Kind == readmodel.AttentionPublication && entry.ID == pipelineRunID {
						found = true
						if entry.Mover != readmodel.MoverHarness || !strings.Contains(entry.Whose(), "next `yoyo reconcile` sweep") {
							t.Fatalf("unread publication waits on %s: %s", entry.Mover, entry.Whose())
						}
					}
				}
				if !found || fixture.tracker.Record().Blocked || fixture.tracker.Record().Closed {
					t.Fatal("an unread drop disappeared or was handed back")
				}
			}
			forge.readError = nil
			forge.reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: tc.behind}
			results, err := reconciler.Reconcile(context.Background())
			if err != nil || len(results) != 1 || results[0].Action != tc.want {
				t.Fatalf("successful retry: Reconcile() = %#v, %v, want %s", results, err, tc.want)
			}
			recorded, err := fixture.store.Load(pipelineRunID)
			if err != nil {
				t.Fatal(err)
			}
			if recorded.PullRequest.Checks != nil && recorded.PullRequest.Checks.ReadError != "" {
				t.Fatal("the successful retry retained the earlier read error")
			}
			if tc.want == ActionUpdating {
				updates, err := reconciler.ContinueUpdates(context.Background())
				if err != nil || len(updates) != 1 || !updates[0].Continued || updates[0].Failure != "" || updates[0].Outcome == nil || updates[0].Outcome.PullRequest == nil || !updates[0].Outcome.PullRequest.MergeQueued {
					t.Fatalf("ContinueUpdates() = %#v, %v, want the dropped head replayed and requeued", updates, err)
				}
			} else if recorded.MergeDrop == nil || recorded.PullRequest.MergeQueued {
				t.Fatal("a decided drop still reads as queued")
			}
		})
	}
}

// A merge the forge drops while its head is behind the target, failing nothing
// the change touches, is the race a replay answers: the sweep brings the head up
// to date from the kept branch and queues the merge again, and hands nothing
// back. There is no queued merge left to withdraw.
func TestADroppedMergeWhoseHeadFellBehindIsReplayedFromTheKeptBranch(t *testing.T) {
	t.Parallel()

	fixture, forge, outcome := queuedOnProtectedTarget(t)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.DropQueuedMerge()
	forge.reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: 1}
	reconciler := fixture.sweep(t, forge, true)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionUpdating || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the dropped landing put back at its promotion", results)
	}
	if len(forge.withdrawn) != 0 {
		t.Errorf("withdrawn = %v, want nothing withdrawn from a merge the forge no longer holds", forge.withdrawn)
	}
	record := fixture.tracker.Record()
	if record.Blocked || record.Closed {
		t.Fatalf("blocked = %t, closed = %t; a replayable drop hands nothing back", record.Blocked, record.Closed)
	}
	if !strings.Contains(record.Notes, "dropped by the forge while its head was behind") {
		t.Errorf("the item was not told the drop is being replayed:\n%s", record.Notes)
	}
	resumed, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !updatingQueuedHead(resumed) || resumed.MergeDrop != nil || resumed.PublishFailure != "" {
		t.Fatalf("record = status %q, phase %q, drop %#v, publish failure %q; want a live run at its promotion with no drop recorded",
			resumed.Status, resumed.Phase, resumed.MergeDrop, resumed.PublishFailure)
	}

	updates, err := reconciler.ContinueUpdates(context.Background())
	if err != nil {
		t.Fatalf("ContinueUpdates() error = %v", err)
	}
	if len(updates) != 1 || !updates[0].Continued || updates[0].Failure != "" || updates[0].Outcome == nil {
		t.Fatalf("updates = %#v, want the run hosted through its replay", updates)
	}
	replayed := *updates[0].Outcome
	if replayed.PullRequest == nil || !replayed.PullRequest.MergeQueued || len(forge.MergeRequests()) != 2 {
		t.Fatalf("outcome pull request = %#v, merges = %d; want the replayed change's merge queued again", replayed.PullRequest, len(forge.MergeRequests()))
	}
	if again := forge.MergeRequests()[1].HeadCommit; again == outcome.PullRequest.HeadCommit {
		t.Errorf("the merge was queued again on the old head %s, want the replayed one", again)
	}
	assertKept(t, fixture, replayed, "after the replay queued the merge again")
	(&protectedRun{repository: fixture.repository, remote: fixture.remote}).assertMainNotAhead(t)
}

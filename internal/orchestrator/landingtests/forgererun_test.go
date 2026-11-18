package landingtests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive yoyodyne-ifd.429.87.7: a queued merge whose GitHub checks fail
// only in tests the change does not touch has the failing job run once more on
// the same commit before anything is withdrawn or blamed on the change.

// untouchedLog is the build job's log of pull request 1555's failure on
// October 31: one test failing in internal/gitworktree, which the change does
// not touch, with GitHub's timestamps at the front of each line.
const untouchedLog = "2026-10-31T19:32:01.0000000Z ok  \tgithub.com/mason-bryant/yoyodyne/internal/cli\t40.1s\n" +
	"2026-10-31T19:32:02.0000000Z --- FAIL: TestConcurrentCreationSurvivesAGitCommandTheHarnessDidNotCompose (3.10s)\n" +
	"2026-10-31T19:32:02.0000000Z     concurrent_test.go:88: worktree add: exit status 128\n" +
	"2026-10-31T19:32:02.0000000Z FAIL\n" +
	"2026-10-31T19:32:02.0000000Z FAIL\tgithub.com/mason-bryant/yoyodyne/internal/gitworktree\t31.4s\n" +
	"2026-10-31T19:32:03.0000000Z FAIL\n" +
	"2026-10-31T19:32:03.0000000Z ##[error]Process completed with exit code 2."

// buildFailing is the build check failing on check run id, its only annotation
// the forge's own on .github, with the head level with main.
func buildFailing(id int64, files ...string) publish.CheckReading {
	if len(files) == 0 {
		files = []string{"internal/orchestrator/pipeline.go", "docs/operations.md"}
	}
	return publish.CheckReading{
		Files: files,
		Failing: []publish.FailedCheck{{
			Name: "build", Paths: []string{".github"}, ID: id, Conclusion: "failure",
			Annotations: []publish.Annotation{{Path: ".github", Level: "failure", Message: "Process completed with exit code 2."}},
		}},
		Passing: 3,
	}
}

// recordFirstForge is a forge that, when asked to run a job again, reads the
// run's record to see the request was written down before it was made.
type recordFirstForge struct {
	*orchestratortest.CheckedForge
	t        *testing.T
	store    *runstate.Store
	recorded []bool
}

func (f *recordFirstForge) RerunCheck(ctx context.Context, checkRun int64) error {
	state := testharness.LoadRun(f.t, f.store, testharness.PipelineRunID)
	checks := state.PullRequest.Checks
	f.recorded = append(f.recorded, checks != nil && checks.UntouchedRerun != nil && checks.UntouchedRerun.AskedAt == nil)
	return f.CheckedForge.RerunCheck(ctx, checkRun)
}

func untouchedSweep(t *testing.T) (testharness.QueuedFixture, *recordFirstForge, *orchestratortest.Tracker, orchestrator.Reconciler, *orchestratortest.JobLogs) {
	t.Helper()
	filer := &orchestratortest.RecordingFiler{}
	fixture, forge, tracker, reconciler, logs := testharness.RedTargetSweep(t, filer)
	forge.Reading = buildFailing(7001)
	logs.Tail = untouchedLog
	wrapped := &recordFirstForge{CheckedForge: forge, t: t, store: fixture.Store}
	reconciler.Checks = wrapped
	// Main's own head passes the build, so without the further run the failure
	// would be handed back as the change's, as it was on October 31.
	reconciler.TargetChecks = &orchestratortest.TargetChecks{Reading: publish.BranchCheckReading{HeadCommit: "90cac74d0000000000000000000000000000beef", Passing: []string{"build"}}}
	return fixture, wrapped, tracker, reconciler, logs
}

func sweepOnce(t *testing.T, reconciler orchestrator.Reconciler) orchestrator.Reconciliation {
	t.Helper()
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want one", results)
	}
	return results[0]
}

// The failure confined to an untouched test is run once more, recorded before
// GitHub is asked; a sweep before the second run starts asks nothing new; the
// second run passing lets GitHub make the merge, with nothing withdrawn, nothing
// handed back, no repair spent, and the first failure kept on the run.
func TestGitHubsFailingJobIsRunOnceMoreWhenOnlyUntouchedTestsFailAndAPassLetsTheMergeLand(t *testing.T) {
	t.Parallel()

	fixture, forge, tracker, reconciler, _ := untouchedSweep(t)

	first := sweepOnce(t, reconciler)
	if first.Action != orchestrator.ActionQueued || !strings.Contains(first.Detail, "run the failing job once more") {
		t.Fatalf("first sweep = %#v, want the merge left queued with the job run once more", first)
	}
	if len(forge.Reruns) != 1 || forge.Reruns[0] != 7001 {
		t.Fatalf("re-runs = %v, want the build job asked once", forge.Reruns)
	}
	if len(forge.recorded) != 1 || !forge.recorded[0] {
		t.Fatalf("recorded before asking = %v, want the request written down before GitHub was asked", forge.recorded)
	}
	if len(forge.Withdrawn) != 0 || !forge.HoldsQueuedMerge() || tracker.Record().Blocked {
		t.Fatalf("withdrawn = %v, blocked = %t; want nothing withdrawn or handed back", forge.Withdrawn, tracker.Record().Blocked)
	}
	state := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	rerun := state.PullRequest.Checks.UntouchedRerun
	if rerun == nil || !rerun.Asked() || len(rerun.Tests.Tests) != 1 || rerun.Tests.Tests[0].Name != "TestConcurrentCreationSurvivesAGitCommandTheHarnessDidNotCompose" || rerun.Tests.Tests[0].Touched {
		t.Fatalf("further run = %#v, want the untouched test recorded and the request accepted", rerun)
	}

	// A reading still naming the failed run was taken before the second run
	// began, and spends nothing.
	again := sweepOnce(t, reconciler)
	if again.Action != orchestrator.ActionQueued || len(forge.Reruns) != 1 || len(forge.Withdrawn) != 0 {
		t.Fatalf("second sweep = %#v with re-runs %v; want it waiting on the second run", again, forge.Reruns)
	}

	// The second run passes, and GitHub makes the merge it was holding.
	forge.Reading = publish.CheckReading{Files: []string{"internal/orchestrator/pipeline.go"}, Passing: 4}
	if passing := sweepOnce(t, reconciler); passing.Action != orchestrator.ActionQueued {
		t.Fatalf("sweep on a passing second run = %#v, want the merge left to GitHub", passing)
	}
	kept := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if kept.PullRequest.Checks.UntouchedRerun == nil || kept.PullRequest.Checks.Red() {
		t.Fatalf("checks = %#v, want the passing reading with the first failure kept beside it", kept.PullRequest.Checks)
	}
	if kept.RepairAttempts != state.RepairAttempts || kept.CheckFailure != nil || kept.MergeDrop != nil {
		t.Fatalf("repairs %d -> %d, check failure %#v, drop %#v; want nothing spent or handed back", state.RepairAttempts, kept.RepairAttempts, kept.CheckFailure, kept.MergeDrop)
	}
	forge.PerformQueuedMerge(t)
	if landed := sweepOnce(t, reconciler); landed.Action == orchestrator.ActionBlocked {
		t.Fatalf("sweep after GitHub merged = %#v, want the landing settled", landed)
	}
	if len(forge.Reruns) != 1 {
		t.Fatalf("re-runs = %v, want exactly one", forge.Reruns)
	}
}

// A second run that fails too stops the run as before — here as the change's,
// since main's head passes — and the stop names the test and says the change
// does not touch it. No third run is asked for.
func TestASecondFailureOfGitHubsJobStopsTheRunNamingTheUntouchedTest(t *testing.T) {
	t.Parallel()

	_, forge, tracker, reconciler, _ := untouchedSweep(t)
	sweepOnce(t, reconciler)
	forge.Reading = buildFailing(7002)
	stopped := sweepOnce(t, reconciler)
	if stopped.Action != orchestrator.ActionBlocked {
		t.Fatalf("sweep after the second failure = %#v, want the merge withdrawn and the run stopped", stopped)
	}
	if len(forge.Reruns) != 1 || len(forge.Withdrawn) != 1 {
		t.Fatalf("re-runs = %v, withdrawn = %v; want one further run and the merge withdrawn", forge.Reruns, forge.Withdrawn)
	}
	reason := tracker.Record().BlockReason
	for _, want := range []string{
		"TestConcurrentCreationSurvivesAGitCommandTheHarnessDidNotCompose in github.com/mason-bryant/yoyodyne/internal/gitworktree",
		"the failing tests the change does not touch",
		"and it failed again",
		"failed on GitHub's second run of the job as well, so the failure is not put down to this change",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("blocker does not say %q:\n%s", want, reason)
		}
	}
}

// A failing test in a directory the change touches, a failure no test is named
// for, and a commit already given its further run before a restart cut the
// request off are each stopped as before, with no further run asked for.
func TestNoFurtherRunIsAskedWhenTheFailureIsNotConfinedToUntouchedTests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		setUp func(*recordFirstForge, *orchestratortest.JobLogs)
		says  string
	}{
		{"touched", func(forge *recordFirstForge, _ *orchestratortest.JobLogs) {
			forge.Reading = buildFailing(7001, "internal/gitworktree/worktree.go")
		}, "the failing tests the change touches: TestConcurrentCreationSurvivesAGitCommandTheHarnessDidNotCompose"},
		{"unnamed", func(_ *recordFirstForge, logs *orchestratortest.JobLogs) {
			logs.Tail = "panic: test timed out after 10m0s\nFAIL\tgithub.com/mason-bryant/yoyodyne/internal/gitworktree\t600.1s\n##[error]Process completed with exit code 2."
		}, "internal/gitworktree failed without naming a failing test"},
		{"refused", func(forge *recordFirstForge, _ *orchestratortest.JobLogs) {
			forge.RefuseRerun = errors.New("HTTP 403: Resource not accessible by integration")
		}, "GitHub would not run it once more"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, forge, tracker, reconciler, logs := untouchedSweep(t)
			c.setUp(forge, logs)
			stopped := sweepOnce(t, reconciler)
			if stopped.Action != orchestrator.ActionBlocked {
				t.Fatalf("sweep = %#v, want the run stopped as before", stopped)
			}
			if len(forge.Reruns) != 0 {
				t.Fatalf("re-runs = %v, want none", forge.Reruns)
			}
			if reason := tracker.Record().BlockReason; !strings.Contains(reason, c.says) {
				t.Errorf("blocker does not say %q:\n%s", c.says, reason)
			}
		})
	}
}

// A harness that restarted after writing the further run down and before
// GitHub answered finds it recorded and never asks again for the same commit.
func TestARestartBetweenRecordingAndAskingNeverAsksASecondTime(t *testing.T) {
	t.Parallel()

	fixture, forge, tracker, reconciler, _ := untouchedSweep(t)
	sweepOnce(t, reconciler)
	// The process stopped after the record was saved and before GitHub's answer
	// was: the record says the request was made and not that it was accepted.
	state := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	checks := *state.PullRequest.Checks
	rerun := *checks.UntouchedRerun
	rerun.AskedAt = nil
	checks.UntouchedRerun = &rerun
	published := *state.PullRequest
	published.Checks = &checks
	state.PullRequest = &published
	if err := fixture.Store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	forge.Reruns = nil

	stopped := sweepOnce(t, reconciler)
	if len(forge.Reruns) != 0 {
		t.Fatalf("re-runs = %v, want none asked a second time for the same commit", forge.Reruns)
	}
	if stopped.Action != orchestrator.ActionBlocked || !strings.Contains(tracker.Record().BlockReason, "cut off before GitHub answered") {
		t.Fatalf("sweep = %#v, blocker = %q; want the run stopped saying the request was cut off", stopped, tracker.Record().BlockReason)
	}
}

// queueDropped is GitHub's own merge queue having removed pull request 1563 on
// November 1: the build job failed on the queue's merge commit, in a test the
// change does not touch, while the pull request's own head passes.
func queueDropped(forge *recordFirstForge, id int64) {
	forge.Removal = publish.QueueRemoval{Events: []publish.QueueEvent{{Kind: "RemovedFromMergeQueueEvent"}}}
	forge.QueuedMerge = publish.QueuedMergeChecks{
		Commit:  "5505b7794135000000000000000000000000beef",
		Failing: []publish.FailedCheck{{Name: "build", Paths: []string{".github"}, ID: id, Conclusion: "failure"}},
	}
	forge.Reading = publish.CheckReading{Files: []string{"internal/orchestrator/pipeline.go"}, Passing: 4}
	forge.DropQueuedMerge()
}

// A request GitHub's merge queue dropped only for a test the change does not
// touch is queued once more, recorded first; a pass lets it land, and a second
// drop is settled as before, naming the test and not blaming the change.
func TestAMergeGitHubsQueueDroppedForAnUntouchedTestIsQueuedOnceMore(t *testing.T) {
	t.Parallel()

	for _, again := range []bool{false, true} {
		fixture, forge, tracker, reconciler, _ := untouchedSweep(t)
		reconciler.Drops = forge
		merges := len(forge.MergeRequests())
		queueDropped(forge, 8001)

		requeued := sweepOnce(t, reconciler)
		if requeued.Action != orchestrator.ActionQueued || !strings.Contains(requeued.Detail, "queued the merge once more") {
			t.Fatalf("again=%t: sweep = %#v, want the merge queued once more", again, requeued)
		}
		if len(forge.MergeRequests()) != merges+1 || !forge.HoldsQueuedMerge() || tracker.Record().Blocked {
			t.Fatalf("again=%t: merges %d -> %d, queued %t, blocked %t; want one more merge request and nothing handed back",
				again, merges, len(forge.MergeRequests()), forge.HoldsQueuedMerge(), tracker.Record().Blocked)
		}
		state := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
		rerun := state.PullRequest.QueueRerun
		if rerun == nil || !rerun.Asked() || rerun.HeadCommit != state.PullRequest.HeadCommit || rerun.CheckRuns[0] != 8001 {
			t.Fatalf("again=%t: further run = %#v, want it recorded against the request's head", again, rerun)
		}

		if !again {
			forge.PerformQueuedMerge(t)
			if landed := sweepOnce(t, reconciler); landed.Action == orchestrator.ActionBlocked {
				t.Fatalf("sweep after GitHub merged = %#v, want the landing settled", landed)
			}
			continue
		}
		queueDropped(forge, 8002)
		stopped := sweepOnce(t, reconciler)
		if stopped.Action != orchestrator.ActionBlocked || len(forge.MergeRequests()) != merges+1 {
			t.Fatalf("second drop: sweep = %#v with %d merge requests; want it settled as before and nothing asked again", stopped, len(forge.MergeRequests()))
		}
		reason := tracker.Record().BlockReason
		for _, want := range []string{"TestConcurrentCreationSurvivesAGitCommandTheHarnessDidNotCompose", "it failed again", "not put down to this change"} {
			if !strings.Contains(reason, want) {
				t.Errorf("blocker does not say %q:\n%s", want, reason)
			}
		}
	}
}

// A drop on a test the change touches is settled as before at once, naming it.
func TestAMergeGitHubsQueueDroppedForATouchedTestIsNotQueuedAgain(t *testing.T) {
	t.Parallel()

	_, forge, tracker, reconciler, _ := untouchedSweep(t)
	reconciler.Drops = forge
	merges := len(forge.MergeRequests())
	queueDropped(forge, 8001)
	forge.Reading.Files = []string{"internal/gitworktree/worktree.go"}
	if stopped := sweepOnce(t, reconciler); stopped.Action != orchestrator.ActionBlocked {
		t.Fatalf("sweep = %#v, want the drop settled as before", stopped)
	}
	if len(forge.MergeRequests()) != merges {
		t.Fatal("the merge was asked for again")
	}
	if reason := tracker.Record().BlockReason; !strings.Contains(reason, "the failing tests the change touches") {
		t.Errorf("blocker does not name the touched test:\n%s", reason)
	}
}

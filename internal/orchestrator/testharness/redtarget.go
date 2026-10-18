package testharness

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// RedTargetGoal is the goal the fixture's item served, which the filed item is
// attributed to.
const RedTargetGoal = "Run development nearly autonomously."

// RedTargetReading is pull request 863's shape on 2026-09-28: the head level
// with main, a failing check on a file under .github the change does not touch.
func RedTargetReading() publish.CheckReading {
	return publish.CheckReading{
		Files:   []string{"feature.txt"},
		Failing: []publish.FailedCheck{{Name: "adoption", Paths: []string{".github/workflows/adoption.yml"}, ID: 4215, Conclusion: "failure"}},
		Passing: 3,
	}
}

// RedTargetSweep is a run whose merge is queued on a protected main, whose item
// names the goal it served, swept by a reconciler that can file work and read
// a job's log.
func RedTargetSweep(t *testing.T, filer *orchestratortest.RecordingFiler) (QueuedFixture, *orchestratortest.CheckedForge, *orchestratortest.Tracker, orchestrator.Reconciler, *orchestratortest.JobLogs) {
	t.Helper()
	fixture := NewQueuedFixture(t)
	tracker := fixture.Tracker.(*orchestratortest.Tracker)
	tracker.Item.Notes = goal.Note(RedTargetGoal)
	fixture.Forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "ruleset"})
	outcome := fixture.Run(t)
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	forge := &orchestratortest.CheckedForge{Forge: fixture.Forge}
	forge.Reading = RedTargetReading()
	fixture.Docket = &MemoryDocket{}
	reconciler := fixture.Sweep(t, forge, true)
	logs := &orchestratortest.JobLogs{Tail: "--- FAIL: TestAdoption (0.01s)\n    adoption_test.go:12: bd is not installed"}
	reconciler.Filer = filer
	reconciler.JobLogs = logs
	return fixture, forge, tracker, reconciler, logs
}

// PullRequest907Reading is pull request 907's shape on 2026-09-29, for the
// machine home (yoyodyne-ifd.434.12): the head level with main, and the build
// check failing on a test in a package the change adds, reported per package,
// with no annotation but the forge's own on .github.
func PullRequest907Reading() publish.CheckReading {
	return publish.CheckReading{
		Files: []string{"docs/operations.md", "internal/machinehome/home.go", "internal/machinehome/home_test.go"},
		Failing: []publish.FailedCheck{{
			Name: "build", Paths: []string{".github"}, ID: 9070, Conclusion: "failure",
			Annotations: []publish.Annotation{{Path: ".github", Level: "failure", Message: "Process completed with exit code 2."}},
		}},
		Passing: 3,
	}
}

// PullRequest907Log is the tail of the build job's log: the failing package is
// named by its import path, and the failing test's file by its base name only.
const PullRequest907Log = "--- FAIL: TestTheMachineHomeIsResolvedOnce (0.00s)\n" +
	"    home_test.go:31: home = \"\", want the resolved directory\n" +
	"FAIL\n" +
	"FAIL\tgithub.com/mason-bryant/yoyodyne/internal/machinehome\t0.012s\n" +
	"ok  \tgithub.com/mason-bryant/yoyodyne/internal/orchestrator\t41.2s\n" +
	"FAIL\n" +
	"make: *** [test] Error 1\n" +
	"##[error]Process completed with exit code 2."

// AssertHandedBackToTheChange asserts the case of pull request 907 settled as
// the change's own failure: the merge withdrawn and handed back to be repaired,
// nothing filed, and no wait on the target recorded.
func AssertHandedBackToTheChange(t *testing.T, fixture QueuedFixture, forge *orchestratortest.CheckedForge, tracker *orchestratortest.Tracker, filer *orchestratortest.RecordingFiler, results []orchestrator.Reconciliation, wants ...string) {
	t.Helper()
	if len(results) != 1 || results[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the red merge handed back to its change", results)
	}
	if len(filer.Filed) != 0 {
		t.Fatalf("filed = %#v, want nothing filed against main", filer.Filed)
	}
	if len(forge.Withdrawn) != 1 || forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v, queued = %t; want the queued merge withdrawn", forge.Withdrawn, forge.HoldsQueuedMerge())
	}
	if len(tracker.Blockers) != 0 {
		t.Errorf("the item waits on %v, want it waiting on nothing filed for main", tracker.Blockers)
	}
	record := tracker.Record()
	if !record.Blocked {
		t.Fatal("the change's own failure was not handed back")
	}
	for _, want := range append([]string{"fail on this change", "the failure is this change's own rather than main's", "nothing was filed against main", "needs its change repaired"}, wants...) {
		if !strings.Contains(record.BlockReason, want) {
			t.Errorf("blocker does not say %q:\n%s", want, record.BlockReason)
		}
	}
	settled := LoadRun(t, fixture.Store, PipelineRunID)
	if settled.PullRequest.TargetRed != nil || settled.WaitingOnRedTarget() || settled.MergeDrop == nil {
		t.Fatalf("record = target red %#v, drop %#v; want a dropped merge and no wait on main", settled.PullRequest.TargetRed, settled.MergeDrop)
	}
	if settled.PullRequest.Checks == nil || len(settled.PullRequest.Checks.Failing) != 1 {
		t.Errorf("checks = %#v, want the reading that decided it kept on the publication", settled.PullRequest.Checks)
	}
}

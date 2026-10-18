package orchestrator

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// These drive yoyodyne-m5p: a queued merge whose checks fail with its head
// level with the target, on a file the change does not touch, is the target's
// failure. It is filed as the target's, as a red landing is, and the merge waits
// on the item filed for it with the harness as the one to move, rather than
// being handed to a person.

// redTargetGoal is the goal the fixture's item served, which the filed item is
// attributed to.
const redTargetGoal = "Run development nearly autonomously."

// redTargetReading is pull request 863's shape on 2026-09-28: the head level
// with main, a failing check on a file under .github the change does not touch.
func redTargetReading() publish.CheckReading {
	return publish.CheckReading{
		Files:   []string{"feature.txt"},
		Failing: []publish.FailedCheck{{Name: "adoption", Paths: []string{".github/workflows/adoption.yml"}, ID: 4215, Conclusion: "failure"}},
		Passing: 3,
	}
}

// redTargetSweep is a run whose merge is queued on a protected main, whose item
// names the goal it served, swept by a reconciler that can file work and read
// a job's log.
func redTargetSweep(t *testing.T, filer *orchestratortest.RecordingFiler) (queuedFixture, *orchestratortest.CheckedForge, *orchestratortest.Tracker, Reconciler, *orchestratortest.JobLogs) {
	t.Helper()
	fixture := newQueuedFixture(t)
	tracker := fixture.tracker.(*orchestratortest.Tracker)
	tracker.Item.Notes = goal.Note(redTargetGoal)
	fixture.forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "ruleset"})
	outcome := fixture.run(t)
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	forge := &orchestratortest.CheckedForge{Forge: fixture.forge}
	forge.Reading = redTargetReading()
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, true)
	logs := &orchestratortest.JobLogs{Tail: "--- FAIL: TestAdoption (0.01s)\n    adoption_test.go:12: bd is not installed"}
	reconciler.Filer = filer
	reconciler.JobLogs = logs
	return fixture, forge, tracker, reconciler, logs
}

// A head level with main failing a check on a file its change does not touch
// files one p0 bug for main's check, under the goal the item served and carrying
// the job's log; the queued merge is withdrawn and waits on that bug; and nothing
// is handed to a person — no blocker, no dropped merge, and the docket names the
// harness as the one to move.
func TestAQueuedHeadLevelWithItsTargetFailingAnUntouchedFileWaitsOnTheTargetsFiledItem(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{}
	fixture, forge, tracker, reconciler, logs := redTargetSweep(t, filer)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionWaitingOnTarget || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the merge waiting on the target's red check", results)
	}
	if len(forge.Withdrawn) != 1 || forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v, queued = %t; want the queued merge withdrawn", forge.Withdrawn, forge.HoldsQueuedMerge())
	}

	// Filed once, as a red landing is: a p0 bug naming the branch, the commit,
	// the check, and the request that met it, under the item's goal.
	if len(filer.Filed) != 1 {
		t.Fatalf("filed = %#v, want one item for main's red check", filer.Filed)
	}
	filed := filer.Filed[0]
	if filed.Type != "bug" || filed.Priority == nil || *filed.Priority != 0 {
		t.Errorf("filed type %q priority %v, want a p0 bug", filed.Type, filed.Priority)
	}
	recorded := loadRun(t, fixture.store, pipelineRunID)
	for _, want := range []string{"adoption", "main", "pull request " + strconv.Itoa(recorded.PullRequest.Number), shortCommit(recorded.PullRequest.HeadCommit), pipelineRunID} {
		if !strings.Contains(filed.Title+filed.Description, want) {
			t.Errorf("the filed item does not name %q:\n%s\n%s", want, filed.Title, filed.Description)
		}
	}
	for _, want := range []string{redTargetMarker("main", "adoption"), goal.Note(redTargetGoal), "How the forge ended adoption: failure", "> --- FAIL: TestAdoption", "> " + "    adoption_test.go:12: bd is not installed"} {
		if !strings.Contains(filed.Notes, want) {
			t.Errorf("the filed item's notes do not carry %q:\n%s", want, filed.Notes)
		}
	}
	// The log is read to decide whose failure it is, and again for the account
	// the item carries; nothing else is read.
	if len(logs.Asked) == 0 {
		t.Errorf("job logs asked = %v, want the failing job's log read", logs.Asked)
	}
	for _, asked := range logs.Asked {
		if asked != 4215 {
			t.Errorf("job logs asked = %v, want only the failing job's log read", logs.Asked)
		}
	}

	// The merge waits on the filed item, and nothing is handed to a person.
	record := tracker.Record()
	if record.Blocked || record.Closed {
		t.Fatalf("blocked = %t, closed = %t; the target's failure hands nothing to a person", record.Blocked, record.Closed)
	}
	if len(tracker.Blockers) != 1 || tracker.Blockers[0] != "yoyodyne-red-1" {
		t.Errorf("the item waits on %v, want the filed item", tracker.Blockers)
	}
	if !strings.Contains(record.Notes, "fail on the target branch itself") || !strings.Contains(record.Notes, "yoyodyne-red-1") {
		t.Errorf("the item was not told it waits on the target's filed item:\n%s", record.Notes)
	}
	if recorded.MergeDrop != nil || recorded.Blocker != "" || recorded.PullRequest.MergeQueued {
		t.Fatalf("record = drop %#v, blocker %q, queued %t; want no drop and no blocker, and the merge withdrawn", recorded.MergeDrop, recorded.Blocker, recorded.PullRequest.MergeQueued)
	}
	if !recorded.WaitingOnRedTarget() || recorded.PullRequest.TargetRed.WaitingOn()[0] != "yoyodyne-red-1" {
		t.Fatalf("target red = %#v, want the publication waiting on the filed item", recorded.PullRequest.TargetRed)
	}

	// The docket carries it as the harness's, and puts no stoppage to anybody.
	docketer := docketerOverStore(fixture.docket, fixture.store, docketConfig())
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var publication triage.Entry
	for _, entry := range fixture.docket.entries {
		switch entry.Class {
		case triage.ClassPublication:
			publication = entry
		case triage.ClassStoppedRun:
			t.Errorf("docket = %#v; a merge waiting on the target is not a stoppage anybody decides", entry)
		}
	}
	if publication.Publication == nil {
		t.Fatalf("docket = %#v, want the publication docketed", fixture.docket.entries)
	}
	rendered := publication.Render()
	for _, want := range []string{"Waiting on the target", "adoption (filed as yoyodyne-red-1)", "Next mover: the harness"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("docket entry does not say %q:\n%s", want, rendered)
		}
	}

	// A second sweep while the item is open waits and files nothing.
	tracker.AlsoHolds = map[string]beads.WorkItem{"yoyodyne-red-1": {ID: "yoyodyne-red-1", Status: "open"}}
	waiting, err := reconciler.ResumeRedTargets(context.Background())
	if err != nil {
		t.Fatalf("ResumeRedTargets() error = %v", err)
	}
	if len(waiting) != 1 || waiting[0].Action != ActionWaitingOnTarget || !strings.Contains(waiting[0].Detail, "still waits on yoyodyne-red-1") {
		t.Fatalf("resumptions = %#v, want the publication still waiting on its open item", waiting)
	}
	if len(filer.Filed) != 1 {
		t.Errorf("filed = %d items, want nothing filed again while the first is open", len(filer.Filed))
	}
}

// A later request meeting the same red check finds the item already open for it
// and is noted on it, rather than filing a second beside it.
func TestALaterRequestMeetingTheSameRedCheckIsNotedOnTheOpenItem(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{Open: []beads.WorkItem{{
		ID:     "yoyodyne-red-earlier",
		Status: "open",
		Notes:  "Filed by the harness for adoption red on main.\n" + redTargetMarker("main", "adoption"),
	}}}
	fixture, forge, tracker, reconciler, _ := redTargetSweep(t, filer)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionWaitingOnTarget {
		t.Fatalf("reconciliation = %#v, want the merge waiting on the target's red check", results)
	}
	if len(filer.Filed) != 0 {
		t.Fatalf("filed = %#v, want the open item noted rather than a second filed", filer.Filed)
	}
	if len(forge.Withdrawn) != 1 {
		t.Errorf("withdrawn = %v, want the queued merge withdrawn", forge.Withdrawn)
	}
	if !strings.Contains(tracker.Record().Notes, "Red again on main") {
		t.Errorf("the open item was not told about this request:\n%s", tracker.Record().Notes)
	}
	if len(tracker.Blockers) != 1 || tracker.Blockers[0] != "yoyodyne-red-earlier" {
		t.Errorf("the item waits on %v, want the item already open for the check", tracker.Blockers)
	}
	recorded := loadRun(t, fixture.store, pipelineRunID)
	check := recorded.PullRequest.TargetRed.Checks[0]
	if check.WorkItem != "yoyodyne-red-earlier" || !check.FiledEarlier {
		t.Errorf("target red check = %#v, want the earlier item named as filed earlier", check)
	}
}

// Once the filed item closes, the fix having landed on main leaves the head
// behind it: the sweep brings it up to date from the kept branch, checks and
// reviews it again, and queues its merge again, as a queued head behind its
// target is — rather than re-arming a head the target has moved past.
func TestAWaitOnTheTargetWhoseItemClosedIsBroughtUpToDateWhereTheFixLeftTheHeadBehind(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{}
	fixture, forge, tracker, reconciler, _ := redTargetSweep(t, filer)
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// The fix lands on main and its item closes, which the tracker also says on
	// the edge the item was made to wait on.
	driftRemoteTarget(t, fixture.remote, "main")
	tracker.AlsoHolds = map[string]beads.WorkItem{"yoyodyne-red-1": {ID: "yoyodyne-red-1", Status: "closed"}}
	for index := range tracker.Item.Dependencies {
		tracker.Item.Dependencies[index].Status = "closed"
	}
	forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 4, BehindBy: 1}

	resumed, err := reconciler.ResumeRedTargets(context.Background())
	if err != nil {
		t.Fatalf("ResumeRedTargets() error = %v", err)
	}
	if len(resumed) != 1 || resumed[0].Action != ActionUpdating || resumed[0].Failure != "" {
		t.Fatalf("resumptions = %#v, want the head put back at its promotion", resumed)
	}
	live := loadRun(t, fixture.store, pipelineRunID)
	if !updatingQueuedHead(live) || live.PullRequest.TargetRed != nil || live.PublishFailure != "" {
		t.Fatalf("record = status %q, target red %#v, publish failure %q; want a live run at its promotion with the wait ended",
			live.Status, live.PullRequest.TargetRed, live.PublishFailure)
	}
	if !strings.Contains(tracker.Record().Notes, "every item the merge of pull request") {
		t.Errorf("the item was not told why it is being brought up to date:\n%s", tracker.Record().Notes)
	}

	updates, err := reconciler.ContinueUpdates(context.Background())
	if err != nil {
		t.Fatalf("ContinueUpdates() error = %v", err)
	}
	if len(updates) != 1 || !updates[0].Continued || updates[0].Outcome == nil || updates[0].Outcome.PullRequest == nil || !updates[0].Outcome.PullRequest.MergeQueued {
		t.Fatalf("updates = %#v, want the replayed change's merge queued again", updates)
	}
	if tracker.Record().Blocked {
		t.Error("the item was handed to a person on the way")
	}
}

// These drive yoyodyne-c02: a check red on a level head is filed as the
// target's only once it is confirmed the target's, so a failing test the change
// itself adds goes back to the change rather than being filed against main.

// pullRequest907Reading is pull request 907's shape on 2026-09-29, for the
// machine home (yoyodyne-ifd.434.12): the head level with main, and the build
// check failing on a test in a package the change adds, reported per package,
// with no annotation but the forge's own on .github.
func pullRequest907Reading() publish.CheckReading {
	return publish.CheckReading{
		Files: []string{"docs/operations.md", "internal/machinehome/home.go", "internal/machinehome/home_test.go"},
		Failing: []publish.FailedCheck{{
			Name: "build", Paths: []string{".github"}, ID: 9070, Conclusion: "failure",
			Annotations: []publish.Annotation{{Path: ".github", Level: "failure", Message: "Process completed with exit code 2."}},
		}},
		Passing: 3,
	}
}

// pullRequest907Log is the tail of the build job's log: the failing package is
// named by its import path, and the failing test's file by its base name only.
const pullRequest907Log = "--- FAIL: TestTheMachineHomeIsResolvedOnce (0.00s)\n" +
	"    home_test.go:31: home = \"\", want the resolved directory\n" +
	"FAIL\n" +
	"FAIL\tgithub.com/mason-bryant/yoyodyne/internal/machinehome\t0.012s\n" +
	"ok  \tgithub.com/mason-bryant/yoyodyne/internal/orchestrator\t41.2s\n" +
	"FAIL\n" +
	"make: *** [test] Error 1\n" +
	"##[error]Process completed with exit code 2."

// assertHandedBackToTheChange asserts the case of pull request 907 settled as
// the change's own failure: the merge withdrawn and handed back to be repaired,
// nothing filed, and no wait on the target recorded.
func assertHandedBackToTheChange(t *testing.T, fixture queuedFixture, forge *orchestratortest.CheckedForge, tracker *orchestratortest.Tracker, filer *orchestratortest.RecordingFiler, results []Reconciliation, wants ...string) {
	t.Helper()
	if len(results) != 1 || results[0].Action != ActionBlocked {
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
	settled := loadRun(t, fixture.store, pipelineRunID)
	if settled.PullRequest.TargetRed != nil || settled.WaitingOnRedTarget() || settled.MergeDrop == nil {
		t.Fatalf("record = target red %#v, drop %#v; want a dropped merge and no wait on main", settled.PullRequest.TargetRed, settled.MergeDrop)
	}
	if settled.PullRequest.Checks == nil || len(settled.PullRequest.Checks.Failing) != 1 {
		t.Errorf("checks = %#v, want the reading that decided it kept on the publication", settled.PullRequest.Checks)
	}
}

// A path is named whole: not inside a longer name, and a directory at the top of
// the repository only as part of a path, so an ordinary word is not read as it.
func TestMentionsPathNamesAPathWhole(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		text, name string
		asPath     bool
		want       bool
	}{
		{"FAIL\tgithub.com/acme/thing/internal/machinehome\t0.01s", "internal/machinehome", false, true},
		{"FAIL\tgithub.com/acme/thing/internal/machinehome\t0.01s", "internal/machine", false, false},
		{"see internal/machinehome/home_test.go:31", "internal/machinehome", false, true},
		{"broken in internal/machinehome.", "internal/machinehome", false, true},
		{"the docs are fine", "docs", true, false},
		{"FAIL\tgithub.com/acme/thing/cmd [build failed]", "cmd", true, true},
		{"make: *** [Makefile:12: test] Error 1", "Makefile", false, true},
		{"xfeature.txt", "feature.txt", false, false},
	} {
		if got := mentionsPath(tc.text, tc.name, tc.asPath); got != tc.want {
			t.Errorf("mentionsPath(%q, %q, %t) = %t, want %t", tc.text, tc.name, tc.asPath, got, tc.want)
		}
	}
}

package landingtests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive yoyodyne-m5p: a queued merge whose checks fail with its head
// level with the target, on a file the change does not touch, is the target's
// failure. It is filed as the target's, as a red landing is, and the merge waits
// on the item filed for it with the harness as the one to move, rather than
// being handed to a person.

// A filing the tracker refuses leaves the merge queued and writes nothing, so
// the next sweep files it: the merge is never withdrawn to wait on nothing.
func TestAFilingThatFailsLeavesTheMergeQueuedForTheNextSweep(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{Refuse: errors.New("bd create timed out")}
	fixture, forge, tracker, reconciler, _ := testharness.RedTargetSweep(t, filer)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != orchestrator.ActionQueued || !strings.Contains(results[0].Detail, "could not be filed") {
		t.Fatalf("reconciliation = %#v, want the merge left queued saying the filing failed", results)
	}
	if len(forge.Withdrawn) != 0 || !forge.HoldsQueuedMerge() || tracker.Record().Blocked {
		t.Fatalf("withdrawn = %v, blocked = %t; a filing that failed withdraws nothing and hands nothing back", forge.Withdrawn, tracker.Record().Blocked)
	}
	if recorded := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID); !recorded.PullRequest.MergeQueued || recorded.PullRequest.TargetRed != nil {
		t.Errorf("record = queued %t, target red %#v; want the merge still recorded as queued", recorded.PullRequest.MergeQueued, recorded.PullRequest.TargetRed)
	}
}

// redTargetRearmForge is the forge a re-arm reads: nothing unmet on the request,
// and the head's checks as the fixture sets them.
type redTargetRearmForge struct {
	*orchestratortest.CheckedForge
}

func (redTargetRearmForge) MergeState(context.Context, int) (string, error) {
	return "CLEAN", nil
}

// A failed read after the target's item closes keeps the wait. A later passing
// reading replaces the error while the watch still owes the re-arm, and keeps
// the re-runs spent on the same head.
func TestAClosedRedTargetWaitReplacesUnreadChecksBeforeTheWatchRearms(t *testing.T) {
	t.Parallel()
	for _, sameHead := range []bool{true, false} {
		name := "same head"
		if !sameHead {
			name = "new head"
		}
		t.Run(name, func(t *testing.T) {
			fixture, forge, tracker, reconciler, _ := testharness.RedTargetSweep(t, &orchestratortest.RecordingFiler{})
			if _, err := reconciler.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			prior := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
			prior.PullRequest.Checks.Reruns = 1
			prior.PullRequest.Checks.RerunChecks = []int64{4215}
			if err := fixture.Store.Save(prior); err != nil {
				t.Fatal(err)
			}
			tracker.AlsoHolds = map[string]beads.WorkItem{"yoyodyne-red-1": {ID: "yoyodyne-red-1", Status: "closed"}}
			forge.ReadError = errors.New("decode the comparison: unexpected end of JSON input")
			resumed, err := reconciler.ResumeRedTargets(context.Background())
			if err != nil || len(resumed) != 1 || resumed[0].Action != orchestrator.ActionWaitingOnTarget {
				t.Fatalf("failed reading: ResumeRedTargets() = %#v, %v", resumed, err)
			}
			unread := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
			if unread.PullRequest.Checks.ReadError != forge.ReadError.Error() || !unread.WaitingOnRedTarget() {
				t.Fatalf("failed reading = %#v, want the error recorded and the wait kept", unread.PullRequest)
			}

			forge.ReadError = nil
			head := prior.PullRequest.Checks.HeadCommit
			if !sameHead {
				head = strings.Repeat("b", 40)
			}
			forge.Reading = publish.CheckReading{HeadCommit: head, Files: []string{"feature.txt"}, Passing: 4}
			resumed, err = reconciler.ResumeRedTargets(context.Background())
			if err != nil || len(resumed) != 1 || resumed[0].Action != orchestrator.ActionWaitingOnTarget || !strings.Contains(resumed[0].Detail, "re-arm carry-out") {
				t.Fatalf("passing reading: ResumeRedTargets() = %#v, %v", resumed, err)
			}
			recovered := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
			checks := recovered.PullRequest.Checks
			if checks.ReadError != "" || checks.HeadCommit != head || checks.Passing != 4 || len(checks.Failing) != 0 {
				t.Fatalf("passing reading = %#v, want fresh checks replacing the unread state", checks)
			}
			if sameHead && (checks.Reruns != 1 || len(checks.RerunChecks) != 1 || checks.RerunChecks[0] != 4215) {
				t.Fatalf("same head's re-runs = %#v, want the spent re-run preserved", checks)
			}
			if !sameHead && (checks.Reruns != 0 || len(checks.RerunChecks) != 0) {
				t.Fatalf("new head's re-runs = %#v, want no re-runs carried from the old head", checks)
			}
			if !recovered.WaitingOnRedTarget() || recovered.PullRequest.MergeQueued || recovered.MergeDrop != nil || tracker.Record().Blocked {
				t.Fatalf("publication = %#v, want it still waiting for the harness to re-arm", recovered.PullRequest)
			}
		})
	}
}

// Once the filed item closes on a head still level with main whose checks now
// pass, the watch's re-arm carry-out arms the merge with nobody deciding, and
// spends no re-arm. While the item is open the Rearmer refuses and says what it
// waits on.
func TestTheWatchRearmsAMergeWaitingOnTheTargetOnceItsItemClosesWithNoDecision(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{}
	fixture, forge, tracker, reconciler, _ := testharness.RedTargetSweep(t, filer)
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	docketer := testharness.DocketerOverStore(fixture.Docket, fixture.Store, testharness.DocketConfig())
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	rearmer := orchestrator.Rearmer{
		Docket:    fixture.Docket,
		Runs:      fixture.Store,
		Forge:     redTargetRearmForge{forge},
		Checks:    forge,
		Worktrees: testharness.NewSweepManager(t, fixture.Repository, fixture.WorktreeRoot),
		Decisions: fixture.Store.Triage(),
		Items:     tracker,
	}
	carry := orchestrator.CarryOut{
		Docket:    fixture.Docket,
		Decisions: fixture.Store.Triage(),
		Reruns:    fixture.Store.Reruns(),
		Runs:      fixture.Store,
		Rearmer:   rearmer,
		Items:     tracker,
	}

	// The item is open: nothing is attempted, and the verb refuses naming it.
	tracker.AlsoHolds = map[string]beads.WorkItem{"yoyodyne-red-1": {ID: "yoyodyne-red-1", Status: "open"}}
	if carried, err := carry.CarryRearms(context.Background(), false); err != nil || len(carried) != 0 {
		t.Fatalf("CarryRearms() = %#v, %v; want nothing attempted while the item is open", carried, err)
	}
	if _, err := rearmer.Rearm(context.Background(), orchestrator.RearmRequest{Run: testharness.PipelineRunID, Reason: "asked"}); err == nil || !strings.Contains(err.Error(), "waits on yoyodyne-red-1") {
		t.Fatalf("Rearm() error = %v, want it refused naming the open item", err)
	}

	// The item closes and the head, still level, passes.
	tracker.AlsoHolds["yoyodyne-red-1"] = beads.WorkItem{ID: "yoyodyne-red-1", Status: "closed"}
	forge.Reading = publish.CheckReading{HeadCommit: forge.Reading.HeadCommit, Files: []string{"feature.txt"}, Passing: 4}
	merges := len(forge.MergeRequests())
	carried, err := carry.CarryRearms(context.Background(), false)
	if err != nil {
		t.Fatalf("CarryRearms() error = %v", err)
	}
	if len(carried) != 1 || !carried[0].Carried {
		t.Fatalf("carried = %#v, want the merge armed again by the harness", carried)
	}
	if len(forge.MergeRequests()) != merges+1 {
		t.Fatalf("merge requests = %d, want one arming", len(forge.MergeRequests())-merges)
	}
	armed := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if !armed.PullRequest.MergeQueued || armed.PullRequest.TargetRed != nil || armed.PublishFailure != "" || armed.PullRequest.MergeRearms != 0 {
		t.Fatalf("record = queued %t, target red %#v, publish failure %q, re-arms %d; want the merge queued again, the wait ended, and no re-arm spent",
			armed.PullRequest.MergeQueued, armed.PullRequest.TargetRed, armed.PublishFailure, armed.PullRequest.MergeRearms)
	}
	if tracker.Record().Blocked {
		t.Error("the item was handed to a person on the way")
	}
}

// A closed wait whose level head is still red, but only on a job the forge ended
// itself, is neither the target's failure to file again nor a head the watch
// can arm: the job is run again within its bound, the wait standing meanwhile,
// and a job ended again past the bound is handed back as a queued merge's is —
// never left for a re-arm that its own checks gate would refuse forever.
func TestAClosedWaitWhoseHeadTheForgeEndedIsRunAgainThenHandedBack(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{}
	fixture, forge, tracker, reconciler, _ := testharness.RedTargetSweep(t, filer)
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	tracker.AlsoHolds = map[string]beads.WorkItem{"yoyodyne-red-1": {ID: "yoyodyne-red-1", Status: "closed"}}

	for sweep := 1; sweep <= runstate.MaxCheckReruns; sweep++ {
		checkRun := int64(5300 + sweep)
		forge.Reading = testharness.JobFailure(checkRun)
		resumed, err := reconciler.ResumeRedTargets(context.Background())
		if err != nil {
			t.Fatalf("sweep %d: ResumeRedTargets() error = %v", sweep, err)
		}
		if len(resumed) != 1 || resumed[0].Action != orchestrator.ActionWaitingOnTarget || !strings.Contains(resumed[0].Detail, "asked it to run them again") {
			t.Fatalf("sweep %d: resumptions = %#v, want the ended job run again and the wait kept", sweep, resumed)
		}
		if len(forge.Reruns) != sweep || forge.Reruns[sweep-1] != checkRun {
			t.Fatalf("sweep %d: re-runs = %v, want check run %d run again", sweep, forge.Reruns, checkRun)
		}
		if waiting := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID); !waiting.WaitingOnRedTarget() || tracker.Record().Blocked {
			t.Fatalf("sweep %d: waiting = %t, blocked = %t; a job being run again keeps the wait and hands nothing back", sweep, waiting.WaitingOnRedTarget(), tracker.Record().Blocked)
		}
	}

	forge.Reading = testharness.JobFailure(5399)
	resumed, err := reconciler.ResumeRedTargets(context.Background())
	if err != nil {
		t.Fatalf("ResumeRedTargets() error = %v", err)
	}
	if len(resumed) != 1 || resumed[0].Action != orchestrator.ActionBlocked {
		t.Fatalf("resumptions = %#v, want the merge handed back once the re-runs are spent", resumed)
	}
	if len(forge.Reruns) != runstate.MaxCheckReruns || len(filer.Filed) != 1 {
		t.Errorf("re-runs = %v, filed = %d; want no re-run past the bound and nothing filed again", forge.Reruns, len(filer.Filed))
	}
	handed := testharness.LoadRun(t, fixture.Store, testharness.PipelineRunID)
	if handed.WaitingOnRedTarget() || handed.PullRequest.TargetRed != nil || handed.MergeDrop == nil {
		t.Fatalf("record = target red %#v, drop %#v; want the wait ended and the drop recorded", handed.PullRequest.TargetRed, handed.MergeDrop)
	}
	if !tracker.Record().Blocked || !strings.Contains(tracker.Record().BlockReason, "ended by the forge before any step failed") {
		t.Errorf("blocked = %t, reason = %q; want it handed back saying the forge ended the job", tracker.Record().Blocked, tracker.Record().BlockReason)
	}
}

// These drive yoyodyne-c02: a check red on a level head is filed as the
// target's only once it is confirmed the target's, so a failing test the change
// itself adds goes back to the change rather than being filed against main.

// Pull request 907, where the forge cannot say how main's own head fared: the
// build check's log names the package the change adds, so the failure is the
// change's, handed back to it, and nothing is filed against main.
func TestAFailingTestInAPackageTheChangeAddsIsHandedBackToTheChangeAndNothingIsFiled(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{}
	fixture, forge, tracker, reconciler, logs := testharness.RedTargetSweep(t, filer)
	forge.Reading = testharness.PullRequest907Reading()
	logs.Tail = testharness.PullRequest907Log

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	testharness.AssertHandedBackToTheChange(t, fixture, forge, tracker, filer, results,
		"nothing is wired to this harness to read the target's own checks",
		"the forge's account of build names internal/machinehome, which this change adds or modifies")
	if len(logs.Asked) == 0 || logs.Asked[0] != 9070 {
		t.Errorf("job logs asked = %v, want the build job's log read", logs.Asked)
	}

	// A forge that refuses the target's reading is the same case.
	filer = &orchestratortest.RecordingFiler{}
	fixture, forge, tracker, reconciler, logs = testharness.RedTargetSweep(t, filer)
	forge.Reading = testharness.PullRequest907Reading()
	logs.Tail = testharness.PullRequest907Log
	reconciler.TargetChecks = &orchestratortest.TargetChecks{Refuse: errors.New("gh: HTTP 502")}
	results, err = reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	testharness.AssertHandedBackToTheChange(t, fixture, forge, tracker, filer, results, "could not say how the checks ended on main's own head")
}

// Pull request 907, where the forge can say: the build check passes on main's
// own head, so it is the change's however little its log names.
func TestACheckThatPassesOnTheTargetsOwnHeadIsTheChangesAndNothingIsFiled(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{}
	fixture, forge, tracker, reconciler, logs := testharness.RedTargetSweep(t, filer)
	forge.Reading = testharness.PullRequest907Reading()
	logs.Tail = "##[error]Process completed with exit code 2."
	main := &orchestratortest.TargetChecks{Reading: publish.BranchCheckReading{HeadCommit: "90cac74d0000000000000000000000000000beef", Passing: []string{"build", "vet"}}}
	reconciler.TargetChecks = main

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	testharness.AssertHandedBackToTheChange(t, fixture, forge, tracker, filer, results, "build passes on main's own head, 90cac74d0000")
	if len(main.Asked) != 1 || main.Asked[0] != "main" {
		t.Errorf("target checks asked of %v, want main's", main.Asked)
	}
}

// A check the forge reports red on main's own head as well is main's, and is
// filed as main's saying so — even where the change touches the directory its
// log names, because the target's own head is the better witness.
func TestACheckRedOnTheTargetsOwnHeadIsFiledAsTheTargetsSayingSo(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{}
	_, forge, tracker, reconciler, logs := testharness.RedTargetSweep(t, filer)
	forge.Reading = testharness.PullRequest907Reading()
	logs.Tail = testharness.PullRequest907Log
	reconciler.TargetChecks = &orchestratortest.TargetChecks{Reading: publish.BranchCheckReading{HeadCommit: "90cac74d0000000000000000000000000000beef", Failing: []string{"build"}, Passing: []string{"vet"}}}

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != orchestrator.ActionWaitingOnTarget {
		t.Fatalf("reconciliation = %#v, want the merge waiting on main's red check", results)
	}
	if len(filer.Filed) != 1 || !strings.Contains(filer.Filed[0].Notes, "The forge reports build red on main's own head, 90cac74d0000, as well.") {
		t.Fatalf("filed = %#v, want one item for main saying the forge confirmed it on main's head", filer.Filed)
	}
	if tracker.Record().Blocked {
		t.Error("main's failure was handed back to the change")
	}
}

// Where main's own head cannot be read and the log names nothing of the change,
// the check is filed as main's as it was before, and the item says the harness
// could not confirm it on main's head.
func TestAnUnconfirmedCheckWhoseLogNamesNothingOfTheChangeIsFiledSayingSo(t *testing.T) {
	t.Parallel()

	filer := &orchestratortest.RecordingFiler{}
	_, _, tracker, reconciler, _ := testharness.RedTargetSweep(t, filer)
	reconciler.TargetChecks = &orchestratortest.TargetChecks{Reading: publish.BranchCheckReading{HeadCommit: "90cac74d0000000000000000000000000000beef", Pending: []string{"adoption"}}}

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != orchestrator.ActionWaitingOnTarget || tracker.Record().Blocked {
		t.Fatalf("reconciliation = %#v, want the merge waiting on main's red check", results)
	}
	if len(filer.Filed) != 1 || !strings.Contains(filer.Filed[0].Notes, "Whether adoption is red on main's own head was not confirmed, because adoption has not finished on main's own head") {
		t.Fatalf("filed = %#v, want the item to say the check was not confirmed on main's head", filer.Filed)
	}
}

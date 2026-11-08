package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive yoyodyne-ifd.428.111: a run the sweep put back at its promotion
// to bring its queued head up to date is hosted by the sweep's own process, so
// when the product restarts onto a new build that process is ended with the
// old build and the run is left part-way through its gate with nothing
// recorded about the stop. The sweep on the new build carries it on from the
// step it had reached, rather than leaving it until the claim audit gives its
// item back as abandoned.

// lostHostAt is a queued head the sweep put back at its promotion, whose
// hosting process then went on into the gate and ended there: the record as
// that process last wrote it, at phase, naming a worker on an older build that
// no longer runs, and with no process holding the run's lease.
func lostHostAt(t *testing.T, phase runstate.Phase) (queuedFixture, *orchestratortest.CheckedForge, Outcome) {
	t.Helper()
	fixture, forge, outcome := queuedOnProtectedTarget(t)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.Reading = publish.CheckReading{
		Files:    []string{"feature.txt"},
		Failing:  []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}},
		Passing:  3,
		BehindBy: 1,
	}
	// The sweep before the restart withdraws the merge and puts the run back
	// at its promotion; it hosts nothing here, because its process is the one
	// the restart ends.
	before := fixture.sweep(t, forge, true)
	before.Continue = nil
	results, err := before.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionUpdating {
		t.Fatalf("Reconcile() = %#v, %v; want the queued head put back at its promotion", results, err)
	}
	putBack, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatingQueuedHead(putBack) {
		t.Fatalf("record = status %q, phase %q; want a live run at its promotion", putBack.Status, putBack.Phase)
	}
	// The worker is the store's to write, so the record keeps the one that
	// last held the run; which build it names is describeLostHost's, tested
	// below.
	died := putBack
	died.Phase = phase
	died.UpdatedAt = time.Now()
	if err := fixture.store.Save(died); err != nil {
		t.Fatal(err)
	}
	if updatingQueuedHead(died) || !updateLostItsHost(died) {
		t.Fatalf("updatingQueuedHead = %t, updateLostItsHost = %t; want the run read as an update that lost its host", updatingQueuedHead(died), updateLostItsHost(died))
	}
	return fixture, forge, outcome
}

func TestAnUpdateWhoseHostEndedForANewBuildIsCarriedOnByTheNewBuild(t *testing.T) {
	t.Parallel()
	for _, phase := range []runstate.Phase{runstate.PhaseChecking, runstate.PhaseReviewing} {
		t.Run(string(phase), func(t *testing.T) {
			t.Parallel()
			fixture, forge, outcome := lostHostAt(t, phase)
			after := fixture.sweep(t, forge, true)

			// The new build's sweep reads the run as owed its update, never as an
			// interrupted run to abandon.
			results, err := after.Reconcile(context.Background())
			if err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			if len(results) != 1 || results[0].Action != ActionResumable || !strings.Contains(results[0].Detail, "carries the update on") {
				t.Fatalf("reconciliation = %#v, want the run left for the update's continuation", results)
			}
			if record := fixture.tracker.Record(); record.Blocked || record.Closed {
				t.Fatalf("blocked = %t, closed = %t; a lost host hands nothing back and closes nothing", record.Blocked, record.Closed)
			}

			updates, err := after.ContinueUpdates(context.Background())
			if err != nil {
				t.Fatalf("ContinueUpdates() error = %v", err)
			}
			if len(updates) != 1 || !updates[0].Continued || updates[0].Failure != "" || updates[0].Outcome == nil {
				t.Fatalf("updates = %#v, want the run carried on by the new build", updates)
			}
			if !strings.Contains(updates[0].Detail, "the process hosting it ended") {
				t.Errorf("detail = %q, want the ending of the process hosting the update named", updates[0].Detail)
			}
			carried := *updates[0].Outcome
			if carried.PullRequest == nil || !carried.PullRequest.MergeQueued || len(forge.MergeRequests()) != 2 {
				t.Fatalf("outcome pull request = %#v, merges = %d; want the change's merge queued again", carried.PullRequest, len(forge.MergeRequests()))
			}
			if carried.RepairAttempts != 0 {
				t.Errorf("repair attempts = %d, want none charged for the lost host", carried.RepairAttempts)
			}
			settled, err := fixture.store.Load(pipelineRunID)
			if err != nil {
				t.Fatal(err)
			}
			if !settled.Outstanding() || settled.Integration == nil || settled.PullRequest == nil || !settled.PullRequest.MergeQueued {
				t.Fatalf("record = status %q, phase %q; want a finished run waiting on its merge queued again", settled.Status, settled.Phase)
			}
			if settled.RepairAttempts != 0 || settled.StopClass != "" {
				t.Errorf("repair attempts = %d, stop class = %q; want nothing charged and nothing stopped", settled.RepairAttempts, settled.StopClass)
			}
			if settled.BaseCommit == outcome.BaseCommit {
				t.Errorf("base commit = %s, want the change brought up to date onto the moved target", settled.BaseCommit)
			}
			if settled.ReviewHeadCommit != settled.Integration.SourceCommit {
				t.Errorf("review head = %s, promoted = %s; want the verdict to be about the change as brought up to date", settled.ReviewHeadCommit, settled.Integration.SourceCommit)
			}
			notes := fixture.tracker.Record().Notes
			if !strings.Contains(notes, "carrying on bringing this run's change up to date") || !strings.Contains(notes, "no repair attempt is charged") {
				t.Errorf("the item was not told the update was carried on after its host ended:\n%s", notes)
			}
		})
	}
}

// A run whose update lost its host but which carries a park or stop of its own
// is left to whatever that says continues it, and nothing but an update in the
// gate is read as one.
func TestAnUpdateThatLostItsHostIsLeftToItsOwnPark(t *testing.T) {
	t.Parallel()
	base := runstate.State{
		Status:                 runstate.StatusRunning,
		Phase:                  runstate.PhaseChecking,
		WorktreePath:           "/w",
		Branch:                 "b",
		BaseCommit:             "c",
		TargetBranch:           "main",
		ProviderSessionID:      "s",
		IntegrationResumptions: []runstate.IntegrationResumption{{Cause: runstate.CauseQueuedHeadBehind}},
	}
	if !updateLostItsHost(base) {
		t.Fatal("an update at its checks with nothing on its record was not read as one that lost its host")
	}
	for name, change := range map[string]func(*runstate.State){
		"redeploy stop":   func(s *runstate.State) { s.RedeployStop = &runstate.RedeployStop{Phase: runstate.PhaseChecking} },
		"provider stop":   func(s *runstate.State) { s.ProviderStop = "silent" },
		"other cause":     func(s *runstate.State) { s.IntegrationResumptions[0].Cause = runstate.CauseProcessVanished },
		"landed":          func(s *runstate.State) { s.Integration = &runstate.Integration{} },
		"at promotion":    func(s *runstate.State) { s.Phase = runstate.PhaseIntegrating },
		"fresh developer": func(s *runstate.State) { s.Phase = runstate.PhaseDeveloping },
		"no session":      func(s *runstate.State) { s.ProviderSessionID = "" },
		"finished":        func(s *runstate.State) { s.Status = runstate.StatusFailed },
	} {
		state := base
		state.IntegrationResumptions = append([]runstate.IntegrationResumption(nil), base.IntegrationResumptions...)
		change(&state)
		if updateLostItsHost(state) {
			t.Errorf("%s: read as an update that lost its host", name)
		}
	}
}

// The restart is named as the cause where the worker the run recorded ran
// another build than the one carrying it on, and the ending is named without a
// cause where that cannot be told.
func TestALostHostNamesTheRestartWhereTheBuildMoved(t *testing.T) {
	t.Parallel()
	moved := runstate.State{Worker: &runstate.WorkerIdentity{PID: 1, Build: buildinfo.Commit() + "-older", RunID: "run", Attempt: 1}}
	if said := describeLostHost(moved); !strings.Contains(said, "ended when the product restarted onto a new build") || !strings.Contains(said, moved.Worker.Build) {
		t.Errorf("describeLostHost() = %q, want the restart onto a new build named with the build the run ran", said)
	}
	for name, state := range map[string]runstate.State{
		"same build": {Worker: &runstate.WorkerIdentity{PID: 1, Build: buildinfo.Commit(), RunID: "run", Attempt: 1}},
		"no build":   {Worker: &runstate.WorkerIdentity{PID: 1, RunID: "run", Attempt: 1}},
		"no worker":  {},
	} {
		if said := describeLostHost(state); strings.Contains(said, "ended when the product restarted") || !strings.Contains(said, "either for a restart onto a new build or at its own time limit") {
			t.Errorf("%s: describeLostHost() = %q, want the ending named without claiming a cause", name, said)
		}
	}
}

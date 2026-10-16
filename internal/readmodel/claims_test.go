package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// auditNoon is the moment every reading below is taken at.
var auditNoon = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func claimed(id, title string) Claim { return Claim{WorkItemID: id, Title: title} }

// inFlight is a run whose record says it is still going and which last wrote to
// itself `ago` before auditNoon. It is what every killed process leaves behind.
func inFlight(runID, workItemID string, ago time.Duration) runstate.State {
	moment := auditNoon.Add(-ago)
	return runstate.State{
		RunID:      runID,
		WorkItemID: workItemID,
		Status:     runstate.StatusRunning,
		Phase:      runstate.PhaseDeveloping,
		StartedAt:  moment.Add(-time.Hour),
		UpdatedAt:  moment,
	}
}

// ended is a run that reached a terminal status `ago` before auditNoon without giving
// its claim back, which is what an in-process failure leaves behind.
func ended(runID, workItemID string, status runstate.Status, ago time.Duration) runstate.State {
	moment := auditNoon.Add(-ago)
	return runstate.State{
		RunID:       runID,
		WorkItemID:  workItemID,
		Status:      status,
		Phase:       runstate.PhaseDeveloping,
		StartedAt:   moment.Add(-time.Hour),
		UpdatedAt:   moment,
		CompletedAt: &moment,
	}
}

// The four nights this exists for, each as the records actually held it. Every
// one of them is a work item the tracker called in progress with no process
// anywhere working on it, and every existing derivation reads all four as a
// machine that is busy.
func TestTheFourNightsAreAllDeadClaims(t *testing.T) {
	t.Parallel()

	nights := map[string]struct {
		claim Claim
		runs  []runstate.State
	}{
		// yoyodyne-ifd.211, killed by the backend and left claimed for two days.
		"a backend kill two days ago": {
			claim: claimed("yoyodyne-ifd.211", "The two-day one"),
			runs:  []runstate.State{inFlight("run-211", "yoyodyne-ifd.211", 48*time.Hour)},
		},
		// yoyodyne-ifd.209.6, which died in its bootstrap twice: the run was
		// reserved and the item claimed, and then the process was gone before it
		// wrote anything else.
		"a bootstrap death that never got past its reservation": {
			claim: claimed("yoyodyne-ifd.209.6", "The one that died starting"),
			runs: []runstate.State{{
				RunID:      "run-209-6",
				WorkItemID: "yoyodyne-ifd.209.6",
				Status:     runstate.StatusPending,
				StartedAt:  auditNoon.Add(-6 * time.Hour),
				UpdatedAt:  auditNoon.Add(-6 * time.Hour),
			}},
		},
		// yoyodyne-ifd.209.7, one of the two that went inside an hour on the last
		// night, killed on the network.
		"a network kill last night": {
			claim: claimed("yoyodyne-ifd.209.7", "The first of the pair"),
			runs:  []runstate.State{inFlight("run-209-7", "yoyodyne-ifd.209.7", 9*time.Hour)},
		},
		// yoyodyne-ifd.264, the other of the pair, whose run recorded its own
		// ending and left the claim standing anyway.
		"a run that ended and did not give the claim back": {
			claim: claimed("yoyodyne-ifd.264", "The second of the pair"),
			runs:  []runstate.State{ended("run-264", "yoyodyne-ifd.264", runstate.StatusFailed, 9*time.Hour)},
		},
	}
	for name, night := range nights {
		dead := DeadClaims([]Claim{night.claim}, night.runs, auditNoon, 0, 0, nil)
		if len(dead) != 1 {
			t.Fatalf("%s: DeadClaims() = %+v, want the claim read as dead", name, dead)
		}
		if dead[0].WorkItemID != night.claim.WorkItemID {
			t.Fatalf("%s: dead claim names %q, want %q", name, dead[0].WorkItemID, night.claim.WorkItemID)
		}
		if dead[0].Since.IsZero() {
			t.Fatalf("%s: dead claim carries no moment to measure from", name)
		}
		if strings.TrimSpace(dead[0].Because) == "" {
			t.Fatalf("%s: dead claim says nothing about what became of the run", name)
		}
		if dead[0].RunID == "" {
			t.Fatalf("%s: dead claim names no run, so nobody can go and look at it", name)
		}
	}
}

// A run that is genuinely working keeps its claim, whatever else is true. This
// is the reading that must not be wrong in this direction: releasing an item a
// developer is halfway through would put a second one on it.
func TestAClaimWithALiveRunBehindItIsLeftAlone(t *testing.T) {
	t.Parallel()

	live := []runstate.State{inFlight("run-live", "yoyodyne-ifd.9", time.Minute)}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Working")}, live, auditNoon, 0, 0, nil); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want a live run to keep its claim", dead)
	}
	// And an item whose newest run is alive keeps it even though an earlier
	// attempt at the same item ended hours ago.
	together := []runstate.State{
		ended("run-first", "yoyodyne-ifd.9", runstate.StatusFailed, 9*time.Hour),
		inFlight("run-second", "yoyodyne-ifd.9", time.Minute),
	}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Working")}, together, auditNoon, 0, 0, nil); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want a retried item with a live run to keep its claim", dead)
	}
}

// A run that stopped short and is owed a continuation keeps its claim however
// long it has been quiet. Each of these returns and lets its process exit, so its
// record goes as still as a killed one's — and its item is claimed on purpose,
// with the worktree and the developer session that continuation needs. Giving one
// of these back would put a second run on work that is waiting to be picked up.
func TestARunWaitingToBeContinuedKeepsItsClaimHoweverQuietItIs(t *testing.T) {
	t.Parallel()

	waited := auditNoon.Add(-30 * time.Hour)
	held := auditNoon.Add(-30 * time.Hour)
	for name, park := range map[string]func(*runstate.State){
		"waiting out a provider usage limit": func(s *runstate.State) { s.UsageLimitResetsAt = &waited },
		"waiting out a named refusal":        func(s *runstate.State) { s.PauseCause = "overloaded" },
		"stopped on a provider that stalled": func(s *runstate.State) { s.ProviderStop = runstate.ProviderStopStalled },
		"held up by a directive": func(s *runstate.State) {
			s.DirectivePause = &runstate.DirectivePause{DirectiveID: "directive-1", Unresolved: "which artifact?"}
		},
		"waiting on work it depends on": func(s *runstate.State) {
			s.DependencyPause = &runstate.DependencyPause{}
		},
		"parked by the operator's hold": func(s *runstate.State) { s.OperatorHeldSince = &held },
	} {
		run := inFlight("run-parked", "yoyodyne-ifd.9", 30*time.Hour)
		park(&run)
		dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Parked")}, []runstate.State{run}, auditNoon, 0, 0, nil)
		if len(dead) != 0 {
			t.Fatalf("%s: DeadClaims() = %+v, want a run owed a continuation to keep its claim", name, dead)
		}
	}
}

// The threshold, which is what separates a dead claim from a run that ended a
// moment ago with the tracker not yet caught up.
func TestAClaimIsNotDeadUntilItHasBeenQuietPastTheThreshold(t *testing.T) {
	t.Parallel()

	runs := []runstate.State{ended("run-fresh", "yoyodyne-ifd.9", runstate.StatusFailed, time.Minute)}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Just ended")}, runs, auditNoon, 0, 0, nil); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want a run that ended a minute ago to be left alone", dead)
	}
	past := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Just ended")}, runs, auditNoon, 30*time.Second, 0, nil)
	if len(past) != 1 {
		t.Fatalf("DeadClaims() = %+v, want the same claim dead once the threshold is shorter than the silence", past)
	}
}

// A killed run that had already been round a repair is a dead claim like any
// other. The count of rounds it took is history rather than a wait: it never goes
// back down, so reading it as a continuation being owed would exempt every
// long-running item from every audit for the rest of the product's life — which
// is the shape of the item that sat claimed for two days.
func TestARunKilledAfterARepairRoundIsStillADeadClaim(t *testing.T) {
	t.Parallel()

	repaired := inFlight("run-repaired", "yoyodyne-ifd.264", 9*time.Hour)
	repaired.RepairAttempts = 2
	repaired.Phase = runstate.PhaseReviewing
	dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.264", "Round once already")}, []runstate.State{repaired}, auditNoon, 0, 0, nil)
	if len(dead) != 1 {
		t.Fatalf("DeadClaims() = %+v, want a killed run read as dead however many repair rounds it took", dead)
	}
}

// An item whose change already landed is not work to be started again. The run
// promoted it and then stopped somewhere after the promotion, so what the item
// wants is closing rather than developing a second time — and giving the claim
// back would buy the same change twice.
func TestAClaimOverWorkThatAlreadyLandedIsNotGivenBack(t *testing.T) {
	t.Parallel()

	promoted := ended("run-landed", "yoyodyne-ifd.9", runstate.StatusFailed, 9*time.Hour)
	promoted.Integration = &runstate.Integration{TargetBranch: "main", TargetCommit: "abc1234"}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Landed")}, []runstate.State{promoted}, auditNoon, 0, 0, nil); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want an item whose change landed left for reconciliation to close", dead)
	}
}

// A run that handed its approved change to the merge queue ended succeeded
// with its item still claimed, and the claim is what keeps the change from
// being started again while it waits to land; once the queue has landed it,
// the run's own integration says so as for any landing.
func TestAClaimOverAChangeWaitingInTheMergeQueueIsNotGivenBack(t *testing.T) {
	t.Parallel()

	queued := ended("run-queued", "yoyodyne-ifd.9", runstate.StatusSucceeded, 9*time.Hour)
	queued.MergeQueue = &runstate.MergeQueueAdmissionRecord{
		EntryID: "mqe-" + strings.Repeat("a", 32), Order: 1, Repository: "yoyodyne", TargetBranch: "main",
		Mode: runstate.MergeQueueHarness, ApprovedHead: strings.Repeat("b", 40), AdmittedAt: auditNoon.Add(-9 * time.Hour),
	}
	if !queued.AwaitingMergeQueue() {
		t.Fatal("a succeeded run admitted to the queue and not landed does not read as waiting in it")
	}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Queued")}, []runstate.State{queued}, auditNoon, 0, 0, nil); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want an item whose change waits in the merge queue to keep its claim", dead)
	}
	plain := ended("run-plain", "yoyodyne-ifd.9", runstate.StatusSucceeded, 9*time.Hour)
	if plain.AwaitingMergeQueue() {
		t.Fatal("a run never admitted to a queue reads as waiting in one")
	}
}

// The 2026-09-22 shape, at the grain this reading works at. run-b0b6d18d's
// change was approved and then stopped short of the target branch by a tracker
// read that timed out, leaving a record that ends `failed` with the stop on it
// and the change on its branch. The claim is the one thing saying the item is
// spoken for: given back, the next pull started a fresh run that re-derived the
// same change, spent a developer run and a review, and integrated it a second
// time.
func TestAClaimOverAnApprovedChangeTheEnvironmentStoppedIsNotGivenBack(t *testing.T) {
	t.Parallel()

	stopped := ended("run-b0b6d18d", "yoyodyne-ifd.436.4", runstate.StatusFailed, 9*time.Hour)
	stopped.Phase = runstate.PhaseReviewing
	stopped.Branch = "yoyodyne/yoyodyne-ifd-436-4/b0b6d18d"
	stopped.WorktreePath = "/state/worktrees/yoyodyne-ifd-436-4-b0b6d18d"
	stopped.Failure = "bd show failed with status timed_out and exit code -1: "
	stopped.ReviewDecision = runstate.ReviewApprove
	stopped.ReviewSessionID = "f4c1a0de-review"
	stopped.IntegrationStop = &runstate.IntegrationStop{
		Cause:      runstate.CauseTransportFailure,
		Detail:     stopped.Failure,
		Phase:      runstate.PhaseReviewing,
		RecordedAt: auditNoon.Add(-9 * time.Hour),
	}

	dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.436.4", "The duplicated one")}, []runstate.State{stopped}, auditNoon, 0, 0, nil)
	if len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want an approved change the environment stopped left for `yoyo triage resume`", dead)
	}
	// And a stop whose branch is gone is nothing anybody can resume, so the claim
	// is the dead one it looks like: there is no change left to start over.
	stopped.BranchRemoved, stopped.WorktreeRemoved = true, true
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.436.4", "Swept")}, []runstate.State{stopped}, auditNoon, 0, 0, nil); len(dead) != 1 {
		t.Fatalf("DeadClaims() = %+v, want the claim given back once nothing is left to resume", dead)
	}
}

// The pull's hold and the claim audit are two readers of one run, and an
// integration stop whose change is gone is where they once came apart: the audit
// gave the claim back because nothing survives to resume, while the hold answered
// every integration stop as resumable and named `yoyo triage resume` — a verb
// that refuses once the branch is gone — as what finishes it. So the run is
// driven through both over the same look, which finds neither the branch nor the
// worktree, and both have to give the same answer and neither may send anybody
// to a resume.
func TestAnIntegrationStopWhoseChangeIsGoneIsReadAlikeByTheHoldAndTheClaimAudit(t *testing.T) {
	t.Parallel()

	stopped := ended("run-5e1f0a77", "yoyodyne-ifd.428.10", runstate.StatusFailed, 9*time.Hour)
	stopped.Phase = runstate.PhaseReviewing
	stopped.Branch = "yoyodyne/yoyodyne-ifd-428-10/5e1f0a77"
	stopped.WorktreePath = "/state/worktrees/yoyodyne-ifd-428-10-5e1f0a77"
	stopped.Failure = "bd show failed with status timed_out and exit code -1: "
	stopped.ReviewDecision = runstate.ReviewApprove
	stopped.ReviewSessionID = "f4c1a0de-review"
	stopped.IntegrationStop = &runstate.IntegrationStop{
		Cause:      runstate.CauseTransportFailure,
		Detail:     stopped.Failure,
		Phase:      runstate.PhaseReviewing,
		RecordedAt: auditNoon.Add(-9 * time.Hour),
	}
	stopped.BranchRemoved, stopped.WorktreeRemoved = true, true
	gone := &remainsOf{survives: map[string]gitworktree.Survival{}}
	look := Looking(context.Background(), gone, func() time.Time { return auditNoon })
	runs := []runstate.State{stopped}

	// Nothing decided about it: nothing holds it and nothing keeps its claim.
	held := heldForAPerson(runs, nil, nothingDecided, look)
	reason, holding := held.Reason(stopped.WorkItemID)
	dead := DeadClaims([]Claim{claimed(stopped.WorkItemID, "Stopped and swept")}, runs, auditNoon, 0, 0, look)
	if released := len(dead) == 1; holding == released {
		t.Fatalf("the hold says held = %t (%q) and the claim audit says released = %t: one run, two answers", holding, reason, released)
	}
	if holding {
		t.Fatalf("the item is held for %q, want nothing holding a stop that has nothing left to resume", reason)
	}

	// A re-run decided about it: the hold is the carry-out, the harness's move and
	// one it can make without the branch, and it still names no resume.
	rerun := decisions(map[string]runstate.TriageCounters{stopped.WorkItemID: {
		Decisions: []runstate.TriageDecision{{Decision: runstate.TriageDecisionRerun, RunID: stopped.RunID}},
	}})
	decided := heldForAPerson(runs, nil, rerun, look)
	reason = heldReason(t, decided, stopped.WorkItemID)
	if !decided.Decided(stopped.WorkItemID) || !strings.Contains(reason, "carrying that decision out") ||
		!strings.Contains(reason, "a re-run is the way on") {
		t.Fatalf("the hold says %q, want the re-run named and its carry-out named as what is outstanding", reason)
	}
	for _, said := range []string{reason, because(dead)} {
		if strings.Contains(said, "triage resume") {
			t.Fatalf("%q names a resume, which refuses once the branch is gone", said)
		}
	}
}

// The wider rule under it, which the stop above is one case of: a run that ended
// holding its change keeps its item's claim, whether what says so is a blocker
// the harness handed somebody — a replay that conflicted against a moved target
// — or a death inside the run's own process, which hands over nothing at all,
// or a check stage its bound stopped. Each leaves a change on a branch that a fresh run would start over the top of.
func TestAClaimOverAChangeStillOnItsBranchIsNotGivenBack(t *testing.T) {
	t.Parallel()

	for name, ending := range map[string]func(*runstate.State){
		"a replay that conflicted": func(s *runstate.State) {
			s.Blocker = "Yoyodyne stopped this item: replaying the change onto main conflicted, and both sides are preserved."
		},
		"a death inside the run's own process": func(s *runstate.State) {
			s.Failure = "the provider ended this run without judging the work after 2 of 2 permitted relaunch(es)"
		},
		// The harness continues this one at its checks once the load allows, and a
		// claim given back here is a fresh run that redoes the development.
		"a check stage its bound stopped": func(s *runstate.State) {
			s.Status, s.Phase = runstate.StatusTimedOut, runstate.PhaseChecking
			s.CheckStage = &runstate.CheckStage{StartedAt: auditNoon.Add(-10 * time.Hour), BoundSeconds: 1800, Command: "make race", StoppedAtBound: true}
		},
	} {
		run := ended("run-holding", "yoyodyne-ifd.9", runstate.StatusFailed, 9*time.Hour)
		run.Branch = "yoyodyne/yoyodyne-ifd-9/holding"
		run.WorktreePath = "/state/worktrees/yoyodyne-ifd-9-holding"
		ending(&run)
		if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Still on a branch")}, []runstate.State{run}, auditNoon, 0, 0, nil); len(dead) != 0 {
			t.Fatalf("%s: DeadClaims() = %+v, want the claim kept over a change still on its branch", name, dead)
		}
		// And the same ending with its change swept keeps nothing: there is no
		// change left to start over, so the claim is the dead one it looks like.
		run.BranchRemoved, run.WorktreeRemoved = true, true
		if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Swept")}, []runstate.State{run}, auditNoon, 0, 0, nil); len(dead) != 1 {
			t.Fatalf("%s: DeadClaims() = %+v, want the claim given back once nothing of the change survives", name, dead)
		}
	}
}

// And the case the rule above must not swallow, which is the one the whole audit
// exists for: a killed process leaves a record still saying it is in flight, and
// the branch and worktree it was halfway through are exactly as present as a
// stoppage's. Nothing ended it and nothing owes it a move, so the claim is dead
// and the record has to be settled before anything pulls the item again.
func TestAKilledRunHoldingAHalfFinishedChangeIsStillADeadClaim(t *testing.T) {
	t.Parallel()

	killed := inFlight("run-killed", "yoyodyne-ifd.209.7", 9*time.Hour)
	killed.Branch = "yoyodyne/yoyodyne-ifd-209-7/killed"
	killed.WorktreePath = "/state/worktrees/yoyodyne-ifd-209-7-killed"
	dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.209.7", "Killed mid-change")}, []runstate.State{killed}, auditNoon, 0, 0, nil)
	if len(dead) != 1 {
		t.Fatalf("DeadClaims() = %+v, want a killed process read as dead however much of its change is on disk", dead)
	}
}

// A claim the harness never made is not the harness's to give back. The harness
// reserves a run before it claims anything, so a claim with no run behind it is
// a person's — and taking work back off somebody is not this reading's to do.
func TestAClaimWithNoRunBehindItIsNobodysToGiveBack(t *testing.T) {
	t.Parallel()

	other := []runstate.State{ended("run-elsewhere", "yoyodyne-ifd.8", runstate.StatusSucceeded, 9*time.Hour)}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Somebody's own")}, other, auditNoon, 0, 0, nil); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want a claim with no recorded run left alone", dead)
	}
}

// The two histories are said apart, because a reader acts on the difference: a
// run that ended without giving its claim back is a hole in the pipeline, and a
// run still recorded as in flight is a process something killed.
func TestWhatBecameOfTheRunTellsTheTwoDeathsApart(t *testing.T) {
	t.Parallel()

	over := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Ended")},
		[]runstate.State{ended("run-over", "yoyodyne-ifd.9", runstate.StatusFailed, 9*time.Hour)}, auditNoon, 0, 0, nil)
	if len(over) != 1 || !strings.Contains(over[0].Because, "ended") {
		t.Fatalf("Because = %q, want a run that ended said as one", because(over))
	}
	killed := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Killed")},
		[]runstate.State{inFlight("run-killed", "yoyodyne-ifd.9", 9*time.Hour)}, auditNoon, 0, 0, nil)
	if len(killed) != 1 || !strings.Contains(killed[0].Because, "the process holding it is gone") {
		t.Fatalf("Because = %q, want a killed process said as one", because(killed))
	}
	// Neither clause hands anybody a chore: the audit settles the run and gives the
	// item back itself, so a message that named a command would be telling somebody
	// to do what has already been done.
	for _, dead := range append(over, killed...) {
		if strings.Contains(dead.Because, "yoyo reconcile") {
			t.Fatalf("Because = %q, want no command in a clause about work already put right", dead.Because)
		}
	}
}

func because(dead []DeadClaim) string {
	if len(dead) == 0 {
		return ""
	}
	return dead[0].Because
}

// A run the reconciling sweep put back at its promotion to bring a queued head
// up to date is owed that continuation however still its record has gone, so
// its claim is not one nothing is working on; once it is promoted again, or
// ended, it is no longer that wait.
func TestAQueuedHeadPutBackAtItsPromotionAwaitsContinuation(t *testing.T) {
	t.Parallel()
	updating := runstate.State{
		Status: runstate.StatusRunning,
		Phase:  runstate.PhaseIntegrating,
		IntegrationResumptions: []runstate.IntegrationResumption{
			{Cause: runstate.CauseQueuedHeadBehind, Reason: "brought up to date", ResumedAt: time.Now()},
		},
	}
	if !AwaitingContinuation(updating) {
		t.Fatal("a queued head put back at its promotion was not read as awaiting its continuation")
	}
	promoted := updating
	promoted.Integration = &runstate.Integration{TargetBranch: "main"}
	ended := updating
	ended.Status = runstate.StatusSucceeded
	other := updating
	other.IntegrationResumptions = []runstate.IntegrationResumption{{Cause: runstate.CauseDirtyPrimary, Reason: "resumed", ResumedAt: time.Now()}}
	for name, run := range map[string]runstate.State{"promoted again": promoted, "ended": ended, "resumed for another cause": other} {
		if AwaitingContinuation(run) {
			t.Errorf("%s: read as awaiting the queued-head continuation", name)
		}
	}
}

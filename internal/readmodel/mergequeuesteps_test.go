package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var stepMoment = time.Date(2026, 11, 9, 16, 23, 0, 0, time.UTC)

// stepGeneration is a candidate built at built with the three configured
// checks, nothing yet run on it.
func stepGeneration(entry runstate.MergeQueueEntry, built time.Time) runstate.MergeQueueGeneration {
	generation := runstate.MergeQueueGeneration{
		Number: 1, EntryID: entry.EntryID, EntryOrder: entry.Order, TargetBranch: "main",
		TargetBase: strings.Repeat("d", 40), Heads: []string{entry.ApprovedHead}, Candidate: strings.Repeat("e", 40),
		Content: strings.Repeat("f", 40), Checks: runstate.NewMergeQueueCheckConfiguration([]string{"make fmtcheck", "make test", "make race"}),
		AuthorSession: "developer-session", CreatedAt: built,
	}
	generation.Paths = &runstate.MergeQueuePathEvidence{Binding: generation.Binding(), CheckedAt: built, Changed: 1}
	return generation
}

func passed(command string) runstate.MergeQueueCheckResult {
	return runstate.MergeQueueCheckResult{Command: command, Passed: true, Status: "succeeded"}
}

// readOne reads a queue holding the one entry and its records, and returns the
// entry as the read model shows it.
func readQueueOf(t *testing.T, entry runstate.MergeQueueEntry, generations []runstate.MergeQueueGeneration, landing *runstate.MergeQueueLanding, reading *runstate.MergeQueueForgeReading) QueueEntryStanding {
	t.Helper()
	queues := fixtureQueues{
		keys:        []runstate.MergeQueueKey{{Repository: "yoyodyne", TargetBranch: "main"}},
		entries:     map[string][]runstate.MergeQueueEntry{"main": {entry}},
		generations: map[string][]runstate.MergeQueueGeneration{entry.EntryID: generations},
		landings:    map[string]runstate.MergeQueueLanding{},
		readings:    map[string]runstate.MergeQueueForgeReading{},
	}
	if landing != nil {
		queues.landings[entry.EntryID] = *landing
	}
	if reading != nil {
		queues.readings[entry.EntryID] = *reading
	}
	read, problem := ReadMergeQueues(queues)
	if problem != "" || len(read) != 1 || len(read[0].Entries) != 1 {
		t.Fatalf("ReadMergeQueues() = %#v, %q", read, problem)
	}
	return read[0].Entries[0]
}

func assertStep(t *testing.T, name string, got QueueEntryStanding, step string, since time.Time) {
	t.Helper()
	if !strings.Contains(got.Step, step) {
		t.Errorf("%s: step = %q, want it to say %q", name, got.Step, step)
	}
	if got.StepSince == nil || !got.StepSince.Equal(since) {
		t.Errorf("%s: step since = %v, want %s", name, got.StepSince, since)
	}
	if says := got.StepSays(); !strings.Contains(says, localMoment(since)) {
		t.Errorf("%s: step said as %q, want the local time %s", name, says, localMoment(since))
	}
}

// An entry is shown at each step the queue takes it through — waiting for its
// candidate, starting its checks, running each named check, waiting for and
// having its review, waiting to land, landing, waiting on GitHub, recording
// its landing — each with the moment its records say that step began.
func TestEachQueueStepIsShownWithTheMomentItBegan(t *testing.T) {
	t.Parallel()
	entry := fixtureEntry(1, "yoyodyne-ifd.428.126.6.5", "Stop the local race check", runstate.MergeQueueHarness)
	entry.AdmittedAt = stepMoment.Add(-6 * time.Hour)
	built := stepMoment
	assertStep(t, "admitted", readQueueOf(t, entry, nil, nil, nil), "waiting for its candidate to be built", entry.AdmittedAt)

	generation := stepGeneration(entry, built)
	assertStep(t, "built", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil), "starting its checks", built)

	binding := generation.Binding()
	testStarted, raceStarted := built.Add(time.Minute), built.Add(28*time.Minute)
	generation.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: binding, StartedAt: built, Results: []runstate.MergeQueueCheckResult{passed("make fmtcheck")}}
	generation.Launches = []runstate.MergeQueueLaunch{{Stage: runstate.MergeQueueStageChecks, Command: "make test", Host: "here", PID: 10, ProcessGroup: 10, StartedAt: testStarted}}
	assertStep(t, "second check", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil), "running make test, check 2 of 3", testStarted)

	generation.CheckRun.Results = append(generation.CheckRun.Results, passed("make test"))
	generation.Launches = append(generation.Launches, runstate.MergeQueueLaunch{Stage: runstate.MergeQueueStageChecks, Command: "make race", Host: "here", PID: 11, ProcessGroup: 11, StartedAt: raceStarted})
	assertStep(t, "third check", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil), "running make race, check 3 of 3", raceStarted)

	checked := built.Add(time.Hour)
	generation.CheckRun.Results = append(generation.CheckRun.Results, passed("make race"))
	generation.CheckRun.FinishedAt = &checked
	assertStep(t, "checked", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil), "waiting for its independent review to start", checked)

	reviewStarted := checked.Add(10 * time.Second)
	generation.Review = &runstate.MergeQueueReviewEvidence{Binding: binding, StartedAt: reviewStarted}
	assertStep(t, "reviewing", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil), "under independent review", reviewStarted)

	reviewed := reviewStarted.Add(30 * time.Second)
	generation.Review.FinishedAt, generation.Review.Decision, generation.Review.SessionID, generation.Review.Model = &reviewed, "approve", "reviewer", "opus"
	verified := readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil)
	if verified.State != QueueEntryVerified {
		t.Fatalf("reviewed entry is %q, want verified", verified.State)
	}
	assertStep(t, "verified", verified, "waiting to begin landing", reviewed)

	pushed := reviewed.Add(3 * time.Second)
	landing := runstate.NewMergeQueueLanding(entry)
	landing.Attempts = []runstate.MergeQueuePromotionAttempt{{
		Number: 1, Path: runstate.MergeQueueLandThroughPullRequest, Generation: 1, PullRequest: 1697, IntendedAt: reviewed,
		Mutations: []runstate.MergeQueueMutationRecord{{
			Mutation: runstate.MergeQueuePushCandidate, RequestedAt: reviewed,
			Settled: &runstate.MergeQueueSettlement{Result: runstate.MergeQueueMutationDone, At: pushed},
		}},
	}}
	assertStep(t, "landing", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, &landing, nil), "landing, attempt 1: pushing the candidate to the forge", pushed)

	queued := pushed.Add(10 * time.Second)
	landing.Attempts[0].Mutations = append(landing.Attempts[0].Mutations, runstate.MergeQueueMutationRecord{
		Mutation: runstate.MergeQueueRequestMerge, RequestedAt: pushed,
		Settled: &runstate.MergeQueueSettlement{Result: runstate.MergeQueueMutationQueued, At: queued},
	})
	held := readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, &landing, nil)
	if held.State != QueueEntryForgeHolds {
		t.Fatalf("queued merge is %q, want held by the forge", held.State)
	}
	assertStep(t, "GitHub holds", held, "waiting for GitHub to merge pull request 1697", queued)

	confirmed := queued.Add(time.Hour)
	landing.Attempts[0].Landed = &runstate.MergeQueueLanded{Commit: generation.Candidate, ConfirmedAt: confirmed}
	assertStep(t, "recording", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, &landing, nil), "recording its landing", confirmed)

	landing.Completion = &runstate.MergeQueueCompletion{Attempt: 1, Landed: generation.Candidate, QueueAt: confirmed, RunAt: &confirmed, WorkItemAt: &confirmed}
	if done := readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, &landing, nil); done.State != QueueEntryLanded || done.Step != "" || done.StepSays() != "" {
		t.Fatalf("landed entry = %q with step %q, want landed and no step", done.State, done.Step)
	}
}

// The head of the queue waiting for its candidate has waited since the entry
// ahead of it left, not since it was admitted; an entry admitted before the
// one the queue chose first is shown as passed over, with the rule that chose
// the other; and an entry handed back says so, with when.
func TestTheHeadWaitsSinceTheEntryAheadLeftAndAPassedOverEntrySaysWhy(t *testing.T) {
	t.Parallel()
	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	landed := fixtureEntry(1, "yoyodyne-ifd.78.4", "Landed", runstate.MergeQueueHarness)
	head := fixtureEntry(2, "yoyodyne-ifd.2", "Head", runstate.MergeQueueHarness)
	head.AdmittedAt = stepMoment.Add(-5 * time.Hour)
	completed := runstate.NewMergeQueueLanding(landed)
	completed.Completion = &runstate.MergeQueueCompletion{Generation: 1, Landed: strings.Repeat("9", 40), QueueAt: stepMoment, RunAt: ptr(stepMoment), WorkItemAt: ptr(stepMoment)}
	queues := fixtureQueues{
		keys:     []runstate.MergeQueueKey{key},
		entries:  map[string][]runstate.MergeQueueEntry{"main": {landed, head}},
		landings: map[string]runstate.MergeQueueLanding{landed.EntryID: completed},
	}
	read, _ := ReadMergeQueues(queues)
	if got := read[0].Entries[0]; got.EntryID != head.EntryID {
		t.Fatalf("first entry %s, want the head", got.EntryID)
	} else {
		assertStep(t, "head", got, "waiting for its candidate to be built", stepMoment)
	}

	// The queue chose the code entry, admitted third, ahead of a document.
	document := fixtureEntry(3, "run-49f3", "Publishing a design", runstate.MergeQueueHarness)
	code := fixtureEntry(4, "yoyodyne-ifd.4", "Ordinary code", runstate.MergeQueueHarness)
	built := stepMoment.Add(time.Minute)
	chosen := stepGeneration(code, built)
	chosen.Selection = &runstate.MergeQueueSelection{Rule: "code, by priority", Why: "the last change the queue landed was a document"}
	handed := fixtureEntry(5, "yoyodyne-ifd.5", "Handed back", runstate.MergeQueueHarness)
	handedBack := runstate.NewMergeQueueLanding(handed)
	handedAt := stepMoment.Add(-time.Hour)
	handedBack.Handback = &runstate.MergeQueueHandback{At: handedAt, Class: runstate.MergeQueueCandidateDefect, Continuation: runstate.MergeQueueContinueRepair, Mover: "developer", Reason: "make test failed", Generation: 1}
	queues = fixtureQueues{
		keys:        []runstate.MergeQueueKey{key},
		entries:     map[string][]runstate.MergeQueueEntry{"main": {document, code, handed}},
		generations: map[string][]runstate.MergeQueueGeneration{code.EntryID: {chosen}},
		landings:    map[string]runstate.MergeQueueLanding{handed.EntryID: handedBack},
		pass: &runstate.MergeQueuePass{At: built, Outcome: runstate.MergeQueuePassWaiting, Says: "checking",
			Order: &runstate.MergeQueueOrder{Entries: []string{code.EntryID, document.EntryID}, Rule: "code, by priority", Why: "the last change the queue landed was a document"}},
	}
	read, _ = ReadMergeQueues(queues)
	steps := map[string]QueueEntryStanding{}
	for _, entry := range read[0].Entries {
		steps[entry.WorkItemID] = entry
	}
	assertStep(t, "passed over", steps["run-49f3"], "passed over: the queue's rule (code, by priority) chose yoyodyne-ifd.4 first, because the last change the queue landed was a document", built)
	assertStep(t, "handed back", steps["yoyodyne-ifd.5"], "sent back to its run for repair", handedAt)
	rendered := Standing{MergeQueues: read}.RenderMergeQueues()
	if !strings.Contains(rendered, "now: passed over") || !strings.Contains(rendered, "now: starting its checks, since "+localMoment(built)) {
		t.Fatalf("rendered queue does not say each entry's step and since when:\n%s", rendered)
	}
}

// An entry that fails and is tried again says which try it is on and why the
// earlier one was set aside, with the time the new try began: a check run
// cancelled and started again, a check that failed waiting to be handed back,
// and a landing attempt set aside and made again.
func TestAnEntryThatFailsAndIsRetriedShowsItsNewTry(t *testing.T) {
	t.Parallel()
	entry := fixtureEntry(1, "yoyodyne-pxdz", "Refused re-runs", runstate.MergeQueueHarness)
	built := stepMoment
	generation := stepGeneration(entry, built)
	restarted := built.Add(40 * time.Minute)
	generation.Interruptions = []runstate.MergeQueueInterruption{{
		Stage: runstate.MergeQueueStageChecks, StartedAt: built, FoundAt: restarted,
		Problem: "make race was cancelled and judged nothing",
	}}
	assertStep(t, "check restart pending", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil), "starting its checks, try 2 after 1 set aside (the last because make race was cancelled and judged nothing)", restarted)
	generation.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: generation.Binding(), StartedAt: restarted}
	assertStep(t, "checks again", readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil), "running make fmtcheck, check 1 of 3, try 2 after 1 set aside", restarted)

	failedAt := restarted.Add(20 * time.Minute)
	generation.CheckRun.Results = []runstate.MergeQueueCheckResult{passed("make fmtcheck"), {Command: "make test", ExitCode: 1, Status: "failed"}, {Command: "make race", ExitCode: 0, Status: "skipped"}}
	generation.CheckRun.FinishedAt = &failedAt
	failed := readQueueOf(t, entry, []runstate.MergeQueueGeneration{generation}, nil, nil)
	if failed.State != QueueEntryFailed {
		t.Fatalf("failed candidate is %q", failed.State)
	}
	assertStep(t, "failed", failed, "waiting for the queue to hand it back after it did not pass", failedAt)

	// A verified candidate whose first landing attempt was set aside lands
	// again in a second attempt.
	verified := verifiedQueueGeneration(entry)
	setAside := stepMoment.Add(2 * time.Hour)
	landing := runstate.NewMergeQueueLanding(entry)
	landing.Attempts = []runstate.MergeQueuePromotionAttempt{
		{Number: 1, Path: runstate.MergeQueueLandThroughPullRequest, Generation: 1, IntendedAt: stepMoment, SetAside: &runstate.MergeQueueSetAside{At: setAside, Reason: "retried by the development manager"}},
		{Number: 2, Path: runstate.MergeQueueLandThroughPullRequest, Generation: 1, IntendedAt: setAside},
	}
	assertStep(t, "second landing attempt", readQueueOf(t, entry, []runstate.MergeQueueGeneration{verified}, &landing, nil), "landing, attempt 2", setAside)
}

// An entry the queue is working whose step has run past the bound is a
// problem on the attention line and in the health check's factory problems,
// the development manager's to move, saying what it waits on; one under the
// bound, and one waiting behind the head, are not; the project can set the
// bound; and a merge GitHub holds whose required check ended without passing,
// cancelled or failed, is a problem at once rather than a wait.
func TestAnEntryPastItsBoundIsAProblemForTheDevelopmentManager(t *testing.T) {
	t.Parallel()
	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	head := fixtureEntry(1, "yoyodyne-ifd.428.126.6.5", "Stop the local race check", runstate.MergeQueueHarness)
	behind := fixtureEntry(2, "yoyodyne-pxdz", "Refused re-runs", runstate.MergeQueueHarness)
	behind.AdmittedAt = stepMoment.Add(-8 * time.Hour)
	built := stepMoment
	generation := stepGeneration(head, built)
	generation.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: generation.Binding(), StartedAt: built}
	queues := fixtureQueues{
		keys:        []runstate.MergeQueueKey{key},
		entries:     map[string][]runstate.MergeQueueEntry{"main": {head, behind}},
		generations: map[string][]runstate.MergeQueueGeneration{head.EntryID: {generation}},
		landings:    map[string]runstate.MergeQueueLanding{},
		readings:    map[string]runstate.MergeQueueForgeReading{},
	}
	at := func(now time.Time, bound time.Duration) Standing {
		return ReadStanding(context.Background(), Sources{MergeQueues: queues, MergeQueueStepOverdueAfter: bound, Now: func() time.Time { return now }})
	}
	overdueOf := func(standing Standing) []Attention {
		var found []Attention
		for _, entry := range standing.FactoryProblems {
			if entry.Kind == AttentionMergeQueueOverdue {
				found = append(found, entry)
			}
		}
		return found
	}

	if found := overdueOf(at(built.Add(119*time.Minute), 0)); len(found) != 0 {
		t.Fatalf("under the default bound, problems = %+v, want none", found)
	}
	standing := at(built.Add(3*time.Hour), 0)
	found := overdueOf(standing)
	if len(found) != 1 || found[0].ID != head.EntryID || found[0].WorkItemID != head.WorkItemID {
		t.Fatalf("past the bound, problems = %+v, want the head alone, not the entry waiting behind it", found)
	}
	entry := found[0]
	if entry.Mover != ownership.MoverDevelopmentManager || entry.Ownership.Owner != ownership.MoverDevelopmentManager {
		t.Fatalf("mover = %q, want the development manager", entry.Mover)
	}
	for _, want := range []string{"merge queue entry 1 for main", "running make fmtcheck, check 1 of 3", localMoment(built), "past the 2 hours", "it is waiting on its step to finish"} {
		if !strings.Contains(entry.What(), want) {
			t.Errorf("what = %q, want it to say %q", entry.What(), want)
		}
	}
	if whose := entry.Whose(); !strings.Contains(whose, "yoyo queue retry "+head.EntryID) || !strings.Contains(whose, "yoyo queue hand-back "+head.EntryID) {
		t.Errorf("whose = %q, want both commands that move it", whose)
	}
	onTheLine := false
	for _, needed := range standing.NeedsHuman {
		onTheLine = onTheLine || (needed.Kind == AttentionMergeQueueOverdue && needed.ID == head.EntryID)
	}
	if !onTheLine {
		t.Fatalf("the overdue entry is not on the attention line")
	}
	stalled := false
	for _, problem := range standing.Problems {
		stalled = stalled || (problem.Kind == ProblemStalled && problem.Entry.Kind == AttentionMergeQueueOverdue)
	}
	if !stalled {
		t.Fatalf("problems = %+v, want the overdue entry as a stalled problem", standing.Problems)
	}
	if rendered := standing.RenderMergeQueues(); !strings.Contains(rendered, "PROBLEM, the development manager's to move: merge queue entry 1") {
		t.Fatalf("rendered queue does not mark the problem:\n%s", rendered)
	}

	// A project's own bound is the one applied.
	if found := overdueOf(at(built.Add(31*time.Minute), 30*time.Minute)); len(found) != 1 || !strings.Contains(found[0].What(), "past the 30 minutes") {
		t.Fatalf("past a 30-minute bound, problems = %+v, want the head", found)
	}

	// GitHub holding a merge whose required check was cancelled, with nothing
	// failed and nothing still running, is a problem at once.
	reviewed := verifiedQueueGeneration(head)
	queued := stepMoment.Add(time.Hour)
	landing := runstate.NewMergeQueueLanding(head)
	landing.Attempts = []runstate.MergeQueuePromotionAttempt{{
		Number: 1, Path: runstate.MergeQueueLandThroughPullRequest, Generation: 1, PullRequest: 1697, IntendedAt: queued,
		Mutations: []runstate.MergeQueueMutationRecord{{
			Mutation: runstate.MergeQueueRequestMerge, RequestedAt: queued,
			Settled: &runstate.MergeQueueSettlement{Result: runstate.MergeQueueMutationQueued, At: queued},
		}},
	}}
	queues.generations[head.EntryID] = []runstate.MergeQueueGeneration{reviewed}
	queues.landings[head.EntryID] = landing
	queues.readings[head.EntryID] = runstate.MergeQueueForgeReading{PullRequest: 1697, At: queued.Add(20 * time.Minute), WaitingOn: "required checks", FailedChecks: []string{"build"}}
	found = overdueOf(at(queued.Add(25*time.Minute), 0))
	if len(found) != 1 || found[0].QueueOverdue.BoundSeconds != 0 || !strings.Contains(found[0].What(), `GitHub's required check build ended without passing (failed, or cancelled with nothing failed)`) ||
		!strings.Contains(found[0].What(), "GitHub will not make this merge by itself") {
		t.Fatalf("a cancelled required check, problems = %+v, want the head at once", found)
	}
	// The same reading with the check running again is a wait under the bound.
	queues.readings[head.EntryID] = runstate.MergeQueueForgeReading{PullRequest: 1697, At: queued.Add(20 * time.Minute), WaitingOn: "required checks", FailedChecks: []string{"build"}, PendingChecks: []string{"build"}}
	if found := overdueOf(at(queued.Add(25*time.Minute), 0)); len(found) != 0 {
		t.Fatalf("a check running again, problems = %+v, want none", found)
	}
}

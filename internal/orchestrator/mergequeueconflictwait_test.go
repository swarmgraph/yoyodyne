package orchestrator

// A change handed back for a conflict waits for the entries ahead that touch its
// conflicting files, through a real run, a real queue and a real run store; then
// it is brought up to date from the target as it stands, or rejoins the queue
// where it merges again.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// returnedRun is a queued change that conflicts with the target, handed back.
type returnedRun struct {
	*queueRun
	t        *testing.T
	admitted Outcome
	entry    runstate.MergeQueueEntry
	promoter MergeQueuePromoter
	now      time.Time
}

// newReturnedRun queues a change and lands a conflicting one on the target.
func newReturnedRun(t *testing.T) *returnedRun {
	t.Helper()
	queued, _ := newCatchUpRun(t, resolveBothSides)
	r := &returnedRun{queueRun: queued, t: t, admitted: queued.run(t), now: time.Now()}
	conflictOnTarget(t, r.repository, "something else\n")
	r.promoter = r.catchUpDriver().Promoter
	entry, _, err := r.promoter.entry(r.admitted.MergeQueue.Key(), r.admitted.MergeQueue.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	r.entry = entry
	return r
}

// handback is the hand-back wired as the product wires it, with nothing in
// memory: each call is a process that has just started.
func (r *returnedRun) handback() MergeQueueRunHandback {
	worktrees := r.pipeline.Worktrees.(*gitworktree.Manager)
	return MergeQueueRunHandback{
		Store: r.runs, Tracker: r.tracker, CatchUp: r.runs, Capacity: 1, Checkouts: worktrees,
		Now:     func() time.Time { return r.now },
		Returns: &MergeQueueReturns{Pipeline: &r.pipeline, Queue: r.queue, Git: worktrees},
	}
}

// look finds the conflict and hands the change back, as a step of a pass does.
func (r *returnedRun) look() {
	r.t.Helper()
	r.promoter.Repair = r.handback()
	if _, err := r.promoter.handBackConflicts(context.Background(), r.entry.Key(), &MergeQueueMergeTests{}); err != nil {
		r.t.Fatal(err)
	}
}

// give tries the owed hand-back again, as the next look does.
func (r *returnedRun) give() error {
	r.t.Helper()
	landing, _, err := r.queue.Landing(r.entry.Key(), r.entry.EntryID)
	if err != nil || landing.Handback == nil {
		r.t.Fatalf("landing = %#v, %v; want it handed back", landing, err)
	}
	return r.handback().HandBack(context.Background(), r.entry, *landing.Handback)
}

func (r *returnedRun) state() runstate.State {
	r.t.Helper()
	state, err := r.runs.Load(r.admitted.RunID)
	if err != nil {
		r.t.Fatal(err)
	}
	return state
}

// admitBehindTarget queues another change, made on the target as it stands.
func (r *returnedRun) admitBehindTarget(branch, file, item string) runstate.MergeQueueEntry {
	r.t.Helper()
	base := gitLine(r.t, r.repository, "rev-parse", "main")
	runPipelineGit(r.t, r.repository, "switch", "-c", branch, "main")
	writeQueueFile(r.t, r.repository, file, "from "+branch+"\n")
	runPipelineGit(r.t, r.repository, "add", file)
	runPipelineGit(r.t, r.repository, "commit", "-m", "the change on "+branch)
	head := gitLine(r.t, r.repository, "rev-parse", "HEAD")
	runPipelineGit(r.t, r.repository, "switch", "main")
	runID, err := runstate.NewRunID()
	if err != nil {
		r.t.Fatal(err)
	}
	other := r.state()
	other.RunID, other.Branch, other.BaseCommit, other.HarnessCommit, other.MergeQueue = runID, branch, base, head, nil
	if err := r.runs.Create(other); err != nil {
		r.t.Fatal(err)
	}
	entry, _, err := r.queue.Admit(context.Background(), runstate.MergeQueueAdmission{
		Key: r.entry.Key(), WorkItemID: item, WorkItemTitle: "The change on " + branch, RunID: runID,
		ApprovedHead: head, IntegrationPolicy: "automatic", Mode: runstate.MergeQueueHarness, ModeEvidence: harnessModeEvidence(),
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return entry
}

// leaveQueue hands an entry back, as a failing candidate is.
func (r *returnedRun) leaveQueue(entry runstate.MergeQueueEntry) {
	r.t.Helper()
	// Both changes are on the fixture's one item, which that hand-back must not
	// block.
	r.promoter.Repair = &recordingRepair{}
	if _, err := r.promoter.HandBack(context.Background(), entry.Key(), entry.EntryID, "its candidate failed"); err != nil {
		r.t.Fatal(err)
	}
}

func (r *returnedRun) assertWaiting(err error, entries ...string) {
	r.t.Helper()
	var waiting MergeQueueCatchUpWaiting
	if err != nil && !errors.As(err, &waiting) {
		r.t.Fatalf("error = %v, want the change waiting", err)
	}
	state := r.state()
	wait, recorded := state.ConflictWaitFor(r.entry.EntryID)
	if state.Status != runstate.StatusSucceeded || state.Reconciliation != nil || state.HoldsDeveloperSlot() || !recorded || wait.EndedAt != nil {
		r.t.Fatalf("run = status %q, reconciliation %#v, wait %#v; want it waiting with no slot and no attempt", state.Status, state.Reconciliation, wait)
	}
	var named []string
	for _, ahead := range wait.Ahead {
		named = append(named, ahead.EntryID)
	}
	if strings.Join(named, " ") != strings.Join(entries, " ") {
		r.t.Fatalf("waits for %v, want %v", named, entries)
	}
}

func (r *returnedRun) assertAttempted(err error) {
	r.t.Helper()
	if state := r.state(); err != nil || !state.CatchingUpWithMergeQueueTarget() || state.Reconciliation == nil {
		r.t.Fatalf("HandBack() = %v, run status %q, reconciliation %#v; want its attempt started", err, state.Status, state.Reconciliation)
	}
}

func TestAReturnedChangeWaitsForAnEntryAheadThatTouchesItsConflictingFile(t *testing.T) {
	t.Parallel()

	r := newReturnedRun(t)
	touching := r.admitBehindTarget("touching", "feature.txt", "yoyodyne-task")
	r.admitBehindTarget("elsewhere", "elsewhere.txt", "yoyodyne-task")
	r.look()
	r.assertWaiting(nil, touching.EntryID)
	wait := r.state().ConflictWait
	landing, _, err := r.queue.Landing(r.entry.Key(), r.entry.EntryID)
	if err != nil || !wait.Until.Equal(landing.Handback.At.Add(2*time.Hour)) || strings.Join(wait.Ahead[0].Paths, " ") != "feature.txt" {
		t.Fatalf("wait = %#v (%v), want two hours from the hand-back, for feature.txt", wait, err)
	}
	for _, said := range []string{"The change on touching (yoyodyne-task)", "feature.txt", wait.Until.Local().Format("15:04 MST")} {
		if !strings.Contains(wait.Says(), said) {
			t.Fatalf("Says() = %q, want it to name %q", wait.Says(), said)
		}
	}
	// A process that has just started goes on from the record, and a look that
	// finds the same entries writes nothing.
	before := r.state().UpdatedAt
	r.now = r.now.Add(time.Hour)
	r.assertWaiting(r.give(), touching.EntryID)
	if !r.state().UpdatedAt.Equal(before) {
		t.Fatal("a look that found nothing new wrote the run again")
	}
	// The entry leaves the queue: the attempt starts.
	r.leaveQueue(touching)
	r.assertAttempted(r.give())
	if wait := r.state().ConflictWait; wait == nil || wait.EndedAt == nil || wait.Ended == "" {
		t.Fatalf("wait = %#v, want its end recorded", wait)
	}
}

func TestAReturnedChangeWaitsTwoHoursAtMost(t *testing.T) {
	t.Parallel()

	r := newReturnedRun(t)
	touching := r.admitBehindTarget("touching", "feature.txt", "yoyodyne-task")
	r.look()
	until := r.state().ConflictWait.Until
	r.now = until.Add(-time.Second)
	r.assertWaiting(r.give(), touching.EntryID)
	r.now = until
	r.assertAttempted(r.give())
}

func TestAReturnedChangeDoesNotWaitForAnEntryBehindItOrAnotherReturnedChange(t *testing.T) {
	t.Parallel()

	r := newReturnedRun(t)
	// Code whose priority cannot be read is taken after code whose priority can.
	r.tracker.Item.Priority = 1
	r.admitBehindTarget("behind", "feature.txt", "not a tracker id")
	returned := r.admitBehindTarget("returned", "feature.txt", "yoyodyne-task")
	r.leaveQueue(returned)
	// One the order takes first is waited for, so the two above are passed over
	// for where they stand.
	first := r.admitBehindTarget("first", "feature.txt", "yoyodyne-task")
	r.look()
	r.assertWaiting(nil, first.EntryID)
	// With that one gone too, what is left behind it holds nothing up.
	r.leaveQueue(first)
	r.assertAttempted(r.give())
}

func TestAReturnedChangeThatMergesAgainRejoinsWithItsApprovedHead(t *testing.T) {
	t.Parallel()

	r := newReturnedRun(t)
	touching := r.admitBehindTarget("touching", "feature.txt", "yoyodyne-task")
	r.look()
	r.assertWaiting(nil, touching.EntryID)
	r.leaveQueue(touching)
	runPipelineGit(t, r.repository, "rm", "feature.txt")
	runPipelineGit(t, r.repository, "commit", "-m", "the target's feature.txt removed")
	for range 2 {
		// Given again, as after a process that died before writing that it was.
		if err := r.give(); err != nil {
			t.Fatalf("HandBack() = %v", err)
		}
	}
	state := r.state()
	entry, standing, err := r.queue.Standing(r.entry.Key(), state.RunID)
	if err != nil || !standing || entry.EntryID == r.entry.EntryID || entry.ApprovedHead != r.entry.ApprovedHead {
		t.Fatalf("entry = %#v, %t, %v; want the same head in the queue again", entry, standing, err)
	}
	if state.MergeQueue.EntryID != entry.EntryID || state.Status != runstate.StatusSucceeded || state.Reconciliation != nil || state.RepairAttempts != 0 {
		t.Fatalf("run = %q in entry %q, reconciliation %#v; want it waiting in the new entry with no attempt used", state.Status, state.MergeQueue.EntryID, state.Reconciliation)
	}
	if len(state.ConflictHandbacks) != 1 || state.ConflictSpending != nil {
		t.Fatalf("hand-backs %#v, spending %#v; want the hand-back kept and nothing charged to it", state.ConflictHandbacks, state.ConflictSpending)
	}
	if notes := r.tracker.Record().Notes; strings.Count(notes, "is back in the queue as entry") != 1 {
		t.Fatalf("notes:\n%s\nwant the item told once", notes)
	}
	entries, err := r.queue.Entries(r.entry.Key())
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %d, %v; want the change admitted again once", len(entries), err)
	}
}

func TestAnUnreadableWaitEndsAndTheAttemptStarts(t *testing.T) {
	t.Parallel()

	r := newReturnedRun(t)
	touching := r.admitBehindTarget("touching", "feature.txt", "yoyodyne-task")
	r.look()
	r.assertWaiting(nil, touching.EntryID)
	if _, err := r.runs.Update(context.Background(), r.admitted.RunID, runstate.ConflictWaitRecorded{Wait: runstate.ConflictWait{Unreadable: "not a wait"}, At: r.now}); err != nil {
		t.Fatal(err)
	}
	r.assertAttempted(r.give())
	if wait := r.state().ConflictWait; wait.EndedAt == nil || !strings.Contains(wait.Ended, "could not be read") {
		t.Fatalf("wait = %#v, want it to say why it ended", wait)
	}
}

// While a candidate is verified, the watch on the target starts what is due.
func TestAWaitThatEndsWhileACandidateIsVerifiedStartsTheAttempt(t *testing.T) {
	t.Parallel()

	r := newReturnedRun(t)
	touching := r.admitBehindTarget("touching", "feature.txt", "yoyodyne-task")
	r.look()
	r.now = r.state().ConflictWait.Until
	r.promoter.Repair = r.handback()
	lease, held, err := r.queue.LeaseWorker(context.Background(), r.entry.Key())
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %t, %v", held, err)
	}
	defer lease.Release()
	found := r.promoter.handBackConflictsUnder(context.Background(), lease, r.entry.Key(), touching.EntryID, &MergeQueueMergeTests{})
	if found.handedBack != 1 || len(found.problems) != 0 {
		t.Fatalf("handBackConflictsUnder() = %#v, want the returned change given its attempt", found)
	}
	r.assertAttempted(nil)
}

func TestAReturnedChangeWithNoSlotFreeAsksForTheNextOne(t *testing.T) {
	t.Parallel()

	r := newReturnedRun(t)
	holder := occupySlot(t, r.runs, "yoyodyne-holder", 1)
	r.look()
	if owed, err := r.runs.ConflictSlotsAsked(r.now); err != nil || len(owed) != 1 || owed[0].RunID != r.admitted.RunID {
		t.Fatalf("ConflictSlotsAsked() = %d runs, %v; want the returned change", len(owed), err)
	}
	if owed, err := r.runs.ConflictSlotsAsked(r.now.Add(runstate.ConflictSlotAskHeld)); err != nil || len(owed) != 0 {
		t.Fatalf("ConflictSlotsAsked() = %d runs, %v; want a request nobody repeats to lapse", len(owed), err)
	}
	holder.Status = runstate.StatusCancelled
	completed := time.Now().UTC()
	holder.CompletedAt = &completed
	if err := r.runs.Save(holder); err != nil {
		t.Fatal(err)
	}
	r.assertAttempted(r.give())
	if owed, err := r.runs.ConflictSlotsAsked(r.now); err != nil || len(owed) != 0 {
		t.Fatalf("ConflictSlotsAsked() = %d runs, %v; want none once the attempt started", len(owed), err)
	}
}

// owedRuns is a run store with changes owed the next free slots.
type owedRuns struct {
	ScheduleRuns
	owed func() int
}

func (r owedRuns) ConflictSlotsAsked(time.Time) ([]runstate.State, error) {
	return make([]runstate.State, r.owed()), nil
}

func TestAFreeSlotIsLeftForAReturnedChangeAheadOfFreshWork(t *testing.T) {
	t.Parallel()

	schedule := func(owed int) Schedule {
		harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
		harness.capacity = 2
		pass, err := Scheduler{Limit: 2, Open: func(ctx context.Context) (Pull, error) {
			pull, err := harness.open(ctx)
			pull.Runs = owedRuns{ScheduleRuns: pull.Runs, owed: func() int { return owed }}
			return pull, err
		}}.Schedule(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return pass
	}
	if pass := schedule(2); len(pass.Started) != 0 {
		t.Fatalf("started %d items, want both slots left for the returned changes", len(pass.Started))
	}
	if pass := schedule(0); len(pass.Started) != 2 {
		t.Fatalf("started %d items with nothing owed, want both", len(pass.Started))
	}
}

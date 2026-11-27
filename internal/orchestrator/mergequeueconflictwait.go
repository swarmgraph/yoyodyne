package orchestrator

// What comes before the attempt to bring a change up to date after the merge
// queue handed it back for a conflict: decision 1 of
// docs/designs/approved-changes-that-no-longer-merge.md.
//
// The attempt waits while an entry the queue would take before the returned
// change, and has not finished with, changes one of its conflicting files:
// bringing the change up to date before that entry lands would only conflict
// again. The returned change is placed in the order as it would be were it
// admitted now. An entry handed back has left the order, so two returned changes
// never wait for each other. Only what the queue's records show is waited for:
// an entry whose files cannot be read is not, and a queue that cannot be read
// ends the wait, because starting costs one attempt and loses no work.
//
// The wait is asked about each time the hand-back is tried again, which is every
// pass of the queue and, while a candidate is verified, every targetWatchEvery
// (handBackConflictsUnder). The run has ended and holds no developer slot
// meanwhile. Its record is written before what it decides
// (runstate.ConflictWait), and two hours from the hand-back end it.
//
// Once nothing is waited for, the approved head is merged onto the target as it
// then stands. Where it merges, the change is admitted to the queue again at
// that head, and no developer session starts and no attempt is used. Where it
// does not, the attempt is made, and a change that finds every developer slot
// taken asks for the next free one on its record, which a pull leaves for it
// ahead of fresh work (slotsOwedToReturned).

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// slotAskAgain is how old a recorded request for a slot grows before it is made
// again. It is well under runstate.ConflictSlotAskHeld, so a request made at
// every look never lapses.
const slotAskAgain = 2 * time.Minute

// MergeQueueReturnRecords is the queue a returned change is placed in and
// rejoins. It is satisfied by *runstate.MergeQueueStore.
type MergeQueueReturnRecords interface {
	runstate.MergeQueueAdmitter
	queueLandingRecords
}

// MergeQueueReturns is the queue as a returned change asks about it: which
// entries it waits for, whether it merges again, and how it rejoins.
type MergeQueueReturns struct {
	Pipeline *Pipeline
	Queue    MergeQueueReturnRecords
	Git      MergeQueueLander
	Forge    publish.QueueCapabilityReader
	Harness  queuemode.Harness
}

// Ahead is every entry the queue would take before the returned one that
// changes any of its conflicting files, in the order it would take them.
func (r MergeQueueReturns) Ahead(ctx context.Context, returned runstate.MergeQueueEntry, conflict runstate.MergeQueueConflict, now time.Time) ([]runstate.ConflictWaitEntry, error) {
	key := returned.Key()
	entries, err := r.Queue.Entries(key)
	if err != nil {
		return nil, err
	}
	conflicting := r.conflictingPaths(ctx, returned, conflict)
	var candidates []runstate.MergeQueueEntry
	generations := map[string][]runstate.MergeQueueGeneration{}
	underWay, unlanded := map[string]bool{}, map[string]bool{}
	for _, entry := range runstate.MergeQueueWorkOrder(entries) {
		landing, _, err := r.Queue.Landing(key, entry.EntryID)
		if err != nil || !landing.Waiting() {
			continue
		}
		attempt, attempted := landing.Current()
		if attempted && attempt.Landed != nil {
			continue
		}
		built, err := r.Queue.Generations(key, entry.EntryID)
		underWay[entry.EntryID] = attempted && attempt.SetAside == nil
		if err == nil && !underWay[entry.EntryID] && len(built) > 0 && built[len(built)-1].WaitsOnTarget() {
			// The queue passes it over until the target passes a check, so it is
			// not what the queue takes next.
			continue
		}
		unlanded[entry.EntryID] = true
		generations[entry.EntryID] = built
		candidates = append(candidates, entry)
	}
	touching := map[string]runstate.ConflictWaitEntry{}
	for _, entry := range candidates {
		if paths := r.touches(ctx, entry, conflicting); len(paths) > 0 {
			kept, _ := runstate.BoundConflictPaths(paths)
			touching[entry.EntryID] = runstate.ConflictWaitEntry{
				EntryID: entry.EntryID, Order: entry.Order, WorkItemID: entry.WorkItemID,
				Title: oneline.Fold(entry.WorkItemTitle, runstate.MaxConflictWaitTextBytes-8), Paths: kept,
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(touching) == 0 {
		return nil, nil
	}
	started := map[string]bool{}
	for _, entry := range candidates {
		started[entry.EntryID] = underWay[entry.EntryID] || entryInProgress(generations[entry.EntryID], unlanded)
	}
	placed := returned
	placed.AdmittedAt = now.UTC()
	order := queueOrder{pipeline: r.Pipeline, paths: r.Git.CandidatePaths, now: now.UTC(), lastLandedDocument: lastLandingWasDocument(r.Queue, key, ""), records: r.Queue}
	choice := order.choose(ctx, append(candidates, placed), started)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var ahead []runstate.ConflictWaitEntry
	for _, id := range choice.Order {
		if id == returned.EntryID {
			break
		}
		if entry, waited := touching[id]; waited {
			ahead = append(ahead, entry)
		}
	}
	return ahead, nil
}

// conflictingPaths is every conflicting file. The hand-back keeps a bounded
// number of them, so one that dropped some is merged again to name them all.
func (r MergeQueueReturns) conflictingPaths(ctx context.Context, returned runstate.MergeQueueEntry, conflict runstate.MergeQueueConflict) []string {
	if conflict.Omitted > 0 && conflict.TargetBase != "" {
		_, err := r.Git.MergeTree(ctx, conflict.TargetBase, returned.Head())
		var found *gitworktree.QueueCandidateConflict
		if errors.As(err, &found) && len(found.Paths) > 0 {
			return found.Paths
		}
	}
	return conflict.Paths
}

// touches is which of the conflicting files an entry's change makes a change
// to, and none where what it changes cannot be read.
func (r MergeQueueReturns) touches(ctx context.Context, entry runstate.MergeQueueEntry, conflicting []string) []string {
	run, err := r.Pipeline.Store.Load(entry.RunID)
	if err != nil || run.BaseCommit == "" {
		return nil
	}
	changed, err := r.Git.CandidatePaths(ctx, run.BaseCommit, entry.Head())
	if err != nil {
		return nil
	}
	var both []string
	for _, path := range conflicting {
		if slices.Contains(changed, path) {
			both = append(both, path)
		}
	}
	return both
}

// Merges reports whether the returned head merges onto the target as it stands.
func (r MergeQueueReturns) Merges(ctx context.Context, returned runstate.MergeQueueEntry) (bool, error) {
	target, err := r.Git.TargetCommit(ctx, returned.TargetBranch)
	if err != nil {
		return false, err
	}
	_, err = r.Git.MergeTree(ctx, target, returned.Head())
	var conflict *gitworktree.QueueCandidateConflict
	if errors.As(err, &conflict) {
		return false, nil
	}
	return err == nil, err
}

// Rejoined is the entry that took the returned one's place, where the run has
// been admitted again since.
func (r MergeQueueReturns) Rejoined(returned runstate.MergeQueueEntry) (runstate.MergeQueueEntry, bool, error) {
	entries, err := r.Queue.Entries(returned.Key())
	if err != nil {
		return runstate.MergeQueueEntry{}, false, err
	}
	for _, entry := range entries {
		if entry.Supersedes == returned.EntryID {
			return entry, true, nil
		}
	}
	return runstate.MergeQueueEntry{}, false, nil
}

// Rejoin admits the returned change again at the same approved head. What it
// answers says the entry, or why the queue will not take the change now.
func (r MergeQueueReturns) Rejoin(ctx context.Context, returned runstate.MergeQueueEntry, run runstate.State, now time.Time) (queuemode.Admitted, error) {
	admitter := queuemode.Admitter{Queue: r.Queue, Forge: r.Forge, Harness: r.Harness, Now: func() time.Time { return now }}
	// A resolution admitted on the approval of the head it resolves has the
	// harness's queue review its candidate, so it goes back to that queue only.
	if (run.ChecksPassed != nil && run.ChecksPassed.WholeLeftToQueue) || returned.ResolvedHead != "" {
		admitter.Only = runstate.MergeQueueHarness
	}
	return admitter.Admit(ctx, runstate.MergeQueueAdmission{
		Key: returned.Key(), WorkItemID: returned.WorkItemID, WorkItemTitle: returned.WorkItemTitle, RunID: returned.RunID,
		Publication: returned.Publication, ApprovedHead: returned.ApprovedHead, ResolvedHead: returned.ResolvedHead, Resolution: returned.Resolution,
		IntegrationPolicy: returned.IntegrationPolicy, At: now,
	})
}

// waitForAhead holds the hand-back while entries ahead touch its conflicting
// files. It returns the wait as recorded, or MergeQueueCatchUpWaiting while it
// lasts.
func (h MergeQueueRunHandback) waitForAhead(ctx context.Context, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback, state runstate.State) (runstate.ConflictWait, error) {
	now := h.now().UTC()
	wait, recorded := state.ConflictWaitFor(entry.EntryID)
	fresh := runstate.ConflictWait{EntryID: entry.EntryID, Since: handback.At.UTC(), Until: handback.At.UTC().Add(runstate.MaxConflictWait)}
	var why string
	switch {
	case recorded && wait.Unreadable != "":
		why = "the record of what it waited for could not be read: " + wait.Unreadable
		wait = fresh
	case recorded && wait.EndedAt != nil:
		return wait, nil
	case !recorded:
		wait = fresh
	}
	if why == "" && !now.Before(wait.Until) {
		why = "the two hours it waits at most had passed"
	}
	if why == "" {
		ahead, err := h.Returns.Ahead(ctx, entry, *handback.Conflict, now)
		switch {
		case ctx.Err() != nil:
			return wait, ctx.Err()
		case err != nil:
			why = "which entries are ahead of it could not be read: " + err.Error()
		case len(ahead) == 0:
			why = "no entry ahead of it touches its conflicting files"
		default:
			omitted := max(len(ahead)-runstate.MaxConflictWaitAhead, 0)
			ahead = ahead[:len(ahead)-omitted]
			if !recorded || omitted != wait.Omitted || !slices.EqualFunc(ahead, wait.Ahead, sameWaitedEntry) {
				wait.Ahead, wait.Omitted = ahead, omitted
				if err := h.recordWait(ctx, entry, wait); err != nil {
					return wait, err
				}
			}
			return wait, MergeQueueCatchUpWaiting{RunID: entry.RunID, TargetBranch: entry.TargetBranch, Why: wait.Says()}
		}
	}
	wait.EndedAt, wait.Ended = &now, oneline.Fold(why, runstate.MaxConflictWaitTextBytes-8)
	return wait, h.recordWait(ctx, entry, wait)
}

func sameWaitedEntry(a, b runstate.ConflictWaitEntry) bool {
	return a.EntryID == b.EntryID && slices.Equal(a.Paths, b.Paths)
}

func (h MergeQueueRunHandback) recordWait(ctx context.Context, entry runstate.MergeQueueEntry, wait runstate.ConflictWait) error {
	if _, err := h.CatchUp.Update(ctx, entry.RunID, runstate.ConflictWaitRecorded{Wait: wait, At: h.now()}); err != nil {
		return fmt.Errorf("record on run %s what its returned change waits for: %w", entry.RunID, err)
	}
	return nil
}

// askForSlot records that the returned change found every developer slot
// taken, so the next one to free is left for it. The record is an aid: where it
// cannot be written, the change still tries again at the next look.
func (h MergeQueueRunHandback) askForSlot(ctx context.Context, entry runstate.MergeQueueEntry, wait runstate.ConflictWait) {
	now := h.now().UTC()
	if wait.EndedAt == nil || (wait.SlotAskedAt != nil && now.Sub(*wait.SlotAskedAt) < slotAskAgain) {
		return
	}
	wait.SlotAskedAt = &now
	_ = h.recordWait(ctx, entry, wait)
}

// rejoin puts a returned change that merges again back in the queue, and
// reports whether it did. Its caller holds the run.
func (h MergeQueueRunHandback) rejoin(ctx context.Context, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback, run runstate.State, wait runstate.ConflictWait, marker string) (bool, error) {
	merges, err := h.Returns.Merges(ctx, entry)
	if err != nil {
		return false, fmt.Errorf("try whether the approved head of run %s merges onto %s again: %w", entry.RunID, entry.TargetBranch, err)
	}
	if !merges || handback.Head() != run.HarnessCommit || entry.Head() != run.HarnessCommit {
		return false, nil
	}
	admitted, err := h.Returns.Rejoin(ctx, entry, run, h.now().UTC())
	switch {
	case err != nil:
		return false, fmt.Errorf("admit the approved change of run %s to the merge queue again: %w", entry.RunID, err)
	case admitted.Hold != nil:
		return false, MergeQueueCatchUpWaiting{RunID: entry.RunID, TargetBranch: entry.TargetBranch,
			Why: "the change merges again, and the queue cannot take it now: " + strings.TrimSpace(admitted.Hold.Requirement)}
	case admitted.Elsewhere != "":
		return false, nil
	}
	return true, h.recordRejoined(ctx, entry, handback, admitted.Entry, wait, marker)
}

// recordRejoined writes the new entry on the run and tells the item, each once.
func (h MergeQueueRunHandback) recordRejoined(ctx context.Context, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback, rejoined runstate.MergeQueueEntry, wait runstate.ConflictWait, marker string) error {
	now := h.now().UTC()
	if wait.EntryID != entry.EntryID || wait.EndedAt == nil {
		wait = runstate.ConflictWait{EntryID: entry.EntryID, Since: handback.At.UTC(), Until: handback.At.UTC().Add(runstate.MaxConflictWait),
			EndedAt: &now, Ended: "the change is back in the queue"}
	}
	wait.Rejoined, wait.SlotAskedAt = rejoined.EntryID, nil
	state, err := h.CatchUp.Update(ctx, entry.RunID, runstate.RejoinedMergeQueue{
		From: entry.EntryID, Record: rejoined.AdmissionRecord(), Handback: queueConflictHandback(entry, *handback.Conflict, handback.At), Wait: wait, At: now,
	})
	if err != nil {
		return fmt.Errorf("record on run %s that its change is back in the merge queue: %w", entry.RunID, err)
	}
	if state.ConflictWait == nil || state.ConflictWait.Rejoined != rejoined.EntryID {
		// The run was admitted again through an attempt, or has moved on since;
		// nothing here is owed to its item.
		return nil
	}
	return h.noteCatchUp(ctx, entry, strings.Join([]string{
		fmt.Sprintf("The merge queue handed this change back because it conflicted with %s as entry %d (conflicting: %s). It merges onto %s again without a conflict, so it is back in the queue as entry %d with the same approved head. No developer session was started, and none of its attempts to bring it up to date was used.",
			entry.TargetBranch, entry.Order, nonEmpty(conflictPathsSay(handback.Conflict.Paths, handback.Conflict.Omitted), "no paths named"), entry.TargetBranch, rejoined.Order),
		marker,
	}, "\n"))
}

// slotsOwedToReturned is how many free developer slots a pull leaves for
// changes the merge queue handed back whose attempt is next to start. Runs that
// cannot answer, and an answer that cannot be read, leave none.
func slotsOwedToReturned(runs ScheduleRuns, now time.Time) int {
	asked, ok := runs.(interface {
		ConflictSlotsAsked(now time.Time) ([]runstate.State, error)
	})
	if !ok {
		return 0
	}
	owed, err := asked.ConflictSlotsAsked(now)
	if err != nil {
		return 0
	}
	return len(owed)
}

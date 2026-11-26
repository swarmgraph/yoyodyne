package orchestrator

// Taking up approved changes the merge queue held: the "GitHub's own merge
// queue" section of docs/designs/integration-through-a-merge-queue.md.
//
// With execution.merge_queue on, a change whose target branch requires the
// forge's own merge queue, or whose target's protection could not be read, is
// not admitted and not replayed the old way: its run stops at the promotion
// with its approval standing and the setting named (mergequeueadmission.go,
// CauseMergeQueueHeld). Nothing a person decides is needed to take it up again,
// only the condition clearing, so the harness looks for itself. A held change
// is taken up at the first reading that finds the protection readable and the
// forge's queue not required, and the changes one queue holds are taken up in
// the order they were held, which is the order they were approved: each is
// resumed at its promotion (IntegrationResumer), where it is admitted to the
// queue as any approved change is. A switch turned off while changes are held
// clears the condition too, and the resumed promotion then integrates the old
// way, replay, checks and review included, because that is what the switch
// says now.
//
// Clearing is also what ends the queue's one report of the condition: the
// queue's refusal record is removed, so a condition found again later is
// reported again, once. The harness never changes a repository setting.
//
// A change the queue refused because its own record is over the size limit
// the harness sets for it (CauseMergeQueueFull) is taken up the same way, in
// its own turn: it waits at its promotion with its approval and review
// standing, nothing tries it again while the record has no room, and it is
// resumed at the first look that finds room (RoomForAdmission) or the switch
// off. That limit is the harness's own, so the stop charged nothing and the
// hold on new work left it out.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// MergeQueueHeldRefusals is the queue's record of the condition that holds its
// changes. It is satisfied by *runstate.MergeQueueStore.
type MergeQueueHeldRefusals interface {
	ClearRefusal(ctx context.Context, key runstate.MergeQueueKey) error
}

// MergeQueueRoom says whether a queue's record has room for another
// admission. It is satisfied by *runstate.MergeQueueStore.
type MergeQueueRoom interface {
	RoomForAdmission(key runstate.MergeQueueKey) (bool, string, error)
}

// MergeQueueHeldRuns reads the run a held change was approved by. It is
// satisfied by *runstate.Store.
type MergeQueueHeldRuns interface {
	Load(runID string) (runstate.State, error)
}

// MergeQueueHeldChanges takes up the approved changes the merge queue held,
// once what held them has cleared.
type MergeQueueHeldChanges struct {
	// Docket is where a held change's stoppage is listed, as every integration
	// stop is.
	Docket   ResumeDocket
	Runs     MergeQueueHeldRuns
	Refusals MergeQueueHeldRefusals
	// Room is asked whether a queue whose record was full has room again; nil
	// leaves every change held for that waiting, because nothing can say the
	// refusal would not recur.
	Room MergeQueueRoom
	// Forge is asked again whether the target requires its own merge queue;
	// nil for a project with no forge that can say, whose targets nothing of
	// the forge's holds.
	Forge   publish.QueueCapabilityReader
	Harness queuemode.Harness
	// Enabled is execution.merge_queue as the configuration says now.
	Enabled bool
	// ProductID names the queue of a run that recorded no repository.
	ProductID string
	// Resume resumes one held run at its promotion, given what was found to
	// have cleared. It is satisfied by IntegrationResumer.Resume.
	Resume func(ctx context.Context, runID, cleared string) (IntegrationResumeResult, error)
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

func (h MergeQueueHeldChanges) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// MergeQueueHeldPass is what one look at the held changes found and did.
type MergeQueueHeldPass struct {
	// Held is how many held changes were found, and Resumed how many of them
	// were taken up.
	Held    int `json:"held"`
	Resumed int `json:"resumed"`
	// Says is the look's account, in ordinary words.
	Says string `json:"says,omitempty"`
}

type heldChange struct {
	entry triage.Entry
	run   runstate.State
}

// heldQueue is one queue's changes held for one cause, which clear together.
type heldQueue struct {
	key   runstate.MergeQueueKey
	cause runstate.EnvironmentalCause
}

// heldByQueue reports a cause whose changes this pass takes up.
func heldByQueue(cause runstate.EnvironmentalCause) bool {
	return cause == runstate.CauseMergeQueueHeld || cause == runstate.CauseMergeQueueFull
}

// Take looks at every change the merge queue holds and takes up those whose
// condition has cleared. It never returns an error: what stopped it is its
// account.
func (h MergeQueueHeldChanges) Take(ctx context.Context) MergeQueueHeldPass {
	var pass MergeQueueHeldPass
	if h.Docket == nil || h.Runs == nil || h.Resume == nil {
		return pass
	}
	entries, err := h.Docket.List()
	if err != nil {
		pass.Says = fmt.Sprintf("the changes the merge queue holds could not be listed: %v", err)
		return pass
	}
	queues := map[heldQueue][]heldChange{}
	var keys []heldQueue
	for _, entry := range entries {
		if entry.Class != triage.ClassStoppedRun || entry.IntegrationStop == nil || !heldByQueue(runstate.EnvironmentalCause(entry.IntegrationStop.Cause)) {
			continue
		}
		run, err := h.Runs.Load(entry.RunID)
		if err != nil || run.IntegrationStop == nil || string(run.IntegrationStop.Cause) != entry.IntegrationStop.Cause {
			// A run whose record no longer says it is held was taken up already,
			// or settled some other way, and its entry closes on its own terms.
			continue
		}
		key := heldQueue{
			key:   runstate.MergeQueueKey{Repository: nonEmpty(run.RepositoryID, h.ProductID), TargetBranch: run.TargetBranch},
			cause: run.IntegrationStop.Cause,
		}
		if _, seen := queues[key]; !seen {
			keys = append(keys, key)
		}
		queues[key] = append(queues[key], heldChange{entry: entry, run: run})
		pass.Held++
	}
	if pass.Held == 0 {
		return pass
	}
	var says []string
	for _, key := range keys {
		held := queues[key]
		// Held in the order they were approved, which is the order they were
		// held in; the run identifier breaks a tie, so two readings agree.
		sort.SliceStable(held, func(i, j int) bool {
			a, b := held[i].run.IntegrationStop.RecordedAt, held[j].run.IntegrationStop.RecordedAt
			if !a.Equal(b) {
				return a.Before(b)
			}
			return held[i].run.RunID < held[j].run.RunID
		})
		stands, why := h.stands(ctx, key)
		if stands {
			says = append(says, fmt.Sprintf("%d change(s) into %s stay held: %s", len(held), key.key.TargetBranch, why))
			continue
		}
		if key.cause == runstate.CauseMergeQueueHeld && h.Refusals != nil {
			_ = h.Refusals.ClearRefusal(context.WithoutCancel(ctx), key.key)
		}
		resumed, stopped := 0, ""
		var outside []string
		for _, change := range held {
			if ctx.Err() != nil {
				stopped = "the session working the queue stopped"
				break
			}
			result, err := h.Resume(ctx, change.run.RunID, why)
			switch {
			case result.Resumed:
				resumed++
			case result.IntakeHeld != nil:
				stopped = "intake is held, so nothing is taken up until it is released"
			case result.OutsideNamedEpics != nil:
				// About the one item, so the rest are still asked.
				outside = append(outside, result.OutsideNamedEpics.Error())
				continue
			case result.CapacityFull != nil:
				stopped = fmt.Sprintf("every developer slot is occupied (%d of %d), so the rest wait for one", result.CapacityFull.Active, result.CapacityFull.Limit)
			case err != nil:
				stopped = fmt.Sprintf("run %s could not be taken up: %v", change.run.RunID, err)
			default:
				stopped = fmt.Sprintf("run %s was not taken up", change.run.RunID)
			}
			if stopped != "" {
				break
			}
		}
		pass.Resumed += resumed
		account := fmt.Sprintf("%s: %s; took up %d of the %d change(s) held for it, in the order they were approved", key.key.TargetBranch, why, resumed, len(held))
		for _, why := range outside {
			account += "; " + why
		}
		if stopped != "" {
			account += "; " + stopped
		}
		says = append(says, account)
	}
	pass.Says = strings.Join(says, ". ")
	return pass
}

// stands reports whether what holds a queue's changes still stands, and says
// what was found either way.
func (h MergeQueueHeldChanges) stands(ctx context.Context, held heldQueue) (bool, string) {
	key := held.key
	if !h.Enabled {
		return false, "execution.merge_queue is off now, so each is integrated the way changes integrate without the queue"
	}
	if held.cause == runstate.CauseMergeQueueFull {
		return h.full(key)
	}
	if h.Forge == nil {
		return false, "nothing here can say the forge requires its own merge queue any more"
	}
	observed, err := h.Forge.QueueCapabilities(ctx, key.TargetBranch)
	if err != nil {
		return true, fmt.Sprintf("the protection of %s still could not be read: %v", key.TargetBranch, err)
	}
	selection, err := queuemode.Select(key.TargetBranch, observed, h.Harness, h.now())
	if err != nil {
		return true, err.Error()
	}
	if selection.Hold != nil {
		return true, strings.TrimSpace(selection.Hold.Requirement)
	}
	return false, fmt.Sprintf("the protection of %s is readable and no longer requires the forge's own merge queue", key.TargetBranch)
}

// full reports whether a queue whose record was over its size limit still has
// no room for another admission. Where nothing can say, the change stays held,
// so no retry is started that the same refusal would end.
func (h MergeQueueHeldChanges) full(key runstate.MergeQueueKey) (bool, string) {
	if h.Room == nil {
		return true, "nothing here can read whether the merge queue's record has room"
	}
	room, says, err := h.Room.RoomForAdmission(key)
	if err != nil {
		return true, fmt.Sprintf("whether the merge queue's record for %s has room could not be read: %v", key.TargetBranch, err)
	}
	if !room {
		return true, says + ", which leaves no room for another admission"
	}
	return false, says + ", which has room for another admission"
}
